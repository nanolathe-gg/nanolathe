package movement

// The direct position commit — retail's "carried-position setter"
// [04 R-COLL-01 §4]. It is the occupancy commit's success branch WITHOUT the
// validator: nothing here asks whether the destination is placeable, because
// none of its callers propose a move, they announce one that has already
// happened.
//
// [04 R-COLL-01 §4]'s writer census names its callers: the commit's own carried
// branch (SyncCarriedMotion, which reaches the same clear/stamp pair inline) and
// the `Teleport` order row ([04 R-SPEC-01 §2], [04 R-ORD-01 §2]). This file is
// the entry point the second caller needs; the first keeps its inline form
// because it is already inside the commit.

import (
	"github.com/nanolathe-gg/nanolathe/internal/orders"
)

// PlaceUnit commits one live unit to a world position without the placement
// validator [04 R-COLL-01 §4].
//
// The setter, exactly as that section states it: "same cell and mode → write
// XYZ only; otherwise clear the old footprint, write XYZ, cell pair and mode,
// stamp the new footprint with the overlap protocol, and publish LOS; dirty in
// both cases."
//
//   - The cell pair is quantized from the COMMITTED position, not from a
//     proposal: [04 R-COLL-01 §4] forms the sector bucket "from the committed
//     16.16 position". CollisionState.ProposedAnchor adds the mover's velocity
//     because it is answering next tick's question; a direct commit has no
//     proposal, so the anchor here is QuantizedAnchor over the written X/Z.
//   - The clear/stamp pair is syncMoverStamp for a mover and the shared yard
//     selector for the building class — the same helpers unit creation and unit
//     finalisation use, including the clear's class-layer maintenance
//     [04 R-MOV-03 §3][04 R-PATH-01 §14]. There is no second copy of the
//     restamp here.
//   - "Publish LOS" needs no call in this build: the visibility sweep rebuilds
//     its sensor inputs from each unit's current X/Y/Z every tick (phase 5 of
//     [01 §4.4]), so writing the position IS the publication.
//
// It reports false when the owner cannot accept the request — no world, no
// unit, or a unit that is not live — which is the contract every callback on
// the order package's movement seam follows.
func (s *System) PlaceUnit(req orders.PlaceRequest) bool {
	if s == nil || s.world == nil || req.Unit == 0 {
		return false
	}
	u := s.world.Unit(req.Unit)
	if u == nil || !u.Alive {
		return false
	}
	u.X, u.Y, u.Z = req.X, req.Y, req.Z
	// "Dirty in both cases" [04 R-COLL-01 §4] — before the same-cell early
	// return below, so the fast path dirties too. This build carries the signal
	// on the unit's transform-dirty bit and on the mover record's own flag; the
	// ground post-move correction reads either [04 R-MOV-01 §5].
	u.Flags |= unitTransformDirty
	if fl := handleRow(s.Flights, req.Unit); fl != nil {
		fl.X = int32(u.X.Raw())
		fl.Y = int32(u.Y.Raw())
		fl.Z = int32(u.Z.Raw())
	}
	if st := handleRow(s.Steers, req.Unit); st != nil {
		st.X = int32(u.X.Raw())
		st.Z = int32(u.Z.Raw())
	}
	coll := handleRow(s.Collisions, req.Unit)
	if coll == nil {
		// No collision record: the unit holds no occupancy word, so the XYZ
		// write is the whole commit.
		return true
	}
	coll.X = int32(u.X.Raw())
	coll.Y = int32(u.Y.Raw())
	coll.Z = int32(u.Z.Raw())
	coll.Dirty = true
	bx, bz := coll.HalfBias()
	anchor := QuantizedAnchor(coll.X, coll.Z, bx, bz)
	mode := u.Move.Mode & 0x3
	if anchor == coll.CachedAnchor && (coll.Building || mode == coll.CachedMode) {
		return true // same cell and mode: write XYZ only [04 R-COLL-01 §4]
	}
	if coll.Building {
		// The building class selects its cells by yard byte, so its clear and
		// its stamp both run through the shared selector; the clear owes the
		// class-layer maintenance at the rectangle it actually released
		// [04 R-COLL-01 §4].
		old := coll.CachedAnchor
		s.clearBuildingGrid(old, coll.FootPrintX, coll.FootPrintZ, coll.Yard, coll.YardOpen, coll.ID)
		s.noteFootprintClear(req.Unit, old, coll.FootPrintX, coll.FootPrintZ, false)
		coll.CachedAnchor, coll.OldAnchor = anchor, anchor
		if s.stampBuildingGrid(anchor, coll.FootPrintX, coll.FootPrintZ, coll.Yard, coll.YardOpen, coll.ID) {
			s.noteOccupancyCommit(req.Unit, s.tick)
		}
		return true
	}
	// "Write XYZ, cell pair and mode", then the clear/stamp pair. The cached
	// mode moves with the cached pair for the reason the carried branch gives:
	// a stale mirror lets the next commit take the same-cell fast path and skip
	// the restamp [04 R-COLL-01 §1][04 R-FAC-02 §2].
	coll.CachedAnchor, coll.OldAnchor = anchor, anchor
	coll.Mode, coll.CachedMode = mode, mode
	s.syncMoverStamp(u)
	return true
}
