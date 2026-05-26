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

    function parseStoryQuery(q) {
        const out = { status: [], priority: [], category: [], tags: [], text: '' };
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
            }
            free.push(p.toLowerCase());
        }
        out.text = free.join(' ');
        return out;
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

            get tokens() { return parseStoryQuery(this.query); },
            get selectionCount() { return this.selectedIDs.size; },

            matchesRow(el) {
                const ds = (el && el.dataset) || {};
                if (ds.id && ds.id === this.expanded) { return true; }
                const t = this.tokens;
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
                    if (k === 'tags') {
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

    document.addEventListener('alpine:init', function () {
        window.Alpine.data('storyPanel', storyPanel);
    });

    window.storyPanel = storyPanel;
    window.storyPanel.__test__ = { parseStoryQuery };
    window.storyPanelFactory = storyPanel;
})();
