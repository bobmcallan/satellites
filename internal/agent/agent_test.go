package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// scriptedModel returns a fixed sequence of steps and records the history it was
// asked to reason over — a deterministic stand-in for a real model, no network.
type scriptedModel struct {
	steps    []AgentStep
	i        int
	lastSeen []Turn
}

func (m *scriptedModel) Step(_ context.Context, _ string, history []Turn, _ []Tool) (AgentStep, error) {
	m.lastSeen = append([]Turn(nil), history...)
	if m.i >= len(m.steps) {
		return AgentStep{Final: "ran out of script"}, nil
	}
	s := m.steps[m.i]
	m.i++
	return s, nil
}

func tools(names ...string) []Tool {
	out := make([]Tool, len(names))
	for i, n := range names {
		out[i] = Tool{Name: n}
	}
	return out
}

// A tool call is dispatched, its result fed back, and the model's final answer
// returned.
func TestRun_ToolThenFinal(t *testing.T) {
	model := &scriptedModel{steps: []AgentStep{
		{ToolCalls: []ToolCall{{Name: "semantic_search", Args: json.RawMessage(`{"query":"x"}`)}}},
		{Final: "the answer"},
	}}
	var dispatched string
	disp := func(_ context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
		dispatched = name + ":" + string(args)
		return json.RawMessage(`{"hits":3}`), nil
	}
	out, err := Run(context.Background(), model, "sys", "do it", tools("semantic_search"), disp, 8)
	if err != nil || out != "the answer" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if dispatched != `semantic_search:{"query":"x"}` {
		t.Fatalf("dispatched=%q", dispatched)
	}
	// The model's second step must have seen the tool result fed back.
	var sawResult bool
	for _, turn := range model.lastSeen {
		if turn.Role == "tool" && strings.Contains(turn.ToolResult, `"hits":3`) {
			sawResult = true
		}
	}
	if !sawResult {
		t.Fatalf("tool result was not fed back into history: %+v", model.lastSeen)
	}
}

// A non-allowlisted tool call is refused without dispatching, and the error is
// fed back so the model can recover.
func TestRun_RefusesNonAllowlistedTool(t *testing.T) {
	model := &scriptedModel{steps: []AgentStep{
		{ToolCalls: []ToolCall{{Name: "document_delete", Args: json.RawMessage(`{}`)}}},
		{Final: "done"},
	}}
	dispatchedCount := 0
	disp := func(_ context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
		dispatchedCount++
		return nil, nil
	}
	out, err := Run(context.Background(), model, "sys", "task", tools("semantic_search"), disp, 8)
	if err != nil || out != "done" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if dispatchedCount != 0 {
		t.Fatalf("non-allowlisted tool was dispatched %d times", dispatchedCount)
	}
	var refused bool
	for _, turn := range model.lastSeen {
		if turn.Role == "tool" && strings.Contains(turn.ToolResult, "not permitted") {
			refused = true
		}
	}
	if !refused {
		t.Fatalf("refusal not fed back: %+v", model.lastSeen)
	}
}

// A tool error is fed back as the result, not fatal to the run.
func TestRun_ToolErrorFedBack(t *testing.T) {
	model := &scriptedModel{steps: []AgentStep{
		{ToolCalls: []ToolCall{{Name: "document_get", Args: json.RawMessage(`{"name":"x"}`)}}},
		{Final: "recovered"},
	}}
	disp := func(_ context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
		return nil, context.DeadlineExceeded
	}
	out, err := Run(context.Background(), model, "sys", "task", tools("document_get"), disp, 8)
	if err != nil || out != "recovered" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

// The loop is bounded: a model that never finalizes returns an error, not a hang.
func TestRun_MaxStepsBound(t *testing.T) {
	loop := AgentStep{ToolCalls: []ToolCall{{Name: "semantic_search", Args: json.RawMessage(`{}`)}}}
	model := &scriptedModel{steps: []AgentStep{loop, loop, loop, loop, loop}}
	disp := func(_ context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}
	_, err := Run(context.Background(), model, "sys", "task", tools("semantic_search"), disp, 3)
	if err == nil || !strings.Contains(err.Error(), "max steps") {
		t.Fatalf("want max-steps error, got %v", err)
	}
}

// A model turn with neither final text nor a tool call is an error, not an
// empty success.
func TestRun_EmptyStepErrors(t *testing.T) {
	model := &scriptedModel{steps: []AgentStep{{}}}
	disp := func(_ context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) { return nil, nil }
	if _, err := Run(context.Background(), model, "sys", "task", tools("x"), disp, 8); err == nil {
		t.Fatal("want error on empty step")
	}
}
