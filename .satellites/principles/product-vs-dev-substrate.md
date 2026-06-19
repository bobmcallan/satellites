---
name: product-vs-dev-substrate
tags: [principles:project]
---
# Product substrate ships; dev substrate stays

Satellites carries two substrate homes, and every artifact belongs to exactly one
— decided by WHO needs it, not by what it does.

- **`config/` is the PRODUCT process.** The skills, principles, and documents
  satellites SHIPS embedded in the binary and reconciles as `scope: system` at
  server boot, to govern ANY repo that uses satellites. Generic and
  repo-agnostic: a reviewer here judges a skill / principle / document / diff that
  could come from any project. `config/skills/satellites-skill-review` is product
  — every satellites user authoring a skill needs it.
- **`.satellites/` is THIS repo's DEVELOPMENT process.** The project-scoped
  substrate for building satellites itself — coupled to this repo's CI, its Go
  build/test, its `.version`, its own governance.
  `.satellites/skills/satellites-commit-push-review` and
  `satellites-integration-test-review` are dev: they only make sense against
  satellites' own pipeline.

The placement test, applied to every new artifact: **would a different repo using
satellites need this?** → `config/` (product, system scope). **Is it about
developing satellites itself?** → `.satellites/` (dev, project scope). Putting
product machinery in `.satellites/` hides it from the repos that need it; putting
this repo's dev plumbing in `config/` ships it to everyone.

See [[constitution]], [[reviewer-only-model]].
