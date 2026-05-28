# Workflow skill format

A workflow skill is a markdown file that declares the state machine
satellites uses to drive a story type from `backlog` to `completed`.
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

The `request_review` verb (sty_b8c5c23f) reads the project config to
map the story's type to a workflow-skill path, then calls
`workflow.Parse` on the file's contents. The parsed state machine is
the source of truth for the transition that follows — the verb
gates on `FindTransition(from, to)` before dispatching the named
reviewer skill.
