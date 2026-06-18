---
name: satellites-principle-authoring
type: skill
kind: capability
description: Author or revise a satellites principle — a standing belief or constraint, not a procedure. Invoke when capturing a durable rule for the substrate; produce it into .satellites/principles/ and run satellites-principle-review before upload.
scope: system
tags: [kind:capability, area:substrate, sync:true]
---
# satellites-principle-authoring

Author or revise a satellites `principle`. Use when capturing a standing belief or constraint for the substrate. A principle is NOT a skill — do not give it the Spec/Verifier/Environment template; that is for procedures. Produce the refined principle into `.satellites/principles/<name>.md`, then run `satellites-principle-review` before upload.

## Clarify the one belief

Before writing, settle with the operator the single constraint this encodes:

- State it as one rule an agent can obey on every relevant action.
- Confirm it is always-on, not a one-time procedure (that is a skill) and not a fact (that is a document).
- Make it testable — a reviewer must be able to point at an action and say "this violates it."

If it bundles several rules, split it into separate principles.

## Shape

Keep the file minimal:

- **Frontmatter** — `name`, `tags`. Add `principles:always` to the tags ONLY when the belief must stay resident in every session (it is then injected at session start); otherwise leave it discoverable via `satellites document index`.
- **Body** — one short belief, imperative or declarative, plus the *why* in a line. Link related principles with `[[name]]`. No procedure steps, no Spec/Verifier/Environment sections, no concrete substrate ids.

## Then review + ship

Run `satellites-principle-review` and resolve every REVISE before upload. Keep it durable and repo-agnostic — a principle must read cleanly to someone outside any one repository.
