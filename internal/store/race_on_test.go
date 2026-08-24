//go:build race

package store

// raceEnabled reports that this binary was built with the race detector.
//
// The detector instruments every memory access, and modernc's SQLite is C
// translated into Go, so it makes millions of them per transaction. Measured
// throughput under -race is a measurement of the detector, not of the store:
// the same burst that runs at 15,000 events a second normally runs at 550
// under it. The load tests therefore say so and skip rather than fail, while
// every correctness test in this package still runs under the detector.
const raceEnabled = true
