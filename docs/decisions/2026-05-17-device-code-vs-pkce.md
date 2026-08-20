# Device Code vs PKCE for kshare-cli

Decided 2026-05-17. Status: **active, revisit on trigger below**.

## Context

Zitadel's app-creation wizard flags **PKCE** as `recommended` and **Device Code** without the badge. That
ordering reflects [RFC 8252 §8.4 + OAuth 2.1 guidance][rfc8252]: PKCE is the recommended flow for native apps
with browser access; Device Code (RFC 8628) is meant for **input-constrained devices** (TVs, IoT, headless
servers), not as a general native-CLI default.

We picked Device Code for `kshare-cli` anyway.

[rfc8252]: https://www.rfc-editor.org/rfc/rfc8252#section-8.4

## Constraint that drove the choice

The CLI must work from a **browserless SSH session**: the operator SSH'd into a remote box, no
port-forwarding configured, no display. That's the canonical CLI scenario `gh`, `gcloud`, `aws sso` all
target with their PKCE+loopback flows, **but those flows require a browser on the same host** (they bind
`127.0.0.1:0` and expect the OS to open a URL in a local browser). On a headless SSH session there is no such
browser, so the loopback callback never fires.

Device Code sidesteps the constraint: the CLI prints a code + URL, the operator opens that URL from any
device (their laptop, their phone), enters the code, and the CLI polls the token endpoint until consent
lands. No callback, no port binding, no display required where the CLI runs.

## Cost we accepted

- Device Code is **not** Zitadel's recommended flow; the wizard's preset omits `refresh_token` from the app's
  `grant_types` list, which silently breaks refresh-on-expiry. Operational fix is in `docs/zitadel.md` step
  8a; runtime diagnostic is produced by `cmd/cli/auth.go::ensureFreshAccessToken` and surfaced to stderr by
  `cmd/cli/main.go::printRefreshError`.
- Slightly worse UX than the PKCE-on-laptop happy path: operator copies a short dash-separated code (Zitadel
  default is 8 chars like `RPJD-CZFC`) instead of just clicking through a browser redirect.
- RFC 8628 §5.4 phishing concern: an attacker who controls a separate device-code session can
  social-engineer a victim into authorising it. Mitigated by single-user scope + slug-as-secret read path;
  not load-bearing for kshare.

## Alternative considered: PKCE + loopback

Pros:
- Zitadel-recommended, OAuth 2.1-recommended.
- Wizard preset includes `authorization_code` and surfaces the Refresh Token toggle as an enabled checkbox,
  with no hidden config trap.
- Smoother UX when the CLI runs on a host with a local browser.

Cons:
- Breaks the headless SSH use case (the explicit constraint above).
- ~80 LOC of CLI work: bind `net.Listen("tcp", "127.0.0.1:0")`, open browser to authorise URL, receive code
  on `/callback`, exchange via `rp.CodeExchange`. Not hard, just net-new.
- Zitadel accepts loopback redirects port-agnostically per RFC 8252 §7.3. The matcher (`equalURI` in
  `github.com/zitadel/oidc` `pkg/op/auth_request.go`) compares path + query, not port, so you register
  `http://127.0.0.1/callback` once and the CLI binds any ephemeral port at runtime. So the redirect-URI
  registration cost is one-time.

## Decision

**Stay on Device Code.** The headless-SSH use case is real and not covered by PKCE. The cost of staying is
bounded: the refresh-token-grant-type misconfig is documented operationally in `docs/zitadel.md` 8a and
surfaced at runtime by `cmd/cli/main.go::printRefreshError`. The two together close the silent-failure
surface that motivated this decision.

## Triggers to reopen

Revisit if **any** of these become true:

1. We add a GUI uploader or web flow that already assumes a local browser; at that point PKCE costs nothing
   extra and we may as well unify.
2. Device-code phishing becomes a real concern (would require multi-user kshare, currently out of scope).
3. Zitadel deprecates or removes Device Code support. Observable signal:
   `console/src/app/pages/projects/apps/authmethods.ts` in `github.com/zitadel/zitadel` no longer exports
   `DEVICE_CODE_METHOD`, OR Zitadel's CHANGELOG mentions `device_code` grant-type deprecation. Both
   greppable.
4. We add a `--auth-method` flag for users on laptops who want the smoother PKCE UX. Device Code then
   becomes the fallback for headless and PKCE becomes the default for interactive.

(4) is the most likely path. Don't pre-build it; build it the first time someone asks.

## Related

- `docs/zitadel.md` step 8a: the refresh_token grant-type trap.
- `cmd/cli/auth.go::runLogin`: current device-flow login impl.
- Upstream presets: `github.com/zitadel/zitadel`
  `console/src/app/pages/projects/apps/authmethods.ts` defines `DEVICE_CODE_METHOD` and the wizard mapping
  that omits `refresh_token` from the default `grantTypesList`.
- Loopback-redirect matching for the PKCE-alternative cost estimate: `github.com/zitadel/oidc`
  `pkg/op/auth_request.go` (`equalURI`, `validateAuthReqRedirectURINative`).
