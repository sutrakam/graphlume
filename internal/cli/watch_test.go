package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// lockedBuffer is a bytes.Buffer a command can write while a test reads it.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// `graphlume watch` from start to stop: an edit that the index has not seen
// is reconciled before the banner, the banner says how many directories are
// watched, a later edit is picked up, and cancellation ends it promptly.
//
// The first of those is the one at risk. Start-up that reconciles and then
// registers its watches loses a file saved in between, which is in neither; the
// reconcile comes after the registration.
func TestWatchReconcilesThenWatchesThenStops(t *testing.T) {
	root := queryFixture(t)
	target := filepath.Join(root, "svc", "unrelated.go")
	appendTo := func(text string) {
		t.Helper()
		f, err := os.OpenFile(target, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // test fixture inside t.TempDir
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		if _, err := f.WriteString(text); err != nil {
			t.Fatal(err)
		}
	}
	appendTo("\nfunc SavedBeforeWatching() {}\n")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &lockedBuffer{}
	cmd := NewRoot()
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--project", root, "watch", "--debounce", "20ms"})
	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()

	waitFor := func(what string, within time.Duration) {
		t.Helper()
		deadline := time.Now().Add(within)
		for !strings.Contains(out.String(), what) {
			if time.Now().After(deadline) {
				t.Fatalf("no %q within %s; output so far:\n%s", what, within, out.String())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	waitFor("Watching ", 60*time.Second)
	banner := out.String()
	if !strings.Contains(banner, "directories") || !strings.Contains(banner, "ready in") {
		t.Errorf("the banner does not say how many directories are watched or how long start-up took:\n%s", banner)
	}
	// By the time the banner is printed the earlier edit is in the index.
	if got, err := run(t, root, "symbol", "SavedBeforeWatching"); err != nil || !strings.Contains(got, "SavedBeforeWatching") {
		t.Errorf("an edit made before watching began was not reconciled by the banner: %v\n%s", err, got)
	}

	// An edit while watching. In place, which is the kind macOS finds by
	// sweeping rather than from the kernel.
	// The update is logged, not printed, so the index is what is watched for
	// it.
	appendTo("\nfunc SavedWhileWatching() {}\n")
	deadline := time.Now().Add(60 * time.Second)
	for {
		got, err := run(t, root, "symbol", "SavedWhileWatching")
		if err == nil && strings.Contains(got, "SavedWhileWatching") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("an edit made while watching was not indexed within 60 s: %v\n%s", err, got)
		}
		time.Sleep(50 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("watch ended with an error on cancellation: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("watch did not stop within 30 s of cancellation")
	}
	if !strings.Contains(out.String(), "Stopped.") {
		t.Errorf("watch did not say it stopped:\n%s", out.String())
	}
}

// Cancelled before it has begun watching, `watch` says so and exits cleanly.
func TestWatchInterruptedDuringStartUp(t *testing.T) {
	root := queryFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := &lockedBuffer{}
	cmd := NewRoot()
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--project", root, "watch"})
	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("watch did not stop within 30 s of a cancellation made before it started")
	}
	if strings.Contains(out.String(), "Watching ") {
		t.Errorf("a cancelled start printed the watching banner:\n%s", out.String())
	}
}
