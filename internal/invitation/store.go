package invitation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Store wraps DB-backed invitation operations against the invitations
// table (migration 0029).
type Store struct {
	DB *sql.DB
}

// New returns a Store bound to the given database/sql handle.
func New(db *sql.DB) *Store { return &Store{DB: db} }

// CreateInput is the set of fields accepted by Create. For workspace scope
// WorkspaceID is required; for project scope ProjectID is required (and
// WorkspaceID should be the project's workspace, for listing/audit).
type CreateInput struct {
	Email       string
	Scope       string
	WorkspaceID string
	ProjectID   string
	Role        string
	InvitedBy   string
}

// Create records a pending invitation. The email is stored lower-cased.
// Returns ErrDuplicate when a pending invite for the same (email, target)
// already exists.
func (s *Store) Create(ctx context.Context, in CreateInput, now time.Time) (Invitation, error) {
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if email == "" {
		return Invitation{}, fmt.Errorf("invitation: email required")
	}
	if !IsValidScope(in.Scope) {
		return Invitation{}, ErrInvalidScope
	}
	if !validRoleForScope(in.Scope, in.Role) {
		return Invitation{}, ErrInvalidRole
	}
	switch in.Scope {
	case ScopeWorkspace:
		if in.WorkspaceID == "" {
			return Invitation{}, fmt.Errorf("invitation: workspace_id required for workspace scope")
		}
	case ScopeProject:
		if in.ProjectID == "" {
			return Invitation{}, fmt.Errorf("invitation: project_id required for project scope")
		}
	}

	// Pre-check for an existing pending invite on the same target.
	if dup, err := s.pendingExists(ctx, email, in.Scope, in.WorkspaceID, in.ProjectID); err != nil {
		return Invitation{}, err
	} else if dup {
		return Invitation{}, ErrDuplicate
	}

	now = now.UTC()
	inv := Invitation{
		ID:          NewID(),
		Email:       email,
		Scope:       in.Scope,
		WorkspaceID: in.WorkspaceID,
		ProjectID:   in.ProjectID,
		Role:        in.Role,
		InvitedBy:   in.InvitedBy,
		Status:      StatusPending,
		CreatedAt:   now,
	}
	if _, err := s.DB.ExecContext(ctx, `
        INSERT INTO invitations (id, email, scope, workspace_id, project_id, role, invited_by, status, created_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', $8)
    `, inv.ID, inv.Email, inv.Scope, nullStr(inv.WorkspaceID), nullStr(inv.ProjectID), inv.Role, nullStr(inv.InvitedBy), now); err != nil {
		if isUniqueViolation(err) {
			return Invitation{}, ErrDuplicate
		}
		return Invitation{}, fmt.Errorf("invitation: insert: %w", err)
	}
	return inv, nil
}

func (s *Store) pendingExists(ctx context.Context, email, scope, wsID, pjID string) (bool, error) {
	var q string
	var arg string
	if scope == ScopeWorkspace {
		q = `SELECT 1 FROM invitations WHERE status='pending' AND scope='workspace' AND email=$1 AND workspace_id=$2 LIMIT 1`
		arg = wsID
	} else {
		q = `SELECT 1 FROM invitations WHERE status='pending' AND scope='project' AND email=$1 AND project_id=$2 LIMIT 1`
		arg = pjID
	}
	var one int
	err := s.DB.QueryRowContext(ctx, q, email, arg).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("invitation: pending check: %w", err)
	}
	return true, nil
}

// CreateLinkInput is accepted by CreateLink. Exactly one of WorkspaceID /
// ProjectID is set per scope; Email is intentionally absent — a link invite is
// redeemed by whoever holds the token. TTL 0 falls back to DefaultLinkTTL.
type CreateLinkInput struct {
	Scope       string
	WorkspaceID string
	ProjectID   string
	Role        string
	InvitedBy   string
	TTL         time.Duration
}

// CreateLink records a pending link invitation bearing a fresh secret token,
// not bound to any email. Returns the stored invite with Token populated.
func (s *Store) CreateLink(ctx context.Context, in CreateLinkInput, now time.Time) (Invitation, error) {
	if !IsValidScope(in.Scope) {
		return Invitation{}, ErrInvalidScope
	}
	if !validRoleForScope(in.Scope, in.Role) {
		return Invitation{}, ErrInvalidRole
	}
	switch in.Scope {
	case ScopeWorkspace:
		if in.WorkspaceID == "" {
			return Invitation{}, fmt.Errorf("invitation: workspace_id required for workspace scope")
		}
	case ScopeProject:
		if in.ProjectID == "" {
			return Invitation{}, fmt.Errorf("invitation: project_id required for project scope")
		}
	}
	token, err := NewToken()
	if err != nil {
		return Invitation{}, err
	}
	ttl := in.TTL
	if ttl <= 0 {
		ttl = DefaultLinkTTL
	}
	now = now.UTC()
	expires := now.Add(ttl)
	inv := Invitation{
		ID:          NewID(),
		Scope:       in.Scope,
		WorkspaceID: in.WorkspaceID,
		ProjectID:   in.ProjectID,
		Role:        in.Role,
		InvitedBy:   in.InvitedBy,
		Status:      StatusPending,
		Token:       token,
		ExpiresAt:   &expires,
		CreatedAt:   now,
	}
	if _, err := s.DB.ExecContext(ctx, `
        INSERT INTO invitations (id, scope, workspace_id, project_id, role, invited_by, status, token, expires_at, created_at)
        VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7, $8, $9)
    `, inv.ID, inv.Scope, nullStr(inv.WorkspaceID), nullStr(inv.ProjectID), inv.Role, nullStr(inv.InvitedBy), token, expires, now); err != nil {
		return Invitation{}, fmt.Errorf("invitation: insert link: %w", err)
	}
	return inv, nil
}

