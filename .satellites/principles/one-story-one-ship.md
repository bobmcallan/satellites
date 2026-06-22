---
name: one-story-one-ship
type: document
scope: project
tags: [principles:project]
---

# One story, one ship

In this repo, one engaged story maps to one commit and one release. A story is
shipped through the `satellites-workflow` commit-push step (`shipping → done`),
where the executor commits and pushes and `satellites-commit-push-review` enacts
the close — so what binds a ship to the shipping step is the workflow and its
reviewer, never the binary.

The commitgate requires only a lease-fresh editable engagement to commit or push;
it does NOT bind the share to a particular workflow step (sty_028c3f92). That
binding is substrate's to hold (the workflow + commit-push-review), and it must
stay there — never re-baked as a Go branch.

See [[reviewer-only-model]] and [[agent-goals]].
