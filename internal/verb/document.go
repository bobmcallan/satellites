// Document verbs — the documents-as-substrate read/write surface.
//
// document_get is the only verb registered here in story 2; upsert +
// delete arrive in story 3, variables in 4, templating in 5.
//
// Scope-aware auth:
//   - system    : any authenticated caller reads
//   - workspace : caller must be a member of the named workspace
//   - project   : caller must be a member of the project's workspace
//
// CLI-local invocations (no authStore wired) bypass the membership
// check; the satellites-server boot path that wires authStore also
// wires workspaceStore + projectStore, so an authenticated HTTP request
// goes through the full check.

package verb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bobmcallan/satellites/internal/auth"
	"github.com/bobmcallan/satellites/internal/document"
	"github.com/bobmcallan/satellites/internal/frontmatter"
	"github.com/bobmcallan/satellites/internal/project"
	"github.com/bobmcallan/satellites/internal/variable"
	"github.com/bobmcallan/satellites/internal/workspace"
)

// isWorkflowUpsert reports whether a key-addressed upsert is storing a
// kind:workflow document. A workflow stores as a type:skill row, so the kind is
// read from the kind:workflow tag (set by the CLI upload from frontmatter) or,
// defensively, the body's frontmatter — never the type. Used to require
// project-admin for a workflow write (sty_fc39ca77 AC2): a workflow defines the
// process governing every story, a privileged change above an ordinary document.
func isWorkflowUpsert(req DocumentUpsertRequest) bool {
	if req.Tags != nil {
		for _, t := range *req.Tags {
			if strings.EqualFold(strings.TrimSpace(t), "kind:workflow") {
				return true
			}
		}
	}
	if fm, _, err := frontmatter.Parse([]byte(req.Body)); err == nil {
		if strings.EqualFold(strings.TrimSpace(fm.Kind), "workflow") {
			return true
		}
	}
	return false
}

// reviewRequiredKind reports whether a non-id upsert targets a BEHAVIOUR-kind
// row whose content the per-type reviewer governs (sty_e6226180): a skill
// (kind:workflow stores AS a type:skill row, so type=skill covers both) or a
// principle (a principles:* tag on a type:document row). Stories, tasks, and
// free-form documents are NOT behaviour kinds.
func reviewRequiredKind(req DocumentUpsertRequest) bool {
	switch strings.TrimSpace(strings.ToLower(req.Type)) {
	case string(document.TypeSkill):
		return true
	case string(document.TypeStory), string(document.TypeTask):
		return false
	}
	if req.Tags != nil {
		for _, t := range *req.Tags {
			if strings.HasPrefix(strings.TrimSpace(t), principleTagPrefix) {
				return true
			}
		}
	}
	return false
}

// uploadNounForReq names the CLI upload verb to point a refused author at.
func uploadNounForReq(req DocumentUpsertRequest) string {
	if isWorkflowUpsert(req) {
		return "workflow"
	}
	if req.Tags != nil {
		for _, t := range *req.Tags {
			if strings.HasPrefix(strings.TrimSpace(t), principleTagPrefix) {
				return "principle"
			}
		}
	}
	return "skill"
}

// sha256Hex is the content hash a review attestation binds to.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// verifyReviewAttestation enforces the behaviour-kind review barrier: the
// attestation must be present, decision=accept, and content_sha256 == the body's
// hash (rejecting a stale/replayed accept for different content). A CLI upload
// mints it after the per-type reviewer accepts; the raw verb does not review, so
// an omitted attestation is refused with the path to the reviewed command.
func verifyReviewAttestation(req DocumentUpsertRequest) error {
	r := req.Review
	if r == nil || strings.TrimSpace(r.Decision) == "" {
		return fmt.Errorf("document_upsert: %w: a %s upsert needs a review attestation — author it under .satellites/ and run `satellites %s upload`, which runs the per-type reviewer then upserts; the raw verb does not review", ErrForbidden, uploadNounForReq(req), uploadNounForReq(req))
	}
	if !strings.EqualFold(strings.TrimSpace(r.Decision), "accept") {
		return fmt.Errorf("document_upsert: %w: review decision is %q, not \"accept\"", ErrForbidden, r.Decision)
	}
	if want := sha256Hex(req.Body); !strings.EqualFold(strings.TrimSpace(r.ContentSHA256), want) {
		return fmt.Errorf("document_upsert: %w: review attestation content_sha256 does not match the body (stale or replayed) — re-run `satellites %s upload`", ErrForbidden, uploadNounForReq(req))
	}
	return nil
}

// behaviourBodyUnchanged reports whether a key-addressed behaviour-kind upsert
// leaves the body byte-identical to the stored version — a TAG/metadata-only patch
// (e.g. `context curate` toggling principles:always, which re-sends the RAW body).
// The review barrier gates CONTENT; an unchanged body has nothing new to review, so
// such a patch is exempt (sty_dc44e359 — the barrier otherwise refused curate,
// breaking it). Fails closed: any resolution error, no stored version, or a body
// mismatch → not unchanged → review is still required.
func behaviourBodyUnchanged(ctx context.Context, req DocumentUpsertRequest) bool {
	if documentStore == nil || strings.TrimSpace(req.Name) == "" {
		return false
	}
	scope, err := parseScope(req.Scope)
	if err != nil {
		return false
	}
	key := document.Key{Scope: scope, WorkspaceID: req.WorkspaceID, ProjectID: req.ProjectID, Name: req.Name}
	switch scope {
	case document.ScopeUser:
		key.UserID = callerUserID(ctx)
	case document.ScopeProject:
		if strings.TrimSpace(key.WorkspaceID) == "" && projectStore != nil && strings.TrimSpace(key.ProjectID) != "" {
			if p, perr := projectStore.GetByID(ctx, key.ProjectID); perr == nil {
				key.WorkspaceID = p.WorkspaceID
			}
		}
	}
	res, err := documentStore.Get(ctx, key, document.GetOptions{})
	if err != nil || len(res.Versions) == 0 {
		return false
	}
	return res.Versions[0].Body == req.Body
}

var documentStore *document.Store

// SetDocumentStore wires the server's document.Store into the verb
// package. Called from cmd/satellites-server on boot.
func SetDocumentStore(s *document.Store) { documentStore = s }

// DocumentGetRequest is the input shape for document_get.
//
//	Version: "" or "latest" → most recent active version
//	         "<N>"          → that exact version row
//	         "all"          → every version, ascending
//
// Inherit=true performs project → workspace → system fallback when the
// named document doesn't exist at the requested scope.
//
// OS / Arch / CurrentVersion are per-request system-variable inputs.
// The templating layer reads them via the system-variables resolver;
// callers that aren't rendering templates can leave them empty.
type DocumentGetRequest struct {
	// ID-based fetch (used for stories and any document addressable by
	// id). When present, scope/name/workspace_id/project_id are ignored.
	ID string `json:"id,omitempty"`

	// Key-based fetch (used for free-form documents). Required when ID
	// is empty.
	Name        string `json:"name,omitempty"`
	Scope       string `json:"scope,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`

	Version        string `json:"version,omitempty"`
	Inherit        bool   `json:"inherit,omitempty"`
	OS             string `json:"os,omitempty"`
	Arch           string `json:"arch,omitempty"`
	CurrentVersion string `json:"current_version,omitempty"`
}

// DocumentGetResponse bundles the resolved document row + version slice.
// resolved_scope reports the scope the document was actually found at
// (relevant when inherit cascade kicks in).
//
// raw_body / rendered_body / unresolved_vars are populated only for
// single-version reads (latest or a specific version). version=all
// returns the chain verbatim; rendering each historical body would
// silently rewrite old text against today's variables, which is
// usually not what an operator viewing history wants.
type DocumentGetResponse struct {
	Document       document.Document  `json:"document"`
	Versions       []document.Version `json:"versions"`
	ResolvedScope  string             `json:"resolved_scope"`
	RawBody        string             `json:"raw_body,omitempty"`
	RenderedBody   string             `json:"rendered_body,omitempty"`
	UnresolvedVars []string           `json:"unresolved_vars,omitempty"`
	Principles     []Principle        `json:"principles,omitempty"`
}

// documentTemplateCache holds per-(document_id, version) parsed
// templates. Substrate contract: a (doc, version) body is immutable,
// so cached *Parsed values are valid for the binary's lifetime.
var documentTemplateCache document.Cache

