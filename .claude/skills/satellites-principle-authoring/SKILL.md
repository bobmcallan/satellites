<!-- satellites-sync:begin {"document_id":"doc_6eb7e05d","version":1,"hash":"77a22fd74ea5c307b927e3a93ac09b70aaaefb9142e13da93e758530e82782a9"} satellites-sync:end -->
---
name: satellites-principle-authoring
type: skill
kind: capability
scope: system
tags: [kind:capability, area:substrate]
---
# satellites-principle-authoring

Author or revise a satellites `principle`. Use when capturing a standing belief or constraint for the substrate. A principle is NOT a skill — do not give it the Spec/Verifier/Environment template; that is for procedures. Produce the refined principle into `.satellites/principles/<name>.md`, then run `principle-review` before upload.

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

Run `principle-review` and resolve every REVISE before upload. Keep it durable and repo-agnostic — a principle must read cleanly to someone outside any one repository.
