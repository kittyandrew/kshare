package api

import "regexp"

// SlugRe is the slug shape: 8 base64url chars from 6 random bytes, 48 bits. Must stay in sync with
// generateSlug in cmd/server/helpers.go.
//
// Single-uploader scope plus the fail2ban backstop make 48 bits comfortably safe: brute force at 1k req/s
// expects a first hit after ~10^11 seconds, a few thousand years.
//
// The on-disk extension allowlist deliberately does not live here: the URL never carries an extension, so
// only the server needs it (cmd/server/diskname.go).
var SlugRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8}$`)
