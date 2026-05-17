# Architecture
<!-- .claude/rules/001-architecture.md -- component topology, scope,
     pointers to flow diagrams + auth -->

kshare is a single-uploader, public-reader file share. Two
components plus a reverse proxy in front:

- **kshare CLI** (`cmd/cli/`, `kshare` binary) -- runs on the
  host. Subcommands: `auth login [--server URL] | logout | status`,
  default upload (`kshare <file>`), `replace <slug> <file>`,
  `ls [--json]`, `rm <slug>`. Talks to the server over HTTPS with
  an OIDC bearer; persists tokens at `~/.config/kshare/auth.json`
  (mode 0600).
- **kshared server** (`cmd/server/`, `kshared` binary,
  docker-only). Owns ALL routes:
  - `POST /api/upload`, `PUT /api/files/{slug}` (replace),
    `GET /api/files`, `DELETE /api/files/{slug}` -- bearer-gated by
    the `upload` role.
  - `GET /s/{slug}` -- public, unauthenticated. The 48-bit slug
    is the only access control. URL is slug-only; the uploader's
    filename surfaces via the `Content-Disposition` header on the
    served response, not in the URL.
  - `GET /healthz` -- unauthenticated; bounded SELECT 1 + files-dir
    stat under a 1s ceiling. Probes the wedged-WAL / disk-full /
    mount-gone failure modes that let the bare process answer 200
    while every upload 500s.
  Writes uploads to `/data/files/<slug><ext>`, metadata to
  `/data/share.db` (SQLite). A background sweeper goroutine reaps
  expired rows + files every `MinTTL` (default 10m). At boot, three
  reconcile passes close crash windows: stale `.partial` reap,
  file-without-row reap, row-without-file reap.
- **Reverse proxy** (Caddy in production) -- TLS termination and
  `reverse_proxy` to the container. No `file_server`, no bind-mount
  of `/data/files`. Local dev hits `http://localhost:6980` directly
  with no proxy in the loop.

## Data flow

ASCII diagrams for upload / read / replace live in
`docs/architecture.md`. They're reference material, not directives
-- consult when you need to trace how a request becomes
slug-on-disk.

## Auth

OIDC contract + dual-grant requirement + CLI error sentinels live
in `.claude/rules/005-auth.md`. Pin reading order: read 005-auth.md
before touching `cmd/cli/auth.go`, `cmd/cli/main.go::failRequest`,
or anything in `cmd/server/auth.go`.

## Storage shape

`upload (slug, extension, original_filename, content_type, size,
uploaded_at, ttl_ns)`. Both `uploaded_at` and `ttl_ns` are unix
nanoseconds. `expires_at` is derived in Go via
`Upload.ExpiresAt() = UploadedAt + TTL` — never stored as a column,
which eliminates two-field sync hazards on replace.

The on-disk filename is `slug || extension` — also derived (Go
`Upload.DiskName()`), never a separate column.

## Scope

- **Single uploader, public readers.** No multi-user. No registration.
- **Mandatory expiry.** Every upload has a TTL; no "permanent" mode.
- **kshared owns the read path.** Reverse proxy is `reverse_proxy`
  only.
- **fail2ban-ready.** Slug-miss events on `/s/` carry `client_ip`
  for host-level ip-ban (Caddy forwards real IP via X-Forwarded-For,
  trusted unconditionally).

## Deferred

- Web UI for managing uploads (CLI covers ls/rm/replace).
- Per-upload view-count or download-count caps.
- Image / thumbnail metadata extraction.
- Multi-user / multi-tenant. Changes the auth model meaningfully.
