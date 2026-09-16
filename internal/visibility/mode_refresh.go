package visibility

// ModeRefreshObserver is one defined sight source supplied in deterministic
// unit order to a bulk visibility rebuild: the live visibility-mode command
// refresh of [03 R-VIS-01 §1] and the entry rebuild of [08 R-ENTRY-01 §7].
// Observer coordinates are already expressed for the target raster: a sprite
// observer cell and a terrain-ray tile are deliberately different coordinate
// spaces.
type ModeRefreshObserver struct {
	ID       ObserverID
	Observer Observer
}

// RebuildEntry is the bulk visibility-and-mapping rebuild retail calls with the
// full argument at battle entry and again at every commander respawn and
// watch-mode entry [08 R-ENTRY-01 §7][08 R-SKIR-01 §3][03 R-VIS-01 §4] pass 1.
// It is not the live-command refresh: RefreshMode keeps mapping history unless
// its caller asks for a reset, while the entry rebuild always refills both
// stores from the mode word.
//
//  1. the whole word grid is refilled from mode bit 0;
//  2. every eligible slot's byte grid is refilled from mode bit 1 — a slot that
//     fails the live/controller/side test keeps its stale bytes;
//  3. with mode bit 1 set, every supplied active unit is stamped in record
//     order through the bit-2 raster, with no refresh throttle; with bit 1
//     clear no unit is visited at all and every saved observer record is left
//     alone [03 R-VIS-01 §1] item 1;
//  4. the minimap/fog presentation is invalidated.
//
// Skipping step 3 cannot leak coverage: the refilled byte grids hold the
// all-visible fill, and both retirement and the byte half of publication are
// themselves gated on mode bit 1, so a retained record contributes nothing
// until a later command refills the grids for it.
func (s *Service) RebuildEntry(eligible [10]bool, observers []ModeRefreshObserver) {
	if s == nil || s.wordMask == nil {
		return
	}
	s.fillWordGrid()
	s.fillEligibleByteGrids(eligible)
	if s.mode.CurrentEnabled() {
		// The stamps replace every stored record, so no pre-rebuild footprint
		// may survive to throttle a republication or a later retirement.
		s.footprints = make(map[ObserverID]footprint, len(observers))
		s.rebuildingPresentation = true
		for _, stamped := range observers {
			s.stampEntryObserver(stamped.ID, stamped.Observer)
		}
		s.rebuildingPresentation = false
	}
	s.invalidatePresentation()
}

// stampEntryObserver is step 3's direct stamp. The entry rebuild forms the
// observer record from the unit's current state and calls the selected raster's
// stamper outright: there is no stored record left to compare against, so the
// ordinary refresh throttle of [03 R-VIS-01 §2] must not be entered here.
func (s *Service) stampEntryObserver(id ObserverID, ob Observer) {
	if !validPlayer(ob.Owner) {
		return
	}
	ray := s.mode.TerrainRay()
	quantized := int32(s.spriteShapeIndex(ob.Radius))
	storedCX, storedCZ := s.spriteStoredOrigin(ob.CX, ob.CZ, ob.Radius)
	storedByte := uint8(quantized)
	if ray {
		quantized = int32(s.rayTableIndex(ob.Radius))
		storedCX, storedCZ = ob.CX, ob.CZ
		storedByte = ob.HeightByte
	}
	next := footprint{
		owner: ob.Owner, cx: ob.CX, cz: ob.CZ, heightByte: ob.HeightByte,
		radius: ob.Radius, quantized: quantized,
		storedCX: storedCX, storedCZ: storedCZ, storedByte: storedByte,
	}
	// Only the ray branch rejects an off-map observer cell; circular masks still
	// publish their clipped overlap [03 R-VIS-01 §2].
	if ray && (uint32(ob.CX) >= uint32(s.W) || uint32(ob.CZ) >= uint32(s.H)) {
		s.footprints[id] = next
		return
	}
	next.live = true
	s.footprints[id] = next
	s.Publish(ob.Owner, ob.CX, ob.CZ, ob.HeightByte, ob.Radius)
}

