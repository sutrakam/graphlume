package fs

// Deciding that a file has not changed without reading it.
//
// A walk that opens, reads and hashes every file it discovers, on every pass,
// to compare the hash with the one in the index. On a tree where nothing
// changed that is the whole cost of the pass: on django (7,090 files) the walk
// is 1.1 s to 4.5 s of a re-index that then does nothing, almost all of it in
// open/read/close, and it is why an update would cost seconds however small the edit.
// Git solved the same problem the same way, and this is its rule with one
// addition.
//
// THE RULE. A file is taken as unchanged, and not read, only when ALL of these
// hold:
//
//  1. An earlier pass recorded it, under the same build, with a content hash.
//  2. Its size is the recorded size.
//  3. Its modification time is the recorded modification time, to the second.
//  4. Its modification time is at least RacyWindowSeconds older than the
//     moment the earlier pass BEGAN reading files (Trust.VerifiedUnix).
//  5. Where the platform reports an inode change time (Linux, macOS), that is
//     also at least RacyWindowSeconds older than that moment.
//
// Anything else is read and hashed, and the hash decides. The hash is the
// tie-breaker in both directions: a file whose time changed and whose content
// did not (a branch switch and back, a touch) is hashed, found identical and
// not re-parsed.
//
// WHY 4. Sizes and whole-second times cannot tell two same-size edits made in
// one second apart, and many filesystems record no finer time. So a recorded
// time is only believed when it is settled: the earlier pass read the file at
// or after VerifiedUnix, and any later write stamps a time at or after that
// read. If the recorded time is safely before VerifiedUnix, a later write
// cannot have left the time unchanged. If it is not (the file was written in
// the seconds before or during the pass), the file is hashed again, however
// many times it takes until a pass begins comfortably after its last write.
// This is git's "racily clean" entry. The window is two seconds because FAT
// stores times in two-second steps.
//
// WHY 5. Rule 4 assumes a write moves the modification time forward. A tool
// that restores it (cp -p, rsync -t, tar x, touch -r, a build that normalises
// timestamps) breaks that, and git's rule misses a same-size edit made that
// way. The inode change time cannot be set by a program: the kernel stamps it
// on every write and every metadata change, including the call that put the
// old modification time back. A file changed in any way since the earlier pass
// began has a change time after it, and is hashed.
//
// WHAT THIS DOES NOT COVER, stated because it is the cost of not reading:
//
//   - Windows reports no change time through a directory listing, so rule 5 is
//     skipped there. A same-size edit whose modification time was deliberately
//     restored to the second is missed on Windows.
//   - A clock stepped backwards by more than the window between two passes,
//     or a network filesystem whose server clock is more than the window
//     behind this machine, can stamp a new write with an old time. It still
//     needs a same-size edit landing on the recorded second to be missed.
//
// `graphlume index --rehash` reads and hashes everything, for those cases and
// for anyone who would rather not take the rule on trust.

// RacyWindowSeconds is how much older than the start of the earlier pass a
// file's times must be before they are believed. See rule 4 above.
const RacyWindowSeconds = 2

// Known is what an earlier pass recorded about one file.
type Known struct {
	SizeBytes   int64
	ModTimeUnix int64

	// ContentHash is the hash the earlier pass computed. Empty when that pass
	// skipped the file, in which case SkipReason says why.
	ContentHash string
	SkipReason  string
}

// Trust is the evidence a walk needs to leave unchanged files unread. The zero
// value trusts nothing, which is a walk that hashes every file.
type Trust struct {
	// Files maps a repo-relative slash path to what the earlier pass recorded.
	Files map[string]Known

	// VerifiedUnix is when the earlier pass began reading files, in Unix
	// seconds by the wall clock. Every record in Files was read at or after
	// it. Zero trusts nothing.
	VerifiedUnix int64
}

// settled reports whether a time is old enough to be believed.
func (t *Trust) settled(unix int64) bool {
	return unix <= t.VerifiedUnix-RacyWindowSeconds
}

// unchanged applies the rule at the top of this file to one file's current
// state. changeUnix is the inode change time and hasChange says whether the
// platform reported one.
func (t *Trust) unchanged(rel string, size, modUnix, changeUnix int64, hasChange bool) (Known, bool) {
	if t == nil || t.VerifiedUnix <= 0 {
		return Known{}, false
	}
	k, ok := t.Files[rel]
	if !ok {
		return Known{}, false
	}
	// Only two recorded states can be carried forward: a file that was hashed,
	// and a file that was sniffed as binary. Every other skip (unreadable, a
	// size limit) is a state worth looking at again.
	if k.ContentHash == "" && k.SkipReason != SkipBinary {
		return Known{}, false
	}
	if k.SizeBytes != size || k.ModTimeUnix != modUnix {
		return Known{}, false
	}
	if !t.settled(modUnix) {
		return Known{}, false
	}
	if hasChange && !t.settled(changeUnix) {
		return Known{}, false
	}
	return k, true
}
