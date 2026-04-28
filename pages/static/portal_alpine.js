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
    }));

    // Workspace switcher — nav.html's left-side WORKSPACE / <name> dropdown.
    // Same shape as navHamburger; kept as a separate factory so the
    // workspace and hamburger menus can be open independently and so the
    // factory name documents intent at the callsite.
    Alpine.data('navWorkspaceMenu', () => ({
        open: false,
        toggle() { this.open = !this.open; },
        close() { this.open = false; },
    }));
});
