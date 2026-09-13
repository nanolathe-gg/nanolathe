package visibility

import "github.com/nanolathe-gg/nanolathe/internal/sim/numeric"

// TargetFromBounds translates a unit position and its signed definition bounds
// into the first hull probe and full spans. The first corner is minimum X,
// maximum Y, minimum Z; span subtraction retains signed 32-bit arithmetic
// before widening to Fixed [06 §3.1][03 §3.2].
func TargetFromBounds(t Target, min, max [3]int32) Target {
	t.X += numeric.Fixed(min[0])
	t.Y += numeric.Fixed(max[1])
	t.Z += numeric.Fixed(min[2])
	t.XExtent = numeric.Fixed(max[0] - min[0])
	t.YExtent = numeric.Fixed(max[1] - min[1])
	t.ZExtent = numeric.Fixed(max[2] - min[2])
	return t
}
