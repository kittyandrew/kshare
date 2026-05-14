# Dev Stack
<!-- .claude/rules/003-dev-stack.md -- local build, run, test cycle -->

Single Go module, single Nix flake. No external Docker dependencies
for the binary -- SQLite is in-process via `modernc.org/sqlite`
(pure Go, no CGO).

## Setup

```sh
nix develop          # dev shell: go + sqlite + docker + curl + dev-* scripts
cp .env.example .env # fill in Zitadel issuer + audience + client_id
direnv allow         # picks up .env via .envrc
```

The server hard-fails at boot on missing or unreachable OIDC issuer
by design, so `.env` is a prerequisite even for local dev. Point
`KSHARE_OIDC_ISSUER` at the prod Zitadel (it's a public IdP).
The Zitadel `kshare` project must already exist; see `docs/zitadel.md`.

## Dev loop (Docker-based, end-to-end)

The dev shell exposes `dev-up`, `dev-down`, `dev-rebuild`, `dev-clean`
on PATH. They wrap `nix build .#kshared-image` + `docker run` with
sane defaults; state lives at `./data/` (gitignored).

```sh
dev-up               # build OCI image, start kshared on :6980, wait for /healthz
# in another shell, with direnv loading .env:
nix run .#kshare -- auth login                         # defaults to http://localhost:6980
nix run .#kshare -- /tmp/file.html --ttl 1h
nix run .#kshare -- ls

dev-rebuild          # after Go changes: rebuild image + restart
dev-down             # stop container, preserve ./data
dev-clean            # stop + wipe ./data
```

`dev-up` overrides `KSHARE_LOG_FORMAT=console` for human-readable
logs. Public URLs are constructed CLI-side from the server URL
persisted in `auth.json` at login time (`--server` flag, no env
var); no base-URL config on the server.

## Run without Docker (tighter inner loop)

```sh
# Terminal 1 -- server
go run ./cmd/server

# Terminal 2 -- CLI (after first `kshare auth login`)
go run ./cmd/cli auth login              # device flow -- browser opens
go run ./cmd/cli ~/some.html --ttl 1h    # upload, prints URL
go run ./cmd/cli ls
go run ./cmd/cli replace <slug> <file>
go run ./cmd/cli rm <slug>
```

The bare `go run` server uses `${KSHARE_DATA}` (defaults to `./data/`,
gitignored) for the DB + files. Wipe to reset state.

## Build

```sh
nix build .#kshare          # CLI binary -> result/bin/kshare
nix build .#kshared         # server binary -> result/bin/kshared
nix build .#kshared-image   # OCI image tarball -> result
nix flake check            # all derivations evaluate
```

## Test

```sh
go test ./... -count=1     # store + handler tests, all pass
go vet ./...
```

Handler tests live alongside their handlers (`cmd/server/*_test.go`).
They construct a `server` with a real `store.Container` in
`t.TempDir()` and bypass the JWKS-fetching middleware via `withClaims`
in the request context.

## Updating Go deps

```sh
go get <module>@<version>
go mod tidy
# Then bump vendorHash in nix/kshare.nix:
#   1. Set vendorHash = pkgs.lib.fakeHash
#   2. Run `nix build .#kshared`
#   3. Copy the "got" sha256 from the error message (printed near the
#      top of stderr; don't pipe through `| tail` or you'll miss it)
#   4. Paste it back into vendorHash
```
