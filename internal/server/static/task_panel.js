// task_panel.js — Alpine component for the project-detail tasks panel inline
// expand (sty_f46fe4f2), peer to storyPanel. Read-only: clicking a task row
// toggles its inline detail (task content + ledger/log by execution episode).
// The ?task=<id> deep link (from the /tasks/{id} redirect) auto-expands and
// scrolls that row on load, mirroring the stories ?story=<id> behaviour.
//
// MUST load before alpine.min.js (same ordering reason as story_panel.js): the
// bundled Alpine starts via queueMicrotask(start) at script-eval time, so a
// defer script after alpine.min.js would register its alpine:init listener too
// late and x-data="taskPanel" would be treated as a bare expression.
(function () {
    function taskPanel() {
        return {
            // The id of the currently-expanded task row ('' = none). Only one
            // row is open at a time, like the stories panel.
            expanded: '',

            init() {
                try {
                    const url = new URL(window.location.href);
                    // Deep-link from /tasks/{id} → /projects/{id}?task=<id>:
                    // expand that row on load and scroll it into view.
                    const task = url.searchParams.get('task');
                    if (task) {
                        this.expanded = task;
                        this.$nextTick(() => {
                            const host = this.$root || this.$el;
                            const sel = (window.CSS && CSS.escape) ? CSS.escape(task) : task;
                            const row = host && host.querySelector(
                                'tr.task-detail[data-detail-for="' + sel + '"]');
                            if (row && row.scrollIntoView) { row.scrollIntoView({ block: 'center' }); }
                        });
                    }
                } catch (e) { /* URL ctor unavailable — best effort */ }
            },

            toggleTaskRow(ev) {
                const target = ev && ev.currentTarget;
                const id = (target && target.dataset && target.dataset.id) || '';
                if (!id) { return; }
                this.expanded = this.expanded === id ? '' : id;
            },

            isTaskExpanded(el) {
                const id = (el && el.dataset && el.dataset.detailFor) || '';
                return !!id && this.expanded === id;
            },

            taskRowClass(el) {
                const id = (el && el.dataset && el.dataset.id) || '';
                return id && this.expanded === id ? 'is-expanded' : '';
            },

            // The category/tag chips ship clickable in epic-order:1; the actual
            // task search/filter wiring lands in epic-order:3, which fills these
            // in. They are stubs here so the chip @click.stop handlers resolve
            // (stopping row-toggle propagation) without a console error.
            addTagToQuery() { /* epic-order:3 wires the task filter */ },
            addCategoryToQuery() { /* epic-order:3 wires the task filter */ },
        };
    }

    document.addEventListener('alpine:init', function () {
        if (window.Alpine && typeof window.Alpine.data === 'function') {
            window.Alpine.data('taskPanel', taskPanel);
        }
    });
    window.taskPanelFactory = taskPanel;
})();
