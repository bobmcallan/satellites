package configseed

import (
	"context"
	"strings"
	"testing"
)

// templatedAgentMD has `template: true` plus a {{KEY}} placeholder so
// LoadDirWithResolver renders it through kvtemplate.Render.
const templatedAgentMD = `---
name: templated_agent
template: true
permission_patterns:
  - "Read:**"
tags: [test, templated]
---
# Templated Agent

Region: {{REGION}}
`

// untemplatedAgentMD has `template: true` but a missing key — render
// must hard-fail and the file must be skipped with a structured error.
const unresolvedAgentMD = `---
name: unresolved_agent
template: true
permission_patterns:
  - "Read:**"
---
# Body
Missing: {{MISSING_KEY}}
`

// staticTemplateResolver implements kvtemplate.Resolver against a fixed map.
type staticTemplateResolver struct {
	values map[string]string
}

func (s staticTemplateResolver) Resolve(_ context.Context, key string) (string, bool, error) {
	v, ok := s.values[key]
	return v, ok, nil
}

func TestLoadDirWithResolver_RendersTemplatedBody(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "agents/templated.md", templatedAgentMD)

	resolver := staticTemplateResolver{values: map[string]string{"REGION": "us-east"}}
	inputs, errs := LoadDirWithResolver(context.Background(), dir, KindAgent, "wksp_sys", "system", resolver)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(inputs) != 1 {
		t.Fatalf("inputs = %d, want 1", len(inputs))
	}
	if !strings.Contains(string(inputs[0].Body), "Region: us-east") {
		t.Errorf("body not rendered: %q", string(inputs[0].Body))
	}
	if strings.Contains(string(inputs[0].Body), "{{REGION}}") {
		t.Errorf("body still contains placeholder: %q", string(inputs[0].Body))
	}
}

func TestLoadDirWithResolver_HardFailsOnUnresolvedKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "agents/unresolved.md", unresolvedAgentMD)

	resolver := staticTemplateResolver{values: map[string]string{}}
	inputs, errs := LoadDirWithResolver(context.Background(), dir, KindAgent, "wksp_sys", "system", resolver)
	if len(inputs) != 0 {
		t.Errorf("expected file to be skipped on unresolved keys, got %d inputs", len(inputs))
	}
	if len(errs) != 1 {
		t.Fatalf("errs = %d, want 1", len(errs))
	}
	if !strings.Contains(errs[0].Reason, "MISSING_KEY") {
		t.Errorf("error reason = %q, want it to cite MISSING_KEY", errs[0].Reason)
	}
}

func TestLoadDirWithResolver_NoTemplateFlagBypassesRender(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// A normal agent file with no template flag should load with body
	// preserved verbatim, even when its body coincidentally contains
	// a {{X}} fragment.
	writeFile(t, dir, "agents/literal.md", `---
name: literal_agent
permission_patterns:
  - "Read:**"
---
# Body with {{LITERAL}} unrendered
`)

	// Resolver is a poison-pill: any call to Resolve would change the
	// outcome. Since template:false, Resolve must NOT be called.
	resolver := staticTemplateResolver{values: map[string]string{"LITERAL": "REPLACED"}}
	inputs, errs := LoadDirWithResolver(context.Background(), dir, KindAgent, "wksp_sys", "system", resolver)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(inputs) != 1 {
		t.Fatalf("inputs = %d, want 1", len(inputs))
	}
	if !strings.Contains(string(inputs[0].Body), "{{LITERAL}}") {
		t.Errorf("body should preserve literal {{LITERAL}}, got %q", string(inputs[0].Body))
	}
	if strings.Contains(string(inputs[0].Body), "REPLACED") {
		t.Errorf("body should NOT be rendered when template flag absent")
	}
}

func TestLoadDirWithResolver_NilResolverSkipsRender(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "agents/templated.md", templatedAgentMD)

	// nil resolver: even with template:true, no render path runs and
	// the body stays literal. Equivalent to the original LoadDir path.
	inputs, errs := LoadDirWithResolver(context.Background(), dir, KindAgent, "wksp_sys", "system", nil)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(inputs) != 1 || !strings.Contains(string(inputs[0].Body), "{{REGION}}") {
		t.Errorf("nil resolver should leave body literal, got %q / errs %v", string(inputs[0].Body), errs)
	}
}
