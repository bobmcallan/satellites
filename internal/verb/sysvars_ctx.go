package verb

import (
	"context"
	"runtime"
)

// Per-request system-variable context.
//
// document_get / variable_get accept os, arch, and current_version on
// their request body so the caller's session attributes survive into
// the system-variables resolver. We stamp them onto ctx instead of
// passing them through the resolver signature directly — keeps the
// SystemVariableResolver type narrow (one name in, value out) and lets
// downstream resolvers compose without re-plumbing arguments.

type sysVarKey int

const (
	sysVarKeyOS sysVarKey = iota
	sysVarKeyArch
	sysVarKeyCurrentVersion
)

// WithSystemVarContext returns a derived ctx carrying the per-request
// system-variable inputs. Empty strings are treated as "not supplied"
// — the lookup helpers fall back to runtime values.
func WithSystemVarContext(ctx context.Context, os, arch, currentVersion string) context.Context {
	if os != "" {
		ctx = context.WithValue(ctx, sysVarKeyOS, os)
	}
	if arch != "" {
		ctx = context.WithValue(ctx, sysVarKeyArch, arch)
	}
	if currentVersion != "" {
		ctx = context.WithValue(ctx, sysVarKeyCurrentVersion, currentVersion)
	}
	return ctx
}

// OSFromContext returns the caller-supplied os, falling back to the
// server's runtime.GOOS. Reasonable default: a CLI install session
// asking the install schema for a download URL will usually be on the
// same OS as the server in dev; in prod, MCP clients always populate
// the field.
func OSFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(sysVarKeyOS).(string); ok {
		return v
	}
	return runtime.GOOS
}

// ArchFromContext mirrors OSFromContext.
func ArchFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(sysVarKeyArch).(string); ok {
		return v
	}
	return runtime.GOARCH
}

// CurrentVersionFromContext returns the caller's currently-installed
// CLI version (empty when the caller has nothing installed). The
// `state` system variable is derived from this against
// CLIVersionEffective.
func CurrentVersionFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(sysVarKeyCurrentVersion).(string); ok {
		return v
	}
	return ""
}

// ComputeInstallState is the install-state rule: empty
// current_version → install_required; matching → up_to_date; otherwise
// update_available. Exposed for the system-variables resolver so the
// `{{state}}` placeholder renders coherently from any caller surface.
func ComputeInstallState(currentVersion, serverCLIVersion string) string {
	switch {
	case currentVersion == "":
		return "install_required"
	case currentVersion == serverCLIVersion:
		return "up_to_date"
	default:
		return "update_available"
	}
}
