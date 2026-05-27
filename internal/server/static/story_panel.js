/*
 * story_panel.js — Alpine factory for the project-detail story panel.
 * Ported from satellites-v4 (V3-parity) — trimmed: no realtime,
 * no task sub-table, no URL expand persistence.
 *
 * Token grammar parsed off `query`:
 *   status:<v[,v...]>     all|open|backlog|ready|in_progress|review|done|cancelled
 *   priority:<v[,v...]>   critical|high|medium|low|all
 *   category:<v[,v...]>   feature|bug|improvement|...
 *   tags:<v>              single tag; multiple tokens AND
 *   <free text>           lowercased substring match against data-search
 *
 * Defaults rendered as is-default chips: status:open, priority:all,
 * category:all. Removing a default chip drops the predicate (e.g.
 * status:open → status:all).
 */
(function () {
    'use strict';

    // legacyCopy writes value to the clipboard via a temporary textarea
    // + document.execCommand('copy'). Used as the fallback when
    // navigator.clipboard is missing (older browsers) or rejected
    // (non-secure-context Chrome). Returns true on success.
    function legacyCopy(value) {
        try {
            const ta = document.createElement('textarea');
            ta.value = value;
            ta.setAttribute('readonly', '');
            ta.style.position = 'fixed';
            ta.style.top = '-1000px';
            ta.style.opacity = '0';
            document.body.appendChild(ta);
            ta.select();
            const ok = document.execCommand('copy');
            document.body.removeChild(ta);
            return ok;
        } catch (e) {
            return false;
        }
    }

    // orderFields lists the columns the panel can reorder by. Mirrors
    // v4's whitelist. id is appended here because data-id is always
    // stamped on tr.story-row and operators occasionally want a stable
    // alpha sort by id.
    const orderFields = { updated: 1, created: 1, priority: 1, status: 1, title: 1, id: 1 };

    // orderTagFields lists order keys whose sort value is extracted
    // from a numeric tag (e.g. `epic-order:<n>`) rather than a row
    // dataset attribute. Rows missing the tag sink to the bottom.
    const orderTagFields = { 'epic-order': 1 };

    function parseStoryQuery(q) {
        const out = { status: [], priority: [], category: [], tags: [], order: '', text: '' };
        const free = [];
        const parts = (q || '').trim().split(/\s+/).filter(Boolean);
        for (let i = 0; i < parts.length; i++) {
            const p = parts[i];
            const idx = p.indexOf(':');
            if (idx > 0) {
                const k = p.slice(0, idx).toLowerCase();
                const v = p.slice(idx + 1).toLowerCase();
                if (k === 'status' || k === 'priority' || k === 'category') {
                    const vals = v.split(',').filter(Boolean);
                    for (let j = 0; j < vals.length; j++) { out[k].push(vals[j]); }
                    continue;
                }
                if (k === 'tags' || k === 'tag') {
                    if (v) { out.tags.push(v); }
                    continue;
                }
                if (k === 'order' && (orderFields[v] || orderTagFields[v])) {
                    out.order = v;
                    continue;
                }
                // Unknown key OR `order:<unknown>` falls through to free
                // text — matches v4's behaviour. Operators get a hint
                // when their typo doesn't surface as a chip.
            }
            free.push(p.toLowerCase());
        }
        out.text = free.join(' ');
        return out;
    }

    // tagOrderValue pulls the numeric value out of a `<prefix>:<n>`
    // tag in the row's space-joined data-tags attribute. Returns
    // Number.POSITIVE_INFINITY when the tag is absent so missing-tag
    // rows sink to the bottom of an ascending sort.
    function tagOrderValue(row, prefix) {
        const tags = (row.dataset.tags || '').toLowerCase();
        const re = new RegExp('(?:^|\\s)' + prefix + ':(\\d+)(?:\\s|$)');
        const m = re.exec(tags);
        if (!m) { return Number.POSITIVE_INFINITY; }
        return parseInt(m[1], 10);
    }

    // applyStoryOrder physically reorders the tbody story-row pairs by
    // the given field. Each story has TWO rows (the row itself + the
    // detail row); pairs are kept together so click-to-expand keeps
    // binding to the correct detail. Field is one of orderFields' keys,
    // one of orderTagFields' keys (tag-derived numeric sort), OR
    // '' / unknown — the last leaves the table untouched.
    function applyStoryOrder(host, field) {
        if (!host || !field) { return; }
        const tagSort = !!orderTagFields[field];
        if (!tagSort && !orderFields[field]) { return; }
        const tbody = host.querySelector('tbody');
        if (!tbody) { return; }
        const rows = tbody.querySelectorAll('tr.story-row');
        const pairs = [];
        rows.forEach((row, idx) => {
            const id = row.dataset.id;
            const detail = id ? tbody.querySelector('tr.story-detail[data-detail-for="' + id + '"]') : null;
            pairs.push({ row, detail, idx });
        });
        pairs.sort((a, b) => {
            if (tagSort) {
                const av = tagOrderValue(a.row, field);
                const bv = tagOrderValue(b.row, field);
                if (av === bv) { return a.idx - b.idx; }
                return av < bv ? -1 : 1;
            }
            const aval = (a.row.dataset[field] || '').toLowerCase();
            const bval = (b.row.dataset[field] || '').toLowerCase();
            if (aval === bval) { return a.idx - b.idx; }
            // Title sorts ascending (alphabetical reads naturally);
            // every other field descends (newest / highest first).
            if (field === 'title') { return aval < bval ? -1 : 1; }
            return aval < bval ? 1 : -1;
        });
        for (let i = 0; i < pairs.length; i++) {
            tbody.appendChild(pairs[i].row);
            if (pairs[i].detail) { tbody.appendChild(pairs[i].detail); }
        }
    }

    // writeQueryToURL persists the current filter query as ?stories_q=
    // on the URL via history.replaceState (NOT pushState — every
    // keystroke is not a history entry). Empty value removes the param.
    // Pagination params (stories_cursor / stories_page / stories_back)
    // and any other URL params are preserved.
    function writeQueryToURL(value) {
        if (typeof window === 'undefined' || !window.history ||
            typeof window.history.replaceState !== 'function') { return; }
        try {
            const url = new URL(window.location.href);
            if (value && value.length > 0) {
                url.searchParams.set('stories_q', value);
            } else {
                url.searchParams.delete('stories_q');
            }
            window.history.replaceState(window.history.state, '', url.toString());
        } catch (e) { /* URL ctor or replaceState unavailable — best effort */ }
    }

    function storyPanel() {
        return {
            query: '',
            expanded: '',
            statusBusy: {},
            selectedIDs: new Set(),
            bulkTarget: 'ready',
            bulkBusy: false,
            bulkResultText: '',

            // Seed this.query from the URL ?stories_q= param so refresh
            // + deep-link preserve the filter. The $watch wired below
            // mirrors mutations back to the URL via replaceState. Both
            // halves preserve the cursor-pagination params already on
            // the URL (stories_cursor / stories_page / stories_back) —
            // only stories_q rotates. The watcher also re-applies the
            // order:<field> reorder on every query change so typing or
            // removing the order chip rearranges the visible rows
            // without a server round-trip.
            init() {
                try {
                    const url = new URL(window.location.href);
                    const seed = url.searchParams.get('stories_q');
                    if (seed) { this.query = seed; }
                } catch (e) { /* URL ctor unavailable — best effort */ }
                const root = this.$root || this.$el;
                this.$watch('query', (value) => {
                    writeQueryToURL(value);
                    applyStoryOrder(root, parseStoryQuery(value).order);
                });
                // Apply once on mount so a seeded URL filter with
                // order:<field> takes effect after the first paint.
                this.$nextTick(() => {
                    applyStoryOrder(root, parseStoryQuery(this.query).order);
                });
            },

            get selectionCount() { return this.selectedIDs.size; },

            // visibleRowCount returns the number of story rows currently
            // visible in the panel — used by the "shown / total" header
            // indicator. Reads this.query so the getter re-evaluates
            // reactively when the chip filter changes. The actual count
            // is from matchesRow (the same predicate x-show uses) so the
            // indicator and the visible rows stay synchronised.
            //
            // Uses $root (the x-data ancestor) rather than $el (which
            // resolves to the binding's own element — the counter span,
            // not the panel root that contains the rows).
            get visibleRowCount() {
                void this.query;
                const root = this.$root || this.$el;
                if (!root) { return 0; }
                const rows = root.querySelectorAll('tr.story-row');
                let n = 0;
                for (let i = 0; i < rows.length; i++) {
                    if (this.matchesRow(rows[i])) { n++; }
                }
                return n;
            },

            matchesRow(el) {
                const ds = (el && el.dataset) || {};
                if (ds.id && ds.id === this.expanded) { return true; }
                // Read this.query directly so Alpine's reactive proxy
                // tracks the dependency. A `get tokens()` accessor used
                // to wrap this, but Alpine 3.15.12 did not re-trigger
                // x-show on every row when the query changed via the
                // getter path — direct property access works.
                const t = parseStoryQuery(this.query);
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
                if (t.priority.length > 0 && t.priority.indexOf('all') === -1) {
                    if (t.priority.indexOf((ds.priority || '').toLowerCase()) === -1) { return false; }
                }
                if (t.category.length > 0 && t.category.indexOf('all') === -1) {
                    if (t.category.indexOf((ds.category || '').toLowerCase()) === -1) { return false; }
                }
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

            // copyStoryID writes the story id to the clipboard and flips
            // the cell text to "copied!" for ~1.2s. Bound to the .col-id
            // cell with @click.stop so it short-circuits the row-expand
            // handler. Falls back to a hidden-textarea + execCommand path
            // on browsers without async clipboard (or when the page is
            // served over plain http — Chrome gates navigator.clipboard
            // behind secure-context).
            copyStoryID(id, ev) {
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
                if (navigator.clipboard && typeof navigator.clipboard.writeText === 'function') {
                    navigator.clipboard.writeText(id).then(flash).catch(function () {
                        legacyCopy(id) && flash();
                    });
                    return;
                }
                if (legacyCopy(id)) { flash(); }
            },

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
            },

            getEffectiveChips() {
                const t = parseStoryQuery(this.query);
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
                // order is NOT seeded as a default chip — matches v4.
                // The default chip strip is status / priority / category
                // only; order surfaces only when the operator types it.
                if (t.order) {
                    chips.push({ key: 'order', value: t.order, isDefault: false });
                }
                if (t.text) {
                    chips.push({ key: 'search', value: t.text, isDefault: false });
                }
                return chips;
            },

            removeChip(key, value) {
                if (!key) { return; }
                if (key === 'search') {
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
                        // Single-value keys: drop the matching token; keep others.
                        if (v !== String(value).toLowerCase()) { kept.push(p); }
                        continue;
                    }
                    const vals = v.split(',').filter(s => s !== String(value).toLowerCase());
                    if (vals.length > 0) { kept.push(k + ':' + vals.join(',')); }
                }
                this.query = kept.join(' ');
            },

            clearAllFilters() { this.query = ''; },

            async applyRowStatus(id, target) {
                if (!id || !target || this.statusBusy[id]) { return; }
                this.statusBusy = Object.assign({}, this.statusBusy, { [id]: true });
                try {
                    const res = await this._postStatus(id, target);
                    if (res.ok) { this._patchRowStatus(id, target); }
                    return res;
                } finally {
                    const next = Object.assign({}, this.statusBusy);
                    delete next[id];
                    this.statusBusy = next;
                }
            },

            isSelected(id) { return this.selectedIDs.has(id); },

            toggleRowSelection(id, ev) {
                if (ev && ev.stopPropagation) { ev.stopPropagation(); }
                if (!id) { return; }
                const next = new Set(this.selectedIDs);
                if (next.has(id)) { next.delete(id); } else { next.add(id); }
                this.selectedIDs = next;
                this.bulkResultText = '';
            },

            clearSelection() {
                this.selectedIDs = new Set();
                this.bulkResultText = '';
            },

            async applySelectionStatus() {
                if (this.bulkBusy || this.selectedIDs.size === 0 || !this.bulkTarget) { return; }
                this.bulkBusy = true;
                this.bulkResultText = '';
                const ids = Array.from(this.selectedIDs);
                const target = this.bulkTarget;
                const results = await Promise.all(ids.map(id => this._postStatus(id, target)));
                let applied = 0, failed = 0;
                for (let i = 0; i < results.length; i++) {
                    if (results[i].ok) {
                        applied++;
                        this._patchRowStatus(ids[i], target);
                    } else { failed++; }
                }
                this.bulkResultText = 'applied ' + applied + ' / failed ' + failed;
                this.bulkBusy = false;
            },

            async _postStatus(id, target) {
                try {
                    const resp = await fetch('/api/stories/' + encodeURIComponent(id) + '/status', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        credentials: 'same-origin',
                        body: JSON.stringify({ status: target }),
                    });
                    const text = await resp.text();
                    return { ok: resp.ok, status: resp.status, body: text };
                } catch (err) {
                    return { ok: false, status: 0, body: String(err) };
                }
            },

            _patchRowStatus(id, target) {
                const row = this.$el.querySelector('tr.story-row[data-id="' + id + '"]');
                if (row) {
                    row.dataset.status = target;
                    const pill = row.querySelector('.col-status .status-pill');
                    if (pill) { pill.textContent = target; }
                }
                const buttons = this.$el.querySelectorAll(
                    'section[data-story-status="' + id + '"] button.status-button'
                );
                buttons.forEach(b => {
                    if (b.dataset.statusTarget === target) {
                        b.setAttribute('disabled', '');
                        b.setAttribute('aria-pressed', 'true');
                    } else {
                        b.removeAttribute('disabled');
                        b.removeAttribute('aria-pressed');
                    }
                });
            },
        };
    }

    // The template loads story_panel.js BEFORE alpine.min.js so this
    // alpine:init listener is attached before Alpine fires the event.
    // Reversing those <script> tags causes Alpine to walk the DOM and
    // treat x-data="storyPanel" as a bare expression (yielding the
    // function value, not the data object) — chip strip + row x-show
    // bind to an empty stack and nothing renders. Keep the order.
    document.addEventListener('alpine:init', function () {
        if (window.Alpine && typeof window.Alpine.data === 'function') {
            window.Alpine.data('storyPanel', storyPanel);
        }
    });

    window.storyPanelFactory = storyPanel;
    window.storyPanelFactory.__test__ = { parseStoryQuery };
})();
