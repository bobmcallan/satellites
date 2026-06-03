// `satellites install` — place the client binary, the shape of `claude install`
// (sty_a62ba0c7). `install` *places* a binary; `update` *refreshes* the one
// that is already there — same release-channel + sha-verify core, different
// target resolution. `--global` (default) installs the shared binary on PATH at
// ~/.local/bin/satellites; `--local` installs the per-repo dev binary at the
// repo's .satellites/satellites. Neither touches anything else under a repo's
// .satellites/ (it is per-repo working state, not the executable).

package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/bobmcallan/satellites/internal/cliconfig"
	"github.com/spf13/cobra"
)

func init() {
	var (
		global    bool
		local     bool
		version   string
		force     bool
		configArg string
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the client binary (global on PATH by default, or --local in-repo)",
		Long: `install downloads the published release artifact, verifies its sha256, and
places the satellites binary.

--global (default) installs to ~/.local/bin/satellites — the shared binary on
PATH, the claude model. --local installs to the repo's .satellites/satellites —
the dev path. --version <tag> pins a release; omitted installs the latest.

install refuses to overwrite a binary of a different version without --force; a
matching version is a no-op. It never touches a repo's .satellites/ beyond the
binary itself — toml, logs, worktree, and work state are preserved.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			if local && cmd.Flags().Changed("global") && global {
				return fmt.Errorf("install: --global and --local are mutually exclusive")
			}
			return installRunE(ctx, cmd.OutOrStdout(), installFlags{
				Local:     local,
				Version:   strings.TrimSpace(version),
				Force:     force,
				ConfigArg: configArg,
			}, githubReleaseSource{})
		},
	}
	cmd.Flags().BoolVar(&global, "global", true, "Install the shared binary to ~/.local/bin/satellites (on PATH).")
	cmd.Flags().BoolVar(&local, "local", false, "Install the per-repo dev binary to the repo's .satellites/satellites.")
	cmd.Flags().StringVar(&version, "version", "", "Release tag to install (e.g. v0.0.121). Omitted installs the latest.")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite an existing binary of a different version.")
	cmd.Flags().StringVar(&configArg, "config", "", "Path to satellites.toml (used to resolve the repo for --local).")
	register(cmd)
}

type installFlags struct {
	Local     bool
	Version   string
	Force     bool
	ConfigArg string
}

// installRunE resolves the target path + release tag, then delegates to the
// testable runInstall core.
func installRunE(ctx context.Context, out io.Writer, f installFlags, src releaseSource) error {
	target, err := installTarget(f.Local, f.ConfigArg, userHome())
	if err != nil {
		return err
	}

	tag := f.Version
	if tag == "" {
		latest, _, lerr := src.Latest(ctx)
		if lerr != nil {
			return fmt.Errorf("install: query latest release: %w", lerr)
		}
		tag = strings.TrimSpace(latest)
	}
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}

	name := assetName(tag)
	binURL, shaURL := releaseAssetURLs(tag, name)

	plan := installPlan{
		TargetPath:      target,
		Tag:             tag,
		AssetURL:        binURL,
		ShaURL:          shaURL,
		ExistingVersion: installedVersionAt(target),
		Force:           f.Force,
		OnPath:          dirOnPath(filepath.Dir(target), envPathDirs()),
		Global:          !f.Local,
	}
	return runInstall(ctx, out, plan)
}

type installPlan struct {
	TargetPath      string
	Tag             string // resolved release being installed, e.g. v0.0.121
	AssetURL        string
	ShaURL          string
	ExistingVersion string // version of the binary currently at TargetPath ("" if none)
	Force           bool
	OnPath          bool // whether TargetPath's dir is on PATH
	Global          bool
}

// runInstall is the testable core: clobber policy, then download + sha-verify +
// atomic place. Intact-on-failure — a mismatch or download error leaves any
// existing binary untouched and writes no partial file.
func runInstall(ctx context.Context, out io.Writer, p installPlan) error {
	if p.ExistingVersion != "" {
		if p.ExistingVersion == p.Tag {
			fmt.Fprintf(out, "already installed: %s at %s\n", p.Tag, p.TargetPath)
			return nil
		}
		if !p.Force {
			return fmt.Errorf("install: %s already holds %s; refusing to overwrite with %s without --force", p.TargetPath, p.ExistingVersion, p.Tag)
		}
	}

	dir := filepath.Dir(p.TargetPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("install: create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".satellites-install-*")
	if err != nil {
		return fmt.Errorf("install: create temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()

	got, err := downloadTo(ctx, tmp, p.AssetURL)
	if err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("install: download %s: %w", p.Tag, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("install: finalize download: %w", err)
	}

	want, err := fetchChecksum(ctx, p.ShaURL)
	if err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("install: fetch checksum: %w", err)
	}
	if !strings.EqualFold(got, want) {
		os.Remove(tmpPath)
		return fmt.Errorf("install: checksum mismatch (got %s, want %s) — nothing installed", got, want)
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("install: chmod: %w", err)
	}
	if err := os.Rename(tmpPath, p.TargetPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("install: place binary at %s: %w", p.TargetPath, err)
	}

	fmt.Fprintf(out, "installed %s → %s\n", p.Tag, p.TargetPath)
	if p.Global && !p.OnPath {
		fmt.Fprintf(out, "note: %s is not on PATH. Add it, e.g.:\n  export PATH=\"%s:$PATH\"\n", dir, dir)
	}
	return nil
}

// installTarget resolves where the binary goes: the repo's .satellites/satellites
// for --local (via config walk-up), else the global ~/.local/bin/satellites.
func installTarget(local bool, configArg, home string) (string, error) {
	if local {
		if _, path, err := cliconfig.Load(configArg); err == nil && path != "" {
			return filepath.Join(filepath.Dir(path), "satellites"), nil
		}
		// No resolvable repo config — fall back to ./.satellites/satellites.
		return filepath.Join(".satellites", "satellites"), nil
	}
	if home == "" {
		return "", fmt.Errorf("install: cannot resolve home directory for --global; use --local or set HOME")
	}
	return globalBinaryPath(home), nil
}

// globalBinaryPath is the canonical global install location — the shared binary
// on PATH (the claude model). Shared with the update self-heal (sty_7d469fb6).
func globalBinaryPath(home string) string {
	return filepath.Join(home, ".local", "bin", "satellites")
}

// assetName is the release artifact name for a tag on this os/arch.
func assetName(tag string) string {
	return fmt.Sprintf("satellites-%s-%s-%s", tag, runtime.GOOS, runtime.GOARCH)
}

// releaseAssetURLs builds the canonical GitHub release download URLs for an
// asset — usable for any tag (latest or pinned), no API call needed.
func releaseAssetURLs(tag, name string) (binURL, shaURL string) {
	base := "https://github.com/" + releaseRepo + "/releases/download/" + tag + "/" + name
	return base, base + ".sha256"
}

// installedVersionAt returns the version string of the binary at path by
// executing `<path> version`, or "" if absent/unreadable. Used for the
// no-op / refuse-clobber policy.
func installedVersionAt(path string) string {
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	out, err := exec.Command(path, "version").Output()
	if err != nil {
		return ""
	}
	// "satellites v0.0.121 (commit …, built …)" → "v0.0.121"
	fields := strings.Fields(string(out))
	if len(fields) >= 2 && strings.HasPrefix(fields[1], "v") {
		return fields[1]
	}
	return ""
}
