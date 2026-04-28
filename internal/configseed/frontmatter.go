package configseed

import (
	"bytes"
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

// Frontmatter is the parsed YAML envelope from a markdown file.
// Loaders cast individual fields to typed shapes via mapstructure-style
// helpers in parsers.go.
type Frontmatter map[string]any

// ErrNoFrontmatter is returned when a file lacks the leading
// "---\n…\n---\n" envelope. Loaders treat this as a skip with an
// ErrorEntry — the file is well-formed markdown but isn't a seed entry.
var ErrNoFrontmatter = errors.New("configseed: no frontmatter envelope")

// Parse splits a markdown file into its YAML frontmatter and body.
// Convention: the file MUST open with "---\n", followed by YAML, a
// terminating "---\n", then the body. Parse trims a single leading
// newline from the body so common authoring patterns work.
func Parse(content []byte) (Frontmatter, []byte, error) {
	if !bytes.HasPrefix(content, []byte("---\n")) && !bytes.HasPrefix(content, []byte("---\r\n")) {
		return nil, nil, ErrNoFrontmatter
	}
	rest := content[4:]
	if bytes.HasPrefix(content, []byte("---\r\n")) {
		rest = content[5:]
	}
	end := bytes.Index(rest, []byte("\n---"))
	if end < 0 {
		return nil, nil, fmt.Errorf("configseed: unterminated frontmatter")
	}
	yamlBytes := rest[:end]
	bodyStart := end + len("\n---")
	if bodyStart >= len(rest) {
		return nil, nil, fmt.Errorf("configseed: no body after frontmatter")
	}
	body := rest[bodyStart:]
	body = bytes.TrimPrefix(body, []byte("\n"))
	body = bytes.TrimPrefix(body, []byte("\r\n"))

	var fm Frontmatter
	if err := yaml.Unmarshal(yamlBytes, &fm); err != nil {
		return nil, nil, fmt.Errorf("configseed: invalid yaml: %w", err)
	}
	if fm == nil {
		fm = Frontmatter{}
	}
	return fm, body, nil
}

// String returns fm[key] as a string, or empty when absent or
// non-string. Loaders use this for required and optional string fields.
func (fm Frontmatter) String(key string) string {
	v, ok := fm[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// Bool returns fm[key] as a bool. Defaults to false when absent or
// non-bool. Used for opt-in flags like `template: true` (story_6593bb8c).
func (fm Frontmatter) Bool(key string) bool {
	v, ok := fm[key]
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}

// StringSlice returns fm[key] as a []string, accepting either a YAML
// sequence of strings or a single string (which is wrapped). Returns
// nil when absent or non-string-shaped.
func (fm Frontmatter) StringSlice(key string) []string {
	v, ok := fm[key]
	if !ok {
		return nil
	}
	switch typed := v.(type) {
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return []string{typed}
	}
	return nil
}
