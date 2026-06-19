---
name: constitution
tags: [principles:always, area:substrate]
---
# satellites constitution

Satellites is a HARNESS that runs any repo's process as configuration. It ships
no process of its own as code. Process, gates, workflows, and opinions are
substrate — documents, principles, skills, workflow config the operator authors
and edits without a binary release — never Go branches. When a change proposes
baking a process, gate, or opinion into the binary, that is the violation: move
it to the substrate.

- **No gate as code.** A gate is configuration: an internal skill (LLM judgment)
  plus an optional functional check (a deterministic command the gate carries).
  The binary RUNS gates; it never IS one. A version-bump rule, a debt rule, or a
  surface rule baked into the binary is the defect this constitution exists to
  prevent — no other repo could change it.
- **Determinism is never a licence to hardcode the DECISION.** A gate's pass/fail
  rule is configuration even when deterministic — never a Go branch. The functional
  check it names is a COMMAND that MAY invoke a binary MECHANISM (build/test,
  enumerate the binary's own surface, diff a version against its tag): running
  mechanism is the binary's job, deciding is the gate's. Retire a check that
  DUPLICATES a config gate; keep a unique mechanism a config gate names.
- **The binary holds MECHANISM only:** the workflow engine, the document
  substrate and its CRUD, the skill/gate run path (claude -p → verdict), hooks,
  code index, lifecycle, and the product tier. BEHAVIOUR — which gate runs, what
  it judges, the process it enforces — lives in the substrate.
- **Gates read this constitution,** so enforcement tracks evolving intent
  without a recompile.

## Substrate naming

A substrate artifact's name encodes its type so owner and kind read from the
name alone:

- **Principles** — kebab-case, NO prefix (`broken-windows`, `agent-goals`).
- **Skills / capabilities** — `satellites-<kebab>` (`satellites-skill-review`);
  reviewer gates are `satellites-<object>-<stage>-review`.
- **MCP / install SCHEMA docs** — `satellites_<snake>` (`satellites_client_install`,
  `satellites_mcp_load_context`): the machinery a client consumes verbatim.
- **Standalone taxonomy / config docs** keep bare names (`system_variables`,
  `agent-operating-prompt`) — the `satellites_` prefix marks SCHEMA machinery, not
  every document.

The `satellites-` skill prefix is also enforced in code (skill sync's
localSkillName, the config embed guard); this section is the authored intent the
gates read.

See [[process-as-configuration]], [[agent-goals]], [[reviewer-only-model]].
