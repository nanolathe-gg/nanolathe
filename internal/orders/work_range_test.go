package orders

import (
	"math"
	"math/big"
	"testing"
)

// Work ranges truncate their magnitudes [05 R-WORK-01 §2]. Exercise the
// boundary where searching by squaring a doubled candidate used to overflow.
func TestWorkRangeSquareRootBoundaries(t *testing.T) {
	for _, v := range []int64{0, 1, 2, 15, 16, 17, 1<<62 - 1, 1 << 62, 1<<62 + 1, math.MaxInt64} {
		want := new(big.Int).Sqrt(big.NewInt(v)).Int64()
		if got := isqrt64(v); got != want {
			t.Errorf("isqrt64(%d) = %d, want %d", v, got, want)
		}
	}
}

// These coordinates fit a large custom map. The range test retains the signed
// high-word conversion of [05 R-WORK-01 §2], even when that word is negative.
func TestRepairLargeSeparationCompletes(t *testing.T) {
	q, builder, target := workFixture()
	builder.X, builder.Z = 16<<16, 16<<16
	target.X, target.Z = 24016<<16, 24016<<16
	q.Push(Lookup("RepairUnit"), Node{Owner: builder.Handle, Target: target.Handle, Phase: 1})
	q.Pump(builder, 1)
	if q.LenPrimary() != 0 {
		t.Fatal("repair of a full-health target did not complete after the signed range test")
	}
}
