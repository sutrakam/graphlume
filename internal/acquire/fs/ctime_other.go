//go:build !linux && !darwin

package fs

import iofs "io/fs"

// changeTimeUnix reports that this platform gives no inode change time
// through a directory listing. Windows keeps one, but reading it needs a
// handle on the file, and opening every file is the cost this exists to
// avoid. The walk then relies on size and modification time alone; trust.go
// states what that leaves uncovered.
func changeTimeUnix(iofs.FileInfo) (int64, bool) { return 0, false }
