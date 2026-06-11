package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestBlobUpload_Unauthenticated: with no authenticated user on the request
// context (which the Bearer middleware would otherwise provide), the handler
// refuses before touching multipart parsing or the store.
func TestBlobUpload_Unauthenticated(t *testing.T) {
	h := blobUploadHandler(Config{})
	req := httptest.NewRequest(http.MethodPost, "/projects/proj_x/blobs", nil)
	req.SetPathValue("id", "proj_x")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for an unauthenticated upload", rr.Code)
	}
}