// DocumentUpsertRequest is the input shape for document_upsert. Three
// modes, chosen on inspection:
//
//  1. id present → patch the row at id. Body change appends a new
//     document_versions row; metadata fields (Tags, Status, Priority,
//     Category, ParentID, AcceptanceCriteria, and Name as title for
//     stories) apply in place. Currently used for stories only.
//
//  2. id empty + type=='story' → create a new story (fresh sty_<id>).
//     project_id + name (title) required.
//
//  3. id empty + type=='document' (or empty) → key-addressed upsert
//     against (scope, workspace_id, project_id, name). On first call
//     for that key, inserts a documents row; subsequent calls append
//     a new version. Existing pre-unification behaviour.
//
// scope='system' is rejected at the store boundary (ErrScopeReadonly).
type DocumentUpsertRequest struct {
	ID          string `json:"id,omitempty"`
	Type        string `json:"type,omitempty"`
	Name        string `json:"name,omitempty"`
	Scope       string `json:"scope,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
	Body        string `json:"body,omitempty"`

	// Story-shaped metadata. Pointer-typed so the dispatcher can tell
	// "unset" from "explicit empty" on patch — nil means leave alone,
	// non-nil means apply.
	Tags               *[]string `json:"tags,omitempty"`
	Status             *string   `json:"status,omitempty"`
	Priority           *string   `json:"priority,omitempty"`
	Category           *string   `json:"category,omitempty"`
	ParentID           *string   `json:"parent_id,omitempty"`
	AcceptanceCriteria *string   `json:"acceptance_criteria,omitempty"`

	// Headline is the caveman one-liner for a document/principle. Pointer-typed
	// with the same leave-alone semantics as Tags: nil means "do not touch",
	// non-nil applies (empty string clears). Stored as document-level metadata,
	// not versioned with the body (epic:always-context).
	Headline *string `json:"headline,omitempty"`

	// Review is the CLI upload's evidence that the per-type reviewer accepted
	// this exact content (sty_e6226180). REQUIRED on a behaviour-kind upsert
	// (skill / principle / workflow); absent → the write is refused, redirecting
	// to `satellites <kind> upload`. nil for stories, tasks, and free-form docs.
	Review *ReviewAttestation `json:"review,omitempty"`
}

// ReviewAttestation is proof the per-type reviewer ACCEPTED a behaviour-kind
// body. content_sha256 binds it to the body so a stale/replayed attestation for
// different content is rejected. LIMIT (sty_e6226180): a CLI-controlling caller
// can forge an accept — true non-forgeability needs SERVER-SIDE review (filed
// follow-up). This closes the SILENT/omit bypass, not a deliberate forger.
type ReviewAttestation struct {
	Skill         string `json:"skill,omitempty"`
	Decision      string `json:"decision,omitempty"`
	ContentSHA256 string `json:"content_sha256,omitempty"`
}

// DocumentUpsertResponse mirrors document_get's shape so callers can
// reuse parse paths: the new version row is returned alongside the
// document pointer.
type DocumentUpsertResponse struct {
	Document document.Document `json:"document"`
	Version  document.Version  `json:"version"`
}

// DocumentDeleteRequest is the input shape for document_delete. Two
// modes, chosen on inspection:
//
//  1. id present → the stored row's type chooses behaviour. type='story'
//     rows are hard-deleted (DELETE FROM documents — children's
//     parent_id is nulled, ledger entries persist). type='document' rows
//     get the pre-unification soft-delete (append tombstone version).
//
//  2. id empty → key-addressed soft delete against (scope, workspace_id,
//     project_id, name). Existing pre-unification behaviour.
type DocumentDeleteRequest struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	Scope       string `json:"scope,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
}

// DocumentDeleteResponse returns the tombstone version that was
// appended.
type DocumentDeleteResponse struct {
	Document document.Document `json:"document"`
	Version  document.Version  `json:"version"`
}

// DocumentListRequest is the structured filter shape for document_list.
// Every field is optional. type/status="all" or "" disables that
// predicate. tags acts as an AND filter. limit defaults to 50, capped
// at 200. cursor is opaque base64 returned in the previous page's
// next_cursor; pass empty to fetch the first page.
type DocumentListRequest struct {
	Type         string   `json:"type,omitempty"`
	Scope        string   `json:"scope,omitempty"`
	WorkspaceID  string   `json:"workspace_id,omitempty"`
	ProjectID    string   `json:"project_id,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	Status       string   `json:"status,omitempty"`
	NamePrefix   string   `json:"name_prefix,omitempty"`
	NameContains string   `json:"name_contains,omitempty"`
	Limit        int      `json:"limit,omitempty"`
	Cursor       string   `json:"cursor,omitempty"`
	// Effective overlays the caller's user-scope override rows onto the
	// requested-scope list, shadowing same-named rows (user wins), so the
	// result is the post-cascade effective set (sty_cbeeb452). Single merged
	// page, no cursor — intended for config-resolution reads (skills,
	// principles, config), which are small sets.
	Effective bool `json:"effective,omitempty"`
}

// DocumentListResponse is the paged response from document_list.
// next_cursor is empty when there are no more pages; non-empty means
// the client should issue the next call with that value in cursor.
type DocumentListResponse struct {
	Items      []document.Document `json:"items"`
	NextCursor string              `json:"next_cursor,omitempty"`
}

// DocumentCountRequest mirrors DocumentListRequest's filter fields. No
// Limit / Cursor — count is over the entire matching set. Paired with
// document_list for "page N of M"-style indicators and total-row
// counters in the portal.
type DocumentCountRequest struct {
	Type         string   `json:"type,omitempty"`
	Scope        string   `json:"scope,omitempty"`
	WorkspaceID  string   `json:"workspace_id,omitempty"`
	ProjectID    string   `json:"project_id,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	Status       string   `json:"status,omitempty"`
	NamePrefix   string   `json:"name_prefix,omitempty"`
	NameContains string   `json:"name_contains,omitempty"`
}

// DocumentCountResponse is the count for a filter — a single int wire
// shape so callers don't bring in the whole document.Document type.
type DocumentCountResponse struct {
	Count int `json:"count"`
}

func init() {
	Register(&Verb{
		Name:        "document_get",
		Description: "Fetch a document by id (any row, including stories) OR by (scope, name) with version selection + inherit cascade (free-form documents).",
		Invoke:      invokeDocumentGet,
	})
	Register(&Verb{
		Name: "document_upsert",
		Description: "Create or update. Three modes by inspection: " +
			"(1) type='story' + project_id + name → create story; " +
			"(2) id present → patch story (body change appends a new version); " +
			"(3) type='document' + scope + name → key-addressed document upsert.",
		Invoke: invokeDocumentUpsert,
	})
	Register(&Verb{
		Name: "document_delete",
		Description: "Delete by id (hard-delete for stories, soft-tombstone for documents — chosen by stored type) " +
			"OR by (scope, name) (soft-tombstone).",
		Invoke: invokeDocumentDelete,
	})
	Register(&Verb{
		Name: "document_list",
		Description: "List substrate rows with structured filters and cursor pagination. " +
			"Pass type:'story' / 'document' / 'skill' to restrict; omit to list every type.",
		Invoke: invokeDocumentList,
	})
	Register(&Verb{
		Name: "document_count",
		Description: "Count rows matching the same filter shape document_list accepts. " +
			"Paired with document_list for total-row indicators and 'page N of M' style paginators.",
		Invoke: invokeDocumentCount,
	})
}

func invokeDocumentList(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if documentStore == nil {
		return nil, fmt.Errorf("document_list: store not configured")
	}
	var req DocumentListRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("document_list: %w: %v", ErrBadRequest, err)
		}
	}
	// A scoped list must be bounded to its scope key (project-bound skills,
	// sty_2fa6f087) and authorized for the caller, before any store read.
	if err := requireScopeKey(req.Scope, req.WorkspaceID, req.ProjectID); err != nil {
		return nil, err
	}
	if err := authorizeListScope(ctx, document.Scope(req.Scope), req.WorkspaceID, req.ProjectID); err != nil {
		return nil, err
	}
	// Skills/principles are off the MCP surface (epic:authoring-as-config): an
	// explicit type=skill list is refused; principle-tagged rows are filtered
	// from any mixed listing below. Non-MCP transports are unaffected.
	transport := auth.TransportFromContext(ctx)
	if mcpForbidsSkillList(transport, req.Type) {
		return nil, fmt.Errorf("document_list: %w: %q is read client-side (`satellites skill sync` / `satellites document index`), not over MCP", ErrForbidden, "skill")
	}
	if req.Effective {
		if userID := callerUserID(ctx); userID != "" {
			return effectiveList(ctx, req, userID)
		}
		// No caller identity — nothing to overlay; fall through to the plain
		// requested-scope list.
	}
	res, err := documentStore.List(ctx, document.ListFilter{
		Type:         req.Type,
		Scope:        document.Scope(req.Scope),
		WorkspaceID:  req.WorkspaceID,
		ProjectID:    req.ProjectID,
		Tags:         req.Tags,
		Status:       req.Status,
		NamePrefix:   req.NamePrefix,
		NameContains: req.NameContains,
		Audience:     libraryAudienceFilter(document.Scope(req.Scope)),
	}, document.ListOptions{
		Limit:  req.Limit,
		Cursor: req.Cursor,
	})
	if err != nil {
		return nil, err
	}
	items := filterMCPReadable(transport, res.Items)
	if items == nil {
		items = []document.Document{}
	}
	return json.Marshal(DocumentListResponse{Items: items, NextCursor: res.NextCursor})
}

