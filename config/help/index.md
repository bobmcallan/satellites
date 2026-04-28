---
title: Welcome to Satellites
slug: index
order: 0
tags: [help, overview]
---
# Welcome to Satellites

Satellites is a developer-in-the-loop agentic engineering platform. It
coordinates AI agents with a contract-driven lifecycle so the work an
autonomous agent does survives audit by a human reviewer.

## Mental model

Five primitives within a project:

- **documents** — typed content (artifacts, contracts, skills,
  principles, reviewers, agents, configurations, workflows, help).
- **stories** — units of deliverable work.
- **tasks** — orchestration queue.
- **ledger** — append-only audit log.
- **repo** — git remote + semantic index.

A story moves through a workflow — a sequence of contracts, each
performed by an agent, each producing evidence that the next contract
(and a human reviewer) can read.

## Getting around

- **Projects** — your top-level work surface.
- **Stories** — the unit of deliverable work; click in to see CIs,
  evidence, and the ledger.
- **Tasks** — the orchestration queue (cron-driven reviews,
  scheduled jobs, dispatcher work).
- **Config** — workflow, contracts, skills, principles for the
  active project.
- **Help** — you are here.

Use the workspace switcher (top left) to move between client
workspaces if you have memberships in more than one.
