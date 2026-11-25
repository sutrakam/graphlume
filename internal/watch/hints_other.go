//go:build !darwin

package watch

import (
	"fmt"
	"time"

	"github.com/fsnotify/fsnotify"
)

// newDefaultBackend is the backend a Watcher uses when a test has not supplied
// one. Away from macOS that is fsnotify: inotify and ReadDirectoryChangesW
// both report a write to a file through a watch on its directory, so they hold
// one watch per directory and open no file, which is what scan.go exists to
// achieve where the kernel does not offer it.
func newDefaultBackend(time.Duration) (backend, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("watch: create backend: %w", err)
	}
	return fsnotifyBackend{w: fsw}, nil
}
