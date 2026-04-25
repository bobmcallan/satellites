/*
 * SATELLITES ledger_view.js — Alpine factory for the ledger inspection
 * page (slice 11.3, story_a9f8be3c). Tailing toggle + N-new pill + URL
 * querystring filter sync + row expansion.
 *
 * Bootstrap input (set by project_ledger.html):
 *   window.SATELLITES_LEDGER = { projectID, apiURL }
 *
 * WS event kinds the factory cares about:
 *   - "ledger.created"   → push to pendingRows or auto-prepend (if tailing)
 *   - "ledger.status_changed" → if row already in `rows`, update Status
 *
 * Filter state mirrors `URLSearchParams` so reload + back-button keep the
 * view in sync with the URL.
 */
(function () {
    'use strict';

    function readURLFilters() {
        const sp = new URLSearchParams(window.location.search);
        const tags = (sp.get('tag') || '').split(',').map(function (s) { return s.trim(); }).filter(Boolean);
        return {
            query: sp.get('q') || '',
            type: sp.get('type') || '',
            durability: sp.get('durability') || '',
            source_type: sp.get('source_type') || '',
            status: sp.get('status') || '',
            story_id: sp.get('story_id') || '',
            contract_id: sp.get('contract_id') || '',
            tags: tags
        };
    }

    function writeURLFilters(filters, tagInput) {
        const sp = new URLSearchParams();
        if (filters.query) sp.set('q', filters.query);
        if (filters.type) sp.set('type', filters.type);
        if (filters.durability) sp.set('durability', filters.durability);
        if (filters.source_type) sp.set('source_type', filters.source_type);
        if (filters.status) sp.set('status', filters.status);
        if (filters.story_id) sp.set('story_id', filters.story_id);
        if (filters.contract_id) sp.set('contract_id', filters.contract_id);
        if (tagInput) sp.set('tag', tagInput);
        const qs = sp.toString();
        const url = window.location.pathname + (qs ? '?' + qs : '');
        window.history.replaceState(null, '', url);
    }

    window.ledgerView = function () {
        return {
            projectID: '',
            apiURL: '',
            rows: [],
            pendingRows: [],
            expanded: {},
            tailing: false,
            wsStatus: 'idle',
            filters: { query: '', type: '', tags: [], story_id: '', contract_id: '', durability: '', source_type: '', status: '' },
            tagInput: '',

            start() {
                const cfg = window.SATELLITES_LEDGER || {};
                this.projectID = cfg.projectID || '';
                this.apiURL = cfg.apiURL || '';
                const fromURL = readURLFilters();
                this.filters.query = fromURL.query;
                this.filters.type = fromURL.type;
                this.filters.durability = fromURL.durability;
                this.filters.source_type = fromURL.source_type;
                this.filters.status = fromURL.status;
                this.filters.story_id = fromURL.story_id;
                this.filters.contract_id = fromURL.contract_id;
                this.filters.tags = fromURL.tags;
                this.tagInput = fromURL.tags.join(',');
                this.hydrateFromSSR();
                this.attachWS();
                // Persist tailing across page loads.
                try {
                    const stored = window.localStorage.getItem('ledger.tailing');
                    if (stored === 'on') { this.tailing = true; }
                } catch (e) { /* ignore */ }
                this.$watch && this.$watch('tailing', function (v) {
                    try { window.localStorage.setItem('ledger.tailing', v ? 'on' : 'off'); } catch (e) {}
                });
            },

            hydrateFromSSR() {
                // The SSR markup includes <li data-testid="ledger-row-ssr-..."> rows.
                // We don't reuse them — Alpine renders fresh from `rows`. The
                // server already populated `rows` at SSR time, but for a JS-active
                // page we re-fetch via the JSON API to get the structured field.
                this.reload();
            },

            liveClass() { return 'live-dot-' + (this.wsStatus || 'idle'); },

            reloadTags() {
                this.filters.tags = (this.tagInput || '').split(',').map(function (s) { return s.trim(); }).filter(Boolean);
                this.reload();
            },

            async reload() {
                writeURLFilters(this.filters, this.tagInput);
                if (!this.apiURL) { return; }
                const sp = new URLSearchParams();
                if (this.filters.query) sp.set('q', this.filters.query);
                if (this.filters.type) sp.set('type', this.filters.type);
                if (this.filters.durability) sp.set('durability', this.filters.durability);
                if (this.filters.source_type) sp.set('source_type', this.filters.source_type);
                if (this.filters.status) sp.set('status', this.filters.status);
                if (this.filters.story_id) sp.set('story_id', this.filters.story_id);
                if (this.filters.contract_id) sp.set('contract_id', this.filters.contract_id);
                for (const t of this.filters.tags) { sp.append('tag', t); }
                try {
                    const r = await fetch(this.apiURL + (sp.toString() ? '?' + sp.toString() : ''), { credentials: 'same-origin' });
                    if (!r.ok) { return; }
                    const data = await r.json();
                    this.rows = data.rows || [];
                    this.pendingRows = [];
                } catch (e) { /* leave UI as-is */ }
            },

            toggleExpand(id) {
                this.expanded[id] = !this.expanded[id];
            },

            attachWS() {
                if (!window.SATELLITES_WS || !window.SATELLITES_WS.workspaceId) { return; }
                if (!window.SatellitesWS) { return; }
                const cfg = window.SATELLITES_WS;
                const self = this;
                this._ws = new window.SatellitesWS({
                    workspaceId: cfg.workspaceId,
                    debug: cfg.debug,
                    onStatusChange: function (next) { self.wsStatus = next; },
                    onEvent: function (ev) { self.applyEvent(ev); }
                });
                this._ws.connect();
            },

            applyEvent(ev) {
                if (!ev || !ev.Kind) { return; }
                const data = ev.Data || ev.data || {};
                const row = data.row || data;
                if (!row || !row.id) { return; }
                if (ev.Kind === 'ledger.created') {
                    if (!this._matchesFilters(row)) { return; }
                    const view = mapRowToView(row);
                    if (this.tailing) {
                        this.rows = prependRow(this.rows, view);
                    } else {
                        this.pendingRows = prependRow(this.pendingRows, view);
                    }
                } else if (ev.Kind === 'ledger.status_changed') {
                    for (let i = 0; i < this.rows.length; i++) {
                        if (this.rows[i].id === row.id) {
                            this.rows[i] = Object.assign({}, this.rows[i], { status: row.status || this.rows[i].status });
                            break;
                        }
                    }
                }
            },

            flushPending() {
                this.rows = this.pendingRows.concat(this.rows);
                this.pendingRows = [];
                window.scrollTo({ top: 0, behavior: 'smooth' });
            },

            _matchesFilters(row) {
                const f = this.filters;
                if (f.type && row.type !== f.type) return false;
                if (f.story_id && row.story_id !== f.story_id) return false;
                if (f.contract_id && row.contract_id !== f.contract_id) return false;
                if (f.durability && row.durability !== f.durability) return false;
                if (f.source_type && row.source_type !== f.source_type) return false;
                if (f.tags && f.tags.length > 0) {
                    const have = row.tags || row.Tags || [];
                    for (const t of f.tags) {
                        if (have.indexOf(t) < 0) return false;
                    }
                }
                return true;
            }
        };
    };

    function mapRowToView(row) {
        const tags = row.tags || row.Tags || [];
        return {
            id: row.id || '',
            type: row.type || row.Type || '',
            tags: tags,
            story_id: row.story_id || '',
            contract_id: row.contract_id || '',
            durability: row.durability || '',
            source_type: row.source_type || '',
            status: row.status || '',
            content: row.content || row.Content || '',
            created_at: row.created_at || row.CreatedAt || '',
            structured: row.structured ? (typeof row.structured === 'string' ? row.structured : JSON.stringify(row.structured)) : ''
        };
    }

    function prependRow(arr, row) {
        const out = [row];
        for (let i = 0; i < arr.length; i++) {
            if (arr[i].id !== row.id) { out.push(arr[i]); }
        }
        return out;
    }

    window.ledgerView.__test__ = { mapRowToView: mapRowToView, prependRow: prependRow, readURLFilters: readURLFilters };
})();
