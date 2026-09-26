//go:build !darwin && !linux

package headless

import "errors"

var errSimBenchCPUUnavailable = errors.New("CPU clocks are supported only on Darwin and Linux")

func simBenchProcessCPU() (int64, error) { return 0, errSimBenchCPUUnavailable }
func simBenchThreadCPU() (int64, error)  { return 0, errSimBenchCPUUnavailable }
