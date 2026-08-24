// Package numeric contains the authoritative integer numeric types used by the
// simulation. Renderer-facing conversions belong outside this package.
//
// Two rounding rules coexist here and they are not interchangeable (I3):
//
//   - Narrowing a floating-point value to an integer truncates toward zero,
//     because retail routes those through the compiler's __ftol helper, which
//     sets round-control to truncate for the store [01 §8].
//   - Shifting a fixed-point intermediate down floors, because retail does it
//     with an arithmetic shift. The two disagree on every negative value with a
//     nonzero fraction, which is the map's west and north edges [03 §2.1].
package numeric

const (
	// FractionBits is the fixed-point scale: world coordinates are 16.16, so
	// one map pixel is 65,536 world units [03 §2.1].
	FractionBits = 16
	FractionOne  = int64(1 << FractionBits)

	// AngleUnitsTurn is a full turn. Angles are uint16, 65,536 per circle
	// [04 §5.1].
	AngleUnitsTurn = 1 << 16
)

// Fixed is signed 16.16 fixed point, backed by int64.
type Fixed int64

const FixedOne Fixed = Fixed(FractionOne)

func FixedFromRaw(raw int64) Fixed { return Fixed(raw) }
func (v Fixed) Raw() int64         { return int64(v) }
func FixedFromInt(v int64) Fixed   { return Fixed(v * FractionOne) }

// Int narrows to whole units, truncating toward zero — the __ftol rule
// [01 §8], I3. This is NOT the conversion to use for a cell or tile index:
// those floor, and world.WorldToCell owns them [03 §2.1].
func (v Fixed) Int() int64 { return int64(v) / FractionOne }

// Floor narrows to whole units rounding down, for callers that need the
// arithmetic-shift behaviour rather than __ftol truncation [03 §2.1], I3.
func (v Fixed) Floor() int64 { return int64(v) >> FractionBits }

func (v Fixed) Add(other Fixed) Fixed { return v + other }
func (v Fixed) Sub(other Fixed) Fixed { return v - other }
func (v Fixed) Neg() Fixed            { return -v }

// Mul multiplies two 16.16 values. The product is formed at full width and
// shifted down arithmetically, so it FLOORS rather than truncating toward zero.
//
// TODO(question): research does not state the rounding of a fixed-by-fixed
// multiply directly. The shift is the mechanism it does describe — [03 §2.1]
// has the coordinate hierarchy using "signed, floor-like shifts", and [01 §8]
// scopes truncation to the __ftol path, which is float-to-integer and not this.
// An x86 fixed multiply is imul into edx:eax followed by a shift of the 64-bit
// product, which floors; Go's `/ FractionOne` would truncate toward zero and
// disagree on every negative product with a fraction. If a probe ever shows
// otherwise, this is the one line to change.
func (v Fixed) Mul(other Fixed) Fixed {
	return Fixed((int64(v) * int64(other)) >> FractionBits)
}

// Div divides two 16.16 values, truncating toward zero.
//
// Unlike Mul this is a divide, not a shift: an x86 idiv truncates toward zero,
// and Go's `/` matches it. The asymmetry with Mul is real and is the hardware's,
// not ours. Division by zero reports false rather than faulting — retail's
// unguarded divides are reproduced where they are established (I11), but no
// established contract routes through this helper yet.
func (v Fixed) Div(other Fixed) (Fixed, bool) {
	if other == 0 {
		return 0, false
	}
	return Fixed((int64(v) * FractionOne) / int64(other)), true
}

func (v Fixed) Clamp(minimum, maximum Fixed) Fixed {
	if v < minimum {
		return minimum
	}
	if v > maximum {
		return maximum
	}
	return v
}

// Angle is a full-turn 16-bit angle, 65,536 per circle [04 §5.1]. Conversion is
// integer-only so it is deterministic across platforms.
type Angle uint16

func AngleFromRaw(raw uint16) Angle   { return Angle(raw) }
func (a Angle) Raw() uint16           { return uint16(a) }
func (a Angle) Add(other Angle) Angle { return Angle(uint16(a + other)) }
func (a Angle) Sub(other Angle) Angle { return Angle(uint16(a - other)) }
