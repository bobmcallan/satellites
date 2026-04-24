package document

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a document lookup misses.
var ErrNotFound = errors.New("document: not found")

// ErrImmutableField is returned when Update is asked to mutate a field
// that the schema forbids changing post-create.
var ErrImmutableField = errors.New("document: field is immutable")

// ErrDanglingBinding is returned when a write references a
// ContractBinding id that does not resolve to an active type=contract row
// inside the same workspace visibility.
var ErrDanglingBinding = errors.New("document: contract_binding does not resolve to an active type=contract document")

// UpsertResult is the outcome of Upsert. Changed==false means the body
// matched the existing hash, so version and body were left untouched.
type UpsertResult struct {
	Document Document
	Changed  bool
	Created  bool
}

// UpsertInput collects the fields Upsert needs in one struct (per
// golang-code-style: ≥4 parameters → options struct).
type UpsertInput struct {
	WorkspaceID     string
	ProjectID       *string
	Type            string
	Name            string
	Body            []byte
	Structured      []byte
	ContractBinding *string
	Scope           string
	Tags            []string
	Actor           string
}

// UpdateFields names the per-call mutable subset for Update. Nil-valued
// fields mean "leave alone"; non-nil means "set to this value". The
// caller cannot pass id, workspace_id, project_id, type, scope, or name
// — those are immutable post-create.
type UpdateFields struct {
	Body            *string
	Structured      *[]byte
	Tags            *[]string
	Status          *string
	ContractBinding *string
}

// DeleteMode discriminates Delete behaviour. DeleteArchive is the default
// and flips Status to StatusArchived; DeleteHard removes the row.
type DeleteMode string

const (
	DeleteArchive DeleteMode = "archive"
	DeleteHard    DeleteMode = "hard"
)

// ListOptions are the structured filters consumed by Store.List.
// Workspace scoping is non-negotiable and supplied via the memberships
// slice on the call itself, not through this struct.
type ListOptions struct {
	Type            string
	Scope           string
	Tags            []string
	ContractBinding string
	ProjectID       string
	Limit           int
}

// Store is the persistence surface for documents. SurrealStore is the
// production implementation; MemoryStore is the in-process test double.
//
// Workspace scoping is enforced on every read via the memberships slice
// (per docs/architecture.md §8): nil = no scoping, empty = deny-all,
// non-empty = workspace_id IN memberships.
type Store interface {
	// Upsert inserts or updates a document keyed by (project_id, name).
	// If the body hash matches the existing row, no write happens and
	// Changed=false. Used by the seed/ingest path; per-doc Create is
	// available for the explicit create surface.
	Upsert(ctx context.Context, in UpsertInput, now time.Time) (UpsertResult, error)

	// Create writes a new document. The caller supplies a fully-formed
	// Document (id may be empty — Create mints one); shape and
	// contract-binding integrity are validated before the write.
	Create(ctx context.Context, doc Document, now time.Time) (Document, error)

	// Update applies fields to the document with the given id. Immutable
	// fields cannot be set; ErrImmutableField is returned if attempted
	// at the wrapper layer.
	Update(ctx context.Context, id string, fields UpdateFields, actor string, now time.Time, memberships []string) (Document, error)

	// Delete archives or hard-deletes the document with the given id.
	// memberships scoping enforced.
	Delete(ctx context.Context, id string, mode DeleteMode, memberships []string) error

	// List returns documents matching opts. Filters compose with AND.
	List(ctx context.Context, opts ListOptions, memberships []string) ([]Document, error)

	// GetByID returns the document with the given id, or ErrNotFound.
	GetByID(ctx context.Context, id string, memberships []string) (Document, error)

	// GetByName returns the active document with the given name inside
	// projectID. Replaces the v3 GetByFilename surface.
	GetByName(ctx context.Context, projectID, name string, memberships []string) (Document, error)

	// Count returns the number of active documents in projectID. Boot
	// seeding uses this to skip work on a pre-populated project.
	Count(ctx context.Context, projectID string, memberships []string) (int, error)

	// BackfillWorkspaceID stamps workspaceID on documents with the given
	// projectID whose workspace_id is empty. Idempotent.
	BackfillWorkspaceID(ctx context.Context, projectID, workspaceID string, now time.Time) (int, error)
}

// MemoryStore is a concurrency-safe in-process Store used by unit tests.
type MemoryStore struct {
	mu   sync.Mutex
	rows map[string]Document // key = id
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{rows: make(map[string]Document)}
}

