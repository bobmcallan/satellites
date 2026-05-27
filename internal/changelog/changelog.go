// Package changelog models release-note entries written via the
// changelog_add verb and rendered on /changelog. One row per release
// of one service; ordering is (effective_date DESC NULLS LAST,
// created_at DESC, id) so backfill entries without an effective_date
// still surface.
//
// The verb layer is the only writer that stamps created_by — the
// store accepts whatever the verb passes, so callers in tests / CLI
// can supply an empty string. Server-side verbs derive created_by
// from the authed identity and never trust the request body.
package changelog

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned when a lookup misses.
var ErrNotFound = errors.New("changelog: not found")

// Entry is the persisted row shape.
type Entry struct {
	ID            string     `json:"id"`
	Service       string     `json:"service"`
	VersionFrom   string     `json:"version_from"`
	VersionTo     string     `json:"version_to"`
	Content       string     `json:"content"`
	CreatedBy     string     `json:"created_by,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	EffectiveDate *time.Time `json:"effective_date,omitempty"`
}

// NewID mints a fresh changelog id (`cl_<8hex>`).
func NewID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read failure on Linux is fatal-on-boot territory; surface
		// via panic rather than return a degraded id.
		panic(fmt.Errorf("changelog: rand.Read: %w", err))
	}
	return "cl_" + hex.EncodeToString(b[:])
}
