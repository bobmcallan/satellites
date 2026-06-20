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
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	substrate "github.com/bobmcallan/satellites/config"
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
			source:      "local",
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

// embeddedWorkflows reads the config/workflows/*.md embed (substrate.FS — the
// binary's system-default workflows) into matSkill entries (kind forced to
// "workflow", scope "system"). This is the GOVERNANCE SOURCE that makes
// .satellites/workflows optional (override-only, sty_6c6056f9): a repo with no
// client-dir workflows is still governed by these embedded defaults. A missing
// or unreadable embed yields none (the build-time TestConfigWorkflowsSystemContained
// pins the embed's presence and self-containment).
func embeddedWorkflows() []matSkill {
	entries, err := fs.ReadDir(substrate.FS, "workflows")
	if err != nil {
		return nil
	}
	var out []matSkill
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		raw, rerr := fs.ReadFile(substrate.FS, "workflows/"+e.Name())
		if rerr != nil {
			continue
		}
		fm, bodyB, perr := frontmatter.Parse(raw)
		name := strings.TrimSuffix(e.Name(), ".md")
		body := string(raw)
		if perr == nil {
			if n := strings.TrimSpace(fm.Name); n != "" {
				name = n
			}
			body = string(bodyB)
		}
		out = append(out, matSkill{
			name:        name,
			kind:        "workflow",
			scope:       "system",
			source:      "embed",
			description: strings.TrimSpace(fm.Description),
			body:        body,
			raw:         string(raw),
		})
	}
	return out
}

// embeddedWorkflowSources projects the config/workflows embed into the verb
// resolver's input (name + full raw body).
func embeddedWorkflowSources() []verb.WorkflowSource {
	var out []verb.WorkflowSource
	for _, s := range embeddedWorkflows() {
		out = append(out, verb.WorkflowSource{Name: s.name, Body: s.raw})
	}
	return out
}

// governingWorkflowSources is the merged source set the governing-workflow
// resolver considers, in PRECEDENCE order: client-dir workflows FIRST (a local
// .satellites/workflows file wins an applies_to tie), then the leftover
// materialised kind:workflow skills, then the config/workflows EMBED LAST (the
// system-default governance source). Local wins over embed because the resolver
// returns the first match in source order (sty_6c6056f9). This is the one helper
// the gate dispatcher and the workflow tooling route through.
func governingWorkflowSources(configPath string) []verb.WorkflowSource {
	out := clientWorkflowSources(configPath)
	out = append(out, materialisedWorkflowSources()...)
	out = append(out, embeddedWorkflowSources()...)
	return out
}

// withEmbeddedWorkflows merges the config/workflows embed into a client-dir
// workflow set with LOCAL-WINS by name: an embedded default is appended only
// when no local workflow already carries that name. So `workflow check` over a
// repo that still ships its own .satellites/workflows copies is unchanged, while
// a repo with none is governed by the embedded defaults (sty_6c6056f9).
func withEmbeddedWorkflows(local []matSkill) []matSkill {
	have := map[string]bool{}
	for _, s := range local {
		have[s.name] = true
	}
	out := append([]matSkill{}, local...)
	for _, s := range embeddedWorkflows() {
		if !have[s.name] {
			out = append(out, s)
		}
	}
	return out
}
