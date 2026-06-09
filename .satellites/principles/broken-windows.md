---
name: broken-windows
type: document
tags: [principles:project, principles:always]
---

# Broken windows

A failure you encounter is yours: fix it if cheap or in your change's path,
otherwise file a tracked story naming it and surface it to the operator. Never
silently pass a failure by.

Never add a new red — a change that introduces a failing test, a skipped check,
or undocumented debt is not done. Known debt only shrinks: every quarantined
failure is named in the technical-debt register and owned by a story. At commit
the tree is clean or its debt is a story you created; "it was already broken" is
not a pass.

See [[agent-goals]], [[story-execution-process]].
