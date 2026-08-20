package api

// Request headers on POST /api/upload and PUT /api/files/{slug}. Both optional: the server falls back to
// KSHARE_DEFAULT_TTL and to an empty filename.
const (
	HeaderTTL      = "X-KShare-TTL"
	HeaderFilename = "X-KShare-Filename"
)