// findByName scans for the active row matching (projectID, name). Caller
// must hold m.mu.
func (m *MemoryStore) findByName(projectID, name string) (Document, bool) {
	for _, d := range m.rows {
		if d.Name != name || d.Status != StatusActive {
			continue
		}
		if d.ProjectID == nil {
			if projectID == "" {
				return d, true
			}
			continue
		}
		if *d.ProjectID == projectID {
			return d, true
		}
	}
	return Document{}, false
}

// validateBindingLocked enforces FK integrity against the in-memory rows.
// Caller must hold m.mu.
func (m *MemoryStore) validateBindingLocked(binding *string) error {
	if binding == nil || *binding == "" {
		return nil
	}
	target, ok := m.rows[*binding]
	if !ok || target.Type != TypeContract || target.Status != StatusActive {
		return ErrDanglingBinding
	}
	return nil
}

// Upsert implements Store for MemoryStore.
func (m *MemoryStore) Upsert(ctx context.Context, in UpsertInput, now time.Time) (UpsertResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	hash := HashBody(in.Body)
	projectID := ""
	if in.ProjectID != nil {
		projectID = *in.ProjectID
	}
	candidate := Document{
		WorkspaceID:     in.WorkspaceID,
		ProjectID:       in.ProjectID,
		Type:            in.Type,
		Name:            in.Name,
		Body:            string(in.Body),
		Structured:      in.Structured,
		ContractBinding: in.ContractBinding,
		Scope:           in.Scope,
		Tags:            in.Tags,
		Status:          StatusActive,
		BodyHash:        hash,
	}
	if err := candidate.Validate(); err != nil {
		return UpsertResult{}, err
	}
	if err := m.validateBindingLocked(in.ContractBinding); err != nil {
		return UpsertResult{}, err
	}
	if existing, ok := m.findByName(projectID, in.Name); ok {
		if existing.BodyHash == hash {
			return UpsertResult{Document: existing}, nil
		}
		updated := existing
		updated.Body = string(in.Body)
		updated.BodyHash = hash
		updated.Version++
		updated.UpdatedAt = now
		updated.UpdatedBy = in.Actor
		updated.Type = in.Type
		updated.Structured = in.Structured
		updated.ContractBinding = in.ContractBinding
		updated.Tags = in.Tags
		if updated.WorkspaceID == "" {
			updated.WorkspaceID = in.WorkspaceID
		}
		m.rows[updated.ID] = updated
		return UpsertResult{Document: updated, Changed: true}, nil
	}
	doc := candidate
	doc.ID = NewID()
	doc.Version = 1
	doc.CreatedAt = now
	doc.CreatedBy = in.Actor
	doc.UpdatedAt = now
	doc.UpdatedBy = in.Actor
	m.rows[doc.ID] = doc
	return UpsertResult{Document: doc, Changed: true, Created: true}, nil
}

// Create implements Store for MemoryStore.
func (m *MemoryStore) Create(ctx context.Context, doc Document, now time.Time) (Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if doc.Status == "" {
		doc.Status = StatusActive
	}
	if err := doc.Validate(); err != nil {
		return Document{}, err
	}
	if err := m.validateBindingLocked(doc.ContractBinding); err != nil {
		return Document{}, err
	}
	if doc.ID == "" {
		doc.ID = NewID()
	}
	if _, exists := m.rows[doc.ID]; exists {
		return Document{}, fmt.Errorf("document: id %q already exists", doc.ID)
	}
	doc.BodyHash = HashBody([]byte(doc.Body))
	doc.Version = 1
	doc.CreatedAt = now
	doc.UpdatedAt = now
	m.rows[doc.ID] = doc
	return doc, nil
}

// Update implements Store for MemoryStore.
func (m *MemoryStore) Update(ctx context.Context, id string, fields UpdateFields, actor string, now time.Time, memberships []string) (Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, ok := m.rows[id]
	if !ok || !inDocMemberships(doc.WorkspaceID, memberships) {
		return Document{}, ErrNotFound
	}
	if fields.Body != nil {
		doc.Body = *fields.Body
		doc.BodyHash = HashBody([]byte(doc.Body))
		doc.Version++
	}
	if fields.Structured != nil {
		doc.Structured = *fields.Structured
	}
	if fields.Tags != nil {
		doc.Tags = *fields.Tags
	}
	if fields.Status != nil {
		switch *fields.Status {
		case StatusActive, StatusArchived:
			doc.Status = *fields.Status
		default:
			return Document{}, fmt.Errorf("document: invalid status %q", *fields.Status)
		}
	}
	if fields.ContractBinding != nil {
		if err := m.validateBindingLocked(fields.ContractBinding); err != nil {
			return Document{}, err
		}
		doc.ContractBinding = fields.ContractBinding
	}
	if err := doc.Validate(); err != nil {
		return Document{}, err
	}
	doc.UpdatedAt = now
	doc.UpdatedBy = actor
	m.rows[id] = doc
	return doc, nil
}

