/*
 * story_panel.js — Alpine factory for the project-detail story panel.
 * Ported from satellites-v4 (V3-parity) — trimmed: no realtime,
 * no task sub-table, no URL expand persistence.
 *
 * Token grammar parsed off `query`:
 *   status:<v[,v...]>     `all`, `open`, or ANY status value — matched literally
 *                         against each row's real status, so a custom workflow's
 *                         states (e.g. integration_review) filter with no fixed
 *                         enum. `open` = not in the TERMINAL set (done/cancelled,
 *                         where every workflow ends); `all` clears the filter.
 *                         The status vocabulary is whatever the stories'
 *                         workflows use — never a hardcoded lifecycle
 *                         (epic:dynamic-workflow-status).
 *   priority:<v[,v...]>   critical|high|medium|low|all
 *   category:<v[,v...]>   feature|bug|improvement|...
 *   tags:<v>              single tag; multiple tokens OR (union of matched rows)
 *   order:<field>         updated|created|priority|status|title|id|epic-order
 *                         (unknown fields render as an order: chip too,
 *                         they just have no sort effect)
 *   <free text>           lowercased substring match against data-search
 *
 * Defaults rendered as is-default chips: status:open, priority:all,
 * category:all. Removing a default chip drops the predicate (e.g.
 * status:open → status:all). Removing the order chip restores the
 * server-rendered DOM order.
 */
