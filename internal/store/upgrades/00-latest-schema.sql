-- v0 -> v1: initial kshare schema. Fresh databases converge directly to this file; numbered NN-*.sql
-- migrations would step existing deployments up to the same shape. Mirrors the signalmeow / mautrix-viber
-- syncbundlestore convention.
--
-- One row per stored upload. expires_at is computed in Go as (uploaded_at + ttl_ns) and is NOT a stored
-- column. Both the sweeper and the read-path expiry filter use `(uploaded_at + ttl_ns) <= <unix_nanos_now>`.

CREATE TABLE upload (
    slug              TEXT    NOT NULL,
    extension         TEXT    NOT NULL,    -- ".html", ".pdf", ... or "" for none. Leading dot included.
    original_filename TEXT    NOT NULL,    -- as uploaded; used for Content-Disposition + `kshare ls`
    content_type      TEXT    NOT NULL,    -- e.g. "text/html; charset=utf-8"
    size              BIGINT  NOT NULL,
    uploaded_at       BIGINT  NOT NULL,    -- unix nanoseconds; reset on replace
    ttl_ns            BIGINT  NOT NULL,    -- the user input, in nanoseconds; expires_at is derived

    PRIMARY KEY (slug)
);

-- Stored columns are the things the user actually specified:
--   * slug: URL identity. The /s/ path is `/s/<slug>`, slug only, no extension in the URL.
--   * extension: on-disk filename suffix only (the file is `<slug><ext>`); also surfaces in
--     `Content-Disposition: inline; filename=...`.
--   * original_filename: surfaced via Content-Disposition and `kshare ls`
--   * content_type, size: sniffed/measured at write
--   * uploaded_at: write time of the current content (reset on replace), stored as unix nanoseconds so
--     sub-second writes don't collide on `uploaded_at DESC` ordering
--   * ttl_ns: lifetime requested at upload, in nanoseconds (matches Go's time.Duration natural unit)
--
-- expires_at is NOT stored; it is uploaded_at + ttl_ns. Storing both created a two-field sync hazard on
-- replace, so now the relationship is unambiguous.

-- Expression index for the sweeper's `WHERE uploaded_at + ttl_ns <= ?`. SQLite 3.9+ honours expression
-- indexes.
CREATE INDEX upload_expiry_idx ON upload (uploaded_at + ttl_ns);
