package story

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// ErrNotFound is returned when a story lookup misses.
var ErrNotFound = errors.New("story: not found")

// Store wraps DB-backed story operations against the stories table
// (migration 0001 + parent_id added in 0007).
type Store struct {
	DB *sql.DB
}

// New returns a Store bound to the given database/sql handle.
func New(db *sql.DB) *Store { return &Store{DB: db} }

// CreateInput is the set of fields accepted by Create.
type CreateInput struct {
	ProjectID          string
	ParentID           string
	Title              string
	Body               string
	AcceptanceCriteria string
	Status             string
	Priority           string
	Category           string
	Tags               []string
}

// Create inserts a new story. Defaults are stamped here so callers can
// pass empty values for status/priority/category and get the substrate
// defaults (backlog / medium / feature).
func (s *Store) Create(ctx context.Context, in CreateInput, now time.Time) (Story, error) {
	if in.ProjectID == "" {
		return Story{}, fmt.Errorf("story: project_id required")
	}
	if in.Title == "" {
		return Story{}, fmt.Errorf("story: title required")
	}
	if in.Status == "" {
		in.Status = StatusBacklog
	}
	if in.Priority == "" {
		in.Priority = PriorityMedium
	}
	if in.Category == "" {
		in.Category = CategoryFeature
	}
	if in.Tags == nil {
		in.Tags = []string{}
	}
	id := NewID()
	now = now.UTC()
	var parent sql.NullString
	if in.ParentID != "" {
		parent = sql.NullString{String: in.ParentID, Valid: true}
	}
	if _, err := s.DB.ExecContext(ctx, `
        INSERT INTO stories
            (id, project_id, parent_id, title, body, acceptance_criteria,
             status, priority, category, tags, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)
    `, id, in.ProjectID, parent, in.Title, in.Body, in.AcceptanceCriteria,
		in.Status, in.Priority, in.Category, pq.Array(in.Tags), now); err != nil {
		return Story{}, fmt.Errorf("story: insert: %w", err)
	}
	return Story{
		ID:                 id,
		ProjectID:          in.ProjectID,
		ParentID:           in.ParentID,
		Title:              in.Title,
		Body:               in.Body,
		AcceptanceCriteria: in.AcceptanceCriteria,
		Status:             in.Status,
		Priority:           in.Priority,
		Category:           in.Category,
		Tags:               in.Tags,
		CreatedAt:          now,
		UpdatedAt:          now,
	}, nil
}

// GetByID returns the story with the given id, or ErrNotFound.
func (s *Store) GetByID(ctx context.Context, id string) (Story, error) {
	row := s.DB.QueryRowContext(ctx, selectColumns+" FROM stories WHERE id = $1", id)
	return scanRow(row)
}

// ListByProject returns every story bound to the given project_id,
// ordered status (in-progress first) then priority then most-recent.
// When tags is non-empty the result is filtered to stories carrying
// ALL listed tags (AND semantics via Postgres `@>`).
func (s *Store) ListByProject(ctx context.Context, projectID string, tags []string) ([]Story, error) {
	q := selectColumns + ` FROM stories WHERE project_id = $1`
	args := []any{projectID}
	if len(tags) > 0 {
		q += ` AND tags @> $2`
		args = append(args, pq.Array(tags))
	}
	q += `
        ORDER BY
            CASE status
                WHEN 'in_progress' THEN 0
                WHEN 'ready' THEN 1
                WHEN 'review' THEN 2
                WHEN 'backlog' THEN 3
                WHEN 'done' THEN 4
                WHEN 'cancelled' THEN 5
                ELSE 6
            END,
            CASE priority
                WHEN 'critical' THEN 0
                WHEN 'high' THEN 1
                WHEN 'medium' THEN 2
                WHEN 'low' THEN 3
                ELSE 4
            END,
            updated_at DESC, id`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("story: list: %w", err)
	}
	defer rows.Close()
	var out []Story
	for rows.Next() {
		st, err := scanRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// UpdateInput carries the mutable fields story_update accepts. Each
// pointer field is "set when non-nil"; project_id is immutable here
// (a cross-project move would be a separate verb).
type UpdateInput struct {
	ParentID           *string
	Title              *string
	Body               *string
	AcceptanceCriteria *string
	Status             *string
	Priority           *string
	Category           *string
	Tags               *[]string
}

// Update mutates the story row at id. Returns the updated row.
func (s *Store) Update(ctx context.Context, id string, in UpdateInput, now time.Time) (Story, error) {
	now = now.UTC()
	st, err := s.GetByID(ctx, id)
	if err != nil {
		return Story{}, err
	}
	if in.ParentID != nil {
		st.ParentID = *in.ParentID
	}
	if in.Title != nil {
		if *in.Title == "" {
			return Story{}, fmt.Errorf("story: title cannot be cleared")
		}
		st.Title = *in.Title
	}
	if in.Body != nil {
		st.Body = *in.Body
	}
	if in.AcceptanceCriteria != nil {
		st.AcceptanceCriteria = *in.AcceptanceCriteria
	}
	if in.Status != nil {
		st.Status = *in.Status
	}
	if in.Priority != nil {
		st.Priority = *in.Priority
	}
	if in.Category != nil {
		st.Category = *in.Category
	}
	if in.Tags != nil {
		st.Tags = *in.Tags
	}
	if st.Tags == nil {
		st.Tags = []string{}
	}
	st.UpdatedAt = now
	var parent sql.NullString
	if st.ParentID != "" {
		parent = sql.NullString{String: st.ParentID, Valid: true}
	}
	if _, err := s.DB.ExecContext(ctx, `
        UPDATE stories SET
            parent_id = $1, title = $2, body = $3, acceptance_criteria = $4,
            status = $5, priority = $6, category = $7, tags = $8, updated_at = $9
        WHERE id = $10
    `, parent, st.Title, st.Body, st.AcceptanceCriteria,
		st.Status, st.Priority, st.Category, pq.Array(st.Tags), now, id); err != nil {
		return Story{}, fmt.Errorf("story: update: %w", err)
	}
	return st, nil
}

const selectColumns = `SELECT id, project_id, parent_id, title, body,
    acceptance_criteria, status, priority, category, tags,
    created_at, updated_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanCommon(rs rowScanner) (Story, error) {
	var (
		st     Story
		parent sql.NullString
		tags   pq.StringArray
	)
	if err := rs.Scan(&st.ID, &st.ProjectID, &parent, &st.Title, &st.Body,
		&st.AcceptanceCriteria, &st.Status, &st.Priority, &st.Category, &tags,
		&st.CreatedAt, &st.UpdatedAt); err != nil {
		return Story{}, err
	}
	if parent.Valid {
		st.ParentID = parent.String
	}
	st.Tags = []string(tags)
	if st.Tags == nil {
		st.Tags = []string{}
	}
	return st, nil
}

func scanRow(row *sql.Row) (Story, error) {
	st, err := scanCommon(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Story{}, ErrNotFound
	}
	if err != nil {
		return Story{}, fmt.Errorf("story: scan: %w", err)
	}
	return st, nil
}

func scanRows(rows *sql.Rows) (Story, error) {
	st, err := scanCommon(rows)
	if err != nil {
		return Story{}, fmt.Errorf("story: scan: %w", err)
	}
	return st, nil
}
