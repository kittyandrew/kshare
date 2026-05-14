package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/rs/zerolog"

	"github.com/kittyandrew/kshare/internal/api"
	"github.com/kittyandrew/kshare/internal/store"
)

func (s *server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := s.container.ListAll(ctx)
	if err != nil {
		httpError(ctx, w, http.StatusInternalServerError, "list uploads", err)
		return
	}
	out := make([]api.Upload, 0, len(rows))
	for _, u := range rows {
		out = append(out, api.Upload{
			Slug:             u.Slug,
			Extension:        u.Extension,
			OriginalFilename: u.OriginalFilename,
			Size:             u.Size,
			ContentType:      u.ContentType,
			UploadedAt:       u.UploadedAt,
			ExpiresAt:        u.ExpiresAt(),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// handleDeleteFile implements DELETE /api/files/{slug}.
// Order: validate slug -> SELECT row -> DELETE row -> remove file
// -> 204. The DB DELETE runs BEFORE the file remove so a crash
// between the two leaves the file unreferenced (boot reconcile reap
// catches it) and not a row pointing at nothing.
func (s *server) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := zerolog.Ctx(ctx)

	slug := r.PathValue("slug")
	if !api.SlugRe.MatchString(slug) {
		httpError(ctx, w, http.StatusBadRequest, "invalid slug", nil)
		return
	}

	u, err := s.container.GetBySlug(ctx, slug)
	if err != nil {
		httpError(ctx, w, http.StatusInternalServerError, "get upload", err)
		return
	}
	if u == nil {
		httpError(ctx, w, http.StatusNotFound, "not found", nil)
		return
	}

	delErr := s.container.DeleteBySlug(ctx, slug)
	if delErr != nil && !errors.Is(delErr, store.ErrNotFound) {
		httpError(ctx, w, http.StatusInternalServerError, "delete row", delErr)
		return
	}
	if errors.Is(delErr, store.ErrNotFound) {
		log.Debug().Str("slug", slug).Msg("delete: row already gone")
	}
	if err := s.container.RemoveFile(u.DiskName()); err != nil {
		log.Warn().Err(err).Str("slug", slug).Str("disk_name", u.DiskName()).
			Msg("delete: remove file failed")
	}

	log.Info().
		Str("slug", slug).
		Str("disk_name", u.DiskName()).
		Str("subject", subjectFromCtx(ctx)).
		Msg("upload deleted")
	w.WriteHeader(http.StatusNoContent)
}

// handleReplaceFile implements PUT /api/files/{slug}. Slug is the
// only stable identity across replacements; the public URL is
// unchanged. On-disk extension follows the new filename.
// TTL resets from X-KShare-TTL (or server DEFAULT_TTL if absent).
// Content-type is re-sniffed from new bytes.
//
// Atomicity contract:
//  1. Stream new content to a nonce-named .partial. The live file
//     (old content) is untouched.
//  2. UPDATE the DB row. On failure, remove the .partial; old state
//     intact.
//  3. After UPDATE commits, rename .partial -> <slug><newExt>.
//  4. If newExt != oldExt, best-effort remove the old <slug><oldExt>.
//
// Crash recovery: a .partial left from steps 1-2 is reaped by the
// boot scanner. A committed file with an UPDATE-but-no-rename gap
// is recovered by boot ReconcileFiles.
func (s *server) handleReplaceFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := zerolog.Ctx(ctx)

	slug := r.PathValue("slug")
	if !api.SlugRe.MatchString(slug) {
		httpError(ctx, w, http.StatusBadRequest, "invalid slug", nil)
		return
	}

	u, err := s.container.GetBySlug(ctx, slug)
	if err != nil {
		httpError(ctx, w, http.StatusInternalServerError, "get upload", err)
		return
	}
	if u == nil {
		httpError(ctx, w, http.StatusNotFound, "not found", nil)
		return
	}

	partialName, size, in, status, perr := s.streamToPartial(w, r)
	if perr != nil {
		httpError(ctx, w, status, perr.Error(), nil)
		return
	}

	oldDiskName := u.DiskName()
	newDiskName := slug + in.Extension
	uploadedAt := time.Now().UTC()
	if err := s.container.ReplaceContent(ctx, &store.Upload{
		Slug:             slug,
		Extension:        in.Extension,
		OriginalFilename: in.OriginalFilename,
		ContentType:      in.ContentType,
		Size:             size,
		UploadedAt:       uploadedAt,
		TTL:              in.TTL,
	}); err != nil {
		_ = s.container.RemoveFile(partialName)
		if errors.Is(err, store.ErrNotFound) {
			httpError(ctx, w, http.StatusNotFound, "not found", nil)
			return
		}
		httpError(ctx, w, http.StatusInternalServerError, "update row", err)
		return
	}

	if err := s.container.RenameFile(partialName, newDiskName); err != nil {
		log.Error().Err(err).
			Str("slug", slug).
			Str("partial", partialName).
			Str("new_disk_name", newDiskName).
			Msg("replace: rename failed after UPDATE committed; row + content drift")
		httpError(ctx, w, http.StatusInternalServerError, "promote partial", err)
		return
	}

	if in.Extension != u.Extension {
		if err := s.container.RemoveFile(oldDiskName); err != nil {
			log.Warn().Err(err).Str("old_disk_name", oldDiskName).
				Msg("replace: failed to remove old-extension file (orphan)")
		}
	}

	log.Info().
		Str("slug", slug).
		Str("old_disk_name", oldDiskName).
		Str("new_disk_name", newDiskName).
		Str("original_filename", in.OriginalFilename).
		Int64("size", size).
		Str("content_type", in.ContentType).
		Dur("ttl", in.TTL).
		Str("subject", subjectFromCtx(ctx)).
		Msg("upload replaced")

	writeUploadJSON(w, slug, in, size, uploadedAt)
}
