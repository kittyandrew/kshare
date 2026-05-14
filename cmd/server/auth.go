// OIDC bearer-token authentication for the API surface. Single
// Zitadel issuer + audience pair, configured at boot via env; tokens
// are verified offline against the JWKS published at
// `${issuer}/oauth/v2/keys`. No introspection, no per-request RTT to
// Zitadel. The audience is pinned to the Zitadel project ID; only
// the `upload` project role is required.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/zitadel/oidc/v3/pkg/client"
	"github.com/zitadel/oidc/v3/pkg/client/rp"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// authenticator wraps the access-token verifier for one Zitadel
// issuer + audience pair. Constructed once via newAuthenticator;
// consulted on every authenticated request.
type authenticator struct {
	issuer   string
	audience string // = Zitadel project ID
	verifier *op.AccessTokenVerifier
}

// newAuthenticator dials OIDC discovery, builds a remote JWKS keyset,
// and returns a verifier ready to validate access tokens. Hard-fails
// if the issuer is unreachable: the alternative (lazy retry on first
// request) hides config errors at boot.
func newAuthenticator(ctx context.Context, issuer, audience string) (*authenticator, error) {
	if issuer == "" {
		return nil, errors.New("KSHARE_OIDC_ISSUER is required")
	}
	if audience == "" {
		return nil, errors.New("KSHARE_OIDC_AUDIENCE is required")
	}
	cfg, err := client.Discover(ctx, issuer, http.DefaultClient)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery at %s: %w", issuer, err)
	}
	if cfg.JwksURI == "" {
		return nil, fmt.Errorf("oidc discovery at %s returned empty jwks_uri", issuer)
	}
	keys := rp.NewRemoteKeySet(http.DefaultClient, cfg.JwksURI)
	return &authenticator{
		issuer:   issuer,
		audience: audience,
		verifier: op.NewAccessTokenVerifier(issuer, keys),
	}, nil
}

// claims is what kshare needs from a verified access token. Role
// names come from the project-id-scoped Zitadel claim; the unscoped
// variant is also present in the token but the scoped form is
// unambiguous.
type claims struct {
	Subject   string
	Roles     map[string]struct{}
	ExpiresAt time.Time
}

func (c *claims) HasRole(name string) bool {
	_, ok := c.Roles[name]
	return ok
}

type claimsCtxKey struct{}

func claimsFromContext(ctx context.Context) (*claims, bool) {
	c, ok := ctx.Value(claimsCtxKey{}).(*claims)
	return c, ok
}

// verify is the per-request entry point: parse the bearer header,
// validate signature / issuer / expiry, then audience-pin and parse
// roles. Returns claims or a 401-shaped error.
func (a *authenticator) verify(ctx context.Context, header string) (*claims, error) {
	raw := bearerFrom(header)
	if raw == "" {
		return nil, errors.New("missing bearer token")
	}
	tok, err := op.VerifyAccessToken[*oidc.AccessTokenClaims](ctx, raw, a.verifier)
	if err != nil {
		return nil, fmt.Errorf("verify token: %w", err)
	}
	if !slices.Contains(tok.Audience, a.audience) {
		return nil, fmt.Errorf("token audience does not contain %s", a.audience)
	}
	return &claims{
		Subject:   tok.Subject,
		Roles:     parseRoles(tok.Claims, a.audience),
		ExpiresAt: tok.GetExpiration(),
	}, nil
}

// middleware wraps an http.Handler with bearer-token verification.
// 401 on missing/invalid token; on success attaches claims to the
// request context.
func (a *authenticator) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := a.verify(r.Context(), r.Header.Get("Authorization"))
		if err != nil {
			// `method` + `path` already on the context logger via
			// withRequestLogger; don't duplicate here.
			zerolog.Ctx(r.Context()).Warn().Err(err).
				Msg("auth rejected")
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), claimsCtxKey{}, c)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Role constant. Name matches the Zitadel project-role key. Single
// source of truth so a typo on a route doesn't silently authorise
// something it shouldn't.
const roleUpload = "upload"

// requireRole returns a handler that 403s if the authenticated
// principal lacks the named role. Stack on top of
// authenticator.middleware.
func requireRole(role string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, ok := claimsFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !c.HasRole(role) {
			http.Error(w, "forbidden: missing role "+role, http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

func bearerFrom(header string) string {
	if header == "" {
		return ""
	}
	const prefix = "bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

// parseRoles extracts role names from Zitadel's per-project roles
// claim (`urn:zitadel:iam:org:project:<projectID>:roles`). The value
// is a map of role-key -> { orgID: orgDomain, ... }; we discard
// everything but the key set. Returns nil (not an empty map) when the
// claim is absent so callers don't pay for an allocation on the
// reject path.
func parseRoles(extra map[string]any, projectID string) map[string]struct{} {
	if extra == nil {
		return nil
	}
	key := "urn:zitadel:iam:org:project:" + projectID + ":roles"
	raw, ok := extra[key]
	if !ok {
		return nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]struct{}, len(m))
	for k := range m {
		out[k] = struct{}{}
	}
	return out
}
