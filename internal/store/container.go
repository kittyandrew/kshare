// Package store owns both halves of kshare's persistence: the SQLite rows and the files under
// `${KSHARE_DATA}/files/`. It is the only place that knows about both, which is what keeps the handlers in
// cmd/server/ thin wrappers that can't invent their own file layout.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"

	_ "modernc.org/sqlite" // registers driver name "sqlite"

	"github.com/kittyandrew/kshare/internal/store/upgrades"
)

// Container owns the SQLite database and the on-disk file tree at ${dataDir}/files/. Constructed once at
// boot. `.partial` scratch files share the files/ dir so promote-on-commit is a same-FS rename(2), and the
// suffix means they can never match a slug pattern, keeping them invisible to the /s/ read path.
type Container struct {
	db      *dbutil.Database
	log     zerolog.Logger
	dataDir string
}

// Config is the constructor input; the caller (cmd/server) applies env-based defaults upstream.
type Config struct {
	// DataDir is the root that holds share.db and files/. Required.
	DataDir string

	// Logger is used for sweeper + DB logs. Required.
	Logger zerolog.Logger
}

// NewContainer boot-fails on any error, including a missing data dir: misconfigured storage should be loud,
// not deferred to the first upload.
func NewContainer(ctx context.Context, cfg Config) (*Container, error) {
	if cfg.DataDir == "" {
		return nil, errors.New("store: DataDir is required")
	}
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "files"), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir files dir: %w", err)
	}

	dsn := buildDSN(filepath.Join(cfg.DataDir, "share.db"))
	db, err := dbutil.NewWithDialect(dsn, "sqlite")
	if err != nil {
		return nil, fmt.Errorf("open sqlite at %s: %w", dsn, err)
	}
	db.Log = dbutil.ZeroLogger(cfg.Logger)
	db.Owner = "kshare"
	db.UpgradeTable = upgrades.Table
	if err := db.Upgrade(ctx); err != nil {
		return nil, fmt.Errorf("upgrade schema: %w", err)
	}

	return &Container{
		db:      db,
		log:     cfg.Logger,
		dataDir: cfg.DataDir,
	}, nil
}

// Close must be deferred at boot: it is what lets the underlying *sql.DB flush its WAL file.
func (c *Container) Close() error {
	return c.db.Close()
}

// Ping verifies the DB is alive enough to answer a no-op query. Bounded by the caller's context deadline.
func (c *Container) Ping(ctx context.Context) error {
	var one int
	return c.db.QueryRow(ctx, "SELECT 1").Scan(&one)
}

// FilesDir returns the path to the on-disk file tree, ${dataDir}/files.
func (c *Container) FilesDir() string {
	return filepath.Join(c.dataDir, "files")
}

// Upload is one row of the upload table. Two values are derived rather than stored:
//   - DiskName() = Slug + Extension, the on-disk filename.
//   - ExpiresAt() = UploadedAt + TTL, computed for the API and for display.
//
// Storing expiry next to TTL created a two-field sync hazard on replace. TTL is the input the user gave;
// expiry is the answer to "when does this die", and it is always a function of the two.
type Upload struct {
	Slug             string
	Extension        string        // ".html", ".pdf", ... or "" for none. Leading dot included.
	OriginalFilename string        // as uploaded; surfaced via Content-Disposition + `kshare ls`
	ContentType      string        // e.g. "text/html; charset=utf-8"
	Size             int64         // bytes
	UploadedAt       time.Time     // write time of the current content; reset on replace
	TTL              time.Duration // lifetime as the user specified it
}

// DiskName returns the file's name on disk, `<slug><extension>`. The URL is slug-only, so this never appears
// in a /s/ path; the extension reaches browsers through Content-Disposition instead.
func (u *Upload) DiskName() string {
	return u.Slug + u.Extension
}

// ExpiresAt is derived from UploadedAt + TTL rather than stored, so the two can't drift.
func (u *Upload) ExpiresAt() time.Time {
	return u.UploadedAt.Add(u.TTL)
}

// Insert writes a row. Returns ErrSlugExists if the slug is already taken; the caller retries with a fresh
// slug, bounded by slugCollisionRetries.
func (c *Container) Insert(ctx context.Context, u *Upload) error {
	_, err := c.db.Exec(ctx, insertUploadQ,
		u.Slug, u.Extension, u.OriginalFilename, u.ContentType, u.Size,
		u.UploadedAt.UnixNano(), int64(u.TTL),
	)
	if err != nil {
		// Mapping the driver's UNIQUE violation to a sentinel here keeps the handler free of
		// driver-specific error parsing (see isUniqueViolation).
		if isUniqueViolation(err) {
			return ErrSlugExists
		}
		return fmt.Errorf("insert upload %s: %w", u.Slug, err)
	}
	return nil
}

