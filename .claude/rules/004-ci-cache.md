# CI Binary Cache
<!-- .claude/rules/004-ci-cache.md: attic cache, ryanccn/attic-action, ATTIC_GENERAL_PUSH_TOKEN -->

CI pushes build outputs to a self-hosted [Attic](https://github.com/zhaofengli/attic) binary cache shared
across all kittyandrew flake repos. Wiring lives in `.github/workflows/build.yml`, a separate workflow from
`ci.yml` so the push token is isolated to main-branch context.

## Workflow split

- **`ci.yml`** runs on every PR + push to main. Go-side quality only: `gofmt -l`, `go vet`,
  `go test -race`, `go build`. No Nix steps; no secret access; fork PRs run safely.
- **`build.yml`** runs **on push to main only**. Runs `nix flake check`, builds `.#kshared-image`, and pushes
  every produced derivation via attic-action's post-build hook. The workflow trigger is the only gate, with
  no `if:` guard needed, because fork PRs and tag pushes don't satisfy `on: push: branches: [main]`.

## Wiring

Three pieces, all in `build.yml`:
- **`ryanccn/attic-action@v0`** step, placed BEFORE the `nix flake check` + `nix build` so the post-hook is
  registered before any derivations are produced.
- **`vars.ATTIC_ENDPOINT`**: the public URL, `https://cache.kittyandrew.dev/`. Set per repo via
  `gh variable set ATTIC_ENDPOINT --repo kittyandrew/<repo> --body "..."`.
- **`secrets.ATTIC_GENERAL_PUSH_TOKEN`**: shared scoped JWT (`--pull general --push general`), reused across
  every kittyandrew flake repo. Provenance and how to install: `.claude/rules/selfhosted/202-attic.md` in the
  kittyos repo.

The action's post-hook auto-captures every new store path the job built and pushes it. Paths already in
`cache.nixos.org` are skipped via Attic's upstream-cache filter.

## Don't

- Don't swap in the bare `attic` CLI (`nix run github:zhaofengli/attic -- ...`). The action wraps the same
  calls with a clean post-hook; ad-hoc CLI in CI is extra YAML without benefit at this scale.
- Don't mint a per-repo push token. The shared `--sub ci` token is intentional: attribution per repo isn't
  load-bearing at one operator's scale, and revocation is all-or-nothing anyway (rotate the RS256 signing
  secret to kill every token).
- Don't widen `build.yml`'s trigger to PRs or tags without thinking through the blast radius. Fork PRs can't
  read secrets even if the workflow runs, but on-push with branch-filter is the simplest safe shape.
- Don't move the Attic step back into `ci.yml`. The split exists so PR runs (incl. forks) never sit in the
  same workflow as the push token.

## If the cache is unreachable

CI degrades gracefully: builds still run, just without cache hits. No need to gate the workflow on cache
availability.
