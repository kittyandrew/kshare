package main

import (
	"net/http"

	"github.com/kittyandrew/kshare/internal/api"
)

// runReplace implements `kshare replace <slug> <file> [--ttl X]`. The slug stays, so the public URL survives
// the replacement; the on-disk extension follows the new filename and the old file is cleaned up.
func runReplace(args []string) {
	ttl, pos := parseTTLFlag(args, "kshare replace <slug> <file> [--ttl 24h]")
	if len(pos) != 2 {
		fail("kshare replace: expected <slug> <file>, got %d positional args", len(pos))
	}
	slug, path := pos[0], pos[1]
	if !api.SlugRe.MatchString(slug) {
		fail("kshare replace: %q is not a valid slug (expected pattern %s)", slug, api.SlugRe)
	}

	fileRequest{
		method: http.MethodPut,
		path:   "/api/files/" + slug,
		label:  "kshare replace",
		file:   path,
		ttl:    ttl,
	}.send()
}
