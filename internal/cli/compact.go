package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/sutrakam/graphlume/internal/registry"
)

// compactPolicy decides whether a write gives the index's free pages back to
// the file system afterwards, and where a failure to do so is reported.
type compactPolicy struct {
	enabled bool // index.auto_compact
	log     *slog.Logger
}

func (g *Global) compactPolicy() compactPolicy {
	return compactPolicy{enabled: g.Config().AutoCompact, log: g.Log()}
}

// after is the one step every writer takes once its write is in: the
// published full index (runIndex), the incremental update `index` and `watch`
// share (refreshIndex), and resolve. It returns the bytes reclaimed, zero when
// there was too little free space to bother, when the index was made without
// auto_vacuum, or when index.auto_compact is false.
//
// A failure never fails the write that came before it: the index is correct
// either way, and the space is reclaimed after the next write.
func (p compactPolicy) after(ctx context.Context, reg *registry.Registry) int64 {
	if !p.enabled {
		return 0
	}
	res, err := reg.ReclaimFreeSpace(ctx)
	if err != nil {
		if p.log != nil {
			p.log.Warn("could not reclaim free space in the index; it is retried after the next write", "error", err)
		}
		return 0
	}
	if res.ReclaimedBytes > 0 && p.log != nil {
		p.log.Debug("reclaimed free space in the index", "bytes", res.ReclaimedBytes, "checkpoint", res.Checkpoint)
	}
	return res.ReclaimedBytes
}

// writeReclaimed prints the one line index and resolve add when they gave
// space back, and nothing when they did not.
func writeReclaimed(out io.Writer, indent string, reclaimed int64) {
	if reclaimed > 0 {
		_, _ = fmt.Fprintf(out, "%sReclaimed %s of free space in the index.\n", indent, humanBytes(reclaimed))
	}
}