// effectiveList returns the post-cascade effective set for a caller: the
// requested-scope rows with same-named rows replaced by the caller's
// user-scope override (user wins), plus user overrides that have no
// lower-scope counterpart (sty_cbeeb452). It is a single merged page (no
// cursor) for config-resolution reads, which are small sets; it never
// crosses users — the user sublist is keyed to the caller.
func effectiveList(ctx context.Context, req DocumentListRequest, userID string) (json.RawMessage, error) {
	const effectiveLimit = 200 // store List caps at 200; config sets are small
	base, err := documentStore.List(ctx, document.ListFilter{
		Type:         req.Type,
		Scope:        document.Scope(req.Scope),
		WorkspaceID:  req.WorkspaceID,
		ProjectID:    req.ProjectID,
		Tags:         req.Tags,
		Status:       req.Status,
		NamePrefix:   req.NamePrefix,
		NameContains: req.NameContains,
	}, document.ListOptions{Limit: effectiveLimit})
	if err != nil {
		return nil, err
	}
	overrides, err := documentStore.List(ctx, document.ListFilter{
		Type:         req.Type,
		Scope:        document.ScopeUser,
		UserID:       userID,
		Tags:         req.Tags,
		Status:       req.Status,
		NamePrefix:   req.NamePrefix,
		NameContains: req.NameContains,
	}, document.ListOptions{Limit: effectiveLimit})
	if err != nil {
		return nil, err
	}

	// Overlay: a user override shadows the same-named base row; a brand-new
	// user row (no base counterpart) is appended.
	shadowed := make(map[string]int, len(base.Items))
	out := make([]document.Document, 0, len(base.Items)+len(overrides.Items))
	for _, d := range base.Items {
		shadowed[d.Name] = len(out)
		out = append(out, d)
	}
	for _, ov := range overrides.Items {
		if idx, ok := shadowed[ov.Name]; ok {
			out[idx] = ov
			continue
		}
		out = append(out, ov)
	}
	out = filterMCPReadable(auth.TransportFromContext(ctx), out)
	if out == nil {
		out = []document.Document{}
	}
	return json.Marshal(DocumentListResponse{Items: out})
}

func invokeDocumentCount(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if documentStore == nil {
		return nil, fmt.Errorf("document_count: store not configured")
	}
	var req DocumentCountRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("document_count: %w: %v", ErrBadRequest, err)
		}
	}
	if err := authorizeListScope(ctx, document.Scope(req.Scope), req.WorkspaceID, req.ProjectID); err != nil {
		return nil, err
	}
	if mcpForbidsSkillList(auth.TransportFromContext(ctx), req.Type) {
		return nil, fmt.Errorf("document_count: %w: %q is read client-side, not over MCP", ErrForbidden, "skill")
	}
	n, err := documentStore.Count(ctx, document.ListFilter{
		Type:         req.Type,
		Scope:        document.Scope(req.Scope),
		WorkspaceID:  req.WorkspaceID,
		ProjectID:    req.ProjectID,
		Tags:         req.Tags,
		Status:       req.Status,
		NamePrefix:   req.NamePrefix,
		NameContains: req.NameContains,
		Audience:     libraryAudienceFilter(document.Scope(req.Scope)),
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(DocumentCountResponse{Count: n})
}

func invokeDocumentGet(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if documentStore == nil {
		return nil, fmt.Errorf("document_get: store not configured")
	}
	var req DocumentGetRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("document_get: %w: %v", ErrBadRequest, err)
		}
	}

	// ID-addressed fetch — used for stories and any document fetched by
	// id rather than by (scope, name) key.
	if strings.TrimSpace(req.ID) != "" {
		d, body, err := documentStore.GetByIDWithLatestBody(ctx, req.ID)
		if err != nil {
			return nil, mapStoreError(err, "document_get")
		}
		if mcpReadForbiddenDoc(auth.TransportFromContext(ctx), d.Type, d.Tags) {
			return nil, fmt.Errorf("document_get: %w: skills/principles are read client-side, not over MCP", ErrForbidden)
		}
		// Enforce the same scope read gate as the by-(scope,name) path
		// (sty_14410cb3): the id-addressed fetch is the PRIMARY story-read path and
		// must not bypass project scope, or a caller limited to project A (a
		// non-member, or an agent-role downscope) could read any project-B story by
		// id. authorizeRead resolves system (ok) / user (owner-only) / library (ok,
		// audience below) / project (≥read) / workspace (membership) from the
		// fetched row's own scope keys.
		if err := authorizeRead(ctx, document.Key{Scope: d.Scope, WorkspaceID: d.WorkspaceID, ProjectID: d.ProjectID, UserID: d.UserID, Name: d.Name}); err != nil {
			return nil, err
		}
		if err := authorizeLibraryAudience(ctx, d); err != nil {
			return nil, err
		}
		resp := DocumentGetResponse{
			Document:      d,
			Versions:      []document.Version{{DocumentID: d.ID, Version: d.LatestVersion, Body: body, Status: document.StatusActive}},
			ResolvedScope: string(d.Scope),
			RawBody:       body,
			RenderedBody:  body,
		}
		if d.Type == document.TypeStory && d.WorkspaceID != "" && d.ProjectID != "" {
			resp.Principles = LoadPrinciples(ctx,
				PrincipleScopeRequest{Scope: PrincipleScopeProject, WorkspaceID: d.WorkspaceID, ProjectID: d.ProjectID},
				PrincipleScopeRequest{Scope: PrincipleScopeStory, WorkspaceID: d.WorkspaceID, ProjectID: d.ProjectID},
			)
		}
		return json.Marshal(resp)
	}

	if strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("document_get: %w: name or id required", ErrBadRequest)
	}
	scope, err := parseScope(req.Scope)
	if err != nil {
		return nil, err
	}

	opts, err := parseVersionSelector(req.Version)
	if err != nil {
		return nil, err
	}

	// Stamp per-request system-variable inputs onto ctx so the
	// templating resolver and the variable_get system terminator see
	// the same values.
	ctx = WithSystemVarContext(ctx, req.OS, req.Arch, req.CurrentVersion)

	cascade := buildResolutionChain(req.Name, scope, req.WorkspaceID, req.ProjectID, callerUserID(ctx), req.Inherit)
	for i, key := range cascade {
		if err := authorizeRead(ctx, key); err != nil {
			return nil, err
		}
		res, lookupErr := documentStore.Get(ctx, key, opts)
		if lookupErr == nil {
			if mcpReadForbiddenDoc(auth.TransportFromContext(ctx), res.Document.Type, res.Document.Tags) {
				return nil, fmt.Errorf("document_get: %w: skills/principles are read client-side, not over MCP", ErrForbidden)
			}
			if err := authorizeLibraryAudience(ctx, res.Document); err != nil {
				return nil, err
			}
			return marshalDocumentGet(ctx, res, key, opts, &req)
		}
		if !errors.Is(lookupErr, document.ErrNotFound) {
			return nil, lookupErr
		}
		if i == len(cascade)-1 {
			return nil, fmt.Errorf("document_get: %w: %s/%s", ErrNotFound, key.Scope, key.Name)
		}
	}
	return nil, fmt.Errorf("document_get: %w", ErrNotFound)
}

// buildResolutionChain returns the ordered list of Keys to probe; the first
// match wins, so the slice is ordered highest-precedence first. With
// inherit=false the slice is exactly one key (the requested scope).
//
// With inherit=true the chain is the cascade, highest precedence first:
// user > project > workspace > system (sty_cbeeb452). The user layer is
// prepended only when the caller is identified (userID != ""); it is keyed
// to that caller, so a user resolves only their own overrides. A request for
// scope=user itself resolves the single user key.
func buildResolutionChain(name string, scope document.Scope, wsID, pjID, userID string, inherit bool) []document.Key {
	base := document.Key{Scope: scope, WorkspaceID: wsID, ProjectID: pjID, Name: name}
	if scope == document.ScopeUser {
		base.UserID = userID
	}
	chain := []document.Key{base}
	if !inherit || scope == document.ScopeLibrary {
		// The library is a distribution surface, not a precedence layer: it
		// joins no cascade and the caller's user overlay does not shadow it.
		return chain
	}
	// Prepend the caller's user override layer at highest precedence for
	// non-user reads.
	if userID != "" && scope != document.ScopeUser {
		chain = append([]document.Key{{Scope: document.ScopeUser, UserID: userID, Name: name}}, chain...)
	}
	switch scope {
	case document.ScopeProject:
		if wsID != "" {
			chain = append(chain, document.Key{Scope: document.ScopeWorkspace, WorkspaceID: wsID, Name: name})
		}
		chain = append(chain, document.Key{Scope: document.ScopeSystem, Name: name})
	case document.ScopeWorkspace:
		chain = append(chain, document.Key{Scope: document.ScopeSystem, Name: name})
	}
	return chain
}

