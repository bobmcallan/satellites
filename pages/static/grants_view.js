/*
 * SATELLITES grants_view.js — Alpine factory for the live role-grants
 * panel (slice 6.7, story_5cc349a9). Subscribes to the workspace
 * websocket; receives `role_grant.created` / `role_grant.released`
 * events; updates the table without page refresh.
 */
(function () {
    'use strict';

    window.grantsView = function () {
        return {
            wsStatus: 'idle',
            rows: [],
            isAdmin: false,

            start() {
                const cfg = window.SATELLITES_GRANTS || {};
                this.isAdmin = !!cfg.isAdmin;
                this.hydrateFromSSR();
                this.attachWS();
            },

            hydrateFromSSR() {
                this.rows = [];
                document.querySelectorAll('[data-testid^="grant-row-ssr-"]').forEach(function (tr) {
                    const id = tr.getAttribute('data-testid').replace('grant-row-ssr-', '');
                    const cells = tr.querySelectorAll('td code');
                    if (cells.length < 5) { return; }
                    this.rows.push({
                        id: id,
                        role_id: '',
                        role_name: cells[1] ? cells[1].textContent.trim() : '',
                        agent_id: '',
                        agent_name: cells[2] ? cells[2].textContent.trim() : '',
                        grantee_kind: '',
                        grantee_id: cells[3] ? cells[3].textContent.trim() : '',
                        status: 'active',
                        issued_at: cells[4] ? cells[4].textContent.trim() : ''
                    });
                }.bind(this));
            },

            liveClass() { return 'live-dot-' + (this.wsStatus || 'idle'); },

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
                const row = data.row || data.grant || data;
                if (!row || !row.id) { return; }
                if (ev.Kind === 'role_grant.created') {
                    this.rows = upsertGrant(this.rows, normaliseGrant(row));
                } else if (ev.Kind === 'role_grant.released') {
                    this.rows = removeGrantByID(this.rows, row.id);
                }
            },

            async release(g) {
                const cfg = window.SATELLITES_GRANTS || {};
                if (!cfg.releaseURL || !this.isAdmin) { return; }
                if (!window.confirm('Revoke grant ' + g.id + '?')) { return; }
                try {
                    const r = await fetch(cfg.releaseURL + g.id + '/release', {
                        method: 'POST',
                        credentials: 'same-origin'
                    });
                    if (r.ok) {
                        this.rows = removeGrantByID(this.rows, g.id);
                    }
                } catch (e) { /* ignore */ }
            }
        };
    };

    function normaliseGrant(row) {
        return {
            id: row.id || '',
            role_id: row.role_id || '',
            role_name: row.role_name || '',
            agent_id: row.agent_id || '',
            agent_name: row.agent_name || '',
            grantee_kind: row.grantee_kind || '',
            grantee_id: row.grantee_id || '',
            status: row.status || 'active',
            issued_at: row.issued_at || ''
        };
    }

    function upsertGrant(arr, card) {
        const out = [card];
        for (let i = 0; i < arr.length; i++) {
            if (arr[i].id !== card.id) { out.push(arr[i]); }
        }
        return out;
    }

    function removeGrantByID(arr, id) {
        return arr.filter(function (c) { return c.id !== id; });
    }

    window.grantsView.__test__ = { upsertGrant: upsertGrant, removeGrantByID: removeGrantByID };
})();
