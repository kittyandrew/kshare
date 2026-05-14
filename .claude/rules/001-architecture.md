# Architecture
<!-- .claude/rules/001-architecture.md -- component topology, data flow, scope -->

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

## Flow (upload)

```
kshare <file>           # CLI
 |  ensure-fresh-access-token (device flow if missing/expired)
 |
 +- POST share.example.com/api/upload
        Authorization: Bearer <JWT>
        X-KShare-TTL: 7d                  (optional; server default if absent)
        X-KShare-Filename: recipe.html    (optional; URL-extension + Content-Disposition)
        body: <raw file bytes>
        |
        reverse_proxy --> kshared:
              verify JWT (JWKS, audience pin, role:upload)
              MaxBytesReader(body) + read X-KShare-{TTL,Filename}
              clamp ttl to [MIN_TTL, MAX_TTL]; apply DEFAULT_TTL if empty
              sniff content-type from first 512 bytes
              generate slug (8 base64url chars / 48 bits, retry on collision)
              WriteFile("<nonce>.partial", body)          # streams to disk
              Insert row (slug, extension, original_filename,
                          content_type, size, uploaded_at, ttl_ns)
              RenameFile("<nonce>.partial", "<slug><ext>")  # intra-dir atomic rename
              return JSON: {slug, extension, original_filename,
                            size, content_type, uploaded_at, expires_at}
 |
 <- 200 OK
 |
 print URL (constructed CLI-side from server URL) + wl-copy
```

## Flow (read)

```
GET share.example.com/s/aF3xK9pQ
 |
 +- reverse_proxy --> kshared:
      SlugRe match? (8 chars [A-Za-z0-9_-])
        no  -> 404 + WARN event=slug_miss reason=bad_format
      GetBySlug(slug):
        no row -> 404 + WARN event=slug_miss reason=no_row
        ok     -> http.ServeContent
                    Content-Type from DB row
                    Content-Disposition: inline; filename="<original>"
                    Range / If-Modified-Since handled
```

## Flow (replace)

```
kshare replace <slug> <file> [--ttl X]
 |
 +- PUT share.example.com/api/files/{slug}
        Authorization: Bearer <JWT>
        X-KShare-TTL / X-KShare-Filename (same as upload)
        body: <raw file bytes>
        |
        reverse_proxy --> kshared:
              same parse + validate as upload
              WriteFile("<nonce>.partial", body)
              ReplaceContent row (extension, original_filename, content_type,
                                  size, uploaded_at, ttl_ns)
              RenameFile("<nonce>.partial", slug + newExt)
              if newExt != oldExt: RemoveFile(slug + oldExt)   best-effort
              return JSON (same shape as upload; URL may differ if ext changed)
```

## Auth

Zitadel OIDC. One project (`kshare`), one role (`upload`). Two
apps: API `kshare-server` (audience pin, JWT private key auth)
and Native `kshare-cli` (device flow, JWT, "Add user roles to
access token" toggle ON). The server validates JWT signature,
audience, and the project-role claim per request. Reads
(`/s/{slug}`) are NOT authenticated -- the slug is the gate.

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