// parseScope turns the wire-string scope into the typed Scope value.
func parseScope(s string) (document.Scope, error) {
	switch strings.TrimSpace(s) {
	case "system":
		return document.ScopeSystem, nil
	case "workspace":
		return document.ScopeWorkspace, nil
	case "project":
		return document.ScopeProject, nil
	case "user":
		return document.ScopeUser, nil
	case "library":
		return document.ScopeLibrary, nil
	case "":
		return "", fmt.Errorf("document_get: %w: scope required", ErrBadRequest)
	default:
		return "", fmt.Errorf("document_get: %w: unknown scope %q", ErrBadRequest, s)
	}
}

// parseVersionSelector turns the "" / "latest" / "<N>" / "all" wire
// strings into a typed GetOptions value.
func parseVersionSelector(v string) (document.GetOptions, error) {
	v = strings.TrimSpace(v)
	if v == "" || v == "latest" {
		return document.GetOptions{}, nil
	}
	if v == "all" {
		return document.GetOptions{AllVersions: true}, nil
	}
	n, err := parsePositiveInt(v)
	if err != nil {
		return document.GetOptions{}, fmt.Errorf("document_get: %w: version must be 'latest', 'all', or a positive integer; got %q", ErrBadRequest, v)
	}
	return document.GetOptions{Version: n}, nil
}

func parsePositiveInt(s string) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not a positive integer")
		}
		n = n*10 + int(r-'0')
		if n > 1_000_000 {
			return 0, fmt.Errorf("version out of range")
		}
	}
	if n == 0 {
		return 0, fmt.Errorf("must be positive")
	}
	return n, nil
}

// authorizeRead enforces the scope-mismatch rule from acceptance
// criterion 4: workspace/project reads from a caller that's not a
// member of the workspace return 403, not 404. CLI-local in-process
// invocations (no authStore configured) skip the check.
func authorizeRead(ctx context.Context, key document.Key) error {
	if authStore == nil {
		return nil
	}
	if key.Scope == document.ScopeSystem {
		return nil
	}
	u := auth.FromContext(ctx)
	if u == nil {
		return fmt.Errorf("document_get: %w: bearer required for %s scope", ErrUnauthorized, key.Scope)
	}
	// User scope is the caller's own override layer: a caller may read only
	// rows keyed to their own id, never another user's (sty_cbeeb452).
	if key.Scope == document.ScopeUser {
		if key.UserID != u.ID {
			return fmt.Errorf("document_get: %w: user scope is readable only by its owner", ErrForbidden)
		}
		return nil
	}
	// Library reads are open to any authenticated caller — no workspace or
	// project membership (epic:skill-library, sty_5678cc4b). The audience
	// filter is enforced post-fetch (authorizeLibraryAudience): it lives on
	// the row, not the key.
	if key.Scope == document.ScopeLibrary {
		return nil
	}
	if workspaceStore == nil {
		// Workspace store unwired: in-process tests that don't exercise
		// auth shouldn't fail closed. Server boot always wires it.
		return nil
	}
	// Project scope: enforce the project capability matrix (≥ read) via the
	// effective-role resolver, replacing the workspace-membership shortcut
	// (epic:user-admin, sty_c96cc77f).
	if key.Scope == document.ScopeProject {
		return enforceProjectScope(ctx, key.WorkspaceID, key.ProjectID, "document_get", project.RoleRead)
	}
	wsID := key.WorkspaceID
	if wsID == "" {
		return fmt.Errorf("document_get: %w: %s scope requires workspace_id", ErrBadRequest, key.Scope)
	}
	// Global admin is admin everywhere (docs/project-rbac.md) — it may read any
	// workspace's documents without an explicit membership row, the same
	// inheritance the project path grants via effectiveProjectRole (sty_3c2f02bf).
	if callerIsGlobalAdmin(ctx) {
		return nil
	}
	if _, err := workspaceStore.GetRole(ctx, wsID, u.ID); err != nil {
		if errors.Is(err, workspace.ErrMemberNotFound) {
			return fmt.Errorf("document_get: %w: user not a member of workspace %s", ErrForbidden, wsID)
		}
		return err
	}
	return nil
}

// libraryAudienceFilter returns the audience predicate a scoped list/count
// must carry: library listings surface only audience='all' rows (the read
// filter, epic:skill-library); every other scope is unfiltered. A publisher
// reads its own non-'all' rows by (scope, name) get, not via list.
func libraryAudienceFilter(scope document.Scope) string {
	if scope == document.ScopeLibrary {
		return "all"
	}
	return ""
}

// authorizeLibraryAudience enforces the library read filter on a fetched row:
// audience='all' is readable by any authenticated caller; anything else only
// by callers with ≥ read on the publisher project — the future door for
// restricted libraries, with no grant model now (epic:skill-library).
func authorizeLibraryAudience(ctx context.Context, d document.Document) error {
	if d.Scope != document.ScopeLibrary || d.Audience == "" || d.Audience == "all" {
		return nil
	}
	if authStore == nil {
		return nil
	}
	return enforceProjectScope(ctx, "", d.ProjectID, "document_get", project.RoleRead)
}

// requireScopeKey enforces that a scoped document_list is bounded to a single
// scope key, so the result set can never silently widen to every project /
// workspace. A project-scoped list must carry both project_id and the
// workspace_id its membership check needs; a workspace-scoped list must carry
// a workspace_id. Without this an empty id adds no SQL predicate and the store
// returns every project's rows — the cross-project skill leak (sty_2fa6f087).
// Unconditional (not gated on authStore) so in-process CLI calls are bounded
// too.
func requireScopeKey(scope, wsID, pjID string) error {
	switch strings.TrimSpace(scope) {
	case "project":
		if strings.TrimSpace(pjID) == "" {
			return fmt.Errorf("document_list: %w: project scope requires project_id", ErrBadRequest)
		}
		if strings.TrimSpace(wsID) == "" {
			return fmt.Errorf("document_list: %w: project scope requires workspace_id", ErrBadRequest)
		}
	case "workspace":
		if strings.TrimSpace(wsID) == "" {
			return fmt.Errorf("document_list: %w: workspace scope requires workspace_id", ErrBadRequest)
		}
	case "all":
		// scope=all cross-lists system + the caller's workspace (+ project)
		// in one query; workspace_id is the scope key that bounds it and is
		// authorized against membership. project_id is optional — when given
		// it adds the project branch to the OR. (sty_2eccc1ea)
		if strings.TrimSpace(wsID) == "" {
			return fmt.Errorf("document_list: %w: all scope requires workspace_id", ErrBadRequest)
		}
	case "library":
		// The library is intentionally global — a list spans every publisher
		// (that is the discovery surface). An optional project_id narrows to
		// one publisher's namespace. (epic:skill-library, sty_5678cc4b)
	}
	return nil
}

// authorizeListScope is the authorization chokepoint for a scoped
// document_list — the list-side counterpart of authorizeRead. For a
// non-system scope it requires a bearer and verifies the caller is a member of
// the workspace the list is bound to. In-process invocations (no authStore
// wired) skip the check, exactly like authorizeRead.
//
// For project scope it now enforces the project capability matrix (≥ read)
// via effectiveProjectRole — the per-PROJECT role gate the sty_2fa6f087 STUB
// anticipated (epic:user-admin, sty_c96cc77f). Workspace + all scope keep the
// workspace-membership check.
func authorizeListScope(ctx context.Context, scope document.Scope, wsID, pjID string) error {
	if authStore == nil {
		return nil
	}
	if scope == document.ScopeSystem || scope == "" {
		return nil
	}
	u := auth.FromContext(ctx)
	if u == nil {
		return fmt.Errorf("document_list: %w: bearer required for %s scope", ErrUnauthorized, scope)
	}
	if scope == document.ScopeUser {
		// User-scope list is the caller's own overlay; effectiveList keys it
		// to the caller, never crossing users.
		return nil
	}
	if scope == document.ScopeLibrary {
		// Library lists are open to any authenticated caller; the store
		// filter narrows rows to audience='all' (epic:skill-library).
		return nil
	}
	if workspaceStore == nil {
		return nil
	}
	if scope == document.ScopeProject {
		return enforceProjectScope(ctx, wsID, pjID, "document_list", project.RoleRead)
	}
	if wsID == "" {
		return fmt.Errorf("document_list: %w: %s scope requires workspace_id", ErrBadRequest, scope)
	}
	// Global admin is admin everywhere (docs/project-rbac.md) — it may list any
	// workspace's documents without an explicit membership row (sty_3c2f02bf).
	if callerIsGlobalAdmin(ctx) {
		return nil
	}
	if _, err := workspaceStore.GetRole(ctx, wsID, u.ID); err != nil {
		if errors.Is(err, workspace.ErrMemberNotFound) {
			return fmt.Errorf("document_list: %w: user not a member of workspace %s", ErrForbidden, wsID)
		}
		return err
	}
	return nil
}

