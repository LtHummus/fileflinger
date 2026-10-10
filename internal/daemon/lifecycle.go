package daemon

import (
	"context"
	"log/slog"
	"time"

	"github.com/lthummus/fileflinger/internal/handles"
	"github.com/lthummus/fileflinger/internal/spool"
	"github.com/lthummus/fileflinger/internal/store"
)

const reapInterval = time.Minute

// backing owns whatever keeps a share's bytes alive: a held fd for ephemeral
// shares, a spooled copy for persistent ones, [some third thing for the comedy rule of threes]
type backing struct {
	handles *handles.Handles
	spool   *spool.Spool // nil when persistent shares are disabled
	log     *slog.Logger

	exhaustedGracePeriod time.Duration
}

// release frees what backs a share that has already been removed from the
// store. Readers that already hold a dup'd fd will keep working until they finish.
func (b *backing) release(sh *store.Share) {
	switch {
	case sh.Path == "":
		b.handles.Remove(sh.Code)
	case b.spool != nil && b.spool.IsOwnedByUs(sh.Path):
		if err := b.spool.Remove(sh.Path); err != nil {
			b.log.Warn("could not remove spooled file", "code", sh.Code, "path", sh.Path, "error", err)
		}
	default:
		// points at a file outside our spool (spool directory changed?); leave it alone
	}
}

func (b *backing) reap(ctx context.Context, st store.Store) {
	t := time.NewTicker(reapInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			b.log.Debug("running reap routine")
			expired, err := st.DeleteExpired(ctx, now)
			if err != nil {
				b.log.Warn("could not reap expired shares", "error", err)
				continue
			}
			for _, sh := range expired {
				b.release(sh)
			}
			if len(expired) > 0 {
				b.log.Info("reaped expired shares", "count", len(expired))
			}

			exhausted, err := st.DeleteExhausted(ctx, now.Add(-b.exhaustedGracePeriod))
			if err != nil {
				b.log.Warn("could not reap exhausted shares", "error", err)
				continue
			}
			for _, sh := range exhausted {
				b.release(sh)
			}
			if len(exhausted) > 0 {
				b.log.Info("reaped exhausted shares", "count", len(exhausted))
			}
		}
	}
}

// reconcile brings the store and spool back in line after a restart. Any FD-backed shares no longer
// exist, spooled things are fine. Whatever we have, line it all up
func (b *backing) reconcile(ctx context.Context, st store.Store) error {
	b.log.Info("running reconciliation process")
	expired, err := st.DeleteExpired(ctx, time.Now())
	if err != nil {
		return err
	}
	for _, sh := range expired {
		b.release(sh)
	}
	if len(expired) > 0 {
		b.log.Info("removed shares that expired while down", "count", len(expired))
	}

	exhausted, err := st.DeleteExhausted(ctx, time.Now().Add(-b.exhaustedGracePeriod))
	if err != nil {
		return err
	}
	for _, sh := range exhausted {
		b.release(sh)
	}
	if len(exhausted) > 0 {
		b.log.Info("removed shares that ran out of downloads", "count", len(exhausted))
	}

	shares, err := st.List(ctx, true)
	if err != nil {
		return err
	}

	referenced := make(map[string]struct{}, len(shares))
	for _, sh := range shares {
		switch {
		case sh.Path == "":
			// the fd died with the previous process
			if _, err := st.Delete(ctx, sh.Code); err != nil {
				return err
			}
			b.log.Info("dropped non-persistent share from previous run", "code", sh.Code, "filename", sh.Filename)
		case b.spool != nil && b.spool.IsOwnedByUs(sh.Path):
			if !b.spool.Exists(sh.Path) {
				if _, err := st.Delete(ctx, sh.Code); err != nil {
					return err
				}
				b.log.Warn("dropped persistent share whose spooled file is missing", "code", sh.Code, "path", sh.Path)
				continue
			}
			referenced[sh.Path] = struct{}{}
		}
	}

	if b.spool == nil {
		return nil
	}

	files, unexpected, err := b.spool.Files()
	if err != nil {
		return err
	}
	for _, f := range unexpected {
		b.log.Warn("unexpected entry in spool directory, leaving it alone", "path", f)
	}
	for _, f := range files {
		if _, ok := referenced[f]; ok {
			continue
		}
		if err := b.spool.Remove(f); err != nil {
			b.log.Warn("could not remove orphaned spool file", "path", f, "error", err)
			continue
		}
		b.log.Info("removed orphaned spool file", "path", f)
	}

	return nil
}
