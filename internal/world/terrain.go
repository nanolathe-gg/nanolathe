// Package world provides authoritative world geometry and terrain loading [03 §2.2].
package world

import (
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/vfs"
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
	TileIndices  []uint16      // (CellW/2) x (CellH/2) row-major [03 §2.2] C5
	TileSet      [][1024]byte  // Tiles x 1024 bytes [03 §2.2] C5
	Plot         []PlotCell    // CellW x CellH row-major [03 §2.2] C6
	SeaLevel     uint8         // header byte [03 §2.2] C9
	Gravity      numeric.Fixed // per-tick gravity [03 §2.2] C4
	WindMin      int32         // [03 §2.2] C3/C4
	WindMax      int32         // [03 §2.2] C3/C4
	Tidal        numeric.Fixed // [03 §2.2] C4

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

	// metalSeeded records whether ApplySchema has run. SampleMetal refuses to
	// answer before it has: an unseeded metal field reads as zero everywhere,
	// which is indistinguishable from a genuinely metal-free map and silently
	// makes every extractor's yield wrong [05 "Terrain metal extraction"].
	metalSeeded bool
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
// metal value [05 "Terrain metal extraction"]: "When the mission provides a
// uniform surface-metal value, map loading initializes the cell metal field
// from it; maps can also supply per-cell values."
//
// The value is per-schema in the OTA, and schema selection is a battle-setup
// decision [08], so it cannot happen inside Load. Battle setup must call this
// before any extractor is placed; SampleMetal fails until it does.
//
// TODO(question): "maps can also supply per-cell values" is established, but
// where a per-cell metal map is stored is not. No retail map in the reference
// install carries one that we can identify, so only the uniform path exists
// here.
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
	if value > 255 {
		value = 255 // the cell field is one unsigned byte [02 "Terrain file"]
	}
	for i := range t.Plot {
		t.Plot[i][7] = uint8(value)
	}
	t.metalSeeded = true
	return nil
}

