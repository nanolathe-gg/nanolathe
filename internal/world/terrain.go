package world

import (
	"fmt"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Version is the TNT version word [03 §2.2] C2. Its value IS the header word,
// so a Version can be compared against raw file bytes without a lookup table.
// It deliberately mirrors formats.Version rather than re-numbering it: an enum
// whose VersionLegacy was 0 sitting next to a comment saying 0x1020 is a trap.
type Version = formats.Version

const (
	VersionLegacy    = formats.VersionLegacy    // 0x1020 [03 §2.2] C3
	VersionCanonical = formats.VersionCanonical // 0x2000 [03 §2.2] C3
)

// Terrain is authoritative world geometry [03 §2.2].
type Terrain struct {
	CellW, CellH int32
	Version      Version
	TileIndices  []uint16     // (CellW/2) x (CellH/2) row-major [03 §2.2] C5
	TileSet      [][1024]byte // Tiles x 1024 bytes [03 §2.2] C5
	Plot         []PlotCell   // CellW x CellH row-major [03 §2.2] C6
	SeaLevel     uint8        // header byte [03 §2.2] C9

	// Movers is the mover-written half of retail's single ground-occupancy
	// word, bound once when the movement system is built.
	//
	// Retail has ONE occupancy word per cell, written by ground movers and by
	// building-class units alike, and the footprint validator's occupant test
	// reads exactly that word [04 R-COLL-01 §2][04 R-COLL-01 §4]. Nanolathe
	// stores the two halves separately — the plot cell above and the mover
	// lattice here — so a placement check that consults only the plot half
	// cannot see a unit standing on the rectangle at all.
	//
	// It lives on the terrain, not on PlacementQuery, because which halves to
	// test is not a per-call decision. While each caller supplied its own, the
	// build-placement preview, AI siting and the commander-death check all
	// omitted it and silently skipped the mover test — the preview drew a site
	// legal with a unit parked on it. Every caller now reaches the same two
	// halves through CheckPlacement.
	//
	// Nil means no mover plane exists yet, which is the pre-movement and
	// terrain-only-fixture case, not a caller's choice.
	Movers MobileOccupancy

	// ClassRestamp is the movement class layers' half of the feature stamper.
	// Retail's single stamping service and its footprint teardown helper each
	// END by restamping every named movement class over the footprint
	// rectangle, synchronously, inside the feature service and in the calling
	// phase [03 §5.1.2][03 R-LAYER §2]. This port is how that call crosses the
	// package boundary: internal/features writes the plot cells and calls
	// NoteFootprintRestamp; internal/movement binds this field at map load and
	// runs the rectangle restamp of [04 R-MOV-03 §3] over every layer.
	//
	// It lives on the terrain for the same reason the static-obstacle revision
	// does: the terrain is the one object every feature writer already holds,
	// and internal/features cannot import internal/movement
	// (docs/ARCHITECTURE.md). Nil means no class layers exist yet — map load
	// and terrain-only fixtures — and a layer allocated later stamps the whole
	// map from the plot as it stands, so nothing is lost.
	ClassRestamp FootprintRestamp

	// losWords is built once during map load and remains immutable for the
	// battle, including across terrain deformation [03 §3.5][R-P0-18-B §4].
	losWords      []uint16
	losBuildCount int
	Gravity       numeric.Fixed // per-tick gravity [03 §2.2] C4
	// AuthoredGravity is the map's gravity word as authored, before the
	// per-tick projectile conversion above. The smoke-puff family reads the
	// word itself — its sub-records rise by `word << 4` of raw 16.16 Y every
	// tick [03 R-FX-01 §3] — so the two consumers need the two forms.
	AuthoredGravity int32
	// OTAGravity retains the mission header's gravity independently of terrain
	// selection: AirStrike reads this raw integer, including zero/negative
	// values and the unparsed-header value −1 [04 R-AIR-01 §8].
	OTAGravity int32
	// LavaWorld is the map's authored `lavaworld` flag. Besides the void flood
	// below, it selects which of a weapon's two authored explosion-art pairs
	// fills its single water-or-lava holder [06 R-WFX-01 §1].
	LavaWorld bool
	// WaterDoesDamage and WaterDamage are the mission's two acid-water words
	// [fmt ota]. They are the pair the per-unit sweep's water-damage step reads
	// [04 R-MOV-03 §1] step 9: damage is applied only when BOTH are nonzero,
	// and the amount applied is WaterDamage [04 §9.2].
	//
	// They live beside SeaLevel because they are its co-operands and because
	// retail reads them, like the sea-level byte, from one set of globals
	// loaded once with the map. The mission OTA and the map OTA are the same
	// file on every entry path this build has — a terrain key is that OTA's own
	// basename — so a campaign mission and a skirmish reach the same two words.
	WaterDoesDamage int32
	WaterDamage     int32
	WindMin         int32   // [03 §2.2] C3/C4
	WindMax         int32   // [03 §2.2] C3/C4
	Tidal           float32 // authored single-precision scalar [03 R-TERR-01 §6]

	// Playable insets derived at void-fixup time [P0-17]: PlayRight = Wpix-32, PlayBottom = Hpix-128.
	PlayRight  int32 // Wpix-32 in map pixels, Wpix=CellW*16 [P0-17]
	PlayBottom int32 // Hpix-128, Hpix=CellH*16 [P0-17]

	// FeatureNames is the map's own feature-record list, in record order, from
	// the TNT feature table [fmt tnt]. A plot cell's feature field indexes THIS
	// list, not the catalog.
	FeatureNames []string

	// FeatureDefs binds each record to its catalog definition, matched
	// case-insensitively by name [fmt tnt]. A nil entry means the map names a
	// feature the catalog does not have, which is the out-of-range case of
	// [04 §6.2]: blocking for yard bit 5, non-satisfying for bit 7. It is not a
	// load failure — retail maps outlive their feature sets.
	FeatureDefs []*content.FeatureDef

	// staticObstacleRevision is the monotonic Nanolathe revision for blocking
	// feature mutations, and only those. No completed-structure writer bumps
	// it, and none is missing: a building blocks through the occupancy grid's
	// occupant word, where the mover-null arm hard-blocks it unconditionally at
	// every watermark and from the first classification that finds it
	// [04 R-PATH-01 §14], and the class layer restamps the occupant rectangle
	// at commit time — neither route reads this counter. This comment used to
	// call that boundary an open question, which read as an unwired writer.
	// It is runtime metadata only; retail route save bytes do not contain it
	// [04 §7.3][04 §8.2][docs/SPEC_CONFLICTS SC22].
	staticObstacleRevision uint64

	// PlayableWpix/Hpix are raw Wpix/Hpix; PlayRight/Bottom are Wpix-32/Hpix-128 [P1-15] used by camera clamp.

	// metalSeeded records whether ApplySchema has run. SampleMetal refuses to
	// answer before it has: an unseeded metal field reads as zero everywhere,
	// which is indistinguishable from a genuinely metal-free map and silently
	// makes every extractor's yield wrong [05 "Terrain metal extraction"].
	metalSeeded bool
}

// StaticObstacleRevision returns the shared movement-facing revision for
// blocking feature changes that persist in routing. Mobile occupancy and
// completed structures are a separate channel — the occupancy grid's occupant
// word, read live by the classifier [04 R-PATH-01 §14] — and do not change
// this value [04 §8.2][docs/SPEC_CONFLICTS SC22].
func (t *Terrain) StaticObstacleRevision() uint64 {
	if t == nil {
		return 0
	}
	return t.staticObstacleRevision
}

// FootprintRestamp restamps the movement class layers over one footprint
// rectangle: the anchor cell and the footprint pair of the definition whose
// cells just changed [03 R-LAYER §2][04 R-MOV-03 §3].
type FootprintRestamp func(anchorX, anchorZ int32, footX, footZ int16)

// NoteFootprintRestamp is the tail of retail's stamping service and of its
// footprint teardown helper: having written the plot cells, restamp every
// named movement class over the rectangle, synchronously, in the calling
// phase [03 §5.1.2][03 R-LAYER §2]. Call it AFTER the plot write, because the
// classifier reads the cells that just changed.
//
// It is unconditional, exactly as the two callers in [03 R-LAYER §2] are: a
// non-blocking feature replacing a blocking one changes the layer just as much
// as the reverse, and the classifier decides which it was.
func (t *Terrain) NoteFootprintRestamp(anchorX, anchorZ int32, footX, footZ int16) {
	if t == nil || t.ClassRestamp == nil {
		return
	}
	if footX <= 0 {
		footX = 1
	}
	if footZ <= 0 {
		footZ = 1
	}
	t.ClassRestamp(anchorX, anchorZ, footX, footZ)
}

// BumpStaticObstacleRevision advances the shared static revision. Saturating
// at the maximum keeps the value monotonic even if an artificial test drives
// the counter to its boundary; ordinary battles cannot reach that boundary.
func (t *Terrain) BumpStaticObstacleRevision() {
	if t == nil || t.staticObstacleRevision == ^uint64(0) {
		return
	}
	t.staticObstacleRevision++
}

// FeatureDefAt resolves a plot cell's feature field to a catalog definition.
// It reports ok only for a real record index that binds to a definition; every
// sentinel, out-of-range index and unbound name reports false [04 §6.2].
func (t *Terrain) FeatureDefAt(feature uint16) (*content.FeatureDef, bool) {
	if t == nil || feature >= plotFeatureRealLimit {
		return nil, false
	}
	if int(feature) >= len(t.FeatureDefs) {
		return nil, false
	}
	def := t.FeatureDefs[feature]
	return def, def != nil
}

// ApplySchema seeds the per-cell metal byte from the mission's uniform surface
// metal value [05 "Terrain metal extraction"] [P1-15]: uniform SurfaceMetal scalar
// via char write to every plot cell +7 (truncates via uint8), no W*H metal raster
// allocated and TNT unk3 byte uniformly 0 corpus-wide [P1-15]. The uniform write
// applies to canonical maps only: a legacy (0x1020) map's per-cell metal was
// already seeded from the legacy attribute record's byte 6 during plot
// expansion, which is the only varying per-cell source — the earlier
// "per-cell varying metal file" question is closed [02 "Terrain file"].
// Extractor yield Σ(byte+1)*extractsMetal sampled once into the unit's stored
// extraction rate, never resampled [P1-10][P1-15]; feature metal is reclaim
// reward only [P1-15].
//
// The value is per-schema in the OTA, and schema selection is a battle-setup
// decision [08], so it cannot happen inside Load. Battle setup must call this
// before any extractor is placed; SampleMetal fails until it does.
func (t *Terrain) ApplySchema(mh *content.MapHeader, schemaIndex int) error {
	if t == nil {
		return fmt.Errorf("world: nil terrain")
	}
	value := int32(0)
	if mh != nil && schemaIndex >= 0 && schemaIndex < len(mh.Schemas) {
		value = mh.Schemas[schemaIndex].SurfaceMetal
	} else if mh != nil && len(mh.Schemas) > 0 {
		return fmt.Errorf("world: schema %d out of range (map has %d) [02 \"Map files\"]", schemaIndex, len(mh.Schemas))
	}
	if value < 0 {
		value = 0
	}
	// The cell field is one unsigned byte; retail stores the schema value
	// through a char, i.e. it truncates rather than clamps
	// (notes/terrain/01_attribute_cells.md +7).
	if t.Version != VersionLegacy {
		cellByte := uint8(value)
		for i := range t.Plot {
			t.Plot[i][7] = cellByte
		}
	}
	t.metalSeeded = true
	return nil
}

// SeaLevelWorld returns sea level in world units as byte*65536 [03 §2.2] C9.
func (t *Terrain) SeaLevelWorld() numeric.Fixed {
	return numeric.Fixed(int64(t.SeaLevel) * 65536)
}

// PlotAt returns the plot cell at cell coordinates (cx,cz) or nil if out of bounds [03 §2.2][GAP T14].
func (t *Terrain) PlotAt(cx, cz int32) *PlotCell {
	if t == nil || t.Plot == nil {
		return nil
	}
	if cx < 0 || cz < 0 || cx >= t.CellW || cz >= t.CellH {
		return nil
	}
	idx := int(cz*t.CellW + cx)
	if idx < 0 || idx >= len(t.Plot) {
		return nil
	}
	return &t.Plot[idx]
}

// HeightAt returns the bilinearly interpolated height at world coordinates (x,z) [03 §2.3] C7.
// It samples four neighboring plot-cell heights and interpolates using the low four
// bits of each cell-space coordinate with signed right-shift bias — never a float lerp.
// World→cell conversion uses the floor-corrected helpers in coords.go [03 §2.1] I3.
// Quantization is floor >>4 for AOE tile phase (radius>>4+1) and >>20 for world→cell
// (projX>>20 as arithmetic shift) [P1-07 §4] (1<<20 =16*65536).
//
// There is no neighbour clamping: retail's guard requires `cx+1 < Width &&
// cz+1 < Height` and otherwise returns the integer −1 sentinel
// (notes/terrain/01_attribute_cells.md:434, notes/terrain/00_terrain_grids.md:128).
// The sentinel is kept raw in the Fixed type — it is an out-of-band marker,
// not a height; callers on the map's last row/column must tolerate it.
func (t *Terrain) HeightAt(x, z numeric.Fixed) numeric.Fixed {
	if t == nil || t.Plot == nil || t.CellW <= 0 || t.CellH <= 0 {
		return 0
	}
	cx := WorldToCell(x) // floor semantics with sign correction [03 §2.1] C1
	cz := WorldToCell(z) // floor semantics with sign correction [03 §2.1] C1
	// Negative coordinates are the unsigned-compare case of the same guard:
	// they must fail, not wrap into border cells.
	if cx < 0 || cz < 0 || cx >= t.CellW || cz >= t.CellH ||
		cx+1 >= t.CellW || cz+1 >= t.CellH {
		return -1 // retail's raw −1 sentinel, see above
	}
	// Fractional position within the cell: low 4 bits of the cell-space
	// coordinate (0..15) where one cell = 16 map pixels [03 §2.1][03 §2.3] C7.
	// Use the floor-corrected cell origin so negative coordinates wrap correctly (I3).
	cellOriginX := CellToWorld(cx)         // [03 §2.1] C1
	cellOriginZ := CellToWorld(cz)         // [03 §2.1] C1
	fxRaw := int64(x) - int64(cellOriginX) // 0 .. worldUnitsPerCell-1
	fzRaw := int64(z) - int64(cellOriginZ)
	fx := int32(fxRaw / worldUnitsPerPixel) // 0..15
	fz := int32(fzRaw / worldUnitsPerPixel)
	// Four-corner heights; the guard above guarantees all four exist.
	w := t.CellW
	base := int(cz*w + cx)
	h00 := int32(t.Plot[base].Height())
	h10 := int32(t.Plot[int(cz*w+cx+1)].Height())
	h01 := int32(t.Plot[int((cz+1)*w+cx)].Height())
	h11 := int32(t.Plot[int((cz+1)*w+cx+1)].Height())
	// Two-stage axis interpolation with truncating bias [03 §2.3] C7.
	// Each axis is a + trunc((b-a)*f/16) where negative deltas bias +15
	// before the arithmetic shift to emulate truncate-toward-zero.
	v := interpStep(interpStep(h00, h10, fx), interpStep(h01, h11, fx), fz)
	return numeric.Fixed(int64(v) * 65536)
}

// interpStep performs one retail axis step a + trunc((b-a)*f/16) with signed
// right-shift bias so negative differences truncate toward zero [03 §2.3] C7.
func interpStep(a, b int32, f int32) int32 {
	d := (b - a) * f
	if d < 0 {
		d += 15
	}
	return a + (d >> 4)
}

// CoarseHeightAt returns the coarse height at cell (cx,cz) as (Min+Max)/2 [03 §2.3] C8.
// This is a separate query from HeightAt and must not be substituted for it.
// Min/Max are the derived floor bytes: hmax at plot cell offset 5 and hmin at
// offset 6 [03 §2.3] [GAP T14].
// Out-of-bounds cells return 0.
func (t *Terrain) CoarseHeightAt(cx, cz int32) numeric.Fixed {
	if t == nil || t.Plot == nil || t.CellW <= 0 || t.CellH <= 0 {
		return 0
	}
	if cx < 0 || cz < 0 || cx >= t.CellW || cz >= t.CellH {
		return 0
	}
	idx := int(cz*t.CellW + cx)
	if idx < 0 || idx >= len(t.Plot) {
		return 0
	}
	cell := t.Plot[idx]
	v := (int32(cell.MinHeight()) + int32(cell.MaxHeight())) >> 1
	return numeric.Fixed(int64(v) * 65536)
}

// LOSHeightWord returns the aggregated two-byte terrain word for visibility
// tile (vx,vz) as (low, high) [03 §3.2] C5.
//
// The LOS writer uses its own, coarser height representation: it "quantizes to
// 32-pixel visibility tiles and reads aggregated terrain heights; it does not
// use the four-corner bilinear query" [03 §2.3]. A visibility tile covers 2x2
// attribute cells [03 §2.1]. It must not be substituted for HeightAt, and a
// tall feature does not raise it — only terrain data does [03 §2.3].
//
// The terrain-ray horizon rule uses both bytes for different things: the LOW
// byte supplies the candidate difference that gates admission, and the HIGH
// byte is tested with the identical comparison afterwards to decide whether the
// retained horizon advances.
//
// The aggregates are established from the map-load builder [R-P0-18-B]:
// the LOW byte is a MAXIMUM and the HIGH byte a MINIMUM, seeded 0x00 / 0xFF and
// finished with a 1/3-2/3 blend floored at sea level. Reading them the other
// way round — the intuitive (min, max) — is the maximally occlusive choice and
// litters flat ground with false shadows.
func (t *Terrain) LOSHeightWord(vx, vz int32) (low, high uint8) {
	if t == nil || t.Plot == nil || t.CellW <= 0 || t.CellH <= 0 {
		return 0, 0
	}
	w, h := t.CellW/2, t.CellH/2
	if w <= 0 || h <= 0 || vx < 0 || vz < 0 || vx >= w || vz >= h {
		return 0, 0
	}
	// Production reads are deliberately side-effect free. Load performs the
	// one build; an unbootstrapped hand-built terrain returns the safe empty
	// value until its test/bootstrap helper is called [03 §3.5].
	if t.losBuildCount == 0 || len(t.losWords) != int(w*h) {
		return 0, 0
	}
	word := t.losWords[vz*w+vx]
	return uint8(word), uint8(word >> 8)
}

// LOSHeightBuildCount reports how many times the map-load LOS builder ran.
// It is primarily a conformance seam for world bootstrap tests.
func (t *Terrain) LOSHeightBuildCount() int {
	if t == nil {
		return 0
	}
	return t.losBuildCount
}

// BuildLOSHeightWordsForTest explicitly bootstraps the table for a manually
// constructed terrain. Production map loading calls the private builder at its
// defined load point; runtime reads never invoke it implicitly.
func (t *Terrain) BuildLOSHeightWordsForTest() {
	if t == nil || t.losBuildCount != 0 {
		return
	}
	t.buildLOSHeightWords()
}

// SetLOSHeightWord installs one visibility tile's LOS height word directly for
// deterministic fixtures. The explicit test/bootstrap build preserves the
// production no-lazy-read contract.
//
// It exists so fixtures and probes can drive the terrain-ray horizon rule from
// exact byte pairs instead of reverse-engineering authored heights through the
// scatter in buildLOSHeightWords. The simulation never calls it.
func (t *Terrain) SetLOSHeightWord(vx, vz int32, low, high uint8) {
	if t == nil || t.CellW <= 0 || t.CellH <= 0 {
		return
	}
	w, h := t.CellW/2, t.CellH/2
	if w <= 0 || h <= 0 || vx < 0 || vz < 0 || vx >= w || vz >= h {
		return
	}
	if t.losBuildCount == 0 {
		t.BuildLOSHeightWordsForTest()
	}
	if len(t.losWords) != int(w*h) {
		return
	}
	t.losWords[vz*w+vx] = uint16(low) | uint16(high)<<8
}

// buildLOSHeightWords fills the per-visibility-tile height table at map load
// [R-P0-18-B]. There is no invalidation API: the table is built once and never
// rebuilt — terrain deformation does not clear it, and only the fog cache is
// dirty-tracked [03 §3.2].
//
// The table is TileW x TileH u16 words seeded low=0x00, high=0xFF, so the low
// byte accumulates a MAXIMUM and the high byte a MINIMUM. Cells are SCATTERED
// into it rather than gathered: iterating columns then rows, each attribute
// cell at (x, z) with height cellH projects to
//
//	zs    = z*16 - cellH/2          (the beam shear, as the observer's own
//	tileZ = zs >> 5                  coverage tile uses [R-P0-18-B §2])
//
// and contributes to tile columns (x-1)>>1 and x>>1 at that row, plus the two
// tiles the PREVIOUS row's cell in this column resolved to — the carry is what
// keeps tiles from being skipped where the shear jumps a row. Cells whose
// tileZ is negative contribute to the carried tiles only.
//
// Two values are scattered per cell: first the perspective-scaled
//
//	value = ((tileZ*32 + 31) * cellH) / (zs + 31)
//
// then the raw cellH. Since value <= cellH, the net effect is low = max of raw
// heights and high = min of scaled values over the neighbourhood.
//
// A final pass blends and floors each word:
//
//	low  = max(SeaLevel, (high + 2*low) / 3)
//	high = max(SeaLevel, (low  + 2*high) / 3)
//
// with truncating division, the low result feeding the high computation from
// the ORIGINAL bytes (retail computes both from the pre-blend pair).
func (t *Terrain) buildLOSHeightWords() {
	if t == nil || t.losBuildCount != 0 {
		return
	}
	t.losBuildCount = 1
	w, h := t.CellW/2, t.CellH/2
	if w <= 0 || h <= 0 {
		t.losWords = nil
		return
	}
	words := make([]uint16, int(w)*int(h))
	for i := range words {
		words[i] = 0xFF00 // low 0x00, high 0xFF [R-P0-18-B §1]
	}
	// scatter applies the low-maximum / high-minimum update to one tile.
	scatter := func(index int, v int32) {
		if index < 0 {
			return
		}
		word := words[index]
		lo, hi := int32(uint8(word)), int32(uint8(word>>8))
		if v > lo { // low keeps the maximum [R-P0-18-B §1]
			lo = v
		}
		if v < hi { // high keeps the minimum [R-P0-18-B §1]
			hi = v
		}
		words[index] = uint16(lo) | uint16(hi)<<8
	}
	tileAt := func(col, row int32) int {
		if col < 0 || row < 0 || col >= w || row >= h {
			return -1
		}
		return int(row*w + col)
	}
	for x := int32(0); x < t.CellW; x++ {
		colA, colB := (x-1)>>1, x>>1
		carryA, carryB := -1, -1
		zPix := int32(0)
		for z := int32(0); z < t.CellH; z++ {
			cellH := int32(t.Plot[z*t.CellW+x].Height())
			zs := zPix - cellH>>1 // beam shear [R-P0-18-B §2]
			tileZ := zs >> 5
			if tileZ > -1 { // [R-P0-18-B §2]
				value := ((tileZ*32 + 31) * cellH) / (zs + 31)
				scatter(carryA, value)
				scatter(carryB, value)
				carryA = tileAt(colA, tileZ)
				scatter(carryA, value)
				carryB = -1
				if colA != colB {
					carryB = tileAt(colB, tileZ)
					scatter(carryB, value)
				}
			}
			// The raw height reaches the same two tiles on both paths.
			scatter(carryA, cellH)
			scatter(carryB, cellH)
			zPix += 16
		}
	}
	sea := int32(t.SeaLevel)
	for i, word := range words {
		lo, hi := int32(uint8(word)), int32(uint8(word>>8))
		newLo := (hi + 2*lo) / 3 // [R-P0-18-B §3]
		newHi := (lo + 2*hi) / 3
		if newLo <= sea {
			newLo = sea
		}
		if newHi <= sea {
			newHi = sea
		}
		words[i] = uint16(newLo) | uint16(newHi)<<8
	}
	t.losWords = words
}

// gravityFromAuthored converts an authored OTA/TNT gravity integer into
// per-tick Fixed 16.16 world units via *65536/900 [fmt ota] [03 §2.2].
// Uses trunc toward zero [INVARIANTS I3].
func gravityFromAuthored(authored int32) numeric.Fixed {
	return numeric.Fixed(int64(authored) * 65536 / 900)
}

// The canonical map's wind, gravity and tidal rules [03 §2.2] C3/C4.
//
// Corrected 2026-09-01 (WU-19-16). [03 §2.2]'s correction against
// [02 R-MAP-01] settles what "absent" means, and this build had it inverted:
// an OMITTED key is not "unparsed". When a `[GlobalHeader]` was parsed at all,
// the OTA parser stores the key's own default — integer 0, float 0.0 — and
// that default then passes the `>= 0` test exactly like an authored value.
// The "default when absent" column applies only to a NEGATIVE authored value,
// to legacy terrain, or to a session with no parsed `[GlobalHeader]` (the
// loader prologue seeds -1 there). This build tested key PRESENCE, so an
// omitted key took the fallback — reading an omission like a negative. A
// canonical map that omits `gravity` therefore runs at gravity 0, not 112,
// which is what makes `AirStrike` cancel there ([04 R-AIR-01 §8], SC23).
//
// content.MapHeader already compiles each absent key to the parser's own
// default ([02 "Map files"]), so no presence probe is needed. All 275 stock
// OTAs author all four keys [RWU-19-8 census], so this is unreachable on stock
// content.
func canonicalGlobals(mh *content.MapHeader) *content.MapHeader {
	if mh == nil || mh.RawOTA == nil || mh.RawOTA.Global == nil {
		return nil
	}
	return mh
}

// canonicalTidal is the mission's `tidalstrength` unless it is < 0.0 (strict),
// else 0.5 [03 §2.2] C4.
func canonicalTidal(mh *content.MapHeader) float32 {
	g := canonicalGlobals(mh)
	if g == nil {
		return 0.5
	}
	tidal := float32(g.TidalStrength)
	if tidal < 0 {
		return 0.5
	}
	return tidal
}

// canonicalWindAndGravity resolves the canonical map's wind pair and gravity.
// The hard-coded 100/2000 and the 0x1FDB gravity fallback stand only for a
// negative authored value or an unparsed `[GlobalHeader]` [03 §2.2] C3/C4.
// 0x1FDB = 112*65536/900 = 8155 [fmt ota].
func canonicalWindAndGravity(mh *content.MapHeader) (windMin, windMax int32, gravity numeric.Fixed, authoredGravity int32) {
	windMin, windMax = 100, 2000
	g := canonicalGlobals(mh)
	if g != nil {
		if g.MinWindSpeed >= 0 {
			windMin = g.MinWindSpeed
		}
		if g.MaxWindSpeed >= 0 {
			windMax = g.MaxWindSpeed
		}
		if g.Gravity >= 0 {
			return windMin, windMax, gravityFromAuthored(g.Gravity), g.Gravity
		}
	}
	return windMin, windMax, numeric.Fixed(0x1FDB), 112
}

// Load loads terrain for mapKey through the VFS and catalog [03 §2.2].
// mapKey is the map basename (e.g. "ashap plateau") case-insensitively [02 §5].
// It validates the TNT version [03 §2.2] C2, expands tile data [03 §2.2] C5,
// and resolves wind/gravity/tidal per [03 §2.2] C3/C4 and sea level per C9.
func Load(fs vfs.FSOps, cat *content.Catalog, mapKey string) (*Terrain, error) {
	if fs == nil {
		return nil, fmt.Errorf("world: nil VFS")
	}
	key := strings.TrimSpace(mapKey)
	if key == "" {
		return nil, fmt.Errorf("world: empty map key")
	}
	var mh *content.MapHeader
	var logicalTNT string
	if cat != nil && cat.Maps != nil {
		if hdr, ok := cat.Maps[content.CanonicalKey(key)]; ok {
			mh = hdr
			logicalTNT = hdr.LogicalTNT
		}
	}
	if logicalTNT == "" {
		// Fallback logical path construction when catalog does not contain the key.
		// VFS paths are case-folded lower [vfs.path] and logical is maps/<key>.tnt.
		logicalTNT = "maps/" + strings.ToLower(key) + ".tnt"
	}
	data, err := fs.ReadFileLimit(logicalTNT, 32<<20)
	if err != nil {
		return nil, fmt.Errorf("world: %s: %w", logicalTNT, err)
	}
	if len(data) < 4 {
		return nil, fmt.Errorf("world: %s: file is too small", logicalTNT)
	}
	// Version gating lives in formats.LoadTNT, which is the only place that
	// knows which header slots each version uses [03 §2.2] C2/C3. Duplicating
	// the check here is how the two drifted apart in the first place.
	tnt, err := formats.LoadTNT(data)
	if err != nil {
		return nil, fmt.Errorf("world: %s: %w", logicalTNT, err)
	}
	ver := Version(tnt.Version)
	cellW := int32(tnt.Width)         // [03 §2.2]
	cellH := int32(tnt.Height)        // [03 §2.2]
	tileW := int32(tnt.TileMapWidth)  // Width/2 [03 §2.2] C5
	tileH := int32(tnt.TileMapHeight) // Height/2 [03 §2.2] C5
	// C5: tile indices row-major cellWidth/2 x cellHeight/2.
	expectedTiles := int(tileW) * int(tileH)
	if expectedTiles != len(tnt.TileIndices) {
		return nil, fmt.Errorf("world: tile indices mismatch %d vs %d", len(tnt.TileIndices), expectedTiles)
	}
	indices := make([]uint16, len(tnt.TileIndices))
	copy(indices, tnt.TileIndices)
	// C5: each tile selects a 1024-byte 32x32 block.
	tileSet := make([][1024]byte, int(tnt.Tiles))
	for i := range tileSet {
		off := i * 1024
		if off+1024 <= len(tnt.TileGraphics) {
			copy(tileSet[i][:], tnt.TileGraphics[off:off+1024])
		}
	}
	sea := uint8(tnt.SeaLevel) // header byte [03 §2.2] C9
	// Resolve wind/gravity per C3/C4 [03 §2.2].
	var windMin, windMax int32
	var gravity numeric.Fixed
	var authoredGravity int32
	tidal := canonicalTidal(mh)
	if ver == VersionLegacy {
		// Legacy (0x1020) carries minimum wind, maximum wind and gravity in
		// its own header and always uses those values — the OTA overrides do
		// not apply [03 §2.2] C3. The gravity integer converts like the OTA
		// key (*65536/900); its unit is the same authored data model
		// [fmt ota]. No fallback replacement for a legacy header gravity;
		// keep even if 0.
		windMin = int32(tnt.LegacyMinWind)
		windMax = int32(tnt.LegacyMaxWind)
		authoredGravity = int32(tnt.LegacyGravity)
		gravity = gravityFromAuthored(authoredGravity)
	} else {
		// [03 §2.2] C3: canonical hard-codes gravity 0, wind 100/2000, and an
		// authored non-negative OTA wind/gravity overrides the terrain value —
		// for canonical maps only.
		windMin, windMax, gravity, authoredGravity = canonicalWindAndGravity(mh)
	}
	// The acid-water pair is OTA-only on both terrain versions: a legacy TNT
	// header carries wind and gravity but no water words, so the mission's own
	// globals are the only source [fmt ota][04 §9.2].
	var waterDoesDamage, waterDamage int32
	if mh != nil {
		waterDoesDamage = mh.WaterDoesDamage
		waterDamage = mh.WaterDamage
	}
	// Plot expansion goes through the one path in plot.go [03 §2.2], [GAP T14].
	plot := ExpandPlot(tnt.Attributes, int(cellW), int(cellH))

	// Bind the map's feature records to catalog definitions. The names are
	// matched case-insensitively against the feature TDF sections [fmt tnt].
	names := make([]string, len(tnt.FeatureTable))
	defs := make([]*content.FeatureDef, len(tnt.FeatureTable))
	for i, rec := range tnt.FeatureTable {
		names[i] = rec.Name
		if cat != nil && cat.Features != nil {
			if def, ok := cat.Features[content.CanonicalKey(rec.Name)]; ok {
				defs[i] = def
			}
		}
	}

	otaGravity := int32(-1) // mission header initializer [04 R-AIR-01 §8]
	if g := canonicalGlobals(mh); g != nil {
		otaGravity = g.Gravity
	}
	t := &Terrain{
		CellW:           cellW,
		CellH:           cellH,
		Version:         ver,
		TileIndices:     indices,
		TileSet:         tileSet,
		Plot:            plot,
		SeaLevel:        sea,
		Gravity:         gravity,
		AuthoredGravity: authoredGravity,
		OTAGravity:      otaGravity,
		LavaWorld:       mh != nil && mh.LavaWorld != 0,
		// The two acid-water words come from the same compiled globals, with
		// the parser's own integer default 0 for an omitted key [fmt ota].
		// A map the catalog does not carry supplies neither, which is the
		// authored-zero case: no water damage [04 §9.2].
		WaterDoesDamage: waterDoesDamage,
		WaterDamage:     waterDamage,
		WindMin:         windMin,
		WindMax:         windMax,
		Tidal:           tidal,
		FeatureNames:    names,
		FeatureDefs:     defs,
	}
	// The terrain-ray height words are a load-time product. Build after the
	// derived height pair exists, before feature and void post-processing, and
	// never invalidate it during the battle [03 §3.5][R-P0-18-B §4].
	t.buildLOSHeightWords()
	t.stampFeatureAnchors()
	t.applyVoidFixup(mh)

	return t, nil
}

// PlayInsets returns a map's playable extents from its cell counts:
// PlayRight = Wpix − 32 and PlayBottom = Hpix − 128, with Wpix = cellW·16 and
// Hpix = cellH·16 [03 §3.4][P0-17]. They are the camera clamp's map size and
// the minimap lens's divisors, not the raw pixel dimensions. applyVoidFixup
// writes them onto the terrain as rule 1 of its sweep.
func PlayInsets(cellW, cellH int32) (playRight, playBottom int32) {
	return cellW*16 - 32, cellH*16 - 128
}

// applyVoidFixup is the loader's edge/lava void sweep [03 R-TERR-01 §2].
//
// It runs once, after the derived floor pair exists (ExpandPlot's
// deriveFloorPair — rule 5 reads the derived minimum) and after the feature
// stamp, which is why Load calls it last. Void writes ONE thing and nothing
// else: the feature word at bytes 0x08/0x09 becomes 0xFFFD. No height byte, no
// derived pair, no occupancy word and no flag bit is touched, and the sweep
// only ever converts cells whose feature word is empty (0xFFFF) or fringe
// (0xFFFE) — a live index, an anchor and an already-void cell all survive
// unchanged [03 R-TERR-01 §1 "Reader census"][03 R-TERR-01 §2].
//
// Four rules in this order, after rule 1's play insets:
//
//  1. Play insets. PlayRight = Width*16 − 32, PlayBottom = Height*16 − 128,
//     written here and read by the camera clamp and the minimap lens.
//  2. Right columns. For every row z, cells (W−2, z) and (W−1, z).
//  3. North strip. Per column, walk rows z = 0, 1, 2, … in order and STOP at
//     the first row for which z*16 − (height >> 1) >= 0; every row before the
//     stop is voided, and the tested and the voided cell are the same. Since
//     height>>1 <= 127, row 7 is the deepest reachable (112 < 127) and row 8
//     never is.
//  4. South strip, one row ABOVE the tested row. Per column, walk rows
//     z = H−1, H−2, … in order and STOP at the first row for which
//     z*16 − (height >> 1) <= PlayBottom; for every row before the stop the
//     cell voided is (x, z−1). Equivalently row z is tested with
//     (H−1−z)*16 + (height >> 1) < 112. So the bottom row H−1 is never voided
//     by this rule — only rule 2 can void it, in its two columns — row H−2 is
//     voided when the bottom row's height is below 224, and the walk cannot
//     reach past row H−8.
//  5. Lava flood. When the mission's lavaworld is non-zero, every convertible
//     cell whose derived MINIMUM byte is <= SeaLevel (unsigned byte compare).
//
// Rule order is immaterial to the result — every rule writes the same value and
// none of the walks' stop tests read the feature word — but it is kept as the
// spec states it so the two can be compared line by line.
//
// Corrected (WU-19-48). The previous implementation carried doc 02's earlier
// summary of rules 3 and 4, which [03 R-TERR-01 §2] corrects: it applied both
// height predicates to every row of every column independently, with no stop,
// and at the south edge it voided the row it had tested. Both are wrong in the
// same direction — they void cells retail leaves alone. Without the stop a high
// cell anywhere in rows 1..7 (or in the bottom eight rows) voids its own row
// even though the walk had already halted above it; and voiding the tested row
// rather than the row above it voids one extra row at the south edge of every
// map, including the bottom row, which retail never voids.
//
// Map-authored void sentinels (0xFFFC, stamped by the feature pass) still load
// verbatim; only these engine-derived voids are added here.
func (t *Terrain) applyVoidFixup(mh *content.MapHeader) {
	if t == nil || t.Plot == nil || t.CellW <= 0 || t.CellH <= 0 {
		return
	}
	// Rule 1, play insets [03 R-TERR-01 §2]: Wpix = CellW*16, Hpix = CellH*16.
	t.PlayRight, t.PlayBottom = PlayInsets(t.CellW, t.CellH)
	// The sweep's one conversion gate, shared by all four rules
	// [03 R-TERR-01 §2].
	voidIfConvertible := func(cell *PlotCell) {
		if f := cell.Feature(); f == PlotFeatureNone || f == PlotFeatureFringe {
			cell.SetFeature(PlotFeatureVoid)
		}
	}
	// Rule 2, right columns: (W−2, z) and (W−1, z) for every row
	// [03 R-TERR-01 §2].
	for cz := int32(0); cz < t.CellH; cz++ {
		for cx := t.CellW - 2; cx < t.CellW; cx++ {
			if cx < 0 {
				continue
			}
			voidIfConvertible(&t.Plot[cz*t.CellW+cx])
		}
	}
	// Rule 3, north strip: per column, stop at the first row whose
	// z*16 − (height>>1) is non-negative; void every row before it
	// [03 R-TERR-01 §2].
	for cx := int32(0); cx < t.CellW; cx++ {
		for cz := int32(0); cz < t.CellH; cz++ {
			cell := &t.Plot[cz*t.CellW+cx]
			if cz*16-int32(cell.Height()>>1) >= 0 {
				break
			}
			voidIfConvertible(cell)
		}
	}
	// Rule 4, south strip: per column, walk upward from the bottom row and stop
	// at the first row whose z*16 − (height>>1) is at or below PlayBottom; while
	// it does not stop, the cell voided is the row ABOVE the tested one
	// [03 R-TERR-01 §2].
	for cx := int32(0); cx < t.CellW; cx++ {
		// The walk stops at or before row 0 on any map at least eight cells
		// tall (at z = 0 continuing would need −(height>>1) > H*16 − 128 >= 0,
		// which no height satisfies), so the z >= 1 bound never truncates a
		// real map. It is ours, and it exists so an authored fixture shorter
		// than that cannot walk off the front of the plot.
		for cz := t.CellH - 1; cz >= 1; cz-- {
			cell := &t.Plot[cz*t.CellW+cx]
			if cz*16-int32(cell.Height()>>1) <= t.PlayBottom {
				break
			}
			voidIfConvertible(&t.Plot[(cz-1)*t.CellW+cx])
		}
	}
	// Rule 5, lava flood: derived minimum <= SeaLevel, unsigned byte compare
	// [03 R-TERR-01 §2].
	if mh != nil && mh.LavaWorld != 0 {
		for i := range t.Plot {
			// hmin is the derived floor minimum at byte 0x06 [03 R-TERR-01 §1].
			if t.Plot[i].MinHeight() <= t.SeaLevel {
				voidIfConvertible(&t.Plot[i])
			}
		}
	}
}
