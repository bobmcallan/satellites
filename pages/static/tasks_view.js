/*
 * SATELLITES tasks_view.js — Alpine factory for the task queue page
 * (slice 11.2, story_f2d71c27). Subscribes to the workspace websocket
 * via the global SatellitesWS client and live-updates the three columns.
 *
 * Migrated to Alpine.data registration with CSP-compatible templates
 * (epic:portal-csp-strict, story_597a6b9c). Per-row class strings and
 * the testid attribute are precomputed in mergeTaskCard /
 * hydrateFromSSR so tasks.html can bind via bare property access on
 * the iteration variable. The drawer click handler reads its task id
 * from data-task-id via $event.currentTarget.dataset.taskId so the
 * template stays free of method-with-arg expressions.
 *
 * Bootstrap input (set by tasks.html):
 *   window.SATELLITES_TASKS = { drawerURL }
 *
 * WS event kinds the factory cares about:
 *   - "task.enqueued" / "task.created" → append to enqueued column
 *   - "task.claimed" / "task.in_flight" / "task.updated" → move to in_flight
 *   - "task.closed" → move to recently closed
 *   - any other kind ignored
 */
(function () {
    'use strict';

    function originMatches(filter, t) { return !filter || t.origin === filter; }
    function priorityMatches(filter, t) { return !filter || t.priority === filter; }

    function tasksView() {
        return {
            wsStatus: 'idle',
            inFlight: [],
            enqueued: [],
            closed: [],
            filter: { origin: '', priority: '' },
            drawer: {
                open: false,
                task: { id: '', origin: '', status: '', priority: '', claimed_by: '' },
                payload: '',
                excerpts: []
            },

            init() {
                this.hydrateFromSSR();
                this.attachWS();
            },

            // hydrateFromSSR pulls the SSR-rendered <noscript> rows into
            // the Alpine arrays so the JS-active path keeps the same
            // visible state. Each card's data-* attrs carry the
            // cards. We re-parse from DOM via querySelectorAll so the
            // template is the single source of truth.
            hydrateFromSSR() {
                const grab = function (selector) {
                    const out = [];
                    document.querySelectorAll(selector + ' li[data-testid^="task-card-"]').forEach(function (li) {
                        const id = li.getAttribute('data-testid').replace('task-card-', '');
                        const status = li.getAttribute('data-status') || '';
                        const idEl = li.querySelector('.task-id');
                        const originEl = li.querySelector('.origin-pill');
                        const priorityEl = li.querySelector('.priority-pill');
                        const outcomeEl = li.querySelector('.outcome-pill');
                        const storyEl = li.querySelector('.task-story-link');
                        const whenEl = li.querySelector('.task-when');
                        const workerEl = li.querySelector('.task-worker');
                        const card = {
                            id: idEl ? idEl.textContent.trim() : id,
                            origin: originEl ? originEl.textContent.trim() : '',
                            priority: priorityEl ? priorityEl.textContent.trim() : '',
                            status: status,
                            outcome: outcomeEl ? outcomeEl.textContent.trim() : '',
                            story_id: storyEl ? storyEl.textContent.trim() : '',
                            created_at: whenEl ? whenEl.textContent.trim() : '',
                            claimed_at: status === 'claimed' || status === 'in_flight' ? (whenEl ? whenEl.textContent.trim() : '') : '',
                            completed_at: status === 'closed' ? (whenEl ? whenEl.textContent.trim() : '') : '',
                            claimed_by: workerEl ? workerEl.textContent.trim() : ''
                        };
                        out.push(decorateCard(card));
                    });
                    return out;
                };
                this.inFlight = grab('[data-testid="column-in-flight"]');
                this.enqueued = grab('[data-testid="column-enqueued"]');
                this.closed = grab('[data-testid="column-closed"]');
            },

            get liveClass() { return 'live-dot-' + (this.wsStatus || 'idle'); },

            filteredInFlight() { return this.inFlight.filter(this._match.bind(this)); },
            filteredEnqueued() { return this.enqueued.filter(this._match.bind(this)); },
            filteredClosed() { return this.closed.filter(this._match.bind(this)); },

            get inFlightCount() { return this.filteredInFlight().length; },
            get enqueuedCount() { return this.filteredEnqueued().length; },
            get closedCount() { return this.filteredClosed().length; },

            get inFlightEmpty() { return this.inFlightCount === 0; },
            get enqueuedEmpty() { return this.enqueuedCount === 0; },
            get closedEmpty() { return this.closedCount === 0; },

            _match(t) {
                return originMatches(this.filter.origin, t) && priorityMatches(this.filter.priority, t);
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
                const row = data.row || data.task || data;
                if (!row || !row.id) { return; }
                const card = mergeTaskCard(row);
                switch (ev.Kind) {
                    case 'task.enqueued':
                    case 'task.created':
                        this.enqueued = upsertById(this.enqueued, card);
                        this.inFlight = removeById(this.inFlight, card.id);
                        this.closed = removeById(this.closed, card.id);
                        break;
                    case 'task.claimed':
                    case 'task.in_flight':
                    case 'task.updated':
                        if (card.status === 'closed') {
                            this.closed = upsertById(this.closed, card, 50);
                            this.enqueued = removeById(this.enqueued, card.id);
                            this.inFlight = removeById(this.inFlight, card.id);
                        } else if (card.status === 'enqueued') {
                            this.enqueued = upsertById(this.enqueued, card);
                            this.inFlight = removeById(this.inFlight, card.id);
                            this.closed = removeById(this.closed, card.id);
                        } else {
                            this.inFlight = upsertById(this.inFlight, card);
                            this.enqueued = removeById(this.enqueued, card.id);
                            this.closed = removeById(this.closed, card.id);
                        }
                        break;
                    case 'task.closed':
                        this.closed = upsertById(this.closed, card, 50);
                        this.enqueued = removeById(this.enqueued, card.id);
                        this.inFlight = removeById(this.inFlight, card.id);
                        break;
                }
            },

            async openDrawer($event) {
                const target = $event && $event.currentTarget ? $event.currentTarget : null;
                const taskID = target && target.dataset ? target.dataset.taskId : '';
                if (!taskID) { return; }
                const cfg = window.SATELLITES_TASKS || {};
                if (!cfg.drawerURL) { return; }
                try {
                    const r = await fetch(cfg.drawerURL + taskID, { credentials: 'same-origin' });
                    if (!r.ok) { return; }
                    const data = await r.json();
                    this.drawer = {
                        open: true,
                        task: data.task || {},
                        payload: data.payload || '',
                        excerpts: data.ledger_excerpts || []
                    };
                } catch (e) { /* drawer stays closed */ }
            },

            closeDrawer() { this.drawer.open = false; },

            // x-show workaround for @alpinejs/csp x-show reactivity bug
            // (story_739823eb): bind :class to a getter returning ''
            // or 'is-hidden' instead of relying on x-show to toggle
            // display. The supporting `.is-hidden` class is defined in
            // portal.css.
            get hiddenWhenDrawerOpen() { return this.drawer.open ? '' : 'is-hidden'; },
        };
    }

    // decorateCard adds the precomputed strings the CSP-safe template
    // binds to via `t.X` property access. Iteration vars in Alpine CSP
    // cannot use string concatenation in directive expressions, so the
    // factory pre-builds the per-row class names + testid here.
    function decorateCard(card) {
        card.testid = 'task-card-' + card.id;
        card.priorityClass = 'priority-pill priority-' + (card.priority || '');
        card.statusClass = 'status-pill status-' + (card.status || '');
        card.outcomeClass = 'outcome-pill outcome-' + (card.outcome || '');
        return card;
    }

    function mergeTaskCard(row) {
        return decorateCard({
            id: row.id || '',
            origin: row.origin || '',
            status: row.status || '',
            priority: row.priority || 'medium',
            story_id: row.story_id || (row.payload && row.payload.story_id) || '',
            claimed_by: row.claimed_by || '',
            claimed_at: row.claimed_at || '',
            completed_at: row.completed_at || '',
            outcome: row.outcome || '',
            created_at: row.created_at || ''
        });
    }

    function upsertById(arr, card, cap) {
        const out = [card];
        for (let i = 0; i < arr.length && (!cap || out.length < cap); i++) {
            if (arr[i].id !== card.id) { out.push(arr[i]); }
        }
        return out;
    }

    function removeById(arr, id) {
        return arr.filter(function (c) { return c.id !== id; });
    }

    document.addEventListener('alpine:init', function () {
        window.Alpine.data('tasksView', tasksView);
    });

    // Expose helpers for tests that grep the source.
    window.tasksView = tasksView;
    window.tasksView.__test__ = { mergeTaskCard: mergeTaskCard, upsertById: upsertById, removeById: removeById, decorateCard: decorateCard };
})();
