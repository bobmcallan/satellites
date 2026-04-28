/* SATELLITES portal_alpine.js — Alpine.data factory registry (epic:portal-csp-strict)
 *
 * Substrate for migrating every portal page off CSP-incompatible inline
 * Alpine expressions (`x-data="{open: false}"`, `:class="'pre-' + s"`,
 * `@click="x = !x"`) onto the registered-factory pattern that runs cleanly
 * under @alpinejs/csp. The CDN swap and the `'unsafe-eval'` removal happen
 * in story_21b228b1 (the stitch story) once every per-view migration has
 * shipped — until then the standard Alpine build keeps un-migrated pages
 * working.
 *
 * ## Migration shape per-view stories follow
 *
 * 1. Move the inline shape into a named factory and register it here:
 *
 *      Alpine.data('factoryName', () => ({
 *        // state
 *        open: false,
 *        // methods
 *        toggle() { this.open = !this.open; },
 *        // getters (computed string returned for :class bindings)
 *        get headerClass() { return 'header header-' + (this.open ? 'open' : 'closed'); },
 *      }));
 *
 *    Then the template is `x-data="factoryName"` (no parens — Alpine CSP
 *    accepts the bare name) or `x-data="factoryName()"` (the function form
 *    is also CSP-safe).
 *
 * 2. Factories that need an initial value accept it as a function argument:
 *
 *      Alpine.data('searchInput', (initialQuery) => ({ q: initialQuery }));
 *
 *    Templates pass the value via `x-data="searchInput('{{.Composite.Filters.Query}}')"`.
 *
 * 3. Class bindings against the iteration variable (`<template x-for>`) move
 *    to a getter or a method that takes the row as an argument. CSP-safe
 *    pattern: the row binds to `t`, the template renders the per-row
 *    discriminator on `data-*`, and the method reads it back from the
 *    event target.
 *
 *      <li :class="priorityPillClass(t)" data-priority="..." @click="openRow">
 *
 *      // factory:
 *      priorityPillClass(t) { return 'priority-pill priority-' + t.priority; },
 *      openRow($event) { this.open($event.target.closest('li').dataset.priority); },
 *
 * 4. Multi-statement `@click="a; b; c"` becomes a single method call:
 *
 *      <button @click="reset"></button>
 *      // factory:
 *      reset() { this.a(); this.b(); this.c(); }
 *
 * Per-view story implementations register their factories alongside this
 * file (or in their own per-view JS file that calls Alpine.data inside its
 * own `alpine:init` listener — both forms are CSP-compatible).
 */

document.addEventListener('alpine:init', () => {
    // Hamburger dropdown — nav.html's right-side menu (settings + sign-out).
    Alpine.data('navHamburger', () => ({
        open: false,
        toggle() { this.open = !this.open; },
        close() { this.open = false; },
        get hiddenWhenOpen() { return this.open ? '' : 'is-hidden'; },
    }));

    // Workspace switcher — nav.html's left-side WORKSPACE / <name> dropdown.
    // Same shape as navHamburger; kept as a separate factory so the
    // workspace and hamburger menus can be open independently and so the
    // factory name documents intent at the callsite.
    Alpine.data('navWorkspaceMenu', () => ({
        open: false,
        toggle() { this.open = !this.open; },
        close() { this.open = false; },
        get hiddenWhenOpen() { return this.open ? '' : 'is-hidden'; },
    }));

    // Theme picker — login page's three-button dark/system/light selector.
    // Each button has its own getter so the CSP build can bind
    // `:aria-pressed="isDarkMode"` without an inline comparison. Initial
    // mode is read from `data-mode` on the host element — @alpinejs/csp
    // does not invoke factories declared with arguments via
    // `x-data="themePicker('mode')"` (story_739823eb).
    Alpine.data('themePicker', () => ({
        mode: '',
        init() {
            this.mode = (this.$el && this.$el.dataset && this.$el.dataset.mode) || '';
        },
        get isDarkMode() { return this.mode === 'dark'; },
        get isLightMode() { return this.mode === 'light'; },
        get isSystemMode() { return this.mode === 'system'; },
    }));

    // Project-detail search input — debounced text filter with a clear
    // button. Methods reach the form/input via Alpine's $refs so the
    // template stays free of inline expressions like `$el.form...`. The
    // initial query is read from `data-initial-query` on the host
    // element for the same reason as themePicker (story_739823eb).
    Alpine.data('projectSearchInput', () => ({
        q: '',
        init() {
            this.q = (this.$el && this.$el.dataset && this.$el.dataset.initialQuery) || '';
        },
        get hasQuery() { return this.q.length > 0; },
        get hiddenWhenHasQuery() { return this.hasQuery ? '' : 'is-hidden'; },
        submitForm() {
            if (this.$root && typeof this.$root.requestSubmit === 'function') {
                this.$root.requestSubmit();
            }
        },
        clearAndSubmit() {
            this.q = '';
            const input = this.$refs && this.$refs.searchInput;
            if (input) {
                input.value = '';
            }
        },
    }));
});
