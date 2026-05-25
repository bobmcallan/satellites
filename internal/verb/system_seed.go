// system_seed verbs — read-only surface onto the system_seeds registry.
//
// Only system_seeds_list is registered. There is no upsert/delete verb
// by design: system seeds are mutated exclusively by the boot
// reconciler running against the embed.FS bytes baked into the binary,
// so the table content is pinned to the release the operator deployed.
// Treat any future temptation to expose a mutation verb as the same
// "which prompt is running where" drift class the embed.FS pin exists
// to prevent.

package verb

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bobmcallan/satellites/internal/document"
)

var systemSeedStore *document.SystemSeedStore

// SetSystemSeedStore wires the server's system-seed store into the verb
// package. Called from cmd/satellites-server on boot, after the boot
// reconciler has applied the embedded artifacts.
func SetSystemSeedStore(s *document.SystemSeedStore) { systemSeedStore = s }

// SystemSeedsListResponse returns every row in the registry, ordered by
// name. Operators reach this verb to audit "what's actually baked into
// this server right now" — useful when diagnosing whether a prompt
// edit has shipped.
type SystemSeedsListResponse struct {
	Seeds []document.SystemSeed `json:"seeds"`
}

func init() {
	Register(&Verb{
		Name:        "system_seeds_list",
		Description: "List embedded system seeds (audit registry; no mutation verbs exist by design).",
		Invoke:      invokeSystemSeedsList,
	})
}

func invokeSystemSeedsList(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	if systemSeedStore == nil {
		return nil, fmt.Errorf("system_seeds_list: store not configured")
	}
	seeds, err := systemSeedStore.List(ctx)
	if err != nil {
		return nil, err
	}
	if seeds == nil {
		seeds = []document.SystemSeed{}
	}
	return json.Marshal(SystemSeedsListResponse{Seeds: seeds})
}
