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

// httpClient is the per-process http.Client. 30 min timeout covers
// large uploads over slow links; individual subcommands can shrink
// the per-request budget via context if needed.
var httpClient = &http.Client{Timeout: 30 * time.Minute}

// Body-size caps for response reads. errBodyMax is the cap for non-2xx
// bodies (libserv-shaped errors are short by design -- "unauthorized\n"
// is 13 bytes, "forbidden: missing role upload\n" is 32). jsonBodyMax
// is the cap for 2xx JSON: kshare's largest responses are file-list
// arrays, ample under 64KB even with hundreds of entries. Caps exist
// so a hostile or buggy server can't OOM the CLI via a huge response
// body.
const (
	errBodyMax  = 256
	jsonBodyMax = 64 * 1024
)

// server401Err builds the errRefreshFailed-shape error returned for
// any HTTP 401 from kshared. The server's response body carries the
// load-bearing reason (kshared returns short libserv-shaped messages
// like "unauthorized" or "forbidden: missing role upload"); we read
// up to errBodyMax bytes and join it with the sentinel so callers
// route through failRequest's "auth rejected" hint.
//
// Caller must defer resp.Body.Close(); we read but don't close.
func server401Err(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, errBodyMax))
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = resp.Status
	}
	return errors.Join(errRefreshFailed, fmt.Errorf("server rejected token: %s", msg))
}

// doJSON issues req, decodes JSON-on-2xx into out, and translates
// non-2xx into a flat error including the response body. Bearer
// attachment is the caller's responsibility (newRequest sets it).
//
// 401 from kshared after newRequest successfully refreshed means
// the token is fresh and the IdP accepted it -- but the server is
// rejecting it (audience mismatch, JWT vs Bearer setting reverted,
// clock skew). Re-logging in mints another identical-shape token,
// so we route 401 through server401Err / errRefreshFailed (same
// shape as a refresh-grant rejection: "see the upstream error, fix
// the cause"). printRefreshError in main.go surfaces the message.
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

// newRequest loads tokens, refreshes on expiry, builds an
// authenticated *http.Request against the persisted server URL, and
// returns BOTH the request and the loaded tokenFile. Returning the
// token lets callers reuse t.Server for things like building public
// URLs without re-reading auth.json -- before this signature,
// upload/replace did three loadTokens() per command (server URL,
// bearer attachment, server URL again for the public URL).
//
// ctx controls both the refresh roundtrip and the resulting request;
// the caller is responsible for setting body content-type. Errors
// follow the project's error-routing contract:
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