// GetByToken returns the invitation bearing token, or ErrNotFound.
func (s *Store) GetByToken(ctx context.Context, token string) (Invitation, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Invitation{}, ErrNotFound
	}
	row := s.DB.QueryRowContext(ctx, `SELECT `+invColumns+` FROM invitations WHERE token = $1`, token)
	inv, err := scanRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Invitation{}, ErrNotFound
	}
	return inv, err
}

// RedeemToken claims a pending, non-expired link invite for userID: it creates
// the workspace_members / project_members row at the invite role and marks the
// invite accepted with accepted_by = userID. ErrNotFound for an unknown token,
// ErrNotPending if already redeemed/revoked, ErrExpired past expiry.
func (s *Store) RedeemToken(ctx context.Context, token, userID string, now time.Time) (Invitation, error) {
	token = strings.TrimSpace(token)
	if token == "" || userID == "" {
		return Invitation{}, ErrNotFound
	}
	now = now.UTC()
	inv, err := s.GetByToken(ctx, token)
	if err != nil {
		return Invitation{}, err
	}
	if inv.Status != StatusPending {
		return Invitation{}, ErrNotPending
	}
	if inv.ExpiresAt != nil && now.After(*inv.ExpiresAt) {
		return Invitation{}, ErrExpired
	}
	if err := s.redeemOne(ctx, inv, userID, now); err != nil {
		return Invitation{}, err
	}
	inv.Status = StatusAccepted
	inv.AcceptedAt = &now
	inv.AcceptedBy = userID
	return inv, nil
}

func (s *Store) redeemOne(ctx context.Context, inv Invitation, userID string, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("invitation: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	switch inv.Scope {
	case ScopeWorkspace:
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO workspace_members (workspace_id, user_id, role, added_at, added_by)
            VALUES ($1, $2, $3, $4, $5)
            ON CONFLICT (workspace_id, user_id) DO UPDATE SET role = EXCLUDED.role
        `, inv.WorkspaceID, userID, inv.Role, now, nullStr(inv.InvitedBy)); err != nil {
			return fmt.Errorf("invitation: redeem workspace membership: %w", err)
		}
	case ScopeProject:
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO project_members (project_id, user_id, role, added_at, added_by)
            VALUES ($1, $2, $3, $4, $5)
            ON CONFLICT (project_id, user_id) DO UPDATE SET role = EXCLUDED.role
        `, inv.ProjectID, userID, inv.Role, now, nullStr(inv.InvitedBy)); err != nil {
			return fmt.Errorf("invitation: redeem project membership: %w", err)
		}
	default:
		return ErrInvalidScope
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE invitations SET status='accepted', accepted_at=$1, accepted_by=$2 WHERE id=$3 AND status='pending'`,
		now, userID, inv.ID)
	if err != nil {
		return fmt.Errorf("invitation: mark redeemed: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotPending // raced — redeemed/revoked between read and write
	}
	return tx.Commit()
}

// ListInput filters List. Any non-empty field narrows the result.
type ListInput struct {
	WorkspaceID string
	ProjectID   string
	Status      string
}

// List returns invitations matching the filter, newest-first.
func (s *Store) List(ctx context.Context, in ListInput) ([]Invitation, error) {
	q := `SELECT ` + invColumns + ` FROM invitations`
	var conds []string
	var args []any
	add := func(col, val string) {
		if val != "" {
			args = append(args, val)
			conds = append(conds, fmt.Sprintf("%s = $%d", col, len(args)))
		}
	}
	add("workspace_id", in.WorkspaceID)
	add("project_id", in.ProjectID)
	add("status", in.Status)
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += " ORDER BY created_at DESC, id"
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("invitation: list: %w", err)
	}
	defer rows.Close()
	var out []Invitation
	for rows.Next() {
		inv, err := scanRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// GetByID returns the invitation, or ErrNotFound.
func (s *Store) GetByID(ctx context.Context, id string) (Invitation, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+invColumns+` FROM invitations WHERE id = $1`, id)
	inv, err := scanRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Invitation{}, ErrNotFound
	}
	return inv, err
}

// Revoke marks a pending invitation revoked. ErrNotFound when missing;
// ErrNotPending when it is not pending (already accepted/revoked).
func (s *Store) Revoke(ctx context.Context, id string, now time.Time) error {
	inv, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if inv.Status != StatusPending {
		return ErrNotPending
	}
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE invitations SET status='revoked' WHERE id=$1 AND status='pending'`, id); err != nil {
		return fmt.Errorf("invitation: revoke: %w", err)
	}
	return nil
}