// mcpForbidsType reports whether a (non-id) document_upsert of reqType arriving
// over transport t must be refused — operational content writes
// (document/skill/principle) are kept off the MCP surface (sty_f302bd8b) so the
// local agent's review skill can't be bypassed. Story and task writes, and any
// non-MCP transport, pass. An id-addressed patch is handled by the caller
// (req.ID != "") before this is consulted.
// systemChangelogWriteAllowed reports whether this is the single system-scope
// write the verb layer authorizes: a type:changelog (release-notes) write by a
// global admin arriving OFF the MCP tool surface. The changelog collapsed out
// of its dedicated verbs into a generic system-scope document (sty_d2b25c4b),
// and authorizeWrite refuses every system write — so without this the
// /changelog page would have no runtime write path. The transport boundary is
// the same one mcpForbidsType draws: the HTTP exec / in-process CLI passes, an
// agent's MCP tool call does not. Every other system write stays read-only.
func systemChangelogWriteAllowed(ctx context.Context, scope document.Scope, reqType string) bool {
	return scope == document.ScopeSystem &&
		reqType == document.TypeChangelog &&
		auth.TransportFromContext(ctx) != auth.TransportMCP &&
		callerIsGlobalAdmin(ctx)
}

func mcpForbidsType(t auth.Transport, reqType string) bool {
	if t != auth.TransportMCP {
		return false
	}
	switch strings.TrimSpace(strings.ToLower(reqType)) {
	case string(document.TypeStory), string(document.TypeTask):
		return false
	default:
		return true
	}
}

// principleTagPrefix marks a document as a principle (a principles:* layer tag).
// Principles are stored as type=document rows, so they are identified by tag,
// not by a distinct type.
const principleTagPrefix = "principles:"

// mcpReadForbiddenDoc reports whether a fetched document must NOT be returned
// over the MCP transport. Skills and principles are code-aware, client-owned
// knowledge (epic:authoring-as-config): the agent reads them client-side (skill
// sync, `satellites document index`, the always-context hook), so they are off
// the MCP surface entirely — read as well as write. Stories, tasks, and plain
// reference documents stay readable. Any non-MCP transport (CLI exec /
// in-process / portal) passes.
func mcpReadForbiddenDoc(t auth.Transport, docType string, tags []string) bool {
	if t != auth.TransportMCP {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(docType), string(document.TypeSkill)) {
		return true
	}
	for _, tag := range tags {
		if strings.HasPrefix(strings.TrimSpace(tag), principleTagPrefix) {
			return true
		}
	}
	return false
}

// filterMCPReadable drops skill/principle rows from a list result over the MCP
// transport, so a mixed type=document listing still returns reference docs while
// principles do not leak. A non-MCP transport returns the slice unchanged.
func filterMCPReadable(t auth.Transport, items []document.Document) []document.Document {
	if t != auth.TransportMCP {
		return items
	}
	out := make([]document.Document, 0, len(items))
	for _, d := range items {
		if mcpReadForbiddenDoc(t, d.Type, d.Tags) {
			continue
		}
		out = append(out, d)
	}
	return out
}

// mcpForbidsSkillList reports whether a list/count explicitly scoped to
// type=skill arriving over MCP must be refused outright (rather than silently
// returning an empty set) — the caller asked for skills, which are off MCP.
func mcpForbidsSkillList(t auth.Transport, reqType string) bool {
	return t == auth.TransportMCP && strings.EqualFold(strings.TrimSpace(reqType), string(document.TypeSkill))
}

func invokeDocumentUpsert(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if documentStore == nil {
		return nil, fmt.Errorf("document_upsert: store not configured")
	}
	var req DocumentUpsertRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("document_upsert: %w: %v", ErrBadRequest, err)
		}
	}

	// MCP keeps operational content writes off its surface (sty_f302bd8b): a
	// document/skill/principle content upsert must run through the CLI upload
	// path, which invokes the local agent's content-review skill — a
	// server-side MCP write would bypass it. Story/task writes and id-addressed
	// patches pass; the CLI exec path and in-process callers leave the
	// transport unset and pass.
	if req.ID == "" && mcpForbidsType(auth.TransportFromContext(ctx), req.Type) {
		typ := strings.TrimSpace(req.Type)
		if typ == "" {
			typ = "document"
		}
		return nil, fmt.Errorf("document_upsert: %w: %q content is uploaded with the CLI (`satellites document|skill|principle upload`), not over MCP — MCP is read + setup/init", ErrForbidden, typ)
	}

	// Workspace is not a valid sharing scope for skills — the "shared across a
	// workspace's repos" model is unworkable; cross-project reuse is the library's
	// job (publish/adopt). Project-scoped substrate is keyed by project, not
	// workspace (sty_c6de961e).
	if reason := workspaceSkillRefused(req.Type, req.Scope); reason != "" {
		return nil, fmt.Errorf("document_upsert: %w: %s", ErrBadRequest, reason)
	}

	// Behaviour-kind upserts (skill / workflow / principle) require a
	// content-bound review attestation the CLI `upload` mints AFTER the per-type
	// reviewer accepts (sty_e6226180). This refuses the silent exec/raw bypass —
	// a behaviour write that skips review. Stories, tasks, free-form documents,
	// id-addressed patches, and tag/metadata-only patches whose body is unchanged
	// (sty_dc44e359 — the barrier gates CONTENT, not tags) are exempt.
	if req.ID == "" && reviewRequiredKind(req) && !behaviourBodyUnchanged(ctx, req) {
		if err := verifyReviewAttestation(req); err != nil {
			return nil, err
		}
	}

	// Mode 1: patch by id. Currently only stories support id-addressed
	// upsert; documents are key-addressed via (scope, name).
	if strings.TrimSpace(req.ID) != "" {
		return upsertByID(ctx, req)
	}

	// Mode 2: create a new story.
	if req.Type == document.TypeStory {
		return createStory(ctx, req)
	}

	// Mode 2b: create a new task. Always id-less; tasks are minted with
	// a fresh tsk_<id>. parent_id is optional (a top-level project object);
	// when set it must reference a story (epic:project-tasks).
	if req.Type == document.TypeTask {
		return createTask(ctx, req)
	}

	// Mode 3: key-addressed document upsert (pre-unification behaviour).
	if strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("document_upsert: %w: name required", ErrBadRequest)
	}
	scope, err := parseScope(req.Scope)
	if err != nil {
		return nil, err
	}
	key := document.Key{Scope: scope, WorkspaceID: req.WorkspaceID, ProjectID: req.ProjectID, Name: req.Name}
	if scope == document.ScopeLibrary && strings.TrimSpace(key.ProjectID) == "" {
		return nil, fmt.Errorf("document_upsert: %w: library scope requires project_id (the publisher project)", ErrBadRequest)
	}
	if scope == document.ScopeUser {
		// A user override is keyed to the caller, never a request-body field.
		key.UserID = callerUserID(ctx)
		if key.UserID == "" {
			return nil, fmt.Errorf("document_upsert: %w: user scope requires a caller identity", ErrBadRequest)
		}
	}
	// Project scope binds to a workspace, but the client no longer authors the
	// workspace_id — repo-local substrate carries only project_id (resolved
	// from .satellites/satellites.toml). When omitted, derive it from the
	// project row, mirroring createStory. Backward-compatible: an explicit
	// workspace_id (the pre-relocation path-encoded form) is left untouched.
	if scope == document.ScopeProject && strings.TrimSpace(key.WorkspaceID) == "" {
		if strings.TrimSpace(key.ProjectID) == "" {
			return nil, fmt.Errorf("document_upsert: %w: project scope requires project_id", ErrBadRequest)
		}
		if projectStore == nil {
			return nil, fmt.Errorf("document_upsert: project store not configured")
		}
		p, err := projectStore.GetByID(ctx, key.ProjectID)
		if err != nil {
			return nil, fmt.Errorf("document_upsert: resolve workspace: %w", err)
		}
		key.WorkspaceID = p.WorkspaceID
	}
	// A workflow upsert is a privileged write — it changes the PROCESS that
	// governs every story — so it requires project-admin, stricter than the
	// RoleWrite an ordinary project document needs (sty_fc39ca77 AC2). Detected
	// from the kind:workflow tag/frontmatter (a workflow stores as type:skill),
	// enforced before the generic write authz.
	if isWorkflowUpsert(req) && key.Scope == document.ScopeProject {
		if err := enforceProjectScope(ctx, key.WorkspaceID, key.ProjectID, "workflow upsert", project.RoleAdmin); err != nil {
			return nil, err
		}
	}
	// A changelog (release notes) is a system-scope type:changelog document.
	// authorizeWrite refuses every system write via verbs, so the changelog —
	// collapsed out of its dedicated verbs into a generic document — would have
	// no runtime write path. Permit just this one, on the trusted operator
	// surface only. Every other system write stays read-only.
	sysChangelog := systemChangelogWriteAllowed(ctx, key.Scope, req.Type)
	if !sysChangelog {
		if err := authorizeWrite(ctx, key); err != nil {
			return nil, err
		}
	}
	doc, v, err := documentStore.Upsert(ctx, document.UpsertInput{
		Key:                key,
		Type:               req.Type,
		Body:               req.Body,
		CreatedBy:          callerUserID(ctx),
		SystemWriteAllowed: sysChangelog,
	}, time.Now().UTC())
	if err != nil {
		return nil, mapStoreError(err, "document_upsert")
	}
	// Tags are document-level metadata (not versioned with the body).
	// Apply only when the request carries them — nil means "leave alone",
	// which matches the patch semantics elsewhere in this surface.
	if req.Tags != nil {
		doc2, err := documentStore.SetDocumentTags(ctx, doc.ID, *req.Tags, time.Now().UTC())
		if err != nil {
			return nil, mapStoreError(err, "document_upsert")
		}
		doc = doc2
	}
	// Headline is document-level metadata too — apply only when the request
	// carries it (nil = leave alone), the same patch semantics as tags.
	if req.Headline != nil {
		doc2, err := documentStore.SetDocumentHeadline(ctx, doc.ID, *req.Headline, time.Now().UTC())
		if err != nil {
			return nil, mapStoreError(err, "document_upsert")
		}
		doc = doc2
	}
	return json.Marshal(DocumentUpsertResponse{Document: doc, Version: v})
}

