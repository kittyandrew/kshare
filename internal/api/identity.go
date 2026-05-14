package api

import "regexp"

// SlugRe is the slug validation regex used everywhere a slug appears
// in a URL path or argument. 8 base64url characters from 6 random
// bytes = 48 bits of entropy. Mirrors generateSlug() in
// cmd/server/helpers.go.
//
// Single-uploader scope + fail2ban backstop make 48 bits comfortably
// safe: brute-force at 1k req/s expected to first hit after ~10^11
// seconds (a few thousand years).
//
// This is the only identity regex shared with the CLI -- the server
// owns the on-disk extension allowlist (it controls what lands on
// disk; URL never carries the extension). See cmd/server/diskname.go.
var SlugRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8}$`)
