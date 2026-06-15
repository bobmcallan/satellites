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
| TestStoryPanel_FilterBugs | sty_b8aac474 | Residual chromedp websocket/render timing flake under full-tier CPU contention — passes deterministically in isolation and in small batches; not a product defect. The systemic tier instability (per-test container churn saturating the Docker daemon) was fixed in sty_0c98760e via a shared container; this lone chromedp flake is the known class scoped out of that story (AC4). Quarantined until sty_b8aac474 hardens the test's readiness wait, then this row is removed. |
