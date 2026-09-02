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
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// attachModeUnchanged is Nanolathe's "the request carried no mode" sentinel,
// not a retail value. [04 R-AIR-01 §9] establishes that the attachment helper
// overwrites the child's committed mover-mode pair with the request's mode on
// both halves, and names the modes used by three of its four callers: 0 on
// every ordinary attach, 2 on the self-detach a carried carrier performs in the
// takeoff preamble, and 1 on the `VTOL_Unload` phase-2 release.
// [04 R-AIR-01 §10]'s closing aside supplies the fourth from the factory-egress
// trace: the factory product's builder link passes request mode 1
// ([04 R-FAC-02 §1] item 4), which is also the mode a nanoframe needs to hold
// the ground plane for the whole build [04 R-FAC-02 §2].
//
// The sentinel therefore survives only for the detach callers no section names
// — the carrier-death cascade and the orphan cleanup — which keep the mode the
// child already has rather than inventing one.
const attachModeUnchanged = -1

// AttachCargo attaches cargo to carrier on piece [04 §10.2] load phase 4.
// It updates both sides: cargo.Carrier = carrier, carrier.Cargo appends cargo.
// Piece -1 is root fallback [04 §5.3][04 §10.2]. Cargo must not already be carried.
// Carrier must have live mover and canfly per gate [04 §10.2] but this helper does not re-check gates.
//
// This form carries no request mode; see AttachCargoMode.
func AttachCargo(w *units.World, carrierHandle, cargoHandle pool.Handle, piece int) bool {
	return AttachCargoMode(w, carrierHandle, cargoHandle, piece, attachModeUnchanged)
}

// AttachCargoMode is AttachCargo with the request mode of [04 R-AIR-01 §9].
// After the linkage is recorded the helper "overwrites the child's committed
// mover-mode pair with the request's mode value", and the write is DIRECT — it
// does not go through the mover-mode setter, so attaching never zeroes
// velocity, never levels bank and pitch, and never raises `Activate` or
// `Deactivate` [04 R-AIR-01 §3].
func AttachCargoMode(w *units.World, carrierHandle, cargoHandle pool.Handle, piece, mode int) bool {
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
	writeRequestedMoverMode(cargo, mode)
	return true
}

// writeRequestedMoverMode is the attachment helper's mode write: the request's
// LOW TWO BITS go straight into the committed mover-mode pair, bypassing the
// mover-mode setter [04 R-AIR-01 §9][04 R-AIR-01 §3].
func writeRequestedMoverMode(child *units.Unit, mode int) {
	if child == nil || mode == attachModeUnchanged {
		return
	}
	child.Move.Mode = uint8(mode) & 0x3
}

// attachModeFactoryProduct is the factory product's builder-link request mode.
// [04 R-AIR-01 §10]'s closing aside reads it off the factory-egress trace:
// "the factory product's builder link — the fourth caller of the attachment
// helper — passes request mode 1 ([04 R-FAC-02 §1] item 4)", which is the
// grounded mode every product including an aircraft takes.
const attachModeFactoryProduct = 1

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
	return AttachCargoMode(w, carrierHandle, productHandle, piece, attachModeFactoryProduct)
}

// DetachCargo detaches cargo from its carrier [04 §10.2] unload phase 2.
// Returns carrier handle if found. This form carries no request mode; see
// DetachCargoMode.
func DetachCargo(w *units.World, cargoHandle pool.Handle) (pool.Handle, bool) {
	return DetachCargoMode(w, cargoHandle, attachModeUnchanged)
}