(function () {
    'use strict';

    // loadStoryFragment lazy-loads the merged Ledger/Log fragment into el on
    // first tab open in the inline story panel (sty_762730ad; merged view
    // epic:graduated-workflow ledger-log-merge). The fetched markup is plain
    // HTML (no Alpine directives), so a direct innerHTML swap is safe.
    // credentials:'same-origin' carries the session cookie.
    window.loadStoryFragment = function (storyID, el) {
        if (!el) { return; }
        fetch('/stories/' + encodeURIComponent(storyID) + '/trace.fragment',
              { headers: { 'Accept': 'text/html' }, credentials: 'same-origin' })
            .then(function (r) { return r.ok ? r.text() : null; })
            .then(function (html) {
                el.innerHTML = (html !== null && html !== '')
                    ? html : '<p class="empty">nothing to show</p>';
            })
            .catch(function () { el.innerHTML = '<p class="empty">failed to load</p>'; });
    };

    // debounce coalesces a burst of calls into one trailing invocation after
    // ms of quiet — used so a flurry of SSE triggers yields a single refetch
    // (sty_8f69be8b).
    function debounce(fn, ms) {
        let t = null;
        return function () {
            const args = arguments, self = this;
            if (t) { clearTimeout(t); }
            t = setTimeout(function () { t = null; fn.apply(self, args); }, ms);
        };
    }

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

    // orderFields lists the columns the panel can reorder by — these
    // resolve to `row.dataset[field]` for sort key. Any other value
    // passed to applyStoryOrder is interpreted as a tag prefix, so
    // `order:epic-order`, `order:order`, or any project-specific
    // sequencing tag works uniformly.
    const orderFields = { updated: 1, created: 1, priority: 1, status: 1, title: 1, id: 1 };

    function parseStoryQuery(q) {
        const out = { status: [], priority: [], category: [], tags: [], order: '', text: '', textTokens: [] };
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
                if (k === 'order') {
                    // Accept any order:<v> structurally — the chip
                    // always renders as `order:<v>`, never as search
                    // free-text. Unknown fields just have no sort
                    // effect (applyStoryOrder bails when the field
                    // isn't in orderFields / orderTagFields).
                    if (v) { out.order = v; }
                    continue;
                }
                // Unknown key (any other `<word>:<val>`) falls through
                // to free text — matches v4's behaviour. Operators get
                // a hint when their typo doesn't surface as a chip.
            }
            free.push(p.toLowerCase());
        }
        out.text = free.join(' ');
        // textTokens drives matching with OR (union) semantics — a row
        // matches when its haystack contains ANY token, so multiple IDs
        // return all of them (sty_1bd0098a). out.text stays the joined
        // string for the single search chip's label.
        out.textTokens = free;
        return out;
    }

    // tagOrderValue pulls the value out of a `<prefix>:<v>` tag in the
    // row's space-joined data-tags attribute. Value is alphanumeric
    // (any non-whitespace) — projects use `epic-order:1`, `order:a`,
    // `priority-rank:p0`, etc.; sort uses localeCompare(numeric:true)
    // for natural `1 < 2 < 10 < a < b` ordering. Returns null when the
    // tag is absent so the caller can sink missing-tag rows.
    function tagOrderValue(row, prefix) {
        const tags = (row.dataset.tags || '').toLowerCase();
        const re = new RegExp('(?:^|\\s)' + prefix + ':([^\\s]+)');
        const m = re.exec(tags);
        if (!m) { return null; }
        return m[1];
    }

    // applyStoryOrder physically reorders the tbody story-row pairs by
    // the given field. Each story has TWO rows (the row itself + the
    // detail row); pairs are kept together so click-to-expand keeps
    // binding to the correct detail.
    //
    // Field resolution:
    //   - in orderFields → sort by row.dataset[field]
    //   - anything else  → sort by the tag prefix `<field>:<v>`
    //                      (alphanumeric values, natural sort,
    //                       missing-tag rows sink to bottom)
    function applyStoryOrder(host, field) {
        if (!host || !field) { return; }
        const tagSort = !orderFields[field];
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
                // Missing-tag rows sink to the bottom of an ascending
                // sort; localeCompare with numeric:true handles
                // `1 < 2 < 10 < a < b` natural ordering for the rest.
                if (av === null && bv === null) { return a.idx - b.idx; }
                if (av === null) { return 1; }
                if (bv === null) { return -1; }
                const cmp = av.localeCompare(bv, undefined, { numeric: true, sensitivity: 'base' });
                if (cmp === 0) { return a.idx - b.idx; }
                return cmp;
            }
            // Status sorts by the server-supplied workflow rank (the status's
            // position in the story's own ## Workflow), ascending = lifecycle
            // order (first state → terminal), NOT alphabetically. Unranked rows
            // (no workflow / off-workflow status) sink to the bottom. This is
            // what makes a custom lifecycle (integration_review, commit, …) sort
            // by its real position (epic:dynamic-workflow-status order:3).
            if (field === 'status') {
                const ar = parseInt(a.row.dataset.statusRank, 10);
                const br = parseInt(b.row.dataset.statusRank, 10);
                const arank = isNaN(ar) ? Number.MAX_SAFE_INTEGER : ar;
                const brank = isNaN(br) ? Number.MAX_SAFE_INTEGER : br;
                if (arank === brank) { return a.idx - b.idx; }
                return arank - brank;
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

    // restoreStoryOrder puts story-row + story-detail pairs back into
    // the original server-rendered order captured at init(). Called
    // when the operator removes the order chip — without this, the
    // table stays in whatever applyStoryOrder last sorted it into.
    function restoreStoryOrder(host, originalOrder) {
        if (!host || !originalOrder || originalOrder.length === 0) { return; }
        const tbody = host.querySelector('tbody');
        if (!tbody) { return; }
        const rowByID = new Map();
        const detailByID = new Map();
        tbody.querySelectorAll('tr.story-row').forEach(r => {
            if (r.dataset.id) { rowByID.set(r.dataset.id, r); }
        });
        tbody.querySelectorAll('tr.story-detail').forEach(d => {
            const id = d.dataset.detailFor;
            if (id) { detailByID.set(id, d); }
        });
        for (let i = 0; i < originalOrder.length; i++) {
            const id = originalOrder[i];
            const row = rowByID.get(id);
            if (row) { tbody.appendChild(row); }
            const detail = detailByID.get(id);
            if (detail) { tbody.appendChild(detail); }
        }
    }

    // isKnownToken returns true when `p` parses as a recognised key:value
    // token (status / priority / category / tags / tag / order). Mirrors
    // parseStoryQuery's structured-bucket discrimination — anything
    // isKnownToken says is false will end up in parseStoryQuery's
    // free-text bucket (out.text). `order:<anything>` is structurally
    // known even when the field isn't in orderFields / orderTagFields;
    // applyStoryOrder no-ops on unknown fields so the chip is harmless.
    function isKnownToken(p) {
        const idx = p.indexOf(':');
        if (idx <= 0) { return false; }
        const k = p.slice(0, idx).toLowerCase();
        const v = p.slice(idx + 1).toLowerCase();
        if (k === 'status' || k === 'priority' || k === 'category') { return true; }
        if (k === 'tags' || k === 'tag') { return v.length > 0; }
        if (k === 'order') { return v.length > 0; }
        return false;
    }

    // removeFromQuery is the pure half of removeChip — given a current
    // query string, a key (status / priority / category / tags / order
    // / search), and the value being dropped, returns the rewritten
    // query. Pulled out of the Alpine method so unit tests can exercise
    // the rewrite logic directly via window.storyPanelFactory.__test__.
    //
    // The `search` key drops the entire free-text bucket. Free text is
    // anything parseStoryQuery would not pull into a structured field —
    // including colon-bearing tokens with an unrecognised key (e.g.
    // `order:order`, which falls through because `order` isn't an
    // orderFields entry). isKnownToken handles the discrimination so the
    // two paths stay in sync.
    function removeFromQuery(query, key, value) {
        if (!key) { return query || ''; }
        if (key === 'search') {
            const parts = (query || '').trim().split(/\s+/).filter(Boolean);
            const kept = parts.filter(isKnownToken);
            return kept.join(' ');
        }
        const parts = (query || '').trim().split(/\s+/).filter(Boolean);
        const kept = [];
        const wantVal = String(value).toLowerCase();
        for (let i = 0; i < parts.length; i++) {
            const p = parts[i];
            const idx = p.indexOf(':');
            if (idx <= 0) { kept.push(p); continue; }
            const k = p.slice(0, idx).toLowerCase();
            const v = p.slice(idx + 1).toLowerCase();
            if (k !== key) { kept.push(p); continue; }
            if (k === 'tags' || k === 'order') {
                if (v !== wantVal) { kept.push(p); }
                continue;
            }
            const vals = v.split(',').filter(s => s !== wantVal);
            if (vals.length > 0) { kept.push(k + ':' + vals.join(',')); }
        }
        return kept.join(' ');
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
            // Active tab of the expanded row's inline panel. Lives here (not
            // in a per-row x-data) so it survives the liveRefresh tbody swap,
            // same as `expanded` (sty_0ac9c75a). One row expands at a time,
            // so a single field suffices.
            detailTab: 'description',
            _ledgerLoaded: false,
            // _originalRowOrder is the snapshot of story-row ids in
            // server-rendered order, captured once on init() so removing
            // the order chip can restore the table.
            _originalRowOrder: [],
            // The count indicator reads `filtered / total` (sty_47234d6e):
            // filtered = stories matching the active filter across all pages,
            // total = project-wide all-status count. Both are server-authoritative
            // — seeded from data attributes, refreshed from the fragment headers.
            filtered: 0,
            total: 0,
            // _rev bumps on each live refresh so visibleRowCount (and the
            // indicator) recompute even though this.query did not change.
            _rev: 0,
            _liveOff: null,

            // Seed this.query from the URL ?stories_q= param so refresh
            // + deep-link preserve the filter. The $watch wired below
            // mirrors mutations back to the URL via replaceState. Both
            // halves preserve the cursor-pagination params already on
            // the URL (stories_cursor / stories_page / stories_back) —
            // only stories_q rotates. The watcher also re-applies the
            // order:<field> reorder on every query change so typing or
            // removing the order chip rearranges the visible rows
            // without a server round-trip. When the operator drops the
            // order chip the watcher hands off to restoreStoryOrder
            // which puts rows back into _originalRowOrder.
            init() {
                try {
                    const url = new URL(window.location.href);
                    const seed = url.searchParams.get('stories_q');
                    if (seed) { this.query = seed; }
                    // Deep-link from the deprecated standalone story page
                    // (sty_0633bcf5): ?story=<id> expands that row on load.
                    const story = url.searchParams.get('story');
                    if (story) {
                        this.expanded = story;
                        this.$nextTick(() => {
                            const host = this.$root || this.$el;
                            const sel = (window.CSS && CSS.escape) ? CSS.escape(story) : story;
                            const row = host && host.querySelector('tr.story-detail[data-detail-for="' + sel + '"]');
                            if (row && row.scrollIntoView) { row.scrollIntoView({ block: 'center' }); }
                        });
                    }
                } catch (e) { /* URL ctor unavailable — best effort */ }
                const root = this.$root || this.$el;
                // Capture original DOM order BEFORE any applyStoryOrder
                // call. This is what restoreStoryOrder uses on chip-remove.
                if (root) {
                    const initialRows = root.querySelectorAll('tbody tr.story-row');
                    const order = [];
                    initialRows.forEach(r => {
                        if (r.dataset && r.dataset.id) { order.push(r.dataset.id); }
                    });
                    this._originalRowOrder = order;
                }
                this.$watch('query', (value) => {
                    writeQueryToURL(value);
                    const ord = parseStoryQuery(value).order;
                    if (ord) {
                        applyStoryOrder(root, ord);
                    } else {
                        restoreStoryOrder(root, this._originalRowOrder);
                    }
                });
                // Apply once on mount so a seeded URL filter with
                // order:<field> takes effect after the first paint.
                this.$nextTick(() => {
                    applyStoryOrder(root, parseStoryQuery(this.query).order);
                });

                // Live updates off the SSE bus (sty_8f69be8b). Seed the total
                // from the server-rendered data attr, then subscribe to this
                // project's topic — the shared client (live.js) opens its one
                // EventSource on this first live.on call. A burst of triggers
                // debounces into a single refetch.
                if (root) {
                    const f0 = parseInt(root.dataset.storyFiltered || '', 10);
                    if (!isNaN(f0)) { this.filtered = f0; }
                    const t0 = parseInt(root.dataset.storyTotal || '', 10);
                    if (!isNaN(t0)) { this.total = t0; }
                    const pid = root.dataset.projectId || '';
                    if (pid && window.live && typeof window.live.on === 'function') {
                        this._liveOff = window.live.on('project:' + pid,
                            debounce(() => { this.liveRefresh(pid, root); }, 250));
                    }
                }
            },

            // liveRefresh refetches the CURRENT page's story-rows fragment
            // (same query — never resets to page 1) and swaps it into the
            // tbody, re-binds Alpine on the new rows, re-captures the server
            // order, re-applies the active sort, and updates the total. Scroll
            // is untouched (no reload) and selection/expand survive because
            // they live in component state, not the DOM (sty_8f69be8b).
            async liveRefresh(projectID, root) {
                const tbody = root.querySelector('tbody[data-field="stories-tbody"]');
                if (!tbody) { return; }
                let resp;
                try {
                    resp = await fetch(
                        '/projects/' + encodeURIComponent(projectID) + '/stories.fragment' + window.location.search,
                        { headers: { 'Accept': 'text/html' }, credentials: 'same-origin' });
                } catch (e) { return; }
                if (!resp || !resp.ok) { return; }
                const html = await resp.text();
                const filtered = parseInt(resp.headers.get('X-Story-Filtered') || '', 10);
                const total = parseInt(resp.headers.get('X-Story-Total') || '', 10);
                const page = parseInt(resp.headers.get('X-Story-Page') || '', 10);
                const pageCount = parseInt(resp.headers.get('X-Story-Page-Count') || '', 10);

                tbody.innerHTML = html;
                if (window.Alpine && typeof window.Alpine.initTree === 'function') {
                    window.Alpine.initTree(tbody);
                }
                // Fresh rows carry fresh data-last-seen — re-run the
                // engagement-dot aging pass so colors snap immediately
                // (sty_25e2e8ac).
                if (typeof window.satAgeEngagementDots === 'function') {
                    window.satAgeEngagementDots();
                }

                // The swap wiped the expanded row's lazily-loaded ledger
                // fragment; detailTab survived (component state, not DOM),
                // so re-load the content the open tab is showing.
                if (this.expanded && this.detailTab === 'ledger') {
                    window.loadStoryFragment(this.expanded, this.ledgerPanelFor(this.expanded));
                }

                // Re-capture the server order for the freshly-rendered rows,
                // then re-apply the active sort (or restore server order).
                const order = [];
                tbody.querySelectorAll('tr.story-row').forEach(r => {
                    if (r.dataset && r.dataset.id) { order.push(r.dataset.id); }
                });
                this._originalRowOrder = order;
                const ord = parseStoryQuery(this.query).order;
                if (ord) { applyStoryOrder(root, ord); } else { restoreStoryOrder(root, order); }

                if (!isNaN(filtered)) { this.filtered = filtered; }
                if (!isNaN(total)) { this.total = total; }
                this._rev++; // force any visibleRowCount readers to recompute

                const ind = root.querySelector('[data-field="panel-stories-page-indicator"]');
                if (ind && !isNaN(page)) {
                    ind.textContent = 'page ' + page + (pageCount > 0 ? ' of ' + pageCount : '');
                }
            },

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
                void this._rev;
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
                    // Multiple `tags:` tokens combine with OR — a row
                    // matches when at least one tag token is present in
                    // its data-tags. Operators reach the AND surface by
                    // typing a single token; OR is the multi-token
                    // default because it serves the common "stories in
                    // either epic" query (epic:foo + epic:bar are
                    // mutually exclusive memberships).
                    const rowTags = ' ' + (ds.tags || '').toLowerCase() + ' ';
                    let tagOk = false;
                    for (let i = 0; i < t.tags.length; i++) {
                        if (rowTags.indexOf(' ' + t.tags[i] + ' ') !== -1) { tagOk = true; break; }
                    }
                    if (!tagOk) { return false; }
                }
                if (!t.textTokens || t.textTokens.length === 0) { return true; }
                // Free-text tokens OR: a row matches when its haystack
                // contains ANY token (sty_1bd0098a). Mirrors the server.
                const hay = (ds.search || '').toLowerCase();
                for (let i = 0; i < t.textTokens.length; i++) {
                    if (t.textTokens[i] && hay.indexOf(t.textTokens[i]) !== -1) { return true; }
                }
                return false;
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
                // A newly expanded (or collapsed) row starts back on the
                // Description tab with a fresh ledger lazy-load state.
                this.detailTab = 'description';
                this._ledgerLoaded = false;
            },

            // ledgerPanelFor locates the expanded row's ledger container.
            // Looked up by data attribute rather than x-ref — every detail
            // row carries the same ref name, so refs would collide.
            ledgerPanelFor(id) {
                const host = this.$root || this.$el;
                const sel = (window.CSS && CSS.escape) ? CSS.escape(id) : id;
                return host && host.querySelector(
                    'tr.story-detail[data-detail-for="' + sel + '"] [data-field="story-ledger"]');
            },

            // openLedger switches the expanded row's panel to Ledger/Log and
            // lazy-loads the merged fragment on first open (sty_762730ad).
            openLedger(id) {
                this.detailTab = 'ledger';
                if (this._ledgerLoaded) { return; }
                this._ledgerLoaded = true;
                window.loadStoryFragment(id, this.ledgerPanelFor(id));
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
                this.applyToServer();
            },

            // addCategoryToQuery mirrors addTagToQuery for the category chip
            // (sty_7772f005). A story has one category, so this REPLACES any
            // existing category:<v> token rather than appending — clicking a
            // different category swaps the filter.
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

            // applyToServer commits the current query to the SERVER so the
            // filter/sort applies across every page, not just the rendered
            // one (sty_1bd0098a). It navigates to ?stories_q=<query>, resetting
            // to page 1 and dropping the cursor-pagination params — the server
            // offset-paginates the filtered set. Live typing still previews
            // instantly via matchesRow/x-show; Enter and chip changes commit.
            applyToServer() {
                if (typeof window === 'undefined' || !window.location) { return; }
                try {
                    const url = new URL(window.location.href);
                    const params = new URLSearchParams();
                    const qv = (this.query || '').trim();
                    if (qv) { params.set('stories_q', qv); }
                    const search = params.toString();
                    window.location.assign(url.pathname + (search ? '?' + search : ''));
                } catch (e) { /* URL ctor / assign unavailable — best effort */ }
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
                this.query = removeFromQuery(this.query, key, value);
                this.applyToServer();
            },

            clearAllFilters() {
                this.query = '';
                this.applyToServer();
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
    window.storyPanelFactory.__test__ = { parseStoryQuery, removeFromQuery };
})();

