# kshare

OIDC-gated, single-uploader file share. Drop any file from the CLI;
get back a random, opaque, public URL that renders inline in the
browser.

## Layout

| Path | Contents |
|------|----------|
| `cmd/cli/` | CLI (`kshare` binary): auth, upload, replace, ls, rm |
| `cmd/server/` | Server (`kshared` binary): OIDC-gated API, /s/ file serving, expiry sweeper |
| `internal/store/` | SQLite-backed metadata + atomic file write |
| `nix/kshare.nix` | `buildGoModule` derivations + `dockerTools` OCI image |
| `nix/scripts/` | Dev lifecycle scripts (dev-up/down/rebuild/clean) |
| `docs/deployment.md` | Threat model + production wiring (Docker, Caddy, fail2ban, smoke test) |
| `docs/zitadel.md` | Operator-facing Zitadel setup procedure |
| `.claude/rules/` | Auto-loaded operating manual for Claude Code sessions |

## Setup

You need an existing Zitadel project to authenticate the CLI. The
operator-side procedure (creating the `kshare` project, the `upload`
role, the API + CLI apps) is in `docs/zitadel.md`. Once it's done:

```sh
cp .env.example .env
$EDITOR .env                 # fill in OIDC issuer, audience, client id
nix develop                  # dev shell (Go toolchain + dev-* scripts on PATH)
direnv allow                 # picks up .env via .envrc
```

## Authenticating the CLI

```sh
nix run .#kshare -- auth login     # opens browser for Zitadel device flow
nix run .#kshare -- ls        # confirm auth works (empty list is fine)
```

Tokens persist at `~/.config/kshare/auth.json` (mode 0600).
Refresh-on-expiry is automatic; re-run `login` only if Zitadel
rotates your refresh token (rare) or you `logout`.

## Local dev loop (Docker-based, end-to-end)

```sh
dev-up                       # build OCI image, start kshared, wait for /healthz
nix run .#kshare -- /tmp/file.html --ttl 1h
# → http://localhost:6980/s/aF3xK9pQ
dev-rebuild                  # rebuild image + restart after Go changes
dev-down                     # stop container, ./data persists
dev-clean                    # stop + wipe ./data
```

`dev-up` boots the same OCI image that ships to production. State
lives in `./data/` (gitignored). For local dev `kshare auth login`
defaults to `http://localhost:6980`; pass `--server URL` for a
deployed instance.

```sh
nix run .#kshare -- auth login                              # localhost:6980
nix run .#kshare -- auth login --server https://share.example.com
```

## CLI commands

```
kshare auth login [--server URL]        OIDC device-flow login (default: localhost:6980)
kshare auth logout                      Forget cached tokens
kshare auth status                      Show server URL + token freshness
kshare <file> [--ttl 24h]               Upload (default verb)
kshare replace <slug> <file> [--ttl X]  Replace existing slug's content
kshare ls [--json]                      List your uploads
kshare rm <slug>                        Delete an upload
```

`--ttl` accepts everything Go's `time.ParseDuration` accepts (`1h`,
`30m`) plus `d`/`w` (`7d`, `52w`). If omitted the server applies its
`DEFAULT_TTL` (7d). Allowed range: `KSHARE_MIN_TTL` (10m) to
`KSHARE_MAX_TTL` (365d), both env-tunable on the server.

`replace` keeps the **slug** stable, so the public URL is unchanged
across replacements. The on-disk extension follows the new filename;
the old on-disk file is removed.

## Production deployment

Run the OCI image (`nix build .#kshared-image`) behind a reverse
proxy that handles TLS, forwards `X-Forwarded-For`, and blocks
`/healthz` from public exposure. The full recipe (Docker run flags,
env file, Caddy snippet, fail2ban jail, smoke test) is in
`docs/deployment.md` — do not improvise the Caddy block, the
directive order matters.

## Testing

```sh
go test ./... -count=1       # store + handler tests
go vet ./...
nix flake check              # verifies all packages evaluate
```

## More

- `docs/deployment.md` — production wiring, threat model, smoke test
- `docs/zitadel.md` — Zitadel project setup walkthrough
- `.claude/rules/` — agent operating manual; auto-loaded each session

## License

[AGPL-3.0-or-later](LICENSE). If you run a modified kshared exposed
over a network, the AGPL's §13 obligates you to make the modified
source available to your users.
