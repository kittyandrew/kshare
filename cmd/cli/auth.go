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
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
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

// @NOTE: not atomic against concurrent writers (no flock, no temp +
// rename). kshare's scope is single-uploader per .claude/rules/001-architecture.md;
// two simultaneous refreshes from the same user are a deliberate
// foot-gun we accept (parallel `kshare upload &` invocations can race
// and trip Zitadel's refresh-token reuse-detection, see the third
// failure mode noted in ensureFreshAccessToken). Revisit if/when the
// scope changes.
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

	// @NOTE: zitadel-specific. Zitadel's device_authorization_endpoint
	// returns verification URLs pointing at the legacy v1 login UI
	// (/device, /ui/login/), which has known WebAuthn projection bugs
	// for org-scoped users. We bypass by constructing the v2 path
	// from the issuer base. A future port to a different OIDC
	// provider (Authelia, Keycloak, Dex) needs this gone -- see
	// docs/decisions/2026-05-17-device-code-vs-pkce.md for the
	// broader portability discussion.
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
// CLI receives a JWE that the server can't parse as a JWT.
//
// The refresh runs BEFORE the token line is printed so the displayed
// expiry reflects the post-refresh state -- otherwise users see the
// confusing "token: expired ... api: ok" pair that hints at a bug
// where there isn't one. On refresh failure we show the on-disk
// expiry and route the skipped-reason on the error type, so the
// "see stderr" hint only appears when there's actually something on
// stderr.
func runStatus() {
	t, err := loadTokens()
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, "kshare: not logged in; run `kshare auth login`")
			os.Exit(1)
		}
		fail("kshare auth status: %v", err)
	}

	fmt.Printf("server:    %s\n", t.Server)
	fmt.Printf("issuer:    %s\n", t.Issuer)
	fmt.Printf("audience:  %s\n", t.Audience)
	fmt.Printf("client_id: %s\n", t.ClientID)

	// Parent ctx watches SIGINT so Ctrl-C cancels a hung JWKS or
	// probe roundtrip cleanly; without this the user waits out the
	// full 8s/5s budgets. Refresh and probe each get their own
	// derived timeout so a slow refresh can't starve the probe.
	// 8s for refresh covers cold-JWKS + IdP roundtrip on a typical
	// link; 5s for the probe is enough for a single GET against
	// the configured server.
	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	refreshCtx, refreshCancel := context.WithTimeout(rootCtx, 8*time.Second)
	defer refreshCancel()
	fresh, err := ensureFreshAccessToken(refreshCtx, t)
	if err != nil {
		// Stdout block first so the structured status output stays
		// coherent; stderr diagnostic last so terminal-buffering
		// quirks don't interleave it into the middle of the
		// stdout block.
		printTokenLine(t)
		switch {
		case errors.Is(err, errNotLoggedIn):
			fmt.Println("api:       skipped (no usable tokens; run `kshare auth login`)")
		case errors.Is(err, errRefreshFailed):
			fmt.Println("api:       skipped (auth rejected -- see stderr)")
			printRefreshError(err)
		default:
			fmt.Printf("api:       skipped (%v)\n", err)
		}
		return
	}
	printTokenLine(fresh)
	probeCtx, probeCancel := context.WithTimeout(rootCtx, 5*time.Second)
	defer probeCancel()
	fmt.Printf("api:       %s\n", probeAPI(probeCtx, fresh))
}

// printTokenLine prints the one-line summary of the access token's
// expiry state. Pulled out of runStatus so both the refresh-succeeded
// (`fresh`) and refresh-failed (on-disk `t`) branches can use it.
func printTokenLine(t *tokenFile) {
	left := time.Until(t.ExpiresAt).Round(time.Second)
	freshness := "valid"
	if t.expired() {
		freshness = "expired"
	}
	fmt.Printf("token:     %s (%s, %s)\n",
		freshness, t.ExpiresAt.Local().Format(time.RFC3339), humanLeft(left))
}

// probeAPI sends a GET /api/files against the configured server.
// Returns a one-line human-readable status. Five outcomes:
//
//   - 2xx: "ok"
//   - Network error: "unreachable: <err>"
//   - 401: server rejected an otherwise-fresh token. Common causes
//     (in rough order of likelihood the first time you see this):
//     audience mismatch between server/IdP config; Zitadel
//     `kshare-cli` Auth Token Type reverted from JWT to Bearer;
//     server-side clock skew beyond JWT nbf/exp tolerance. The
//     server's response body carries the reason; we surface it.
//   - 403: "forbidden: missing role <role>" -- server's own text.
//   - Other non-2xx: status line + body excerpt.
//
// Context flows in from the caller so Ctrl-C cancels cleanly. Body
// is capped at 256B; the server's libserv-shaped responses are
// short by design.
func probeAPI(ctx context.Context, t *tokenFile) string {
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
	if resp.StatusCode == http.StatusOK {
		return "ok"
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = resp.Status
	}
	return fmt.Sprintf("FAIL (%d) -- %s", resp.StatusCode, msg)
}

