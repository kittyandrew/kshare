package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/kittyandrew/kshare/internal/api"
	"github.com/kittyandrew/kshare/internal/store"
)

// testServer constructs a *server with a real on-disk SQLite container in t.TempDir(). Tests call handler
// methods directly, so auth.require never runs and no verifier stub is needed; claims come from withClaims.
func testServer(t *testing.T) *server {
	t.Helper()
	log := zerolog.Nop()
	dir := t.TempDir()
	container, err := store.NewContainer(context.Background(), store.Config{
		DataDir: dir,
		Logger:  log,
	})
	if err != nil {
		t.Fatalf("NewContainer: %v", err)
	}
	t.Cleanup(func() { _ = container.Close() })
	cfg := &config{
		Listen:        ":0",
		DataDir:       dir,
		OIDCIssuer:    "https://test.invalid",
		OIDCAudience:  "test-aud",
		MaxUploadSize: 1 << 20, // 1 MiB
		DefaultTTL:    time.Hour,
		MinTTL:        time.Second,
		MaxTTL:        24 * time.Hour,
	}
	return &server{
		cfg:       cfg,
		container: container,
		log:       log,
	}
}

func withClaims(r *http.Request, sub string) *http.Request {
	c := &claims{
		Subject: sub,
		Roles:   map[string]struct{}{roleUpload: {}},
	}
	return r.WithContext(context.WithValue(r.Context(), claimsCtxKey{}, c))
}

// newUploadRequest builds a request mimicking what the CLI sends: raw body + X-KShare-{TTL,Filename}
// headers. An empty ttl is omitted entirely, so the server applies its DefaultTTL.
func newUploadRequest(method, target, ttl, filename, body string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/octet-stream")
	if ttl != "" {
		r.Header.Set(api.HeaderTTL, ttl)
	}
	if filename != "" {
		r.Header.Set(api.HeaderFilename, filename)
	}
	return r
}

// withPathValue stands in for the ServeMux, which tests bypass by calling handlers directly.
func withPathValue(r *http.Request, key, val string) *http.Request {
	r.SetPathValue(key, val)
	return r
}
