//go:build pathbench && retail && !unix

package session

// pbThreadCPU reports no thread CPU time where the clock is unavailable; the
// runner's wall-clock samples remain.
func pbThreadCPU() int64 { return 0 }
