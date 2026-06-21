package verb

import (
	"errors"
	"strings"
	"testing"
)

func tagsP(t ...string) *[]string { return &t }

// TestReviewRequiredKind pins which upserts demand a review attestation
// (sty_e6226180): behaviour kinds (skill, workflow-as-skill, principle) do;
// stories, tasks, and free-form documents do not.
func TestReviewRequiredKind(t *testing.T) {
	cases := []struct {
		name string
		req  DocumentUpsertRequest
		want bool
	}{
		{"skill", DocumentUpsertRequest{Type: "skill"}, true},
		{"workflow (kind:workflow stored as skill)", DocumentUpsertRequest{Type: "skill", Tags: tagsP("kind:workflow")}, true},
		{"principle (principles:* tag)", DocumentUpsertRequest{Type: "document", Tags: tagsP("principles:always")}, true},
		{"free-form document", DocumentUpsertRequest{Type: "document", Tags: tagsP("area:x")}, false},
		{"document, no tags", DocumentUpsertRequest{Type: "document"}, false},
		{"story", DocumentUpsertRequest{Type: "story"}, false},
		{"task", DocumentUpsertRequest{Type: "task"}, false},
	}
	for _, c := range cases {
		if got := reviewRequiredKind(c.req); got != c.want {
			t.Errorf("%s: reviewRequiredKind = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestVerifyReviewAttestation pins the content-bound barrier: present +
// decision==accept + content_sha256 matching the body, else ErrForbidden.
func TestVerifyReviewAttestation(t *testing.T) {
	body := "behaviour-kind body content"
	good := sha256Hex(body)
	base := DocumentUpsertRequest{Type: "skill", Body: body}

	// 1) Omitted → refused, and the message points at the upload command.
	err := verifyReviewAttestation(base)
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("omitted attestation: want ErrForbidden, got %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "upload") {
		t.Errorf("omitted attestation: message must name the upload path, got %v", err)
	}

	// 2) Wrong decision → refused.
	r := base
	r.Review = &ReviewAttestation{Decision: "reject", ContentSHA256: good}
	if !errors.Is(verifyReviewAttestation(r), ErrForbidden) {
		t.Errorf("reject decision must be refused")
	}

	// 3) Content mismatch (stale/replayed accept for different content) → refused.
	r = base
	r.Review = &ReviewAttestation{Decision: "accept", ContentSHA256: sha256Hex("different body")}
	if !errors.Is(verifyReviewAttestation(r), ErrForbidden) {
		t.Errorf("content_sha256 mismatch must be refused")
	}

	// 4) Valid accept bound to this body → allowed.
	r = base
	r.Review = &ReviewAttestation{Decision: "accept", ContentSHA256: good}
	if err := verifyReviewAttestation(r); err != nil {
		t.Errorf("valid attestation: want nil, got %v", err)
	}
}
