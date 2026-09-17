# kshare

OIDC-gated, single-uploader file share. Drop any file from the CLI; get back a random, opaque, public URL
that renders inline in the browser.

## Setup

The CLI authenticates against any OIDC provider that can stamp an audience into the access token and
assert a `roles` string array containing `upload`. `docs/zitadel.md` is a worked example for Zitadel;
`KSHARE_OIDC_SCOPES` covers providers that need extra scopes to emit those claims. Once that exists:

```sh
cp .env.example .env
$EDITOR .env                 # fill in OIDC issuer, audience, client id
nix develop                  # dev shell (Go toolchain + dev-* scripts on PATH)
direnv allow                 # picks up .env via .envrc
```

## Authenticating the CLI

```sh
nix run .#kshare -- auth login     # opens browser for the device flow
nix run .#kshare -- ls        # confirm auth works (empty list is fine)
```

Tokens persist at `~/.config/kshare/auth.json` (mode 0600). Refresh-on-expiry is automatic; re-run `login`
only if Zitadel rotates your refresh token (rare) or you `logout`. If a refresh ever fails silently,
`kshare auth status` surfaces the upstream OIDC error. The canonical first-time silent failure is a missing
`refresh_token` entry in the Zitadel app's grant types (see `docs/zitadel.md` 8a).

## Local dev loop (Docker-based, end-to-end)

```sh
dev-up                       # build OCI image, start kshared, wait for /healthz
nix run .#kshare -- /tmp/file.html --ttl 1h
# → http://localhost:6980/s/aF3xK9pQ
dev-rebuild                  # rebuild image + restart after Go changes
dev-down                     # stop container, ./data persists
dev-clean                    # stop + wipe ./data
```

`dev-up` boots the same OCI image that ships to production. State lives in `./data/` (gitignored). For local
dev `kshare auth login` defaults to `http://localhost:6980`; point it at a deployed instance with
`--server URL`, or set `KSHARE_SERVER` once so the flag is never needed.

```sh
nix run .#kshare -- auth login                              # localhost:6980
nix run .#kshare -- auth login --server https://share.example.com
```

## CLI commands

```
kshare auth login [--server URL]        OIDC device-flow login ($KSHARE_SERVER, else localhost:6980)
kshare auth logout                      Forget cached tokens
kshare auth status                      Show server URL + token freshness
kshare <file> [--ttl 24h]               Upload (default verb)
kshare replace <slug> <file> [--ttl X]  Replace existing slug's content
kshare ls [--json]                      List your uploads
kshare rm <slug>                        Delete an upload
```

`--ttl` accepts everything Go's `time.ParseDuration` accepts (`1h`, `30m`) plus `d`/`w` (`7d`, `52w`). If
omitted the server applies its `DEFAULT_TTL` (7d). Allowed range: `KSHARE_MIN_TTL` (10m) to `KSHARE_MAX_TTL`
(365d), both env-tunable on the server.

`replace` keeps the **slug** stable, so the public URL is unchanged across replacements. The on-disk
extension follows the new filename; the old on-disk file is removed.

## Production deployment

Run the OCI image (`nix build .#kshared-image`) behind a reverse proxy that handles TLS, forwards
`X-Forwarded-For`, and blocks `/healthz` from public exposure. The full recipe (Docker run flags, env file,
Caddy snippet, fail2ban jail, smoke test) is in `docs/deployment.md`. Do not improvise the Caddy block, the
directive order matters.

## Testing

```sh
go test ./... -count=1       # store + handler tests
go vet ./...
gofmt -l .                   # must print nothing
nix flake check              # verifies all packages evaluate
```

## Binary cache

Builds are published to `cache.kittyandrew.dev`, so Nix can download these outputs instead of rebuilding them.

On NixOS:

```nix
nix.settings = {
  extra-substituters = ["https://cache.kittyandrew.dev/nix-cache"];
  extra-trusted-public-keys = ["cache.kittyandrew.dev-1:yy5fdErj1riKOjND10kzD5mp0L8/C8RFG3VkMizhGg4="];
};
```

Elsewhere, in `~/.config/nix/nix.conf` (or `/etc/nix/nix.conf` for all users):

```
extra-substituters = https://cache.kittyandrew.dev/nix-cache
extra-trusted-public-keys = cache.kittyandrew.dev-1:yy5fdErj1riKOjND10kzD5mp0L8/C8RFG3VkMizhGg4=
```

The `extra-` prefixes append rather than replace, so `cache.nixos.org` keeps working. The cache is read-only
and needs no credentials; it serves only what this repository's flake builds.

## More

- `docs/deployment.md`: production wiring, threat model, smoke test
- `docs/zitadel.md`: Zitadel project setup walkthrough
- `docs/decisions/`: ADRs for choices that need to outlive a session (e.g.
  `2026-05-17-device-code-vs-pkce.md`)
- `.claude/rules/`: agent operating manual, auto-loaded each session

## License

[AGPL-3.0-or-later](LICENSE). If you run a modified kshared exposed over a network, the AGPL's §13 obligates
you to make the modified source available to your users.
