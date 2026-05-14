package store

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// newTestContainer constructs a Container backed by an on-disk SQLite
// in t.TempDir(). Each test gets a private data dir. Logger is at
// disabled level so test output stays clean; flip to Trace if you're
// chasing a flake.
func newTestContainer(t *testing.T) *Container {
	t.Helper()
	c, err := NewContainer(context.Background(), Config{
		DataDir: t.TempDir(),
		Logger:  zerolog.Nop(),
	})
	if err != nil {
		t.Fatalf("NewContainer: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// mustInsert seeds a row that expires at `expires`. Constructs the
// stored fields (UploadedAt + TTL) from a single absolute expiry so
// tests stay readable; ExpiresAt() will return `expires` (truncated
// to seconds, since that's what the DB round-trip preserves).
func mustInsert(t *testing.T, c *Container, slug string, expires time.Time) *Upload {
	t.Helper()
	uploadedAt := time.Now().UTC().Truncate(time.Second)
	u := &Upload{
		Slug:             slug,
		Extension:        ".html",
		OriginalFilename: "test.html",
		ContentType:      "text/html; charset=utf-8",
		Size:             42,
		UploadedAt:       uploadedAt,
		TTL:              expires.UTC().Truncate(time.Second).Sub(uploadedAt),
	}
	if err := c.Insert(context.Background(), u); err != nil {
		t.Fatalf("Insert %s: %v", slug, err)
	}
	return u
}

func TestContainer_OpenAndUpgrade(t *testing.T) {
	c := newTestContainer(t)
	// Empty list on a fresh DB.
	got, err := c.ListAll(context.Background())
	if err != nil {
		t.Fatalf("ListAll on empty DB: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("fresh DB should be empty, got %d rows", len(got))
	}
	// files/ exists -- .partial scratch lives alongside committed files.
	info, err := os.Stat(filepath.Join(c.dataDir, "files"))
	if err != nil {
		t.Fatalf("stat files: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("files should be a directory")
	}
}

func TestContainer_InsertGetDelete(t *testing.T) {
	c := newTestContainer(t)
	ctx := context.Background()
	want := mustInsert(t, c, "abc12345", time.Now().Add(time.Hour))

	got, err := c.GetBySlug(ctx, want.Slug)
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if got == nil {
		t.Fatal("GetBySlug returned nil for just-inserted row")
	}
	if got.Extension != want.Extension || got.Size != want.Size {
		t.Fatalf("round-trip mismatch: got %+v want %+v", got, want)
	}

	// Delete + verify gone.
	if err := c.DeleteBySlug(ctx, want.Slug); err != nil {
		t.Fatalf("DeleteBySlug: %v", err)
	}
	got, err = c.GetBySlug(ctx, want.Slug)
	if err != nil {
		t.Fatalf("GetBySlug after delete: %v", err)
	}
	if got != nil {
		t.Fatalf("row should be gone after delete, got %+v", got)
	}

	// Second delete -> ErrNotFound (idempotency check).
	if err := c.DeleteBySlug(ctx, want.Slug); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteBySlug second time: want ErrNotFound, got %v", err)
	}
}

func TestContainer_InsertCollision(t *testing.T) {
	c := newTestContainer(t)
	ctx := context.Background()
	mustInsert(t, c, "dupe1234", time.Now().Add(time.Hour))

	dupe := &Upload{
		Slug:             "dupe1234",
		Extension:        ".txt",
		OriginalFilename: "dupe.txt",
		ContentType:      "text/plain",
		Size:             1,
		UploadedAt:       time.Now().UTC(),
		TTL:              time.Hour,
	}
	err := c.Insert(ctx, dupe)
	if !errors.Is(err, ErrSlugExists) {
		t.Fatalf("duplicate Insert: want ErrSlugExists, got %v", err)
	}
}

func TestContainer_ListAll_NewestFirst(t *testing.T) {
	c := newTestContainer(t)
	ctx := context.Background()
	// Three rows with explicit uploaded_at offset back from now,
	// oldest first. Long TTL so the new server-side expiry filter
	// in ListAll doesn't hide them.
	now := time.Now().UTC().Truncate(time.Second)
	for i, slug := range []string{"oldslug1", "midslug1", "newslug1"} {
		u := &Upload{
			Slug:             slug,
			Extension:        ".html",
			OriginalFilename: slug + ".html",
			ContentType:      "text/html",
			Size:             int64(100 + i),
			UploadedAt:       now.Add(time.Duration(i-3) * 10 * time.Second),
			TTL:              time.Hour,
		}
		if err := c.Insert(ctx, u); err != nil {
			t.Fatalf("Insert %s: %v", slug, err)
		}
	}
	rows, err := c.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if rows[0].Slug != "newslug1" || rows[2].Slug != "oldslug1" {
		t.Fatalf("ListAll not newest-first: %v %v %v", rows[0].Slug, rows[1].Slug, rows[2].Slug)
	}
}

// TestContainer_ListAll_FiltersExpired confirms the new server-side
// expiry filter hides rows that are past their (uploaded_at + ttl).
func TestContainer_ListAll_FiltersExpired(t *testing.T) {
	c := newTestContainer(t)
	ctx := context.Background()
	// Fresh row: should appear.
	mustInsert(t, c, "fresh111", time.Now().Add(time.Hour))
	// Expired row: uploaded 2h ago with 1h TTL.
	expired := &Upload{
		Slug:             "exp1234x",
		Extension:        ".txt",
		OriginalFilename: "expired.txt",
		ContentType:      "text/plain",
		Size:             10,
		UploadedAt:       time.Now().Add(-2 * time.Hour).UTC(),
		TTL:              time.Hour,
	}
	if err := c.Insert(ctx, expired); err != nil {
		t.Fatalf("Insert expired: %v", err)
	}

	rows, err := c.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(rows) != 1 || rows[0].Slug != "fresh111" {
		t.Fatalf("ListAll should hide expired rows, got %v", rows)
	}

	// And GetBySlug should also hide the expired one.
	got, err := c.GetBySlug(ctx, "exp1234x")
	if err != nil {
		t.Fatalf("GetBySlug expired: %v", err)
	}
	if got != nil {
		t.Errorf("GetBySlug on expired row should be nil, got %+v", got)
	}
}

func TestContainer_WriteAtomic(t *testing.T) {
	c := newTestContainer(t)
	const body = "<!doctype html><h1>hi</h1>\n"
	n, err := c.WriteFile("aF3xK9pQ.html", strings.NewReader(body))
	if err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	if n != int64(len(body)) {
		t.Fatalf("WriteAtomic wrote %d bytes, want %d", n, len(body))
	}
	got, err := os.ReadFile(c.FilePath("aF3xK9pQ.html"))
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(got) != body {
		t.Fatalf("file contents mismatch: %q vs %q", string(got), body)
	}
	// Only one file should exist in files/ after a successful write.
	entries, err := os.ReadDir(filepath.Join(c.dataDir, "files"))
	if err != nil {
		t.Fatalf("read files dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("files/ should have exactly the one file, has %d", len(entries))
	}
}

// errReader is an io.Reader that returns an error after the first
// successful read. Used to drive WriteAtomic into its failure branch
// so we can verify the tempfile is cleaned up.
type errReader struct {
	delivered bool
}

func (e *errReader) Read(p []byte) (int, error) {
	if !e.delivered {
		copy(p, []byte("partial"))
		e.delivered = true
		return len("partial"), nil
	}
	return 0, io.ErrUnexpectedEOF
}

func TestContainer_WriteFile_FailureCleansUp(t *testing.T) {
	c := newTestContainer(t)
	_, err := c.WriteFile("doesnotmatter.html", &errReader{})
	if err == nil {
		t.Fatal("WriteFile with failing reader should error")
	}
	// Destination must NOT exist.
	if _, err := os.Stat(c.FilePath("doesnotmatter.html")); err == nil {
		t.Fatal("dest file should not exist after a failed write")
	}
	// files/ must be empty (no orphan from failed write).
	entries, _ := os.ReadDir(filepath.Join(c.dataDir, "files"))
	if len(entries) != 0 {
		t.Fatalf("files/ should be empty after failed write, has %d entries", len(entries))
	}
}

func TestContainer_RemoveFile_TolerateENOENT(t *testing.T) {
	c := newTestContainer(t)
	if err := c.RemoveFile("never-existed.html"); err != nil {
		t.Fatalf("RemoveFile on missing file should be nil, got %v", err)
	}
}

func TestSweeper_RemovesExpiredOnly(t *testing.T) {
	c := newTestContainer(t)
	ctx := context.Background()

	now := time.Now()
	// Two rows expired, one fresh.
	expired := mustInsert(t, c, "exp1aaaa", now.Add(-time.Hour))
	expired2 := mustInsert(t, c, "exp2aaaa", now.Add(-time.Minute))
	fresh := mustInsert(t, c, "fresh1aa", now.Add(time.Hour))

	// Materialise files on disk so RemoveFile has something to delete.
	for _, u := range []*Upload{expired, expired2, fresh} {
		if _, err := c.WriteFile(u.DiskName(), strings.NewReader("payload")); err != nil {
			t.Fatalf("seed file %s: %v", u.DiskName(), err)
		}
	}

	// One immediate sweep via the unexported entry point: avoids
	// having to drive a ticker. Equivalent to the boot-time sweep
	// inside RunSweeper.
	c.sweepOnce(ctx, c.log)

	if got, _ := c.GetBySlug(ctx, expired.Slug); got != nil {
		t.Fatalf("expired1 should be gone, got %+v", got)
	}
	if got, _ := c.GetBySlug(ctx, expired2.Slug); got != nil {
		t.Fatalf("expired2 should be gone, got %+v", got)
	}
	if got, _ := c.GetBySlug(ctx, fresh.Slug); got == nil {
		t.Fatal("fresh row should survive the sweep")
	}
	// On-disk: expired files gone, fresh stays.
	for _, u := range []*Upload{expired, expired2} {
		if _, err := os.Stat(c.FilePath(u.DiskName())); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expired file %s should be gone (err=%v)", u.DiskName(), err)
		}
	}
	if _, err := os.Stat(c.FilePath(fresh.DiskName())); err != nil {
		t.Fatalf("fresh file %s should remain: %v", fresh.DiskName(), err)
	}
}

// TestSweeper_BootSweep covers the synchronous boot pass used by
// main.go to close the "URL still serves expired" window across
// restart. BootSweep is the exported synchronous variant; the
// timer-driven RunSweeper wraps it. No goroutine, no time.Sleep.
func TestSweeper_BootSweep(t *testing.T) {
	c := newTestContainer(t)
	mustInsert(t, c, "expired0", time.Now().Add(-time.Hour))

	c.BootSweep(context.Background())

	got, err := c.GetBySlug(context.Background(), "expired0")
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if got != nil {
		t.Fatalf("boot sweep should have removed expired row, got %+v", got)
	}
}
