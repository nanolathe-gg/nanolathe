package main

import (
	"fmt"
	"io"

	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/kernel"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/world"
)

// defaultRNGDumpTicks is the tick budget for --dump rng when --ticks is 0.
// Long enough to cross at least one wind change, whose deadline is 150..420
// ticks ahead [01 §7.3].
const defaultRNGDumpTicks = 600

// dumpRNG runs a headless tick loop and reports both stream states and draw
// counts. It is the falsifiable form of PLAN_03's exit gate — "two runs with
// the same seed produce identical RNG draw counts" — which had no command
// behind it.
//
// It is deliberately end-to-end rather than a stream unit test: it seeds via
// rng.SeedGlobal, drives clock.State through kernel.Kernel, and registers the
// one subsystem that draws today, world.Wind, into kernel phases 8 and 9
// [01 §4.4]. That covers the seeding path, the tick increment position (C6),
// phase order (C7), and the two-stream call order of [01 §7.3] in one command.
//
// Wind bounds are the canonical-TNT hard-coded pair 100/2000 [03 §2.2] so the
// dump does not depend on a map being resolvable.
func dumpRNG(opts Options, out io.Writer) error {
	if rng.Global.Sim == nil || rng.Global.Crt == nil {
		return fmt.Errorf("nanolathe: --dump rng: streams not seeded")
	}
	sim, crt := rng.Global.Sim, rng.Global.Crt

	ticks := opts.Ticks
	if ticks <= 0 {
		ticks = defaultRNGDumpTicks
	}

	wind := world.NewWind(100, 2000) // [03 §2.2] canonical hard-coded bounds
	wind.SeedBriefing(crt, 0)        // briefing draws happen before tick 1 [01 §7.3]

	var k kernel.Kernel
	wind.Register(&k, crt, sim)

	// The budget is clamped 0..5 per call [01 §4.2], so drive the loop in
	// bursts of at most 5 rather than asking for `ticks` at once.
	state := clock.State{Requested: 10, Active: 10}
	for state.GlobalTick < uint32(ticks) {
		remaining := int(uint32(ticks) - state.GlobalTick)
		if remaining > 5 {
			remaining = 5
		}
		k.Run(&state, remaining)
	}

	fmt.Fprintf(out, "rng ticks: %d\n", state.GlobalTick)
	fmt.Fprintf(out, "rng sim: state=%#08x draws=%d\n", sim.State, sim.Draws())
	fmt.Fprintf(out, "rng crt: state=%#08x draws=%d\n", crt.State, crt.Draws())
	fmt.Fprintf(out, "wind: strength=%d heading=%d scalar=%v changes=%d\n",
		wind.Strength, wind.Heading, wind.Scalar, wind.LastChange)
	return nil
}
