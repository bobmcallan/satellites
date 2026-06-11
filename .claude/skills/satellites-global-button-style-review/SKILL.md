<!-- satellites-sync:begin {"document_id":"doc_5d268705","version":1,"hash":"c9105e183fd79013f313edd3f42d9a08cb5a4e3d44278c5ac5560ef254b3cbca"} satellites-sync:end -->
---
name: satellites-global-button-style-review
type: skill
kind: gate
when: pre-commit
tags: [kind:gate, area:portal]
description: Run before a commit that touches the portal UI (internal/server/templates, internal/server/static/styles.css). The global-button-style gate fails closed when an action button does not use the canonical `.btn` component — a bare button, an inline `style=` on a button, or per-form one-off button CSS — so the shared button convention cannot drift. Pairs with the `.btn` definition documented in styles.css.
---

# satellites-global-button-style-review

Run before the commit step of any change that touches the portal templates
(`internal/server/templates/*.html`) or the stylesheet
(`internal/server/static/styles.css`). It fails closed when an **action button**
does not render through the canonical global `.btn` component.

## Spec

The decision rule (gate-enforced, scoped to `<button>` so it is deterministically
detectable): every action `<button>` MUST carry the global `.btn` class plus a
variant (`primary`/`secondary`/`danger`/`dev`/`oauth`), defined once in
`static/styles.css`, and MUST NOT carry an inline `style=` attribute. New
per-form/one-off button CSS MUST NOT be introduced — add a `.btn` variant instead.

Non-action `<button>` elements that are their own UI component — tabs
(`story-tab`), chips (`category-chip`, `tag-chip`, `panel-filter-chip-remove`,
`panel-filter-chip-clear`), the expand toggle (`story-expand-toggle`), the modal
close (`modal-close`), search-clear (`ledger-search-clear`), and menu items
(`user-menu*`) — are exempt: they are recognised components, not action buttons.

**Convention (not gate-enforced):** a link styled as a button (`<a>`) should
likewise carry `.btn` + a variant. The gate does not scan `<a>` because a plain
hyperlink is not mechanically distinguishable from a button-styled link; apply
`.btn` to button links by convention. The mechanical gate covers `<button>`.

The question it answers: "does every action `<button>` use the global `.btn`
style?" Any violation fails the gate closed and the commit must not proceed.

## Verifier — Routine

Run from the repo root:

```bash
# (1) Action <button>s lacking the global .btn class (component classes are exempt).
perl -0777 -ne 'while(/<button\b[^>]*>/gs){$t=$&;
  next if $t=~/\bclass="[^"]*\b(?:btn|story-tab|category-chip|tag-chip|story-expand-toggle|modal-close|user-menu[\w-]*|ledger-search-clear|panel-filter-chip-remove|panel-filter-chip-clear)\b/;
  print "FLAG bare/non-.btn button: $t\n"}' internal/server/templates/*.html

# (2) Any button carrying an inline style= override.
grep -rEzoP '<button\b[^>]*\bstyle="[^"]*"[^>]*>' internal/server/templates/*.html
```

**CLEAN (SHIP)** — both checks print nothing. The convention holds; the commit
may proceed.

**FLAG (REVISE)** — either check prints a tag. Do not commit. Fix in the same
change: give the action button `class="btn <variant>"`, remove any inline
`style=`, and move shared styling into a `.btn` variant in `styles.css`
(never a per-form rule). Re-run until both checks are silent.

The mechanical example is locked by `internal/server/button_convention_test.go`
(`TestInviteFormButtonsUseGlobalStyle`): every `<button>` in `admin_people.html`
uses `.btn` and none is inline-styled.

## Environment

Runs in the satellites repo over `internal/server/templates/*.html` and
`internal/server/static/styles.css`. Read-only review — it inspects source and
reports; it makes no external writes.

```yaml
guardrails:
  always:
    - Run both checks from the repo root before committing a portal-UI change.
    - Fix a FLAG in the same change that introduced it (broken-windows).
  ask_first:
    - Adding a new entry to the component allowlist (a genuinely new non-action
      button component) — confirm it is not just an unstyled action button, and
      land the addition in all three sites (description, `## Spec` list, regex).
  never:
    - Commit past a FLAG.
    - Resolve a FLAG with an inline `style=` or a per-form button rule instead of
      a `.btn` variant.
    - Bypass the gate with `--skip-review` or any equivalent override.
```
