/* SATELLITES common.js — Alpine.js components */

// Navigation menu: desktop dropdown + mobile slide-out
function navMenu() {
    return {
        dropdownOpen: false,
        mobileOpen: false,
        isMobile() { return window.innerWidth <= 768; },
        toggle() {
            if (this.isMobile()) { this.mobileOpen = true; this.dropdownOpen = false; }
            else { this.dropdownOpen = !this.dropdownOpen; this.mobileOpen = false; }
        },
        closeDropdown() { this.dropdownOpen = false; },
        closeMobile() { this.mobileOpen = false; },
    };
}

// Toast notifications
function toasts() {
    return {
        list: [],
        add(detail) {
            const t = { id: Date.now(), msg: detail.msg || detail, dark: detail.dark || false };
            this.list.push(t);
            setTimeout(() => { this.list = this.list.filter(x => x.id !== t.id); }, 3500);
        }
    };
}

// panels store (sty_70c0f7a3) — URL is the source of truth for which
// panels are open on /projects/{id}. `?expand=stories,documents` lists
// the open keys; absence falls back to per-panel data-default-expanded.
// Toggling rewrites the URL via history.replaceState; nothing is
// persisted server-side, so the page remains stateless and shareable.
function panelsStoreInit(store) {
    const params = new URLSearchParams(window.location.search);
    if (params.has('expand')) {
        store._urlMode = true;
        const set = params.get('expand').split(',').map(s => s.trim()).filter(Boolean);
        const next = {};
        set.forEach(k => { next[k] = true; });
        store._expanded = next;
    }
}

// sectionToggle (story_25695308; URL-state for sty_70c0f7a3) —
// collapsible panel-headed section. Open state lives in the `panels`
// Alpine store, which mirrors the `?expand=...` URL param. The stable
// per-panel identifier is read from `data-toggle-key` on the host
// element; `data-default-expanded` ("true"/"false") is the fallback
// when the URL has no `expand` param.
function sectionToggle() {
    return {
        _key: '',
        init() {
            const ds = (this.$el && this.$el.dataset) || {};
            this._key = ds.toggleKey || '';
            const def = ds.defaultExpanded !== 'false';
            const store = Alpine.store('panels');
            if (!this._key || !store) { return; }
            if (!store._urlMode && def) {
                store._expanded = Object.assign({}, store._expanded, { [this._key]: true });
            }
        },
        toggle() {
            const store = Alpine.store('panels');
            if (!this._key || !store) { return; }
            store.toggle(this._key);
        },
        get open() {
            const store = Alpine.store('panels');
            return !!(store && store._expanded[this._key]);
        },
        get caret() { return this.open ? '▾' : '▸'; },
        get collapsed() { return !this.open; },
        get hiddenWhenOpen() { return this.open ? '' : 'is-hidden'; },
    };
}

