//go:build linux

package fs

import (
	iofs "io/fs"
	"syscall"
)

// changeTimeUnix returns the inode change time, which the kernel stamps on
// every write and metadata change and no program can set. See trust.go.
func changeTimeUnix(info iofs.FileInfo) (int64, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return 0, false
	}
	// The field is int32 on 32-bit targets and int64 elsewhere; the conversion
	// is a no-op where it is not needed.
	return int64(st.Ctim.Sec), true //nolint:unconvert // differs by GOARCH
}
