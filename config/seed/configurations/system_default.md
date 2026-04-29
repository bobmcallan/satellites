---
name: system_default
contract_refs:
  - preplan
  - plan
  - develop
  - push
  - merge_to_main
  - story_close
skill_refs: []
principle_refs: []
tags: [v4, system, configuration, default]
---
# System Default Configuration

The canonical bundle every story passes through unless its project
overrides the workflow shape. Combines the six lifecycle contracts
in their default phase order with no skill or principle attachments
beyond what the contracts themselves declare.

## Contents

| Phase | Contract | Notes |
|---|---|---|
| 0 | `preplan` | Readiness gate — relevance, dependencies, prior delivery, decision. |
| 1 | `plan` | Implementation strategy + review criteria. |
| 2 | `develop` | Code edits, build, test, vet, fmt, commit. Repeatable up to 10 instances when a story splits naturally. |
| 3 | `push` | Ship the develop commit to origin. |
| 4 | `merge_to_main` | Local fast-forward bookkeeping. |
| 5 | `story_close` | LLM-assessed review verdict, story transitions to done. |

## Use

Operators clone this Configuration into a project Configuration to
get a working starting point — change the contract list, layer in
skill or principle refs, or rename it. The system_default itself
must not be edited at the portal layer; edit the seed file and
re-run the seed loader.

Stories can pin a Configuration via `configuration_id`; agents can
declare a `default_configuration_id`. When neither is set, the
project's default Configuration takes precedence over this system
default — see `pr_a9ccecfb` and `story_fb600b97` for the resolver
precedence chain.

## Why scope=system

`scope=system` lets the configseed loader ship a default Configuration
without owning a dedicated "system" project (which would conflict with
the workspace tenancy model in `pr_0779e5af`). Project Configurations
remain `scope=project` and reference their owning project. story_764726d3.
