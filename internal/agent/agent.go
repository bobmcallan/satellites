// Package agent is the server-side agent harness (epic:workspace-agents): a
// tool-using loop where a model decides, calls tools, observes results, and
// iterates to a final answer — the same shape as the client-side `claude -p`
// process, but here WE own the loop (the Gemini API gives one model turn at a
// time). Mirroring synth's "the executor is the only difference" seam, the loop
// is a pure function over an injected AgentModel; a deterministic fake drives it
// in tests, GeminiAgentModel drives it in production. The loop itself makes no
// network call, so it is unit-testable without the live API.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
)

// Tool is a capability the agent may call: a name, a human description, and a
// JSON Schema for its parameters (the model's function declaration).
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON Schema object
}

// ToolCall is the model's request to invoke a tool.
type ToolCall struct {
	Name string
	Args json.RawMessage
}

// AgentStep is one model turn: either final text (the run is done) or one or
// more tool calls to execute and feed back.
type AgentStep struct {
	Final     string
	ToolCalls []ToolCall
}

// Turn is one entry in the conversation history replayed to the (stateless)
// model on each step.
type Turn struct {
	Role       string     // "user" | "model" | "tool"
	Text       string     // user/model text
	ToolCalls  []ToolCall // a model turn that called tools
	ToolName   string     // a tool-result turn
	ToolResult string     // a tool-result turn (JSON or text)
}

// AgentModel is the executor seam: given the system instruction, the
// conversation so far, and the available tools, produce the next step.
type AgentModel interface {
	Step(ctx context.Context, system string, history []Turn, tools []Tool) (AgentStep, error)
}

// ToolDispatcher executes a tool call and returns its raw result. Injected by
// the caller (the verb layer wires it to the allowlisted verb registry), so the
// agent package never imports internal/verb — no import cycle.
type ToolDispatcher func(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error)

// DefaultMaxSteps bounds the loop so a misbehaving model cannot run forever.
const DefaultMaxSteps = 8

// Run drives the loop: ask the model for a step; on final text, stop; otherwise
// dispatch each tool call, append the results, and loop — bounded by maxSteps. A
// non-allowlisted tool call and a tool error are both fed back to the model as
// the tool result (it can recover), never fatal to the run.
func Run(ctx context.Context, model AgentModel, system, task string, tools []Tool, dispatch ToolDispatcher, maxSteps int) (string, error) {
	if maxSteps <= 0 {
		maxSteps = DefaultMaxSteps
	}
	allowed := make(map[string]bool, len(tools))
	for _, t := range tools {
		allowed[t.Name] = true
	}
	history := []Turn{{Role: "user", Text: task}}

	for step := 0; step < maxSteps; step++ {
		s, err := model.Step(ctx, system, history, tools)
		if err != nil {
			return "", fmt.Errorf("agent: model step %d: %w", step, err)
		}
		if len(s.ToolCalls) == 0 {
			if s.Final == "" {
				return "", fmt.Errorf("agent: model returned neither final text nor a tool call at step %d", step)
			}
			return s.Final, nil
		}
		history = append(history, Turn{Role: "model", ToolCalls: s.ToolCalls})
		for _, tc := range s.ToolCalls {
			var result string
			switch {
			case !allowed[tc.Name]:
				result = fmt.Sprintf("error: tool %q is not permitted", tc.Name)
			default:
				out, derr := dispatch(ctx, tc.Name, tc.Args)
				if derr != nil {
					result = "error: " + derr.Error()
				} else {
					result = string(out)
				}
			}
			history = append(history, Turn{Role: "tool", ToolName: tc.Name, ToolResult: result})
		}
	}
	return "", fmt.Errorf("agent: exceeded max steps (%d) without a final answer", maxSteps)
}
