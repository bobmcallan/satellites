---
name: system_variables
scope: system
tags: [kind:variable-taxonomy, v1]
---
# system variables

The system layer of the variable substrate is computed per-request and
non-overridable. Document templates render `{{name}}` against this set
first, then against any operator-set project/workspace variable of the
same name. Use this list when authoring a templated workspace- or
project-scoped document.

| Name              | Type   | Source                                                                                                          | Example shape                                           |
| ----------------- | ------ | --------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------- |
| `version`         | string | satellites-server's own build version (ldflag-stamped at release).                                              | `vX.Y.Z`                                                |
| `cli_version`     | string | The CLI release the server advertises (ldflag-stamped at release, may differ from `version`).                   | `vX.Y.Z`                                                |
| `os`              | string | Caller-supplied on the request; defaults to the server's runtime OS when unsupplied.                            | `linux` / `darwin` / `windows`                          |
| `arch`            | string | Caller-supplied on the request; defaults to the server's runtime architecture when unsupplied.                  | `amd64` / `arm64`                                       |
| `server_url`      | string | The satellites-server's public origin, configured on the deployment.                                            | `https://<host>`                                        |
| `current_version` | string | Caller-supplied on the request: the version of the locally-installed CLI, or empty when absent.                 | `vX.Y.Z` / `""`                                         |
| `state`           | string | Computed from `current_version` vs `cli_version`. One of `install_required`, `up_to_date`, `update_available`.  | `install_required`                                      |

## Resolution rules

- A `{{name}}` placeholder resolves system first, then project, then
  workspace. Operator-set variables of the same name as a system
  variable cannot override the system value.
- A name not in this taxonomy and not set as an operator variable
  surfaces on the `document_get` response's `unresolved_vars` list;
  the placeholder text is preserved in `rendered_body`.
- `os`, `arch`, `current_version` are session-scoped. Supply them on
  every `document_get` call that needs them to render correctly.

## Escape syntax

Write `\{\{name\}\}` in document bodies to keep a literal `{{name}}`
that should not be substituted.
