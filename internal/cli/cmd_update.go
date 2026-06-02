// `satellites update` — self-update the client binary, the shape of
// `claude update`. It discovers the latest GitHub release, verifies the
// published sha256, and atomically replaces the running binary in place.
// Per document:project/client-command-surface, this targets the global
// binary (os.Executable()). No-op when already current; on any failure
// the existing binary is left intact and the command exits non-zero.

package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/spf13/cobra"
)

const releaseRepo = "bobmcallan/satellites"

var updateHTTPClient = &http.Client{Timeout: 120 * time.Second}

// releaseSource discovers the latest release tag + its asset download
// URLs. Abstracted so the update flow is unit-testable without GitHub.
type releaseSource interface {
	// Latest returns the release tag (e.g. "v0.0.108") and a map of
	// asset-name → download URL.
	Latest(ctx context.Context) (tag string, assets map[string]string, err error)
}

func init() {
	var checkOnly bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Self-update the satellites binary to the latest release",
		Long: `update checks the release channel for a newer satellites build, verifies
its published sha256, and atomically replaces the running binary in place.
No-op when already current. With --check it only reports current vs latest.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("update: locate running binary: %w", err)
			}
			if resolved, rErr := filepath.EvalSymlinks(exe); rErr == nil {
				exe = resolved
			}
			return runUpdate(ctx, cmd.OutOrStdout(), exe, currentCLIVersion(), githubReleaseSource{}, checkOnly)
		},
	}
	cmd.Flags().BoolVar(&checkOnly, "check", false, "Report current vs latest without downloading or replacing.")
	register(cmd)
}

// currentCLIVersion reads the running binary's version through the same
// single source as `satellites version` (the version verb).
func currentCLIVersion() string {
	resp, err := verb.Dispatch(context.Background(), "version", nil)
	if err != nil {
		return verb.Version
	}
	var info verb.VersionInfo
	if err := json.Unmarshal(resp, &info); err != nil {
		return verb.Version
	}
	return info.Version
}

// runUpdate is the testable core: given the target path, the current
// version, and a release source, it reports and (unless checkOnly)
// downloads+verifies+atomically-replaces. Intact-on-failure.
func runUpdate(ctx context.Context, out io.Writer, exePath, currentVer string, src releaseSource, checkOnly bool) error {
	tag, assets, err := src.Latest(ctx)
	if err != nil {
		return fmt.Errorf("update: query latest release: %w", err)
	}
	latest := strings.TrimSpace(tag)
	cur := strings.TrimSpace(currentVer)
	fmt.Fprintf(out, "current: %s   latest: %s\n", displayVer(cur), latest)

	if !isNewer(cur, latest) {
		fmt.Fprintf(out, "already up to date (%s)\n", displayVer(cur))
		return nil
	}
	if checkOnly {
		fmt.Fprintf(out, "update available: %s → %s (run `satellites update`)\n", displayVer(cur), latest)
		return nil
	}

	name := fmt.Sprintf("satellites-%s-%s-%s", latest, runtime.GOOS, runtime.GOARCH)
	binURL, ok := assets[name]
	if !ok {
		return fmt.Errorf("update: release %s has no asset %q (os/arch unsupported?)", latest, name)
	}
	shaURL, ok := assets[name+".sha256"]
	if !ok {
		return fmt.Errorf("update: release %s missing checksum asset %q", latest, name+".sha256")
	}

	dir := filepath.Dir(exePath)
	tmp, err := os.CreateTemp(dir, ".satellites-update-*")
	if err != nil {
		return fmt.Errorf("update: create temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()

	got, err := downloadTo(ctx, tmp, binURL)
	if err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("update: download %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("update: finalize download: %w", err)
	}

	want, err := fetchChecksum(ctx, shaURL)
	if err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("update: fetch checksum: %w", err)
	}
	if !strings.EqualFold(got, want) {
		os.Remove(tmpPath)
		return fmt.Errorf("update: checksum mismatch (got %s, want %s) — binary left unchanged", got, want)
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("update: chmod: %w", err)
	}
	if err := os.Rename(tmpPath, exePath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("update: replace binary: %w", err)
	}
	fmt.Fprintf(out, "updated %s → %s\n", displayVer(cur), latest)
	return nil
}

// downloadTo streams url into w, returning the content's sha256 hex.
func downloadTo(ctx context.Context, w io.Writer, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := updateHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(w, h), resp.Body); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// fetchChecksum fetches a sha256sum-format file and returns the first
// token (the hex digest), lowercased.
func fetchChecksum(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := updateHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return "", fmt.Errorf("empty checksum file")
	}
	return strings.ToLower(fields[0]), nil
}

func displayVer(v string) string {
	if v == "" || v == "dev" {
		return "dev"
	}
	return v
}

// isNewer reports whether latest > cur. An unparseable latest is never
// newer (don't chase a malformed tag); an unparseable current (e.g. a
// "dev" build) is always older than a parseable latest, so an explicit
// `update` proceeds.
func isNewer(cur, latest string) bool {
	lv, lok := parseVer(latest)
	if !lok {
		return false
	}
	cv, cok := parseVer(cur)
	if !cok {
		return true
	}
	for i := 0; i < 3; i++ {
		if lv[i] != cv[i] {
			return lv[i] > cv[i]
		}
	}
	return false
}

func parseVer(s string) ([3]int, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// githubReleaseSource is the production releaseSource — GitHub's
// "latest release" REST endpoint for the satellites repo.
type githubReleaseSource struct{}

func (githubReleaseSource) Latest(ctx context.Context) (string, map[string]string, error) {
	url := "https://api.github.com/repos/" + releaseRepo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := updateHTTPClient.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	var rel struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", nil, err
	}
	assets := make(map[string]string, len(rel.Assets))
	for _, a := range rel.Assets {
		assets[a.Name] = a.URL
	}
	return rel.TagName, assets, nil
}
