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
// `key` is a stable per-page identifier (e.g. "proj-{id}-stories"); the
// open/closed state is persisted in sessionStorage so navigation within
// the same browser tab restores the user's choice.
//
// Migrated to Alpine.data registration (story_ecce93ea). Inline
// expression directives are gone from project_detail.html — getters
// `caret` + `collapsed` keep the bindings CSP-compatible.
function sectionToggle(key) {
    return {
        open: true,
        init() {
            try {
                const stored = sessionStorage.getItem('section-toggle:' + key);
                if (stored === 'closed') { this.open = false; }
            } catch (e) { /* sessionStorage unavailable */ }
        },
        toggle() {
            this.open = !this.open;
            try {
                sessionStorage.setItem('section-toggle:' + key, this.open ? 'open' : 'closed');
            } catch (e) { /* sessionStorage unavailable */ }
        },
        get caret() { return this.open ? '▾' : '▸'; },
        get collapsed() { return !this.open; },
    };
}

document.addEventListener('alpine:init', () => {
    Alpine.data('sectionToggle', sectionToggle);
});
