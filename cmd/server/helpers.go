package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
)

// slugCollisionRetries bounds the slug-generation loop. With 48 bits
// of entropy collision probability is negligible at v1's scale (a
// few hundred live uploads); 5 retries covers the impossible case
// where crypto/rand returns a duplicate.
const slugCollisionRetries = 5

// generateSlug returns an 8-char base64url string from 6 random
// bytes (48 bits of entropy). Single-uploader scope; fail2ban
// backstops slug brute-force.
func generateSlug() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// randHex returns n hex-encoded random bytes. Used for .partial /
// nonce filenames where we want something obviously not-a-slug.
func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// sanitiseOriginalFilename returns a save-as-friendly representation
// of the uploader's filename. Strips path separators, control chars,
// and quote-shaped chars so it can be embedded into a
// Content-Disposition header without escaping concerns. Truncates by
// rune (not byte) so a multi-byte codepoint at the boundary isn't
// split.
func sanitiseOriginalFilename(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c < 0x20 || c == 0x7f || c == '"' || c == '\\' {
			continue
		}
		b.WriteByte(c)
	}
	out := b.String()
	const maxRunes = 200
	rs := []rune(out)
	if len(rs) > maxRunes {
		out = string(rs[:maxRunes])
	}
	return out
}

// subjectFromCtx returns the authenticated principal's subject claim,
// or "" if no claims are attached.
func subjectFromCtx(ctx context.Context) string {
	if c, ok := claimsFromContext(ctx); ok {
		return c.Subject
	}
	return ""
}

// isMaxBytesErr returns true if err is or wraps http.MaxBytesError.
func isMaxBytesErr(err error) bool {
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe)
}
