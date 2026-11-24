//go:build !race

package parse

// probeScale is 1 in a normal build: the first-parse allowance is used as
// measured. See probe_race_on.go.
const probeScale = 1