// DetachCargoMode is DetachCargo with the request mode of [04 R-AIR-01 §9]:
// the detach half takes the mover mode from the request and writes its low two
// bits directly into the child's committed mover-mode pair. `VTOL_Unload`'s
// phase-2 release passes 1 — grounded — which is what puts the released cargo
// back into the ground occupancy plane [04 R-COLL-01 §4].
func DetachCargoMode(w *units.World, cargoHandle pool.Handle, mode int) (pool.Handle, bool) {
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
	writeRequestedMoverMode(cargo, mode)
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
				// Carried motion bypasses validation, but the carried-position
				// setter still clears and stamps on a cell/mode change, in the
				// plane its mode names: a mode-1 nanoframe holds the ground word
				// of its pad for the whole build, and an attached mover in mode 0
				// writes nothing [04 R-FAC-02 §2][04 R-COLL-01 §4].
				collCargo.OldAnchor = newAnchor
				collCargo.CachedAnchor = newAnchor
				collCargo.Mode = cargo.Move.Mode
				// The setter writes "XYZ, cell pair and MODE" on a change
				// [04 R-FAC-02 §2], so the cached mode moves with the cached
				// pair. Keeping it stale is what would let the released
				// cargo's next commit take the same-cell fast path and skip
				// the restamp [04 R-COLL-01 §1] — the commit
				// [04 R-AIR-01 §10] item 2 requires to run.
				collCargo.CachedMode = cargo.Move.Mode & 0x3
				s.syncMoverStamp(cargo)
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
			dmg := int32(30000)
			// Apply through funnel: for now subtract health and mark.
			// In combat, would go through death funnel with armor/veterancy [06 §9.1]; keep direct.
			if cargo.Health > 0 {
				cargo.Health -= dmg
				if cargo.Health <= 0 {
					cargo.Health = 0
					// The cascade's packets carry the CARRIER's killer as their
					// attacker, not the carrier [06 §12.1]: "cargo killed by
					// carrier death credits the carrier's killer ... the cascade
					// applies its 30000 damage per cargo with the attacker
					// argument set to the carrier's killer". That attacker is
					// then what the death handler's row writes into each
					// cargo's recorded-attacker link [04 R-UNIT-06 §5], and it
					// is what the kill credit reads. A carrier that dies with
					// no killer behind it — a packetless finalisation — passes
					// the null through, which is the same row's "may be null".
					w.DestroyBy(cargoHandle, units.DeathKilled, killerHandle) // will trigger recursive detach
				}
			}
			DetachCargo(w, cargoHandle)
		}
		// Clear carrier list
		dying.Attachment.Cargo = nil
	}
}

// unloadFootprint is the cargo's packed footprint dimension pair the unload
// anchor conversion reads [04 §10.2]. The unit keeps a copy of the
// definition's footprint-size pair [04 R-ORD-01 §1]; a resolved movement
// profile carries the class width and depth when one is bound.
func (s *System) unloadFootprint(cargoHandle pool.Handle, cargo *units.Unit) (fx, fz int32) {
	fx, fz = 1, 1
	if cargo != nil && cargo.Def != nil {
		if cargo.Def.FootprintX > 0 {
			fx = cargo.Def.FootprintX
		}
		if cargo.Def.FootprintZ > 0 {
			fz = cargo.Def.FootprintZ
		}
	}
	prof := s.ProfileFor(cargoHandle)
	if prof.FootPrintX > 0 {
		fx = int32(prof.FootPrintX)
	}
	if prof.FootPrintZ > 0 {
		fz = int32(prof.FootPrintZ)
	}
	return fx, fz
}