// storyPanel (sty_70c0f7a3 + sty_6300fb27 + sty_48198f3e) — V3-style
// story panel.
//
// Parses these tokens out of the query (plus free-text against
// data-search):
//   - `order:<field>` — updated|created|priority|status|title — re-orders
//     the visible rows client-side.
//   - `status:<value>` — all|open|done|cancelled|... — comma-separated
//     values OR. Default chip is `status:open`, which matches any row
//     whose status is NOT done/cancelled.
//   - `priority:<value>` — critical|high|medium|low|all — comma-separated.
//   - `category:<value>` — feature|bug|improvement|... — comma-separated.
//   - `tags:<value>` — single tag; multiple tags compose with AND.
//   - free text — matched against data-search (id + title + tags haystack).
//
// Clicking a tag-chip appends `tags:<tag>` to the query (V3 parity).
// `_filterTick` is a reactive counter the realtime bridge bumps when a
// `story.<status>` WS frame patches a row's data-status, so x-show
// re-evaluates against the new status (sty_48198f3e bug fix — Alpine
// doesn't track dataset mutations natively).
function storyPanel() {
    return {
        query: '',
        expanded: '',
        // Reactive bump counter for realtime row patches. Read from
        // matchesRow so x-show re-runs when bumped.
        _filterTick: 0,
        get tokens() { return parseStoryQuery(this.query); },
        matchesRow(el) {
            // Touch the reactive counter so dataset mutations applied
            // by _applyStoryEvent re-trigger x-show evaluation.
            void this._filterTick;
            const ds = (el && el.dataset) || {};
            const t = this.tokens;
            // status: chip default is `open` (= not done, not cancelled).
            // `status:all` lifts any status filtering. Multi-value lists
            // OR (any-of).
            if (t.status.length === 0) {
                if (ds.status === 'done' || ds.status === 'cancelled') { return false; }
            } else if (t.status.indexOf('all') === -1) {
                let ok = false;
                for (let i = 0; i < t.status.length; i++) {
                    const s = t.status[i];
                    if (s === 'open') {
                        if (ds.status !== 'done' && ds.status !== 'cancelled') { ok = true; break; }
                    } else if (ds.status === s) { ok = true; break; }
                }
                if (!ok) { return false; }
            }
            // priority: any-of (or `all` lifts).
            if (t.priority.length > 0 && t.priority.indexOf('all') === -1) {
                if (t.priority.indexOf((ds.priority || '').toLowerCase()) === -1) { return false; }
            }
            // category: any-of (or `all` lifts).
            if (t.category.length > 0 && t.category.indexOf('all') === -1) {
                if (t.category.indexOf((ds.category || '').toLowerCase()) === -1) { return false; }
            }
            // tags: AND across the chip list. data-tags is a
            // space-delimited string ("foo bar baz ").
            if (t.tags.length > 0) {
                const rowTags = ' ' + (ds.tags || '').toLowerCase() + ' ';
                for (let i = 0; i < t.tags.length; i++) {
                    if (rowTags.indexOf(' ' + t.tags[i] + ' ') === -1) { return false; }
                }
            }
            if (!t.text) { return true; }
            const hay = (ds.search || '').toLowerCase();
            return hay.indexOf(t.text) !== -1;
        },
        isExpanded(el) {
            const id = (el && el.dataset && el.dataset.detailFor) || '';
            return !!id && this.expanded === id;
        },
        rowClass(el) {
            const id = (el && el.dataset && el.dataset.id) || '';
            return id && this.expanded === id ? 'is-expanded' : '';
        },
        toggleRow(ev) {
            const target = ev && ev.currentTarget;
            const id = (target && target.dataset && target.dataset.id) || '';
            if (!id) { return; }
            this.expanded = this.expanded === id ? '' : id;
        },
        addTagToQuery(ev) {
            const target = ev && ev.currentTarget;
            const tag = (target && target.dataset && target.dataset.tag) || '';
            if (!tag) { return; }
            const token = 'tags:' + tag;
            const q = (this.query || '').trim();
            // Avoid duplicate tokens — both the tags:<tag> form and the
            // bare-tag form V3 used to write.
            const parts = q.length ? q.split(/\s+/) : [];
            if (parts.indexOf(token) !== -1 || parts.indexOf(tag) !== -1) { return; }
            parts.push(token);
            this.query = parts.join(' ');
        },
        // Effective chip list for the chip strip beneath the search
        // input. Defaults (status:open, priority:all, category:all)
        // appear when the user hasn't overridden that key. User-entered
        // key:value tokens appear after the defaults; free text gets a
        // single `search:<text>` chip. V3 parity (sty_48198f3e).
        getEffectiveChips() {
            const t = this.tokens;
            const chips = [];
            if (t.status.length === 0) {
                chips.push({ key: 'status', value: 'open', isDefault: true });
            } else {
                for (let i = 0; i < t.status.length; i++) {
                    chips.push({ key: 'status', value: t.status[i], isDefault: false });
                }
            }
            if (t.priority.length === 0) {
                chips.push({ key: 'priority', value: 'all', isDefault: true });
            } else {
                for (let i = 0; i < t.priority.length; i++) {
                    chips.push({ key: 'priority', value: t.priority[i], isDefault: false });
                }
            }
            if (t.category.length === 0) {
                chips.push({ key: 'category', value: 'all', isDefault: true });
            } else {
                for (let i = 0; i < t.category.length; i++) {
                    chips.push({ key: 'category', value: t.category[i], isDefault: false });
                }
            }
            for (let i = 0; i < t.tags.length; i++) {
                chips.push({ key: 'tags', value: t.tags[i], isDefault: false });
            }
            if (t.order) {
                chips.push({ key: 'order', value: t.order, isDefault: false });
            }
            if (t.text) {
                chips.push({ key: 'search', value: t.text, isDefault: false });
            }
            return chips;
        },
        // Strip a key:value (or free-text) token from the query. Default
        // chips are no-ops — a `status:open` default chip becomes a
        // user-set chip the moment the user types `status:done`, so
        // dismissing the default has no token to remove.
        removeChip(key, value) {
            if (!key) { return; }
            if (key === 'search') {
                // Drop free-text by stripping all non-key:value tokens.
                const parts = (this.query || '').trim().split(/\s+/).filter(Boolean);
                const kept = [];
                for (let i = 0; i < parts.length; i++) {
                    if (parts[i].indexOf(':') > 0) { kept.push(parts[i]); }
                }
                this.query = kept.join(' ');
                return;
            }
            const parts = (this.query || '').trim().split(/\s+/).filter(Boolean);
            const kept = [];
            for (let i = 0; i < parts.length; i++) {
                const p = parts[i];
                const idx = p.indexOf(':');
                if (idx <= 0) { kept.push(p); continue; }
                const k = p.slice(0, idx).toLowerCase();
                const v = p.slice(idx + 1).toLowerCase();
                if (k !== key) { kept.push(p); continue; }
                if (k === 'tags' || k === 'order') {
                    if (v !== String(value).toLowerCase()) { kept.push(p); }
                    continue;
                }
                // Comma-separated values: drop matching entry, keep rest.
                const vals = v.split(',').filter(s => s !== String(value).toLowerCase());
                if (vals.length > 0) { kept.push(k + ':' + vals.join(',')); }
            }
            this.query = kept.join(' ');
        },
        // Reset to defaults — drops every token, including free text.
        clearAllFilters() { this.query = ''; },
        // Apply `order:<field>` by physically reordering the table's
        // tbody after a query change. Triggered via a watcher Alpine
        // sets up in init(); kept side-effect-y rather than reactive
        // because re-sorting visible DOM rows is the simplest path.
        init() {
            this.$watch('query', () => { applyStoryOrder(this.$el, this.tokens.order); });
            // Apply once on mount so any initial query (e.g. via #hash) takes effect.
            this.$nextTick(() => { applyStoryOrder(this.$el, this.tokens.order); });
            this._attachRealtimeBridge();
        },
        destroy() {
            if (this._ws && typeof this._ws.close === 'function') { this._ws.close(); }
        },
        // sty_af303c26 (focused slice) — open a workspace-scoped WebSocket
        // and apply `story.<status>` events to the matching story row in
        // place. Existing `internal/story/emit.go` already publishes the
        // event on every UpdateStatus; we just patch the DOM in
        // < 500 ms. Document/contract/ledger panels are not yet wired —
        // they ship in the realtime epic.
        _attachRealtimeBridge() {
            if (!window.SATELLITES_WS || !window.SATELLITES_WS.workspaceId) { return; }
            if (!window.SatellitesWS) { return; }
            const projectID = this._readProjectID();
            if (!projectID) { return; }
            const self = this;
            this._ws = new window.SatellitesWS({
                workspaceId: window.SATELLITES_WS.workspaceId,
                debug: !!window.SATELLITES_WS.debug,
                onEvent: function (ev) { self._applyStoryEvent(ev, projectID); },
            });
            this._ws.connect();
        },
        _readProjectID() {
            const host = document.querySelector('[data-project-id]');
            return host ? (host.dataset.projectId || '') : '';
        },
        _applyStoryEvent(ev, projectID) {
            if (!ev || !ev.Kind) { return; }
            if (ev.Kind.indexOf('story.') !== 0) { return; }
            const data = ev.Data || ev.data || {};
            if (data.project_id && data.project_id !== projectID) { return; }
            const storyID = data.story_id;
            if (!storyID) { return; }
            const row = this.$el.querySelector('tr.story-row[data-id="' + storyID + '"]');
            if (!row) { return; }
            const newStatus = ev.Kind.substring('story.'.length);
            if (!newStatus) { return; }
            row.dataset.status = newStatus;
            const pill = row.querySelector('.col-status .status-pill');
            if (pill) { pill.textContent = newStatus; }
            row.setAttribute('data-realtime-updated-at', String(Date.now()));
            // sty_48198f3e: bump the reactive counter so x-show
            // re-evaluates against the new dataset.status. Alpine
            // doesn't track DOM dataset mutations as reactive deps;
            // this counter is the explicit signal that filters need
            // to re-run.
            this._filterTick++;
        },
    };
}

