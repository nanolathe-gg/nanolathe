package headless

// SimBenchCPUCost reports host CPU consumption, separately from elapsed wall
// time. Unavailable measurements carry a reason and omit numeric fields.
type SimBenchCPUCost struct {
	Available         bool    `json:"available"`
	UnavailableReason string  `json:"unavailable_reason,omitempty"`
	TotalMillis       float64 `json:"total_ms,omitempty"`
	MillisPerTick     float64 `json:"ms_per_tick,omitempty"`
}

func simBenchCPUSummary(nanos int64, ticks int, err error) SimBenchCPUCost {
	if err != nil {
		return SimBenchCPUCost{UnavailableReason: err.Error()}
	}
	if nanos < 0 {
		return SimBenchCPUCost{UnavailableReason: "CPU clock moved backwards"}
	}
	if ticks <= 0 {
		return SimBenchCPUCost{UnavailableReason: "no measured ticks"}
	}
	millis := float64(nanos) / 1e6
	return SimBenchCPUCost{
		Available: true, TotalMillis: millis, MillisPerTick: millis / float64(ticks),
	}
}