// createStory is the document_upsert path for {type:"story"} with no id.
// Resolves the workspace from the project, mints a fresh sty_<id>,
// inserts the documents row + v1 body, and fires the reviewer + ledger
// + summary hooks that the pre-unification story_create used to.
func createStory(ctx context.Context, req DocumentUpsertRequest) (json.RawMessage, error) {
	if strings.TrimSpace(req.ProjectID) == "" {
		return nil, fmt.Errorf("document_upsert: %w: project_id required for type=story", ErrBadRequest)
	}
	if strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("document_upsert: %w: name (title) required for type=story", ErrBadRequest)
	}
	wsID := req.WorkspaceID
	if wsID == "" {
		if projectStore == nil {
			return nil, fmt.Errorf("document_upsert: project store not configured")
		}
		p, err := projectStore.GetByID(ctx, req.ProjectID)
		if err != nil {
			return nil, fmt.Errorf("document_upsert: resolve workspace: %w", err)
		}
		wsID = p.WorkspaceID
	}
	// A story cannot be created under a terminal (done/cancelled) epic — reopen
	// the epic first (sty_d43af8dc). A missing/non-terminal parent is unaffected.
	if reason := refuseTerminalEpicParent(ctx, strDeref(req.ParentID)); reason != "" {
		return nil, fmt.Errorf("document_upsert: %w: %s", ErrBadRequest, reason)
	}
	in := document.CreateStoryInput{
		ProjectID:          req.ProjectID,
		WorkspaceID:        wsID,
		Title:              req.Name,
		Body:               req.Body,
		AcceptanceCriteria: strDeref(req.AcceptanceCriteria),
		// Status on create is honoured only for a non-api-key caller; an api-key
		// caller's value is dropped and the store defaults to "backlog" (the
		// workflow's initial state). Status thereafter moves only via the
		// reviewer gate's status_transition (sty_42d13ae4).
		Status:    createStatus(ctx, req.Status),
		Priority:  strDeref(req.Priority),
		Category:  strDeref(req.Category),
		ParentID:  strDeref(req.ParentID),
		Tags:      sliceDeref(req.Tags),
		CreatedBy: callerUserID(ctx),
	}
	d, err := documentStore.CreateStory(ctx, in, time.Now().UTC())
	if err != nil {
		return nil, mapStoreError(err, "document_upsert")
	}

	// Reviewer + ledger + summary hooks — same chain story_create used
	// to fire pre-unification. The internal envelope is story-shaped so
	// existing reviewer prompts keep working without an md change.
	storyAfterCreate(ctx, d, req.Body)

	return json.Marshal(DocumentUpsertResponse{
		Document: d,
		Version:  document.Version{DocumentID: d.ID, Version: 1, Body: req.Body, Status: document.StatusActive, CreatedAt: d.CreatedAt, CreatedBy: callerUserID(ctx)},
	})
}

// createTask is the document_upsert path for {type:"task"} with no id.
// A task is a top-level project object (epic:project-tasks); parent_id is
// OPTIONAL — when set it must be a type='story' row in the same project.
// Tasks share the document_versions body channel but fire only their own
// task ledger hook, NOT the story reviewer/summary signals.
func createTask(ctx context.Context, req DocumentUpsertRequest) (json.RawMessage, error) {
	if strings.TrimSpace(req.ProjectID) == "" {
		return nil, fmt.Errorf("document_upsert: %w: project_id required for type=task", ErrBadRequest)
	}
	if strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("document_upsert: %w: name (title) required for type=task", ErrBadRequest)
	}
	wsID := req.WorkspaceID
	if wsID == "" {
		if projectStore == nil {
			return nil, fmt.Errorf("document_upsert: project store not configured")
		}
		p, err := projectStore.GetByID(ctx, req.ProjectID)
		if err != nil {
			return nil, fmt.Errorf("document_upsert: resolve workspace: %w", err)
		}
		wsID = p.WorkspaceID
	}
	in := document.CreateTaskInput{
		ProjectID:   req.ProjectID,
		WorkspaceID: wsID,
		ParentID:    strDeref(req.ParentID),
		Title:       req.Name,
		Body:        req.Body,
		Status:      strDeref(req.Status),
		Priority:    strDeref(req.Priority),
		Tags:        sliceDeref(req.Tags),
		CreatedBy:   callerUserID(ctx),
	}
	d, err := documentStore.CreateTask(ctx, in, time.Now().UTC())
	if err != nil {
		return nil, mapStoreError(err, "document_upsert")
	}
	taskAfterCreate(ctx, d)
	return json.Marshal(DocumentUpsertResponse{
		Document: d,
		Version:  document.Version{DocumentID: d.ID, Version: 1, Body: req.Body, Status: document.StatusActive, CreatedAt: d.CreatedAt, CreatedBy: callerUserID(ctx)},
	})
}

// upsertTaskByID patches a top-level task by id (epic:project-tasks). It reuses
// the shared documents-row update path (UpdateStory accepts task rows) but skips
// the story/epic reparent guards and fires only the task ledger hook. Status is
// dropped for api-key callers — the same reviewer-only rule as stories: a task's
// status moves through its reviewer gate's status_transition row, not here.
func upsertTaskByID(ctx context.Context, req DocumentUpsertRequest) (json.RawMessage, error) {
	status := req.Status
	if !upsertStatusHonoured(ctx) {
		status = nil
	}
	patch := document.UpdateStoryPatch{
		Title:     ptrIfNonEmpty(req.Name),
		Body:      ptrIfPresent(req.Body, len(req.Body) > 0),
		Status:    status,
		Priority:  req.Priority,
		ParentID:  req.ParentID,
		Tags:      req.Tags,
		CreatedBy: callerUserID(ctx),
	}
	d2, err := documentStore.UpdateStory(ctx, req.ID, patch, time.Now().UTC())
	if err != nil {
		return nil, mapStoreError(err, "document_upsert")
	}
	body := ""
	if d2.LatestVersion > 0 {
		_, b, _ := documentStore.GetByIDWithLatestBody(ctx, d2.ID)
		body = b
	}
	taskAfterUpdate(ctx, d2)
	return json.Marshal(DocumentUpsertResponse{
		Document: d2,
		Version:  document.Version{DocumentID: d2.ID, Version: d2.LatestVersion, Body: body, Status: document.StatusActive},
	})
}