// ValidateUnloadSite is the unload executor's site test [04 §10.2] phases 1
// and 2: "converts the drop point to a footprint anchor using the cargo's
// packed footprint dimensions and validates the cargo definition through the
// standard placement validator in mode 1".
//
// The anchor conversion is the footprint snap of [04 R-ORD-01 §1] —
// `(pos − foot·2^19 + 2^19) >> 20`, an arithmetic shift — which is
// `world.PlacementAnchor`, the same helper the ghost updater and the order
// issuer use. It replaces a hand-rolled `cell − foot/2` that was neither the
// snap nor a floor and that disagreed with the anchor every other placement
// caller computes for the same point. [04 R-AIR-01 §10] item 1 confirms the
// expression on this caller: "per axis `cell = (goal + 0x80000 − foot ·
// 0x80000) >> 20`, an arithmetic shift on the 32-bit sum", re-derived from the
// record's raw goal on every one of the two phases that needs it — nothing
// stores a snapped drop point anywhere.
//
// Mode 1's contract, for a mobile definition (`bmcode == 0`), is
// [08 R-AI-03 §4]: bounds first — `gx >= 0`, `gz >= 0`, `gx + footX <
// mapCellWidth`, `gz + footZ < mapCellHeight`, off-map is false because only
// mode 2 treats off-map as placeable — then the footprint blocker's per-cell
// walk over blocking features, non-self occupants, the water band and the
// slope tier. `world.Terrain.CheckPlacement` with `Mobile` set is that walk,
// and it reads the mover-written half of the ground word through
// `Terrain.Movers`, which this package installs [04 R-COLL-01 §2].
//
// Self identity: `0`, and mode `1`. [08 R-AI-03 §4]'s mode-1 caller passes 0
// and [04 R-AIR-01 §10] item 1 reads the same pair off both unload phases —
// "handed to the placement validator with self identity `0` and mode `1`". The
// cargo is attached in mode 0 while this runs and mode 0 writes no occupancy
// word [04 R-COLL-01 §4], so the two readings cannot disagree on live content;
// the traced value is used because it is the traced value.
func (s *System) ValidateUnloadSite(w *units.World, cargoHandle pool.Handle, dropX, dropZ numeric.Fixed, terrain *world.Terrain) bool {
	if s == nil || w == nil || cargoHandle == 0 {
		return false
	}
	cargo := w.Unit(cargoHandle)
	if cargo == nil || cargo.Def == nil {
		return false
	}
	if terrain == nil {
		terrain = s.Terrain
	}
	if terrain == nil {
		return false
	}
	fx, fz := s.unloadFootprint(cargoHandle, cargo)
	extent, err := world.NewFootprintExtent(fx, fz)
	if err != nil {
		return false
	}
	cellX, cellZ := world.PlacementAnchor(dropX, dropZ, fx, fz)
	rect, err := world.NewFootprintRect(world.NewFootprintAnchor(cellX, cellZ), extent)
	if err != nil {
		return false
	}
	// The compiled movement profile is this package's resolved copy of the same
	// class record the catalog-facing rule resolver reads, and it is the one the
	// cargo's own mover commits against; taking the limits from it keeps the
	// unload site and the commit validator agreeing [04 §6.1 R-DOC04-A]. The
	// aircraft domain skips the terrain aggregates exactly as the shared
	// resolver does [04 §6.4].
	prof := s.ProfileFor(cargoHandle)
	domain := content.MobilityGround
	if cargo.Def.CanFly {
		domain = content.MobilityAircraft
	}
	rules := world.PlacementRules{
		Domain:          domain,
		Waterline:       cargo.Def.Waterline,
		MaxSlope:        int32(prof.MaxSlope),
		MaxWaterSlope:   int32(prof.MaxWaterSlope),
		MaxWaterDepth:   prof.MaxWaterDepth,
		MinWaterDepth:   prof.MinWaterDepth,
		Terrain:         true,
		ProfileResolved: true,
	}
	_, err = terrain.CheckPlacement(world.PlacementQuery{
		Rect:   rect,
		Rules:  rules,
		Self:   0, // [04 R-AIR-01 §10] item 1
		Mobile: true,
	})
	return err == nil
}

