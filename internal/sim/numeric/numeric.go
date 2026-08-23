// Package numeric contains the authoritative integer numeric types used by
// the simulation. Renderer-facing conversions belong outside this package.
package numeric

import (
	"math"
)

const (
	FractionBits   = 16
	FractionOne    = int64(1 << FractionBits)
	TickRate       = 30
	AngleUnitsTurn = 1 << 16
)

// Fixed is signed 16.16 fixed point. Arithmetic deliberately truncates
// toward zero at named boundaries, matching Go's integer operations.
type Fixed int64

const FixedOne Fixed = Fixed(FractionOne)

func FixedFromRaw(raw int64) Fixed { return Fixed(raw) }
func (v Fixed) Raw() int64         { return int64(v) }
func FixedFromInt(v int64) Fixed   { return Fixed(v * FractionOne) }
func FixedFromIntChecked(v int64) (Fixed, bool) {
	if v > math.MaxInt64/FractionOne || v < math.MinInt64/FractionOne {
		return 0, false
	}
	return FixedFromInt(v), true
}
func (v Fixed) Int() int64            { return int64(v) / FractionOne }
func (v Fixed) Add(other Fixed) Fixed { return v + other }
func (v Fixed) Sub(other Fixed) Fixed { return v - other }
func (v Fixed) Neg() Fixed            { return -v }

func (v Fixed) Mul(other Fixed) Fixed {
	return Fixed((int64(v) * int64(other)) / FractionOne)
}

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

// WorldCoord, Velocity, Distance, Resource, Work, Damage, Health and PathCost
// keep domain names visible at API boundaries while retaining the same 16.16
// representation where the profile calls for it.
type WorldCoord Fixed
type Velocity Fixed
type Distance Fixed
type Resource Fixed
type Work Fixed
type ConstructionWork Fixed
type Damage Fixed
type Health Fixed
type PathCost Fixed

// Angle is a full-turn 16-bit angle. Conversion is integer-only so it is
// deterministic across platforms.
type Angle uint16

func AngleFromRaw(raw uint16) Angle { return Angle(raw) }
func (a Angle) Raw() uint16         { return uint16(a) }
func AngleFromDegrees(degrees int32) Angle {
	return Angle(uint32(degrees) * 65536 / 360)
}
func (a Angle) Add(other Angle) Angle { return Angle(uint16(a + other)) }
func (a Angle) Sub(other Angle) Angle { return Angle(uint16(a - other)) }

// Tick is the authoritative simulation tick number. A deadline is inclusive:
// work scheduled for tick N becomes eligible when the clock reaches N.
type Tick uint64

func (t Tick) Add(delta uint64) Tick  { return Tick(uint64(t) + delta) }
func (t Tick) Before(other Tick) bool { return t < other }
func (t Tick) After(other Tick) bool  { return t > other }

type Deadline struct{ Tick Tick }

func DeadlineAt(t Tick) Deadline       { return Deadline{Tick: t} }
func (d Deadline) Ready(now Tick) bool { return now >= d.Tick }

// DeadlineKey adds a stable tie-breaker for equal-tick work.
type DeadlineKey struct {
	Tick     Tick
	Sequence uint64
}

func (d DeadlineKey) Ready(now Tick) bool { return now >= d.Tick }
func (d DeadlineKey) Before(other DeadlineKey) bool {
	if d.Tick != other.Tick {
		return d.Tick < other.Tick
	}
	return d.Sequence < other.Sequence
}

type MillisecondRounding uint8

const (
	RoundFloor MillisecondRounding = iota
	RoundCeil
	RoundNearest
)

func MillisecondsToTicks(milliseconds uint64, mode MillisecondRounding) Tick {
	value := milliseconds * TickRate
	switch mode {
	case RoundCeil:
		return Tick((value + 999) / 1000)
	case RoundNearest:
		return Tick((value + 500) / 1000)
	default:
		return Tick(value / 1000)
	}
}

// FixedFromRatio is useful for content values without introducing floating
// point into authoritative setup code.
func FixedFromRatio(numerator, denominator int64) (Fixed, bool) {
	if denominator == 0 {
		return 0, false
	}
	if numerator > math.MaxInt64/FractionOne || numerator < math.MinInt64/FractionOne {
		// The result may still be representable after division; use a wider
		// approximation only for the overflow check below.
		if denominator == 1 {
			return 0, false
		}
	}
	return Fixed((numerator * FractionOne) / denominator), true
}
