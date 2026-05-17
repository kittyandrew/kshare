# kshare data flow

ASCII diagrams of the three flows: bearer-gated upload + replace,
plus the public read flow. Reference material for understanding how
a request becomes a slug-on-disk; auth and storage decisions live in
`.claude/rules/001-architecture.md` (topology) and
`.claude/rules/005-auth.md` (OIDC).

## Upload

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

## Read

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

## Replace

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
