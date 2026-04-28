/*
 * SATELLITES repo_view.js — Alpine factory for the repo + index view
 * (slice 11.5, story_d4685302). Symbol search via the portal's
 * `/api/repos/{id}/symbols` endpoint (which wraps codeindex.SearchSymbols);
 * symbol drawer via `/api/repos/{id}/symbols/{symbol_id}` (which wraps
 * codeindex.GetSymbolSource).
 *
 * Migrated to Alpine.data registration (epic:portal-csp-strict,
 * story_384ef71e). Per-symbol strings (testid, location) are
 * precomputed via decorateSymbol; scope-level getters absorb the
 * inline boolean / comparison expressions previously in the template.
 */
(function () {
    'use strict';

    function repoView() {
        return {
            wsStatus: 'idle',
            symbolQuery: '',
            symbolKind: '',
            symbolLanguage: '',
            symbols: [],
            symbolError: '',
            drawer: { open: false, symbol: {}, source: '' },
            diff: { from: '', to: '', result: null, error: '' },
            reindexChip: 'idle',
            reindexError: '',

            init() {
                this.attachWS();
            },

            get liveClass() { return 'live-dot-' + (this.wsStatus || 'idle'); },
            get reindexChipClass() { return 'reindex-chip-' + (this.reindexChip || 'idle'); },
            get reindexChipLabel() {
                switch (this.reindexChip) {
                    case 'running': return 'reindexing…';
                    case 'failed': return 'reindex failed';
                    default: return 'idle';
                }
            },
            get reindexRunning() { return this.reindexChip === 'running'; },
            get symbolsEmpty() { return this.symbols.length === 0 && !this.symbolError; },
            get diffSourceUnavailable() {
                return this.diff.result && this.diff.result.diff_source === 'unavailable';
            },
            get diffCommits() {
                return (this.diff.result && this.diff.result.commits) || [];
            },

            // x-show workaround for @alpinejs/csp x-show reactivity bug
            // (story_739823eb): bind :class to a getter returning ''
            // or 'is-hidden' instead of relying on x-show.
            get hiddenWhenDrawerOpen() { return this.drawer.open ? '' : 'is-hidden'; },
            get hiddenWhenDiffSourceUnavailable() { return this.diffSourceUnavailable ? '' : 'is-hidden'; },

            async triggerReindex() {
                const cfg = window.SATELLITES_REPO || {};
                if (!cfg.repoID) { return; }
                this.reindexError = '';
                this.reindexChip = 'running';
                try {
                    const r = await fetch('/api/repos/' + cfg.repoID + '/reindex', {
                        method: 'POST',
                        credentials: 'same-origin'
                    });
                    if (r.status === 403) {
                        this.reindexChip = 'idle';
                        this.reindexError = 'admin required';
                        return;
                    }
                    if (!r.ok) {
                        this.reindexChip = 'failed';
                        this.reindexError = 'reindex enqueue failed (' + r.status + ')';
                        return;
                    }
                } catch (e) {
                    this.reindexChip = 'failed';
                    this.reindexError = 'reindex enqueue failed: ' + e;
                }
            },

            async loadDiff() {
                const cfg = window.SATELLITES_REPO || {};
                if (!cfg.repoID) { return; }
                this.diff.error = '';
                this.diff.result = null;
                const sp = new URLSearchParams();
                if (this.diff.from) sp.set('from', this.diff.from);
                if (this.diff.to) sp.set('to', this.diff.to);
                try {
                    const r = await fetch('/api/repos/' + cfg.repoID + '/diff?' + sp.toString(),
                        { credentials: 'same-origin' });
                    if (!r.ok) {
                        this.diff.error = 'diff failed (' + r.status + ')';
                        return;
                    }
                    this.diff.result = await r.json();
                } catch (e) {
                    this.diff.error = 'diff failed: ' + e;
                }
            },

            async searchSymbols() {
                const cfg = window.SATELLITES_REPO || {};
                if (!cfg.symbolsURL) { return; }
                const sp = new URLSearchParams();
                if (this.symbolQuery) sp.set('q', this.symbolQuery);
                if (this.symbolKind) sp.set('kind', this.symbolKind);
                if (this.symbolLanguage) sp.set('language', this.symbolLanguage);
                this.symbolError = '';
                try {
                    const r = await fetch(cfg.symbolsURL + (sp.toString() ? '?' + sp.toString() : ''),
                        { credentials: 'same-origin' });
                    if (!r.ok) {
                        this.symbolError = 'symbol search failed (' + r.status + ')';
                        this.symbols = [];
                        return;
                    }
                    const data = await r.json();
                    const list = (data && data.symbols) || [];
                    this.symbols = list.map(decorateSymbol);
                } catch (e) {
                    this.symbolError = 'symbol search failed: ' + e;
                    this.symbols = [];
                }
            },

            async openSymbol($event) {
                const target = $event && $event.currentTarget ? $event.currentTarget : null;
                const id = target && target.dataset ? target.dataset.symbolId : '';
                if (!id) { return; }
                let symbol = null;
                for (let i = 0; i < this.symbols.length; i++) {
                    if (this.symbols[i].id === id) { symbol = this.symbols[i]; break; }
                }
                if (!symbol) { return; }
                const cfg = window.SATELLITES_REPO || {};
                if (!cfg.sourceURL) { return; }
                this.drawer = { open: true, symbol: symbol, source: '(loading…)' };
                try {
                    const r = await fetch(cfg.sourceURL + id, { credentials: 'same-origin' });
                    if (!r.ok) {
                        this.drawer.source = 'load failed (' + r.status + ')';
                        return;
                    }
                    const data = await r.json();
                    this.drawer.source = (data && (data.source || data.body)) || JSON.stringify(data, null, 2);
                } catch (e) {
                    this.drawer.source = 'load failed: ' + e;
                }
            },

            closeDrawer() { this.drawer.open = false; },

            applyReindexEvent(ev) {
                if (!ev || !ev.kind) { return; }
                switch (ev.kind) {
                    case 'repo.reindex.start':
                        this.reindexChip = 'running';
                        break;
                    case 'repo.reindex.complete':
                        this.reindexChip = 'idle';
                        break;
                    case 'repo.reindex.failed':
                        this.reindexChip = 'failed';
                        break;
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
                    onStatusChange: function (next) { self.wsStatus = next; },
                    onEvent: function (ev) { self.applyReindexEvent(ev); }
                });
                this._ws.connect();
            }
        };
    }

    function decorateSymbol(s) {
        s.testid = 'symbol-row-' + (s.id || '');
        s.location = (s.file || '') + ':' + (s.start_line || '');
        return s;
    }

    document.addEventListener('alpine:init', function () {
        window.Alpine.data('repoView', repoView);
    });

    window.repoView = repoView;
})();
