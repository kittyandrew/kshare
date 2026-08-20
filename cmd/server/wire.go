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
	"github.com/kittyandrew/kshare/internal/store"
)

// requestInputs is what handleUpload + handleReplaceFile derive from a request before touching disk; one
// shape so both share the parser.
type requestInputs struct {
	TTL              time.Duration
	OriginalFilename string    // sanitised
	Extension        string    // ".html" or "" (leading dot)
	ContentType      string    // sniffed from first 512 bytes
	File             io.Reader // sniff bytes prepended; rest streams
}

// parseUploadRequest validates the wire-layer inputs and returns a reader positioned at byte 0 of the body
// (sniff buffer + remaining body via io.MultiReader). The caller streams File into WriteFile.
//
// Wire format (POST /api/upload + PUT /api/files/{slug}):
//   - Body: raw file bytes, capped by MaxBytesReader = MaxUploadSize.
//   - X-KShare-TTL: optional duration, DEFAULT_TTL if absent. api.ParseTTL accepts `d`/`w` suffixes.
//   - X-KShare-Filename: optional, and only used to derive Extension + OriginalFilename. The on-disk name
//     is `<slug><extension>` regardless of what the uploader sent.
func (s *server) parseUploadRequest(r *http.Request) (*requestInputs, int, error) {
	out := &requestInputs{TTL: s.cfg.DefaultTTL}

	if t := r.Header.Get(api.HeaderTTL); t != "" {
		d, err := api.ParseTTL(t)
		if err != nil {
			return nil, http.StatusBadRequest, fmt.Errorf("%s: %w", api.HeaderTTL, err)
		}
		out.TTL = d
	}
	if out.TTL < s.cfg.MinTTL || out.TTL > s.cfg.MaxTTL {
		return nil, http.StatusBadRequest, fmt.Errorf(
			"ttl %s outside allowed range [%s, %s]", out.TTL, s.cfg.MinTTL, s.cfg.MaxTTL)
	}

	hf := r.Header.Get(api.HeaderFilename)
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

// streamToPartial is phase 1 of upload + replace: cap the body, parse the wire inputs, stream to a
// nonce-named `.partial` in files/. The caller owns that partial from here, removing it on any error path
// and renaming it to `<slug><ext>` on the happy one. The returned status is 0 on success, otherwise it goes
// straight into httpError.
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

// toAPI is the only store-row to DTO mapping in the server. Every response goes through it, so expiry stays
// derived in exactly one place (store.Upload.ExpiresAt) and no endpoint can drift on field shape.
func toAPI(u *store.Upload) api.Upload {
	return api.Upload{
		Slug:             u.Slug,
		Extension:        u.Extension,
		OriginalFilename: u.OriginalFilename,
		Size:             u.Size,
		ContentType:      u.ContentType,
		UploadedAt:       u.UploadedAt,
		ExpiresAt:        u.ExpiresAt(),
	}
}

func writeUploadJSON(w http.ResponseWriter, u *store.Upload) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(toAPI(u))
}
