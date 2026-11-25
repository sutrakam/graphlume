package registry

import (
	"context"

	"github.com/sutrakam/graphlume/internal/store"
)

// ReclaimFreeSpace returns the index's free pages to the file system when
// there are enough of them to be worth it (see store.Compact). Writers call it
// after a write that can free pages: a published full index, an incremental
// update, resolve. The caller holds the index's write lock.
func (r *Registry) ReclaimFreeSpace(ctx context.Context) (store.CompactResult, error) {
	return store.Compact(ctx, r.db)
}

// FreeSpace reports the index's auto-vacuum mode and its free pages.
func (r *Registry) FreeSpace(ctx context.Context) (store.FreeSpace, error) {
	return store.ReadFreeSpace(ctx, r.db)
}
