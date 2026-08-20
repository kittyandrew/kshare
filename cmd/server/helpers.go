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

// slugCollisionRetries bounds the slug-generation loop. With 48 bits of entropy a collision is negligible at
// v1's scale (a few hundred live uploads); the retries cover the impossible case where crypto/rand repeats.
const slugCollisionRetries = 5

// generateSlug's shape must stay in sync with api.SlugRe, which every other component validates against.
func generateSlug() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// randHex names `.partial` scratch files, where the point is to look obviously unlike a slug.
func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// sanitiseOriginalFilename returns a save-as-friendly version of the uploader's filename: no path
// separators, control chars, or quote-shaped chars, so it can be embedded in a Content-Disposition header
// without escaping concerns. Truncation is by rune, so a multi-byte codepoint at the boundary can't be split.
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

func subjectFromCtx(ctx context.Context) string {
	if c, ok := claimsFromContext(ctx); ok {
		return c.Subject
	}
	return ""
}

func isMaxBytesErr(err error) bool {
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe)
}
