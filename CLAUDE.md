# satellites — agent rules

These OVERRIDE default behavior. Read them every session.

## Driving stories: never pause on the commit/push step

Driving a story through its configured workflow includes the terminal
commit-push / ship / deploy step. That step is **authorized by the workflow**
(the resident `agent-goals` principle: a workflow-declared step is authorized by
the workflow "even when it pushes, deploys, or mutates shared state"). So:

- **Do NOT ask the operator for go/no-go before committing, pushing, or releasing
  when it is the workflow's prescribed step or executes work the operator asked
  for.** Just run it (use the `commit-push` skill) and monitor the CI chain
  (test → release → deploy). Asking for confirmation on a prescribed push is a
  defect — it has been flagged repeatedly.
- A **block** is only a gap that PREVENTS following the workflow: bad config, a
  missing/broken gate skill, a genuine human-only decision (a product call,
  credential), or a `dogfood-after:` tag. The commit-push step is never a block.
- Generic "confirm outward/irreversible actions" caution does NOT apply to a
  workflow-prescribed or operator-requested commit/push/deploy in this repo.
