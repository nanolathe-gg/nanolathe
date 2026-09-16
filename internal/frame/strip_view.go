package frame

import "github.com/nanolathe-gg/nanolathe/internal/sim/numeric"

// StripFamily names the researched strip-object family one mirrored
// sub-record belongs to [03 R-STRIP-01 §1][03 R-FX-01 §3].
//
// The family is the routing key of the draw, not a cosmetic label: the four
// families do not draw alike. Geothermal steam blits without coverage admission;
// weapon smoke and both flame classes blit after the per-particle
// one-point coverage gate, and the sprinkle fills a two-by-two rectangle after
// that same gate [03 R-FX-02 §2][03 R-FX-02 §3][03 R-FX-01 §3]. A view whose
// family is StripFamilyNone names no established draw and is drawn by nothing.
type StripFamily uint8

const (
	StripFamilyNone StripFamily = iota
	// StripFamilySmokePuff is the strips-5/9 smoke puffer: impact, muzzle,
	// trail, emit-sfx, burning-feature and corpse-column smoke [03 R-FX-01 §3]
	// [06 R-WFX-01 §5].
	StripFamilySmokePuff
	// StripFamilyVentSteam is the geothermal vent's own class on strip 4. It
	// shares the puff record but skips the smoke puffer's draw coverage gate
	// [03 R-FX-01 §3][05 R-ECO-02 §3].
	StripFamilyVentSteam
	// StripFamilySprinkle is the strips-2/7 impact sprinkle, the one family
	// that fills rectangles instead of blitting its carried entry
	// [03 R-FX-01 §3].
	StripFamilySprinkle
	// StripFamilyFlame is the strip-5 flame-stream segment [03 R-FX-02 §2].
	StripFamilyFlame
	// StripFamilyFlameTrail is the strips-7/9 flame-stream trail
	// [03 R-FX-01 §3].
	StripFamilyFlameTrail
	// StripFamilyNano is the strip-6 nanolathe particle. Its authoritative
	// particle state is copied here at publication and painted at strip 6;
	// presentation does not reconstruct or advance its own emitter [03 §5.5].
	StripFamilyNano
)

// StripView is one live strip sub-record, copied at the publication boundary
// [03 R-STRIP-01 §2][I6].
//
// Retail's composer walks each strip at that strip's barrier and calls every
// stored object's draw entry, which forwards the camera origin to each of its
// sub-records; the sub-record is what acquires a screen position [03 §1]. The
// simulation owns those records (they are swept in phase 11), so presentation
// cannot read them directly: this value is the committed copy it reads
// instead.
//
// Slice order is the composer's walk order and is a contract: strips ascending
// 0..9, objects in insertion order within a strip, sub-records in vector order
// within an object [03 §1][03 R-FX-02 §1][I1].
type StripView struct {
	// Strip is the barrier this record draws at, 0..9 [03 §1].
	Strip int8
	// Family selects the per-family draw [03 R-FX-01 §3].
	Family StripFamily
	// Bank and Entry are the art identity, which is a PAIR: an entry name is
	// meaningless without the bank that holds it. The strip families' entries
	// come from the engine's own fixed effect-slot table, bound from `fx`
	// [06 R-WFX-01 §1][03 R-FX-01 §3]. An unresolvable pair draws nothing; no
	// stand-in is selected [I9].
	Bank  string
	Entry string
	// Frame is the sub-record's own animation cursor, the frame of Entry the
	// draw blits [03 R-FX-02 §2][03 R-FX-02 §3].
	Frame int32
	// Fill is the raw palette index of a filling family's two-by-two
	// rectangle — the sprinkle pair 0x61/0x67, or the nano ramp 0xa1..0xa7.
	// The byte is written raw: this family's colour is NOT passed through the
	// logical-to-physical remap [03 R-FX-01 §3]. Zero means the record blits
	// instead, since zero is none of the authored fill colours.
	Fill uint8
	// X, Y and Z are the sub-record's own 16.16 world position. The draw
	// projects them with the ordinary half-height shear [03 R-FX-01 §3].
	X, Y, Z numeric.Fixed
	// HasRemaining reports whether this sub-record has a deadline at all. A
	// particle with no expiry is IMMORTAL to the sweep, not expiring now, and
	// the two cannot share a zero: read as expiring, an immortal flame would
	// sit for ever at the bottom of its fade (DESIGN_GPU_RENDERER §31.7).
	HasRemaining bool
	// Remaining is committed ticks left before this sub-record's own expiry,
	// clamped at zero, and meaningful only when HasRemaining is set. It is presentation metadata: nothing about the retail
	// draw reads it, and the classic executor ignores it. The Enhanced ground
	// pass uses it to take a flame's light out with the flame rather than
	// switching it off with the particle (DESIGN_GPU_RENDERER §31.7).
	Remaining uint32
}
