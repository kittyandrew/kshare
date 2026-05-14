package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// httpClient is the per-process http.Client. 30 min timeout covers
// large uploads over slow links; individual subcommands can shrink
// the per-request budget via context if needed.
var httpClient = &http.Client{Timeout: 30 * time.Minute}

// serverURL pulls the server base URL out of the persisted token
// file (set during `kshare auth login`). Server is normalised (no
// trailing slash) at login time, so callers can concatenate directly.
// Returns errNotLoggedIn if no token exists yet.
func serverURL() (string, error) {
	t, err := loadTokens()
	if err != nil {
		// missing file or unreadable: rendered as "log in again"
		// upstream (the actual auth call would also fail with
		// errNotLoggedIn).
		return "", err
	}
	if t.Server == "" {
		return "", errors.New("auth.json has no server URL; re-run `kshare auth login`")
	}
	return t.Server, nil
}

// doJSON issues req, decodes JSON-on-2xx into out, and translates
// non-2xx into a flat error including the response body. Bearer
// attachment is the caller's responsibility (call attachBearer
// before passing the request in).
func doJSON(req *http.Request, out any) error {
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", req.Method, req.URL.String(), err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusUnauthorized {
		// 401 from the server means the token rotated invalid -- the
		// refresh-on-expiry path in attachBearer didn't catch it
		// (server is enforcing aud/role too). Push the user back
		// through the device flow.
		return errNotLoggedIn
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %s: %s",
			req.Method, req.URL.String(), resp.Status, strings.TrimSpace(string(body)))
	}
	if out == nil {
		return nil
	}
	if len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode response from %s: %w (body: %s)",
			req.URL.String(), err, string(body))
	}
	return nil
}

// newRequest builds an authenticated *http.Request against the
// server base URL. ctx controls the request lifetime; the caller is
// responsible for setting body content-type when applicable.
func newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	base, err := serverURL()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return nil, err
	}
	if err := attachBearer(req); err != nil {
		return nil, err
	}
	return req, nil
}
