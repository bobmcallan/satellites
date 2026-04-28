package ledger

import "context"

// impersonationCtxKey is the unexported context key carrying the
// impersonated workspace_id for downstream Append calls. story_3548cde2.
type impersonationCtxKey struct{}

// WithImpersonatingWorkspace returns a derived ctx that carries
// workspaceID for impersonation-audit. When workspaceID is empty the
// returned ctx is the input ctx unchanged. Callers should set this at
// their handler boundary when a global_admin operates on a workspace
// they are not a member of; subsequent ledger.Append calls in that ctx
// will stamp the field on every row that doesn't already carry one.
func WithImpersonatingWorkspace(ctx context.Context, workspaceID string) context.Context {
	if workspaceID == "" {
		return ctx
	}
	return context.WithValue(ctx, impersonationCtxKey{}, workspaceID)
}

// ImpersonatingWorkspaceFromCtx returns the impersonated workspace_id
// stamped on ctx, or empty when the ctx carries no impersonation state.
// Read by Append before persisting an entry whose
// ImpersonatingAsWorkspace is empty.
func ImpersonatingWorkspaceFromCtx(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(impersonationCtxKey{}).(string)
	return v
}

// stampImpersonationFromCtx mutates entry.ImpersonatingAsWorkspace from
// ctx when the entry's field is empty. Used by Store.Append
// implementations to ensure the audit field is set even when callers
// don't populate the entry directly.
func stampImpersonationFromCtx(ctx context.Context, entry *LedgerEntry) {
	if entry == nil || entry.ImpersonatingAsWorkspace != "" {
		return
	}
	if ws := ImpersonatingWorkspaceFromCtx(ctx); ws != "" {
		entry.ImpersonatingAsWorkspace = ws
	}
}