// parseStoryQuery splits a story-panel query string into structured
// tokens. V3 parity (sty_48198f3e):
//   - `order:<field>` (single) — updated|created|priority|status|title.
//   - `status:<value>` — comma-separated list (`status:open,done`).
//   - `priority:<value>` — comma-separated list.
//   - `category:<value>` — comma-separated list.
//   - `tags:<value>` — single tag per token; multiple `tags:` tokens
//     compose. The `tag:` alias maps to `tags:` for V3 input parity.
//   - free text — anything else, lower-cased, joined with spaces.
//
// Unknown colon-tokens flow through as free text so a bare `epic:foo`
// (the V3 wire-format that pre-dates the `tags:` prefix) still
// matches the data-search haystack via free-text path.
function parseStoryQuery(q) {
    const out = {
        order: '',
        status: [],
        priority: [],
        category: [],
        tags: [],
        text: '',
        // statusOverride retained for back-compat with any external
        // caller; equivalent to status.length > 0 today.
        statusOverride: false,
    };
    const free = [];
    const parts = (q || '').trim().split(/\s+/).filter(Boolean);
    const orderFields = { updated: 1, created: 1, priority: 1, status: 1, title: 1 };
    for (let i = 0; i < parts.length; i++) {
        const p = parts[i];
        const idx = p.indexOf(':');
        if (idx > 0) {
            const k = p.slice(0, idx).toLowerCase();
            const v = p.slice(idx + 1).toLowerCase();
            if (k === 'order' && orderFields[v]) { out.order = v; continue; }
            if (k === 'status') {
                const vals = v.split(',').filter(Boolean);
                for (let j = 0; j < vals.length; j++) { out.status.push(vals[j]); }
                out.statusOverride = true;
                continue;
            }
            if (k === 'priority') {
                const vals = v.split(',').filter(Boolean);
                for (let j = 0; j < vals.length; j++) { out.priority.push(vals[j]); }
                continue;
            }
            if (k === 'category') {
                const vals = v.split(',').filter(Boolean);
                for (let j = 0; j < vals.length; j++) { out.category.push(vals[j]); }
                continue;
            }
            if (k === 'tags' || k === 'tag') {
                if (v) { out.tags.push(v); }
                continue;
            }
        }
        free.push(p.toLowerCase());
    }
    out.text = free.join(' ');
    return out;
}

