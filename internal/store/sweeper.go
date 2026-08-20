package store

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog"
)

// RunSweeper deletes expired uploads on a ticker, with one immediate sweep at boot so a restart doesn't
// extend the window where a URL still serves an expired file. Returns only on ctx.Done(); designed to run as
// a goroutine for the process lifetime.
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

// sweepOnce runs a single pass: SELECT expired, then for each row DELETE it and remove the file. Per-row
// errors are logged but don't abort the pass, so one stuck row can't block fresh expiries.
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
		// Honor cancellation between rows so a SIGTERM mid-sweep doesn't burn the shutdown drain budget on
		// context-cancelled DELETEs that all error out.
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
		// Row first, then file (see DeleteBySlug): a later sweep skips an orphaned file because it is no
		// longer in the table, so boot reconcile is what cleans it up.
		if err := c.DeleteBySlug(ctx, u.Slug); err != nil {
			switch {
			case errors.Is(err, ErrNotFound):
				rowLog.Debug().Msg("sweeper: row gone before we got to it")
				continue
			case errors.Is(err, context.Canceled):
				// Planned shutdown; the outer loop exits on the next ctx.Err() check. No WARN noise.
				rowLog.Debug().Msg("sweeper: row delete cancelled (shutdown)")
				return
			}
			rowLog.Warn().Err(err).Msg("sweeper: delete row failed")
			continue
		}
		if err := c.RemoveFile(diskName); err != nil {
			rowLog.Warn().Err(err).Msg("sweeper: remove file failed")
			// The row is gone, so it counts as deleted; next boot's ReconcileFiles sweeps the file.
		}
		rowLog.Info().Msg("sweeper: removed expired upload")
		deleted++
	}
	// INFO only when work was done, DEBUG otherwise: a default MinTTL=10m sweep would emit 144 INFO lines a
	// day of "nothing expired" noise.
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
