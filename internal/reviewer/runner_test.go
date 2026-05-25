package reviewer

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeClient struct {
	resp        string
	err         error
	lastPrompt  string
	lastModel   string
	lastMaxTok  int
	callCounter int
}

func (f *fakeClient) Complete(_ context.Context, model string, maxTokens int, prompt string) (string, error) {
	f.callCounter++
	f.lastModel = model
	f.lastMaxTok = maxTokens
	f.lastPrompt = prompt
	return f.resp, f.err
}

func TestRun_HappyPath(t *testing.T) {
	def := Definition{
		Name: "story_reviewer", Enabled: true, Model: "m", MaxTokens: 100,
		Body: "You are a reviewer.",
	}
	client := &fakeClient{
		resp: `{"findings":[{"severity":"warn","code":"vague_title","field":"title","message":"too short"}]}`,
	}
	findings, err := Run(context.Background(), def, client, map[string]any{"title": "hi"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings", len(findings))
	}
	if findings[0].Code != "vague_title" {
		t.Fatalf("code = %q", findings[0].Code)
	}
	if !strings.Contains(client.lastPrompt, "hi") {
		t.Fatalf("prompt missing story JSON: %q", client.lastPrompt)
	}
	if client.lastModel != "m" {
		t.Fatalf("model not forwarded: %q", client.lastModel)
	}
}

func TestRun_DisabledReturnsNil(t *testing.T) {
	def := Definition{Name: "x", Enabled: false}
	client := &fakeClient{}
	findings, err := Run(context.Background(), def, client, "story")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if findings != nil {
		t.Fatalf("expected nil findings for disabled reviewer")
	}
	if client.callCounter != 0 {
		t.Fatalf("client invoked despite disabled definition")
	}
}

func TestRun_TolerantOfFencedJSON(t *testing.T) {
	def := Definition{Name: "x", Enabled: true, Model: "m"}
	client := &fakeClient{
		resp: "```json\n{\"findings\":[]}\n```",
	}
	findings, err := Run(context.Background(), def, client, "story")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("got %d findings", len(findings))
	}
}

func TestRun_ParseFailureIsAnError(t *testing.T) {
	def := Definition{Name: "x", Enabled: true, Model: "m"}
	client := &fakeClient{resp: "not even close to json"}
	_, err := Run(context.Background(), def, client, "story")
	if err == nil {
		t.Fatalf("expected parse error")
	}
}

func TestRun_ClientErrorBubbles(t *testing.T) {
	def := Definition{Name: "x", Enabled: true, Model: "m"}
	want := errors.New("upstream")
	client := &fakeClient{err: want}
	_, err := Run(context.Background(), def, client, "story")
	if err == nil || !strings.Contains(err.Error(), "upstream") {
		t.Fatalf("error = %v", err)
	}
}

func TestRegistry_RunAll_SkipsDisabled(t *testing.T) {
	defs := map[string]Definition{
		"on":  {Name: "on", Enabled: true, Model: "m", Body: "body"},
		"off": {Name: "off", Enabled: false},
	}
	client := &fakeClient{resp: `{"findings":[]}`}
	reg := NewRegistry(defs, client)

	findings, errs := reg.RunAll(context.Background(), "story")
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if _, ok := findings["on"]; !ok {
		t.Fatalf("enabled reviewer not run: %+v", findings)
	}
	if _, ok := findings["off"]; ok {
		t.Fatalf("disabled reviewer should not produce findings")
	}
	if client.callCounter != 1 {
		t.Fatalf("client calls = %d, want 1", client.callCounter)
	}
}
