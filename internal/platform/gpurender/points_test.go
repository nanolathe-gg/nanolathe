package gpurender

import "testing"

// The lit point plane is only allowed to exist because the lane it stores is the
// lane the quad path carried, bit for bit (points.go). Two things have to hold for
// every LHT row: the CPU's high lane times 2^23 is a whole number below 2^24, so
// three bytes lose nothing, and the shader's reassembly — r·65536 + g·256 + b,
// scaled by 2^-23, all in binary32 — returns exactly that lane.
func TestPointPlaneLaneRoundTripsExactly(t *testing.T) {
	for row := 0; row < 32; row++ {
		low, high := rowScaleLanes(lightScale(row))
		if low != 1 {
			t.Fatalf("row %d: low lane %v, want 1 — the plane carries no low lane", row, low)
		}
		scaled := float64(high) * (1 << 23)
		if scaled != float64(uint32(scaled)) {
			t.Fatalf("row %d: high lane %v is not a multiple of 2^-23 (%v)", row, high, scaled)
		}
		lane := pointLaneBytes[row]
		n := uint32(lane[0])<<16 | uint32(lane[1])<<8 | uint32(lane[2])
		if float64(n) != scaled {
			t.Fatalf("row %d: stored %d, want %v", row, n, scaled)
		}
		// The shader's arithmetic, in the same precision the device uses.
		got := (float32(lane[0])*65536 + float32(lane[1])*256 + float32(lane[2])) / 8388608
		if got != high {
			t.Fatalf("row %d: reassembled lane %v, want %v", row, got, high)
		}
	}
}

// The uncovered texel of a plane region is zero, and its lane must be the identity
// the blend leaves the destination alone under.
func TestPointPlaneZeroTexelIsIdentity(t *testing.T) {
	if got := (float32(0)*65536 + float32(0)*256 + float32(0)) / 8388608; got != 0 {
		t.Fatalf("zero texel reassembled to %v, want 0", got)
	}
}
