package document

import (
	"context"
	"errors"
	"time"
)

// SeedSystem upserts a system-scoped document idempotently. If a
// document at (scope=system, name) already exists and its latest active
// version's body matches the supplied body byte-for-byte, no new version
// is written. Otherwise a new version is appended via the internal-seed
// write path (which bypasses ErrScopeReadonly).
//
// Boot wires every embedded system artifact (install schema, MCP load
// context, system_variables doc, …) through this function so a binary
// upgrade that changes a markdown source materialises as a new version
// row, and an unchanged binary is a no-op.
func SeedSystem(ctx context.Context, s *Store, name, body, createdBy string, now time.Time) error {
	key := Key{Scope: ScopeSystem, Name: name}
	res, err := s.Get(ctx, key, GetOptions{})
	if err == nil && len(res.Versions) == 1 && res.Versions[0].Body == body {
		return nil
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	_, _, err = s.Upsert(ctx, UpsertInput{
		Key:             key,
		Body:            body,
		CreatedBy:       createdBy,
		ViaInternalSeed: true,
	}, now)
	return err
}
