// OIDC bearer auth for the API. Tokens are verified offline against the issuer's JWKS, so no request costs
// an introspection round-trip to Zitadel.
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

type authenticator struct {
	issuer   string
	audience string // = Zitadel project ID
	verifier *op.AccessTokenVerifier
}

// newAuthenticator dials OIDC discovery and builds a remote JWKS keyset. Fails rather than retrying lazily
// on the first request, which would hide a config error until someone tried to upload.
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

// claims is what kshare needs from a verified access token. Role names come from the project-id-scoped
// Zitadel claim: the unscoped variant is in the token too, but the scoped form is unambiguous.
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

// require gates a handler on a verified bearer token carrying `role`.
//
// Verification and the role check are one wrapper on purpose. Split in two, a route could be registered with
// authentication and no authorization, and the authorization half would need a "no claims on the context"
// branch that nothing but a test can reach.
func (a *authenticator) require(role string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := a.verify(r.Context(), r.Header.Get("Authorization"))
		if err != nil {
			zerolog.Ctx(r.Context()).Warn().Err(err).Msg("auth rejected")
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !c.HasRole(role) {
			zerolog.Ctx(r.Context()).Warn().Str("subject", c.Subject).Str("role", role).
				Msg("auth rejected: missing role")
			http.Error(w, "forbidden: missing role "+role, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), claimsCtxKey{}, c)))
	})
}

// roleUpload must match the Zitadel project-role key exactly.
const roleUpload = "upload"

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

// parseRoles reads Zitadel's per-project roles claim (`urn:zitadel:iam:org:project:<projectID>:roles`),
// whose value is role-key -> { orgID: orgDomain, ... }. Only the key set matters here.
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
