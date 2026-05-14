// Package api holds the JSON DTOs + identity regexes shared between
// the kshared HTTP server and the kshare CLI. Single source of truth:
// a field-tag drift between server emit and CLI decode becomes a
// compile-time concern, not a silent runtime mismatch.
package api

import "time"

// Upload is one upload's metadata. Returned as the body of POST
// /api/upload and PUT /api/files/{slug}; returned as a JSON array
// element of GET /api/files. Single type used by both endpoints +
// the CLI decode paths.
//
// No `url` field: the public URL is `serverURL + "/s/" + slug` (the
// URL is slug-only; extension surfaces via Content-Disposition on the
// served response) and the CLI constructs it locally.
type Upload struct {
	Slug             string    `json:"slug"`
	Extension        string    `json:"extension"` // ".html" or "" (leading dot); on-disk + Content-Disposition only
	OriginalFilename string    `json:"original_filename"`
	Size             int64     `json:"size"`
	ContentType      string    `json:"content_type"`
	UploadedAt       time.Time `json:"uploaded_at"`
	ExpiresAt        time.Time `json:"expires_at"` // derived server-side from uploaded_at + ttl_ns
}
