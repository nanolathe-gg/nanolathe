package render

// The calculated-explosion table geometry of [06 R-WFX-01 §2].
//
// Three tables are generated once per battle. Their frame counts, sides and
// holds are researched constants rather than asset data, so they live here
// where the producer (which publishes the secondary cursor's timing) and the
// composer (which generates the pixels) can both read one definition.
const (
	// FlashTableCount is the number of generated tables.
	FlashTableCount = 3
	// FlashFrameHold is every calculated frame's hold, in whole ticks. Table 0
	// therefore plays for 24 ticks and tables 1 and 2 for 30.
	FlashFrameHold int32 = 2
)

// FlashTableSides returns one table's frame sides in play order:
//
//	table 0 — 12 frames, 64 down to 20 in steps of 4  (every ordinary impact)
//	table 1 — 15 frames, 128 down to 30 in steps of 7 (built, drawn by nothing)
//	table 2 — 15 frames, 200 down to 46 in steps of 11 (the `explode` opcode)
//
// Table 1 is included because "nothing passes table 1" is a finding: the strip
// is built and costs its CRT draws, and a census that quietly omitted it would
// lose that.
func FlashTableSides(table int) []int {
	switch table {
	case 0:
		out := make([]int, 0, 12)
		for n := 64; n >= 20; n -= 4 {
			out = append(out, n)
		}
		return out
	case 1:
		out := make([]int, 0, 15)
		for n := 128; n >= 30; n -= 7 {
			out = append(out, n)
		}
		return out
	case 2:
		out := make([]int, 0, 15)
		for n := 200; n >= 46; n -= 11 {
			out = append(out, n)
		}
		return out
	}
	return nil
}

// FlashFrameDurations is one table's authored per-frame timing: every frame
// holds FlashFrameHold ticks, and no calculated table loops.
func FlashFrameDurations(table int) []int32 {
	sides := FlashTableSides(table)
	if len(sides) == 0 {
		return nil
	}
	out := make([]int32, len(sides))
	for i := range out {
		out[i] = FlashFrameHold
	}
	return out
}
