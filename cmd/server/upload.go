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
//  2. Generate a slug and INSERT the row claiming files/<slug><ext>. Retry on collision; the file is
//     already on disk, so this stays inside the DB.
//  3. Atomic rename .partial -> <slug><ext>.
//
// A crash at any step before (3) leaves a .partial behind, which boot-time ReapPartials sweeps.
func (s *server) handleUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := zerolog.Ctx(ctx)

	partialName, size, in, status, perr := s.streamToPartial(w, r)
	if perr != nil {
		httpError(ctx, w, status, perr.Error(), nil)
		return
	}

	u, err := s.insertWithRetry(ctx, in, size, time.Now().UTC())
	if err != nil {
		_ = s.container.RemoveFile(partialName)
		httpError(ctx, w, http.StatusInternalServerError, "insert upload", err)
		return
	}

	if err := s.container.RenameFile(partialName, u.DiskName()); err != nil {
		if delErr := s.container.DeleteBySlug(ctx, u.Slug); delErr != nil {
			log.Error().Err(delErr).Str("slug", u.Slug).
				Msg("upload: failed to roll back row after rename failure")
		}
		_ = s.container.RemoveFile(partialName)
		httpError(ctx, w, http.StatusInternalServerError, "promote partial", err)
		return
	}

	log.Info().
		Str("slug", u.Slug).
		Str("disk_name", u.DiskName()).
		Str("original_filename", u.OriginalFilename).
		Int64("size", u.Size).
		Str("content_type", u.ContentType).
		Dur("ttl", u.TTL).
		Str("subject", subjectFromCtx(ctx)).
		Msg("upload accepted")

	writeUploadJSON(w, u)
}

// insertWithRetry is phase 2 of handleUpload: generate a slug and INSERT, retrying on ErrSlugExists (see
// slugCollisionRetries). The row is built once outside the loop; only Slug mutates per attempt.
func (s *server) insertWithRetry(ctx context.Context, in *requestInputs, size int64, uploadedAt time.Time) (*store.Upload, error) {
	log := zerolog.Ctx(ctx)
	u := &store.Upload{
		Extension:        in.Extension,
		OriginalFilename: in.OriginalFilename,
		ContentType:      in.ContentType,
		Size:             size,
		UploadedAt:       uploadedAt,
		TTL:              in.TTL,
	}
	for attempt := range slugCollisionRetries {
		slug, err := generateSlug()
		if err != nil {
			return nil, fmt.Errorf("generate slug: %w", err)
		}
		u.Slug = slug
		err = s.container.Insert(ctx, u)
		if err == nil {
			return u, nil
		}
		if errors.Is(err, store.ErrSlugExists) {
			log.Debug().Str("slug", slug).Int("attempt", attempt).
				Msg("upload: slug collision; retrying")
			continue
		}
		return nil, fmt.Errorf("insert row: %w", err)
	}
	return nil, fmt.Errorf("could not find unused slug after %d attempts", slugCollisionRetries)
}
