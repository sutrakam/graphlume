//go:build darwin

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
	return st.Ctimespec.Sec, true
}
