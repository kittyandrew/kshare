# Auth
<!-- .claude/rules/005-auth.md: OIDC provider config, dual-grant requirement, refresh-on-expiry contract -->

Zitadel OIDC. One project (`kshare`), one role (`upload`). Two apps: API `kshare-server` (audience pin, JWT
private key auth) and Native `kshare-cli` (device flow, JWT, "Add user roles to access token" toggle ON,
`grant_types` includes BOTH `device_code` AND `refresh_token`).

The server validates JWT signature, audience, and the project-role claim per request. Reads (`/s/{slug}`) are
NOT authenticated; the slug is the gate.

## Dual-grant requirement

Non-obvious: Zitadel's wizard preset omits `refresh_token` for Device Code apps, which silently breaks
refresh-on-expiry. The CLI gets a refresh token on login (because `offline_access` is in scopes), but the
next refresh attempt is rejected by Zitadel's grant-type allowlist with
`unauthorized_client: grant_type "refresh_token" not allowed`.

Full operational fix is in `docs/zitadel.md` step 8a. Design rationale for staying on Device Code despite
Zitadel's PKCE recommendation is in `docs/decisions/2026-05-17-device-code-vs-pkce.md`.

## CLI error contract

Two auth-related sentinels in `cmd/cli/auth.go`:

- **`errNotLoggedIn`**: no usable persisted credentials (auth.json missing or no refresh_token). Fix:
  `kshare auth login`.
- **`errRefreshFailed`**: auth-layer rejection that re-login alone won't fix (IdP refused refresh grant, OR
  server rejected an otherwise-fresh token). Always joined via `errors.Join` with the underlying error so
  callers can `errors.As(*oidc.Error)`.

Surfacing: `cmd/cli/main.go::printRefreshError` is the single helper; called from both `failRequest`
(subcommand exit path) and `runStatus` (interactive diagnostic).

## Token persistence

`~/.config/kshare/auth.json` (mode 0600). On disk: issuer, client id, audience, server URL, access + refresh
tokens, expiry. Single file, no flock: single-user kshare, accepted foot-gun. Concurrent `kshare upload &`
invocations can race on refresh and trip Zitadel's reuse-detection family invalidation; not defended in code.