// upsertByID patches the row at req.ID. For type='story' rows this maps
// to UpdateStory; for type='document' rows it is rejected (documents
// use key-addressed upsert).
func upsertByID(ctx context.Context, req DocumentUpsertRequest) (json.RawMessage, error) {
	d, _, err := documentStore.GetByIDWithLatestBody(ctx, req.ID)
	if err != nil {
		return nil, mapStoreError(err, "document_upsert")
	}
	// Top-level tasks (epic:project-tasks) also patch by id, on their own path
	// — no epic-reparent freeze, task ledger hook instead of the story signals.
	if d.Type == document.TypeTask {
		return upsertTaskByID(ctx, req)
	}
	if d.Type != document.TypeStory {
		return nil, fmt.Errorf("document_upsert: %w: id-addressed upsert is only supported for stories and tasks (id=%s is type=%s)", ErrBadRequest, req.ID, d.Type)
	}
	// Epic membership freezes once a story has started: parent_id may change only
	// while the story is still backlog/ready (sty_409c0af8). This keeps the epic
	// objective the re-anchor propagates (sty_a8a6afc7) from being swapped out
	// from under in-flight work. Every other field stays freely patchable.
	if reason := epicReparentRefusal(d.Status, d.ParentID, req.ParentID); reason != "" {
		return nil, fmt.Errorf("document_upsert: %w: %s", ErrBadRequest, reason)
	}
	// ...and never re-parent INTO a terminal epic — even a backlog/ready story the
	// freeze guard above permits (sty_d43af8dc).
	if req.ParentID != nil && *req.ParentID != d.ParentID {
		if reason := refuseTerminalEpicParent(ctx, *req.ParentID); reason != "" {
			return nil, fmt.Errorf("document_upsert: %w: %s", ErrBadRequest, reason)
		}
	}
	// Status on document_upsert is honoured only for a non-api-key caller — the
	// portal UI's JWT session, or an in-process call. An api-key caller (the
	// agent over MCP/exec, or the gate's minted reviewer key) gets the field
	// dropped: a story's status then moves only through the reviewer gate's
	// status_transition ledger row (see invokeLedgerAppend), so a reviewer-capable
	// credential cannot `document_upsert {status}` and self-accept, bypassing the
	// gate (sty_42d13ae4).
	status := req.Status
	if !upsertStatusHonoured(ctx) {
		status = nil
	}

	var beforeEnv StoryEnvelope
	beforeEnv = NewStoryEnvelope(d, "")
	if d.LatestVersion > 0 {
		_, bodyBefore, _ := documentStore.GetByIDWithLatestBody(ctx, req.ID)
		beforeEnv = NewStoryEnvelope(d, bodyBefore)
	}

	patch := document.UpdateStoryPatch{
		Title:              ptrIfNonEmpty(req.Name),
		Body:               ptrIfPresent(req.Body, len(req.Body) > 0),
		AcceptanceCriteria: req.AcceptanceCriteria,
		Status:             status, // dropped for api-key callers; status moves via the gate's status_transition (sty_42d13ae4)
		Priority:           req.Priority,
		Category:           req.Category,
		ParentID:           req.ParentID,
		Tags:               req.Tags,
		CreatedBy:          callerUserID(ctx),
	}
	d2, err := documentStore.UpdateStory(ctx, req.ID, patch, time.Now().UTC())
	if err != nil {
		return nil, mapStoreError(err, "document_upsert")
	}
	body := ""
	if d2.LatestVersion > 0 {
		_, b, _ := documentStore.GetByIDWithLatestBody(ctx, d2.ID)
		body = b
	}
	storyAfterUpdate(ctx, beforeEnv, d2, body)

	return json.Marshal(DocumentUpsertResponse{
		Document: d2,
		Version:  document.Version{DocumentID: d2.ID, Version: d2.LatestVersion, Body: body, Status: document.StatusActive},
	})
}

// epicReparentRefusal reports whether a story-patch would change epic membership
// (parent_id) on a story whose status forbids it, returning a clear refusal
// reason or "" when the change is allowed. Membership is frozen once a story has
// started: it may change only while the story is still backlog or ready. A patch
// that omits parent_id (newParent == nil) or sets it to the current value is not
// a membership change and is always allowed; clearing parent_id to "" on a
// started story IS a change and is refused.
func epicReparentRefusal(currentStatus, currentParent string, newParent *string) string {
	if newParent == nil || *newParent == currentParent {
		return "" // not changing epic membership
	}
	switch currentStatus {
	case "backlog", "ready":
		return "" // not started — free to re-parent
	default:
		return fmt.Sprintf("epic membership is frozen once a story has started (status=%s) — reopen it to backlog/ready before re-parenting", currentStatus)
	}
}

// epicChildRefusal reports whether a story may be attached to a parent epic with
// the given status, returning a refusal reason or "". A terminal epic (done or
// cancelled) cannot acquire new children — it reached done only because every
// child was terminal (satellites-parent-close-review), so a new child would
// silently reopen finished work. A missing parent (parentFound == false) is left
// to the reviewer's missing-parent finding — write-time referential integrity is
// intentionally not enforced (sty_d43af8dc).
// workspaceSkillRefused reports why a workspace-scoped skill upsert is rejected,
// or "" when allowed. Skills resolve at system / project / library scope; the
// workspace scope was the unworkable "shared across a workspace's repos" model
// (sty_c6de961e). Cross-project skill reuse is the library's job (publish/adopt).
func workspaceSkillRefused(typ, scope string) string {
	if strings.EqualFold(strings.TrimSpace(typ), string(document.TypeSkill)) &&
		strings.EqualFold(strings.TrimSpace(scope), string(document.ScopeWorkspace)) {
		return "workspace is not a valid scope for skills — use project scope, or publish to the library for cross-project reuse"
	}
	return ""
}

func epicChildRefusal(parentStatus string, parentFound bool) string {
	if !parentFound {
		return ""
	}
	switch parentStatus {
	case "done", "cancelled":
		return fmt.Sprintf("cannot attach a story to a %s epic — reopen it (e.g. satellites story set-status <epic> backlog) before adding children", parentStatus)
	default:
		return ""
	}
}

// refuseTerminalEpicParent loads parentID and applies epicChildRefusal. An empty
// id, or a parent that cannot be loaded, yields "" (fail open to the existing
// verbatim-parent behaviour); only an existing terminal parent is refused.
func refuseTerminalEpicParent(ctx context.Context, parentID string) string {
	if strings.TrimSpace(parentID) == "" {
		return ""
	}
	p, _, err := documentStore.GetByIDWithLatestBody(ctx, parentID)
	if err != nil {
		return ""
	}
	return epicChildRefusal(p.Status, true)
}

func strDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// upsertStatusHonoured reports whether a document_upsert may set a story's
// status. The check is TRANSPORT-based, not just credential-based (sty_4f5148c4):
//   - MCP transport → NEVER. An agent on an operator's OAuth MCP session must not
//     patch status; the api-key drop alone (sty_42d13ae4) left that OAuth-MCP hole
//     open (it's how this epic was reopened). Status moves via the gate's
//     status_transition ledger row, or the operator's portal/CLI.
//   - api-key caller (exec / gate key) → NEVER (sty_42d13ae4, preserved).
//   - otherwise (portal UI JWT session, in-process/CLI) → yes.
func upsertStatusHonoured(ctx context.Context) bool {
	if auth.TransportFromContext(ctx) == auth.TransportMCP {
		return false
	}
	return auth.APIKeyRoleFromContext(ctx) == ""
}

// createStatus returns the create-time status an upsert caller may set: the
// requested value for a non-api-key caller, "" (the store then defaults to
// backlog) for an api-key caller.
func createStatus(ctx context.Context, req *string) string {
	if !upsertStatusHonoured(ctx) {
		return ""
	}
	return strDeref(req)
}

func sliceDeref(p *[]string) []string {
	if p == nil {
		return nil
	}
	return *p
}

// ptrIfNonEmpty returns &s when s is non-empty, nil otherwise. Used so
// the document_upsert request shape can express "set this field" vs
// "leave alone" for string fields whose JSON omission ⇒ "leave alone".
func ptrIfNonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ptrIfPresent is the pointer-shaped twin for fields where the caller
// might genuinely want to set an empty value (e.g. Body=""). The caller
// passes a boolean explicitly indicating presence.
func ptrIfPresent(s string, present bool) *string {
	if !present {
		return nil
	}
	return &s
}

