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

// sectionToggle (story_25695308) — collapsible panel-headed section.
// The stable per-page identifier (e.g. "proj-{id}-stories") is read
// from `data-toggle-key` on the host element so the factory takes no
// arguments — @alpinejs/csp@3.14.9 does not invoke factories declared
// via `x-data="sectionToggle('arg')"` (story_739823eb). Bare-name
// `x-data="sectionToggle"` plus `data-toggle-key="..."` is the
// CSP-safe shape.
//
// Migrated to Alpine.data registration (story_ecce93ea). Inline
// expression directives are gone from project_detail.html — getters
// `caret` + `collapsed` keep the bindings CSP-compatible.
function sectionToggle() {
    return {
        open: true,
        _toggleKey: '',
        init() {
            this._toggleKey = (this.$el && this.$el.dataset && this.$el.dataset.toggleKey) || '';
            if (!this._toggleKey) { return; }
            try {
                const stored = sessionStorage.getItem('section-toggle:' + this._toggleKey);
                if (stored === 'closed') { this.open = false; }
            } catch (e) { /* sessionStorage unavailable */ }
        },
        toggle() {
            this.open = !this.open;
            if (!this._toggleKey) { return; }
            try {
                sessionStorage.setItem('section-toggle:' + this._toggleKey, this.open ? 'open' : 'closed');
            } catch (e) { /* sessionStorage unavailable */ }
        },
        get caret() { return this.open ? '▾' : '▸'; },
        get collapsed() { return !this.open; },
        get hiddenWhenOpen() { return this.open ? '' : 'is-hidden'; },
    };
}

document.addEventListener('alpine:init', () => {
    Alpine.data('sectionToggle', sectionToggle);
});
