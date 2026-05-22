package cliconfig

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PersistProjectID writes project_id into the top-level table of the
// TOML at path, preserving surrounding formatting. If a top-level
// project_id key already exists, its value is replaced; otherwise a
// new key is inserted just before the first [section] header (or at
// end-of-file if there are no sections).
//
// The file is rewritten via a tmp+rename so a crash mid-write cannot
// leave a partial config on disk. Returns an error if path is empty
// or the file does not exist — self-heal callers check for the file
// before invoking this.
func PersistProjectID(path, projectID string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("cliconfig: PersistProjectID: path required")
	}
	if strings.TrimSpace(projectID) == "" {
		return fmt.Errorf("cliconfig: PersistProjectID: projectID required")
	}
	in, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cliconfig: read %s: %w", path, err)
	}
	rewritten, err := rewriteProjectIDLine(in, projectID)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, rewritten, 0o600); err != nil {
		return fmt.Errorf("cliconfig: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("cliconfig: rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}

// rewriteProjectIDLine is the pure-function core split out for testing.
func rewriteProjectIDLine(in []byte, projectID string) ([]byte, error) {
	newLine := fmt.Sprintf(`project_id = %q`, projectID)
	var out bytes.Buffer
	scanner := bufio.NewScanner(bytes.NewReader(in))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	inTopLevel := true
	replaced := false
	insertedBeforeSection := false

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// First [section] header ends the top-level table.
		if inTopLevel && strings.HasPrefix(trimmed, "[") {
			if !replaced {
				out.WriteString(newLine)
				out.WriteByte('\n')
				insertedBeforeSection = true
			}
			inTopLevel = false
		}

		if inTopLevel && !replaced && isTopLevelProjectIDLine(trimmed) {
			out.WriteString(newLine)
			out.WriteByte('\n')
			replaced = true
			continue
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("cliconfig: scan: %w", err)
	}
	if !replaced && !insertedBeforeSection {
		// No sections in the file and no existing key — append at EOF.
		out.WriteString(newLine)
		out.WriteByte('\n')
	}
	return trimTrailingBlankLines(out.Bytes()), nil
}

// isTopLevelProjectIDLine matches `project_id =` and `project_id=` at
// the start of the trimmed line, ignoring values and inline comments.
// Commented-out lines (`# project_id = …`) are NOT matched — they stay
// as-is and a new uncommented line is inserted.
func isTopLevelProjectIDLine(trimmed string) bool {
	if strings.HasPrefix(trimmed, "#") {
		return false
	}
	const key = "project_id"
	if !strings.HasPrefix(trimmed, key) {
		return false
	}
	rest := strings.TrimSpace(trimmed[len(key):])
	return strings.HasPrefix(rest, "=")
}

func trimTrailingBlankLines(b []byte) []byte {
	for len(b) >= 2 && b[len(b)-1] == '\n' && b[len(b)-2] == '\n' {
		b = b[:len(b)-1]
	}
	return b
}

// ResolveConfigPath returns the same path Load would use, without
// reading the file. Self-heal callers use this to know where to
// persist after a successful project_match.
func ResolveConfigPath(explicit string) (string, error) {
	path, err := resolvePath(explicit)
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path, nil
	}
	return abs, nil
}
