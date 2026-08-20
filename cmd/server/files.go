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
		out = append(out, toAPI(u))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// handleDeleteFile deletes the row first, then the file; store.DeleteBySlug documents why that order.
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

// handleReplaceFile implements PUT /api/files/{slug}. Slug is the only stable identity across replacements,
// so the public URL never changes; the on-disk extension follows the new filename, TTL resets from
// X-KShare-TTL (or DEFAULT_TTL), and content-type is re-sniffed from the new bytes.
//
// Atomicity contract:
//  1. Stream new content to a nonce-named .partial. The live file (old content) is untouched.
//  2. UPDATE the DB row. On failure, remove the .partial; old state intact.
//  3. After UPDATE commits, rename .partial -> <slug><newExt>.
//  4. If newExt != oldExt, best-effort remove the old <slug><oldExt>.
//
// Crash recovery: a .partial left from steps 1-2 is reaped at boot, and a committed file with an
// UPDATE-but-no-rename gap is recovered by ReconcileFiles.
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
	updated := &store.Upload{
		Slug:             slug,
		Extension:        in.Extension,
		OriginalFilename: in.OriginalFilename,
		ContentType:      in.ContentType,
		Size:             size,
		UploadedAt:       time.Now().UTC(),
		TTL:              in.TTL,
	}
	if err := s.container.ReplaceContent(ctx, updated); err != nil {
		_ = s.container.RemoveFile(partialName)
		if errors.Is(err, store.ErrNotFound) {
			httpError(ctx, w, http.StatusNotFound, "not found", nil)
			return
		}
		httpError(ctx, w, http.StatusInternalServerError, "update row", err)
		return
	}

	if err := s.container.RenameFile(partialName, updated.DiskName()); err != nil {
		log.Error().Err(err).
			Str("slug", slug).
			Str("partial", partialName).
			Str("new_disk_name", updated.DiskName()).
			Msg("replace: rename failed after UPDATE committed; row + content drift")
		httpError(ctx, w, http.StatusInternalServerError, "promote partial", err)
		return
	}

	if updated.Extension != u.Extension {
		if err := s.container.RemoveFile(oldDiskName); err != nil {
			log.Warn().Err(err).Str("old_disk_name", oldDiskName).
				Msg("replace: failed to remove old-extension file (orphan)")
		}
	}

	log.Info().
		Str("slug", updated.Slug).
		Str("old_disk_name", oldDiskName).
		Str("new_disk_name", updated.DiskName()).
		Str("original_filename", updated.OriginalFilename).
		Int64("size", updated.Size).
		Str("content_type", updated.ContentType).
		Dur("ttl", updated.TTL).
		Str("subject", subjectFromCtx(ctx)).
		Msg("upload replaced")

	writeUploadJSON(w, updated)
}
