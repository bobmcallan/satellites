---
name: agent_gemini_reviewer
tier: flash
tool_ceiling:
  - "task_claim"
  - "task_close"
  - "task_get"
  - "task_list"
  - "contract_review_close"
  - "ledger_get"
  - "ledger_list"
  - "document_get"
  - "contract_get"
  - "session_whoami"
provider_chain:
  - provider: gemini
    model: gemini-2.5-flash
    tier: flash
tags: [v4, agents-roles, reviewer]
---
# Gemini Reviewer Agent

The embedded reviewer service's delivery-agent configuration.
provider_chain=gemini/2.5-flash. `permitted_roles` pins
`role_reviewer` so the boot-time grant-mint path
(`ensureReviewerServiceGrant`) resolves. The role's
`allowed_mcp_verbs` covers the verbs the service exercises:
`task_*` (queue claim/close), `contract_review_close`
(verdict commits), and the ledger/document reads needed to
assemble the review packet.

The service runs alongside the satellites server when the system-tier
KV row `reviewer.service.mode` resolves to `embedded` (the default).
It claims `kind:review` tasks from the queue and returns verdicts.
Operators flip the value via `kv_set` at scope=system; the next boot
picks it up.
