package store

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog"
)

// BootSweep runs a single synchronous sweep pass. Used at boot to
// close the "URL still serves the expired file across restart"
// window before the listener starts accepting traffic. Also the
// surface tests target so they don't have to dance around the
// RunSweeper goroutine.
func (c *Container) BootSweep(ctx context.Context) {
	log := c.log.With().Str("component", "sweeper").Logger()
	c.sweepOnce(ctx, log)
}

// RunSweeper deletes expired uploads on a ticker:
//   - one immediate sweep at boot so restart doesn't extend a "URL
//     still serves the expired file" window
//   - per-row errors logged at WARN; the sweep continues
//   - successful deletes logged at INFO with slug + filename
//
// Returns only on ctx.Done(). Designed to be run as a goroutine for
// the lifetime of the process.
func (c *Container) RunSweeper(ctx context.Context, interval time.Duration) {
	log := c.log.With().Str("component", "sweeper").Logger()
	log.Info().Dur("interval", interval).Msg("sweeper starting")
	c.sweepOnce(ctx, log)

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("sweeper stopping")
			return
		case <-t.C:
			c.sweepOnce(ctx, log)
		}
	}
}

// sweepOnce runs a single pass: SELECT expired -> for each, remove
// file + DELETE row. Per-row errors are logged but do not abort the
// pass (a stuck row should not block fresh expiries from being
// reaped).
func (c *Container) sweepOnce(ctx context.Context, log zerolog.Logger) {
	start := time.Now()
	expired, err := c.ListExpired(ctx, start)
	if err != nil {
		log.Err(err).Msg("list expired failed")
		return
	}
	if len(expired) == 0 {
		log.Debug().Dur("duration", time.Since(start)).Msg("sweep: nothing expired")
		return
	}

	deleted := 0
	for _, u := range expired {
		// Honor cancellation between rows so a SIGTERM mid-sweep
		// doesn't burn the shutdown drain budget on context-cancelled
		// DELETEs that all error out.
		if ctx.Err() != nil {
			log.Info().Int("removed_so_far", deleted).Msg("sweep: cancelled mid-iteration")
			return
		}
		diskName := u.DiskName()
		rowLog := log.With().
			Str("slug", u.Slug).
			Str("disk_name", diskName).
			Time("expired_at", u.ExpiresAt()).
			Logger()
		// DB row goes first: a crash between DELETE and os.Remove
		// leaves the file unreferenced (next sweep will skip it
		// because it's no longer in the table), which is the
		// safer failure mode than a row pointing at nothing.
		if err := c.DeleteBySlug(ctx, u.Slug); err != nil {
			switch {
			case errors.Is(err, ErrNotFound):
				rowLog.Debug().Msg("sweeper: row gone before we got to it")
				continue
			case errors.Is(err, context.Canceled):
				// Planned shutdown; the outer loop will exit on the
				// next ctx.Err() check. Suppress per-row WARN noise.
				rowLog.Debug().Msg("sweeper: row delete cancelled (shutdown)")
				return
			}
			rowLog.Warn().Err(err).Msg("sweeper: delete row failed")
			continue
		}
		if err := c.RemoveFile(diskName); err != nil {
			rowLog.Warn().Err(err).Msg("sweeper: remove file failed")
			// row is already gone; counts as deleted from the
			// DB's perspective, leave it counted -- ReconcileFiles
			// at next boot will sweep the orphan disk file.
		}
		rowLog.Info().Msg("sweeper: removed expired upload")
		deleted++
	}
	// INFO only when work was done; otherwise DEBUG. Default-deployed
	// sweep at MinTTL=10m would otherwise emit 144 INFO lines/day of
	// "nothing expired" noise.
	level := log.Debug()
	if deleted > 0 {
		level = log.Info()
	}
	level.
		Int("removed", deleted).
		Int("expired_seen", len(expired)).
		Dur("duration", time.Since(start)).
		Msg("sweep complete")
}
