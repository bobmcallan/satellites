// Package agentprocess holds the canonical agent-process artifact
// content + resolver. The artifact is a `document{type=artifact}` tagged
// `kind:agent-process` carrying the markdown the satellites MCP server
// surfaces as its handshake instructions block.
//
// Resolution chain (sty_e1ab884d):
//
//  1. project-scope `name=agent_process` artifact (per-project override)
//  2. system-scope `name=default_agent_process` artifact (the seed below)
//  3. empty
//
// The body content is the contract — it MUST include the satellites
// fundamentals (configuration over code, story is the unit of work,
// process order, session = one role, five primitives) and the routing
// rules (`satellites_project_set` first, `satellites_story_get` on
// `implement <id>`). A regression test pins these tokens; future edits
// that drop any of them break the build.
package agentprocess

import (
	"context"

	"github.com/bobmcallan/satellites/internal/document"
)

// SystemDefaultName is the name under which the system-scope default
// artifact is seeded.
const SystemDefaultName = "default_agent_process"

// ProjectOverrideName is the name a project's per-project override
// artifact must use to be picked up by the resolver.
const ProjectOverrideName = "agent_process"

// KindTag is the canonical tag every agent-process artifact carries.
// The `document_list(type=artifact, tags=[kind:agent-process])` query
// uses this tag to enumerate every override across a workspace.
const KindTag = "kind:agent-process"

// SystemDefaultBody is the canonical markdown body shipped with the
// system-scope default artifact. The seed and the regression test
// share this constant; if the test fails after an edit, update the
// body deliberately and re-pin the tokens — the seed is the contract.
const SystemDefaultBody = `# satellites · agent process

This block is the satellites MCP server's instructions to your session.
It tells you the *fundamentals* of how this substrate works and the
two routing rules you must apply before any project-scoped work.

## fundamentals

- **configuration over code** — satellites' behaviour is data
  (contracts, agents, configurations, principles) not code paths.
  New behaviour is added by writing rows, not by branching code.
  See ` + "`docs/architecture-configuration-over-code-mandate.md`" + `.
- **story is the unit of work** (` + "`pr_a9ccecfb`" + `). Every change you
  make ties to a story id. There is no work outside a story.
- **workflow is a list of contract names per story**
  (architecture.md §5). There is no separate workflow table —
  the ordered list of ` + "`contract_instance`" + ` rows on a story IS the
  workflow.
- **process order and evidence are first-class.** The
  ` + "`contract_claim`" + ` MCP handler is a server-side gate, not a
  convention. Predecessor CIs must be ` + "`passed`" + ` or ` + "`skipped`" + ` before
  a successor can claim. Evidence on the ledger is the trust
  leverage (` + "`pr_0c11b762`" + `).
- **session = one role.** ` + "`agent_role_claim`" + ` precedes
  ` + "`contract_claim`" + `; sessions don't drift between hats. Reviewer is
  a separate runtime claiming review tasks, not a mode the
  orchestrator switches into.
- **five primitives per project** — projects, stories, contracts
  (instances + documents), documents, ledger.

## routing rules

These rules are mandatory. Apply them in order.

1. **project context first.** Before any project-scoped MCP call,
   identify the active project. If a ` + "`project_id`" + ` is not pinned to
   your session, call ` + "`satellites_project_set(repo_url=…)`" + `.
   Obtain the URL with ` + "`git remote get-url origin`" + ` if needed.
   The verb resolves the existing project for that remote or
   returns ` + "`no_project_for_remote`" + ` — in that case, ask the user
   whether to create the project explicitly via ` + "`project_create`" + `.

2. **story routing.** When the operator says ` + "`implement <story_id>`" + `
   (or ` + "`run <story_id>`" + `), your first MCP call is
   ` + "`satellites_story_get(id=<story_id>)`" + `. The result names the
   project, status, category, tags, and template-required fields —
   everything you need to choose the next call.
`

// ResolverDocs is the read-only subset of document.Store the resolver
// needs. Defined here (rather than imported wholesale) so tests can
// inject a stub without standing up a full document store.
type ResolverDocs interface {
	GetByName(ctx context.Context, projectID, name string, memberships []string) (document.Document, error)
}

// Resolve returns the resolved agent-process body for projectID. Walks
// the project-scope override → system-scope default → empty chain.
// memberships scopes the lookup the same way every other document read
// does (nil = no scoping; empty = deny-all; non-empty = workspace_id IN
// memberships). Errors from the document store fall through to the
// next tier — the resolver never propagates a transient lookup failure.
func Resolve(ctx context.Context, docs ResolverDocs, projectID string, memberships []string) string {
	if docs == nil {
		return ""
	}
	if projectID != "" {
		if d, err := docs.GetByName(ctx, projectID, ProjectOverrideName, memberships); err == nil && isAgentProcess(d) {
			return d.Body
		}
	}
	if d, err := docs.GetByName(ctx, "", SystemDefaultName, memberships); err == nil && isAgentProcess(d) {
		return d.Body
	}
	return ""
}

// isAgentProcess validates that a fetched document is the right shape:
// type=artifact, status=active, carrying the kind:agent-process tag.
// A doc with the right name but the wrong shape is treated as missing —
// the resolver falls through to the next tier rather than serving
// confusing content.
func isAgentProcess(d document.Document) bool {
	if d.Type != document.TypeArtifact || d.Status != document.StatusActive {
		return false
	}
	for _, t := range d.Tags {
		if t == KindTag {
			return true
		}
	}
	return false
}
