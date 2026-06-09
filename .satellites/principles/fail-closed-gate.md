---
name: fail-closed-gate
type: document
tags: [principles:project]
---

# A gate fails closed

A gate that reviews a transition decides by verifying, never by trusting the
story's claims. (A workflow may declare a transition ungated — a deliberate
pass-through; this binds only gates that do review.)

- **Verify, don't trust.** A criterion is met only when the reviewer runs the
  check itself — read the diff, run the build and tests, read the rows the
  criteria reference. An assertion in the body is intent, not completion.
- **Inability to verify is a reject**, with the reason named — "could not
  execute" never rounds up to accept. When in doubt, reject.
- **Name the gap.** A rejection states the specific unmet thing — "AC #3 has no
  test", not "looks incomplete".
- **An accept that didn't land is a reject.** The gate enacts its own verdict;
  if the recording write fails, the move did not happen.

See [[reviewer-process]], [[agent-goals]].
