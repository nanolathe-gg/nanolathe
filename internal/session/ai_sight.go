package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// computerViewerActive reports whether a slot can see at all: a live player
// that is not an observer. Both computer-player sight predicates start here.
func (s *Session) computerViewerActive(viewer uint8) bool {
	return s != nil && s.Econ != nil && int(viewer) < len(s.Econ.Players) &&
		s.Econ.Players[viewer].Exists && !s.Econ.Players[viewer].IsObserver
}

// computerPlayerSees is the retail planner's sight predicate, bound to the
// rally task: the canonical unit predicate for an active slot [03 §3.2] C8.
// It keeps retail's source selection unchanged, including the Permanent LOS
// read of the mapping word at the local viewing slot's bit rather than the
// computer player's [03 §3.2] C8 step 4 [08 R-AI-01 §7].
func (s *Session) computerPlayerSees(viewer uint8, target *units.Unit) bool {
	return s.computerViewerActive(viewer) && s.IsUnitVisible(int(viewer), target)
}

// computerPlayerSeesOwn is a Modern controller's sight predicate
// (ai.Manager.UnitVisible; docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI
// computer player"). It is the same ordered gate — owner bypass, cloak,
// depth, the four-probe hull [03 §3.2] C8 — read from the computer player's
// OWN coverage in both visibility modes. With line of sight on, the canonical
// predicate already samples the viewer's own byte grid. With Permanent LOS it
// samples the mapping word at the local viewing slot's bit, which would let a
// controller see what the human has explored and nothing it explored itself;
// this path tests the controller's own bit instead. It is Nanolathe Modern
// policy for the controller's fairness boundary, not retail behaviour: the
// retail planner keeps computerPlayerSees.
//
// Under Permanent LOS the Community off-map aircraft substitute
// (DESIGN_COMMUNITY_PATCH §4.4) is not applied on this path: that substitute
// is private to the visibility service and reads the local slot's bit. It is
// off unless a feature table enables it.
func (s *Session) computerPlayerSeesOwn(viewer uint8, target *units.Unit) bool {
	if !s.computerViewerActive(viewer) || s.Vis == nil || target == nil {
		return false
	}
	if s.Vis.CurrentEnabled() {
		return s.IsUnitVisible(int(viewer), target)
	}
	var seaLevel numeric.Fixed
	if s.World != nil {
		seaLevel = s.World.SeaLevelWorld()
	}
	id := visibility.PlayerID(viewer)
	mapped := s.Vis.WordMask()
	w, h := s.Vis.GridDimensions()
	bit := uint16(1) << viewer
	var t visibility.Target
	fillUnitVisibilityTarget(&t, target, target.Flags)
	return t.IsVisible(id, seaLevel, func(x, y, z numeric.Fixed) bool {
		cell, ok := visibilityCell(w, h, x, y, z)
		return ok && cell < len(mapped) && mapped[cell]&bit != 0
	})
}

// visibilityCell projects one world point onto the visibility grid the way
// the predicate's sampler does [03 §3.2] C8 step 4: each 16.16 coordinate
// narrows to its signed 16-bit map-pixel component, the point shears by half
// its height, the tile is 32 pixels, and an unsigned bounds test rejects a
// negative projection instead of indexing backwards.
func visibilityCell(w, h int32, x, y, z numeric.Fixed) (int, bool) {
	pixel := func(v numeric.Fixed) int32 { return int32(int16(int64(v) >> 16)) }
	u := int64(pixel(x) >> 5)
	v := int64((pixel(z) - (pixel(y) >> 1)) >> 5)
	if uint32(u) >= uint32(w) || uint32(v) >= uint32(h) {
		return 0, false
	}
	return int(v*int64(w) + u), true
}
