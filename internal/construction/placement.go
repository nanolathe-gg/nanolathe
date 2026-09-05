// Building placement: the builder/product link table, the reserved footprint
// rectangles, the yard stamps and their transactions [05 "Unit creation and
// limits"][04 §6.2].
//
// Moved out of factory.go by CL-5, which split that file by concern; the code
// is unchanged.

package construction

import (
	"fmt"
	"sort"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// BuilderLink returns the builder registered on a product, if any [05 C18].
func (s *Service) BuilderLink(product pool.Handle) (pool.Handle, bool) {
	b, ok := s.builderLinks[product]
	return b, ok
}

// SetBuilderLink registers the builder link on a product [05 C18].
func (s *Service) SetBuilderLink(product, builder pool.Handle) {
	if s.builderLinks == nil {
		s.builderLinks = make(map[pool.Handle]pool.Handle)
	}
	s.builderLinks[product] = builder
}

// ClearBuilderLink clears builder link on completion [P0-14] (helper for test).
func (s *Service) ClearBuilderLink(product pool.Handle) {
	if s.builderLinks != nil {
		delete(s.builderLinks, product)
	}
}

// PlacementForProduct returns the typed occupancy rectangle retained when a
// nanoframe was allocated. It is session state, not a reinterpretation of
// persisted unit/save fields, and it is not saved: derived occupancy is
// rebuilt after a restore, never restored [08 "Load process" step 10] — see
// the placements field.
func (s *Service) PlacementForProduct(product pool.Handle) (world.FootprintRect, bool) {
	if s == nil || s.placements == nil {
		return world.FootprintRect{}, false
	}
	r, ok := s.placements[product]
	return r.rect, ok
}

func (s *Service) recordPlacement(product pool.Handle, def *content.UnitDef, rect world.FootprintRect) {
	if s.placements == nil {
		s.placements = make(map[pool.Handle]placementRecord)
	}
	s.placements[product] = placementRecord{rect: rect, def: def}
}

// reservePlacement commits the product's footprint after the canonical
// placement query has accepted it. Mobile products stamp the complete ground
// footprint. Building-class products stamp exactly the yard cells selected by
// their current port-18 state [04 R-COLL-01 §3–§4].
func (s *Service) reservePlacement(product pool.Handle, def *content.UnitDef, rect world.FootprintRect) error {
	if s == nil || s.Terrain == nil {
		return fmt.Errorf("construction: placement terrain unavailable")
	}
	if product == 0 || uint64(product) > uint64(^uint16(0)>>1) {
		return fmt.Errorf("construction: placement identity %d exceeds occupancy identity range", product)
	}
	id := int16(product)
	building := def != nil && !def.BMCode
	var yard []world.YardCell
	if building {
		var err error
		yard, err = buildingYard(def, rect)
		if err != nil {
			return err
		}
	}
	for z := rect.MinZ(); z < rect.MaxZ(); z++ {
		for x := rect.MinX(); x < rect.MaxX(); x++ {
			cell := s.Terrain.PlotAt(x, z)
			if cell == nil {
				return fmt.Errorf("construction: placement cell %d,%d unavailable", x, z)
			}
			if building && !yard[int((z-rect.MinZ())*rect.Width()+(x-rect.MinX()))].TestsOccupancy() {
				continue // [04 §6.2] C10: bits 1-2 clear, no occupant test
			}
			// Ground word only [04 R-COLL-01 §2]: "the air word is never
			// consulted, so a landed or hovering airborne unit never blocks a
			// ground mover through this test". WU-19-20 gave mode-2 movers the
			// air word [04 R-COLL-01 §4]; testing it here would let an
			// aircraft parked over its own plant's exit refuse every later
			// product.
			if cell.OccupantA() != 0 && cell.OccupantA() != id {
				return fmt.Errorf("construction: placement cell %d,%d occupied", x, z)
			}
		}
	}
	if building {
		open := false
		if s.World != nil {
			if u := s.World.Unit(product); u != nil {
				open = u.YardOpen
			}
		}
		s.stampBuilding(product, placementRecord{rect: rect, def: def}, open)
		return nil
	}
	for z := rect.MinZ(); z < rect.MaxZ(); z++ {
		for x := rect.MinX(); x < rect.MaxX(); x++ {
			cell := s.Terrain.PlotAt(x, z)
			if cell.OccupantA() == 0 || cell.OccupantA() == id {
				cell.SetOccupantA(id)
			}
		}
	}
	return nil
}

// retirePlacement releases a completed mobile product. A completed building
// keeps its placement record and its canonical yard-selected ground stamp, so
// Terrain.CheckPlacement remains the single blocker source [04 R-COLL-01 §3].
func (s *Service) retirePlacement(product pool.Handle) {
	if s == nil || product == 0 {
		return
	}
	record, ok := s.placements[product]
	if !ok {
		return
	}
	if record.def == nil || record.def.BMCode {
		s.ReleasePlacement(product)
		return
	}
	open := false
	if s.World != nil {
		if u := s.World.Unit(product); u != nil {
			open = u.YardOpen
		}
	}
	s.stampBuilding(product, record, open)
}

func buildingYard(def *content.UnitDef, rect world.FootprintRect) ([]world.YardCell, error) {
	w, d := int(rect.Width()), int(rect.Depth())
	if w <= 0 || d <= 0 {
		return nil, fmt.Errorf("construction: invalid building footprint %dx%d", w, d)
	}
	if def == nil || def.BMCode {
		return nil, fmt.Errorf("construction: building yard unavailable")
	}
	yard, err := world.ParseYardMap(def.YardMap, w, d)
	if err != nil {
		return nil, err
	}
	if len(yard) != w*d {
		return nil, fmt.Errorf("construction: building yard length %d != footprint %d", len(yard), w*d)
	}
	return yard, nil
}

// stampBuilding normalizes one building to the exact ground cells selected by
// its current yard state. The global clear pass precedes the global stamp pass
// so an accepted yard transition preserves retail's clear-then-restamp order
// [04 R-COLL-01 §4].
//
// Retail writes one ground word per cell. Nanolathe splits that plane in two —
// the terrain plot cell read by the placement validator, and the movement
// occupancy grid read by the mover commit and the path search — so both must
// follow the yard state together. A building that released its `c`/`C` pad in
// the plot alone still held it in the grid, and the exit-spot query of
// [04 R-FAC-02 §5] (null self identity, so the producer's own stamp blocks it)
// rejected every product forever.
func (s *Service) stampBuilding(product pool.Handle, record placementRecord, open bool) {
	if s == nil || s.Terrain == nil || product == 0 || uint64(product) > uint64(^uint16(0)>>1) {
		return
	}
	yard, err := buildingYard(record.def, record.rect)
	if err != nil {
		return
	}
	id := int16(product)
	var grid *movement.OccupancyGrid
	if s.Movement != nil {
		grid = s.Movement.Grid // nil-safe: every OccupancyGrid method tolerates a nil receiver
	}
	gridID := int(product)
	if current, ok := s.placements[product]; ok {
		current.yardOpen = open
		s.placements[product] = current
	}
	// Keep movement's teardown state in lockstep with this accepted yard state.
	if s.Movement != nil {
		s.Movement.SetBuildingYardState(product, open)
	}
	// First release every self-owned cell no longer selected by the new state.
	for z := record.rect.MinZ(); z < record.rect.MaxZ(); z++ {
		for x := record.rect.MinX(); x < record.rect.MaxX(); x++ {
			cell := s.Terrain.PlotAt(x, z)
			if cell == nil {
				continue
			}
			y := yard[int((z-record.rect.MinZ())*record.rect.Width()+(x-record.rect.MinX()))]
			if !y.Selects(open) {
				if cell.OccupantA() == id {
					cell.SetOccupantA(0)
				}
				// Clear only touches cells this identity holds, which is the
				// plot's self-owned test in the other layer [04 R-COLL-01 §4].
				grid.Clear(movement.Cell{X: x, Z: z}, 1, 1, gridID)
			}
		}
	}
	// Only after the complete clear pass, stamp every cell selected by the new
	// state and set the structure-yard mark on every yard-bit-0 cell.
	for z := record.rect.MinZ(); z < record.rect.MaxZ(); z++ {
		for x := record.rect.MinX(); x < record.rect.MaxX(); x++ {
			cell := s.Terrain.PlotAt(x, z)
			if cell == nil {
				continue
			}
			y := yard[int((z-record.rect.MinZ())*record.rect.Width()+(x-record.rect.MinX()))]
			if y.Selects(open) {
				// The building class takes the same per-cell overlap protocol
				// as every mover stamp [04 R-COLL-01 §4]: the grid arbitrates
				// the cell as it is visited, raises the host/intruder bits on
				// both units, and writes the plot word itself when a terrain
				// is bound to it. When it is not (a grid-less or plot-less
				// fixture), the same arbitration decides the word here; the
				// verdict is deterministic, so asking twice cannot disagree
				// and the bit raises are idempotent.
				grid.Stamp(movement.Cell{X: x, Z: z}, 1, 1, gridID)
				if cell.OccupantA() != id && grid.ArbitrateOverlap(int(cell.OccupantA()), gridID) {
					cell.SetOccupantA(id)
				}
			}
			if y&0x01 != 0 {
				cell.SetStructureYard(true)
			}
		}
	}
	// The overlap protocol itself — host/intruder bits, the displacement of an
	// occupant whose owner is in the eliminated player state, and the clear's
	// overlap scan and restamp — now runs inside the occupancy layer for this
	// stamp exactly as it does for a mover [04 R-COLL-01 §4].
	//
	// [04 R-COLL-01 §4] gives two tails after the cell loop, on the stamp side
	// — "for the building class the derived-height recompute over the grown
	// rectangle and a reclassification of the rectangle in every active class
	// layer follow". WU-19-166 identified both as seams in other packages;
	// WU-19-193 closes the second one.
	//
	// The class-layer reclassification runs HERE, after the complete
	// clear-then-stamp pass and not between them, because the classifier reads
	// the occupant word back out of the grid: a reclassify placed inside the
	// pair would bake in a rectangle that is half released and half claimed. It
	// covers every path into this function — the nanoframe placement stamp, the
	// completion restamp, and the accepted port-18 yard transition, which is
	// retail's third restamp caller and reclassifies "the rectangle in every
	// layer" for the same reason ([04 R-COLL-01 §4] "the yard-open port write").
	// Without it the cells a factory's yard releases to let a product out, and
	// the cells a finished building newly claims, kept whatever a class layer
	// had baked in — a wall where the door opened, and an open door where the
	// wall went up — for the rest of the battle, since the request revision pass
	// walks live units through the occupant-age window and a building never
	// enters it [04 R-PATH-01 §14].
	//
	// The first tail stays a no-op: internal/world's derived min/max floor
	// heights are read through PlotCell.MinHeight/MaxHeight and written by
	// nothing after the map loads, so there is no rectangle recompute to call.
	// In THIS build that is unobservable until some path mutates the height map.
	if s.Movement != nil {
		s.Movement.NoteStructureStamp(
			movement.Cell{X: record.rect.MinX(), Z: record.rect.MinZ()},
			int16(record.rect.Width()),
			int16(record.rect.Depth()),
		)
	}
}

// RegisterBuildingPlacement records and stamps a building at the exact
// footprint derived by session composition before strict COB Create. This is
// the unit-creation stamp writer of [04 R-COLL-01 §4], not a reservation or a
// whole-rectangle approximation.
func (s *Service) RegisterBuildingPlacement(u *units.Unit) error {
	if s == nil || u == nil || u.Def == nil || u.Def.BMCode {
		return nil
	}
	extent, err := world.NewFootprintExtent(int32(u.Def.FootprintX), int32(u.Def.FootprintZ))
	if err != nil {
		return err
	}
	placement, err := world.SnapMobilePlacement(u.X, u.Y, u.Z, extent)
	if err != nil {
		return err
	}
	record := placementRecord{rect: placement.Rect(), def: u.Def}
	if _, err := buildingYard(record.def, record.rect); err != nil {
		return err
	}
	s.recordPlacement(u.Handle, u.Def, record.rect)
	s.stampBuilding(u.Handle, record, u.YardOpen)
	return nil
}

// YardOpenTransaction performs port 18's admission and accepted restamp as
// one ordered operation. It returns false on silent denial [04 §4.7 port 18]
// [04 R-COLL-01 §4][04 R-FAC-02 §5].
func (s *Service) YardOpenTransaction(u *units.Unit, requested bool) bool {
	if s == nil || s.Terrain == nil || u == nil || u.Handle == 0 || uint64(u.Handle) > uint64(^uint16(0)>>1) {
		return false
	}
	record, ok := s.placements[u.Handle]
	if !ok {
		// Retail's admission reads only the unit's cached footprint-origin pair,
		// its footprint words, the map bounds and the ground cells; the pair is
		// written by unit state initialization BEFORE bind-and-Create and before
		// the creation stamp, so a `Create`-time port-18 write is admitted on
		// the ordinary test and its bit survives into the initial stamp
		// [04 §4.7 "a Create-time yard write is admitted and kept"][04 R-CB-01
		// §4]. Nanolathe's equivalent of that pair is the placement record,
		// which RegisterBuildingPlacement writes before strict COB Create for
		// every building, so a building never reaches this branch. A unit with
		// no record here is a mobile (`bmcode`) unit, for which construction
		// keeps no yard geometry.
		//
		// REWRITTEN (WU-19-166): half of the marker that stood here was already
		// answered. It read "whether the mover restamp honours the yard bit is
		// untraced. Decider: the mover stamp's class selection against the yard
		// bit [04 R-COLL-01 §4]." That very section answers it: the stamp's
		// class is chosen by the unit's structure-class bit — "the ground word
		// is written by ground movers (mode 1) and by building-class units
		// (flags bit 29, set at creation from `bmcode == 0`)" — and only the
		// building class "selects cells by yard byte". A mover stamps its whole
		// footprint on the ground plane whatever its yard bit says, so a
		// mobile's restamp cannot differ, and the occupancy this service keeps
		// is identical on both arms of the question.
		//
		// Closed (RWU-19-197, [04 R-FAC-02 §9]): the admission predicate reads
		// the per-cell yard byte through the definition's yard-map pointer with
		// no null test and no structure-class test, and the compiler allocates
		// that map only for `bmcode == 0` ([fmt fbi] `BMcode`; all 152 stock
		// mobile definitions author no `YardMap`). A mobile unit that writes the
		// port passes the cached-pair test and reads the bytes at process
		// addresses 0..footprintX×footprintZ−1 — retail defines no verdict for
		// that read, and no stock mobile script writes the port. Refusing here
		// is Nanolathe's stated choice over an undefined read, not a trace: it
		// is the one verdict that cannot be wrong for a unit that owns no yard
		// cells, and it stays fail-closed.
		return false
	}
	yard, err := buildingYard(record.def, record.rect)
	if err != nil {
		return false
	}
	// The admission bounds are the mobile validator's exact building bounds:
	// positive cached pair and the final map row/column excluded [04 R-COLL-01
	// §2][04 R-FAC-02 §5].
	if record.rect.MinX() <= 0 || record.rect.MinZ() <= 0 ||
		record.rect.MaxX() >= s.Terrain.CellW || record.rect.MaxZ() >= s.Terrain.CellH {
		return false
	}
	id := int16(u.Handle)
	// Retail reads one ground word per cell here, so a factory cannot close its
	// yard while a released product still stands on a `c`/`C` cell, and cannot
	// open it while a foreign unit stands on an `O` cell [04 R-FAC-02 §5].
	// Nanolathe splits that plane in two — the terrain plot cell and the
	// movement occupancy grid — and stampBuilding already writes both. The
	// admission test has to read both for the same reason: construction's plot
	// stamp is released when a mobile product completes, after which the grid is
	// the only layer still holding the pad, so a plot-only test admitted the
	// close with the product still standing in the yard and closed the doors on
	// it. Whether the script retries the refused write is authored behavior
	// [04 R-FAC-02 §5].
	var grid *movement.OccupancyGrid
	if s.Movement != nil {
		grid = s.Movement.Grid // nil-safe: every OccupancyGrid method tolerates a nil receiver
	}
	gridID := int(u.Handle)
	for z := record.rect.MinZ(); z < record.rect.MaxZ(); z++ {
		for x := record.rect.MinX(); x < record.rect.MaxX(); x++ {
			y := yard[int((z-record.rect.MinZ())*record.rect.Width()+(x-record.rect.MinX()))]
			checked := y&0x04 != 0
			if requested {
				checked = y&(0x02|0x08) != 0
			}
			if !checked {
				continue
			}
			cell := s.Terrain.PlotAt(x, z)
			if cell == nil || (cell.OccupantA() != 0 && cell.OccupantA() != id) {
				return false
			}
			if occ, held := grid.OccupantAt(movement.Cell{X: x, Z: z}); held && occ != 0 && occ != gridID {
				return false
			}
		}
	}

	// The authoritative bit commits before the clear/stamp pass [04 R-COLL-01 §4].
	u.YardOpen = requested
	s.stampBuilding(u.Handle, record, requested)
	return true
}

func (s *Service) releaseFrameStamps(product pool.Handle) bool {
	if s == nil || s.placements == nil {
		return false
	}
	record, ok := s.placements[product]
	if !ok {
		return false
	}
	if s.Terrain != nil && uint64(product) <= uint64(^uint16(0)>>1) {
		id := int16(product)
		// The leaving identity releases both halves of Nanolathe's split ground
		// plane, exactly as it took them in stampBuilding [04 R-COLL-01 §4].
		var grid *movement.OccupancyGrid
		if s.Movement != nil {
			grid = s.Movement.Grid // nil-safe: every OccupancyGrid method tolerates a nil receiver
		}
		gridID := int(product)
		var yard []world.YardCell
		if record.def != nil && !record.def.BMCode {
			yard, _ = buildingYard(record.def, record.rect)
		}
		for z := record.rect.MinZ(); z < record.rect.MaxZ(); z++ {
			for x := record.rect.MinX(); x < record.rect.MaxX(); x++ {
				cell := s.Terrain.PlotAt(x, z)
				if cell == nil {
					continue
				}
				if len(yard) != 0 {
					y := yard[int((z-record.rect.MinZ())*record.rect.Width()+(x-record.rect.MinX()))]
					if !y.Selects(record.yardOpen) {
						if y&0x01 != 0 {
							cell.SetStructureYard(false)
						}
						continue
					}
				}
				if cell.OccupantA() == id {
					cell.SetOccupantA(0)
				}
				grid.Clear(movement.Cell{X: x, Z: z}, 1, 1, gridID)
				if len(yard) != 0 {
					y := yard[int((z-record.rect.MinZ())*record.rect.Width()+(x-record.rect.MinX()))]
					if y&0x01 != 0 {
						cell.SetStructureYard(false)
					}
				}
			}
		}
	}
	delete(s.placements, product)
	return true
}

// ReleasePlacement clears only ground words equal to the leaving identity,
// clears its structure-yard marks, and deletes its placement record. The
// death/teardown observer calls this exactly once [04 R-COLL-01 §4][R-P0-09].
func (s *Service) ReleasePlacement(product pool.Handle) bool {
	return s.releaseFrameStamps(product)
}

// BuilderLinks returns a copy of all builder/product links (ON-02).
// Exported accessor replaces reflect/unsafe inspection; used to verify deterministic cleanup.
func (s *Service) BuilderLinks() map[pool.Handle]pool.Handle {
	if s == nil || s.builderLinks == nil {
		return nil
	}
	out := make(map[pool.Handle]pool.Handle, len(s.builderLinks))
	for k, v := range s.builderLinks {
		out[k] = v
	}
	return out
}

// LinkRecord is one builder-product link, product handle owns builder handle [05 C18][RS-10].
type LinkRecord struct {
	Builder pool.Handle
	Product pool.Handle
}

// SnapshotLinks returns a deterministic sorted copy of builder-product links [RS-10][I1].
// Sorted by Product ascending, then Builder ascending, for canonical save ordering.
func (s *Service) SnapshotLinks() []LinkRecord {
	if s == nil || len(s.builderLinks) == 0 {
		return nil
	}
	out := make([]LinkRecord, 0, len(s.builderLinks))
	for prod, builder := range s.builderLinks {
		out = append(out, LinkRecord{Builder: builder, Product: prod})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Product != out[j].Product {
			return out[i].Product < out[j].Product
		}
		return out[i].Builder < out[j].Builder
	})
	return out
}

// ---------------------------------------------------------------------------
// C24 Construction arithmetic [05 "Construction arithmetic"].
// ---------------------------------------------------------------------------