// applyStoryOrder physically sorts the tbody rows of the story-panel
// table. Each story has TWO rows (the row itself + the detail row);
// pairs are kept together so expand-on-click still targets the right
// detail. host is the panel root element; field is the parsed
// `order:<field>` value (empty = leave default order).
function applyStoryOrder(host, field) {
    if (!host || !field) { return; }
    const tbody = host.querySelector('tbody');
    if (!tbody) { return; }
    const rows = tbody.querySelectorAll('tr.story-row');
    // Build pairs: [row, detail] keyed by data-id; preserve original index for stable sort.
    const pairs = [];
    rows.forEach((row, idx) => {
        const detail = tbody.querySelector('tr.story-detail[data-detail-for="' + row.dataset.id + '"]');
        pairs.push({ row, detail, idx });
    });
    pairs.sort((a, b) => {
        const aval = (a.row.dataset[field] || '').toLowerCase();
        const bval = (b.row.dataset[field] || '').toLowerCase();
        if (aval === bval) { return a.idx - b.idx; }
        // Most fields read better newest-first; title sorts ascending.
        if (field === 'title') { return aval < bval ? -1 : 1; }
        return aval < bval ? 1 : -1;
    });
    for (let i = 0; i < pairs.length; i++) {
        tbody.appendChild(pairs[i].row);
        if (pairs[i].detail) { tbody.appendChild(pairs[i].detail); }
    }
}

