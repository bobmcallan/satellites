---
name: role_reviewer
allowed_mcp_verbs:
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
  - "agent_role_claim"
  - "agent_role_release"
  - "agent_role_list"
  - "satellites_info"
required_hooks: []
claim_requirements: []
default_context_policy: "fresh-per-claim"
tags: [v4, agents-roles, reviewer]
---
Reviewer role — the authorisation bundle the standalone reviewer service holds. Covers task queue verbs (task_claim/close), contract_review_close for verdict commits, and the ledger/document reads needed to assemble the review packet. Seeded by platform bootstrap when the system-tier KV row `reviewer.service.mode` resolves to `embedded` (the default).