// TryUnload is the unload executor's phase-2 release [04 §10.2]:
//
//	success starts the deferred zero-argument `EndTransport` FIRST, then
//	detaches the cargo (reserved no-piece index), then constructs the
//	climb-away point command — release order is exactly callback → detach →
//	climb-away.
//
// The climb-away marker is the caller's, so this helper owns exactly the first
// two steps — and nothing else: the release places nothing
// [04 R-AIR-01 §10] item 2. The callback runs on the CARRIER's script, like
// every transport callback [04 R-UNIT-06 §3], and the detach passes request
// mode 1 — grounded — which is the mode [04 R-AIR-01 §9] names for this release
// and which is what puts the cargo back into the ground occupancy plane when
// its own next commit stamps it [04 R-COLL-01 §4].
//
// It still validates before releasing: the second of §10.2's two validator
// calls is the caller's, and this repeats it so a direct caller cannot release
// onto a site the validator refuses.
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
	if !s.ValidateUnloadSite(w, cargoHandle, dropX, dropZ, s.Terrain) {
		return false, UnableUnloadMessage // verbatim [04 §10.2]
	}
	// Step 1 of the release order: the deferred zero-argument `EndTransport`,
	// on the CARRIER's script, BEFORE the detach [04 §10.2][04 R-UNIT-06 §3].
	if bridge := carrier.ScriptBridge(); bridge != nil {
		bridge.Deferred("EndTransport", nil, nil)
	}
	// Step 2: the detach, with the reserved no-piece index and request mode 1.
	//
	// That is the whole release. [04 R-AIR-01 §10] item 2: the detach's apply
	// step "writes the linkage fields ... and the request's low two bits into
	// the committed mover-mode pair — 1, grounded — and NOTHING else: it does
	// not write X, Y or Z, does not write the unit flags word's mover-mode
	// mirror, and does not touch velocity or speed". The cargo therefore keeps
	// exactly what the carried branch of the occupancy commit last wrote
	// [04 R-FAC-02 §2] — the hang position, the floater deck clamp if it is a
	// `floater`, and the carrier's velocity, speed and orientation — and its
	// own next mover tick owns everything else:
	//
	//   - the commit runs at the cargo's ACTUAL X/Z (the hang point, not the
	//     footprint anchor this executor validated) with the mobile validator
	//     in mode 1, and on success clears and restamps the GROUND words;
	//   - the post-move correction [04 R-MOV-01 §5] then writes Y by exactly
	//     its four branches, so an `upright` cargo lands on the terrain, a
	//     `floater` on the deck line, and a cargo whose model root has no
	//     selection primitive keeps its hang height.
	//
	// There is no model-bottom offset anywhere on this path: the definition's
	// lower Y bound is zeroed at catalog time [02 R-CAT-01 §7] and no release
	// code reads it. The build's snap-to-placement-centre-plus-terrain-height
	// was a divergence and is gone; re-centring the cargo onto the validated
	// anchor is precisely what retail does not do.
	//
	// TODO(T25): the released cargo's first free commit does not run in this
	// build until something gives it an order. Retail's mover tick and its
	// post-move correction run for every live unit — [04 R-MOV-01 §5]'s gate is
	// transform-dirty or `canhover`, never an order — but `StepUnit` in
	// internal/movement/integrate.go returns before the ground branch when the
	// unit's primary queue is empty or its head is a goal-less record such as
	// the `BeCarried` this release retires. Until that gate goes, a cargo
	// released and left alone keeps its hang Y and holds no ground word; both
	// appear on its first commit. Removing the gate is an upstream change
	// WU-19-28 does not own (integrate.go is not in its file set).
	DetachCargoMode(w, cargoHandle, 1)
	// The request mode the detach wrote is the committed pair; this package's
	// per-unit motion records carry this package's copy of that same pair
	// [04 §9.1], and the commit reads it as its proposed mode. The MIRROR the
	// commit rewrites on success is the cached mode, which stays at the carried
	// value so the next tick cannot take the same-cell fast path.
	if fl, ok := s.Flights[cargoHandle]; ok {
		fl.Mode = cargo.Move.Mode & 0x3
	}
	if coll, ok := s.Collisions[cargoHandle]; ok {
		coll.Mode = cargo.Move.Mode & 0x3
	}
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
