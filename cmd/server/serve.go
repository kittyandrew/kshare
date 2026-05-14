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

// serveTimeout caps slow-reader DoS on /s/{slug}. Without it, an
// attacker holding 1000 connections at 1 byte/sec each would pin
// goroutines + FDs indefinitely. 30 min covers legitimate slow phones
// drinking a 100 MiB file at ~50 KB/s with margin.
//
// We CANNOT use http.TimeoutHandler for this route: it buffers every
// byte of the response in memory (Go stdlib explicit), which would
// kill the http.ServeContent sendfile(2) fast path AND turn 5
// concurrent 100 MiB downloads into 500 MiB of resident heap --
// within the documented --memory=512M Docker cap. Instead we set a
// write deadline on the underlying conn via http.ResponseController;
// the deadline fires without buffering and without breaking sendfile.
const serveTimeout = 30 * time.Minute

// handleServeFile is the public, UNAUTHENTICATED read route.
// The random 48-bit slug is the only access control.
//
// Misses (bad shape, no row, file gone) emit a structured "slug_miss"
// WARN log so a host-level fail2ban jail can ipban repeat offenders.
// Caddy in production forwards X-Forwarded-For; we trust it
// unconditionally per operator decision (production always sits behind
// a reverse proxy that owns the L7 trust boundary).
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

	// Per-conn write deadline. Slow-reader DoS bound: serveTimeout.
	// Set before writing anything so ServeContent's first byte starts
	// the clock. SetWriteDeadline errors are non-fatal (some test
	// ResponseWriters don't implement it) -- log + continue.
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(serveTimeout)); err != nil {
		log.Debug().Err(err).Msg("serve: SetWriteDeadline failed; continuing without slow-reader bound")
	}

	f, err := os.Open(s.container.FilePath(u.DiskName()))
	if err != nil {
		if os.IsNotExist(err) {
			// Row exists but file is gone -- sweeper raced or someone
			// pruned the dir by hand. Log at WARN so the orphan is
			// visible.
			log.Warn().Str("slug", slug).Str("disk_name", u.DiskName()).
				Msg("serve: row exists but file missing on disk")
			http.NotFound(w, r)
			return
		}
		httpError(ctx, w, http.StatusInternalServerError, "open file", err)
		return
	}
	defer f.Close()

	// Pin Content-Type from DB (sniffed at upload). ServeContent
	// would otherwise re-sniff and might differ.
	if ct := strings.TrimSpace(u.ContentType); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	// Content-Disposition surfaces the original filename for
	// browser save-as without affecting inline rendering. mime
	// .FormatMediaType handles RFC 2616 quoting + RFC 5987
	// fallback for non-ASCII names.
	if u.OriginalFilename != "" {
		cd := mime.FormatMediaType("inline", map[string]string{"filename": u.OriginalFilename})
		if cd != "" {
			w.Header().Set("Content-Disposition", cd)
		}
	}
	http.ServeContent(w, r, u.DiskName(), u.UploadedAt, f)
}

// logSlugMiss emits a structured WARN line for fail2ban consumption.
// Key fields: event=slug_miss, client_ip, path, reason. The host's
// fail2ban jail (systemd backend, journalmatch on the kshare
// container) regex-extracts client_ip via <HOST> token to ban
// repeat offenders.
//
// Guard: if we can't determine a non-empty client IP, we still log
// at WARN but emit `event=slug_miss_no_ip` so the fail2ban regex
// (keyed on `event=slug_miss`) doesn't fire with an unmatchable
// <HOST>. This makes the unbannable case visible to operators
// without poisoning the ban pipeline.
func (s *server) logSlugMiss(r *http.Request, reason, attempted string) {
	ip := clientIP(r)
	event := "slug_miss"
	if ip == "" {
		event = "slug_miss_no_ip"
	}
	// `path` is already set on every request logger by
	// withRequestLogger; don't duplicate it here.
	zerolog.Ctx(r.Context()).Warn().
		Str("event", event).
		Str("reason", reason).
		Str("client_ip", ip).
		Str("attempted", attempted).
		Str("user_agent", r.Header.Get("User-Agent")).
		Msg("slug miss")
}

// clientIP returns the client's IP address as a validated literal,
// or "" if no candidate parses cleanly. Empty return triggers the
// slug_miss_no_ip event variant (see logSlugMiss) so fail2ban's
// regex on `event=slug_miss` doesn't fire with an unmatchable HOST.
//
// Precedence: first comma-separated entry of X-Forwarded-For (Caddy
// + similar reverse proxies put the original client there); falls
// back to RemoteAddr's host portion.
//
// We trust X-Forwarded-For unconditionally -- production runs behind
// Caddy. Direct exposure means attackers can spoof their client IP;
// the operator owns that choice.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		candidate := xff
		if comma := strings.Index(xff, ","); comma >= 0 {
			candidate = xff[:comma]
		}
		candidate = strings.TrimSpace(candidate)
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
