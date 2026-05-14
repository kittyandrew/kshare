package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog"
)

// withRequestLogger is the outermost middleware: enriches per-request
// context with a logger that has method + path baked in. Downstream
// handlers pull from `zerolog.Ctx(r.Context())` for correlated lines.
func withRequestLogger(root zerolog.Logger, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := newRequestID()
		l := root.With().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Str("req_id", reqID).
			Logger()
		ctx := l.WithContext(r.Context())
		w.Header().Set("X-Request-Id", reqID)
		h.ServeHTTP(w, r.WithContext(ctx))
	})
}

// httpError centralises the 4xx/5xx response path.
//
//   - 4xx: `body` is sent to the client verbatim (caller-shaped
//     bugs need to know what's wrong). `err` is optional; when set,
//     it's logged via .Err() so `errors.Is/As` post-hoc still works.
//   - 5xx: body is redacted to "internal server error (req_id=...)";
//     the log captures `body` as the failure label + `err` via .Err()
//     so internal paths/state never leak to remote callers but the
//     error chain is preserved for diagnostics.
func httpError(ctx context.Context, w http.ResponseWriter, status int, body string, err error) {
	log := zerolog.Ctx(ctx)
	if status >= http.StatusInternalServerError {
		// withRequestLogger always sets X-Request-Id on every
		// request, so we just read it back here.
		reqID := w.Header().Get("X-Request-Id")
		ev := log.Error().Str("event", "http_error").Int("status", status).
			Str("label", body).Str("req_id", reqID)
		if err != nil {
			ev = ev.Err(err)
		}
		ev.Msg("http error")
		http.Error(w, fmt.Sprintf("internal server error (req_id=%s)", reqID), status)
		return
	}
	ev := log.Warn().Str("event", "http_error").Int("status", status).Str("body", body)
	if err != nil {
		ev = ev.Err(err)
	}
	ev.Msg("http error")
	http.Error(w, body, status)
}

// newRequestID returns a short hex correlation ID. Used for log/header
// pairing; crypto/rand failure is vanishingly rare on Linux but would
// produce all-zeros IDs that mis-correlate every request with the
// same log line. Falls back to a timestamp-derived ID so correlation
// stays per-request even when crypto/rand is wedged.
func newRequestID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%012x", time.Now().UnixNano())[:12]
	}
	return hex.EncodeToString(b[:])
}
