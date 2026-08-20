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

// withRequestLogger is the outermost middleware: every downstream zerolog.Ctx(r.Context()) already carries
// method, path and req_id, so handlers must not re-log those fields.
//
// The id also goes on the context, not just the response header. http.TimeoutHandler hands its inner handler
// a private header map and only merges it back on the way out, so a handler reading X-Request-Id off its own
// ResponseWriter sees "" on every route that has a timeout wrapper.
func withRequestLogger(root zerolog.Logger, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := newRequestID()
		l := root.With().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Str("req_id", reqID).
			Logger()
		ctx := context.WithValue(l.WithContext(r.Context()), reqIDCtxKey{}, reqID)
		w.Header().Set("X-Request-Id", reqID)
		h.ServeHTTP(w, r.WithContext(ctx))
	})
}

type reqIDCtxKey struct{}

func reqIDFromCtx(ctx context.Context) string {
	id, _ := ctx.Value(reqIDCtxKey{}).(string)
	return id
}

// httpError centralises the 4xx/5xx response path.
//
//   - 4xx: `body` goes to the client verbatim, since caller-shaped bugs need to know what's wrong. `err` is
//     optional; when set it is logged via .Err() so errors.Is/As still work after the fact.
//   - 5xx: the body is redacted to "internal server error (req_id=...)" while the log keeps `body` as the
//     failure label and `err` as the chain, so internal paths never leak to remote callers.
func httpError(ctx context.Context, w http.ResponseWriter, status int, body string, err error) {
	log := zerolog.Ctx(ctx)
	if status >= http.StatusInternalServerError {
		reqID := reqIDFromCtx(ctx)
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

// newRequestID returns a short hex correlation ID. A wedged crypto/rand would hand every request the same
// all-zero ID and collapse the logs into one stream, so fall back to the clock.
func newRequestID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%012x", time.Now().UnixNano())[:12]
	}
	return hex.EncodeToString(b[:])
}
