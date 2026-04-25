/*
 * SATELLITES story_view.js — Alpine factory for the story view (slice 11.1,
 * story_3b450d9e). Subscribes to the workspace websocket via the global
 * `SatellitesWS` client and live-updates the five panels.
 *
 * Bootstrap input (set by story_detail.html):
 *   window.SATELLITES_STORY = { storyID, compositeURL }
 *
 * Server emits a small set of event kinds the panel cares about:
 *   - "ledger.created"        → append/update one ledger row, refilter
 *                                excerpts/verdicts/commits as needed.
 *   - "contract_instance.updated" / "ci.updated" → flip status on a CI
 *                                row, reorder if needed.
 *   - "story.updated"         → refresh the delivery strip (status flip).
 *
 * Any unrecognised event kind is ignored. On reconnect (live → reconnecting
 * → live) the factory refetches `/api/stories/{id}/composite` since
 * client-side replay would be more code than a single round-trip.
 */
(function () {
    'use strict';

    function matchesStory(payload, storyID) {
        if (!payload) { return false; }
        if (payload.story_id === storyID) { return true; }
        if (payload.StoryID === storyID) { return true; }
        if (payload.data && payload.data.story_id === storyID) { return true; }
        return false;
    }

    window.storyView = function () {
        return {
            storyID: '',
            compositeURL: '',
            composite: {
                story: {},
                source_documents: [],
                contract_instances: [],
                verdicts: [],
                commits: [],
                ledger_excerpts: [],
                delivery: { status: '' }
            },
            wsStatus: 'idle',

            start() {
                const cfg = window.SATELLITES_STORY || {};
                this.storyID = cfg.storyID || '';
                this.compositeURL = cfg.compositeURL || '';
                this.fetchComposite();
                this.attachWS();
            },

            liveClass() {
                return 'live-dot-' + (this.wsStatus || 'idle');
            },

            async fetchComposite() {
                if (!this.compositeURL) { return; }
                try {
                    const r = await fetch(this.compositeURL, { credentials: 'same-origin' });
                    if (!r.ok) { return; }
                    const data = await r.json();
                    if (data && data.story) {
                        this.composite = data;
                    }
                } catch (e) {
                    // Network error — leave SSR-rendered state alone; the
                    // websocket reconnect path will retry.
                }
            },

            attachWS() {
                if (!window.SATELLITES_WS || !window.SATELLITES_WS.workspaceId) { return; }
                if (!window.SatellitesWS) { return; }
                const cfg = window.SATELLITES_WS;
                const self = this;
                this._ws = new window.SatellitesWS({
                    workspaceId: cfg.workspaceId,
                    debug: cfg.debug,
                    onStatusChange: function (next) {
                        const prev = self.wsStatus;
                        self.wsStatus = next;
                        // On every fresh transition into 'live' (after the
                        // initial connect), refetch the composite — covers
                        // events missed during the reconnecting window.
                        if (prev !== 'live' && next === 'live' && prev !== 'idle' && prev !== 'connecting') {
                            self.fetchComposite();
                        }
                    },
                    onEvent: function (ev) { self.applyEvent(ev); }
                });
                this._ws.connect();
            },

            applyEvent(ev) {
                if (!ev || !ev.Kind) { return; }
                const data = ev.Data || ev.data || {};
                if (!matchesStory(data, this.storyID)) { return; }
                switch (ev.Kind) {
                    case 'ledger.created':
                        this.applyLedgerCreated(data);
                        break;
                    case 'contract_instance.updated':
                    case 'ci.updated':
                        this.applyCIUpdated(data);
                        break;
                    case 'story.updated':
                        this.applyStoryUpdated(data);
                        break;
                    default:
                        // Unknown event; ignore to keep the surface narrow.
                }
            },

            applyLedgerCreated(row) {
                if (!row || !row.id) {
                    // Server may nest the row inside data.row.
                    row = row && row.row ? row.row : row;
                }
                if (!row || !row.id) { return; }
                const tags = row.tags || row.Tags || [];
                const isVerdict = tags.indexOf('kind:verdict') >= 0;
                const isCommit = tags.indexOf('kind:commit') >= 0;
                const excerpt = {
                    id: row.id,
                    type: row.type || row.Type || '',
                    tags: tags,
                    content: row.content || row.Content || '',
                    created_at: row.created_at || row.CreatedAt || ''
                };
                this.composite.ledger_excerpts = prepend(this.composite.ledger_excerpts, excerpt, 50);
                if (isVerdict) {
                    let phase = '';
                    for (let i = 0; i < tags.length; i++) {
                        if (tags[i].indexOf('phase:') === 0) {
                            phase = tags[i].substring('phase:'.length);
                        }
                    }
                    const verdict = {
                        ledger_id: row.id,
                        contract_instance_id: row.contract_id || row.ContractID || '',
                        contract_name: phase,
                        verdict: (row.structured && row.structured.verdict) || '',
                        score: (row.structured && row.structured.score) || 0,
                        reasoning: (row.structured && row.structured.reasoning) || row.content || '',
                        created_at: excerpt.created_at
                    };
                    this.composite.verdicts = prepend(this.composite.verdicts, verdict, 100);
                    if (verdict.contract_name === 'story_close') {
                        this.composite.delivery = applyVerdictToStrip(this.composite.delivery, verdict);
                    }
                }
                if (isCommit) {
                    const commit = {
                        ledger_id: row.id,
                        sha: (row.structured && row.structured.sha) || '',
                        subject: (row.structured && row.structured.subject) || row.content || '',
                        author: (row.structured && row.structured.author) || '',
                        url: (row.structured && row.structured.url) || '',
                        created_at: excerpt.created_at
                    };
                    this.composite.commits = prepend(this.composite.commits, commit, 100);
                }
            },

            applyCIUpdated(row) {
                if (!row || !row.id) { return; }
                const cis = (this.composite.contract_instances || []).slice();
                let found = false;
                for (let i = 0; i < cis.length; i++) {
                    if (cis[i].id === row.id) {
                        cis[i] = mergeCI(cis[i], row);
                        found = true;
                        break;
                    }
                }
                if (!found) {
                    cis.push(mergeCI({}, row));
                    cis.sort(function (a, b) { return (a.sequence || 0) - (b.sequence || 0); });
                }
                this.composite.contract_instances = cis;
            },

            applyStoryUpdated(row) {
                if (!row || !row.id) {
                    row = row && row.row ? row.row : row;
                }
                if (!row || !row.id || row.id !== this.storyID) { return; }
                if (row.status) {
                    this.composite.delivery = Object.assign({}, this.composite.delivery, {
                        status: row.status,
                        updated_at: row.updated_at || row.UpdatedAt || this.composite.delivery.updated_at
                    });
                }
                if (row.title) { this.composite.story.Title = row.title; }
            }
        };
    };

    function prepend(arr, item, cap) {
        const out = [item];
        for (let i = 0; i < arr.length && out.length < cap; i++) {
            if (arr[i].id === item.id || arr[i].ledger_id === item.ledger_id) { continue; }
            out.push(arr[i]);
        }
        return out;
    }

    function mergeCI(prev, fresh) {
        return {
            id: fresh.id || prev.id || '',
            contract_name: fresh.contract_name || fresh.ContractName || prev.contract_name || '',
            sequence: fresh.sequence || fresh.Sequence || prev.sequence || 0,
            status: fresh.status || fresh.Status || prev.status || '',
            claimed_at: fresh.claimed_at || fresh.ClaimedAt || prev.claimed_at || '',
            closed_at: fresh.closed_at || fresh.ClosedAt || prev.closed_at || '',
            plan_ledger_id: fresh.plan_ledger_id || prev.plan_ledger_id || '',
            close_ledger_id: fresh.close_ledger_id || prev.close_ledger_id || ''
        };
    }

    function applyVerdictToStrip(strip, verdict) {
        let resolution = strip.resolution || '';
        switch (verdict.verdict) {
            case 'approved': resolution = 'delivered'; break;
            case 'rejected': resolution = 'failed'; break;
            case 'needs_changes':
            case 'amended': resolution = 'partially_delivered'; break;
        }
        return Object.assign({}, strip, {
            verdict: verdict.verdict,
            score: verdict.score,
            resolution: resolution,
            updated_at: verdict.created_at
        });
    }

    // Expose the helper for tests that grep the source.
    window.storyView.__test__ = { matchesStory: matchesStory, prepend: prepend, mergeCI: mergeCI, applyVerdictToStrip: applyVerdictToStrip };
})();
