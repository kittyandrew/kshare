package store

import (
	"strings"
	"time"

	"go.mau.fi/util/dbutil"
)

// Constant SQL strings. Per 031-beeper-go.md: no sprintf'd dynamic
// parameters, one query per access shape. Adding a new access pattern
// means adding a new constant here and a new method on Container.

const (
	insertUploadQ = `
		INSERT INTO upload (slug, extension, original_filename, content_type, size, uploaded_at, ttl_ns)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	// getUploadBySlugQ filters out expired rows server-side so /s/
	// returns 404 the instant a row expires, regardless of when the
	// sweeper next runs. Same shape for listUploadsQ.
	getUploadBySlugQ = `
		SELECT slug, extension, original_filename, content_type, size, uploaded_at, ttl_ns
		  FROM upload
		 WHERE slug = $1
		   AND (uploaded_at + ttl_ns) > $2
	`
	listUploadsQ = `
		SELECT slug, extension, original_filename, content_type, size, uploaded_at, ttl_ns
		  FROM upload
		 WHERE (uploaded_at + ttl_ns) > $1
		 ORDER BY uploaded_at DESC
	`
	deleteUploadBySlugQ = `DELETE FROM upload WHERE slug = $1`
	listExpiredQ        = `
		SELECT slug, extension, original_filename, content_type, size, uploaded_at, ttl_ns
		  FROM upload
		 WHERE (uploaded_at + ttl_ns) <= $1
		 ORDER BY (uploaded_at + ttl_ns) ASC
	`
	// replaceUploadQ updates the mutable columns of an existing row.
	// slug is the only stable identity; extension MAY change on
	// replace -- in which case the URL changes too. The handler
	// cleans up the now-orphaned file on disk.
	replaceUploadQ = `
		UPDATE upload
		   SET extension         = $1,
		       original_filename = $2,
		       content_type      = $3,
		       size              = $4,
		       uploaded_at       = $5,
		       ttl_ns            = $6
		 WHERE slug = $7
	`
	// listAllDiskNamesQ returns slug+extension for EVERY row, including
	// expired ones. Used only by the boot reconcile reap to build the
	// set of files that are still owned by a row -- so non-expired and
	// expired-but-not-yet-swept files both stay on disk. Anything in
	// files/ not in this set is a crash orphan.
	listAllDiskNamesQ = `SELECT slug, extension FROM upload`
)

// scanUpload is the single scan shape for `upload` rows. Every Container
// method that returns *Upload must run rows through this function so the
// time-conversion convention (unix nanoseconds in the DB; time.Time +
// time.Duration in Go) is enforced in exactly one place.
func scanUpload(row dbutil.Scannable) (*Upload, error) {
	var u Upload
	var uploadedAt, ttlNs int64
	if err := row.Scan(
		&u.Slug,
		&u.Extension,
		&u.OriginalFilename,
		&u.ContentType,
		&u.Size,
		&uploadedAt,
		&ttlNs,
	); err != nil {
		return nil, err
	}
	u.UploadedAt = time.Unix(0, uploadedAt).UTC()
	u.TTL = time.Duration(ttlNs)
	return &u, nil
}

// isUniqueViolation maps modernc.org/sqlite's UNIQUE-constraint error
// shape to a bool so callers can branch on "collision, try again" vs
// "actual storage error." The driver doesn't expose a typed error for
// constraint violations; matching on the text is the documented
// pattern.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "UNIQUE constraint failed") ||
		strings.Contains(s, "constraint failed: UNIQUE")
}
