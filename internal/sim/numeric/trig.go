// Package numeric — fixed-point trig shared by all simulation code [04 §5.1].
//
// One 512-entry sine table serves all simulation trig: entry i is
// round(8192*sin(i*2π/512)), cosine reads the same table a quarter turn
// ahead (128 entries), and products round to nearest before truncation
// [04 §5.1]. It lives in numeric and is the only trig any sim package calls.
// Renderer model trig is separate floating point [03 §2.4].
package numeric

import "math"

const angleScale = 10430.37835047

// AngleFromAtan2 converts the two signed integer operands used by retail's
// bearing helper into the 16-bit circular angle domain. The first operand is
// the X-like component and the second is the Z-like component; callers choose
// whether those operands are a self-minus-target or target-minus-self delta.
// Retail scales atan2 by the stored 65536/(2*pi) constant and narrows with the
// default round-to-nearest-even mode before wrapping to 16 bits [01 §8][04
// R-MOV-01 §2].
//
// Retail's fpatan computes the angle in the x87 stack before the stored scale
// is narrowed under the default x87 round-to-nearest-even control word [01
// §8][R-DET-01 §2]. Go's math package supplies float64 intermediates; I2
// explicitly permits this bounded transient, and the result is immediately
// narrowed to the retail angle word.
func AngleFromAtan2(first, second int64) Angle {
	value := math.Atan2(float64(first), float64(second)) * angleScale
	return Angle(uint16(int32(math.RoundToEven(value))))
}

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
// truncation [04 §5.1]. The scale is 1<<13 (8192), so rounding to nearest is
// implemented here as add-half-then-arithmetic-shift: (a*b + 4096) >> 13,
// with an int64 intermediate against overflow.
//
// TODO(question): [04 §5.1] states "products round to nearest before
// truncation" without naming the machine sequence; the add-half form is the
// standard fixed-point reading and is what ships here. When phase 6 wires the
// first real consumers (RockUnit/HitByWeapon arguments, flight brake shaping),
// confirm the negative-tie behavior matches before building on it.
func MulRound(a, b int32) int32 {
	return int32((int64(a)*int64(b) + 4096) >> 13)
}
