package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/kittyandrew/kshare/internal/api"
)

// fileRequest is one file heading for the server; upload and replace differ only in these fields.
type fileRequest struct {
	method string // http.MethodPost for upload, http.MethodPut for replace
	path   string // "/api/upload" or "/api/files/<slug>"
	label  string // error prefix, e.g. "kshare" or "kshare replace"
	file   string // local path to stream
	ttl    string // "" leaves the TTL to the server default
}

// send streams the file as the request body, prints the resulting public URL on stdout and copies it to the
// clipboard. Exits the process on any failure; there is nothing for a caller to recover from.
func (fr fileRequest) send() {
	f, err := os.Open(fr.file)
	if err != nil {
		fail("%s: open %s: %v", fr.label, fr.file, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		fail("%s: stat %s: %v", fr.label, fr.file, err)
	}
	if info.IsDir() {
		fail("%s: %s is a directory; only files can be uploaded", fr.label, fr.file)
	}
	if info.Size() == 0 {
		fail("%s: %s is empty; refusing to upload", fr.label, fr.file)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	req, t, err := newRequest(ctx, fr.method, fr.path, f)
	if err != nil {
		failRequest(err)
	}
	req.ContentLength = info.Size()
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set(api.HeaderFilename, filepath.Base(fr.file))
	if fr.ttl != "" {
		req.Header.Set(api.HeaderTTL, fr.ttl)
	}

	var resp api.Upload
	if err := doJSON(req, &resp); err != nil {
		failRequest(err)
	}

	url := publicURL(t.Server, &resp)
	fmt.Println(url)
	wlCopy(url)
}

// publicURL builds the browser URL from the server URL in auth.json, which is the public URL by
// construction here. runLogin already trimmed trailing slashes, so there is no defensive trim. Slug-only:
// the filename's extension rides on Content-Disposition.
func publicURL(server string, r *api.Upload) string {
	return server + "/s/" + r.Slug
}

// wlCopy is a best-effort write to the wayland clipboard. If wl-copy isn't installed or fails, skip it: the
// URL is already on stdout.
//
// @NOTE: stdout AND stderr MUST be *os.File (not io.Discard, not any io.Writer), because wl-copy daemonises
// itself to hold the clipboard for the rest of the Wayland session, and while daemonising it redirects its
// own stdin + stdout to /dev/null but LEAVES STDERR INHERITED (see wl-copy.c). Hand exec a Go writer for
// stderr and exec.Cmd builds a pipe plus a copy-goroutine that drains until EOF, which only arrives when the
// daemon closes that inherited FD, i.e. at logout: cmd.Run() then blocks for the whole session. Passing
// /dev/null as *os.File makes Go dup2 it straight into the child, so there is no pipe, no goroutine and no
// wait. If /dev/null won't open, skip rather than fall back to inherited FDs, which reintroduces the hang.
func wlCopy(s string) {
	bin, err := exec.LookPath("wl-copy")
	if err != nil {
		return
	}
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return
	}
	defer devNull.Close()
	cmd := exec.Command(bin)
	cmd.Stdin = bytes.NewBufferString(s)
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	_ = cmd.Run()
}
