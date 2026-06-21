// task_panel.js — Alpine component for the project-detail tasks panel
// (sty_f46fe4f2 inline expand + sty_80447ada search/count/filter + sty_c54160e1
// stories-parity filter chips), peer to storyPanel. Read-only.
//
//   * Inline expand: clicking a task row toggles its detail (task content +
//     ledger/log by execution episode). ?task=<id> (from the /tasks/{id}
//     redirect) auto-expands + scrolls that row on load.
//   * Filter: the search box + tag/category chips commit the query to the SERVER
//     as ?tasks_q=<query>; the server applies the SAME story-filter grammar and
//     re-renders the matching rows. The effective-filter chip strip + the
//     Filtered/Total count mirror the stories panel exactly.
//
// MUST load before alpine.min.js (same ordering reason as story_panel.js).
(function () {
    // parseTaskQuery mirrors the story grammar (status/priority/category/tags/
    // order chips + free text) — display-only; the SERVER filter is authoritative.
    function parseTaskQuery(s) {
        const q = { status: [], priority: [], category: [], tags: [], order: '', free: [] };
        (s || '').trim().split(/\s+/).filter(Boolean).forEach(function (p) {
            const idx = p.indexOf(':');
            if (idx > 0) {
                const k = p.slice(0, idx).toLowerCase();
                const v = p.slice(idx + 1).toLowerCase();
                if (k === 'status' || k === 'priority' || k === 'category') {
                    v.split(',').forEach(function (vv) { if (vv) { q[k].push(vv); } });
                    return;
                }
                if (k === 'tags' || k === 'tag') { if (v) { q.tags.push(v); } return; }
                if (k === 'order') { if (v) { q.order = v; } return; }
            }
            q.free.push(p.toLowerCase());
        });
        return q;
    }

    // removeFromTaskQuery drops one chip's token from the query string (the
    // story panel's removeFromQuery, ported). For comma-joined status/priority/
    // category it removes just the one value; 'search' drops the free text.
    function removeFromTaskQuery(query, key, value) {
        value = String(value).toLowerCase();
        const out = [];
        (query || '').trim().split(/\s+/).filter(Boolean).forEach(function (p) {
            const idx = p.indexOf(':');
            if (key === 'search') { if (idx > 0) { out.push(p); } return; }
            if (idx <= 0) { out.push(p); return; } // free text — keep
            const k0 = p.slice(0, idx).toLowerCase();
            const v0 = p.slice(idx + 1);
            const k = (k0 === 'tag') ? 'tags' : k0;
            if (k !== key) { out.push(p); return; }
            if (key === 'order') { return; } // drop wholesale
            if (key === 'tags') { if (v0.toLowerCase() !== value) { out.push(p); } return; }
            const remaining = v0.split(',').filter(function (x) { return x && x.toLowerCase() !== value; });
            if (remaining.length) { out.push(key + ':' + remaining.join(',')); }
        });
        return out.join(' ');
    }

    // debounce coalesces a burst of live triggers into one trailing refetch
    // (ported from story_panel.js, sty_8f69be8b).
    function debounce(fn, ms) {
        let t = null;
        return function () {
            const args = arguments, self = this;
            if (t) { clearTimeout(t); }
            t = setTimeout(function () { t = null; fn.apply(self, args); }, ms);
        };
    }

    function taskPanel() {
        return {
            // The id of the currently-expanded task row ('' = none).
            expanded: '',
            // The panel query, seeded from ?tasks_q=; mirrored to the search box.
            query: '',
            // Server-authoritative count: filtered / total.
            filtered: 0,
            total: 0,

            init() {
                try {
                    const url = new URL(window.location.href);
                    const seed = url.searchParams.get('tasks_q');
                    if (seed) { this.query = seed; }
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

                const root = this.$root || this.$el;
                if (root) {
                    const f0 = parseInt(root.dataset.taskFiltered || '', 10);
                    if (!isNaN(f0)) { this.filtered = f0; }
                    const t0 = parseInt(root.dataset.taskTotal || '', 10);
                    if (!isNaN(t0)) { this.total = t0; }
                    // Live updates off the SSE bus (sty_4dc86c77). The
                    // project:<id> topic already fires on any task mutation
                    // (status_transition / new execution / body), so subscribe
                    // and refetch the tasks-rows fragment — RUNS / LAST RUN /
                    // STATUS update without a reload, reusing the story channel.
                    const pid = root.dataset.projectId || '';
                    if (pid && window.live && typeof window.live.on === 'function') {
                        this._liveOff = window.live.on('project:' + pid,
                            debounce(() => { this.liveRefresh(pid, root); }, 250));
                    }
                }
            },

            // liveRefresh refetches the CURRENT query's task-rows fragment and
            // swaps it into the tbody, re-binding Alpine on the fresh rows and
            // updating the filtered/total counts — no reload. The expanded row +
            // filter survive because they live in component state, not the DOM.
            async liveRefresh(projectID, root) {
                const tbody = root.querySelector('tbody[data-field="tasks-tbody"]');
                if (!tbody) { return; }
                let resp;
                try {
                    resp = await fetch(
                        '/projects/' + encodeURIComponent(projectID) + '/tasks.fragment' + window.location.search,
                        { headers: { 'Accept': 'text/html' }, credentials: 'same-origin' });
                } catch (e) { return; }
                if (!resp || !resp.ok) { return; }
                const html = await resp.text();
                const filtered = parseInt(resp.headers.get('X-Task-Filtered') || '', 10);
                const total = parseInt(resp.headers.get('X-Task-Total') || '', 10);
                tbody.innerHTML = html;
                if (window.Alpine && typeof window.Alpine.initTree === 'function') {
                    window.Alpine.initTree(tbody);
                }
                if (!isNaN(filtered)) { this.filtered = filtered; }
                if (!isNaN(total)) { this.total = total; }
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
            // to "copied!" for ~1.2s — the task peer of storyPanel.copyStoryID.
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

            // getEffectiveChips mirrors storyPanel: the status/priority/category
            // defaults plus any user-set tags/order/search, for the chip strip.
            getEffectiveChips() {
                const t = parseTaskQuery(this.query);
                const chips = [];
                if (t.status.length === 0) { chips.push({ key: 'status', value: 'open', isDefault: true }); }
                else { t.status.forEach(function (v) { chips.push({ key: 'status', value: v, isDefault: false }); }); }
                if (t.priority.length === 0) { chips.push({ key: 'priority', value: 'all', isDefault: true }); }
                else { t.priority.forEach(function (v) { chips.push({ key: 'priority', value: v, isDefault: false }); }); }
                if (t.category.length === 0) { chips.push({ key: 'category', value: 'all', isDefault: true }); }
                else { t.category.forEach(function (v) { chips.push({ key: 'category', value: v, isDefault: false }); }); }
                t.tags.forEach(function (v) { chips.push({ key: 'tags', value: v, isDefault: false }); });
                if (t.order) { chips.push({ key: 'order', value: t.order, isDefault: false }); }
                if (t.free.length) { chips.push({ key: 'search', value: t.free.join(' '), isDefault: false }); }
                return chips;
            },

            removeChip(key, value) {
                this.query = removeFromTaskQuery(this.query, key, value);
                this.applyToServer();
            },

            clearAllFilters() {
                this.query = '';
                this.applyToServer();
            },

            // addTagToQuery appends tags:<tag> (the epic-order:1 row chips wire
            // here), then commits. Mirrors the stories panel.
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

            // addCategoryToQuery REPLACES any existing category:<v> token, then commits.
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

            // applyToServer commits the query via ?tasks_q= (preserving other
            // params, dropping the one-shot ?task= expand).
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
        };
    }

    document.addEventListener('alpine:init', function () {
        if (window.Alpine && typeof window.Alpine.data === 'function') {
            window.Alpine.data('taskPanel', taskPanel);
        }
    });
    window.taskPanelFactory = taskPanel;
})();
