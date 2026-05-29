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

The `story_request_review` verb (sty_b8c5c23f) drives every status
transition:

1. Read the story by id; derive `story_type` from the `category` field.
2. Read the project-scoped `project-config` document; look up the
   `workflow_skill` path for the story type.
3. Read the workflow-skill file from disk and parse it through
   `internal/workflow`.
4. Pick the transition from `current_status`:
   - **Declarative** (default): exactly one outgoing edge with a
     non-empty `reviewer_skill`. Multiple gated edges error out — use
     `dynamic: true` to disambiguate.
   - **Dynamic** (`dynamic: true` on any outgoing edge): the workflow
     skill itself is dispatched and returns `next_status` in its JSON
     output. The returned status must reference a declared outgoing
     edge.
5. Mint a short-lived reviewer api-key (see sty_25d5b21e) scoped to
   the story; the executor calling the verb never sees this key.
6. Dispatch the gate skill via the configured `GateDispatcher`
   (`claude -p --skill <name>` in production). Story body + recent
   ledger arrive on stdin; the reviewer key is exported as
   `SATELLITES_REVIEWER_API_KEY`.
7. Expect one JSON object back: `{decision: accept|reject,
   notes, next_status?}`. Anything else is a dispatcher-level error.
8. On `accept`: patch the story's status under reviewer-role context
   and append `review_accept` + `status_transition` ledger rows. On
   `reject`: append `review_reject` only — status unchanged.
9. Revoke the reviewer key (always — `defer`-driven so a panic in the
   dispatch still cleans up).
