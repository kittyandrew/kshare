package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/kittyandrew/kshare/internal/api"
)

// runReplace implements `kshare replace <slug> <file> [--ttl X]`.
// Sends PUT /api/files/<slug>. Slug stays, so the public URL is
// unchanged across replacements. On-disk extension follows the new
// filename; the old on-disk file is cleaned up.
func runReplace(args []string) {
	ttl, pos := parseTTLFlag(args, "kshare replace <slug> <file> [--ttl 24h]")
	if len(pos) != 2 {
		fail("kshare replace: expected <slug> <file>, got %d positional args", len(pos))
	}
	slug, path := pos[0], pos[1]
	if !api.SlugRe.MatchString(slug) {
		fail("kshare replace: %q is not a valid slug (expected pattern %s)", slug, api.SlugRe)
	}

	f, err := os.Open(path)
	if err != nil {
		fail("kshare replace: open %s: %v", path, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		fail("kshare replace: stat %s: %v", path, err)
	}
	if info.IsDir() {
		fail("kshare replace: %s is a directory; only files can be uploaded", path)
	}
	if info.Size() == 0 {
		fail("kshare replace: %s is empty; refusing to upload", path)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	req, err := newRequest(ctx, http.MethodPut, "/api/files/"+slug, f)
	if err != nil {
		failRequest(err)
	}
	req.ContentLength = info.Size()
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-KShare-Filename", filepath.Base(path))
	if ttl != "" {
		req.Header.Set("X-KShare-TTL", ttl)
	}

	var resp api.Upload
	if err := doJSON(req, &resp); err != nil {
		failRequest(err)
	}

	server, _ := serverURL()
	url := publicURL(server, &resp)
	fmt.Println(url)
	wlCopy(url)
}
