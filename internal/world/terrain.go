// Package world provides authoritative world geometry and terrain loading [03 §2.2].
package world

import (
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/vfs"
)

// Version is the TNT version discriminant [03 §2.2] C2.
//
//	VersionLegacy    = 0x1020
//	VersionCanonical = 0x2000
type Version uint8

const (
	VersionLegacy    Version = iota // 0x1020 [03 §2.2] C3
	VersionCanonical                // 0x2000 [03 §2.2] C3
)

const (
	versionWordLegacy    uint32 = 0x1020 // [03 §2.2] C2
	versionWordCanonical uint32 = 0x2000 // [03 §2.2] C2
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

// LOSHeightAt returns the 32-pixel quantized height for visibility tile (vx,vz) [03 §2.3] C8.
// The LOS writer quantizes to 32-pixel visibility tiles and reads aggregated
// terrain heights; it does not use the four-corner bilinear query. Each
// visibility tile covers 2×2 cells (32 map pixels) [03 §2.1]. This query samples
// the cell at the tile origin (vx*2, vz*2) without interpolation and returns the
// raw height byte. Out-of-bounds tiles return 0. It must not be substituted for HeightAt.
func (t *Terrain) LOSHeightAt(vx, vz int32) uint8 {
	if t == nil || t.Plot == nil || t.CellW <= 0 || t.CellH <= 0 {
		return 0
	}
	cx := vx * 2
	cz := vz * 2
	if cx < 0 || cz < 0 || cx >= t.CellW || cz >= t.CellH {
		return 0
	}
	idx := int(cz*t.CellW + cx)
	if idx < 0 || idx >= len(t.Plot) {
		return 0
	}
	return t.Plot[idx].Height()
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
	versionWord := binary.LittleEndian.Uint32(data[0:4])
	if versionWord != versionWordLegacy && versionWord != versionWordCanonical {
		return nil, fmt.Errorf("tnt: unsupported version 0x%04x [03 §2.2]", versionWord)
	}
	tnt, err := formats.LoadTNT(data)
	if err != nil {
		return nil, fmt.Errorf("world: %s: %w", logicalTNT, err)
	}
	var ver Version
	switch tnt.Version {
	case versionWordLegacy:
		ver = VersionLegacy
	case versionWordCanonical:
		ver = VersionCanonical
	default:
		// [03 §2.2] C2: rejects any other version word with a diagnostic.
		return nil, fmt.Errorf("tnt: unsupported version 0x%04x [03 §2.2]", tnt.Version)
	}
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
	if ver == VersionLegacy {
		// [03 §2.2] C3: legacy reads gravity/minWind/maxWind from header slots 13/10/11.
		// Header slots as parsed by formats/tnt.go [fmt tnt]:
		// slot10 (0x28) = PtrMiniMap field, slot11 (0x2C)=UnknownHeader[0],
		// slot13 (0x34)=UnknownHeader[2], minimap offset slot14=UnknownHeader[3],
		// flag slot15 bit0 =UnknownHeader[4].
		windMin = int32(tnt.PtrMiniMap)
		windMax = int32(tnt.UnknownHeader[0])
		gravInt := int32(tnt.UnknownHeader[2])
		gravity = gravityFromAuthored(gravInt)
		// Legacy keeps header values even if OTA would override [03 §2.2] C4.
		// No OTA wind/gravity override for legacy.
		// No fallback replacement for legacy header gravity; keep even if 0.
	} else {
		// [03 §2.2] C3: canonical hard-codes gravity 0, wind 100/2000.
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
	// Build Plot array [03 §2.2] [GAP T14]. Minimal for WU-04-2; full expansion in WU-04-3.
	plotCount := int(cellW) * int(cellH)
	plot := make([]PlotCell, plotCount)
	if len(tnt.Attributes) == plotCount {
		for i, attr := range tnt.Attributes {
			// Height at +4 [GAP T14].
			plot[i][4] = attr.Height
			// Feature at +8 little-endian [GAP T14].
			plot[i][8] = byte(attr.Feature & 0xff)
			plot[i][9] = byte(attr.Feature >> 8)
			// Min/Max derived later; leave zero for now.
			// Occupied flag +0xC bit 0 left zero.
		}
	}
	return &Terrain{
		CellW:       cellW,
		CellH:       cellH,
		Version:     ver,
		TileIndices: indices,
		TileSet:     tileSet,
		Plot:        plot,
		SeaLevel:    sea,
		Gravity:     gravity,
		WindMin:     windMin,
		WindMax:     windMax,
		Tidal:       tidal,
	}, nil
}