// Engagement processing dot aging (sty_25e2e8ac). Color is a pure function of
// now − data-last-seen against the panel's data-eng-*-secs thresholds; a
// lease_until in the past greys the dot. Runs at load, on a 30s aging timer,
// and after every liveRefresh tbody swap; exposed on window so the
// integration tier drives aging deterministically (no reload).
(function () {
    function ageEngagementDots(nowMs) {
        var now = typeof nowMs === 'number' ? nowMs : Date.now();
        var panel = document.querySelector('[data-eng-orange-secs]');
        var orangeMs = (panel ? parseInt(panel.dataset.engOrangeSecs, 10) || 300 : 300) * 1000;
        var redMs = (panel ? parseInt(panel.dataset.engRedSecs, 10) || 900 : 900) * 1000;
        document.querySelectorAll('.engagement-dot').forEach(function (dot) {
            var last = Date.parse(dot.dataset.lastSeen || '');
            if (isNaN(last)) { return; }
            var lease = Date.parse(dot.dataset.leaseUntil || '');
            var age = now - last;
            dot.classList.remove('is-orange', 'is-red', 'is-stale');
            if (!isNaN(lease) && lease < now) {
                dot.classList.add('is-stale');
            } else if (age >= redMs) {
                dot.classList.add('is-red');
            } else if (age >= orangeMs) {
                dot.classList.add('is-orange');
            }
            var mins = Math.max(0, Math.round(age / 60000));
            dot.title = 'last activity ' + mins + 'm ago';
        });
    }
    window.satAgeEngagementDots = ageEngagementDots;
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', function () { ageEngagementDots(); });
    } else {
        ageEngagementDots();
    }
    setInterval(function () { ageEngagementDots(); }, 30000);
})();