// GetBySlug returns the row for slug, or (nil, nil) if it doesn't exist OR has expired. The query filters
// server-side via (uploaded_at + ttl_ns) > now, so callers don't have to gate each read against the
// sweeper's tick cadence: /s/ returns 404 the instant a row expires.
//
// Per 031-beeper-go.md, sql.ErrNoRows is suppressed at the store boundary. Expired rows are
// indistinguishable from missing ones to the caller; that is the intent.
func (c *Container) GetBySlug(ctx context.Context, slug string) (*Upload, error) {
	row := c.db.QueryRow(ctx, getUploadBySlugQ, slug, time.Now().UnixNano())
	u, err := scanUpload(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get upload %s: %w", slug, err)
	}
	return u, nil
}

// ListAll returns every non-expired row newest-first. No pagination: v1 expects a steady state of a few
// dozen rows. Expired rows are filtered in SQL, so `kshare ls` never shows entries waiting for the sweeper.
func (c *Container) ListAll(ctx context.Context) ([]*Upload, error) {
	rows, err := c.db.Query(ctx, listUploadsQ, time.Now().UnixNano())
	return dbutil.NewRowIterWithError(rows, scanUpload, err).AsList()
}

// DeleteBySlug removes the row only; the caller removes the on-disk file. Invariant: the DB delete must
// happen FIRST, so a crash between the steps leaves the file unreferenced (cleaned by the boot reconcile
// reap) rather than a row dangling at a missing file. Returns ErrNotFound if no row matched.
func (c *Container) DeleteBySlug(ctx context.Context, slug string) error {
	res, err := c.db.Exec(ctx, deleteUploadBySlugQ, slug)
	if err != nil {
		return fmt.Errorf("delete upload %s: %w", slug, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected (delete %s): %w", slug, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ReplaceContent updates the mutable columns of an existing row. Slug is the only stable identity across
// replacements, so the public URL never changes; the extension MAY change, and the handler then cleans up
// the old on-disk file. Returns ErrNotFound if the row is gone.
func (c *Container) ReplaceContent(ctx context.Context, u *Upload) error {
	res, err := c.db.Exec(ctx, replaceUploadQ,
		u.Extension, u.OriginalFilename, u.ContentType, u.Size,
		u.UploadedAt.UnixNano(), int64(u.TTL),
		u.Slug,
	)
	if err != nil {
		return fmt.Errorf("replace upload %s: %w", u.Slug, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected (replace %s): %w", u.Slug, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListExpired returns rows whose expiry is at or before `now`. Used by the sweeper.
func (c *Container) ListExpired(ctx context.Context, now time.Time) ([]*Upload, error) {
	rows, err := c.db.Query(ctx, listExpiredQ, now.UnixNano())
	return dbutil.NewRowIterWithError(rows, scanUpload, err).AsList()
}

// FilePath returns the on-disk path for the named file (relative to files/). It does NOT check that the file
// exists; the caller owns that.
func (c *Container) FilePath(name string) string {
	return filepath.Join(c.dataDir, "files", name)
}

// WriteFile streams body to files/<destName> via an O_EXCL create. destName MUST be a `.partial` name, or
// anything else that can't match the slug shape and so isn't reachable via /s/. Callers rename to the final
// slug+extension name only after the DB row commits.
//
// On any error the partial file is removed. Returns the byte count actually copied.
func (c *Container) WriteFile(destName string, body io.Reader) (int64, error) {
	path := filepath.Join(c.dataDir, "files", destName)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return 0, fmt.Errorf("create %s: %w", path, err)
	}
	n, copyErr := io.Copy(f, body)
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(path)
		if copyErr != nil {
			return n, fmt.Errorf("write %s: %w", path, copyErr)
		}
		return n, fmt.Errorf("close %s: %w", path, closeErr)
	}
	return n, nil
}

// RemoveFile deletes the on-disk file for an upload. ENOENT is not an error: the sweeper may have deleted
// the row in parallel, or the file was hand-pruned, and either way "file is gone" already holds.
func (c *Container) RemoveFile(name string) error {
	err := os.Remove(filepath.Join(c.dataDir, "files", name))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// RenameFile is an intra-files-dir atomic rename: upload promotes `<random>.partial` to `<slug><ext>` once
// the DB row is committed, and replace swaps its interim "new content" file into the live name.
func (c *Container) RenameFile(from, to string) error {
	return os.Rename(
		filepath.Join(c.dataDir, "files", from),
		filepath.Join(c.dataDir, "files", to),
	)
}

// ReapPartials removes stale `.partial` files in files/, which crashes during upload or replace leave
// orphaned. Anything older than `maxAge` is fair game; younger files might still be live writes. The caller
// invokes this at boot, before the listener starts.
func (c *Container) ReapPartials(maxAge time.Duration) (int, error) {
	dir := filepath.Join(c.dataDir, "files")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("read files dir: %w", err)
	}
	cutoff := time.Now().Add(-maxAge)
	removed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".partial") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			c.log.Warn().Err(err).Str("name", name).Msg("reap-partials: remove failed")
			continue
		}
		removed++
	}
	return removed, nil
}

// ReconcileFiles deletes non-`.partial` files in files/ that have no corresponding row. It covers the crash
// window after a row DELETE commits but before os.Remove of the disk file, and the equivalent window in
// replace's old-extension cleanup. Called at boot, after ReapPartials. Expired-but-not-yet-swept rows still
// have files on disk and stay, since those are sweeper-owned rather than orphans.
func (c *Container) ReconcileFiles(ctx context.Context) (int, error) {
	rows, err := c.db.Query(ctx, listAllDiskNamesQ)
	if err != nil {
		return 0, fmt.Errorf("list disk names: %w", err)
	}
	defer rows.Close()
	known := make(map[string]struct{})
	for rows.Next() {
		var slug, ext string
		if err := rows.Scan(&slug, &ext); err != nil {
			return 0, fmt.Errorf("scan disk name: %w", err)
		}
		known[slug+ext] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate disk names: %w", err)
	}

	dir := filepath.Join(c.dataDir, "files")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("read files dir: %w", err)
	}
	removed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".partial") {
			continue
		}
		if _, ok := known[name]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			c.log.Warn().Err(err).Str("name", name).Msg("reconcile-files: remove orphan failed")
			continue
		}
		removed++
	}
	return removed, nil
}

