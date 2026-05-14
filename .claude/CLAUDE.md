# kshare

OIDC-gated, single-uploader file share. Drop any file from the CLI;
get back a random, opaque, public URL that renders inline in the
browser.

Monorepo holding both the Go server (`cmd/server/`, `kshared`
binary) and the Go CLI (`cmd/cli/`, `kshare` binary) under one
`go.mod`. This repo ships the OCI image only — the NixOS module +
agenix wiring live out-of-tree in the operator's own NixOS config.

## Orientation

- **The code is the spec.** Every behaviour is documented at the
  call site (handler comments, store package comments, schema SQL).
  Cross-cutting decisions are in `.claude/rules/001-architecture.md`.
- `.claude/rules/` is the operating manual; auto-loaded every
  session. `001-architecture.md` is the topology + flows;
  `003-dev-stack.md` is the dev runbook.
- `docs/deployment.md` is operator-facing: threat model, Docker
  run, Caddy snippet, fail2ban jail, smoke test, decision history.
  Read it before changing anything security- or ops-shaped.
- Go module rooted at the repo root. `go test ./... -count=1` from
  the top; `go vet ./...` clean.
- Dev loop: `nix run .#dev-up` (builds OCI image, runs container,
  waits for /healthz). See `003-dev-stack.md`.
- Zitadel project setup (manual, operator-facing): `docs/zitadel.md`.
