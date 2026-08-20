package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/kittyandrew/kshare/internal/api"
)

// runDelete implements `kshare rm <slug>`. Silent on success, one-line stderr error otherwise.
func runDelete(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: kshare rm <slug>")
		os.Exit(1)
	}
	slug := args[0]
	if !api.SlugRe.MatchString(slug) {
		fail("kshare rm: %q is not a valid slug (expected pattern %s)", slug, api.SlugRe)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, _, err := newRequest(ctx, http.MethodDelete, "/api/files/"+slug, nil)
	if err != nil {
		failRequest(err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		fail("kshare rm: %v", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNoContent:
		return
	case http.StatusNotFound:
		fmt.Fprintf(os.Stderr, "kshare: no such slug %s\n", slug)
		os.Exit(1)
	case http.StatusUnauthorized:
		// A post-refresh 401 is server-side misconfig, not "log in again". Same routing as doJSON.
		failRequest(server401Err(resp))
	default:
		// Pull whatever body the server returned into the error; status messages are short by design.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, errBodyMax))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			fail("kshare rm: server returned %s", resp.Status)
		}
		fail("kshare rm: server returned %s: %s", resp.Status, msg)
	}
}
