package agentprocess

import (
	"context"
	"fmt"
	"time"

	"github.com/bobmcallan/satellites/internal/document"
)

// SeedSystemDefault creates (idempotently) the system-scope
// `default_agent_process` artifact carrying SystemDefaultBody. Re-seed
// leaves an existing row untouched — the user's edits to the seed
// survive deploys.
//
// docStore.Create + GetByName both use the SystemDefaultName lookup;
// any active row with that name + scope=system is treated as
// already-seeded.
func SeedSystemDefault(ctx context.Context, docStore document.Store, workspaceID string, now time.Time) error {
	if docStore == nil {
		return nil
	}
	if existing, err := docStore.GetByName(ctx, "", SystemDefaultName, nil); err == nil && existing.Status == document.StatusActive {
		return nil
	}
	if _, err := docStore.Create(ctx, document.Document{
		WorkspaceID: workspaceID,
		ProjectID:   nil,
		Type:        document.TypeArtifact,
		Name:        SystemDefaultName,
		Scope:       document.ScopeSystem,
		Status:      document.StatusActive,
		Body:        SystemDefaultBody,
		Tags:        []string{KindTag, "v4", "seed"},
		CreatedBy:   "system",
	}, now); err != nil {
		return fmt.Errorf("seed default_agent_process: %w", err)
	}
	return nil
}
