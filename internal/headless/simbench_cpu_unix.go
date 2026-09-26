//go:build darwin || linux

package headless

import "golang.org/x/sys/unix"

// The process clock includes concurrent GC and profiler workers; the thread
// clock measures only the OS thread to which the benchmark host is pinned.
// Neither clock is visible to the simulation [I6].
func simBenchProcessCPU() (int64, error) {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_PROCESS_CPUTIME_ID, &ts); err != nil {
		return 0, err
	}
	return ts.Nano(), nil
}

func simBenchThreadCPU() (int64, error) {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_THREAD_CPUTIME_ID, &ts); err != nil {
		return 0, err
	}
	return ts.Nano(), nil
}
