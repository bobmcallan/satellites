# Satellites

> A reviewer-gated harness for agentic engineering.

## The thesis

As AI moves from stateless prompt-response to autonomous agents that determine their own path over time, systemic failures emerge. Agents loop endlessly, run down wrong roads, stall, or declare success on a stalled task. These are rarely problems with the underlying model. They are failures of **the harness** — the execution infrastructure built around the model.

Harness engineering optimises four levers: **context**, **tools**, **loop**, **governance**. Satellites is opinionated about all four.

---

## The four levers

### 1. Context — what the model sees

The complete state-space available to the model at the millisecond it calculates its next action: system prompts, prior execution steps, retrieved documents, memory, active variables.

**Failure mode.** Information missing entirely, contextually disconnected from the active turn, or over-saturated — bloated context buries the high-priority signals.

**Architecture lens.** The right information, shaped precisely, injected at the right moment.

### 2. Tools — what the model can do

The exposed action surface that lets a text-generation engine execute state changes in the outside world: codebase endpoints, standardised protocols like MCP, sandboxed CLIs.

**Failure mode.** Raw integrations without semantic schemas the model can predictably parse, or unstructured tools (broad bash terminals) that lack safety constraints.

**Architecture lens.** Tools packaged as precise, self-describing functional schemas, tightly tailored to a specific capability boundary.

### 3. Loop — how the agent runs

The closed-loop cycle of the agent runtime: intake context → execute tool → interpret output → evaluate continuation → repeat until complete.

**Failure mode.** Runaway execution chains, infinite generation loops, false-positive completion when the task has stalled.

**Architecture lens.** Deterministic wrapper assertions — step counts, timeout thresholds, progression detection, definitive exit states.

### 4. Governance — what boundaries it has

The permissions, sandboxes, environment limits, audit logging, and human-in-the-loop approval gates that keep automated actions safely bounded.

**Failure mode.** Broad write/delete permissions without verification gates; or, conversely, restrictions so tight the agent cannot autonomously make necessary updates.

**Architecture lens.** Separated permissioned boundaries — deciding distinctly what runs autonomously versus what requires a hard stop.

---

## How Satellites realises the four levers

Satellites does not let a single model manage an open-ended loop over a giant context window. The framework is built around a single load-bearing premise:

> Separation of roles is **structural**, not advisory.

Each lever lands on a concrete part of that structure.

### Context — the satellite as an isolated sub-harness

Each specialised "satellite" is a dedicated compartment with its own minimised context surface. Stories are the working spine — markdown artifacts carrying scope, acceptance criteria, and an append-only phase log. The ledger is bookkeeping the model does not need to read most of the time. Skills, directives, and the MCP context-load surface shape what enters the window when a phase begins.

Signal stays concentrated because the compartment stays small.

### Tools — packaged capability through MCP and skills

Tools enter the harness through two paths. **MCP verbs** expose the substrate as precise, self-describing schemas — `document_*`, `variable_*`, `project_*`, `apikey_*`, and the reviewer-facing surfaces. **Skills** package capability the executor invokes during a phase.

Skills are pure capability. They do not decide whether work is complete. A skill that runs tests reports the result; something else decides what the result means.

The MCP allowlist is a layering test, not a runtime hope. New verbs land with a test that asserts the verb is reachable from the right scope and rejected from the wrong one.

### Loop — orchestrated phases, not open-ended agents

The agent loop is bounded by the phase, and the phase is bounded by an executor with two verbs.

- `start` — moves a phase from pending to in-progress
- `done` — signals readiness for review

That is the entire executor surface. The executor cannot decide whether its work is acceptable. It cannot mark itself complete. It signals ready, and the loop closes.

Three properties fall out:

- **No runaway loops.** The phase is the unit of progress.
- **No false-positive completion.** State only advances through review.
- **Adaptive without being unpredictable.** Workflows can grow — a reviewer can insert a new phase before the next one when the spec demands it — but only via reviewer action, never via the executor deciding to skip.

State between executors and reviewers passes through the story markdown. There is no agent-to-agent channel. No `ask` verb. Reviewers re-read story and ledger every invocation; they are pure functions.

### Governance — embedded reviewers as the gate

Only reviewers can change story status. That is the gate, and it is structural.

