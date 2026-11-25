//go:build unix

package fs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// FileLock represents an exclusive cross-process advisory lock.
type FileLock struct {
	file *os.File
	path string
}

// TryLock attempts to acquire an exclusive, non-blocking lock on path.
// If another process holds the lock, it returns ErrLocked.
func TryLock(path string) (*FileLock, error) {
	//nolint:gosec // path is the lockfile path explicitly requested by caller
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lockfile %s: %w", path, err)
	}
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("flock %s: %w", path, err)
	}
	return &FileLock{file: f, path: path}, nil
}

// Unlock releases the lock and closes the lockfile descriptor.
func (l *FileLock) Unlock() error {
	if l == nil || l.file == nil {
		return nil
	}
	defer func() { _ = l.file.Close() }()
	return syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
}
