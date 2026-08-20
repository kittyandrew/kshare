package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// The 30 min timeout covers large uploads over slow links; subcommands shrink it per request via context.
var httpClient = &http.Client{Timeout: 30 * time.Minute}

// Body-size caps for response reads, so a hostile or buggy server can't OOM the CLI with a huge body.
// errBodyMax covers non-2xx bodies, which are short by design ("unauthorized\n" is 13 bytes, "forbidden:
// missing role upload\n" is 32). jsonBodyMax covers 2xx JSON, where the largest response is a file list and
// stays well under 64KB even with hundreds of entries.
const (
	errBodyMax  = 256
	jsonBodyMax = 64 * 1024
)

// server401Err builds the errRefreshFailed-shape error for any HTTP 401 from kshared. The response body
// carries the load-bearing reason ("unauthorized", "forbidden: missing role upload"), so read up to
// errBodyMax of it and join it with the sentinel, routing callers through failRequest's "auth rejected" hint.
//
// The caller must defer resp.Body.Close(); this reads but does not close.
func server401Err(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, errBodyMax))
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = resp.Status
	}
	return errors.Join(errRefreshFailed, fmt.Errorf("server rejected token: %s", msg))
}

// doJSON issues req, decodes JSON-on-2xx into out, and turns non-2xx into a flat error carrying the response
// body. Bearer attachment is the caller's job (newRequest does it).
//
// A 401 after newRequest already refreshed means the token is fresh and the IdP accepted it, but the server
// is rejecting it: audience mismatch, JWT vs Bearer setting reverted, clock skew. Logging in again mints an
// identically-shaped token, so 401 routes through server401Err / errRefreshFailed, the same shape as a
// refresh-grant rejection. printRefreshError in main.go surfaces the message.
func doJSON(req *http.Request, out any) error {
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", req.Method, req.URL.String(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return server401Err(resp)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, errBodyMax))
		return fmt.Errorf("%s %s: %s: %s",
			req.Method, req.URL.String(), resp.Status, strings.TrimSpace(string(body)))
	}
	if out == nil {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, jsonBodyMax))
	if len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode response from %s: %w (body: %s)",
			req.URL.String(), err, string(body))
	}
	return nil
}

// newRequest loads tokens, refreshes on expiry, and builds an authenticated *http.Request against the
// persisted server URL. It returns the loaded tokenFile too, so callers can reuse t.Server (for building a
// public URL, say) without reading auth.json again.
//
// ctx controls both the refresh roundtrip and the resulting request; the caller sets the body content-type.
// Errors follow the project's error-routing contract:
//
//   - errNotLoggedIn: auth.json missing or has no refresh_token.
//   - errRefreshFailed-joined: refresh exchange failed.
//   - plain wrapped error: corrupt auth.json or http.NewRequest failure.
func newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, *tokenFile, error) {
	t, err := loadTokens()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, errNotLoggedIn
		}
		return nil, nil, err
	}
	if t.Server == "" {
		return nil, nil, errors.New("auth.json has no server URL; re-run `kshare auth login`")
	}
	fresh, err := ensureFreshAccessToken(ctx, t)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, fresh.Server+path, body)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+fresh.AccessToken)
	return req, fresh, nil
}
