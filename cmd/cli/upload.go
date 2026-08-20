package main

import "net/http"

// runUpload is the default verb: `kshare <file> [--ttl 24h]`.
func runUpload(args []string) {
	ttl, pos := parseTTLFlag(args, "kshare <file> [--ttl 24h]")
	if len(pos) == 0 {
		fail("kshare: usage: kshare <file> [--ttl 24h]")
	}
	if len(pos) > 1 {
		fail("kshare: only one file per upload; got %d", len(pos))
	}

	fileRequest{
		method: http.MethodPost,
		path:   "/api/upload",
		label:  "kshare",
		file:   pos[0],
		ttl:    ttl,
	}.send()
}
