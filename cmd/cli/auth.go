// CLI-side OIDC: RFC 8628 device-flow login, token persistence,
// refresh-on-expiry, bearer-header attachment. Tokens live in
// ${XDG_CONFIG_HOME}/kshare/auth.json (mode 0600). The server URL,
// issuer, client_id, audience are all persisted alongside the tokens
// so subsequent CLI calls + token refresh don't need the env again.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zitadel/oidc/v3/pkg/client/rp"
	"github.com/zitadel/oidc/v3/pkg/oidc"
)

// tokenFile is the on-disk schema. The IdP config (issuer, client_id,
// audience) plus the server URL are persisted so refresh + later
// commands work without re-reading the env -- and so multiple
// concurrent CLIs can't disagree about which server or IdP they're
// talking to.
type tokenFile struct {
	Issuer       string    `json:"issuer"`
	ClientID     string    `json:"client_id"`
	Audience     string    `json:"audience"`
	Server       string    `json:"server"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// expired returns true if the access token is past or within 30s of
// its expiry. The grace window keeps long-running operations from
// faceplanting on a token that was good when the call started.
func (t *tokenFile) expired() bool {
	return time.Now().After(t.ExpiresAt.Add(-30 * time.Second))
}

func tokenPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve config dir: %w", err)
	}
	return filepath.Join(dir, "kshare", "auth.json"), nil
}

func loadTokens() (*tokenFile, error) {
	p, err := tokenPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var t tokenFile
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	return &t, nil
}

func saveTokens(t *tokenFile) error {
	p, err := tokenPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(p), err)
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

func deleteTokens() error {
	p, err := tokenPath()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// loginScopes is the set requested in the device flow. The audience
// URN pins the project id into the resulting token's aud[] (so the
// server can audience-check); the roles URN ensures the project role
// claim is asserted on the token.
func loginScopes(audience string) []string {
	return []string{
		"openid",
		"offline_access",
		"urn:zitadel:iam:org:project:id:" + audience + ":aud",
		"urn:zitadel:iam:org:projects:roles",
	}
}

// runLogin runs the RFC 8628 device authorization flow against the
// configured Zitadel issuer and persists the resulting tokens.
// Reads OIDC config from env (KSHARE_OIDC_ISSUER + _CLIENT_ID +
// _AUDIENCE -- mandatory). Server URL is the explicit --server flag
// (defaults to http://localhost:6980 for local dev; pass the public
// hostname for production).
func runLogin(args []string) {
	server := "http://localhost:6980"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--server":
			if i+1 >= len(args) {
				fail("kshare auth login: --server requires a URL argument")
			}
			server = args[i+1]
			i++
		case "-h", "--help":
			fmt.Fprintln(os.Stderr, "usage: kshare auth login [--server URL]")
			os.Exit(0)
		default:
			fail("kshare auth login: unknown argument %q", args[i])
		}
	}

	// Validate --server is an absolute http(s) URL. Reject silently
	// passing `localhost:6980` (no scheme) etc. -- those persist as
	// nonsense and surface as cryptic errors on later commands.
	u, err := url.Parse(server)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		fail("kshare auth login: --server must be an absolute http(s) URL, got %q", server)
	}

	// Normalise URL inputs once, here, so nothing downstream has to
	// re-defend against trailing slashes.
	issuer := strings.TrimRight(os.Getenv("KSHARE_OIDC_ISSUER"), "/")
	clientID := os.Getenv("KSHARE_OIDC_CLIENT_ID")
	audience := os.Getenv("KSHARE_OIDC_AUDIENCE")
	server = strings.TrimRight(server, "/")
	if issuer == "" || clientID == "" || audience == "" {
		fail("kshare auth login: KSHARE_OIDC_ISSUER, KSHARE_OIDC_CLIENT_ID, and KSHARE_OIDC_AUDIENCE are all required (env)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	scopes := loginScopes(audience)
	relying, err := rp.NewRelyingPartyOIDC(ctx, issuer, clientID, "", "", scopes)
	if err != nil {
		fail("kshare auth login: oidc init: %v", err)
	}

	da, err := rp.DeviceAuthorization(ctx, scopes, relying, nil)
	if err != nil {
		fail("kshare auth login: device authorization: %v", err)
	}

	// Zitadel's device_authorization_endpoint returns verification
	// URLs pointing at the legacy v1 login UI (/device, /ui/login/),
	// which has known WebAuthn projection bugs for org-scoped users.
	// Construct the v2 login URL from the issuer base instead.
	verify := issuer + "/ui/v2/login/device?user_code=" + url.QueryEscape(da.UserCode)
	fmt.Fprintf(os.Stderr, "kshare: open %s\n", verify)
	fmt.Fprintf(os.Stderr, "kshare: code %s\n", da.UserCode)

	tok, err := rp.DeviceAccessToken(ctx, da.DeviceCode, time.Duration(da.Interval)*time.Second, relying)
	if err != nil {
		fail("kshare auth login: %v", err)
	}

	t := &tokenFile{
		Issuer:       issuer,
		ClientID:     clientID,
		Audience:     audience,
		Server:       server,
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second),
	}
	if err := saveTokens(t); err != nil {
		fail("kshare auth login: save tokens: %v", err)
	}
	fmt.Fprintln(os.Stderr, "kshare: logged in")
}

func runLogout() {
	if err := deleteTokens(); err != nil {
		fail("kshare auth logout: %v", err)
	}
	fmt.Fprintln(os.Stderr, "kshare: logged out")
}

// runStatus prints the current auth state: server URL, IdP config,
// token expiry, and the result of a real bearer-gated request
// against the server. Local-only checks (clock-based expiry) can
// pass while the token is actually un-verifiable -- this is how we
// caught the Zitadel "Auth Token Type: Bearer" misconfig where the
// CLI receives a JWE that the server can't parse as a JWT. The
// `api` line covers that gap by sending an actual request.
func runStatus() {
	t, err := loadTokens()
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, "kshare: not logged in; run `kshare auth login`")
			os.Exit(1)
		}
		fail("kshare auth status: %v", err)
	}
	left := time.Until(t.ExpiresAt).Round(time.Second)
	freshness := "valid"
	if t.expired() {
		freshness = "expired (next call will refresh)"
	}
	fmt.Printf("server:    %s\n", t.Server)
	fmt.Printf("issuer:    %s\n", t.Issuer)
	fmt.Printf("audience:  %s\n", t.Audience)
	fmt.Printf("client_id: %s\n", t.ClientID)
	fmt.Printf("token:     %s (%s, %s)\n",
		freshness, t.ExpiresAt.Local().Format(time.RFC3339), humanLeft(left))
	fmt.Printf("api:       %s\n", probeAPI(t))
}

// probeAPI sends a GET /api/files with a short timeout. Returns a
// one-line human-readable status. Covers four failure modes plus the
// happy path:
//   - Network error (server down, wrong URL): "unreachable: <err>"
//   - 401 (token rejected by server): "FAIL (401) -- token rejected;
//     check Zitadel `kshare-cli` -> Token Settings -> Auth Token
//     Type: JWT, and 'Add user roles to access token' enabled"
//   - 403 (token authentic but role missing): "FAIL (403) -- missing
//     `upload` role; grant via Zitadel project authorisations"
//   - Other non-2xx: "FAIL (<status>) -- <reason phrase>"
//   - 2xx: "ok"
func probeAPI(t *tokenFile) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.Server+"/api/files", nil)
	if err != nil {
		return fmt.Sprintf("internal: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+t.AccessToken)
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Sprintf("unreachable: %v", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return "ok"
	case http.StatusUnauthorized:
		return "FAIL (401) -- token rejected by server; check Zitadel `kshare-cli` Token Settings: Auth Token Type must be JWT (not Bearer Token)"
	case http.StatusForbidden:
		return "FAIL (403) -- token authentic but missing `upload` role; grant via Zitadel project authorisations"
	default:
		return fmt.Sprintf("FAIL (%d) -- %s", resp.StatusCode, resp.Status)
	}
}

// humanLeft formats a duration with a sign hint ("expired Xm ago" vs
// "in Xh"). Used by status output.
func humanLeft(d time.Duration) string {
	if d < 0 {
		return "expired " + (-d).String() + " ago"
	}
	return "in " + d.String()
}

// ensureFreshAccessToken loads the persisted tokens, refreshes them
// if expired, persists the rotated set, and returns the access token.
// Refresh failures are distinguished from missing-tokens so the
// caller can tell the user to log in again vs surface a generic
// error.
func ensureFreshAccessToken(ctx context.Context) (*tokenFile, error) {
	t, err := loadTokens()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errNotLoggedIn
		}
		return nil, err
	}
	if !t.expired() {
		return t, nil
	}
	if t.RefreshToken == "" {
		return nil, errNotLoggedIn
	}
	relying, err := rp.NewRelyingPartyOIDC(ctx, t.Issuer, t.ClientID, "", "", loginScopes(t.Audience))
	if err != nil {
		return nil, fmt.Errorf("oidc init: %w", err)
	}
	refreshed, err := rp.RefreshTokens[*oidc.IDTokenClaims](ctx, relying, t.RefreshToken, "", "")
	if err != nil {
		// Refresh tokens rotate; once Zitadel rejects ours, the only
		// recovery is interactive. Surface the canonical
		// "log in again" error.
		return nil, errNotLoggedIn
	}
	t.AccessToken = refreshed.AccessToken
	if refreshed.RefreshToken != "" {
		t.RefreshToken = refreshed.RefreshToken
	}
	t.ExpiresAt = refreshed.Expiry
	if t.ExpiresAt.IsZero() {
		// oauth2.Token sets Expiry from ExpiresIn when present; fall
		// back to a conservative 5min if the response was thin.
		t.ExpiresAt = time.Now().Add(5 * time.Minute)
	}
	if err := saveTokens(t); err != nil {
		return nil, fmt.Errorf("save refreshed tokens: %w", err)
	}
	return t, nil
}

// errNotLoggedIn is the sentinel used when the user has no usable
// credentials and needs to run `kshare auth login`. Callers translate this
// into a clean exit with a "run kshare auth login" hint, not a stack trace.
var errNotLoggedIn = errors.New("not logged in")

// attachBearer sets the Authorization header on an outgoing HTTP
// request. Returns errNotLoggedIn if no usable token exists.
func attachBearer(req *http.Request) error {
	t, err := ensureFreshAccessToken(req.Context())
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+t.AccessToken)
	return nil
}
