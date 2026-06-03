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
			out := cmd.OutOrStdout()
			if err := runUpdate(ctx, out, exe, currentCLIVersion(), githubReleaseSource{}, checkOnly); err != nil {
				return err
			}
			// Self-heal the install so `satellites` resolves on PATH with
			// no manual symlink surgery — but only on a real update, not a
			// report-only --check (sty_f651aad9).
			if !checkOnly {
				healInstall(out, exe, envPathDirs(), userHome())
			}
			return nil
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

	// A dev/untagged build isn't "behind" a release — it's off-channel.
	// Say so plainly rather than letting isNewer's "unparseable current
	// is always older" silently present it as a normal upgrade
	// (sty_d6c95262).
	if !verb.IsReleaseVersion(cur) {
		fmt.Fprintf(out, "note: this is a dev/untagged build, not a tagged release — installing latest release %s\n", latest)
	}

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

// canonicalBinary is the installed client's command name; legacyBinaries
// are prior names a rename may have left behind on PATH as stale links.
const canonicalBinary = "satellites"

var legacyBinaries = []string{"satellites-client"}

// healInstall runs reconcileInstall and reports what it did + warns when
// the running binary looks like a source/dev build (sty_f651aad9).
func healInstall(out io.Writer, exePath string, pathDirs []string, home string) {
	for _, a := range reconcileInstall(exePath, pathDirs, home) {
		fmt.Fprintf(out, "install: %s\n", a)
	}
	// The binary file itself sitting outside any PATH dir means the
	// running binary is a source/dev-checkout build (e.g. a gitignored
	// repo bin/), not the installed client — updating it replaces a file
	// in the source tree. Warn regardless of any link we just made.
	if !dirOnPath(filepath.Dir(exePath), pathDirs) {
		fmt.Fprintf(out, "install: note: %s is not on PATH — this looks like a source/dev build, not the installed client\n", exePath)
	}
}

// reconcileInstall repairs the canonical client install so `satellites`
// resolves on PATH after an update, with no manual symlink surgery
// (sty_f651aad9). It removes a stale legacy-named symlink that a binary
// rename left dangling or pointing elsewhere, then ensures a
// `satellites` entry on PATH points at the running binary. It only ever
// removes symlinks — never a real file — and returns the actions taken.
func reconcileInstall(exePath string, pathDirs []string, home string) []string {
	var actions []string

	dirs := dedupeDirs(pathDirs, home)

	// 1. Remove stale legacy-named symlinks (e.g. a prior satellites-client
	//    left dangling, or now pointing at a different binary).
	for _, d := range dirs {
		for _, name := range legacyBinaries {
			p := filepath.Join(d, name)
			fi, err := os.Lstat(p)
			if err != nil || fi.Mode()&os.ModeSymlink == 0 {
				continue // absent, or a real file — never our business to delete
			}
			target, _ := os.Readlink(p)
			abs := target
			if !filepath.IsAbs(abs) {
				abs = filepath.Join(d, target)
			}
			if _, statErr := os.Stat(abs); statErr != nil || abs != exePath {
				if os.Remove(p) == nil {
					actions = append(actions, "removed stale symlink "+p)
				}
			}
		}
	}

	// 2. Ensure `satellites` already resolves on PATH to exePath.
	if resolvesOnPath(exePath, pathDirs) {
		return actions
	}

	// 3. Install a `satellites` link in a PATH dir, preferring ~/.local/bin.
	installDir := preferredInstallDir(pathDirs, home)
	if installDir == "" {
		return actions
	}
	link := filepath.Join(installDir, canonicalBinary)
	_ = os.Remove(link) // replace whatever stale entry is there
	if err := os.Symlink(exePath, link); err == nil {
		actions = append(actions, "linked "+link+" -> "+exePath)
	}
	return actions
}

// resolvesOnPath reports whether a `satellites` entry on PATH resolves
// to exePath (i.e. the canonical command already points at this binary).
func resolvesOnPath(exePath string, pathDirs []string) bool {
	for _, d := range pathDirs {
		p := filepath.Join(d, canonicalBinary)
		if resolved, err := filepath.EvalSymlinks(p); err == nil && resolved == exePath {
			return true
		}
	}
	return false
}

func dirOnPath(dir string, pathDirs []string) bool {
	for _, d := range pathDirs {
		if d == dir {
			return true
		}
	}
	return false
}

// preferredInstallDir picks where to place the `satellites` link:
// ~/.local/bin when it's on PATH, else the first PATH entry.
func preferredInstallDir(pathDirs []string, home string) string {
	if home != "" {
		localBin := filepath.Join(home, ".local", "bin")
		if dirOnPath(localBin, pathDirs) {
			return localBin
		}
	}
	if len(pathDirs) > 0 {
		return pathDirs[0]
	}
	return ""
}

// dedupeDirs returns the PATH dirs plus ~/.local/bin, de-duplicated and
// order-preserving, for the legacy-symlink sweep.
func dedupeDirs(pathDirs []string, home string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(d string) {
		if d == "" || seen[d] {
			return
		}
		seen[d] = true
		out = append(out, d)
	}
	for _, d := range pathDirs {
		add(d)
	}
	if home != "" {
		add(filepath.Join(home, ".local", "bin"))
	}
	return out
}

func envPathDirs() []string {
	raw := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))
	out := make([]string, 0, len(raw))
	for _, d := range raw {
		if d != "" {
			out = append(out, d)
		}
	}
	return out
}

func userHome() string {
	h, _ := os.UserHomeDir()
	return h
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
