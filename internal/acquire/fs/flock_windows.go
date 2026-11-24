//go:build windows

package fs

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// FileLock represents an exclusive cross-process advisory lock.
type FileLock struct {
	file *os.File
	path string
}

// TryLock attempts to acquire an exclusive, non-blocking lock on path on Windows.
//
// LockFileEx and its flags live in golang.org/x/sys/windows, not in the
// standard syscall package: naming them through syscall would not
// compile for Windows at all, which no Unix build can notice. The
// lock covers one byte at offset 0 and, like flock, is released by the OS when
// the process exits.
func TryLock(path string) (*FileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lockfile %s: %w", path, err)
	}
	var ol windows.Overlapped
	err = windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ol)
	if err != nil {
		_ = f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return &FileLock{file: f, path: path}, nil
}

// Unlock releases the lock and closes the lockfile descriptor.
func (l *FileLock) Unlock() error {
	if l == nil || l.file == nil {
		return nil
	}
	defer l.file.Close()
	var ol windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(l.file.Fd()), 0, 1, 0, &ol)
}
