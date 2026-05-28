package reviewer

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"strconv"
	"strings"

	"github.com/bobmcallan/satellites/internal/document"
)

// LoadFromStore loads reviewer definitions from the documents store.
// Every documents row at (type='skill', scope='system') with the
// `kind:reviewer` tag is parsed as a Definition. The stored body
// carries the reviewer's frontmatter (tuning knobs) verbatim — for
// skill rows the boot reconciler does not strip the frontmatter.
//
// Boot wires this into the registry after the system-seed reconciler
// has run, so the store is guaranteed to hold the latest embedded
// reviewer bodies. Operators who edit reviewer prose via
// `document_upsert` (subject to scope=system being internal-seed-only)
// shape the live registry without a binary release.
func LoadFromStore(ctx context.Context, store *document.Store) (map[string]Definition, error) {
	if store == nil {
		return map[string]Definition{}, nil
	}
	res, err := store.List(ctx, document.ListFilter{
		Type:  document.TypeSkill,
		Scope: document.ScopeSystem,
		Tags:  []string{"kind:reviewer"},
	}, document.ListOptions{Limit: 200})
	if err != nil {
		return nil, fmt.Errorf("reviewer: list skills: %w", err)
	}
	out := map[string]Definition{}
	for _, doc := range res.Items {
		got, err := store.Get(ctx, document.Key{Scope: document.ScopeSystem, Name: doc.Name}, document.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("reviewer: get %s: %w", doc.Name, err)
		}
		if len(got.Versions) == 0 {
			return nil, fmt.Errorf("reviewer: %s: empty version chain", doc.Name)
		}
		def, err := parseDefinition([]byte(got.Versions[0].Body))
		if err != nil {
			return nil, fmt.Errorf("reviewer: parse %s: %w", doc.Name, err)
		}
		if def.Name == "" {
			def.Name = doc.Name
		}
		if _, exists := out[def.Name]; exists {
			return nil, fmt.Errorf("reviewer: duplicate name %q", def.Name)
		}
		out[def.Name] = def
	}
	return out, nil
}

// Load walks the given filesystem for *.md files and parses each
// into a Definition. The map is keyed by frontmatter `name:`.
// Duplicate names panic — collisions are bugs, not runtime errors.
//
// An empty filesystem (or one with no markdown files) returns an
// empty map and no error. The orchestrator treats that case as
// "no reviewers configured" — no LLM calls happen.
//
// Production callers use LoadFromStore; Load remains for unit-test
// callers that build a synthetic in-memory FS.
func Load(fsys fs.FS) (map[string]Definition, error) {
	out := map[string]Definition{}
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		body, err := fs.ReadFile(fsys, path)
		if err != nil {
			return fmt.Errorf("reviewer: read %s: %w", path, err)
		}
		def, err := parseDefinition(body)
		if err != nil {
			return fmt.Errorf("reviewer: parse %s: %w", path, err)
		}
		if def.Name == "" {
			return fmt.Errorf("reviewer: %s missing frontmatter `name:`", path)
		}
		if _, exists := out[def.Name]; exists {
			return fmt.Errorf("reviewer: duplicate name %q", def.Name)
		}
		out[def.Name] = def
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// parseDefinition extracts the YAML frontmatter scalars + the body
// that follows. The frontmatter parser handles only the shapes
// reviewer files actually use: `key: value` scalar lines between
// two `---` fences. Unknown keys are ignored so the format can
// grow without code churn.
func parseDefinition(body []byte) (Definition, error) {
	const fence = "---"
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var (
		def        Definition
		inMatter   bool
		matterDone bool
		bodyLines  []string
	)
	first := true
	for scanner.Scan() {
		line := scanner.Text()
		if first {
			first = false
			if strings.TrimSpace(line) == fence {
				inMatter = true
				continue
			}
			// No frontmatter at all; treat whole file as body.
			bodyLines = append(bodyLines, line)
			matterDone = true
			continue
		}
		if inMatter && !matterDone {
			if strings.TrimSpace(line) == fence {
				matterDone = true
				inMatter = false
				continue
			}
			k, v, ok := splitKV(line)
			if !ok {
				continue
			}
			switch k {
			case "name":
				def.Name = v
			case "enabled":
				def.Enabled = parseBool(v)
			case "model":
				def.Model = v
			case "max_tokens":
				if n, err := strconv.Atoi(v); err == nil {
					def.MaxTokens = n
				}
			}
			continue
		}
		bodyLines = append(bodyLines, line)
	}
	if err := scanner.Err(); err != nil {
		return Definition{}, fmt.Errorf("scan: %w", err)
	}
	def.Body = strings.TrimLeft(strings.Join(bodyLines, "\n"), "\n")
	return def, nil
}

func splitKV(line string) (string, string, bool) {
	idx := strings.IndexByte(line, ':')
	if idx <= 0 {
		return "", "", false
	}
	k := strings.TrimSpace(line[:idx])
	v := strings.TrimSpace(line[idx+1:])
	v = strings.Trim(v, `"'`)
	return k, v, true
}

func parseBool(v string) bool {
	switch strings.ToLower(v) {
	case "true", "yes", "1", "on":
		return true
	default:
		return false
	}
}
