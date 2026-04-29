package reviewer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGeminiReviewer_Accepted(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/gemini-2.5-flash:generateContent" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("key") != "test-key" {
			t.Errorf("api key not propagated")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{
					"parts": []map[string]any{{
						"text": `{"outcome":"accepted","rationale":"clean","principles_cited":["pr_evidence"],"review_questions":[]}`,
					}},
				},
			}},
			"usageMetadata": map[string]any{
				"promptTokenCount":     120,
				"candidatesTokenCount": 50,
				"totalTokenCount":      170,
			},
		})
	}))
	defer srv.Close()

	rev := NewGeminiReviewer(GeminiConfig{
		APIKey:  "test-key",
		BaseURL: srv.URL,
	})
	req := Request{
		ContractName:     "develop",
		AgentInstruction: "instruction",
		ReviewerRubric:   "rubric",
		EvidenceMarkdown: "evidence",
	}
	verdict, usage, err := rev.Review(context.Background(), req)
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if verdict.Outcome != VerdictAccepted {
		t.Errorf("outcome = %q, want accepted", verdict.Outcome)
	}
	if verdict.Rationale != "clean" {
		t.Errorf("rationale = %q", verdict.Rationale)
	}
	if len(verdict.PrinciplesCited) != 1 || verdict.PrinciplesCited[0] != "pr_evidence" {
		t.Errorf("principles_cited = %v", verdict.PrinciplesCited)
	}
	if usage.InputTokens != 120 || usage.OutputTokens != 50 {
		t.Errorf("usage tokens = %d/%d, want 120/50", usage.InputTokens, usage.OutputTokens)
	}
	if usage.Model != "gemini-2.5-flash" {
		t.Errorf("usage model = %q", usage.Model)
	}
}

func TestGeminiReviewer_NeedsMoreWithFencedJSON(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{
					"parts": []map[string]any{{
						"text": "Verdict:\n```json\n{\"outcome\":\"needs_more\",\"rationale\":\"two gaps\",\"review_questions\":[\"q1\",\"q2\"]}\n```",
					}},
				},
			}},
		})
	}))
	defer srv.Close()

	rev := NewGeminiReviewer(GeminiConfig{APIKey: "k", BaseURL: srv.URL})
	verdict, _, err := rev.Review(context.Background(), Request{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if verdict.Outcome != VerdictNeedsMore {
		t.Errorf("outcome = %q want needs_more", verdict.Outcome)
	}
	if len(verdict.ReviewQuestions) != 2 {
		t.Errorf("review_questions len = %d want 2", len(verdict.ReviewQuestions))
	}
}

func TestGeminiReviewer_UnparseableResponse(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{
					"parts": []map[string]any{{"text": "I don't have a verdict for you."}},
				},
			}},
		})
	}))
	defer srv.Close()

	rev := NewGeminiReviewer(GeminiConfig{APIKey: "k", BaseURL: srv.URL})
	verdict, _, err := rev.Review(context.Background(), Request{})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if verdict.Outcome != VerdictRejected {
		t.Errorf("outcome = %q want rejected", verdict.Outcome)
	}
	if !strings.Contains(verdict.Rationale, "JSON") && !strings.Contains(verdict.Rationale, "parse") {
		t.Errorf("rationale missing parse-failure hint: %q", verdict.Rationale)
	}
}

func TestGeminiReviewer_HTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	rev := NewGeminiReviewer(GeminiConfig{APIKey: "k", BaseURL: srv.URL})
	_, _, err := rev.Review(context.Background(), Request{})
	if err == nil {
		t.Fatalf("expected error on 500, got nil")
	}
}

func TestGeminiReviewer_PromptIncludesAllFields(t *testing.T) {
	t.Parallel()
	var captured string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body geminiRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.Contents) > 0 && len(body.Contents[0].Parts) > 0 {
			captured = body.Contents[0].Parts[0].Text
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{
					"parts": []map[string]any{{"text": `{"outcome":"accepted","rationale":"ok"}`}},
				},
			}},
		})
	}))
	defer srv.Close()

	rev := NewGeminiReviewer(GeminiConfig{APIKey: "k", BaseURL: srv.URL})
	req := Request{
		AgentInstruction: "INSTR_MARKER",
		ReviewerRubric:   "RUBRIC_MARKER",
		EvidenceMarkdown: "EVIDENCE_MARKER",
	}
	if _, _, err := rev.Review(context.Background(), req); err != nil {
		t.Fatalf("Review: %v", err)
	}
	for _, marker := range []string{"INSTR_MARKER", "RUBRIC_MARKER", "EVIDENCE_MARKER"} {
		if !strings.Contains(captured, marker) {
			t.Errorf("prompt missing %q", marker)
		}
	}
}
