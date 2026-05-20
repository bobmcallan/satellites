package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionCmd_PrintsBuildInfo(t *testing.T) {
	root := NewRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetArgs([]string{"version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	s := out.String()
	if s == "" {
		t.Fatalf("version output empty")
	}
	for _, want := range []string{"satellites ", "commit ", "built "} {
		if !strings.Contains(s, want) {
			t.Fatalf("version output missing %q: %s", want, s)
		}
	}
}
