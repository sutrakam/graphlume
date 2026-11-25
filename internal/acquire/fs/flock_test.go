package fs

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestFileLockMutualExclusion(t *testing.T) {
	tmp := t.TempDir()
	lockPath := filepath.Join(tmp, "test.lock")

	lock1, err := TryLock(lockPath)
	if err != nil {
		t.Fatalf("TryLock 1: %v", err)
	}
	defer lock1.Unlock()

	// Second acquisition must fail with ErrLocked
	lock2, err := TryLock(lockPath)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("TryLock 2: want ErrLocked, got %v (lock: %+v)", err, lock2)
	}

	// Release lock 1
	if err := lock1.Unlock(); err != nil {
		t.Fatalf("Unlock 1: %v", err)
	}

	// Third acquisition should now succeed
	lock3, err := TryLock(lockPath)
	if err != nil {
		t.Fatalf("TryLock 3: %v", err)
	}
	defer lock3.Unlock()
}