A reviewer is an interface returning `accept | reject` with notes, against a contract authored by the operator. Reviewer implementations:

- `agent` — an LLM judging against the contract
- `static` — a deterministic check (lint, tests, schema validation)
- `external` — a third-party webhook
- `process` — a ledger or state validator
- `composite` — AND / OR over other reviewers

The split between *what runs autonomously* and *what requires a hard stop* maps directly onto the reviewer mix. Hard stops are `static` or `external`. Soft judgement is `agent`. Composites are how the operator writes the policy.

Scopes (`system`, `workspace`, `project`) gate which verbs can read or write which rows. The substrate enforces it at the verb layer, not at the policy layer.

---

## The state machine

Each phase has three states: `pending`, `inprogress`, `done`.

```
pending  --executor.start-->  inprogress  --executor.done-->  [reviewer]
                                                                 |
                                  inprogress  <--reject--+-------+
                                                         |
                                  next phase pending  <--accept
```

Reviewer accept advances to the next phase. Reject returns the phase to in-progress with notes appended to the story. Loops on trivia are intentional — that is the contract being enforced.

A reviewer can also return `accept_with_additional_phase` — accept current work but insert a new phase before the next one. The plan reviewer, seeing an auth change in the spec, inserts a threat-model phase. The workflow adapts without an orchestrator deciding to skip.

---

## Status

V5 is the active line. Predecessors: [v3](https://github.com/bobmcallan/satellites-v3) (in-session, no audit) and [v4](https://github.com/bobmcallan/satellites-v4) (dispatch-via-`claude -p`, content-coupled). The reviewer-kind taxonomy and three-state phase machine above describe the architectural target; the substrate, MCP gateway, and document/story/project verbs are landed. The executor-with-two-verbs orchestrator is in flight under `epic:v5-foundation`.

See [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) for the architectural commitment.

---

## Repository layout

```
cmd/satellites-server/   # server binary entrypoint
cmd/satellites/          # CLI binary entrypoint
internal/                # private packages
migrations/              # Postgres migrations
tests/integration/       # Go + testcontainers integration tests
scripts/                 # local dev (docker-compose) + helpers
docs/                    # architecture + design records
.github/workflows/       # CI + release pipelines
```

## Building

```sh
make build           # builds bin/satellites + bin/satellites-server with
                     # per-binary ldflag injection from .version
make version         # report current per-binary version/commit/build
```

`.version` (repo root) is the canonical version source. It carries **per-binary** entries so `satellites` (CLI) and `satellites-server` can rev independently. `/commit-push` bumps the per-binary entries on every commit; the release workflow reads them, builds each binary with its own ldflags, and tags the release `v<satellites.version>`.

## Database & migrations

Schema lives under `internal/db/migrations/` (golang-migrate, embedded into the binary). The migrator runs against any Postgres reachable via `DATABASE_URL`:

```sh
export DATABASE_URL=postgres://satellites:satellites@localhost:5432/satellites?sslmode=disable
make migrate-up       # apply all migrations
make migrate-status   # current version
make migrate-down     # roll back all migrations (dev only)
```

The same migrations are embedded into `satellites-server` for boot-time application and into the integration test harness.

## Local development

```sh
./scripts/dev-up.sh        # docker compose up; satellites-server builds first time
./scripts/dev-logs.sh      # tail container logs
./scripts/dev-reset.sh     # drop + recreate satellites DB, restart server
./scripts/dev-down.sh      # tear down (removes data volume — destructive)
```

Once up:

- Postgres: `localhost:5432` (db=satellites user=satellites)
- Server:   `http://localhost:8080`, MCP at `/mcp`
- Dev keys: `sk_dev_admin` (RoleAdmin), `sk_dev_user` (RoleUser)

```sh
curl -X POST http://localhost:8080/mcp \
     -H 'Authorization: Bearer sk_dev_admin' -d '{}'
```

## Stories

Stories are the substrate's units-of-work primitive. Fields, conventions (purpose paragraph, context links, epic membership tags, `blocked-by:` markers), and minimum invariants are in [`docs/story-schema.md`](docs/story-schema.md). The reviewer agent (`epic:story-reviewer-process`) checks conventions on top of the schema; the verb layer enforces only `project_id` and `title`.
