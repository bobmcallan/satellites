package project

import (
	"context"
	"time"

	"github.com/ternarybob/arbor"
)

// DefaultOwnerUserID is the owner stamp on the system-seeded default
// project. It is a synthetic literal, not a real user id; stories
// intentionally distinct from it own their own projects.
const DefaultOwnerUserID = "system"

// DefaultProjectName is the display name of the seeded default project.
const DefaultProjectName = "Satellites v4"

// SeedDefault returns the id of the default project, creating it if it
// doesn't already exist. Idempotent: running twice returns the same id.
// The default project is owned by DefaultOwnerUserID ("system") and
// scoped to workspaceID when supplied. Backstops document_ingest_file /
// document_get callers that don't supply their own project_id.
func SeedDefault(ctx context.Context, store Store, logger arbor.ILogger, workspaceID string) (string, error) {
	existing, err := store.ListByOwner(ctx, DefaultOwnerUserID, nil)
	if err != nil {
		return "", err
	}
	for _, p := range existing {
		if p.Name == DefaultProjectName {
			logger.Info().Str("project_id", p.ID).Msg("default project already seeded")
			return p.ID, nil
		}
	}
	p, err := store.Create(ctx, DefaultOwnerUserID, workspaceID, DefaultProjectName, time.Now().UTC())
	if err != nil {
		return "", err
	}
	logger.Info().Str("project_id", p.ID).Str("workspace_id", workspaceID).Msg("default project seeded")
	return p.ID, nil
}

// PerUserDefaultName is the display name of the per-user default project
// minted on first login. Distinct from DefaultProjectName (the system
// seed) so a single workspace store can hold both rows without name
// collision.
const PerUserDefaultName = "Default"

// EnsureDefault returns the id of ownerUserID's default project in
// workspaceID, creating it on first sight. Idempotent: a second call
// finds the existing row by (owner, workspace, name) and returns its id
// without creating a duplicate. Wired into the auth OnUserCreated hook
// so every fresh user lands on /projects with a visible project rather
// than the empty-state panel (story_0f415ab3).
func EnsureDefault(ctx context.Context, store Store, logger arbor.ILogger, ownerUserID, workspaceID string, now time.Time) (string, error) {
	existing, err := store.ListByOwner(ctx, ownerUserID, nil)
	if err != nil {
		return "", err
	}
	for _, p := range existing {
		if p.Name == PerUserDefaultName && p.WorkspaceID == workspaceID {
			logger.Info().Str("project_id", p.ID).Str("user_id", ownerUserID).Msg("per-user default project already seeded")
			return p.ID, nil
		}
	}
	p, err := store.Create(ctx, ownerUserID, workspaceID, PerUserDefaultName, now)
	if err != nil {
		return "", err
	}
	logger.Info().Str("project_id", p.ID).Str("user_id", ownerUserID).Str("workspace_id", workspaceID).Msg("per-user default project seeded")
	return p.ID, nil
}
