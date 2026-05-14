package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kittyandrew/kshare/internal/api"
)

func TestUpload_HappyPath(t *testing.T) {
	s := testServer(t)
	req := newUploadRequest(http.MethodPost, "/api/upload", "1h", "test.html", "<h1>hi</h1>")
	req = withClaims(req, "andrew")
	w := httptest.NewRecorder()

	s.handleUpload(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", w.Code, w.Body.String())
	}
	var resp api.Upload
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v\nbody=%s", err, w.Body.String())
	}
	if resp.Slug == "" {
		t.Errorf("missing slug in response")
	}
	if resp.Extension != ".html" {
		t.Errorf("extension: got %q, want .html", resp.Extension)
	}
	if resp.OriginalFilename != "test.html" {
		t.Errorf("original_filename: got %q, want test.html", resp.OriginalFilename)
	}
	if resp.Size != int64(len("<h1>hi</h1>")) {
		t.Errorf("size: got %d, want %d", resp.Size, len("<h1>hi</h1>"))
	}
	if !strings.HasPrefix(resp.ContentType, "text/html") {
		t.Errorf("content_type: got %q, want text/html*", resp.ContentType)
	}
}

func TestUpload_NoTTL_AppliesDefault(t *testing.T) {
	s := testServer(t)
	req := newUploadRequest(http.MethodPost, "/api/upload", "" /* no ttl */, "test.txt", "hello")
	req = withClaims(req, "andrew")
	w := httptest.NewRecorder()

	s.handleUpload(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", w.Code, w.Body.String())
	}
	var resp api.Upload
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	// DefaultTTL is 1h in testServer; expires_at should be ~now+1h.
	expiry := resp.ExpiresAt.Sub(resp.UploadedAt)
	if expiry < 59*time.Minute || expiry > 61*time.Minute {
		t.Errorf("expected ~1h TTL via default, got %s", expiry)
	}
}

func TestUpload_TTLOutsideRange(t *testing.T) {
	s := testServer(t)
	// MaxTTL is 24h in testServer; 48h should reject.
	req := newUploadRequest(http.MethodPost, "/api/upload", "48h", "test.txt", "hi")
	req = withClaims(req, "andrew")
	w := httptest.NewRecorder()

	s.handleUpload(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for over-max ttl, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "outside allowed range") {
		t.Errorf("error body missing 'outside allowed range': %s", w.Body.String())
	}
}

func TestUpload_MalformedTTL(t *testing.T) {
	s := testServer(t)
	req := newUploadRequest(http.MethodPost, "/api/upload", "not-a-duration", "test.txt", "hi")
	req = withClaims(req, "andrew")
	w := httptest.NewRecorder()

	s.handleUpload(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed ttl, got %d", w.Code)
	}
}

func TestUpload_EmptyFile(t *testing.T) {
	s := testServer(t)
	req := newUploadRequest(http.MethodPost, "/api/upload", "1h", "empty.txt", "" /* zero bytes */)
	req = withClaims(req, "andrew")
	w := httptest.NewRecorder()

	s.handleUpload(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty file, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestUpload_OversizeBody(t *testing.T) {
	s := testServer(t)
	s.cfg.MaxUploadSize = 100 // tiny ceiling
	req := newUploadRequest(http.MethodPost, "/api/upload", "1h", "big.txt", strings.Repeat("x", 2048))
	req = withClaims(req, "andrew")
	w := httptest.NewRecorder()

	s.handleUpload(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 over size cap, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestSanitiseOriginalFilename(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"clean.txt", "clean.txt"},
		{"with spaces.pdf", "with spaces.pdf"},
		{"weird\"name.txt", "weirdname.txt"},
		{"control\x01chars.txt", "controlchars.txt"},
		{"path/component.txt", "component.txt"},
		{`back\slash.txt`, "slash.txt"},
		{"emoji😀.txt", "emoji😀.txt"}, // multibyte preserved
	}
	for _, c := range cases {
		if got := sanitiseOriginalFilename(c.in); got != c.want {
			t.Errorf("sanitiseOriginalFilename(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestUpload_Persists(t *testing.T) {
	s := testServer(t)
	req := newUploadRequest(http.MethodPost, "/api/upload", "1h", "test.txt", "payload")
	req = withClaims(req, "andrew")
	w := httptest.NewRecorder()
	s.handleUpload(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("upload failed: %d %s", w.Code, w.Body.String())
	}
	var resp api.Upload
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	// Row should be readable via the store.
	u, err := s.container.GetBySlug(req.Context(), resp.Slug)
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if u == nil {
		t.Fatal("upload row not persisted")
	}
	if u.OriginalFilename != "test.txt" {
		t.Errorf("OriginalFilename: got %q", u.OriginalFilename)
	}
	if u.Extension != ".txt" {
		t.Errorf("Extension: got %q", u.Extension)
	}
}