// ClaimForEmail claims every pending invitation for the given email
// (case-insensitive) on behalf of userID: each creates the corresponding
// workspace_members / project_members row at the invited role and marks the
// invite accepted. Idempotent — already-accepted/revoked invites are skipped,
// and a membership conflict updates the role. Returns the claimed invites.
func (s *Store) ClaimForEmail(ctx context.Context, email, userID string, now time.Time) ([]Invitation, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || userID == "" {
		return nil, nil
	}
	now = now.UTC()
	rows, err := s.DB.QueryContext(ctx, `SELECT `+invColumns+`
        FROM invitations WHERE email = $1 AND status = 'pending'
        ORDER BY created_at ASC, id
    `, email)
	if err != nil {
		return nil, fmt.Errorf("invitation: claim list: %w", err)
	}
	var pending []Invitation
	for rows.Next() {
		inv, err := scanRows(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		pending = append(pending, inv)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	var claimed []Invitation
	for _, inv := range pending {
		if err := s.claimOne(ctx, inv, userID, now); err != nil {
			return claimed, err
		}
		inv.Status = StatusAccepted
		inv.AcceptedAt = &now
		claimed = append(claimed, inv)
	}
	return claimed, nil
}

func (s *Store) claimOne(ctx context.Context, inv Invitation, userID string, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("invitation: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	switch inv.Scope {
	case ScopeWorkspace:
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO workspace_members (workspace_id, user_id, role, added_at, added_by)
            VALUES ($1, $2, $3, $4, $5)
            ON CONFLICT (workspace_id, user_id) DO UPDATE SET role = EXCLUDED.role
        `, inv.WorkspaceID, userID, inv.Role, now, nullStr(inv.InvitedBy)); err != nil {
			return fmt.Errorf("invitation: claim workspace membership: %w", err)
		}
	case ScopeProject:
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO project_members (project_id, user_id, role, added_at, added_by)
            VALUES ($1, $2, $3, $4, $5)
            ON CONFLICT (project_id, user_id) DO UPDATE SET role = EXCLUDED.role
        `, inv.ProjectID, userID, inv.Role, now, nullStr(inv.InvitedBy)); err != nil {
			return fmt.Errorf("invitation: claim project membership: %w", err)
		}
	default:
		return ErrInvalidScope
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE invitations SET status='accepted', accepted_at=$1 WHERE id=$2 AND status='pending'`,
		now, inv.ID); err != nil {
		return fmt.Errorf("invitation: mark accepted: %w", err)
	}
	return tx.Commit()
}

func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func isUniqueViolation(err error) bool {
	// lib/pq surfaces SQLSTATE 23505 in the error text; avoid a hard
	// dependency on the driver type by matching the code string.
	return err != nil && strings.Contains(err.Error(), "23505")
}

type rowScanner interface {
	Scan(dest ...any) error
}

// invColumns is the canonical select list backing scanCommon. Keep the two
// in lock-step.
const invColumns = `id, email, scope, workspace_id, project_id, role, invited_by, status, token, expires_at, accepted_by, created_at, accepted_at`

func scanCommon(sc rowScanner) (Invitation, error) {
	var (
		inv                    Invitation
		email, wsID, pjID      sql.NullString
		invBy, token, acceptBy sql.NullString
		expiresAt, acceptedAt  sql.NullTime
	)
	if err := sc.Scan(&inv.ID, &email, &inv.Scope, &wsID, &pjID, &inv.Role,
		&invBy, &inv.Status, &token, &expiresAt, &acceptBy, &inv.CreatedAt, &acceptedAt); err != nil {
		return Invitation{}, err
	}
	inv.Email = email.String
	inv.WorkspaceID = wsID.String
	inv.ProjectID = pjID.String
	inv.InvitedBy = invBy.String
	inv.Token = token.String
	inv.AcceptedBy = acceptBy.String
	if expiresAt.Valid {
		t := expiresAt.Time
		inv.ExpiresAt = &t
	}
	if acceptedAt.Valid {
		t := acceptedAt.Time
		inv.AcceptedAt = &t
	}
	return inv, nil
}

func scanRow(row *sql.Row) (Invitation, error) { return scanCommon(row) }

func scanRows(rows *sql.Rows) (Invitation, error) {
	inv, err := scanCommon(rows)
	if err != nil {
		return Invitation{}, fmt.Errorf("invitation: scan: %w", err)
	}
	return inv, nil
}