// footerStatus (sty_558c0431) — three-slot footer Alpine factory.
// Owns the local uptime tick and the /api/health poll cycle. The right
// slot is data-driven from `footerStatusItems` — adding a status badge
// is one entry there + one corresponding field on /api/health.
function footerStatusItems() {
    return [
        { id: 'gemini', label: 'gemini', field: 'gemini', testid: 'footer-gemini' },
    ];
}

function footerStatus() {
    return {
        items: footerStatusItems(),
        status: {},
        uptimeSeconds: 0,
        startedAtMs: 0,
        _tickHandle: null,
        _pollHandle: null,
        _visHandler: null,
        async init() {
            // Drive the visible counter off the server's `started_at`
            // when we have it, otherwise off the page-load instant. The
            // first /api/health response replaces this anchor.
            this.startedAtMs = Date.now();
            this._tickHandle = setInterval(() => { this.uptimeSeconds = Math.max(0, Math.floor((Date.now() - this.startedAtMs) / 1000)); }, 1000);
            this._visHandler = () => { if (!document.hidden) { this.poll(); } };
            document.addEventListener('visibilitychange', this._visHandler);
            await this.poll();
            this.schedulePoll();
        },
        destroy() {
            if (this._tickHandle) { clearInterval(this._tickHandle); }
            if (this._pollHandle) { clearTimeout(this._pollHandle); }
            if (this._visHandler) { document.removeEventListener('visibilitychange', this._visHandler); }
        },
        schedulePoll() {
            if (this._pollHandle) { clearTimeout(this._pollHandle); }
            // 30 s while visible; pause while hidden — visibilitychange resumes.
            this._pollHandle = setTimeout(async () => {
                if (!document.hidden) { await this.poll(); }
                this.schedulePoll();
            }, 30000);
        },
        async poll() {
            try {
                const r = await fetch('/api/health', { credentials: 'same-origin' });
                if (!r.ok && r.status !== 503) { return; }
                const j = await r.json();
                this.status = j || {};
                if (typeof j.started_at === 'string') {
                    const t = Date.parse(j.started_at);
                    if (!isNaN(t)) { this.startedAtMs = t; }
                }
            } catch (e) { /* swallow — next poll retries */ }
        },
        get uptimeLabel() {
            const s = this.uptimeSeconds;
            const h = Math.floor(s / 3600);
            const m = Math.floor((s % 3600) / 60);
            const ss = s % 60;
            if (h > 0) { return 'agent up ' + h + 'h ' + m + 'm ' + ss + 's'; }
            return 'agent up ' + m + 'm ' + ss + 's';
        },
        badgeClass(item) {
            const v = (this.status && this.status[item.field]) || '';
            if (v === 'ok') { return 'is-ok'; }
            if (v === 'configured') { return 'is-amber'; }
            if (v === 'unreachable') { return 'is-error'; }
            return 'is-muted';
        },
    };
}

document.addEventListener('alpine:init', () => {
    Alpine.store('panels', {
        _expanded: {},
        _urlMode: false,
        init() { panelsStoreInit(this); },
        isOpen(key) { return !!this._expanded[key]; },
        toggle(key) {
            const next = Object.assign({}, this._expanded);
            if (next[key]) { delete next[key]; } else { next[key] = true; }
            this._expanded = next;
            this._writeURL();
        },
        _writeURL() {
            try {
                const url = new URL(window.location.href);
                const list = Object.keys(this._expanded).sort();
                if (list.length) { url.searchParams.set('expand', list.join(',')); }
                else { url.searchParams.delete('expand'); }
                window.history.replaceState({}, '', url);
            } catch (e) { /* history API unavailable */ }
        },
    });
    Alpine.data('sectionToggle', sectionToggle);
    Alpine.data('storyPanel', storyPanel);
    Alpine.data('footerStatus', footerStatus);
});
