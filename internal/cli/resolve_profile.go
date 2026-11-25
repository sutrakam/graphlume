package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
)

// resolveProfileEnv names a directory. When it is set, `resolve` writes a CPU
// profile (resolve-cpu.pprof), a block profile (resolve-block.pprof) and a
// mutex profile (resolve-mutex.pprof) there. It exists so a slow resolve on a
// large repository can be diagnosed without rebuilding the binary:
//
//	GRAPHLUME_RESOLVE_PROFILE=/tmp/prof graphlume resolve
//	go tool pprof -top -cum /tmp/prof/resolve-cpu.pprof
const resolveProfileEnv = "GRAPHLUME_RESOLVE_PROFILE"

// startResolveProfile begins the profiles and returns the function that stops
// them and writes the block and mutex profiles. An empty dir disables it. A
// failure to create a file is reported on stderr and never fails the command:
// profiling is a diagnostic, not a precondition.
func startResolveProfile(dir string) func() {
	if dir == "" {
		return func() {}
	}
	//nolint:gosec // the directory is named by the person running the diagnostic
	if err := os.MkdirAll(dir, 0o750); err != nil {
		fmt.Fprintf(os.Stderr, "profile: %v\n", err)
		return func() {}
	}
	var cpu *os.File
	//nolint:gosec // the directory is named by the person running the diagnostic
	if f, err := os.Create(filepath.Join(dir, "resolve-cpu.pprof")); err == nil {
		if err := pprof.StartCPUProfile(f); err == nil {
			cpu = f
		} else {
			_ = f.Close()
		}
	}
	runtime.SetBlockProfileRate(1_000_000) // one sample per millisecond blocked
	runtime.SetMutexProfileFraction(100)
	return func() {
		if cpu != nil {
			pprof.StopCPUProfile()
			_ = cpu.Close()
		}
		for _, name := range []string{"block", "mutex"} {
			//nolint:gosec // the directory is named by the person running the diagnostic
			f, err := os.Create(filepath.Join(dir, "resolve-"+name+".pprof"))
			if err != nil {
				continue
			}
			_ = pprof.Lookup(name).WriteTo(f, 0)
			_ = f.Close()
		}
	}
}