func invokeDocumentDelete(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if documentStore == nil {
		return nil, fmt.Errorf("document_delete: store not configured")
	}
	var req DocumentDeleteRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("document_delete: %w: %v", ErrBadRequest, err)
		}
	}

	// ID-addressed delete. Behaviour is chosen by stored type so
	// pre-unification semantics are preserved: stories were hard-deleted,
	// documents soft-tombstoned.
	if strings.TrimSpace(req.ID) != "" {
		d, body, err := documentStore.GetByIDWithLatestBody(ctx, req.ID)
		if err != nil {
			return nil, mapStoreError(err, "document_delete")
		}
		// Skills/principles are client-owned executable configuration: MCP can
		// neither read nor write them, so it must not delete them either —
		// otherwise the one remaining side door undoes review-gated uploads
		// (sty_1c234792 surface contract). The CLI exec path passes.
		if mcpReadForbiddenDoc(auth.TransportFromContext(ctx), d.Type, d.Tags) {
			return nil, fmt.Errorf("document_delete: %w: skills/principles are managed client-side (`satellites exec document_delete` after review), not over MCP", ErrForbidden)
		}
		if d.Type == document.TypeStory {
			if err := documentStore.HardDelete(ctx, req.ID); err != nil {
				return nil, mapStoreError(err, "document_delete")
			}
			// Story shape consumers (portal, reviewers) expect the
			// pre-deletion document plus a synthetic "deleted" version
			// row so the response stays shape-compatible with the
			// document_delete soft-delete path.
			return json.Marshal(DocumentDeleteResponse{
				Document: d,
				Version:  document.Version{DocumentID: d.ID, Version: d.LatestVersion, Body: body, Status: document.StatusDeleted, CreatedBy: callerUserID(ctx)},
			})
		}
		// Document soft-delete by id. Authorize with a key reconstructed from
		// the row (membership is by project_id/workspace_id, which tolerates the
		// NULL workspace_id project rows carry), then tombstone BY ID. We do NOT
		// route through Delete(key): mig-0041 keys project substrate by
		// (project_id, name) with workspace_id NULL, but Key.Validate still
		// requires workspace_id for ScopeProject, so Delete(reconstructed-key)
		// would reject every project document with a scope-coherence error. The
		// id already identifies the exact row — TombstoneByID removes it (sty_52f62518).
		key := document.Key{Scope: d.Scope, WorkspaceID: d.WorkspaceID, ProjectID: d.ProjectID, UserID: d.UserID, Name: d.Name}
		if err := authorizeWrite(ctx, key); err != nil {
			return nil, err
		}
		doc2, v, err := documentStore.TombstoneByID(ctx, req.ID, callerUserID(ctx), time.Now().UTC())
		if err != nil {
			return nil, mapStoreError(err, "document_delete")
		}
		return json.Marshal(DocumentDeleteResponse{Document: doc2, Version: v})
	}

	if strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("document_delete: %w: name or id required", ErrBadRequest)
	}
	scope, err := parseScope(req.Scope)
	if err != nil {
		return nil, err
	}
	key := document.Key{Scope: scope, WorkspaceID: req.WorkspaceID, ProjectID: req.ProjectID, Name: req.Name}
	if scope == document.ScopeUser {
		key.UserID = callerUserID(ctx)
		if key.UserID == "" {
			return nil, fmt.Errorf("document_delete: %w: user scope requires a caller identity", ErrBadRequest)
		}
	}
	if err := authorizeWrite(ctx, key); err != nil {
		return nil, err
	}
	// Same MCP bound as the id-addressed path: a skill/principle addressed by
	// (scope, name) is still client-owned configuration.
	if auth.TransportFromContext(ctx) == auth.TransportMCP {
		if res, gErr := documentStore.Get(ctx, key, document.GetOptions{}); gErr == nil {
			if mcpReadForbiddenDoc(auth.TransportMCP, res.Document.Type, res.Document.Tags) {
				return nil, fmt.Errorf("document_delete: %w: skills/principles are managed client-side (`satellites exec document_delete` after review), not over MCP", ErrForbidden)
			}
		}
	}
	doc, v, err := documentStore.Delete(ctx, key, callerUserID(ctx), false, time.Now().UTC())
	if err != nil {
		return nil, mapStoreError(err, "document_delete")
	}
	return json.Marshal(DocumentDeleteResponse{Document: doc, Version: v})
}

// authorizeWrite is the write-path twin of authorizeRead: system writes
// from user-facing verbs are flatly rejected, workspace/project writes
// require workspace membership. CLI-local in-process invocations
// (authStore unwired) skip the membership check; the store-layer
// ErrScopeReadonly still trips for scope=system regardless.
func authorizeWrite(ctx context.Context, key document.Key) error {
	if key.Scope == document.ScopeSystem {
		return fmt.Errorf("document write: %w: system scope is read-only via verbs", ErrForbidden)
	}
	if authStore == nil {
		return nil
	}
	u := auth.FromContext(ctx)
	if u == nil {
		return fmt.Errorf("document write: %w: bearer required for %s scope", ErrUnauthorized, key.Scope)
	}
	// User scope: a caller writes only their own override rows (sty_cbeeb452).
	if key.Scope == document.ScopeUser {
		if key.UserID != u.ID {
			return fmt.Errorf("document write: %w: user scope is writable only by its owner", ErrForbidden)
		}
		return nil
	}
	if workspaceStore == nil {
		return nil
	}
	// Project scope: enforce the project capability matrix (≥ write).
	if key.Scope == document.ScopeProject {
		return enforceProjectScope(ctx, key.WorkspaceID, key.ProjectID, "document write", project.RoleWrite)
	}
	// Library scope: the one publisher-namespace check (epic:skill-library,
	// sty_5678cc4b). A library write lands only in the caller's OWN publisher
	// namespace — the project named by project_id — so the caller needs
	// ≥ write there; any other namespace is refused.
	if key.Scope == document.ScopeLibrary {
		if err := enforceProjectScope(ctx, "", key.ProjectID, "document write", project.RoleWrite); err != nil {
			return fmt.Errorf("%w: a library write is confined to the caller's own publisher namespace (publisher = project %s)", err, key.ProjectID)
		}
		return nil
	}
	if key.WorkspaceID == "" {
		return fmt.Errorf("document write: %w: %s scope requires workspace_id", ErrBadRequest, key.Scope)
	}
	if _, err := workspaceStore.GetRole(ctx, key.WorkspaceID, u.ID); err != nil {
		if errors.Is(err, workspace.ErrMemberNotFound) {
			return fmt.Errorf("document write: %w: user not a member of workspace %s", ErrForbidden, key.WorkspaceID)
		}
		return err
	}
	return nil
}

// mapStoreError translates store-layer sentinels into verb-layer ones
// so the exec transport can map to canonical HTTP statuses.
func mapStoreError(err error, prefix string) error {
	switch {
	case errors.Is(err, document.ErrScopeReadonly):
		return fmt.Errorf("%s: %w: %v", prefix, ErrForbidden, err)
	case errors.Is(err, document.ErrScopeMismatch):
		return fmt.Errorf("%s: %w: %v", prefix, ErrBadRequest, err)
	case errors.Is(err, document.ErrNotFound):
		return fmt.Errorf("%s: %w: %v", prefix, ErrNotFound, err)
	default:
		return err
	}
}

func marshalDocumentGet(ctx context.Context, res document.GetResult, key document.Key, opts document.GetOptions, req *DocumentGetRequest) (json.RawMessage, error) {
	if res.Versions == nil {
		res.Versions = []document.Version{}
	}
	resp := DocumentGetResponse{
		Document:      res.Document,
		Versions:      res.Versions,
		ResolvedScope: string(key.Scope),
	}
	// Templating only applies to single-version reads. version=all
	// returns the chain raw — rendering historical bodies against
	// today's variables silently rewrites the operator's past.
	if !opts.AllVersions && len(res.Versions) == 1 {
		v := res.Versions[0]
		parsed := documentTemplateCache.Get(res.Document.ID, v.Version, v.Body)
		resolver := newTemplateResolver(ctx, key, req)
		rendered, unresolved := parsed.Render(resolver)
		resp.RawBody = v.Body
		resp.RenderedBody = rendered
		if len(unresolved) > 0 {
			resp.UnresolvedVars = unresolved
		}
	}
	return json.Marshal(resp)
}

// newTemplateResolver builds the per-call resolver document_get uses
// for {{name}} substitution. Order (highest to lowest precedence):
//
//  1. system variables (computed, non-overridable)
//  2. project-scope variables  (when project_id+workspace_id supplied)
//  3. workspace-scope variables (when workspace_id supplied)
//
// System vars come first by design: a workspace operator setting a
// variable named "version" cannot shadow the platform's reported
// version. Unresolved names are surfaced on unresolved_vars; the call
// does not fail.
func newTemplateResolver(ctx context.Context, key document.Key, req *DocumentGetRequest) document.Resolver {
	wsID, pjID := req.WorkspaceID, req.ProjectID
	if wsID == "" && key.Scope != document.ScopeSystem {
		wsID = key.WorkspaceID
	}
	if pjID == "" && key.Scope == document.ScopeProject {
		pjID = key.ProjectID
	}
	return document.ResolverFunc(func(name string) (string, bool) {
		if v, ok := systemVariableResolve(ctx, name); ok {
			return v, true
		}
		if variableStore == nil {
			return "", false
		}
		if wsID != "" && pjID != "" {
			if v, err := variableStore.Get(ctx, variable.Key{
				Scope: variable.ScopeProject, WorkspaceID: wsID, ProjectID: pjID, Name: name,
			}); err == nil {
				return v.Value, true
			}
		}
		if wsID != "" {
			if v, err := variableStore.Get(ctx, variable.Key{
				Scope: variable.ScopeWorkspace, WorkspaceID: wsID, Name: name,
			}); err == nil {
				return v.Value, true
			}
		}
		return "", false
	})
}
