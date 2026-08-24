// Package numeric — fixed-point trig shared by all simulation code [04 §5.1].
//
// One 512-entry sine table serves all simulation trig: entry i is
// round(8192*sin(i*2π/512)), cosine reads the same table a quarter turn
// ahead (128 entries), and products round to nearest before truncation
// [04 §5.1]. It lives in numeric and is the only trig any sim package calls.
// Renderer model trig is separate floating point [03 §2.4].
package numeric

import "math"

// sineTable is the shared 512-entry word sine table scaled by 8192 [04 §5.1].
var sineTable [512]int32

func init() {
	for i := range sineTable {
		// entry i = round(8192*sin(i*2π/512)) [04 §5.1]; i*2π/512 = i*π/256.
		sineTable[i] = int32(math.Round(8192 * math.Sin(float64(i)*math.Pi/256)))
	}
}

// Sin is THE simulation sine. It reads the 512-entry table via the
// Angle→index scaling Angle*512/65536, i.e. Angle>>7 [04 §5.1].
func Sin(a Angle) int32 {
	return sineTable[(uint32(a)*512>>16)&511]
}

// Cos is THE simulation cosine. It reads the same table a quarter turn
// (128 entries) ahead of Sin [04 §5.1].
func Cos(a Angle) int32 {
	return sineTable[((uint32(a)*512>>16)+128)&511]
}

// MulRound multiplies two scaled trig values and rounds to nearest before
// truncation [04 §5.1]. The scale is 1<<13 (8192) so half is 1<<12 (4096).
// Retail adds half then arithmetic-shifts [04 §5.1] — (a*b+4096)>>13 — which
// is reproduced here. The int64 intermediate avoids overflow.
func MulRound(a, b int32) int32 {
	return int32((int64(a)*int64(b) + 4096) >> 13)
}
