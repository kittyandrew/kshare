package main

import (
	"mime"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/kittyandrew/kshare/internal/api"
)

// serveTimeout bounds slow-reader DoS on /s/{slug}: without it, 1000 connections at 1 byte/sec pin
// goroutines and FDs indefinitely. 30 min covers a phone drinking 100 MiB at ~50 KB/s.
//
// http.TimeoutHandler is unusable here: it buffers the whole response, which kills ServeContent's
// sendfile(2) path and turns 5 concurrent 100 MiB downloads into 500 MiB of heap against the documented
// `--memory=512M` cap. handleServeFile sets a conn write deadline via http.ResponseController instead, so
// nothing buffers and sendfile survives.
const serveTimeout = 30 * time.Minute

// handleServeFile is the public, UNAUTHENTICATED read route. The random 48-bit slug is the only access
// control. Misses (bad shape, no row, file gone) emit a "slug_miss" WARN line for fail2ban.
func (s *server) handleServeFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := zerolog.Ctx(ctx)

	slug := r.PathValue("slug")
	if !api.SlugRe.MatchString(slug) {
		s.logSlugMiss(r, "bad_format", slug)
		http.NotFound(w, r)
		return
	}

	u, err := s.container.GetBySlug(ctx, slug)
	if err != nil {
		httpError(ctx, w, http.StatusInternalServerError, "get upload", err)
		return
	}
	if u == nil {
		s.logSlugMiss(r, "no_row", slug)
		http.NotFound(w, r)
		return
	}

	// Set before the first byte so ServeContent's send starts the clock. A failure only costs the
	// slow-reader bound, so log and serve anyway rather than 500 a working download.
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(serveTimeout)); err != nil {
		log.Debug().Err(err).Msg("serve: SetWriteDeadline failed; continuing without slow-reader bound")
	}

	f, err := os.Open(s.container.FilePath(u.DiskName()))
	if err != nil {
		if os.IsNotExist(err) {
			// Sweeper raced, or someone pruned files/ by hand. WARN so the orphan is visible.
			log.Warn().Str("slug", slug).Str("disk_name", u.DiskName()).
				Msg("serve: row exists but file missing on disk")
			http.NotFound(w, r)
			return
		}
		httpError(ctx, w, http.StatusInternalServerError, "open file", err)
		return
	}
	defer f.Close()

	// Pin Content-Type from the DB (sniffed at upload); ServeContent would otherwise re-sniff and can differ.
	if ct := strings.TrimSpace(u.ContentType); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	// `inline` keeps browser rendering while giving save-as the real filename. FormatMediaType handles
	// RFC 2616 quoting and RFC 5987 encoding for non-ASCII names.
	if u.OriginalFilename != "" {
		cd := mime.FormatMediaType("inline", map[string]string{"filename": u.OriginalFilename})
		if cd != "" {
			w.Header().Set("Content-Disposition", cd)
		}
	}
	http.ServeContent(w, r, u.DiskName(), u.UploadedAt, f)
}

// logSlugMiss emits the WARN line the host's fail2ban jail matches on (systemd backend, journalmatch on the
// container; <HOST> extracts client_ip). With no parseable IP the event becomes slug_miss_no_ip, so the
// jail's `event=slug_miss` regex can't fire on an unmatchable <HOST>: the unbannable case stays visible
// without poisoning the ban pipeline.
func (s *server) logSlugMiss(r *http.Request, reason, attempted string) {
	ip := clientIP(r)
	event := "slug_miss"
	if ip == "" {
		event = "slug_miss_no_ip"
	}
	zerolog.Ctx(r.Context()).Warn().
		Str("event", event).
		Str("reason", reason).
		Str("client_ip", ip).
		Str("attempted", attempted).
		Str("user_agent", r.Header.Get("User-Agent")).
		Msg("slug miss")
}

// clientIP returns a validated IP literal, or "" when nothing parses (see logSlugMiss for what "" costs).
//
// X-Forwarded-For is trusted unconditionally because production runs behind Caddy. Exposed directly, the
// header lets a caller spoof its own IP and dodge the jail; the operator owns that choice.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		candidate := strings.TrimSpace(first)
		if ip := net.ParseIP(candidate); ip != nil {
			return ip.String()
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return ""
}
