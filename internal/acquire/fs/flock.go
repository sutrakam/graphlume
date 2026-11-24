package fs

import "errors"

// ErrLocked is returned by TryLock when the file lock is held by another process.
var ErrLocked = errors.New("file is locked by another process")
