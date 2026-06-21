// task_panel.js — Alpine component for the project-detail tasks panel
// (sty_f46fe4f2 inline expand + sty_80447ada search / count / status filter),
// peer to storyPanel. Read-only.
//
//   * Inline expand: clicking a task row toggles its detail (task content +
//     ledger/log by execution episode). ?task=<id> (from the /tasks/{id}
//     redirect) auto-expands + scrolls that row on load.
//   * Filter: the search box + tag/category chips + a status toggle commit the
//     query to the SERVER as ?tasks_q=<query>; the server applies the SAME
//     story-filter grammar and re-renders the matching rows. The Filtered/Total
//     count is server-authoritative (seeded from data attributes).
//
// MUST load before alpine.min.js (same ordering reason as story_panel.js).
(function () {
    // hasStatusAll reports whether the query already carries a status:all token
    // (the "show every status" state). Lightweight — no full query parser.
    function hasStatusAll(query) {
        const parts = (query || '').trim().split(/\s+/);
        for (let i = 0; i < parts.length; i++) {
            if (parts[i].toLowerCase() === 'status:all') { return true; }
        }
        return false;
    }

    // stripStatusTokens drops every status:<v> token, returning the remaining
    // query (so the status filter resets to the default open view).
    function stripStatusTokens(query) {
        const parts = (query || '').trim().split(/\s+/).filter(function (p) {
            return p.length > 0 && p.toLowerCase().indexOf('status:') !== 0;
        });
        return parts.join(' ');
    }

    function taskPanel() {
        return {
            // The id of the currently-expanded task row ('' = none).
            expanded: '',
            // The panel query, seeded from ?tasks_q= so a reload/deep-link keeps
            // the filter; mirrored back to the search box via x-model.
            query: '',
            // Server-authoritative count: filtered = tasks matching the active
            // filter, total = project-wide task count.
            filtered: 0,
            total: 0,

            init() {
                try {
                    const url = new URL(window.location.href);
                    const seed = url.searchParams.get('tasks_q');
                    if (seed) { this.query = seed; }
                    // Deep-link from /tasks/{id} → /projects/{id}?task=<id>:
                    // expand that row on load and scroll it into view.
                    const task = url.searchParams.get('task');
                    if (task) {
                        this.expanded = task;
                        this.$nextTick(() => {
                            const host = this.$root || this.$el;
                            const sel = (window.CSS && CSS.escape) ? CSS.escape(task) : task;
                            const row = host && host.querySelector(
                                'tr.task-detail[data-detail-for="' + sel + '"]');
                            if (row && row.scrollIntoView) { row.scrollIntoView({ block: 'center' }); }
                        });
                    }
                } catch (e) { /* URL ctor unavailable — best effort */ }

                // Seed the count from the server-rendered data attributes.
                const root = this.$root || this.$el;
                if (root) {
                    const f0 = parseInt(root.dataset.taskFiltered || '', 10);
                    if (!isNaN(f0)) { this.filtered = f0; }
                    const t0 = parseInt(root.dataset.taskTotal || '', 10);
                    if (!isNaN(t0)) { this.total = t0; }
                }
            },

            toggleTaskRow(ev) {
                const target = ev && ev.currentTarget;
                const id = (target && target.dataset && target.dataset.id) || '';
                if (!id) { return; }
                this.expanded = this.expanded === id ? '' : id;
            },

            isTaskExpanded(el) {
                const id = (el && el.dataset && el.dataset.detailFor) || '';
                return !!id && this.expanded === id;
            },

            taskRowClass(el) {
                const id = (el && el.dataset && el.dataset.id) || '';
                return id && this.expanded === id ? 'is-expanded' : '';
            },

            // copyTaskID writes the task id to the clipboard and flips the cell
            // to "copied!" for ~1.2s — the task peer of storyPanel.copyStoryID,
            // bound to the col-id cell with @click.stop so it short-circuits the
            // row-expand. Falls back to a hidden-textarea + execCommand path on
            // browsers without async clipboard (or over plain http).
            copyTaskID(id, ev) {
                if (!id) { return; }
                const cell = ev && ev.currentTarget;
                const flash = function () {
                    if (!cell) { return; }
                    const code = cell.querySelector('code');
                    if (!code) { return; }
                    const original = code.textContent;
                    code.textContent = 'copied!';
                    cell.classList.add('is-copied');
                    setTimeout(function () {
                        code.textContent = original;
                        cell.classList.remove('is-copied');
                    }, 1200);
                };
                const legacy = function () {
                    try {
                        const ta = document.createElement('textarea');
                        ta.value = id;
                        ta.setAttribute('readonly', '');
                        ta.style.position = 'absolute';
                        ta.style.left = '-9999px';
                        document.body.appendChild(ta);
                        ta.select();
                        const ok = document.execCommand('copy');
                        document.body.removeChild(ta);
                        return ok;
                    } catch (e) { return false; }
                };
                if (navigator.clipboard && typeof navigator.clipboard.writeText === 'function') {
                    navigator.clipboard.writeText(id).then(flash).catch(function () {
                        if (legacy()) { flash(); }
                    });
                    return;
                }
                if (legacy()) { flash(); }
            },

            // statusAll reports whether the panel is showing every status (the
            // toggle's "all" state) vs the default open/non-terminal view.
            statusAll() { return hasStatusAll(this.query); },

            // toggleStatusAll flips between the default open view and the
            // show-all view (sty_80447ada AC#3), then commits to the server.
            toggleStatusAll() {
                if (hasStatusAll(this.query)) {
                    this.query = stripStatusTokens(this.query);
                } else {
                    const rest = stripStatusTokens(this.query);
                    this.query = (rest ? rest + ' ' : '') + 'status:all';
                }
                this.applyToServer();
            },

            // addTagToQuery appends tags:<tag> (epic-order:1 chips wire here),
            // then commits. Mirrors the stories panel.
            addTagToQuery(ev) {
                const target = ev && ev.currentTarget;
                const tag = (target && target.dataset && target.dataset.tag) || '';
                if (!tag) { return; }
                const token = 'tags:' + tag;
                const q = (this.query || '').trim();
                const parts = q.length ? q.split(/\s+/) : [];
                if (parts.indexOf(token) !== -1) { return; }
                parts.push(token);
                this.query = parts.join(' ');
                this.applyToServer();
            },

            // addCategoryToQuery REPLACES any existing category:<v> token (a task
            // has one category), then commits.
            addCategoryToQuery(ev) {
                const target = ev && ev.currentTarget;
                const cat = (target && target.dataset && target.dataset.category) || '';
                if (!cat) { return; }
                const q = (this.query || '').trim();
                const parts = (q.length ? q.split(/\s+/) : []).filter(function (p) {
                    return p.toLowerCase().indexOf('category:') !== 0;
                });
                parts.push('category:' + cat);
                this.query = parts.join(' ');
                this.applyToServer();
            },

            // applyToServer commits the query to the SERVER via ?tasks_q= so the
            // filter applies to the whole task set (not just rendered rows). It
            // preserves any other params (e.g. the stories panel's stories_q) and
            // drops the one-shot ?task= expand.
            applyToServer() {
                if (typeof window === 'undefined' || !window.location) { return; }
                try {
                    const url = new URL(window.location.href);
                    const params = url.searchParams;
                    const qv = (this.query || '').trim();
                    if (qv) { params.set('tasks_q', qv); } else { params.delete('tasks_q'); }
                    params.delete('task');
                    const search = params.toString();
                    window.location.assign(url.pathname + (search ? '?' + search : ''));
                } catch (e) { /* URL ctor / assign unavailable — best effort */ }
            },

            clearTaskFilter() {
                this.query = '';
                this.applyToServer();
            },
        };
    }

    document.addEventListener('alpine:init', function () {
        if (window.Alpine && typeof window.Alpine.data === 'function') {
            window.Alpine.data('taskPanel', taskPanel);
        }
    });
    window.taskPanelFactory = taskPanel;
})();
