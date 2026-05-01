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

// storyPanel (sty_70c0f7a3 + sty_6300fb27) — V3-style story panel.
// Parses `order:<field>` and `status:<all|done|cancelled>` tokens out of
// the query in addition to free-text matching. Default render hides
// rows whose data-status is "done" or "cancelled" — `status:done`,
// `status:cancelled`, or `status:all` lifts that. `order:<field>` (for
// updated|created|priority|status|title) reorders the visible rows
// client-side. Clicking a tag-chip appends the tag text to the query
// (without navigating) so the user can refine without leaving the panel.
function storyPanel() {
    return {
        query: '',
        expanded: '',
        get tokens() { return parseStoryQuery(this.query); },
        matchesRow(el) {
            const ds = (el && el.dataset) || {};
            const t = this.tokens;
            // Default-hide terminal rows unless the query asks for them.
            if (!t.statusOverride && (ds.status === 'done' || ds.status === 'cancelled')) { return false; }
            if (t.status && t.status !== 'all' && ds.status !== t.status) { return false; }
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
            const q = (this.query || '').trim();
            // Avoid duplicate tokens.
            const parts = q.length ? q.split(/\s+/) : [];
            if (parts.indexOf(tag) === -1) { parts.push(tag); }
            this.query = parts.join(' ');
        },
        // Apply `order:<field>` by physically reordering the table's
        // tbody after a query change. Triggered via a watcher Alpine
        // sets up in init(); kept side-effect-y rather than reactive
        // because re-sorting visible DOM rows is the simplest path.
        init() {
            this.$watch('query', () => { applyStoryOrder(this.$el, this.tokens.order); });
            // Apply once on mount so any initial query (e.g. via #hash) takes effect.
            this.$nextTick(() => { applyStoryOrder(this.$el, this.tokens.order); });
        },
    };
}

// parseStoryQuery splits a story-panel query string into structured
// tokens. Supports `order:<field>`, `status:<value>`, plus free text.
// Unknown colon-tokens flow through as free text so a tag like
// `epic:foo` (V3 convention) still matches the data-search haystack.
function parseStoryQuery(q) {
    const out = { order: '', status: '', statusOverride: false, text: '' };
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
            if (k === 'status') { out.status = v; out.statusOverride = true; continue; }
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
