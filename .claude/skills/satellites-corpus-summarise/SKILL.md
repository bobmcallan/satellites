---
name: corpus-summarise
type: skill
kind: task
tags: [kind:task]
description: Summarise every document in the workspace corpus into one document, aligned to the workspace objective.
tools: [document_list, document_get, semantic_search]
---
<!-- satellites-sync:begin {"document_id":"doc_0e6956a9","version":15,"hash":"69a749c6f7f6add622f5eb978148cf26c8ae55c467e01773a4f0f556be7238e1","publisher":"proj_682cfeed"} satellites-sync:end -->
<!-- satellites-library:begin {"publisher":"proj_682cfeed","repo":"git@github.com:bobmcallan/satellites-skills.git","commit":"02290b00e3a68ffca0360fb69f90f61e83fee8bb"} satellites-library:end -->

# Corpus summarise

A workspace task: read the full workspace corpus and the workspace objective, and
produce a single summary document that captures what the corpus contains and how it
serves the objective. A workspace agent runs this server-side and writes the result as
a workspace document.

## Spec

Produce one markdown document:

- Open with a one-paragraph statement of how the corpus, taken together, serves the
  workspace objective.
- Then a section per significant theme found across the documents — name the theme,
  summarise what the corpus says about it, and cite the source document names.
- Close with **Gaps**: what the objective implies the corpus should cover but does not.

Be faithful to the sources; do not invent facts not present in the corpus. When sources
conflict, prefer the most recent document and say so. Keep each theme summary tight —
2–4 sentences, no nested bullet lists — and hold the whole document to roughly one
screen so it stays scannable at a glance.

## Verifier

The output is well-formed when it names its source documents, ties back to the objective
in the opening paragraph, and contains no claim absent from the corpus.

## Environment

Read-only over the workspace corpus; writes exactly one workspace document (the summary)
under the configured output name.
