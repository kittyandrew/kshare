# Deployment

This repo ships the OCI image. Operator wiring (NixOS module,
agenix secret, Caddy snippet) lives out-of-tree in your own NixOS
config; this doc is the recipe and the threat model.

## Threat model

Trust boundary: the operator holds a Zitadel bearer with the `upload` role.
Reads (`/s/{slug}`) are public; the 48-bit slug is the only access
control. The uploader's filename never appears in the URL -- it's
surfaced via `Content-Disposition` on the served response. Defended: path traversal (slug regex + ext allowlist;
uploader basename never used as path), slug brute-force (fail2ban
backstop), upload DoS (precise `MaxUploadSize` via streaming raw-
body reads, per-route timeouts incl. 30 min on `/s/` for slow-reader
DoS), torn writes (boot reap of `.partial` orphans >5min old). NOT
defended: rate limiting (revoke via Zitadel), content sanitisation
(byte pipe), `X-Forwarded-For` spoofing (trusted unconditionally —
operator owns the "always behind reverse proxy" contract).

## OCI image

```sh
nix build .#kshared-image
docker load < result        # → kshared:v26.05 (CalVer: vYY.MM)
```

Image runs as UID 1000, exposes :6980, `HEALTHCHECK`s `/healthz`
every 10s. Includes `cacert` for JWKS discovery.

**Tag shape.** Image tags follow CalVer (`vYY.MM`) derived from
`self.lastModifiedDate`. Operator-managed git release tags use
`v0.YYMM.Z` (e.g. `v0.2605.0`) so `git tag --sort=v:refname`
orders cleanly across both image tags and release tags.

## docker run

```sh
docker run -d --name kshared \
  --network <edge> \
  --read-only \
  --cap-drop=ALL --security-opt=no-new-privileges --memory=512M \
  -v /srv/containers/kshare:/data \
  --env-file /var/lib/secrets/kshare.env \
  kshared:vYY.MM    # whatever tag `docker load` reported above
```

`--read-only` is safe because the binary writes only under `/data`.
`/data/files` (with `.partial` scratch files inline) + `/data/share.db`
live on the bind mount. No `--tmpfs /data/tmp` — atomic writes scratch
directly under `/data/files/<nonce>.partial` and rename within the
same filesystem.

For ≤2 concurrent uploads at the default 100 MiB cap, 512M memory is
comfortable. Bump if you expect more parallelism.

## env file

```sh
KSHARE_OIDC_ISSUER=https://auth.example.com
KSHARE_OIDC_AUDIENCE=000000000000000000
```

All other server knobs have defaults — see `.env.example` for the
full list. TTL values accept `d` and `w` suffixes (`7d`, `52w`).

## Wire format

POST /api/upload and PUT /api/files/{slug} take **raw bytes in the
body** (no multipart). Two optional request headers:

| Header | Effect |
|---|---|
| `X-KShare-TTL` | Duration string (Go `time.ParseDuration` + `d`/`w`). Falls back to `KSHARE_DEFAULT_TTL` if absent. |
| `X-KShare-Filename` | Uploader's filename. Only used to derive the URL extension + the `Content-Disposition: inline; filename=…` header on `/s/`. Never used as a path component. |

curl example:
```sh
curl -X POST https://share.example.com/api/upload \
  -H "Authorization: Bearer $TOKEN" \
  -H "X-KShare-TTL: 7d" \
  -H "X-KShare-Filename: recipe.html" \
  --data-binary @recipe.html
```

Other endpoints (GET /api/files, DELETE /api/files/{slug}, GET /healthz,
GET /s/{slug}) take/produce JSON or file bytes; no special headers.

## Caddy

TLS termination + reverse proxy. `/healthz` is **not** exposed
publicly — the Docker `HEALTHCHECK` hits `http://localhost:6980/healthz`
from inside the container, and operator probes go via
`docker exec kshared curl http://localhost:6980/healthz`.

```caddy
share.example.com {
    # Block /healthz publicly; Docker HEALTHCHECK + ops only.
    @healthz path /healthz
    handle @healthz {
        respond 404
    }

    handle {
        reverse_proxy kshared:6980
    }
}
```

The `@healthz handle { respond 404 }` block must come BEFORE the
catch-all `handle { reverse_proxy ... }`, and the 404 must be inside
its own `handle` block — a standalone `respond @healthz 404` loses
directive-order against `handle { reverse_proxy }` and leaks through
to the upstream.

## fail2ban

