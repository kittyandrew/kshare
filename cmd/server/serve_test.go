package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServeFile_HappyPath(t *testing.T) {
	s := testServer(t)
	resp := seedUpload(t, s, "page.html", "<h1>hello</h1>")

	req := httptest.NewRequest(http.MethodGet, "/s/"+resp.Slug, nil)
	req = withPathValue(req, "slug", resp.Slug)
	w := httptest.NewRecorder()
	s.handleServeFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	body, _ := io.ReadAll(w.Result().Body)
	if string(body) != "<h1>hello</h1>" {
		t.Errorf("body mismatch: %q", string(body))
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type: got %q", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "page.html") {
		t.Errorf("Content-Disposition should include original filename: got %q", cd)
	}
	if !strings.HasPrefix(w.Header().Get("Content-Disposition"), "inline") {
		t.Errorf("Content-Disposition should be inline, got %q", w.Header().Get("Content-Disposition"))
	}
}

func TestServeFile_404_NoRow(t *testing.T) {
	s := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/s/aaaaaaaa", nil)
	req = withPathValue(req, "slug", "aaaaaaaa")
	w := httptest.NewRecorder()
	s.handleServeFile(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestServeFile_404_BadFormat(t *testing.T) {
	s := testServer(t)
	for _, bad := range []string{
		"short",         // < 8 chars
		"toolong9chars", // > 8 chars
		"aaa//bbb",      // path separator
		"abcdefgh.html", // extension in URL not allowed; URL is slug-only
		"abcd!fgh",      // invalid char
	} {
		req := httptest.NewRequest(http.MethodGet, "/s/"+bad, nil)
		req = withPathValue(req, "slug", bad)
		w := httptest.NewRecorder()
		s.handleServeFile(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("%q: expected 404, got %d", bad, w.Code)
		}
	}
}

func TestServeFile_RangeSupport(t *testing.T) {
	s := testServer(t)
	resp := seedUpload(t, s, "blob.bin", "0123456789ABCDEF")

	req := httptest.NewRequest(http.MethodGet, "/s/"+resp.Slug, nil)
	req.Header.Set("Range", "bytes=4-7")
	req = withPathValue(req, "slug", resp.Slug)
	w := httptest.NewRecorder()
	s.handleServeFile(w, req)

	if w.Code != http.StatusPartialContent {
		t.Fatalf("expected 206 for Range, got %d", w.Code)
	}
	body, _ := io.ReadAll(w.Result().Body)
	if string(body) != "4567" {
		t.Errorf("range body: got %q, want %q", string(body), "4567")
	}
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		xff, remote, want string
	}{
		{"1.2.3.4", "10.0.0.1:1234", "1.2.3.4"},
		{"1.2.3.4, 10.0.0.5", "10.0.0.1:1234", "1.2.3.4"},
		// XFF unparseable -> fall back to RemoteAddr.
		{"not-an-ip", "192.168.1.5:1234", "192.168.1.5"},
		{"", "192.168.1.5:1234", "192.168.1.5"},
		{"", "[::1]:5678", "::1"},
		// Both unparseable -> "" -> triggers slug_miss_no_ip event variant.
		{"garbage", "also-garbage", ""},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		r.RemoteAddr = c.remote
		if got := clientIP(r); got != c.want {
			t.Errorf("clientIP(xff=%q, ra=%q) = %q, want %q", c.xff, c.remote, got, c.want)
		}
	}
}

// TestServeFile_AfterDelete checks that a deleted slug 404s on /s/, covering the delete handler's ordering.
func TestServeFile_AfterDelete(t *testing.T) {
	s := testServer(t)
	resp := seedUpload(t, s, "delme.txt", "going away")

	// Delete via handler.
	delReq := httptest.NewRequest(http.MethodDelete, "/api/files/"+resp.Slug, nil)
	delReq = withPathValue(withClaims(delReq, "andrew"), "slug", resp.Slug)
	delW := httptest.NewRecorder()
	s.handleDeleteFile(delW, delReq)
	if delW.Code != http.StatusNoContent {
		t.Fatalf("delete failed: %d %s", delW.Code, delW.Body.String())
	}

	// Now /s/<slug> should 404.
	getReq := httptest.NewRequest(http.MethodGet, "/s/"+resp.Slug, nil)
	getReq = withPathValue(getReq, "slug", resp.Slug)
	getW := httptest.NewRecorder()
	s.handleServeFile(getW, getReq)
	if getW.Code != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", getW.Code)
	}
}

// TestUploadJSONShape verifies the JSON response shape is stable, catching field renames that break the CLI.
func TestUploadJSONShape(t *testing.T) {
	s := testServer(t)
	req := newUploadRequest(http.MethodPost, "/api/upload", "1h", "test.html", "<h1>hi</h1>")
	req = withClaims(req, "andrew")
	w := httptest.NewRecorder()
	s.handleUpload(w, req)

	var raw map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := []string{"slug", "extension", "original_filename", "size", "content_type", "uploaded_at", "expires_at"}
	for _, k := range want {
		if _, ok := raw[k]; !ok {
			t.Errorf("response missing key %q (got keys: %v)", k, mapKeys(raw))
		}
	}
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
