package main

import (
	"net/http"
	"time"

	"github.com/rs/zerolog"

	"github.com/kittyandrew/kshare/internal/store"
)

// server is the orchestration bundle: handler methods hang off it
// so they can read cfg + container + log without globals. Auth is
// wired into the mux at registration time inside newServer; handlers
// don't consult it directly.
type server struct {
	cfg       *config
	container *store.Container
	log       zerolog.Logger
}

// newServer wires the route table and returns a configured
// *http.Server ready to run. Routes are method-prefixed via Go 1.22+
// ServeMux pattern; auth lives only in this scope (the middleware
// wraps the handlers at registration time).
func newServer(cfg *config, auth *authenticator, container *store.Container, log zerolog.Logger) *http.Server {
	s := &server{
		cfg:       cfg,
		container: container,
		log:       log,
	}

	// Per-route timeouts. shortTimeout + uploadTimeout use
	// http.TimeoutHandler, which buffers the response in memory --
	// that's fine for small JSON / 204 / 413 bodies.
	//
	// /s/{slug} CANNOT use http.TimeoutHandler: it explicitly buffers
	// every byte written to the ResponseWriter, which would kill the
	// http.ServeContent sendfile(2) fast path AND turn 5 concurrent
	// slow downloaders of a 100 MiB file into 500 MiB of resident
	// heap -- within the documented --memory=512M Docker cap. Instead
	// handleServeFile sets a per-conn write deadline via
	// http.ResponseController (Go 1.20+); the deadline fires on the
	// underlying conn without buffering and without breaking sendfile.
	const (
		shortTimeout  = 30 * time.Second // health, list, delete, get-row
		uploadTimeout = 30 * time.Minute // upload + replace stream; covers up to ~50KB/s on 100MiB
	)

	mux := http.NewServeMux()
	// Unauthenticated routes.
	mux.Handle("GET /healthz", http.TimeoutHandler(http.HandlerFunc(s.handleHealthz), shortTimeout, "healthz timeout"))
	mux.Handle("GET /s/{slug}", http.HandlerFunc(s.handleServeFile))
	// Bearer-gated API. Single "upload" role guards all mutations + list.
	mux.Handle("POST /api/upload",
		auth.middleware(http.TimeoutHandler(requireRole(roleUpload, s.handleUpload), uploadTimeout, "upload timeout")))
	mux.Handle("PUT /api/files/{slug}",
		auth.middleware(http.TimeoutHandler(requireRole(roleUpload, s.handleReplaceFile), uploadTimeout, "replace timeout")))
	mux.Handle("GET /api/files",
		auth.middleware(http.TimeoutHandler(requireRole(roleUpload, s.handleListFiles), shortTimeout, "list timeout")))
	mux.Handle("DELETE /api/files/{slug}",
		auth.middleware(http.TimeoutHandler(requireRole(roleUpload, s.handleDeleteFile), shortTimeout, "delete timeout")))

	return &http.Server{
		Addr:    cfg.Listen,
		Handler: withRequestLogger(log, mux),
		// ReadHeaderTimeout defeats slowloris on the header parse;
		// load-bearing for every endpoint. No global Read/Write
		// timeouts on purpose -- those would cut /s/ downloads on
		// slow links. Per-route timeouts above bound the rest.
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
}
