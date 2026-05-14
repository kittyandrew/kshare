package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/rs/zerolog"
)

// handleHealthz is the unauthenticated liveness + readiness probe.
// Probes DB + files-dir under a 1s ceiling. Catches wedged WAL /
// disk-full / mount-gone -- failure modes that would let the bare
// process answer 200 while every upload 500s. If SQLite can't
// answer a no-op SELECT in 1s, the service genuinely isn't healthy.
func (s *server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 1*time.Second)
	defer cancel()

	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)

	if err := s.container.Ping(ctx); err != nil {
		zerolog.Ctx(r.Context()).Warn().Err(err).Msg("healthz: db ping failed")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = enc.Encode(map[string]any{"ok": false, "reason": "db"})
		return
	}
	if _, err := os.Stat(s.container.FilesDir()); err != nil {
		zerolog.Ctx(r.Context()).Warn().Err(err).Msg("healthz: files dir stat failed")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = enc.Encode(map[string]any{"ok": false, "reason": "files_dir"})
		return
	}
	_ = enc.Encode(map[string]bool{"ok": true})
}