// stampFeatureAnchors writes each fringe cell's offset back to its anchor.
//
// A feature covering more than one cell stores its record index in the anchor
// cell — the top-left corner of its footprint — and fills the rest with the
// fringe sentinel [fmt tnt]. Fringe cells carry no index of their own, so every
// consumer resolves them through the anchor [04 §6.2]. The TNT attribute record
// has no anchor field (height, feature reference, one unknown byte [fmt tnt]),
// so the engine must derive the offsets at load; without this pass they stay
// zero and each fringe cell resolves to itself, i.e. to nothing.
//
// The derivation propagates in row-major order — the deterministic map order of
// [01 §6.1]. A fringe cell inherits from its left and upper neighbours,
// preferring whichever anchor comes later in that order, which is what keeps
// two features packed against each other from bleeding into one another.
//
// TODO(question): research states the anchor relationship and the resolver's
// behaviour [fmt tnt], [04 §6.2] but never says how the engine reconstructs the
// offsets. Measured against the reference install (275 maps, 71,916 fringe
// cells):
//
//   - Stamping from the declared FBI footprint resolves 65.1%. The footprint is
//     not the rule: metaltower10 declares 1x2 while the TNT marks a 4x4 region
//     as covered.
//   - This propagation resolves 83.2%.
//   - The residual is not recoverable locally. On dense maps (pincushion,
//     cloaked in the spires — together 83% of it) adjacent features' fringe
//     regions merge into one 4-connected blob: bounding boxes reach 10x9 with
//     seven real cells inside, so no local rule can partition them. A further
//     5,283 cells lie in blobs with no real cell anywhere near, i.e. orphaned
//     source data.
//
// An unresolved fringe cell is a legitimate state, not a failure: [04 §6.2]
// makes an unresolvable reference blocking for yard bit 5 and non-satisfying
// for bit 7, which is what placement does. A probe against retail would be
// needed to settle the real rule.
func (t *Terrain) stampFeatureAnchors() {
	if t.CellW <= 0 || t.CellH <= 0 || len(t.Plot) < int(t.CellW*t.CellH) {
		return
	}
	// anchorOf[i] is the plot index of the anchor owning cell i, or -1.
	anchorOf := make([]int32, len(t.Plot))
	for i := range anchorOf {
		anchorOf[i] = -1
	}
	for cz := int32(0); cz < t.CellH; cz++ {
		for cx := int32(0); cx < t.CellW; cx++ {
			i := cz*t.CellW + cx
			f := t.Plot[i].Feature()
			if f < plotFeatureRealLimit {
				anchorOf[i] = i // a real index anchors itself
				continue
			}
			if f != PlotFeatureFringe {
				continue
			}
			owner := int32(-1)
			if cx > 0 {
				owner = anchorOf[i-1]
			}
			if cz > 0 {
				if above := anchorOf[i-t.CellW]; above > owner {
					// Later in row-major order wins. Two features packed
					// against each other both reach this cell through a
					// neighbour; the one whose anchor comes later is the one
					// whose rectangle starts here, because the earlier
					// rectangle would have had to run past the later anchor's
					// own origin to claim it.
					owner = above
				}
			}
			if owner < 0 {
				continue // orphaned sentinel; stays unresolved [04 §6.2]
			}
			anchorOf[i] = owner
			dx := owner%t.CellW - cx
			dz := owner/t.CellW - cz
			if dx < -128 || dx > 127 || dz < -128 || dz > 127 {
				// The offsets are one signed byte each [02 "Terrain file"].
				continue
			}
			t.Plot[i].SetAnchorSigned(int8(dx), int8(dz))
		}
	}
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
// Points outside the world rectangle sample nothing and return 0. Edge neighbors are
// clamped to the available corners.
func (t *Terrain) HeightAt(x, z numeric.Fixed) numeric.Fixed {
	if t == nil || t.Plot == nil || t.CellW <= 0 || t.CellH <= 0 {
		return 0
	}
	cx := WorldToCell(x) // floor semantics with sign correction [03 §2.1] C1
	cz := WorldToCell(z) // floor semantics with sign correction [03 §2.1] C1
	if cx < 0 || cz < 0 || cx >= t.CellW || cz >= t.CellH {
		return 0
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
	// Four-corner heights with edge clamping [03 §2.3] C7.
	w := t.CellW
	h := t.CellH
	base := int(cz*w + cx)
	h00 := int32(t.Plot[base].Height())
	var h10, h01, h11 int32
	h10 = h00
	if cx+1 < w {
		h10 = int32(t.Plot[int(cz*w+cx+1)].Height())
	}
	h01 = h00
	h11 = h00
	if cz+1 < h {
		h01 = int32(t.Plot[int((cz+1)*w+cx)].Height())
		if cx+1 < w {
			h11 = int32(t.Plot[int((cz+1)*w+cx+1)].Height())
		} else {
			h11 = h01
		}
	}
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
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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

// LOSHeightAt returns the 32-pixel quantized height for visibility tile (vx,vz)
// [03 §2.3] C8.
//
// The LOS writer uses its own, coarser height representation: it "quantizes to
// 32-pixel visibility tiles and reads aggregated terrain heights; it does not
// use the four-corner bilinear query" [03 §2.3]. A visibility tile covers 2x2
// attribute cells [03 §2.1]. It must not be substituted for HeightAt, and a
// tall feature does not raise it — only terrain data does [03 §2.3].
//
// TODO(question): [03 §2.3] establishes that the value is aggregated over the
// tile but does not name the aggregate. This returns the maximum of the four
// covered cells, which is the only choice that behaves like terrain occlusion:
// a ridge crossing one cell of a tile must block the ray, and averaging would
// let sight pass through it. Not attested — phase 5 owns the decision and
// PLAN_05 records it as an open input.
// LOSHeightWord returns the aggregated two-byte terrain word for visibility
// tile (vx,vz) as (low, high) [03 §3.2] C5.
//
// The terrain-ray horizon rule uses both bytes for different things: the LOW
// byte supplies the candidate difference that gates admission, and the HIGH
// byte is tested with the identical comparison afterwards to decide whether the
// retained horizon advances.
//
// TODO(question): [03 §2.3] establishes that the value is aggregated over the
// tile and [03 §3.2] that the word has two distinct bytes, but neither names
// the aggregates. This returns (minimum, maximum) over the tile's four cells,
// which is the only pairing that makes both uses coherent — sight passes over
// the lowest point of a tile, while the horizon it leaves behind rises to the
// highest. Equal bytes would make the high-byte test algebraically identical
// to the admission test and therefore dead, which is how the previous
// single-byte implementation could never exercise it.
func (t *Terrain) LOSHeightWord(vx, vz int32) (low, high uint8) {
	if t == nil || t.Plot == nil || t.CellW <= 0 || t.CellH <= 0 {
		return 0, 0
	}
	cx, cz := vx*2, vz*2
	if cx < 0 || cz < 0 || cx >= t.CellW || cz >= t.CellH {
		return 0, 0
	}
	low, high = 255, 0
	seen := false
	for dz := int32(0); dz < 2; dz++ {
		for dx := int32(0); dx < 2; dx++ {
			px, pz := cx+dx, cz+dz
			if px >= t.CellW || pz >= t.CellH {
				continue
			}
			h := t.Plot[pz*t.CellW+px].Height()
			if h < low {
				low = h
			}
			if h > high {
				high = h
			}
			seen = true
		}
	}
	if !seen {
		return 0, 0
	}
	return low, high
}

func (t *Terrain) LOSHeightAt(vx, vz int32) uint8 {
	if t == nil || t.Plot == nil || t.CellW <= 0 || t.CellH <= 0 {
		return 0
	}
	cx, cz := vx*2, vz*2
	if cx < 0 || cz < 0 || cx >= t.CellW || cz >= t.CellH {
		return 0
	}
	high := uint8(0)
	for dz := int32(0); dz < 2; dz++ {
		for dx := int32(0); dx < 2; dx++ {
			px, pz := cx+dx, cz+dz
			if px >= t.CellW || pz >= t.CellH {
				continue
			}
			if h := t.Plot[pz*t.CellW+px].Height(); h > high {
				high = h
			}
		}
	}
	return high
}

// gravityFromAuthored converts an authored OTA/TNT gravity integer into
// per-tick Fixed 16.16 world units via *65536/900 [fmt ota] [03 §2.2].
// Uses trunc toward zero [INVARIANTS I3].
func gravityFromAuthored(authored int32) numeric.Fixed {
	return numeric.Fixed(int64(authored) * 65536 / 900)
}

// tidalFromFloat converts authored tidalstrength float to Fixed 16.16
// via *65536 truncated toward zero [03 §2.2].
func tidalFromFloat(v float64) numeric.Fixed {
	return numeric.Fixed(int64(v * 65536))
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
	var tidal numeric.Fixed
	// Tidal: OTA tidalstrength, fallback 0.5 [03 §2.2] C4.
	// Presence matters: authored 0 vs missing.
	if mh != nil && mh.RawOTA != nil && mh.RawOTA.Global != nil {
		if _, ok := mh.RawOTA.Global.RawValue("tidalstrength"); ok {
			// Authored value present; 0 is valid explicit 0.
			tidal = tidalFromFloat(mh.TidalStrength)
		} else {
			tidal = numeric.Fixed(32768) // 0.5 *65536 [03 §2.2] C4
		}
	} else {
		tidal = numeric.Fixed(32768) // fallback when no catalog/OTA [03 §2.2] C4
	}
	{
		// [03 §2.2] C3: canonical hard-codes gravity 0, wind 100/2000, and an
		// authored non-negative OTA wind/gravity overrides the terrain value —
		// for canonical maps only. The legacy branch that read wind and gravity
		// from header slots 10/11/13 is gone: formats.LoadTNT now rejects
		// version 0x1020 outright because the width of its attribute record is
		// unrecovered [02 "Terrain file"], so no legacy terrain reaches here.
		windMin = 100
		windMax = 2000
		gravity = numeric.Fixed(0)
		gravitySupplied := false
		if mh != nil && mh.RawOTA != nil && mh.RawOTA.Global != nil {
			g := mh.RawOTA.Global
			if _, ok := g.RawValue("minwindspeed"); ok {
				if mh.MinWindSpeed >= 0 {
					windMin = mh.MinWindSpeed
				}
			}
			if _, ok := g.RawValue("maxwindspeed"); ok {
				if mh.MaxWindSpeed >= 0 {
					windMax = mh.MaxWindSpeed
				}
			}
			if _, ok := g.RawValue("gravity"); ok {
				if mh.Gravity >= 0 {
					gravity = gravityFromAuthored(mh.Gravity)
					gravitySupplied = true
				} else {
					gravitySupplied = false
				}
			} else {
				gravitySupplied = false
			}
		}
		if !gravitySupplied {
			// [03 §2.2] C4: when neither source supplies gravity fallback to 0x1FDB.
			// 0x1FDB = 112*65536/900 = 8155 [fmt ota].
			gravity = numeric.Fixed(0x1FDB)
		}
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

	t := &Terrain{
		CellW:        cellW,
		CellH:        cellH,
		Version:      ver,
		TileIndices:  indices,
		TileSet:      tileSet,
		Plot:         plot,
		SeaLevel:     sea,
		Gravity:      gravity,
		WindMin:      windMin,
		WindMax:      windMax,
		Tidal:        tidal,
		FeatureNames: names,
		FeatureDefs:  defs,
	}
	t.stampFeatureAnchors()

	// TODO(question): PLAN_04's Divergences section commits to deriving void
	// and lava edges after terrain materialization — "two eastmost reserve
	// columns plus the lava flood" — carried over from a prior implementation.
	// Research supports only that void cells exist and describes them as
	// "lava-world fill and map-edge strips" [02 "Terrain file"]; neither the
	// two-column width, the eastmost side, nor the flood rule appears in
	// [03 §2.2] or anywhere else. Implementing it would be inventing three
	// constants, so it is left undone and PLAN_04's Divergences entry is
	// downgraded to this unknown. Map-authored void sentinels still load
	// verbatim; only engine-derived edges are missing.

	return t, nil
}
