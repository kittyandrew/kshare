package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"time"

	"github.com/kittyandrew/kshare/internal/api"
)

// uploadHeaders centralises the X-KShare-* header names used by the
// CLI <-> server wire. Single source of truth so a typo is a compile
// error, not a runtime silent-default fallback.
const (
	headerTTL      = "X-KShare-TTL"
	headerFilename = "X-KShare-Filename"
)

// requestInputs is what handleUpload/handleReplaceFile derive from
// the request before touching disk. Single shape so the two handlers
// share the parser.
type requestInputs struct {
	TTL              time.Duration
	OriginalFilename string    // sanitised
	Extension        string    // ".html" or "" (leading dot)
	ContentType      string    // sniffed from first 512 bytes
	File             io.Reader // sniff bytes prepended; rest streams
}

// parseUploadRequest validates the wire-layer inputs (TTL header in
// range, filename header non-malicious) and returns a reader
// positioned at byte 0 of the file body (sniff buffer + remaining
// body via io.MultiReader). The caller streams from File into
// WriteFile.
//
// Wire format (POST /api/upload + PUT /api/files/{slug}):
//   - Body: raw file bytes, capped by MaxBytesReader = MaxUploadSize.
//   - X-KShare-TTL: optional duration; falls back to DEFAULT_TTL if
//     absent. api.ParseTTL accepts `d`/`w` suffixes.
//   - X-KShare-Filename: optional uploader filename; only used to
//     derive Extension + OriginalFilename. Server synthesises the
//     on-disk name as `<slug><extension>` regardless.
func (s *server) parseUploadRequest(r *http.Request) (*requestInputs, int, error) {
	out := &requestInputs{TTL: s.cfg.DefaultTTL}

	if t := r.Header.Get(headerTTL); t != "" {
		d, err := api.ParseTTL(t)
		if err != nil {
			return nil, http.StatusBadRequest, fmt.Errorf("%s: %w", headerTTL, err)
		}
		out.TTL = d
	}
	if out.TTL < s.cfg.MinTTL || out.TTL > s.cfg.MaxTTL {
		return nil, http.StatusBadRequest, fmt.Errorf(
			"ttl %s outside allowed range [%s, %s]", out.TTL, s.cfg.MinTTL, s.cfg.MaxTTL)
	}

	hf := r.Header.Get(headerFilename)
	out.OriginalFilename = sanitiseOriginalFilename(filepath.Base(hf))
	out.Extension = sanitiseExt(filepath.Ext(hf))

	sniffBuf := make([]byte, 512)
	n, err := io.ReadFull(r.Body, sniffBuf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		if isMaxBytesErr(err) {
			return nil, http.StatusRequestEntityTooLarge,
				fmt.Errorf("upload exceeds %d bytes", s.cfg.MaxUploadSize)
		}
		return nil, http.StatusBadRequest, fmt.Errorf("read body: %w", err)
	}
	if n == 0 {
		return nil, http.StatusBadRequest, errors.New("body is empty")
	}
	ct := http.DetectContentType(sniffBuf[:n])
	if ct == "" {
		ct = "application/octet-stream"
	}
	out.ContentType = ct
	out.File = io.MultiReader(bytes.NewReader(sniffBuf[:n]), r.Body)
	return out, 0, nil
}

// streamToPartial is phase 1 of upload + replace: wrap the body with
// MaxBytesReader, parse the wire-layer inputs, and stream the file
// to a nonce-named `.partial` in files/. The caller owns cleanup of
// the partial on any subsequent error path; on the happy path the
// caller renames it to its final `<slug><ext>` name.
//
// Returns (partialName, size, inputs, statusCode, error). On error
// the caller passes status + error directly into httpError; on
// success status is 0.
func (s *server) streamToPartial(w http.ResponseWriter, r *http.Request) (string, int64, *requestInputs, int, error) {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadSize)

	in, status, perr := s.parseUploadRequest(r)
	if perr != nil {
		return "", 0, nil, status, perr
	}

	nonce, err := randHex(8)
	if err != nil {
		return "", 0, nil, http.StatusInternalServerError, fmt.Errorf("nonce: %w", err)
	}
	partialName := nonce + ".partial"

	size, err := s.container.WriteFile(partialName, in.File)
	if err != nil {
		if isMaxBytesErr(err) {
			return "", 0, nil, http.StatusRequestEntityTooLarge,
				fmt.Errorf("upload exceeds %d bytes", s.cfg.MaxUploadSize)
		}
		return "", 0, nil, http.StatusInternalServerError, fmt.Errorf("write file: %w", err)
	}
	return partialName, size, in, 0, nil
}

// writeUploadJSON serialises the upload-response shape consumed by
// POST /api/upload + PUT /api/files/{slug}. Single source of truth
// so the two handlers can't drift on field shape; a future field add
// is one edit.
func writeUploadJSON(w http.ResponseWriter, slug string, in *requestInputs, size int64, uploadedAt time.Time) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(api.Upload{
		Slug:             slug,
		Extension:        in.Extension,
		OriginalFilename: in.OriginalFilename,
		Size:             size,
		ContentType:      in.ContentType,
		UploadedAt:       uploadedAt,
		ExpiresAt:        uploadedAt.Add(in.TTL),
	})
}
