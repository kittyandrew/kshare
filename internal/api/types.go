// Package api is the wire contract shared by the kshared server and the kshare CLI: DTOs, identity regexes,
// header names. Anything the two binaries would otherwise spell separately belongs here, where drift is a
// compile error instead of a silent runtime mismatch.
package api

import "time"

// Upload is one upload's metadata: the body of POST /api/upload and PUT /api/files/{slug}, and an element of
// the array from GET /api/files. One type for both endpoints and for the CLI decode paths.
//
// No `url` field: the public URL is `serverURL + "/s/" + slug`, which the CLI builds locally. The URL is
// slug-only; the extension surfaces via Content-Disposition on the served response.
type Upload struct {
	Slug             string    `json:"slug"`
	Extension        string    `json:"extension"` // ".html" or "", leading dot included
	OriginalFilename string    `json:"original_filename"`
	Size             int64     `json:"size"`
	ContentType      string    `json:"content_type"`
	UploadedAt       time.Time `json:"uploaded_at"`
	ExpiresAt        time.Time `json:"expires_at"` // derived server-side from uploaded_at + ttl_ns
}
