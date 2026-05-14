package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/kittyandrew/kshare/internal/api"
)

// publicURL returns the URL a browser would hit to fetch this upload.
// Built from the server URL persisted in auth.json -- server URL ==
// public URL by construction in this app. Server is already trimmed
// of trailing slashes at login time, so no defensive trim here. The
// URL is slug-only; the original filename's extension surfaces via
// the Content-Disposition header on the served response.
func publicURL(server string, r *api.Upload) string {
	return server + "/s/" + r.Slug
}

// runUpload parses positional args + --ttl, streams the file as the
// POST body with X-KShare-* headers carrying ttl + filename, prints
// the URL, best-effort copies to the wayland clipboard.
func runUpload(args []string) {
	ttl, pos := parseTTLFlag(args, "kshare <file> [--ttl 24h]")
	if len(pos) == 0 {
		fail("kshare: usage: kshare <file> [--ttl 24h]")
	}
	if len(pos) > 1 {
		fail("kshare: only one file per upload; got %d", len(pos))
	}
	path := pos[0]

	f, err := os.Open(path)
	if err != nil {
		fail("kshare: open %s: %v", path, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		fail("kshare: stat %s: %v", path, err)
	}
	if info.IsDir() {
		fail("kshare: %s is a directory; only files can be uploaded", path)
	}
	if info.Size() == 0 {
		fail("kshare: %s is empty; refusing to upload", path)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	req, err := newRequest(ctx, http.MethodPost, "/api/upload", f)
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

// wlCopy is a best-effort write to the wayland clipboard. If wl-copy
// isn't installed or it fails, we just skip -- the URL is already on
// stdout.
func wlCopy(s string) {
	bin, err := exec.LookPath("wl-copy")
	if err != nil {
		return
	}
	cmd := exec.Command(bin)
	cmd.Stdin = bytes.NewBufferString(s)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	_ = cmd.Run()
}