// RefreshMode applies retail's live visibility-command refresh [03 R-VIS-01
// §1][03 R-VIS-01 §2]. It is intentionally separate from RebuildAll: commands
// reset current coverage, but retain mapping history unless their caller asks
// for a history reset, and preserve saved observer fields while current
// coverage is disabled.
//
// eligible identifies the active player records whose byte grids retail fills.
// observers must contain only defined live units, in the session's stable unit
// order. The caller supplies records in the target raster's coordinate space.
func (s *Service) RefreshMode(m Mode, resetHistory bool, eligible [10]bool, observers []ModeRefreshObserver) {
	if s == nil || s.wordMask == nil {
		return
	}
	const semantic = ModeHistoryEnabled | ModeCurrentEnabled | ModeTerrainRay
	s.mode = s.mode&ModeFogCacheValid | (m & semantic)

	if resetHistory {
		s.fillWordGrid()
	}
	s.fillEligibleByteGrids(eligible)

	// Retail skips the whole saved-observer clear/publication block while
	// current coverage is disabled. In particular, it does not retire or
	// replace a circular or ray observer record in this state.
	if s.mode.CurrentEnabled() {
		if s.footprints == nil {
			s.footprints = make(map[ObserverID]footprint)
		}
		if s.mode.TerrainRay() {
			for _, stamped := range observers {
				s.refreshModeRay(stamped.ID, stamped.Observer)
			}
		} else {
			for _, stamped := range observers {
				s.refreshModeSprite(stamped.ID, stamped.Observer)
			}
		}
	}
	// The retail command refresh invalidates its presentation result even when
	// a repeated command leaves all three semantic bits unchanged.
	s.invalidatePresentation()
}

func (s *Service) fillWordGrid() {
	fill := uint16(0)
	if !s.mode.HistoryEnabled() {
		fill = 0x03FF
	}
	for i := range s.wordMask {
		s.wordMask[i] = fill
	}
}

func (s *Service) fillEligibleByteGrids(eligible [10]bool) {
	fill := uint8(0)
	if !s.mode.CurrentEnabled() {
		fill = 1
	}
	for p := range s.byteGrids {
		if !eligible[p] || s.byteGrids[p] == nil {
			continue
		}
		for i := range s.byteGrids[p] {
			s.byteGrids[p][i] = fill
		}
	}
}

// spriteStoredOrigin is the frame-origin pair retail leaves in a circular
// observer record and the ordinary refresh compares before republishing.
func (s *Service) spriteStoredOrigin(cx, cz, radius int32) (int32, int32) {
	shape := s.shapes.Shape(s.spriteShapeIndex(radius))
	if shape == nil {
		return cx, cz
	}
	return cx - shape.AnchorX, cz - shape.AnchorY
}

// refreshModeSprite follows the live command's direct circular publisher. The
// byte grids were just reset, so it must not retire any previous raster; the
// direct publisher replaces the saved fields and stamps from the target
// sprite-origin coordinate.
func (s *Service) refreshModeSprite(id ObserverID, ob Observer) {
	if !validPlayer(ob.Owner) {
		return
	}
	storedCX, storedCZ := s.spriteStoredOrigin(ob.CX, ob.CZ, ob.Radius)
	quantized := int32(s.spriteShapeIndex(ob.Radius))
	next := footprint{
		owner: ob.Owner, cx: ob.CX, cz: ob.CZ, heightByte: ob.HeightByte, radius: ob.Radius,
		quantized: quantized, storedCX: storedCX, storedCZ: storedCZ,
		storedByte: uint8(quantized),
	}
	// Circular publication clips the mask itself, including off-map centers
	// [03 R-VIS-01 §2]. Retirement must visit that same clipped footprint.
	next.live = true
	s.footprints[id] = next
	s.Publish(ob.Owner, ob.CX, ob.CZ, ob.HeightByte, ob.Radius)
}

// refreshModeRay clears only the saved coverage byte and re-enters the ordinary
// ray throttle. It deliberately retains the stored pair verbatim: on a
// Circular-to-True transition that pair is a sprite origin, not a terrain-ray
// tile, so comparing it with the new ray tile is the required cross-mode edge.
// The byte-grid wipe already disposed of preceding coverage, hence no old
// footprint is removed here.
func (s *Service) refreshModeRay(id ObserverID, ob Observer) {
	if !validPlayer(ob.Owner) {
		return
	}
	old := s.footprints[id]
	old.storedByte = 0
	old.live = false // byte grids were cleared; retained raw fields stay valid
	s.footprints[id] = old

	if old.storedCX == ob.CX && old.storedCZ == ob.CZ && ob.HeightByte <= 5 {
		return
	}
	next := footprint{
		owner: ob.Owner, cx: ob.CX, cz: ob.CZ, heightByte: ob.HeightByte, radius: ob.Radius,
		quantized: int32(s.rayTableIndex(ob.Radius)),
		storedCX:  ob.CX, storedCZ: ob.CZ, storedByte: ob.HeightByte,
	}
	if uint32(ob.CX) >= uint32(s.W) || uint32(ob.CZ) >= uint32(s.H) {
		s.footprints[id] = next
		return
	}
	next.live = true
	s.footprints[id] = next
	s.Publish(ob.Owner, ob.CX, ob.CZ, ob.HeightByte, ob.Radius)
}
