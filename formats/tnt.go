package formats

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/nanolathe/nanolathe/vfs"
)

// Version is the TNT version word [03 §2.2] [fmt tnt].
type Version uint32

const (
	VersionLegacy    Version = 0x1020 // [03 §2.2]
	VersionCanonical Version = 0x2000 // [03 §2.2]
)

// featureSentinelBase is the exclusive upper bound for real feature-record
// indices in an attribute cell. Consumers test below it before dereferencing,
// so 0xFFFB..0xFFFF is a sentinel band rather than an index range
// [02 "Terrain file"], [GAP T14].
const featureSentinelBase uint16 = 0xFFFB

// legacyFeatureSentinelBase is the legacy 8-byte record's sentinel band: a
// one-byte feature reference below 0xFC is a feature-table index, and 0xFC and
// above are void/none — a single sentinel band rather than the canonical
// quaternary [02 "Terrain file"].
const legacyFeatureSentinelBase uint16 = 0xFC

// TNTLimits bounds allocations made while decoding untrusted map data. The
// defaults are deliberately large enough for the retail corpus, but finite.
type TNTLimits struct {
	MaxWidth, MaxHeight uint32
	MaxCells            uint64
	MaxTiles            uint32
	MaxFeatureRecords   uint32
	MaxMinimapPixels    uint64
}

// DefaultTNTLimits returns the decode bounds used when a caller states none.
func DefaultTNTLimits() TNTLimits {
	return TNTLimits{
		MaxWidth: 8192, MaxHeight: 8192, MaxCells: 16 << 20, MaxTiles: 1 << 20,
		MaxFeatureRecords: 1 << 16, MaxMinimapPixels: 16 << 20,
	}
}

// TNTAttribute is one 16x16 source cell [P0-17][P1-15].
// Canonical: W*H*4 attribute records (height + u16 feature + unk0); Feature is
// intentionally uint16 — 0xfffc, 0xfffe and 0xffff are distinct source
// sentinels [P0-17][fmt tnt] and 0xFFFD is the derived void [P1-15].
// Legacy (0x1020): W*H*8 records; byte 0 height, byte 2 a one-byte feature
// reference normalized to the canonical sentinel band here (the 0xFC+ band is
// void/none [02 "Terrain file"]), byte 6 the per-cell metal seed; bytes
// 1, 3, 4, 5, 7 never read. Metal is zero on canonical records, where the
// per-cell metal is seeded uniformly from the mission SurfaceMetal scalar at
// load [02 "Terrain file"].
type TNTAttribute struct {
	Height  byte
	Feature uint16
	Unknown byte // canonical byte 3: zero across the retail corpus, not carried [P1-15]
	Metal   byte // legacy byte 6: per-cell metal seed; zero on canonical [02 "Terrain file"]
}

// TNTFeatureRecord is one entry of a terrain file's feature-name table
// [fmt tnt].
type TNTFeatureRecord struct {
	Index uint32
	Name  string
}

// TNT contains the complete simulation-relevant source data from a .tnt
// file. No terrain interpretation is performed here.
type TNT struct {
	// Raw owns the single decompressed source backing store. TileGraphics and
	// Minimap are validated views into it; callers must treat them as immutable.
	Raw       []byte
	Version   uint32
	Width     uint32
	Height    uint32
	Tiles     uint32
	TileAnims uint32
	SeaLevel  uint32

	PtrMapData   uint32
	PtrMapAttr   uint32
	PtrTileGfx   uint32
	PtrTileAnims uint32

	// Header slots 10..15 are version-dependent [03 §2.2], [02 "Terrain file"].
	// Canonical (0x2000): slot 10 is the minimap offset and slot 11 bit 0 its
	// present flag; wind and gravity are hard-coded, not stored.
	// Legacy (0x1020): slots 10, 11 and 13 carry minimum wind, maximum wind and
	// gravity; the minimap offset moves to slot 14 and its flag to slot 15 bit 0.
	//
	// Read these rather than indexing raw slots: reading slot 10 as an offset on
	// a legacy file seeks to the minimum wind speed.
	MiniMapOffset  uint32
	MiniMapPresent bool

	// Legacy-only. Zero on canonical files, where the engine hard-codes wind
	// 100/2000 and gravity 0 instead [03 §2.2].
	LegacyMinWind uint32
	LegacyMaxWind uint32
	LegacyGravity uint32

	// Unknown1 is header slot 11 on canonical files — always 1 across the retail
	// corpus, meaning unknown [fmt tnt].
	Unknown1 uint32

	TileMapWidth   uint32
	TileMapHeight  uint32
	TileIndices    []uint16
	TileIndexBytes []byte
	Attributes     []TNTAttribute
	TileGraphics   []byte // Tiles * 32 * 32 palette indices, row-major.
	FeatureTable   []TNTFeatureRecord

	MinimapWidth  uint32
	MinimapHeight uint32
	Minimap       []byte
}

