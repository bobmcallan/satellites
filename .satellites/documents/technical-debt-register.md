---
name: technical-debt-register
type: document
tags: [tech-debt-register, content-review:allow-refs]
---

# Technical-debt register

The quarantine register the `technical-debt-review` pre-commit gate
(sty_dd128ef6) reconciles against. Each row is a **known, owned** failing check:
the gate lets a failure pass only when this register names it **and** names the
story that owns it. A failing check that is not here is a *new red* and blocks
the commit; a row with no `story_id` is *unowned* and also blocks (you may not
pad the register to dodge the gate).

The register **only shrinks**: when a quarantined check goes green, its row is
removed (the owning story closes the window). It grows only as a deliberate,
story-backed capture of a failure that cannot be fixed in the moment.

| check_id | story_id | reason |
| --- | --- | --- |
| TestNav_DisabledLinks_DoNotNavigate | sty_b7ba18b3 | portal chromedp UI — flaky/red under WSL headless |
| TestStoryPanel_FilterBugs | sty_b7ba18b3 | portal chromedp UI — flaky/timeout under WSL headless |
| TestStoryPanelOrder | sty_b7ba18b3 | portal UI order/free-text-fallthrough — red under WSL |
| TestProjectDetailPanel_Chromedp | sty_b7ba18b3 | portal chromedp UI — flaky/red under WSL headless |
| TestPrinciplesRideAlong | sty_256a2b3f | principles ride-along sidecar returns empty on read verbs (a0783cc regression) |
| TestDocumentsUploadEndToEnd | sty_256a2b3f | uploaded principle missing from read-verb sidecar (same ride-along regression) |
