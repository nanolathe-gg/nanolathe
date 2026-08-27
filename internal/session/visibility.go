package session

import (
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/visibility"
)

// heightByteAt derives the observer emitter from the sea-level-clamped world
// height and the immutable model-top extent [03 §3.2, §3.5].
func heightByteAt(u *units.Unit, seaLevel uint8) uint8 {
	if u == nil {
		return 0
	}
	y := u.Y
	floor := numeric.Fixed(int64(seaLevel)+1) << 16
	if y < floor {
		y = floor
	}
	h := int32(int16(int64(y) >> 16))
	if u.Def != nil {
		h += u.Def.ModelTop
	}
	if h < 0 {
		h = 0
	} else if h > 255 {
		h = 255
	}
	return uint8(h)
}

// observerTile applies the half-height beam shear before converting to the
// 32-pixel coverage grid [03 §3.2, §3.5].
func observerTile(u *units.Unit, emitter uint8) (cx, cz int32) {
	if u == nil {
		return 0, 0
	}
	px := int32(int16(int64(u.X) >> 16))
	pz := int32(int16(int64(u.Z)>>16)) - int32(emitter)/2
	return px >> 5, pz >> 5
}

func radiusFor(u *units.Unit) int32 {
	if u != nil && u.Def != nil && u.Def.SightDistance > 0 {
		return int32(u.Def.SightDistance)
	}
	return 32
}

// publishOne synchronously refreshes one unit's stored observer footprint.
func publishOne(s *Session, u *units.Unit) {
	if s == nil || s.Vis == nil || u == nil || !u.Alive {
		return
	}
	hb := heightByteAt(u, seaLevelFor(s))
	cx, cz := observerTile(u, hb)
	s.Vis.Refresh(visibility.ObserverID(u.Handle), visibility.Observer{
		Owner: visibility.PlayerID(u.Owner), CX: cx, CZ: cz,
		HeightByte: hb, Radius: radiusFor(u),
	})
}

// unpublishOne retires the stored footprint. Reconstructing from the unit's
// current position is incorrect after movement or an owner transfer [03 §3.2].
func unpublishOne(s *Session, u *units.Unit) {
	if s == nil || s.Vis == nil || u == nil {
		return
	}
	s.Vis.RetireObserver(visibility.ObserverID(u.Handle))
	if s.visStatus != nil {
		delete(s.visStatus, int(u.Handle))
	}
	if s.visDecloak != nil {
		delete(s.visDecloak, int(u.Handle))
	}
}

// publishVisibilityForAll publishes all live units before the first frame.
func publishVisibilityForAll(s *Session) {
	if s == nil || s.Vis == nil || s.Units == nil {
		return
	}
	for _, u := range s.Units.IterSliced() {
		if u != nil && u.Alive {
			publishOne(s, u)
		}
	}
}
