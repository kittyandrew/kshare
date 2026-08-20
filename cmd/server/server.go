package main

import (
	"net/http"
	"time"

	"github.com/rs/zerolog"

	"github.com/kittyandrew/kshare/internal/store"
)

// server carries what handlers need without globals. No auth field: auth.require wraps them at registration
// time in newServer, so a handler never has an authenticator to misuse.
type server struct {
	cfg       *config
	container *store.Container
	log       zerolog.Logger
}

// newServer wires the route table (Go 1.22+ method-prefixed patterns) and returns an *http.Server.
func newServer(cfg *config, auth *authenticator, container *store.Container, log zerolog.Logger) *http.Server {
	s := &server{
		cfg:       cfg,
		container: container,
		log:       log,
	}

	// Per-route timeouts. http.TimeoutHandler buffers the whole response in memory: fine for JSON / 204 / 413
	// bodies, ruinous for /s/, which is left unwrapped and bounds itself with a conn write deadline instead
	// (see serveTimeout in serve.go).
	const (
		shortTimeout  = 30 * time.Second // healthz, list, delete
		uploadTimeout = 30 * time.Minute // upload + replace stream; ~50KB/s on 100MiB
	)

	mux := http.NewServeMux()
	mux.Handle("GET /healthz", http.TimeoutHandler(http.HandlerFunc(s.handleHealthz), shortTimeout, "healthz timeout"))
	mux.Handle("GET /s/{slug}", http.HandlerFunc(s.handleServeFile))
	mux.Handle("POST /api/upload",
		auth.require(roleUpload, http.TimeoutHandler(http.HandlerFunc(s.handleUpload), uploadTimeout, "upload timeout")))
	mux.Handle("PUT /api/files/{slug}",
		auth.require(roleUpload, http.TimeoutHandler(http.HandlerFunc(s.handleReplaceFile), uploadTimeout, "replace timeout")))
	mux.Handle("GET /api/files",
		auth.require(roleUpload, http.TimeoutHandler(http.HandlerFunc(s.handleListFiles), shortTimeout, "list timeout")))
	mux.Handle("DELETE /api/files/{slug}",
		auth.require(roleUpload, http.TimeoutHandler(http.HandlerFunc(s.handleDeleteFile), shortTimeout, "delete timeout")))

	return &http.Server{
		Addr:    cfg.Listen,
		Handler: withRequestLogger(log, mux),
		// ReadHeaderTimeout defeats slowloris on the header parse. No global Read/Write timeouts on purpose:
		// they would cut /s/ downloads on slow links, and the per-route timeouts do the rest.
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
}
