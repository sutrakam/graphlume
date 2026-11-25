//go:build !race

package parse

// probeScale is 1 in a normal build: the first-parse allowance is used as
// measured. See probe_race_on.go.
const probeScale = 1

// raceEnabled reports whether this build has the race detector. Tests whose
// verdict depends on a wall-clock allowance skip under it; CI runs them
// without -race.
const raceEnabled = false
