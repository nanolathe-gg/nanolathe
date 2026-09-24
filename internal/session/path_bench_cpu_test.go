//go:build pathbench && retail && unix

package session

import "golang.org/x/sys/unix"

// pbThreadCPU is the calling OS thread's CPU time in nanoseconds; the runner
// locks its goroutine to one thread while timing, so a loaded host's other
// work does not inflate the samples as wall time does.
func pbThreadCPU() int64 {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_THREAD_CPUTIME_ID, &ts); err != nil {
		return 0
	}
	return ts.Nano()
}
