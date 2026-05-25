// `satellites seed push` — walk .satellites/seeds/ from the current
// working directory and dispatch each markdown file via the seed_apply
// verb chosen by path depth.
//
// Layout (the only shape this command understands today):
//
//   .satellites/seeds/<workspace_id>/workspace.md           → workspace_seed_apply
//   .satellites/seeds/<workspace_id>/<project_id>/project.md → project_seed_apply
//
// Idempotency is delegated to the substrate: every file is dispatched on
// every push, and the server short-circuits when the incoming body
// matches the stored seed_md byte-for-byte. The operator sees one of:
//
//   • "no change"  — body matched, zero writes server-side
//   • "applied"    — body differed, seed_md + seed_updated_at advanced
//
// --dry-run prints the same plan without dispatching, so operators can
// confirm what a push would touch before committing to the round-trip.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/spf13/cobra"
)

// seedsRoot is the conventional location, relative to CWD. Hard-coded
// because the convention IS the contract; the CLI does not let
// operators retarget it (otherwise repo-level seed governance can't
// rely on the path).
const seedsRoot = ".satellites/seeds"

// seedTarget describes one .md file scheduled for a verb dispatch.
type seedTarget struct {
	Path    string // path relative to CWD (printable)
	Kind    string // "workspace" | "project"
	VerbReq json.RawMessage
}

func init() {
	var (
		configArg string
		userArg   string
		dryRun    bool
	)
	seed := &cobra.Command{
		Use:   "seed",
		Short: "Push file-based seeds to the substrate",
	}
	seed.PersistentFlags().StringVar(&configArg, "config", "", "Path to satellites.toml (overrides $SATELLITES_CONFIG / .satellites/satellites.toml walk-up).")
	seed.PersistentFlags().StringVar(&userArg, "user", "", "Caller user id (overrides $SATELLITES_USER_ID). Stamped onto verbs when dispatching in-process.")

	push := &cobra.Command{
		Use:   "push",
		Short: "Walk .satellites/seeds/ and apply each file via the matching seed verb",
		RunE: func(cmd *cobra.Command, args []string) error {
			targets, err := planSeedPush(seedsRoot)
			if err != nil {
				return err
			}
			if len(targets) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no seeds found under .satellites/seeds/ — nothing to push")
				return nil
			}
			out := cmd.OutOrStdout()
			for _, t := range targets {
				if dryRun {
					fmt.Fprintf(out, "[dry-run] %s → %s_seed_apply\n", t.Path, t.Kind)
					continue
				}
				verbName := t.Kind + "_seed_apply"
				resp, err := dispatchVerb(context.Background(), verbName, t.VerbReq, configArg, userArg)
				if err != nil {
					return fmt.Errorf("%s: %w", t.Path, err)
				}
				summary, parseErr := summariseSeedResp(resp)
				if parseErr != nil {
					fmt.Fprintf(out, "%s → %s\n", t.Path, string(resp))
					continue
				}
				fmt.Fprintf(out, "%s → %s\n", t.Path, summary)
			}
			return nil
		},
	}
	push.Flags().BoolVar(&dryRun, "dry-run", false, "Print the planned dispatches without calling the verbs")
	seed.AddCommand(push)
	register(seed)
}

// planSeedPush walks rootDir and returns the ordered list of seed
// dispatches. Workspace files are emitted before project files so a
// workspace seed lands before the project seeds underneath it (matters
// for any future cross-row validation; harmless today).
//
// Unknown paths under rootDir (anything that doesn't match the two
// supported shapes) are skipped silently — the convention is the
// contract, but adding new seed kinds later shouldn't break existing
// pushes.
func planSeedPush(rootDir string) ([]seedTarget, error) {
	info, err := os.Stat(rootDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("seed push: stat %s: %w", rootDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("seed push: %s is not a directory", rootDir)
	}

	var workspaces, projects []seedTarget
	err = filepath.WalkDir(rootDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(rootDir, p)
		if relErr != nil {
			return relErr
		}
		// Normalise to forward slashes so the depth math is OS-independent.
		parts := strings.Split(filepath.ToSlash(rel), "/")
		switch {
		case len(parts) == 2 && parts[1] == "workspace.md":
			body, readErr := os.ReadFile(p)
			if readErr != nil {
				return fmt.Errorf("read %s: %w", p, readErr)
			}
			req, marshalErr := json.Marshal(verb.WorkspaceSeedApplyRequest{
				WorkspaceID: parts[0],
				Body:        string(body),
			})
			if marshalErr != nil {
				return marshalErr
			}
			workspaces = append(workspaces, seedTarget{Path: p, Kind: "workspace", VerbReq: req})
		case len(parts) == 3 && parts[2] == "project.md":
			body, readErr := os.ReadFile(p)
			if readErr != nil {
				return fmt.Errorf("read %s: %w", p, readErr)
			}
			req, marshalErr := json.Marshal(verb.ProjectSeedApplyRequest{
				ProjectID: parts[1],
				Body:      string(body),
			})
			if marshalErr != nil {
				return marshalErr
			}
			projects = append(projects, seedTarget{Path: p, Kind: "project", VerbReq: req})
		default:
			// Unknown shape — skip. Surface to the operator only at
			// --verbose levels (not implemented here) so a stray README
			// doesn't drown out useful output.
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(workspaces, func(i, j int) bool { return workspaces[i].Path < workspaces[j].Path })
	sort.Slice(projects, func(i, j int) bool { return projects[i].Path < projects[j].Path })
	return append(workspaces, projects...), nil
}

// summariseSeedResp pulls the human-readable bit out of a seed_apply
// response. We don't unmarshal into the typed struct — the response
// shape varies slightly between workspace and project verbs and we only
// need the "applied" flag.
func summariseSeedResp(resp json.RawMessage) (string, error) {
	var generic struct {
		Applied bool   `json:"applied"`
		Reason  string `json:"reason,omitempty"`
	}
	if err := json.Unmarshal(resp, &generic); err != nil {
		return "", err
	}
	if generic.Applied {
		return "applied", nil
	}
	if generic.Reason != "" {
		return generic.Reason, nil
	}
	return "no change", nil
}
