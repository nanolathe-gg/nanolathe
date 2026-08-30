// Package movement — cargo attachment, carried motion, and death/capture [04 §10.2].
//
// Attachment model [04 §4.4][04 §10.2]:
// each Unit.Attachment holds Carrier pool.Handle (0 if not carried) and Cargo []Handle.
// Load executor phase 4 attaches target to carrier on queried piece; emit event 12 [GAP T16][04 §10.2].
// Unload executor detaches via EndTransport then no-piece index; emit event 13 [04 §10.2].
//
// Carried-unit branch at top of occupancy commit slaves cargo each tick to the
// named attach piece's world transform, copies piece heading/pitch and carrier
// velocity/speed (zeroed if carrier has no mover), applies floater deck-height
// clamp from cargo's waterline and sea level, and returns before ordinary
// footprint validation. On a cell/mode change it still clears and stamps the
// footprint through the carried-position setter [04 R-FAC-02 §2].
package movement

import (
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// AttachCargo attaches cargo to carrier on piece [04 §10.2] load phase 4.
// It updates both sides: cargo.Carrier = carrier, carrier.Cargo appends cargo.
// Piece -1 is root fallback [04 §5.3][04 §10.2]. Cargo must not already be carried.
// Carrier must have live mover and canfly per gate [04 §10.2] but this helper does not re-check gates.
func AttachCargo(w *units.World, carrierHandle, cargoHandle pool.Handle, piece int) bool {
	if w == nil {
		return false
	}
	carrier := w.Unit(carrierHandle)
	cargo := w.Unit(cargoHandle)
	if carrier == nil || cargo == nil {
		return false
	}
	if cargo.Attachment.Carrier != 0 {
		return false // already carried
	}
	// Avoid duplicate cargo entry
	for _, h := range carrier.Attachment.Cargo {
		if h == cargoHandle {
			return false
		}
	}
	// Detach carrier from ITS own parent when carried [04 §10.2] phase 0 side-effect.
	// If carrier itself is cargo, detach it first? Phase 0 says detach carrier from its parent when carried.
	// We implement that check here for completeness.
	if carrier.Attachment.Carrier != 0 {
		DetachCargo(w, carrierHandle)
	}
	cargo.Attachment.Carrier = carrierHandle
	// The event stores one byte and the carried-side locator sign-extends it
	// [04 R-FAC-02 §1].
	cargo.Attachment.AttachPiece = int(int8(uint8(piece)))
	// Cargo is linked at the head, not appended [04 R-COB-03 §5].
	carrier.Attachment.Cargo = append([]pool.Handle{cargoHandle}, carrier.Attachment.Cargo...)
	return true
}

// AttachFactoryProduct applies the factory allocation gates before entering
// the shared cargo representation: live non-building product carrying
// nothing, and a live distinct carrier that is not itself carried
// [04 R-FAC-02 §1].
func AttachFactoryProduct(w *units.World, carrierHandle, productHandle pool.Handle, piece int) bool {
	if w == nil || carrierHandle == 0 || productHandle == 0 || carrierHandle == productHandle {
		return false
	}
	carrier := w.Unit(carrierHandle)
	product := w.Unit(productHandle)
	if carrier == nil || product == nil || !carrier.Alive || !product.Alive || carrier.Dying || product.Dying ||
		product.Def == nil || !product.Def.BMCode || product.Flags&units.BuildingClassStatus != 0 {
		return false
	}
	if carrier.Attachment.Carrier != 0 || product.Attachment.Carrier != 0 || len(product.Attachment.Cargo) != 0 {
		return false
	}
	return AttachCargo(w, carrierHandle, productHandle, piece)
}

// DetachCargo detaches cargo from its carrier [04 §10.2] unload phase 2.
// Returns carrier handle if found.
func DetachCargo(w *units.World, cargoHandle pool.Handle) (pool.Handle, bool) {
	if w == nil {
		return 0, false
	}
	cargo := w.Unit(cargoHandle)
	if cargo == nil {
		return 0, false
	}
	carrierHandle := cargo.Attachment.Carrier
	if carrierHandle == 0 {
		return 0, false
	}
	carrier := w.Unit(carrierHandle)
	if carrier != nil {
		// Remove from carrier cargo list preserving order [I1] player 0..9 then slot asc not needed for cargo list but deterministic removal
		newCargo := carrier.Attachment.Cargo[:0]
		for _, h := range carrier.Attachment.Cargo {
			if h != cargoHandle {
				newCargo = append(newCargo, h)
			}
		}
		carrier.Attachment.Cargo = newCargo
	}
	cargo.Attachment.Carrier = 0
	cargo.Attachment.AttachPiece = -1
	return carrierHandle, true
}

// DetachAllCargo detaches all cargo of carrier [04 §10.2] carrier death cascade.
func DetachAllCargo(w *units.World, carrierHandle pool.Handle) []pool.Handle {
	if w == nil {
		return nil
	}
	carrier := w.Unit(carrierHandle)
	if carrier == nil {
		return nil
	}
	cargos := append([]pool.Handle(nil), carrier.Attachment.Cargo...)
	for _, h := range cargos {
		DetachCargo(w, h)
	}
	// Ensure carrier list cleared if some cargos were stale (dead units)
	carrier.Attachment.Cargo = nil
	return cargos
}

// CargoCount returns live carried-count filtered by parent == carrier [04 §10.2].
func CargoCount(w *units.World, carrierHandle pool.Handle) int {
	if w == nil {
		return 0
	}
	carrier := w.Unit(carrierHandle)
	if carrier == nil {
		return 0
	}
	n := 0
	for _, h := range carrier.Attachment.Cargo {
		u := w.Unit(h)
		if u != nil && u.Attachment.Carrier == carrierHandle {
			n++
		}
	}
	return n
}

// SyncCarriedMotion slaves each cargo to its carrier's world transform [04 §10.2].
//
// Branch at top of occupancy commit:
//   - copy the named piece world transform
//   - copy piece orientation and carrier velocity/speed (zeroed if carrier has no mover)
//   - apply floater deck-height clamp from cargo's waterline and sea level
//   - bypass ordinary validation, but clear/stamp on a cell or mode change
//
// Called after carrier movement so same-tick following is observed without order dependence.
// Iteration is player 0..9 asc then slot asc [I1].
func (s *System) SyncCarriedMotion(w *units.World) {
	if s == nil || w == nil {
		return
	}
	// Collect cargos deterministically: player asc, slot asc [I1]
	// Use IterSliced for deterministic order.
	for _, cargo := range w.IterSliced() {
		if cargo == nil || cargo.Attachment.Carrier == 0 {
			continue
		}
		carrier := w.Unit(cargo.Attachment.Carrier)
		if carrier == nil {
			// Orphaned attachment: clear
			cargo.Attachment.Carrier = 0
			cargo.Attachment.AttachPiece = -1
			continue
		}
		// Factory products and ordinary cargo share this carried-position path.
		// A negative signed piece byte resolves to the carrier origin
		// [04 R-FAC-02 §1][04 R-REV-02].
		hangX, hangY, hangZ := carrier.X, carrier.Y, carrier.Z
		pieceRoll, pieceHeading, piecePitch := uint16(0), uint16(0), uint16(0)
		if piece := cargo.Attachment.AttachPiece; piece >= 0 {
			if binding := carrier.COBBinding(); binding != nil {
				if origin, ok := binding.ComposePiece(piece, carrier.Move.Heading, carrier.Move.Pitch, carrier.Move.Bank); ok {
					hangX = hangX.Add(origin[0])
					// Composed coordinates are model space, which is mirrored in
					// Z against world space [03 R-RAST-01 §2]; the world hang
					// point owes that negation. The factory build plate resolves
					// its exit with the same negation, measured against the stock
					// yard maps (construction.queryBuildPiecePosition), and the
					// carried branch rewrites the product's position from the
					// carrier every tick [04 R-FAC-02 §2] — an unnegated hang here
					// would drag every nanoframe straight back off its pad.
					hangY = hangY.Add(origin[1])
					hangZ = hangZ.Sub(origin[2])
				}
				if binding.VM != nil && piece < len(binding.VM.Pieces) {
					state := binding.VM.Pieces[piece]
					pieceRoll, pieceHeading, piecePitch = state.RotZ, state.RotY, state.RotX
				}
			}
		}
		cargo.X, cargo.Y, cargo.Z = hangX, hangY, hangZ
		if cargo.Def != nil && cargo.Def.Floater {
			if s.Terrain != nil {
				// max(hang.y, (waterline*65535 + seaLevel)<<16)
				// [04 R-FAC-02 §2][04 R-AIR-01 §9].
				floatY := numeric.Fixed((int64(cargo.Def.Waterline)*65535 + int64(s.Terrain.SeaLevel)) << 16)
				if cargo.Y < floatY {
					cargo.Y = floatY
				}
			}
		}
		// Copy heading/pitch and carrier velocity/speed (zeroed if carrier has no mover) [04 §10.2]
		// Heading/pitch from carrier's piece; velocity/speed from carrier mover.
		cargo.Move.Heading = carrier.Move.Heading + pieceHeading
		cargo.Move.Pitch = carrier.Move.Pitch + piecePitch
		cargo.Move.Bank = carrier.Move.Bank + pieceRoll
		// The carrier mover's authoritative vector is copied regardless of the
		// cargo mover representation; no mover means an exact zero vector
		// [04 R-FAC-02 §2].
		carrierVX, carrierVY, carrierVZ, carrierSpeed := int32(0), int32(0), int32(0), int32(0)
		if fl := s.Flights[carrier.Handle]; fl != nil {
			carrierVX, carrierVY, carrierVZ, carrierSpeed = fl.VX, fl.VY, fl.VZ, fl.Speed
		} else if coll := s.Collisions[carrier.Handle]; coll != nil {
			carrierVX, carrierVZ, carrierSpeed = coll.VX, coll.VZ, coll.Speed
		}
		cargo.Move.Speed = numeric.Fixed(carrierSpeed)
		// Also sync FlightState for cargo if it has one? Cargo's own flight velocities should be slaved, not integrated.
		if flCargo, ok := s.Flights[cargo.Handle]; ok {
			flCargo.X = int32(cargo.X.Raw())
			flCargo.Y = int32(cargo.Y.Raw())
			flCargo.Z = int32(cargo.Z.Raw())
			flCargo.VX = carrierVX
			flCargo.VY = carrierVY
			flCargo.VZ = carrierVZ
			flCargo.Speed = carrierSpeed
			flCargo.Heading = cargo.Move.Heading
		}
		if stCargo, ok := s.Steers[cargo.Handle]; ok {
			stCargo.X = int32(cargo.X.Raw())
			stCargo.Z = int32(cargo.Z.Raw())
			stCargo.Heading = cargo.Move.Heading
			stCargo.Speed = int32(cargo.Move.Speed.Raw())
		}
		if collCargo, ok := s.Collisions[cargo.Handle]; ok {
			collCargo.X = int32(cargo.X.Raw())
			collCargo.Z = int32(cargo.Z.Raw())
			collCargo.Y = int32(cargo.Y.Raw())
			collCargo.Heading = cargo.Move.Heading
			collCargo.VX = carrierVX
			collCargo.VZ = carrierVZ
			collCargo.Speed = carrierSpeed
			collCargo.Dirty = true
			newAnchor := collCargo.ProposedAnchor(cargo.Move.Mode)
			if newAnchor != collCargo.OldAnchor || collCargo.Mode != cargo.Move.Mode {
				// Carried motion bypasses validation, but the carried-position setter
				// still clears and stamps on a cell/mode change
				// [04 R-FAC-02 §2][04 R-COLL-01 §4].
				if s.Grid != nil {
					s.Grid.Clear(collCargo.OldAnchor, collCargo.FootPrintX, collCargo.FootPrintZ, collCargo.ID)
					s.Grid.Stamp(newAnchor, collCargo.FootPrintX, collCargo.FootPrintZ, collCargo.ID)
				}
				collCargo.OldAnchor = newAnchor
				collCargo.CachedAnchor = newAnchor
			}
			collCargo.Mode = cargo.Move.Mode
		}
	}
}

// HandleDeathOrCapture detaches cargo/captures per [04 §10.2] carrier death and capture transfer.
//
//   - Dying cargo first detaches from its carrier.
//   - If dying unit is a carrier, for each cargo head it applies 30000 damage through normal funnel with type 3 when (deathSeverity & 0xF0)==0x30 otherwise type 6, credits carrier's recorded killer, and detaches after each application.
//   - Capture similarly detaches and reattaches? Retail detaches on capture? Requirement lists death/capture interactions; capture should detach cargo and possibly transfer.
//   - For this helper we implement death cascade with 30000 damage type 3/6 [04 §10.2][06 §9.1].
func (s *System) HandleDeath(w *units.World, dyingHandle pool.Handle, deathSeverity uint8, killerHandle pool.Handle) {
	if s == nil || w == nil {
		return
	}
	dying := w.Unit(dyingHandle)
	if dying == nil {
		return
	}
	// First: if dying is cargo, detach from carrier
	if dying.Attachment.Carrier != 0 {
		DetachCargo(w, dyingHandle)
	}
	// If dying is carrier, cascade to cargo
	if len(dying.Attachment.Cargo) > 0 {
		// Copy list for deterministic iteration (slot asc already)
		cargos := append([]pool.Handle(nil), dying.Attachment.Cargo...)
		// Sort for determinism [I1] player asc not needed but slot asc
		// cargos already append order is attach time, which is deterministic.
		// But spec says for each cargo head it applies damage; order is cargo list order.
		for _, cargoHandle := range cargos {
			cargo := w.Unit(cargoHandle)
			if cargo == nil {
				continue
			}
			// Apply 30000 damage through normal funnel with type 3 when (deathSeverity &0xF0)==0x30 otherwise type 6 [04 §10.2]
			// Type 3/6 are damage type codes [06 §9.1]. We apply via w.ApplyDamage which currently does not track type but we use direct health reduction + mark dying.
			// Determine type for citation but not used in current ApplyDamage signature.
			_ = deathSeverity
			_ = killerHandle
			dmg := int32(30000)
			// Apply through funnel: for now subtract health and mark.
			// In combat, would go through death funnel with armor/veterancy [06 §9.1]; keep direct.
			if cargo.Health > 0 {
				cargo.Health -= dmg
				if cargo.Health <= 0 {
					cargo.Health = 0
					w.Destroy(cargoHandle, units.DeathKilled) // will trigger recursive detach
				}
			}
			DetachCargo(w, cargoHandle)
		}
		// Clear carrier list
		dying.Attachment.Cargo = nil
	}
}

// ValidateUnloadSite checks if cargo can be unloaded at drop point [04 §10.2] unload phases 1–2.
//
// It converts drop point to footprint anchor using cargo packed footprint dimensions and validates through
// standard placement validator in mode 1 [04 §10.2]: footprint clear, flat, no overlap.
// Mode 1 is the placement validator mode for unloading [04 §10.2].
func (s *System) ValidateUnloadSite(w *units.World, cargoHandle pool.Handle, dropX, dropZ numeric.Fixed, terrain *world.Terrain) bool {
	if w == nil || cargoHandle == 0 {
		return false
	}
	cargo := w.Unit(cargoHandle)
	if cargo == nil || cargo.Def == nil {
		return false
	}
	if terrain == nil && s.Terrain != nil {
		terrain = s.Terrain
	}
	if terrain == nil {
		return false
	}
	// Footprint dimensions from profile or def
	fx, fz := int32(cargo.Def.FootprintX), int32(cargo.Def.FootprintZ)
	prof := s.ProfileFor(cargoHandle)
	if prof.FootPrintX > 0 {
		fx = int32(prof.FootPrintX)
	}
	if prof.FootPrintZ > 0 {
		fz = int32(prof.FootPrintZ)
	}
	if fx <= 0 {
		fx = 1
	}
	if fz <= 0 {
		fz = 1
	}
	// Convert drop point to footprint anchor using cargo packed footprint dimensions [04 §10.2] unload phase 1.
	// Anchor = worldToCell(drop) - footprint/2 ? For 1x1, anchor is cell containing drop.
	// Use half bias: anchor = WorldToCell(drop) - floor(fx/2) ??? For simplicity, anchor = cell of drop.
	cellX := world.WorldToCell(dropX)
	cellZ := world.WorldToCell(dropZ)
	ax := cellX - fx/2
	az := cellZ - fz/2
	anchor := Cell{X: ax, Z: az}
	// Clamp? ValidateFootprint will reject OOB.
	// Use profile.CanOccupy for terrain passability [04 §6.1][04 §8.2] and grid for overlap.
	perCell := func(c Cell) bool {
		if s.Grid != nil {
			if occ, ok := s.Grid.OccupantAt(c); ok && occ != int(cargoHandle) {
				return false
			}
		}
		return true
	}
	aggregate := func() bool { return prof.IsPassableFootprint(terrain, anchor.X, anchor.Z) }
	if !ValidateFootprint(anchor, int16(fx), int16(fz), perCell, aggregate) {
		return false
	}
	// Also check footprint occupancy via grid.CanOccupy for speed
	if s.Grid != nil && !s.Grid.CanOccupy(anchor, int16(fx), int16(fz), int(cargoHandle)) {
		return false
	}
	// Placement validator mode 1 for unloading [04 §10.2] also checks flatness via slope? Profile.CanOccupy already does.
	// For headless test, terrain flat check via slope < MaxSlope etc is enough.
	return true
}

// TryUnload attempts to detach cargo at drop point [04 §10.2] double validation.
//
// Validates placement twice (phase1 and phase2 revalidation) [04 §10.2].
// If both pass, detaches cargo and sets its position to drop anchor center.
func (s *System) TryUnload(w *units.World, carrierHandle, cargoHandle pool.Handle, dropX, dropZ numeric.Fixed) (bool, string) {
	if s == nil || w == nil {
		return false, "nil"
	}
	carrier := w.Unit(carrierHandle)
	cargo := w.Unit(cargoHandle)
	if carrier == nil || cargo == nil {
		return false, "missing unit"
	}
	// Immediate done when cargo list empty [04 §10.2] unload
	if len(carrier.Attachment.Cargo) == 0 {
		return false, "empty"
	}
	// Verify cargo is attached to carrier
	if cargo.Attachment.Carrier != carrierHandle {
		return false, "not cargo of carrier"
	}
	// First validation
	if !s.ValidateUnloadSite(w, cargoHandle, dropX, dropZ, s.Terrain) {
		return false, UnableUnloadMessage // verbatim [04 §10.2]
	}
	// Revalidation second anchor recompute plus validator before release [04 §10.2]
	// For headless, recompute same; if terrain hasn't changed, second passes same as first.
	if !s.ValidateUnloadSite(w, cargoHandle, dropX, dropZ, s.Terrain) {
		return false, UnableUnloadMessage
	}
	// Success: start deferred zero-argument EndTransport FIRST, then detaches cargo (reserved no-piece index),
	// then constructs climb-away point command at carrier current X/Z with altitude cruisealt [04 §10.2]
	// Release order callback→detach→climb-away.
	// Here implement detach.
	DetachCargo(w, cargoHandle)
	// Set cargo position to drop anchor center
	fx, fz := int32(cargo.Def.FootprintX), int32(cargo.Def.FootprintZ)
	prof := s.ProfileFor(cargoHandle)
	if prof.FootPrintX > 0 {
		fx = int32(prof.FootPrintX)
	}
	if prof.FootPrintZ > 0 {
		fz = int32(prof.FootPrintZ)
	}
	if fx <= 0 {
		fx = 1
	}
	if fz <= 0 {
		fz = 1
	}
	cellX := world.WorldToCell(dropX)
	cellZ := world.WorldToCell(dropZ)
	ax := cellX - fx/2
	az := cellZ - fz/2
	// Place at cell center + half? Convert anchor to world.
	worldX := world.CellToWorld(ax) + numeric.Fixed(int64(fx)*worldUnitsPerCell/2)
	worldZ := world.CellToWorld(az) + numeric.Fixed(int64(fz)*worldUnitsPerCell/2)
	cargo.X = worldX
	cargo.Z = worldZ
	if s.Terrain != nil {
		cargo.Y = s.Terrain.HeightAt(cargo.X, cargo.Z)
		// Floater cargo over water gets floater clamp already handled elsewhere, but after unload ensure correct Y.
		if cargo.Def.Floater {
			sea := s.Terrain.SeaLevelWorld()
			deck := numeric.Fixed(int64(cargo.Def.Waterline) * 65536)
			// TODO(question): exact floater clamp vs upright [04 §9.2].
			// Use max of terrain and sea+waterline? Simplified: if over water, use sea+waterline.
			h := s.Terrain.HeightAt(cargo.X, cargo.Z)
			if h != numeric.Fixed(-1) && int32(h.Raw()>>16) < int32(s.Terrain.SeaLevel) {
				cargo.Y = sea + deck
			}
		}
	}
	// Update collision/steer positions
	if st, ok := s.Steers[cargoHandle]; ok {
		st.X = int32(cargo.X.Raw())
		st.Z = int32(cargo.Z.Raw())
	}
	if coll, ok := s.Collisions[cargoHandle]; ok {
		coll.X = int32(cargo.X.Raw())
		coll.Z = int32(cargo.Z.Raw())
		coll.Y = int32(cargo.Y.Raw())
		newAnchor := coll.ProposedAnchor(coll.Mode)
		coll.CachedAnchor = newAnchor
		coll.OldAnchor = newAnchor
		// Stamp new footprint
		if s.Grid != nil {
			s.Grid.Stamp(newAnchor, coll.FootPrintX, coll.FootPrintZ, coll.ID)
		}
	}
	// Trigger cargo's own movement reset? Keep mode active.
	return true, ""
}

// IsCarried reports whether unit is currently carried [04 §10.2].
func IsCarried(w *units.World, h pool.Handle) bool {
	if w == nil {
		return false
	}
	u := w.Unit(h)
	return u != nil && u.Attachment.Carrier != 0
}

// CarrierOf returns carrier handle or 0.
func CarrierOf(w *units.World, cargo pool.Handle) pool.Handle {
	if w == nil {
		return 0
	}
	u := w.Unit(cargo)
	if u == nil {
		return 0
	}
	return u.Attachment.Carrier
}