// humanLeft formats a duration for status output. The caller's
// "freshness" label ("valid" / "expired") already carries the sign
// information; humanLeft just renders magnitude + direction.
func humanLeft(d time.Duration) string {
	if d < 0 {
		return (-d).String() + " ago"
	}
	return "in " + d.String()
}

// ensureFreshAccessToken refreshes a pre-loaded token if expired,
// persists the rotated set, and returns the (possibly-refreshed)
// token. The caller is responsible for loading -- splitting the load
// out lets runStatus reuse its already-loaded tokenFile (avoiding a
// race window where auth.json changes between two reads) and keeps
// this function purely about the refresh exchange.
//
// Return contract:
//   - (t, nil) on success (either no refresh needed, or refresh succeeded).
//   - (nil, errNotLoggedIn) if the loaded tokens have no refresh_token.
//   - (nil, joined-with-errRefreshFailed) for any failure during the
//     refresh exchange itself: OIDC discovery, the refresh-grant POST,
//     or persisting the rotated tokens. The upstream error is joined
//     via errors.Join so callers can errors.As(*oidc.Error) when they
//     need to surface ErrorType / Description programmatically.
//
// Callers are expected to route errRefreshFailed through
// printRefreshError (in main.go) so the upstream detail lands on
// stderr exactly once, regardless of which CLI subcommand triggered
// the refresh.
func ensureFreshAccessToken(ctx context.Context, t *tokenFile) (*tokenFile, error) {
	if !t.expired() {
		return t, nil
	}
	if t.RefreshToken == "" {
		return nil, errNotLoggedIn
	}
	relying, err := rp.NewRelyingPartyOIDC(ctx, t.Issuer, t.ClientID, "", "", loginScopes(t.Audience))
	if err != nil {
		// IdP-side problem (JWKS unreachable, discovery doc broken);
		// re-login won't fix it. Route through errRefreshFailed so the
		// caller's "fix the upstream cause" hint applies.
		return nil, errors.Join(errRefreshFailed, fmt.Errorf("oidc init: %w", err))
	}
	refreshed, err := rp.RefreshTokens[*oidc.IDTokenClaims](ctx, relying, t.RefreshToken, "", "")
	if err != nil {
		// Three common upstream causes, each needing a different fix:
		//   - "unauthorized_client: grant_type \"refresh_token\" not
		//     allowed" -> `kshare-cli` app missing Refresh Token in
		//     Grant Types (docs/zitadel.md step 8a)
		//   - "invalid_grant: token expired" -> Refresh Token Lifetime
		//     lapsed; re-login required
		//   - "invalid_grant: token (id ...) was already used" ->
		//     reuse detection invalidated the family (concurrent CLI)
		// Surfacing is the caller's job: failRequest in main.go calls
		// printRefreshError which extracts the OIDC error via
		// errors.As and prints to stderr exactly once per upload.
		return nil, errors.Join(errRefreshFailed, err)
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
		return nil, errors.Join(errRefreshFailed, fmt.Errorf("save refreshed tokens: %w", err))
	}
	return t, nil
}

// errNotLoggedIn marks the absence of usable persisted credentials --
// no auth.json on disk, or an auth.json with no refresh_token. The fix
// is `kshare auth login`. Distinct from errRefreshFailed: there's no
// upstream error to surface, just "log in."
var errNotLoggedIn = errors.New("not logged in")

// errRefreshFailed marks an auth-layer rejection that re-login alone
// won't fix: the IdP refused the refresh grant (grant-type misconfig,
// expired refresh-token-lifetime, reuse-detection family
// invalidation), OR the server rejected an otherwise-fresh token
// (audience mismatch, JWT vs Bearer setting reverted, clock skew).
// In both cases, telling the user to re-log in mints another
// identically-broken token; the fix is upstream of the CLI.
//
// Invariant: this sentinel is always joined (via errors.Join) with the
// underlying error so callers can errors.As(*oidc.Error) the inner
// cause. printRefreshError in main.go is the canonical surfacing
// helper; runStatus and failRequest both call it.
var errRefreshFailed = errors.New("auth rejected")