// ReconcileRows is the inverse of ReconcileFiles: at boot, delete rows whose `<slug><ext>` file is missing.
// It closes the crash window where an upload INSERT committed but the rename failed AND the row rollback
// also failed; without this pass the row would sit pointing at nothing until TTL expiry. The same shape
// covers the replace-after-UPDATE-but-no-rename window, where the .partial is reaped and the row is left
// pointing at a missing file.
//
// Called at boot, after ReapPartials and ReconcileFiles. Safe because the listener hasn't started, so no
// in-flight upload can race it.
func (c *Container) ReconcileRows(ctx context.Context) (int, error) {
	rows, err := c.db.Query(ctx, listAllDiskNamesQ)
	if err != nil {
		return 0, fmt.Errorf("list disk names: %w", err)
	}
	defer rows.Close()
	dir := filepath.Join(c.dataDir, "files")
	removed := 0
	for rows.Next() {
		var slug, ext string
		if err := rows.Scan(&slug, &ext); err != nil {
			return 0, fmt.Errorf("scan disk name: %w", err)
		}
		if _, err := os.Stat(filepath.Join(dir, slug+ext)); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			c.log.Warn().Err(err).Str("slug", slug).Msg("reconcile-rows: stat failed")
			continue
		}
		if _, err := c.db.Exec(ctx, deleteUploadBySlugQ, slug); err != nil {
			c.log.Warn().Err(err).Str("slug", slug).Msg("reconcile-rows: delete row failed")
			continue
		}
		c.log.Info().Str("slug", slug).Str("expected_disk_name", slug+ext).
			Msg("reconcile-rows: deleted row pointing at missing file")
		removed++
	}
	if err := rows.Err(); err != nil {
		return removed, fmt.Errorf("iterate disk names: %w", err)
	}
	return removed, nil
}

// Errors returned by Container methods, package-level so handlers can errors.Is against them.
var (
	ErrSlugExists = errors.New("store: slug already exists")
	ErrNotFound   = errors.New("store: not found")
)

// buildDSN composes the modernc.org/sqlite DSN with WAL + busy-timeout pragmas baked into the URI; the
// pure-Go driver honours `_pragma=` query params at connection time.
//
// No `foreign_keys(ON)`: the schema has no FK relations. Enable it only when a schema change introduces one.
func buildDSN(dbPath string) string {
	// `file:` URI form is required for `_pragma=...` to apply.
	v := url.Values{}
	v.Add("_pragma", "journal_mode(WAL)")
	v.Add("_pragma", "busy_timeout(5000)")
	v.Add("_pragma", "synchronous(NORMAL)")
	return "file:" + dbPath + "?" + v.Encode()
}
