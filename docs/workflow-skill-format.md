# Workflow skill format

A workflow skill is a markdown file that declares the state machine
satellites uses to drive a story type from `backlog` to `done`.
The file is checked into the consumer project under
`.claude/skills/<name>.md`; the project config maps each `story_type`
to one skill file.

Lifecycle is configured, not coded. Different story types ship
different workflows; the substrate only enforces that requested
transitions exist and that the named reviewer accepts the change
(per the reviewer gate from sty_25d5b21e).

## File shape

Three sections, in order:

1. **YAML frontmatter** between two `---` delimiters: identifies the
   skill and the story types it applies to.
2. **Free-form markdown body**: a human-readable preamble explaining
   the lifecycle. The parser ignores prose.
3. **One fenced `` ```yaml `` block** in the body: declares the
   states and transitions. Anything outside this block is prose.

### Frontmatter fields

| Field        | Type       | Required | Meaning                                           |
| ------------ | ---------- | -------- | ------------------------------------------------- |
| `name`       | string     | yes      | Skill identifier (kebab-case, unique per repo).   |
| `applies_to` | string[]   | yes      | Story types this workflow drives.                 |

### YAML-block fields

| Field         | Type            | Required | Meaning                                              |
| ------------- | --------------- | -------- | ---------------------------------------------------- |
| `states`      | string[]        | yes      | Ordered list of every reachable state.               |
| `transitions` | Transition[]    | yes      | Allowed edges between states.                        |

### Transition fields

| Field             | Type    | Required | Meaning                                                                 |
| ----------------- | ------- | -------- | ----------------------------------------------------------------------- |
| `from`            | string  | yes      | Source state — must appear in `states`.                                 |
| `to`              | string  | yes      | Destination state — must appear in `states`.                            |
| `reviewer_skill`  | string  | no       | Skill name the request_review verb dispatches; empty = unguarded.       |
| `dynamic`         | bool    | no       | Reserved for the dynamic-phase-insertion flow (parsed, not yet acted on). |

## Parser contract

Parsing is strict — every malformed shape returns a precise error.
The substrate refuses to dispatch a workflow it could not fully
parse, so an invalid file fails loudly at request_review time
instead of silently dropping transitions.

The following are errors:

- Missing or empty `name` or `applies_to` frontmatter.
- Frontmatter opened with `---` but no closing `---`.
- No fenced `` ```yaml `` block in the body.
- Fenced block opened but not closed.
- `states` empty, contains an empty string, or contains a duplicate.
- `transitions` empty, contains an empty `from`/`to`, references a
  state that isn't declared, or contains a duplicate edge.

## Example

The canonical example lives at `examples/skills/feature-workflow.md`.
It declares the five-state lifecycle described above, with empty
`reviewer_skill` on the unguarded transitions and `plan-review` /
`done-review` on the two reviewer-gated edges.

Read it directly — reproducing nested-fence markdown inside this doc
makes the rendered version harder to read than the file itself.

## Resolution

The client command `satellites story review <story-id>` (sty_ffec5dab)
runs the gate on the operator machine. There is no server-side
`story_request_review` verb and no `satellites story run` executor driver
— both were retired in sty_6e1c3641; the local agent is the executor, and
`review` is the one gate primitive. A single review resolves and gates one
transition:

1. Read the story by id; derive `story_type` from the `category` field.
2. Select the workflow skill from the **dynamic skill index**
   (sty_815c09e7): the `kind:workflow` entry whose `applies_to` contains
   the story type. This replaced the `project-config` `story_types`
   lookup — `applies_to` is the single source of the type → workflow
   binding.
3. Read the workflow-skill file from the local worktree
   (`.claude/skills/<name>/SKILL.md`) and parse it through
   `internal/workflow`.
4. Pick the transition from the story's current status:
   - **Declarative** (default): exactly one outgoing edge with a
     non-empty `reviewer_skill`. Multiple gated edges error out — use
     `dynamic: true` to disambiguate.
   - **Dynamic** (`dynamic: true` on any outgoing edge): the workflow
     skill itself is dispatched and returns `next_status` in its JSON
     output. The returned status must reference a declared outgoing
     edge.
5. Mint a short-lived reviewer-role api-key (admin-gated, sty_e16f0553)
   scoped to the story; it is exported to the gate subprocess as
   `SATELLITES_REVIEWER_API_KEY` and revoked when the run ends.
6. Dispatch the gate skill as `claude -p --allowedTools "Bash Read Grep
   Glob" --append-system-prompt <gate-body>` against the worktree
   (sty_cba1d47b). The story body + recent ledger arrive on stdin.
7. Expect one JSON object back: `{decision: accept|reject,
   notes, next_status?}`. Anything else is a dispatcher-level error.
8. The gate skill **self-enacts** its verdict under the reviewer key
   (sty_db5cdef0): on `accept` it patches the story status toward the
   workflow-declared target and appends `review_accept` +
   `status_transition`; on `reject` it appends `review_reject` only —
   status unchanged. The client orchestrates (mint, pick transition,
   record `review_requested`, revoke) but never patches status itself.