`GET /s/{slug}` 404s emit:

```json
{"level":"warn","event":"slug_miss","client_ip":"<IP>","reason":"<r>",...}
```

`reason ∈ {bad_format, no_row}`. The variant
`event=slug_miss_no_ip` is emitted when the client IP can't be
determined (no `X-Forwarded-For`, malformed `RemoteAddr`); it
deliberately does NOT match the jail regex so an unbannable IP
doesn't poison the pipeline.

```nix
services.fail2ban.jails.kshare-slug-miss = {
  filter = {
    Definition.failregex = ''^.*"event":"slug_miss".*"client_ip":"<HOST>"'';
    Init.journalmatch = "_SYSTEMD_UNIT=docker-kshared.service";
  };
  settings = {
    enabled = true; backend = "systemd";
    maxretry = 5; findtime = "10m"; bantime = "1h";
    action = ''iptables-multiport[name=kshare, port="80,443", chain=DOCKER-USER]'';
  };
};
```

5 misses in 10 minutes triggers a 1-hour ban. The slug is 48 bits;
even at the unrestricted single-IP rate of a few hundred attempts
per second to first-hit takes years.

`DOCKER-USER` chain is required because Docker-published ports
bypass `INPUT`. Tune `maxretry` for paranoia.

## Smoke test (post-deploy)

```sh
nix run .#kshare -- auth login                              # device flow
echo '<h1>hi</h1>' > /tmp/t.html
nix run .#kshare -- /tmp/t.html --ttl 1h               # → URL
curl -sI <URL>     # 200, Content-Type: text/html, Content-Disposition: inline; filename="t.html"
nix run .#kshare -- ls                                  # row visible
nix run .#kshare -- replace <slug> /tmp/v2.html        # same URL, new bytes
nix run .#kshare -- rm <slug>
curl -sI <URL>     # 404
```

Short-TTL check: upload with `--ttl 90s`. `/s/` returns 404
*instantly* on expiry (server-side filter); the file leaves disk
within `MIN_TTL` (sweeper cadence).

## Decisions

History captured during build-out; preserved so future-claude knows
why things are this way.

- **Single-writer SQLite** via `modernc.org/sqlite` (pure-Go, no
  CGO); WAL + `busy_timeout=5s`.
- **Slug = 48 bits**, 8 base64url chars. URL is slug-only (`/s/<slug>`);
  filename + extension surface via `Content-Disposition` on the
  served response, never in the URL. fail2ban backstops brute-force
  (`maxretry=5, findtime=10m, bantime=1h`).
- **TTL: optional**; server applies `DEFAULT_TTL` (7d) when
  `X-KShare-TTL` header is absent. Clamp `[MIN_TTL=10m, MAX_TTL=365d]`,
  env-tunable. `api.ParseTTL` extends Go's parser with `d`/`w`.
- **`/s/{slug}` owned by kshared** — `http.ServeContent` uses
  `sendfile(2)`, so the byte path is still kernel-fast. Eliminates
  dual bind-mount choreography vs Caddy. URL is slug-only; the
  uploader's filename appears in `Content-Disposition` on the
  served response, not in the URL.
- **Replace allows extension change**; slug stays, URL updates.
- **`expires_at` is derived**, never stored. Expression index on
  `(uploaded_at + ttl_ns)` serves both the sweeper and the
  read-path expiry filter.
- **Sweep cadence = `MinTTL`**. Disk hygiene only; user-visible
  expiry is server-side filtered.
- **CLI constructs public URLs** from the server URL in `auth.json`.
  Server doesn't echo a `url` field.
- **`X-Forwarded-For` trusted unconditionally.** Direct exposure
  (no reverse proxy) is not a supported deployment.
- **No rate limit / extension restrictions / pagination / audit
  log.** Single-uploader trust boundary makes these moot for v1.
- **Atomicity:** upload writes `<nonce>.partial` → INSERT row →
  rename to `<slug><ext>`. Replace writes `<nonce>.partial` →
  UPDATE row → rename → (if ext changed) remove old. Boot reap
  catches `.partial` orphans from any crash window.
- **Wire format: raw body + X-KShare-* headers**, no multipart.
  Single source of truth for the metadata (no field-order foot-gun);
  CLI streams the file end-to-end.
- **No /data/tmp**. Scratch lives in `/data/files/<nonce>.partial`
  on the same filesystem as the destination. `--tmpfs` was a deploy-
  time foot-gun (cross-FS rename = EXDEV).
