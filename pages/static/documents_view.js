/*
 * SATELLITES documents_view.js — Alpine factory for the documents
 * browser page (slice 11.4, story_5bc06738). Type tabs (rendered as
 * server-side links) + client-side filter on already-loaded cards +
 * WS-driven reload on document.created / document.updated events.
 */
(function () {
    'use strict';

    function readURL() {
        const sp = new URLSearchParams(window.location.search);
        return {
            type: sp.get('type') || '',
            query: sp.get('q') || '',
            sort: sp.get('sort') || ''
        };
    }

    function writeURL(filters) {
        const sp = new URLSearchParams();
        if (filters.type) sp.set('type', filters.type);
        if (filters.query) sp.set('q', filters.query);
        if (filters.sort) sp.set('sort', filters.sort);
        const qs = sp.toString();
        const url = window.location.pathname + (qs ? '?' + qs : '');
        window.history.replaceState(null, '', url);
    }

    window.documentsView = function () {
        return {
            cards: [],
            wsStatus: 'idle',
            filters: { type: '', query: '', sort: '' },

            start() {
                const u = readURL();
                this.filters.type = u.type;
                this.filters.query = u.query;
                this.filters.sort = u.sort;
                this.hydrateFromSSR();
                this.attachWS();
            },

            hydrateFromSSR() {
                this.cards = [];
                document.querySelectorAll('[data-testid^="document-card-ssr-"]').forEach(function (li) {
                    const id = li.getAttribute('data-testid').replace('document-card-ssr-', '');
                    const linkText = li.querySelector('.document-name');
                    const typeEl = li.querySelector('.type-pill');
                    const scopeEl = li.querySelector('.scope-pill');
                    const versionEl = li.querySelector('.version-pill');
                    this.push({
                        id: id,
                        name: linkText ? linkText.textContent.trim() : '',
                        type: typeEl ? typeEl.textContent.trim() : '',
                        scope: scopeEl ? scopeEl.textContent.trim() : '',
                        version: versionEl ? parseInt(versionEl.textContent.replace('v', ''), 10) : 0,
                        tags: [],
                        updated_at: ''
                    });
                }.bind(this));
            },

            push(card) { this.cards.push(card); },

            liveClass() { return 'live-dot-' + (this.wsStatus || 'idle'); },

            async reload() {
                writeURL(this.filters);
                // Reload via the page itself (full SSR refresh) — simpler
                // than maintaining a JSON endpoint for the list page.
                window.location.search = (function () {
                    const sp = new URLSearchParams();
                    if (this.filters.type) sp.set('type', this.filters.type);
                    if (this.filters.query) sp.set('q', this.filters.query);
                    if (this.filters.sort) sp.set('sort', this.filters.sort);
                    return sp.toString();
                }.bind(this))();
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
                if (ev.Kind === 'document.created' || ev.Kind === 'document.updated' || ev.Kind === 'document.archived') {
                    // Soft-refresh on relevant events. We re-fetch via a
                    // page reload to keep the list authoritative.
                    if (typeof window !== 'undefined' && window.location) {
                        window.location.reload();
                    }
                }
            }
        };
    };
})();
