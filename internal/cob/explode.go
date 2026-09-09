package cob

import (
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// ExplosionPieceIdentity identifies the declared COB piece whose model geometry
// a later arena resolves. It deliberately does not carry a model pointer: COB
// owns script identity while the session owns the bound unit and model [04
// R-COB-04 §1] [I13].
type ExplosionPieceIdentity struct {
	COBPiece     int
	GeometryName string
}

// ExplosionPieceState is the source record state copied at physical-spawn
// admission. Transform is the script-owned pose; RenderFlags carries the
// source render state after physical spawn has hidden its draw bit [04
// R-COB-04 §1].
type ExplosionPieceState struct {
	Transform   model.PieceState
	RenderFlags uint8
}

// ExplosionSource keeps the source's identity and mutable state distinct so a
// bounded session arena can resolve geometry without making the COB VM own it
// [04 R-COB-04 §1] [I13].
type ExplosionSource struct {
	Identity ExplosionPieceIdentity
	State    ExplosionPieceState
}

// ExplosionKinematics is the six-draw physical debris seed. Velocities are
// 16.16 world units per tick and angular rates are in the 65536-per-circle
// domain [04 R-COB-04 §1].
type ExplosionKinematics struct {
	AngularRates [3]uint16
	Velocity     [3]numeric.Fixed
	Lifetime     uint16
}

// PhysicalExplosionFlags are the engine-facing flags rebuilt from script
// explode flags. No consumer reads bits above these six [04 R-COB-04 §1].
type PhysicalExplosionFlags uint8

const (
	// FIRE and SMOKE select the established flame-stream and strip-9 smoke
	// classes. Their producer remains presentation-owned because it runs once
	// per rendered frame and consumes CRT there [04 R-COB-04 §2][03 R-FX-01 §3].
	ExplosionFire PhysicalExplosionFlags = 1 << iota
	ExplosionSmoke
	ExplosionShatter
	ExplosionFall
	ExplosionWholePieceOnHit
	ExplosionShatterOnHit
)

// PhysicalExplosion is shared by the mutually exclusive whole-piece and
// shatter requests [04 R-COB-04 §1].
type PhysicalExplosion struct {
	Source     ExplosionSource
	Flags      PhysicalExplosionFlags
	Kinematics ExplosionKinematics
}

// WholePieceExplosion asks the session's 100-slot debris arena to admit one
// copied render piece. Admission follows the six physical draws and source
// hide [04 R-COB-04 §1] [04 R-COB-04 §2].
type WholePieceExplosion struct{ PhysicalExplosion }

// ShatterExplosion asks the session effect/fragment arenas to begin the
// model-order fragment walk. The arena must decide each fragment's admission
// before that fragment's eight simulation draws [04 R-COB-04 §3].
// TODO(question): when an effect record is claimed but no fragment geometry is
// available, whether retail advances its stale animation words is unknown; do
// not infer cleanup or advancement at this boundary [04 R-COB-04 §3].
type ShatterExplosion struct{ PhysicalExplosion }

// BitmapExplosionKind names the fixed animation selected by one explode flag
// bit [04 R-COB-04 §1] [04 R-COB-04 §4].
type BitmapExplosionKind uint8

const (
	BitmapExplosionPrimary BitmapExplosionKind = iota + 1
	BitmapExplode2
	BitmapExplode3
	BitmapExplode4
	BitmapExplode5
	BitmapNuke1
)

// BitmapExplosion is one bitmap flag request. It has no simulation RNG draw
// and is independently offered in ascending script-bit order [04 R-COB-04 §1].
type BitmapExplosion struct {
	Source ExplosionSource
	Kind   BitmapExplosionKind
}

// ExplosionSink is a synchronous allocation boundary. Each method returns the
// owning arena's admission decision; callers must not retain an unbounded event
// queue. A nil sink is the explicit pending-U13 no-consumer path [04
// R-COB-04 §1]–[04 R-COB-04 §4] [I5].
type ExplosionSink interface {
	AdmitWholePiece(WholePieceExplosion) bool
	AdmitShatter(ShatterExplosion) bool
	AdmitBitmap(BitmapExplosion) bool
}

func (v *VM) explosionSource(piece int) ExplosionSource {
	source := ExplosionSource{Identity: ExplosionPieceIdentity{COBPiece: piece}}
	if v == nil {
		return source
	}
	if v.prog != nil && piece >= 0 && piece < len(v.prog.Pieces) {
		source.Identity.GeometryName = v.prog.Pieces[piece]
	}
	if piece >= 0 && piece < len(v.Pieces) {
		source.State.Transform = v.Pieces[piece]
	}
	if flags := v.renderPieceFlags(); piece >= 0 && piece < len(flags) {
		source.State.RenderFlags = flags[piece]
	}
	return source
}

func physicalExplosionFlags(scriptFlags int32) PhysicalExplosionFlags {
	var flags PhysicalExplosionFlags
	if scriptFlags&0x10 != 0 {
		flags |= ExplosionFire
	}
	if scriptFlags&0x08 != 0 {
		flags |= ExplosionSmoke
	}
	if scriptFlags&0x01 != 0 {
		flags |= ExplosionShatter
	}
	if scriptFlags&0x04 != 0 {
		flags |= ExplosionFall
	}
	if scriptFlags&0x02 != 0 {
		if flags&ExplosionShatter != 0 {
			flags |= ExplosionShatterOnHit
		} else {
			flags |= ExplosionWholePieceOnHit
		}
	}
	return flags
}

func (v *VM) physicalExplosion(scriptFlags int32) PhysicalExplosion {
	rates := [3]uint16{
		uint16(v.simRandN(3000)),
		uint16(v.simRandN(3000)),
		uint16(v.simRandN(3000)),
	}
	vx := (int32(20) - int32(v.simRandN(40))) << 14
	vy := int32(v.simRandN(10)) << 16
	vz := (int32(20) - int32(v.simRandN(40))) << 14
	// The upward draw precedes the Z draw. Keeping all six values here exposes
	// the generated physical record without changing stream order [04 R-COB-04 §1].
	return PhysicalExplosion{
		Flags: physicalExplosionFlags(scriptFlags),
		Kinematics: ExplosionKinematics{
			AngularRates: rates,
			Velocity:     [3]numeric.Fixed{numeric.Fixed(vx), numeric.Fixed(vy), numeric.Fixed(vz)},
			Lifetime:     900,
		},
	}
}
