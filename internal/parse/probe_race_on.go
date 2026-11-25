//go:build race

package parse

// probeScale stretches the first-parse allowance in a build with the race
// detector, which runs the parser several times slower than a normal build.
// The allowance is a wall-clock figure measured on normal builds; without the
// stretch a parse that is only slow under the detector ran out of it, and the
// verdict of a file depended on how busy the machine was while the tests ran.
const probeScale = 4

// raceEnabled reports whether this build has the race detector. Tests whose
// verdict depends on a wall-clock allowance skip under it; CI runs them
// without -race.
const raceEnabled = true
