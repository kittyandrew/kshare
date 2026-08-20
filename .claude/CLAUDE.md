# kshare

OIDC-gated, single-uploader file share. Drop any file from the CLI; get back a random, opaque, public URL
that renders inline in the browser.

Monorepo holding both the Go server (`cmd/server/`, `kshared` binary) and the Go CLI (`cmd/cli/`, `kshare`
binary) under one `go.mod`. This repo ships the OCI image only; the NixOS module + agenix wiring live
out-of-tree in the operator's own NixOS config.

## Orientation

- **The code is the spec.** Every behaviour is documented at the call site (handler comments, store package
  comments, schema SQL). Cross-cutting decisions are indexed across the next two bullets.
- `.claude/rules/` is the operating manual; auto-loaded every session. `001-architecture.md` is the topology
  + scope; `003-dev-stack.md` is the dev runbook; `005-auth.md` is the OIDC contract + CLI error-routing.
  Flow diagrams (upload / read / replace) live in `docs/architecture.md`; consult them when tracing how a
  request becomes slug-on-disk.
- `docs/deployment.md` is operator-facing: threat model, Docker run, Caddy snippet, fail2ban jail, smoke
  test, decision history. Read it before changing anything security- or ops-shaped.
- `docs/zitadel.md` is operator-facing too: Zitadel project / role / app setup walkthrough, plus
  Zitadel-source citations (function names in `github.com/zitadel/zitadel` and `github.com/zitadel/oidc`) you
  may need when chasing auth bugs. Read it before touching anything OIDC-shaped.
- `docs/decisions/` holds ADRs for choices that must outlive a session. Filename convention:
  `YYYY-MM-DD-short-name.md`. Sections follow the pattern in `2026-05-17-device-code-vs-pkce.md`: Context /
  Constraint / Cost / Alternative / Decision / Triggers to reopen / Related.
- Go module rooted at the repo root. `go test ./... -count=1` from the top; `go vet ./...` and `gofmt -l .`
  clean.
- Dev loop: `nix run .#dev-up` (builds OCI image, runs container, waits for /healthz). See
  `003-dev-stack.md`.
