# Satellites

**Reviewer-gated execution for agentic engineering.** An agent can write the code. It can't decide the code is done. Only a reviewer moves the work forward — and every verdict is recorded.

---

## The problem

Agentic coding fails in a way unit tests don't catch: the agent declares success on work that's plausible and wrong, or loops until it times out. More tools, more autonomy, and more retries scale that problem — they don't gate it. What's missing isn't a smarter model. It's a structural boundary between *doing the work* and *deciding the work is acceptable*.

## What Satellites does

Satellites makes that boundary structural, not advisory. Work moves as **stories** through a declared **workflow** of phases. The executor — the agent — can start a phase and signal it's ready. That's all. It cannot change a story's status, and it cannot approve itself. Only a **reviewer** advances the story, by running a gate against the work and returning a verdict:

- **accept** → the story moves to the next phase
- **reject** → the story stays where it is, the reason appended to its log

Reject-stays is deliberate. A phase advances only when the work passes the gate its workflow declares — so "done" means *reviewed*, every time.

## What's real today

This is the landed substrate, not a roadmap:

- **Reviewer-gated state machine.** Three states per phase — `pending → inprogress → done`. Accept moves; reject loops back with its reason. The dispatcher refuses to advance an ambiguous transition rather than guessing, and a workflow with an unbounded loop is rejected when it's parsed.
- **Two kinds of gate.** A gate is either an **LLM reviewer** judging the work against an operator-authored contract, or a **deterministic command** whose exit code selects the pass or fail edge (lint, tests, schema validation). Soft judgement and hard stops, in the same machine.
- **A durable evidence trail.** Every gate run and CI outcome is written as a `qa_evidence` record — decision, from/to status, the reject reason, and the commit it ran against. The record *is* the audit log and the metric source, not a console line that scrolls away.
- **Bounded retry with recovery.** Repeated rejects on the same edge spend a quota and block the story; a recovery gate brings it back with a fresh quota. Loops on trivia are the contract being enforced, not a defect.
- **Roles enforced at the verb layer.** Executor, reviewer, and operator hold different keys, and the server — not a policy document — rejects an executor that tries to approve its own work. The reviewer key is minted per review and revoked after it.
- **An MCP verb gateway and a live portal.** Stories, documents, projects, variables, the ledger, and the reviewer surfaces are exposed as scope-checked MCP verbs. The portal renders stories, gate verdicts, the evidence trail, and a code graph in real time.
- **Code indexing.** Satellites indexes the target repository — symbol map and semantic search — so a reviewer judges against the actual codebase, not a summary of it.

## The state machine

```
pending --executor.start--> inprogress --executor.done--> [ reviewer gate ]
                                                                  |
                        inprogress  <-------- reject -------------+   (reason appended)
                                                                  |
                        next phase pending  <----- accept --------+

repeated reject --> quota spent --> blocked --> [ recovery gate ] --> inprogress (fresh quota)
```

## Install the client

The `satellites` CLI is a per-platform release binary, installed the way `claude install` works — client only, no server. The one-line bootstrap fetches the installer, sha-verifies the platform binary, and places it at `~/.local/bin/satellites` (on PATH):

```sh
curl -fsSL https://github.com/bobmcallan/satellites/releases/latest/download/install.sh | sh
```

Pass `--local` for the in-repo dev binary, or `--version <tag>` to pin a release (`sh -s -- --local`). Once `satellites` is on PATH, `satellites install` / `satellites update` place and refresh the same binary. A `--local` install also scaffolds an uninitialized repo (writes `.mcp.json` + `.satellites/`); a `--global` install places only the binary.

## Drive a story to done

```sh
satellites init      # scaffold + bind to your project (writes .satellites/, .mcp.json)
satellites auth      # browser login → executor key
satellites status    # db up, MCP connected

satellites work init <story-id>                                # engage the workflow
satellites story status_transition --skill <gate> <story-id>   # reviewer-gated transition
```

The executor holds a body-only key: it can edit the story, never advance it. The gate runs, writes its verdict to the evidence trail, and either moves the story on or sends it back with the reason.

## Why not just a workflow runner or an agent harness

A workflow runner sequences steps and trusts each step to report its own success. An agent harness tunes context, tools, and the loop — and then lets the agent decide it's finished. Satellites' difference is the one thing neither does: the actor that produces the work is structurally barred from being the actor that accepts it, and the acceptance is recorded. That single boundary turns *"the agent said it's done"* into *"the work passed a gate, and here is the record."*
