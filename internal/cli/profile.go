package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
)

// profileDirEnv names a directory that `graphlume index` writes Go profiles
// into when the variable is set: cpu.prof, block.prof and mutex.prof. It exists
// so a slow index can be explained from evidence, not guessed at. Unset (the
// normal case) it costs nothing and changes nothing.
const profileDirEnv = "GRAPHLUME_PROFILE_DIR"

// startProfiling begins CPU, block and mutex profiling when profileDirEnv is
// set. The returned function stops them and writes the files; it is always safe
// to call. Failures are ignored on purpose: profiling is a diagnostic and must
// never fail an index.
func startProfiling() func() {
	dir := os.Getenv(profileDirEnv)
	if dir == "" {
		return func() {}
	}
	if err := os.MkdirAll(dir, 0o750); err != nil { //nolint:gosec // diagnostic output path chosen by the person running the command
		return func() {}
	}
	cpu, err := os.Create(filepath.Join(dir, "cpu.prof")) //nolint:gosec // diagnostic output path chosen by the person running the command
	if err == nil {
		if err := pprof.StartCPUProfile(cpu); err != nil {
			_ = cpu.Close()
			cpu = nil
		}
	}
	runtime.SetBlockProfileRate(1000) // one sample per microsecond blocked
	runtime.SetMutexProfileFraction(5)
	return func() {
		if cpu != nil {
			pprof.StopCPUProfile()
			_ = cpu.Close()
		}
		for _, name := range []string{"block", "mutex"} {
			f, err := os.Create(filepath.Join(dir, name+".prof")) //nolint:gosec // diagnostic output path chosen by the person running the command
			if err != nil {
				continue
			}
			_ = pprof.Lookup(name).WriteTo(f, 0)
			_ = f.Close()
		}
	}
}
