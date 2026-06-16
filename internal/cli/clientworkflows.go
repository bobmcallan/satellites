// Client-dir workflows (epic:client-dir-separation order-2). A workflow is
// repo-owned CONFIGURATION, not a published/synced skill: it lives as a markdown
// file under .satellites/workflows/ (cliconfig.ResolveWorkflowsDir) reusing the
// workflow-skill shape (frontmatter name + applies_to, a fenced ```yaml state
// machine). The governing-workflow resolver reads these FIRST, then any leftover
// materialised kind:workflow skill — so a client-dir workflow wins on an
// applies_to tie while the skill path keeps working through the migration.
//
// The reader projects each file into the same matSkill shape the skill-corpus
// readers use (kind forced to "workflow" by location), so `workflow check`,
// `workflow show`, and `workflow design` can consider client-dir workflows
// without a second representation.

package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/bobmcallan/satellites/internal/frontmatter"
	"github.com/bobmcallan/satellites/internal/verb"
)

// clientWorkflowsDir resolves the directory holding client-dir workflow files,
// honouring workflows_dir / the repo-root default. Best-effort: an unconfigured
// repo falls back to the zero-value resolution (<cwd-repo-root>/.satellites/workflows).
func clientWorkflowsDir(configPath string) string {
	cfg, path, err := cliconfig.Load(configPath)
	if err != nil {
		return cliconfig.Config{}.ResolveWorkflowsDir(cliconfig.RepoRootFromConfigPath(configPath))
	}
	return cfg.ResolveWorkflowsDir(cliconfig.RepoRootFromConfigPath(path))
}

// clientWorkflows reads .satellites/workflows/*.md into matSkill entries (kind
// forced to "workflow"). Files that are not workflow-shaped are still returned
// (so `workflow check` can flag them); callers that parse the workflow handle a
// parse error. A missing directory yields none.
func clientWorkflows(configPath string) []matSkill {
	dir := clientWorkflowsDir(configPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []matSkill
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		raw, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil {
			continue
		}
		stripped := frontmatter.StripSyncStamp(raw)
		fm, bodyB, perr := frontmatter.Parse(stripped)
		name := strings.TrimSuffix(e.Name(), ".md")
		body := string(stripped)
		if perr == nil {
			if n := strings.TrimSpace(fm.Name); n != "" {
				name = n
			}
			body = string(bodyB)
		}
		out = append(out, matSkill{
			name:        name,
			kind:        "workflow",
			scope:       "project",
			description: strings.TrimSpace(fm.Description),
			body:        body,
			raw:         string(raw),
		})
	}
	return out
}

// clientWorkflowSources projects the client-dir workflows into the verb
// resolver's input (name + full raw body).
func clientWorkflowSources(configPath string) []verb.WorkflowSource {
	var out []verb.WorkflowSource
	for _, s := range clientWorkflows(configPath) {
		out = append(out, verb.WorkflowSource{Name: s.name, Body: s.raw})
	}
	return out
}

// governingWorkflowSources is the merged source set the governing-workflow
// resolver considers: client-dir workflows FIRST (they win an applies_to tie),
// then the leftover materialised kind:workflow skills. This is the one helper
// the gate dispatcher and the workflow tooling route through.
func governingWorkflowSources(configPath string) []verb.WorkflowSource {
	out := clientWorkflowSources(configPath)
	out = append(out, materialisedWorkflowSources()...)
	return out
}
