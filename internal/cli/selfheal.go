package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/bobmcallan/satellites/internal/verb"
)

// selfHealDisableEnv lets ops opt out of project_id self-heal when the
// strict "project_id not defined" error is the desired behaviour
// (CI guards, smoke tests).
const selfHealDisableEnv = "SATELLITES_NO_AUTO_PROJECT_ID"

// selfHealStderr is the destination for self-heal log lines. Tests
// swap this for a buffer; real callers leave it on os.Stderr.
var selfHealStderr io.Writer = os.Stderr

// ErrSelfHealDisabled is returned when SATELLITES_NO_AUTO_PROJECT_ID
// is set. Callers surface the original "project_id not defined" error.
var ErrSelfHealDisabled = errors.New("self-heal: disabled by env")

// ErrSelfHealNonGit is returned when the configured repo_path is not a
// git repository (or lacks an `origin` remote).
var ErrSelfHealNonGit = errors.New("self-heal: repo_path is not a git repo with an origin remote")

// ErrSelfHealNotFound is returned when project_match cannot resolve
// the git remote to a known project.
var ErrSelfHealNotFound = errors.New("self-heal: project_match returned not_found")

// selfHealProjectID attempts to populate a missing project_id from the
// consumer repo's git remote. On success it returns the resolved id
// AND writes it to the TOML at configPath (when configPath != ""),
// logging a single line to stderr describing the action.
//
// On any failure mode (env opt-out, non-git, project_match not_found,
// persist failure) it returns "" + a typed error. Callers surface
// their original "project_id not defined" error in those cases — the
// self-heal never substitutes itself for the operator's failure mode.
func selfHealProjectID(ctx context.Context, cfg cliconfig.Config, configPath, userArg string) (string, error) {
	if os.Getenv(selfHealDisableEnv) != "" {
		return "", ErrSelfHealDisabled
	}
	repoPath := strings.TrimSpace(cfg.RepoPath)
	if repoPath == "" {
		repoPath = "."
	}
	remote, err := readGitOriginRemote(ctx, repoPath)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrSelfHealNonGit, err)
	}
	req, err := json.Marshal(verb.ProjectMatchRequest{GitURL: remote})
	if err != nil {
		return "", err
	}
	resp, err := dispatchVerb(ctx, "project_match", req, configPath, userArg)
	if err != nil {
		if strings.Contains(err.Error(), "not_found") {
			return "", fmt.Errorf("%w: %v", ErrSelfHealNotFound, err)
		}
		return "", fmt.Errorf("self-heal: project_match: %w", err)
	}
	var match verb.ProjectMatchResponse
	if err := json.Unmarshal(resp, &match); err != nil {
		return "", fmt.Errorf("self-heal: parse project_match response: %w", err)
	}
	if strings.TrimSpace(match.ProjectID) == "" {
		return "", ErrSelfHealNotFound
	}
	if configPath != "" {
		if err := cliconfig.PersistProjectID(configPath, match.ProjectID); err != nil {
			return "", fmt.Errorf("self-heal: persist: %w", err)
		}
		fmt.Fprintf(selfHealStderr,
			"satellites: self-heal project_id=%s matched_url=%s persisted to %s\n",
			match.ProjectID, match.MatchedURL, configPath)
	} else {
		fmt.Fprintf(selfHealStderr,
			"satellites: self-heal project_id=%s matched_url=%s (no config file to persist)\n",
			match.ProjectID, match.MatchedURL)
	}
	return match.ProjectID, nil
}

// readGitOriginRemote returns the URL of `origin` for the git repo at
// repoPath. Returns an error when repoPath is not a git repo or has
// no origin remote.
func readGitOriginRemote(ctx context.Context, repoPath string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	remote := strings.TrimSpace(string(out))
	if remote == "" {
		return "", fmt.Errorf("empty origin remote")
	}
	return remote, nil
}