// LoadTNT decodes a terrain file under the default limits [fmt tnt].
func LoadTNT(data []byte) (*TNT, error) {
	return LoadTNTWithLimits(data, DefaultTNTLimits())
}

// LoadTNTWithLimits decodes a terrain file under explicit bounds [fmt tnt].
func LoadTNTWithLimits(data []byte, limits TNTLimits) (*TNT, error) {
	// Mandatory TNT version 0x2000/0x1020 fatal, other versions fatal diagnostic [P1-02 §2.2][fmt tnt][03 §2.2].
	if len(data) < 0x40 {
		return nil, fmt.Errorf("tnt: file is too small")
	}
	if limits.MaxWidth == 0 || limits.MaxHeight == 0 || limits.MaxCells == 0 || limits.MaxTiles == 0 || limits.MaxFeatureRecords == 0 || limits.MaxMinimapPixels == 0 {
		return nil, fmt.Errorf("tnt: invalid decode limits")
	}
	// Copy the decompressed source exactly once so all validated presentation
	// views remain owned by the immutable source record after the loader
	// returns. Simulation arrays are materialized separately below.
	data = append([]byte(nil), data...)
	u32 := func(off int) uint32 { return binary.LittleEndian.Uint32(data[off : off+4]) }
	result := &TNT{
		Raw:          data,
		Version:      u32(0),
		Width:        u32(0x04),
		Height:       u32(0x08),
		PtrMapData:   u32(0x0c),
		PtrMapAttr:   u32(0x10),
		PtrTileGfx:   u32(0x14),
		Tiles:        u32(0x18),
		TileAnims:    u32(0x1c),
		PtrTileAnims: u32(0x20),
		SeaLevel:     u32(0x24),
	}
	// Slots 10..15 are version-dependent [03 §2.2]. Resolve them before any
	// section offset is used.
	switch Version(result.Version) {
	case VersionCanonical:
		result.MiniMapOffset = u32(0x28) // slot 10
		result.Unknown1 = u32(0x2c)      // slot 11, always 1 in the retail corpus
		result.MiniMapPresent = u32(0x2c)&1 != 0
	case VersionLegacy:
		result.LegacyMinWind = u32(0x28) // slot 10
		result.LegacyMaxWind = u32(0x2c) // slot 11
		result.LegacyGravity = u32(0x34) // slot 13
		result.MiniMapOffset = u32(0x38) // slot 14
		result.MiniMapPresent = u32(0x3c)&1 != 0
	default:
		// [03 §2.2][P1-02 §2.2]: the loader accepts exactly two versions and rejects any
		// other version word with a diagnostic — mandatory TNT version 0x2000/0x1020 fatal.
		return nil, fmt.Errorf("tnt: unsupported version 0x%04x, want 0x1020 or 0x2000 [03 §2.2][P1-02 §2.2]", result.Version)
	}
	if result.Width == 0 || result.Height == 0 || result.Width > limits.MaxWidth || result.Height > limits.MaxHeight {
		return nil, fmt.Errorf("tnt: dimensions %dx%d exceed limits", result.Width, result.Height)
	}
	if result.Width%2 != 0 || result.Height%2 != 0 {
		return nil, fmt.Errorf("tnt: dimensions must be even, got %dx%d", result.Width, result.Height)
	}
	if result.Tiles > limits.MaxTiles || result.TileAnims > limits.MaxFeatureRecords {
		return nil, fmt.Errorf("tnt: counts exceed limits")
	}
	result.TileMapWidth, result.TileMapHeight = result.Width/2, result.Height/2

	section := func(name string, off uint32, count uint64, stride uint64) ([]byte, error) {
		if count != 0 && count > math.MaxUint64/stride {
			return nil, fmt.Errorf("tnt: %s size overflows", name)
		}
		size := count * stride
		if uint64(off) > uint64(len(data)) || size > uint64(len(data))-uint64(off) {
			return nil, fmt.Errorf("tnt: %s is outside file", name)
		}
		return data[int(off):int(uint64(off)+size)], nil
	}
	attrCount := uint64(result.Width) * uint64(result.Height)
	if result.Width != 0 && attrCount/uint64(result.Width) != uint64(result.Height) {
		return nil, fmt.Errorf("tnt: attribute dimensions overflow")
	}
	if attrCount > limits.MaxCells {
		return nil, fmt.Errorf("tnt: attribute cells %d exceed limit %d", attrCount, limits.MaxCells)
	}
	tileCount := uint64(result.TileMapWidth) * uint64(result.TileMapHeight)
	if result.TileMapWidth != 0 && tileCount/uint64(result.TileMapWidth) != uint64(result.TileMapHeight) {
		return nil, fmt.Errorf("tnt: tile map dimensions overflow")
	}
	indexBytes, err := section("tile index map", result.PtrMapData, tileCount, 2)
	if err != nil {
		return nil, err
	}
	result.TileIndices = make([]uint16, int(tileCount))
	result.TileIndexBytes = indexBytes
	for i := range result.TileIndices {
		result.TileIndices[i] = binary.LittleEndian.Uint16(indexBytes[i*2:])
		if uint32(result.TileIndices[i]) >= result.Tiles {
			return nil, fmt.Errorf("tnt: tile index %d at cell %d exceeds tile count %d", result.TileIndices[i], i, result.Tiles)
		}
	}
	attrStride := uint64(4)
	if Version(result.Version) == VersionLegacy {
		attrStride = 8 // legacy 8-byte record [02 "Terrain file"]
	}
	attrBytes, err := section("attribute map", result.PtrMapAttr, attrCount, attrStride)
	if err != nil {
		return nil, err
	}
	result.Attributes = make([]TNTAttribute, int(attrCount))
	for i := range result.Attributes {
		b := attrBytes[i*int(attrStride):]
		var feature uint16
		var metal byte
		var unknown byte
		if Version(result.Version) == VersionLegacy {
			// Legacy record: byte 0 height, byte 2 a one-byte feature
			// reference with a single 0xFC+ sentinel band (void/none), byte 6
			// the per-cell metal seed; bytes 1, 3, 4, 5, 7 never read
			// [02 "Terrain file"]. The band is expanded to the canonical
			// empty sentinel: the legacy record does not distinguish empty
			// from void, so mapping the band to void would block movement on
			// featureless terrain.
			if b[2] >= byte(legacyFeatureSentinelBase) {
				feature = 0xFFFF // canonical empty sentinel [02 "Terrain file"]
			} else {
				feature = uint16(b[2])
			}
			metal = b[6]
		} else {
			feature = binary.LittleEndian.Uint16(b[1:3])
			unknown = b[3]
		}
		result.Attributes[i] = TNTAttribute{Height: b[0], Feature: feature, Unknown: unknown, Metal: metal}
		// Values at or above 0xFFFB are the sentinel band, not indices: 0xFFFF
		// empty, 0xFFFE footprint fringe, 0xFFFD void hole, with 0xFFFB/0xFFFC
		// acting as further void thresholds because consumers test below 0xFFFB
		// before dereferencing [02 "Terrain file"], [GAP T14]. On disk the
		// retail corpus uses 0xFFFC for void [fmt tnt]; accept the whole band.
		// Legacy indices were already normalized below the band above.
		feature = result.Attributes[i].Feature
		if feature < featureSentinelBase && uint32(feature) >= result.TileAnims {
			return nil, fmt.Errorf("tnt: feature index %d at cell %d exceeds feature record count %d", feature, i, result.TileAnims)
		}
	}
	graphics, err := section("tile graphics", result.PtrTileGfx, uint64(result.Tiles), 1024)
	if err != nil {
		return nil, err
	}
	result.TileGraphics = graphics
	featureBytes, err := section("feature table", result.PtrTileAnims, uint64(result.TileAnims), 132)
	if err != nil {
		return nil, err
	}
	result.FeatureTable = make([]TNTFeatureRecord, int(result.TileAnims))
	for i := range result.FeatureTable {
		b := featureBytes[i*132 : (i+1)*132]
		index := binary.LittleEndian.Uint32(b)
		nameBytes := b[4:]
		end := 0
		for end < len(nameBytes) && nameBytes[end] != 0 {
			end++
		}
		result.FeatureTable[i] = TNTFeatureRecord{Index: index, Name: string(nameBytes[:end])}
	}
	if result.MiniMapPresent {
		minimapHeader, err := section("minimap header", result.MiniMapOffset, 1, 8)
		if err != nil {
			return nil, err
		}
		result.MinimapWidth = binary.LittleEndian.Uint32(minimapHeader)
		result.MinimapHeight = binary.LittleEndian.Uint32(minimapHeader[4:])
		pixels := uint64(result.MinimapWidth) * uint64(result.MinimapHeight)
		if result.MinimapWidth != 0 && pixels/uint64(result.MinimapWidth) != uint64(result.MinimapHeight) {
			return nil, fmt.Errorf("tnt: minimap dimensions overflow")
		}
		if pixels > limits.MaxMinimapPixels {
			return nil, fmt.Errorf("tnt: minimap exceeds limits")
		}
		if result.MiniMapOffset > ^uint32(0)-8 {
			return nil, fmt.Errorf("tnt: minimap offset overflows")
		}
		minimap, err := section("minimap pixels", result.MiniMapOffset+8, pixels, 1)
		if err != nil {
			return nil, err
		}
		result.Minimap = minimap
	}
	return result, nil
}

// LoadTNTFile reads and decodes a terrain file from the VFS.
func LoadTNTFile(fs *vfs.FS, name string) (*TNT, error) {
	data, err := readVFS(fs, name)
	if err != nil {
		return nil, err
	}
	return LoadTNT(data)
}
