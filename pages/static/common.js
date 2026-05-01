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

// storyPanel (sty_70c0f7a3) — V3-style story panel. Inline search
// filters rows by their `data-search` attribute (id + title + tags),
// and clicking a row toggles its expand-row showing description, AC,
// and recent ledger activity.
function storyPanel() {
    return {
        query: '',
        expanded: '',
        matchesRow(el) {
            const q = (this.query || '').trim().toLowerCase();
            if (!q) { return true; }
            const hay = ((el && el.dataset && el.dataset.search) || '').toLowerCase();
            return hay.indexOf(q) !== -1;
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
});
