package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kittyandrew/kshare/internal/api"
)

// seedUpload pushes a row through the upload handler and returns the
// resulting metadata so subsequent tests don't have to duplicate the
// multipart dance. Uses TTL=1h, fits inside testServer's range.
func seedUpload(t *testing.T, s *server, filename, body string) api.Upload {
	t.Helper()
	req := newUploadRequest(http.MethodPost, "/api/upload", "1h", filename, body)
	req = withClaims(req, "andrew")
	w := httptest.NewRecorder()
	s.handleUpload(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("seedUpload(%s): status %d body %s", filename, w.Code, w.Body.String())
	}
	var resp api.Upload
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("seedUpload decode: %v", err)
	}
	return resp
}

func TestListFiles_Empty(t *testing.T) {
	s := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/files", nil)
	req = withClaims(req, "andrew")
	w := httptest.NewRecorder()
	s.handleListFiles(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if strings.TrimSpace(w.Body.String()) != "[]" {
		t.Errorf("empty store should return [], got %s", w.Body.String())
	}
}

func TestListFiles_NewestFirst(t *testing.T) {
	s := testServer(t)
	first := seedUpload(t, s, "first.txt", "1")
	second := seedUpload(t, s, "second.txt", "2")

	req := httptest.NewRequest(http.MethodGet, "/api/files", nil)
	req = withClaims(req, "andrew")
	w := httptest.NewRecorder()
	s.handleListFiles(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var rows []api.Upload
	_ = json.Unmarshal(w.Body.Bytes(), &rows)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].Slug != second.Slug || rows[1].Slug != first.Slug {
		t.Errorf("list not newest-first: got %s, %s", rows[0].Slug, rows[1].Slug)
	}
}

func TestDeleteFile_HappyPath(t *testing.T) {
	s := testServer(t)
	resp := seedUpload(t, s, "todelete.txt", "bye")
	// File exists on disk before delete.
	if _, err := os.Stat(s.container.FilePath(resp.Slug + resp.Extension)); err != nil {
		t.Fatalf("pre-delete stat: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/files/"+resp.Slug, nil)
	req = withPathValue(withClaims(req, "andrew"), "slug", resp.Slug)
	w := httptest.NewRecorder()
	s.handleDeleteFile(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	// Row gone.
	u, _ := s.container.GetBySlug(context.Background(), resp.Slug)
	if u != nil {
		t.Errorf("row should be gone, got %+v", u)
	}
	// File gone.
	if _, err := os.Stat(s.container.FilePath(resp.Slug + resp.Extension)); !os.IsNotExist(err) {
		t.Errorf("file should be removed: err=%v", err)
	}
}

func TestDeleteFile_NotFound(t *testing.T) {
	s := testServer(t)
	req := httptest.NewRequest(http.MethodDelete, "/api/files/aF3xK9pQ", nil)
	req = withPathValue(withClaims(req, "andrew"), "slug", "aF3xK9pQ")
	w := httptest.NewRecorder()
	s.handleDeleteFile(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestDeleteFile_BadSlug(t *testing.T) {
	s := testServer(t)
	req := httptest.NewRequest(http.MethodDelete, "/api/files/bad", nil)
	req = withPathValue(withClaims(req, "andrew"), "slug", "bad")
	w := httptest.NewRecorder()
	s.handleDeleteFile(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestReplace_SameExtension(t *testing.T) {
	s := testServer(t)
	orig := seedUpload(t, s, "page.html", "<h1>v1</h1>")

	req := newUploadRequest(http.MethodPut, "/api/files/"+orig.Slug, "2h", "page.html", "<h1>v2</h1>")
	req = withPathValue(withClaims(req, "andrew"), "slug", orig.Slug)
	w := httptest.NewRecorder()
	s.handleReplaceFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var resp api.Upload
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	// Slug + extension stay identical (URL identity).
	if resp.Slug != orig.Slug || resp.Extension != orig.Extension {
		t.Errorf("slug+extension changed unexpectedly: %s%s -> %s%s",
			orig.Slug, orig.Extension, resp.Slug, resp.Extension)
	}
	if resp.Size != int64(len("<h1>v2</h1>")) {
		t.Errorf("size mismatch: %d vs %d", resp.Size, len("<h1>v2</h1>"))
	}
	// File on disk has new contents.
	got, err := os.ReadFile(s.container.FilePath(resp.Slug + resp.Extension))
	if err != nil {
		t.Fatalf("read new file: %v", err)
	}
	if string(got) != "<h1>v2</h1>" {
		t.Errorf("disk content: %q", string(got))
	}
}

func TestReplace_ExtensionChange(t *testing.T) {
	s := testServer(t)
	orig := seedUpload(t, s, "doc.html", "<h1>html</h1>")
	oldOnDisk := s.container.FilePath(orig.Slug + orig.Extension)
	if _, err := os.Stat(oldOnDisk); err != nil {
		t.Fatalf("pre-replace stat: %v", err)
	}

	req := newUploadRequest(http.MethodPut, "/api/files/"+orig.Slug, "1h", "doc.txt", "plain text now")
	req = withPathValue(withClaims(req, "andrew"), "slug", orig.Slug)
	w := httptest.NewRecorder()
	s.handleReplaceFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var resp api.Upload
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Extension != ".txt" {
		t.Errorf("new extension: got %q, want .txt", resp.Extension)
	}
	if resp.Extension == orig.Extension {
		t.Errorf("extension should have changed (swap), still %q", resp.Extension)
	}
	// New file exists.
	if _, err := os.Stat(s.container.FilePath(resp.Slug + ".txt")); err != nil {
		t.Errorf("new file missing: %v", err)
	}
	// Old file is cleaned up.
	if _, err := os.Stat(oldOnDisk); !os.IsNotExist(err) {
		t.Errorf("old file should be removed: err=%v", err)
	}
}

func TestReplace_NotFound(t *testing.T) {
	s := testServer(t)
	req := newUploadRequest(http.MethodPut, "/api/files/aaaaaaaa", "1h", "x.txt", "hi")
	req = withPathValue(withClaims(req, "andrew"), "slug", "aaaaaaaa")
	w := httptest.NewRecorder()
	s.handleReplaceFile(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestReplace_BadSlug(t *testing.T) {
	s := testServer(t)
	req := newUploadRequest(http.MethodPut, "/api/files/bad", "1h", "x.txt", "hi")
	req = withPathValue(withClaims(req, "andrew"), "slug", "bad")
	w := httptest.NewRecorder()
	s.handleReplaceFile(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// TestReplace_TTLReset confirms the expiry clock restarts on
// replace: ExpiresAt = new_uploaded_at + new_ttl.
func TestReplace_TTLReset(t *testing.T) {
	s := testServer(t)
	orig := seedUpload(t, s, "page.txt", "v1")

	req := newUploadRequest(http.MethodPut, "/api/files/"+orig.Slug, "30m", "page.txt", "v2")
	req = withPathValue(withClaims(req, "andrew"), "slug", orig.Slug)
	w := httptest.NewRecorder()
	s.handleReplaceFile(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("replace status %d body %s", w.Code, w.Body.String())
	}

	u, err := s.container.GetBySlug(req.Context(), orig.Slug)
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if !u.UploadedAt.After(orig.UploadedAt) {
		t.Errorf("uploaded_at should advance on replace: orig=%s new=%s", orig.UploadedAt, u.UploadedAt)
	}
	if u.TTL != 30*time.Minute {
		t.Errorf("TTL: got %s, want 30m", u.TTL)
	}
}

// Verifies that the store-level invariant (ExpiresAt = UploadedAt +
// TTL) holds across the API layer.
func TestUpload_ExpiryIsDerived(t *testing.T) {
	s := testServer(t)
	resp := seedUpload(t, s, "file.txt", "x")
	u, _ := s.container.GetBySlug(context.Background(), resp.Slug)
	if !u.ExpiresAt().Equal(u.UploadedAt.Add(u.TTL)) {
		t.Errorf("ExpiresAt() not derived: %s != %s + %s", u.ExpiresAt(), u.UploadedAt, u.TTL)
	}
	if !resp.ExpiresAt.Equal(u.ExpiresAt()) {
		t.Errorf("response.ExpiresAt %s != store.ExpiresAt() %s", resp.ExpiresAt, u.ExpiresAt())
	}
}