// Delete implements Store for MemoryStore.
func (m *MemoryStore) Delete(ctx context.Context, id string, mode DeleteMode, memberships []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, ok := m.rows[id]
	if !ok || !inDocMemberships(doc.WorkspaceID, memberships) {
		return ErrNotFound
	}
	switch mode {
	case DeleteHard:
		delete(m.rows, id)
	case DeleteArchive, "":
		doc.Status = StatusArchived
		m.rows[id] = doc
	default:
		return fmt.Errorf("document: invalid delete mode %q", mode)
	}
	return nil
}

// List implements Store for MemoryStore.
func (m *MemoryStore) List(ctx context.Context, opts ListOptions, memberships []string) ([]Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Document, 0, len(m.rows))
	for _, d := range m.rows {
		if !inDocMemberships(d.WorkspaceID, memberships) {
			continue
		}
		if opts.Type != "" && d.Type != opts.Type {
			continue
		}
		if opts.Scope != "" && d.Scope != opts.Scope {
			continue
		}
		if opts.ProjectID != "" {
			if d.ProjectID == nil || *d.ProjectID != opts.ProjectID {
				continue
			}
		}
		if opts.ContractBinding != "" {
			if d.ContractBinding == nil || *d.ContractBinding != opts.ContractBinding {
				continue
			}
		}
		if len(opts.Tags) > 0 && !anyTagMatch(d.Tags, opts.Tags) {
			continue
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if opts.Limit > 0 && len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return out, nil
}

func anyTagMatch(have, want []string) bool {
	set := make(map[string]struct{}, len(have))
	for _, t := range have {
		set[t] = struct{}{}
	}
	for _, w := range want {
		if _, ok := set[w]; ok {
			return true
		}
	}
	return false
}

// GetByID implements Store for MemoryStore.
func (m *MemoryStore) GetByID(ctx context.Context, id string, memberships []string) (Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, ok := m.rows[id]
	if !ok || !inDocMemberships(doc.WorkspaceID, memberships) {
		return Document{}, ErrNotFound
	}
	return doc, nil
}

// GetByName implements Store for MemoryStore.
func (m *MemoryStore) GetByName(ctx context.Context, projectID, name string, memberships []string) (Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, ok := m.findByName(projectID, name)
	if !ok {
		return Document{}, ErrNotFound
	}
	if !inDocMemberships(doc.WorkspaceID, memberships) {
		return Document{}, ErrNotFound
	}
	return doc, nil
}

// Count implements Store for MemoryStore.
func (m *MemoryStore) Count(ctx context.Context, projectID string, memberships []string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, d := range m.rows {
		if d.Status != StatusActive {
			continue
		}
		if d.ProjectID == nil || *d.ProjectID != projectID {
			continue
		}
		if !inDocMemberships(d.WorkspaceID, memberships) {
			continue
		}
		n++
	}
	return n, nil
}

// inDocMemberships is the shared membership predicate. nil = no filter,
// empty = deny-all, non-empty = workspace_id IN memberships.
func inDocMemberships(wsID string, memberships []string) bool {
	if memberships == nil {
		return true
	}
	for _, m := range memberships {
		if m == wsID {
			return true
		}
	}
	return false
}

// NewID returns a fresh document id in the canonical `doc_<8hex>` form.
// Exported so the surreal store + memory store + tests mint ids
// identically.
func NewID() string {
	return fmt.Sprintf("doc_%s", uuid.NewString()[:8])
}

// BackfillWorkspaceID implements Store for MemoryStore.
func (m *MemoryStore) BackfillWorkspaceID(ctx context.Context, projectID, workspaceID string, now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for k, d := range m.rows {
		if d.ProjectID == nil || *d.ProjectID != projectID || d.WorkspaceID != "" {
			continue
		}
		d.WorkspaceID = workspaceID
		d.UpdatedAt = now
		m.rows[k] = d
		n++
	}
	return n, nil
}

// Compile-time assertion.
var _ Store = (*MemoryStore)(nil)
