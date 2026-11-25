package fs

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// The rule in trust.go, one clause per row. verified is when the earlier pass
// began; a time is "settled" when it is at least RacyWindowSeconds before it.
func TestTheTrustRule(t *testing.T) {
	t.Parallel()

	const verified = 1_000_000
	const settled = verified - RacyWindowSeconds
	known := Known{SizeBytes: 40, ModTimeUnix: settled, ContentHash: "abc"}

	cases := []struct {
		name      string
		trust     *Trust
		size      int64
		mod       int64
		change    int64
		hasChange bool
		want      bool
	}{
		{"same size, same settled times", &Trust{Files: map[string]Known{"a": known}, VerifiedUnix: verified},
			40, settled, settled, true, true},
		{"no change time on this platform", &Trust{Files: map[string]Known{"a": known}, VerifiedUnix: verified},
			40, settled, 0, false, true},

		{"size differs", &Trust{Files: map[string]Known{"a": known}, VerifiedUnix: verified},
			41, settled, settled, true, false},
		{"modification time differs", &Trust{Files: map[string]Known{"a": known}, VerifiedUnix: verified},
			40, settled - 1, settled, true, false},
		{"not recorded by the earlier pass", &Trust{Files: map[string]Known{"b": known}, VerifiedUnix: verified},
			40, settled, settled, true, false},

		// Rule 4. The recorded time is inside the racy window: a second
		// same-size write in that second would leave everything equal.
		{"modification time one second short of settled",
			&Trust{Files: map[string]Known{"a": {SizeBytes: 40, ModTimeUnix: settled + 1, ContentHash: "abc"}}, VerifiedUnix: verified},
			40, settled + 1, settled, true, false},
		{"modification time in the same second the pass began",
			&Trust{Files: map[string]Known{"a": {SizeBytes: 40, ModTimeUnix: verified, ContentHash: "abc"}}, VerifiedUnix: verified},
			40, verified, settled, true, false},
		{"modification time in the future",
			&Trust{Files: map[string]Known{"a": {SizeBytes: 40, ModTimeUnix: verified + 3600, ContentHash: "abc"}}, VerifiedUnix: verified},
			40, verified + 3600, settled, true, false},

		// Rule 5. Size and modification time say nothing happened; the inode
		// says something did, after the earlier pass began.
		{"inode changed after the earlier pass began", &Trust{Files: map[string]Known{"a": known}, VerifiedUnix: verified},
			40, settled, verified + 5, true, false},
		{"inode changed inside the racy window", &Trust{Files: map[string]Known{"a": known}, VerifiedUnix: verified},
			40, settled, settled + 1, true, false},

		{"no earlier pass time", &Trust{Files: map[string]Known{"a": known}},
			40, settled, settled, true, false},
		{"nil trust", nil, 40, settled, settled, true, false},

		// A skipped file is carried forward only when it was sniffed as
		// binary. Any other skip is looked at again.
		{"recorded as binary",
			&Trust{Files: map[string]Known{"a": {SizeBytes: 40, ModTimeUnix: settled, SkipReason: SkipBinary}}, VerifiedUnix: verified},
			40, settled, settled, true, true},
		{"recorded as unreadable",
			&Trust{Files: map[string]Known{"a": {SizeBytes: 40, ModTimeUnix: settled, SkipReason: SkipUnreadable}}, VerifiedUnix: verified},
			40, settled, settled, true, false},
	}
	for _, tc := range cases {
		if _, got := tc.trust.unchanged("a", tc.size, tc.mod, tc.change, tc.hasChange); got != tc.want {
			t.Errorf("%s: trusted = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// trustFrom records a walk the way the index does, as of a pass that began at
// verified.
func trustFrom(ds []Discovered, verified int64) *Trust {
	tr := &Trust{Files: map[string]Known{}, VerifiedUnix: verified}
	for _, d := range ds {
		tr.Files[d.Path] = Known{
			SizeBytes: d.SizeBytes, ModTimeUnix: d.ModTimeUnix,
			ContentHash: d.ContentHash, SkipReason: d.SkipReason,
		}
	}
	return tr
}

func byPath(ds []Discovered) map[string]Discovered {
	out := make(map[string]Discovered, len(ds))
	for _, d := range ds {
		out[d.Path] = d
	}
	return out
}

// The walk itself, on a real filesystem, through real time.
//
// The files are written, a pass is taken to have begun three seconds later,
// and the clock is allowed to reach that pass before anything is edited. That
// wait is the test: an inode change time cannot be backdated, so the only
// honest way to produce "changed after the earlier pass began" is to change it
// after the earlier pass began.
func TestWalkTrustingReadsOnlyWhatMayHaveChanged(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"kept.go":     "package a\n\nfunc Kept() {}\n",
		"edited.go":   "package a\n\nfunc One() {}\n",
		"restored.go": "package a\n\nfunc Was() {}\n",
		"touched.go":  "package a\n\nfunc Same() {}\n",
		"blob.bin":    "\x00\x01\x02binary",
	})
	created := time.Now()

	w, err := NewWalker(root, minimalCfg())
	if err != nil {
		t.Fatal(err)
	}
	first, err := w.Walk(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range first {
		if d.Unread {
			t.Fatalf("%s: a walk with no trust left a file unread", d.Path)
		}
	}

	// The earlier pass began three seconds after the files were written, so
	// their times are settled with a second to spare.
	verified := created.Unix() + RacyWindowSeconds + 1
	trust := trustFrom(first, verified)
	// A hash no file has. A file returned with it was provably not read.
	const planted = "0000000000000000000000000000000000000000000000000000000000000000"
	k := trust.Files["kept.go"]
	k.ContentHash = planted
	trust.Files["kept.go"] = k

	second, err := w.WalkTrusting(context.Background(), trust)
	if err != nil {
		t.Fatal(err)
	}
	got := byPath(second)
	if d := got["kept.go"]; !d.Unread || d.ContentHash != planted {
		t.Errorf("kept.go: unread %v, hash %q; a file with settled, matching times must not be opened", d.Unread, d.ContentHash)
	}
	if d := got["blob.bin"]; !d.Unread || !d.Skipped || d.SkipReason != SkipBinary {
		t.Errorf("blob.bin: %+v; a file recorded as binary must stay skipped without being sniffed again", d)
	}

	// Now let the clock reach the earlier pass, and edit.
	time.Sleep(time.Until(time.Unix(verified, 0)) + 50*time.Millisecond)

	before := byPath(first)
	stamp := func(rel string) time.Time { return time.Unix(before[rel].ModTimeUnix, 0) }

	// edited.go: a same-size edit. Its modification time moves, as it does
	// for any ordinary save.
	writeTree(t, root, map[string]string{"edited.go": "package a\n\nfunc Two() {}\n"})

	// restored.go: a same-size edit whose modification time is then put back
	// to the recorded second, as cp -p or rsync -t would. Size and time are
	// exactly what the index holds.
	writeTree(t, root, map[string]string{"restored.go": "package a\n\nfunc Now() {}\n"})
	if err := os.Chtimes(filepath.Join(root, "restored.go"), stamp("restored.go"), stamp("restored.go")); err != nil {
		t.Fatal(err)
	}

	// touched.go: the same bytes, a new time.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(root, "touched.go"), later, later); err != nil {
		t.Fatal(err)
	}

	third, err := w.WalkTrusting(context.Background(), trust)
	if err != nil {
		t.Fatal(err)
	}
	got = byPath(third)

	if d := got["edited.go"]; d.Unread || d.ContentHash == before["edited.go"].ContentHash {
		t.Errorf("edited.go: unread %v; a same-size edit was missed", d.Unread)
	}
	if d := got["touched.go"]; d.Unread || d.ContentHash != before["touched.go"].ContentHash {
		t.Errorf("touched.go: unread %v, hash changed %v; a touched file must be read and found identical",
			d.Unread, d.ContentHash != before["touched.go"].ContentHash)
	}
	if d := got["kept.go"]; !d.Unread {
		t.Error("kept.go: an untouched file was read again")
	}

	d := got["restored.go"]
	if runtime.GOOS == "windows" {
		// No inode change time is available without opening the file, so this
		// is the one edit the rule does not see there. trust.go says so, and
		// this assertion keeps that statement true rather than hopeful.
		if !d.Unread {
			t.Log("restored.go was read on Windows: the documented gap is narrower than stated")
		}
		return
	}
	if d.Unread || d.ContentHash == before["restored.go"].ContentHash {
		t.Errorf("restored.go: unread %v; a same-size edit with its modification time restored was missed, "+
			"which the inode change time exists to catch", d.Unread)
	}
}

// A file written in the seconds before the pass that recorded it is hashed
// again, however exactly its size and time match: that is the window in which
// a second same-size write would be invisible.
func TestWalkTrustingRereadsAFileInsideTheRacyWindow(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTree(t, root, map[string]string{"a.go": "package a\n\nfunc One() {}\n"})
	w, err := NewWalker(root, minimalCfg())
	if err != nil {
		t.Fatal(err)
	}
	first, err := w.Walk(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// The pass began in the same second the file was written.
	trust := trustFrom(first, first[0].ModTimeUnix)

	// Same size, and forced back onto the same second.
	writeTree(t, root, map[string]string{"a.go": "package a\n\nfunc Two() {}\n"})
	same := time.Unix(first[0].ModTimeUnix, 0)
	if err := os.Chtimes(filepath.Join(root, "a.go"), same, same); err != nil {
		t.Fatal(err)
	}

	second, err := w.WalkTrusting(context.Background(), trust)
	if err != nil {
		t.Fatal(err)
	}
	if d := second[0]; d.Unread || d.ContentHash == first[0].ContentHash {
		t.Errorf("a same-size edit in the same second as the pass was missed: unread %v", d.Unread)
	}
}
