---
name: classified-report-corpus
type: document
scope: project
tags: [area:substrate, area:embed, kind:workflow]
description: The scan-adds → retrieve-by-classification → agent-writes-report loop. A document carries a classification via a classification:<value> tag; the server stores, classifies and embeds (workspace- and project/repo-scope alike); agents do the scanning and the assessing. No runner inside satellites.
---

# Classified report corpus — add classified, retrieve by classification

Make a scan's output (e.g. the security/secrets check) a durable, queryable
quality signal. An **agent** adds documents to the server **with a
classification**; a **retrieving agent** queries **by classification** and
writes an assessment report. The server stores, classifies and embeds; the
agents do the work — there is **no runner inside satellites** (satellites-as-
executor was deliberately reverted 2026-06-21). Holds to [[constitution]]:
mechanism in the binary, process/behaviour here in substrate.

## Classification rides on a tag, not a new field

A document carries its classification as a `classification:<value>` **tag**
(e.g. `classification:security`, `classification:strategy`). No dedicated
schema column: `documents.tags` already exists, `document_list` already AND-
filters on `tags @>`, and `document_upsert`/`SetDocumentTags` already write
tags. A tag is the smallest change that serves the goal and keeps a single
source of truth — the same value drives both keyword and semantic retrieval.

## Scope — repo-bound or workspace-bound

- **Repo-bound** (project scope): `document_upsert {scope:"project",
  project_id, name, body}` then tag it `classification:<value>`. The report
  belongs to the repo it was scanned from.
- **Workspace-bound** (workspace scope): `document_upsert {scope:"workspace",
  workspace_id, name, body}` + tag. The report spans the workspace.

The embed reconcile worker embeds **both** scopes, keyed by the owning
workspace_id, so a repo-bound classified report is retrievable by semantic
search exactly like a workspace-bound one.

## The loop (no governed runner)

1. **Scan / add** — a Claude agent runs a scan (its own capability, e.g. the
   `secrets-scan` skill) and uploads the result:
   `document_upsert` (project or workspace scope) → `SetDocumentTags`/`tags`
   with `classification:<value>`. Cadence/recurrence is the agent's concern,
   not satellites'.
2. **Retrieve by classification** — a retrieving agent (local CLI or MCP):
   - keyword/tag filter: `document_list {scope, tags:["classification:security"]}`
     (CLI: `satellites document list --tags classification:security`), or
   - semantic, scoped to the classification:
     `semantic_search {workspace_id, query, tags:["classification:security"]}`
     (CLI: `satellites semantic-search "<query>" --tags classification:security`).
   Both return repo- and workspace-bound matches.
3. **Assess / report** — the agent writes an assessment from what it retrieved
   and uploads it as **itself a classified document** (e.g.
   `classification:assessment`), repo- or workspace-bound, closing the loop:
   the next retrieval can find the assessment by its classification.

## What is mechanism (in the binary) vs behaviour (here)

- **Mechanism**: `documents.tags` + `document_list` tag filter; the embed
  reconcile worker embedding workspace- and project-scope docs; `semantic_search`
  accepting a `tags` classification filter. These are general capabilities, not
  baked-in process.
- **Behaviour**: which scans run, what the classifications mean, when to assess,
  what an assessment contains — all agent process, documented here, never a Go
  branch.
