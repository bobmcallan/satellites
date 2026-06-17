// `satellites changelog upsert` — the runtime write path for a changelog
// (release-notes) entry.
//
// The changelog collapsed out of its dedicated table + four changelog_* verbs
// into a generic, system-scope type:changelog document (sty_d2b25c4b). The
// /changelog portal page reads those documents, but the collapse left no way to
// CREATE one at runtime: document_upsert refuses changelog content over MCP
// (its error names "the CLI"), and `document upload` is project-scope only.
// This restores the write path the collapse pointed at — composing the EXISTING
// document_upsert verb, NO new MCP verb (honors the no-new-mcp-verbs principle).
//
// Verb naming follows the upsert-native substrate: a single create-or-update
// verb keyed by name (one verb covers add + update); delete maps to the
// existing document_delete. The server permits this one system-scope write only
// on the trusted operator surface — a global admin off the MCP tool surface.
//
// The CLI must not import internal/document|verb (the transport layering
// guard); the request/response are local JSON-only structs mirroring the
// fields this command needs.

package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// changelogUpsertRequest is the JSON-only document_upsert payload. Field names
// track verb.DocumentUpsertRequest; the local declaration keeps the import
// surface narrow.
type changelogUpsertRequest struct {
	Type  string   `json:"type"`
	Scope string   `json:"scope"`
	Name  string   `json:"name"`
	Body  string   `json:"body"`
	Tags  []string `json:"tags"`
}

// changelogUpsertResponse mirrors the subset of the document_upsert response
// this command renders.
type changelogUpsertResponse struct {
	Document struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"document"`
}

func init() {
	var (
		configArg string
		userArg   string
		service   string
		from      string
		to        string
		effDate   string
		name      string
		body      string
		file      string
	)
	changelog := &cobra.Command{
		Use:   "changelog",
		Short: "Changelog (release-notes) operations — system-scope type:changelog documents",
	}
	changelog.PersistentFlags().StringVar(&configArg, "config", "", "Path to satellites.toml (overrides $SATELLITES_CONFIG / .satellites/satellites.toml walk-up).")
	changelog.PersistentFlags().StringVar(&userArg, "user", "", "Caller user id (overrides $SATELLITES_USER_ID). Stamped onto verbs when dispatching in-process.")

	upsert := &cobra.Command{
		Use:   "upsert",
		Short: "Create or update a changelog entry (document_upsert, type:changelog, scope:system)",
		Long: "Create or update a changelog entry as a system-scope type:changelog document.\n" +
			"Composes the existing document_upsert verb (no new MCP verb). The body is read\n" +
			"from --body, else --file, else stdin. The write requires a global-admin caller\n" +
			"off the MCP surface; the new entry shows on the /changelog portal page immediately.",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			text, err := resolveChangelogBody(cmd, body, file)
			if err != nil {
				return err
			}
			upReq, err := buildChangelogUpsertRequest(service, from, to, effDate, name, text)
			if err != nil {
				return err
			}
			req, err := json.Marshal(upReq)
			if err != nil {
				return err
			}

			resp, err := dispatchVerb(ctx, "document_upsert", req, configArg, userArg)
			if err != nil {
				return fmt.Errorf("changelog upsert: %w", err)
			}

			var decoded changelogUpsertResponse
			if err := json.Unmarshal(resp, &decoded); err != nil || decoded.Document.Name == "" {
				fmt.Fprintf(out, "changelog upserted: %s (%s → %s)\n", upReq.Name, strings.TrimSpace(from), strings.TrimSpace(to))
				return nil
			}
			fmt.Fprintf(out, "changelog upserted: %s (%s) — %s %s → %s\n",
				decoded.Document.Name, decoded.Document.ID, strings.TrimSpace(service), strings.TrimSpace(from), strings.TrimSpace(to))
			return nil
		},
	}
	upsert.Flags().StringVar(&service, "service", "", "Service the entry covers — the service: tag (e.g. satellites, satellites-server)")
	upsert.Flags().StringVar(&from, "from", "", "Version the entry starts from — the version_from: tag")
	upsert.Flags().StringVar(&to, "to", "", "Version the entry ends at — the version_to: tag")
	upsert.Flags().StringVar(&effDate, "effective-date", "", "Effective date YYYY-MM-DD — the effective_date: tag (optional)")
	upsert.Flags().StringVar(&name, "name", "", "Entry key (cl_… name) — generated when omitted; pass to update an existing entry")
	upsert.Flags().StringVar(&body, "body", "", "Entry markdown body (overrides --file and stdin)")
	upsert.Flags().StringVar(&file, "file", "", "Read the entry markdown body from this file")
	changelog.AddCommand(upsert)
	register(changelog)
}

// buildChangelogUpsertRequest validates the flags and assembles the
// system-scope type:changelog document_upsert payload — the typed fields ride
// as tags (service: / version_from: / version_to: / effective_date:), the body
// is the entry markdown, and the name is the cl_… key (minted when omitted).
func buildChangelogUpsertRequest(service, from, to, effDate, name, body string) (changelogUpsertRequest, error) {
	service = strings.TrimSpace(service)
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if service == "" {
		return changelogUpsertRequest{}, fmt.Errorf("changelog upsert: --service is required (the service: tag)")
	}
	if from == "" || to == "" {
		return changelogUpsertRequest{}, fmt.Errorf("changelog upsert: --from and --to are required (the version_from:/version_to: tags)")
	}
	if strings.TrimSpace(body) == "" {
		return changelogUpsertRequest{}, fmt.Errorf("changelog upsert: body is empty — pass --body, --file, or pipe markdown via stdin")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		n, err := newChangelogName()
		if err != nil {
			return changelogUpsertRequest{}, err
		}
		name = n
	}
	tags := []string{"changelog", "service:" + service, "version_from:" + from, "version_to:" + to}
	if d := strings.TrimSpace(effDate); d != "" {
		tags = append(tags, "effective_date:"+d)
	}
	return changelogUpsertRequest{Type: "changelog", Scope: "system", Name: name, Body: body, Tags: tags}, nil
}

// resolveChangelogBody picks the entry body from --body, then --file, then
// stdin — the first non-empty source wins.
func resolveChangelogBody(cmd *cobra.Command, body, file string) (string, error) {
	if strings.TrimSpace(body) != "" {
		return body, nil
	}
	if strings.TrimSpace(file) != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("changelog upsert: read --file: %w", err)
		}
		return string(b), nil
	}
	b, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return "", fmt.Errorf("changelog upsert: read stdin: %w", err)
	}
	return string(b), nil
}

// newChangelogName mints a cl_<8hex> entry key, matching the changelog id
// convention the collapse migration preserved.
func newChangelogName() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("changelog upsert: mint name: %w", err)
	}
	return "cl_" + hex.EncodeToString(b), nil
}
