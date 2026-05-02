---
name: role_orchestrator
allowed_mcp_verbs:
  - "document_*"
  - "story_*"
  - "ledger_*"
  - "project_*"
  - "repo_*"
  - "workspace_*"
  - "principle_*"
  - "contract_*"
  - "skill_*"
  - "reviewer_*"
  - "agent_*"
  - "role_*"
  - "session_whoami"
  - "satellites_info"
required_hooks:
  - "SessionStart"
  - "PreToolUse"
  - "enforce"
claim_requirements: []
default_context_policy: "fresh-per-claim"
tags: [v4, agents-roles]
---
Orchestrator role — the interactive Claude session's authorisation bundle. Holds every orchestrator-surface MCP verb (document_*, story_*, ledger_*, project_*, repo_*, contract_*, task_*, session_whoami, agent_role_*). Required hooks: SessionStart, PreToolUse, enforce. Seeded by platform bootstrap per pr_contract_separation.
