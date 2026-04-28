---
title: Workspaces & Roles
slug: workspaces-and-roles
order: 50
tags: [help, workspaces, roles]
---
# Workspaces & Roles

A **workspace** is the multi-tenant boundary — typically one per
client / company. Every project, document, story, task, ledger
row, and repo reference belongs to exactly one workspace. Cross-
workspace reads and writes do not exist by default; the
`memberships` filter on every store call enforces this.

## Roles within a workspace

Four tiers, mapped to capability:

- `admin` — full control including member management.
- `member` — day-to-day contributor.
- `reviewer` — can review submissions but not administer.
- `viewer` — read-only.

Workspace admin authority cascades to every project in the
workspace (per the role-inheritance design from
`epic:workspace-roles`).

## Global admin

A global admin (`User.GlobalAdmin = true` or email in
`SATELLITES_GLOBAL_ADMIN_EMAILS`) may operate across workspaces
to assist with client work. Every ledger row written under
cross-workspace authority carries
`impersonating_as_workspace = <target>` so the audit chain
captures the cross-tenancy. The portal nav surfaces a `GLOBAL
ADMIN` chip whenever the active workspace is outside the user's
memberships.

## Limitations

- Workspace ID is immutable on every primitive. A document
  cannot move between workspaces.
- Global admin reads are unrestricted; writes are permitted but
  always audited.
