package utility

// Response curves. Every consideration is an integer in permille: 1000 is
// "neutral / fully satisfied", 0 vetoes, values above 1000 amplify. A score
// is a weight (percent) times the product of its considerations, so a
// nominal choice with every consideration at 1000 and weight 100 scores 100.

const one = 1000

// lin maps x linearly from [x0, x1] onto [0, 1000], clamped. x0 > x1 gives a
// falling ramp.
func lin(x, x0, x1 int64) int64 {
	if x0 == x1 {
		if x >= x1 {
			return one
		}
		return 0
	}
	if x0 < x1 {
		if x <= x0 {
			return 0
		}
		if x >= x1 {
			return one
		}
		return (x - x0) * one / (x1 - x0)
	}
	if x >= x0 {
		return 0
	}
	if x <= x1 {
		return one
	}
	return (x0 - x) * one / (x0 - x1)
}

// half is a hyperbolic decay: 1000 at x = 0, 500 at x = h, → 0 as x grows.
func half(x, h int64) int64 {
	if x <= 0 {
		return one
	}
	if h <= 0 {
		return 0
	}
	return h * one / (h + x)
}

// inv is the reciprocal of a permille ratio, clamped: inv(500) = 2000.
func inv(permille, lo, hi int64) int64 {
	if permille < 1 {
		permille = 1
	}
	return clamp(one*one/permille, lo, hi)
}

// mul combines considerations: a × b / 1000.
func mul(a, b int64) int64 { return a * b / one }

func clamp(v, lo, hi int64) int64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
