// Fixed-point trig shared by all simulation code [04 §5.1].
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

// Sin is THE simulation sine. It reads the 512-entry table at
// ((angle + 32) >> 7) & 511 [04 §5.1][06 §3.3]: retail adds 32 before its
// shift-and-mask (the residue of masking a 64-unit index down to even
// entries), so the entry boundaries sit a quarter step early — angle 96 reads
// entry 1, angle 95 entry 0. The pre-add was missing here until 2026-09-02
// (RWU-19-36); every velocity built through this table moved by up to one
// entry for angles in the top quarter of each step.
func Sin(a Angle) int32 {
	return sineTable[((uint32(a)+indexPreAdd)>>7)&511]
}

// indexPreAdd is the 32 angle units retail adds before the table index shift;
// cosPreAdd is the same plus a quarter turn (0x4000) [04 §5.1][06 §3.3].
const (
	indexPreAdd uint32 = 32
	cosPreAdd   uint32 = 0x4000 + indexPreAdd
)

// Cos is THE simulation cosine. It reads the same table a quarter turn
// (128 entries) ahead of Sin — retail's helper adds the quarter turn to the
// same pre-add before indexing [04 §5.1][06 §3.3].
func Cos(a Angle) int32 {
	return sineTable[((uint32(a)+cosPreAdd)>>7)&511]
}

// MulRound is THE simulation trig component: it multiplies a table entry by an
// unscaled magnitude, adds half the 8192 scale, and shifts the sum down 13 bits
// arithmetically — `(a*b + 0x1000) >> 13`, with an int64 intermediate against
// overflow. The shift FLOORS, so a negative product rounds toward negative
// infinity after the half is added and a tie rounds up; it is not symmetric
// round-to-nearest and it never truncates toward zero.
//
// The machine sequence is named, not inferred (marker retired 2026-09-04,
// WU-19-155; it previously said [04 §5.1] states the rounding "without naming
// the machine sequence" and asked for the negative-tie behavior to be confirmed
// before real consumers were built on it). [04 R-MOV-01 §4] writes it out:
// `component(angle, magnitude) = (table[((angle + 0x20) >> 7) & 0x1ff] *
// magnitude + 0x1000) >> 13`, "as a 64-bit product with an arithmetic shift".
// [04 §5.3] sharpens the same pair of shared component routines from the
// callback side — signed 16-bit entry times signed 32-bit magnitude at full
// 64-bit width, `0x1000` added to the sum, low word of an arithmetic right
// shift by 13, no divide and no float-to-integer conversion in either body.
// internal/cob.trigScalar is the same sequence at the RockUnit/HitByWeapon
// call sites and is locked by TestTrigScalarFloorsAfterAddingHalf.
func MulRound(a, b int32) int32 {
	return int32((int64(a)*int64(b) + 4096) >> 13)
}
