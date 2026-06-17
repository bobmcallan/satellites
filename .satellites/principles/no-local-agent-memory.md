---
name: no-local-agent-memory
headline: no local agent memory — the substrate is the shared memory
tags: [principles:always, area:substrate]
---
# No local agent memory

An agent's local "memory" — facts a harness persists OUTSIDE the substrate, in
files on the machine it runs on (a per-project memory store, a scratch notes
file, a private index) — is bound to one machine, one user, one checkout. It
does not travel to another repo, to a teammate, or to CI. Knowledge parked there
is invisible to the next agent and rots unseen, because nothing reviews or
versions it.

Such a store also forks the source of truth. The substrate says one thing, a
private memory says another, and the process the team can read no longer matches
the process an agent actually follows. That drift — and the inconsistent,
unrealistic process flow it produces — is the failure this principle exists to
prevent.

Durable knowledge belongs in the governed substrate: a document, a principle, or
a story, where it is versioned, reviewed, shared, and delivered as context to
every agent that follows. The substrate IS the shared memory.

- **Do not create a local agent-memory store.** When something is worth
  remembering, write it to the substrate, not to a file the harness keeps to
  itself.
- **Remove one that already exists.** Migrate anything still true into the
  substrate, then delete the local store — a stale private memory is worse than
  none.

See [[process-as-configuration]], [[satellites-constitution]].
