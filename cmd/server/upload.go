package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog"

	"github.com/kittyandrew/kshare/internal/store"
)

// handleUpload implements POST /api/upload.
//
// Atomicity contract:
//  1. Stream the body to files/<nonce>.partial. No slug yet.
//  2. Generate a slug and INSERT the row claiming files/<slug><ext>.
//     Retry on collision (intra-DB; the file is already on disk).
//  3. Atomic rename .partial -> <slug><ext>.
//
// Crash at any step before (3) leaves a .partial behind, which the
// boot-time ReapPartials sweeps.
func (s *server) handleUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := zerolog.Ctx(ctx)

	partialName, size, in, status, perr := s.streamToPartial(w, r)
	if perr != nil {
		httpError(ctx, w, status, perr.Error(), nil)
		return
	}

	uploadedAt := time.Now().UTC()
	slug, err := s.insertWithRetry(ctx, in, size, uploadedAt)
	if err != nil {
		_ = s.container.RemoveFile(partialName)
		httpError(ctx, w, http.StatusInternalServerError, "insert upload", err)
		return
	}

	diskName := slug + in.Extension
	if err := s.container.RenameFile(partialName, diskName); err != nil {
		if delErr := s.container.DeleteBySlug(ctx, slug); delErr != nil {
			log.Error().Err(delErr).Str("slug", slug).
				Msg("upload: failed to roll back row after rename failure")
		}
		_ = s.container.RemoveFile(partialName)
		httpError(ctx, w, http.StatusInternalServerError, "promote partial", err)
		return
	}

	log.Info().
		Str("slug", slug).
		Str("disk_name", diskName).
		Str("original_filename", in.OriginalFilename).
		Int64("size", size).
		Str("content_type", in.ContentType).
		Dur("ttl", in.TTL).
		Str("subject", subjectFromCtx(ctx)).
		Msg("upload accepted")

	writeUploadJSON(w, slug, in, size, uploadedAt)
}

// insertWithRetry is phase 2 of handleUpload: generate a slug and
// INSERT, retrying on ErrSlugExists up to slugCollisionRetries times.
// Collision is negligible at v1's scale (48 bits of entropy, a few
// hundred live uploads); the loop covers the impossible case where
// crypto/rand returns a duplicate. The *store.Upload is built once
// outside the loop; only Slug mutates per attempt.
func (s *server) insertWithRetry(ctx context.Context, in *requestInputs, size int64, uploadedAt time.Time) (string, error) {
	log := zerolog.Ctx(ctx)
	u := &store.Upload{
		Extension:        in.Extension,
		OriginalFilename: in.OriginalFilename,
		ContentType:      in.ContentType,
		Size:             size,
		UploadedAt:       uploadedAt,
		TTL:              in.TTL,
	}
	for attempt := 0; attempt < slugCollisionRetries; attempt++ {
		slug, err := generateSlug()
		if err != nil {
			return "", fmt.Errorf("generate slug: %w", err)
		}
		u.Slug = slug
		err = s.container.Insert(ctx, u)
		if err == nil {
			return slug, nil
		}
		if errors.Is(err, store.ErrSlugExists) {
			log.Debug().Str("slug", slug).Int("attempt", attempt).
				Msg("upload: slug collision; retrying")
			continue
		}
		return "", fmt.Errorf("insert row: %w", err)
	}
	return "", fmt.Errorf("could not find unused slug after %d attempts", slugCollisionRetries)
}
