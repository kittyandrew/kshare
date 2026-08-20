package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// Every /api/ route sits behind http.TimeoutHandler, which gives its inner handler a private header map and
// merges it outward only on the way back. A 5xx must still name the request id the log line carries.
func TestHTTPError_ReqIDSurvivesTimeoutHandler(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpError(r.Context(), w, http.StatusInternalServerError, "boom", nil)
	})
	h := withRequestLogger(zerolog.Nop(), http.TimeoutHandler(inner, time.Second, "timeout"))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/files", nil))

	reqID := w.Header().Get("X-Request-Id")
	if reqID == "" {
		t.Fatal("no X-Request-Id on the response")
	}
	if body := strings.TrimSpace(w.Body.String()); !strings.Contains(body, reqID) {
		t.Errorf("5xx body %q does not name req_id %q", body, reqID)
	}
}

// The label passed to httpError is for the log only; a 5xx body must not leak it.
func TestHTTPError_5xxRedactsLabel(t *testing.T) {
	w := httptest.NewRecorder()
	h := withRequestLogger(zerolog.Nop(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpError(r.Context(), w, http.StatusInternalServerError, "open /data/files/secret", nil)
	}))
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/s/aaaaaaaa", nil))

	if strings.Contains(w.Body.String(), "/data/files/secret") {
		t.Errorf("5xx body leaked the internal label: %q", w.Body.String())
	}
}
