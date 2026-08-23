package formats

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/nanolathe/nanolathe/vfs"
)

// TNTLimits bounds allocations made while decoding untrusted map data. The
// defaults are deliberately large enough for the retail corpus, but finite.
type TNTLimits struct {
	MaxWidth, MaxHeight uint32
	MaxCells            uint64
	MaxTiles            uint32
	MaxFeatureRecords   uint32
	MaxMinimapPixels    uint64
}

func DefaultTNTLimits() TNTLimits {
	return TNTLimits{
		MaxWidth: 8192, MaxHeight: 8192, MaxCells: 16 << 20, MaxTiles: 1 << 20,
		MaxFeatureRecords: 1 << 16, MaxMinimapPixels: 16 << 20,
	}
}

// TNTAttribute is one 16x16 source cell. Feature is intentionally uint16:
// 0xfffc, 0xfffe and 0xffff are distinct source sentinels.
type TNTAttribute struct {
	Height  byte
	Feature uint16
	Unknown byte
}

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

	PtrMapData    uint32
	PtrMapAttr    uint32
	PtrTileGfx    uint32
	PtrTileAnims  uint32
	PtrMiniMap    uint32
	UnknownHeader [5]uint32

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

func LoadTNT(data []byte) (*TNT, error) {
	return LoadTNTWithLimits(data, DefaultTNTLimits())
}

func LoadTNTWithLimits(data []byte, limits TNTLimits) (*TNT, error) {
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
		PtrMiniMap:   u32(0x28),
	}
	result.UnknownHeader[0] = u32(0x2c)
	for i := 1; i < len(result.UnknownHeader); i++ {
		result.UnknownHeader[i] = u32(0x30 + (i-1)*4)
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
	attrBytes, err := section("attribute map", result.PtrMapAttr, attrCount, 4)
	if err != nil {
		return nil, err
	}
	result.Attributes = make([]TNTAttribute, int(attrCount))
	for i := range result.Attributes {
		b := attrBytes[i*4:]
		result.Attributes[i] = TNTAttribute{Height: b[0], Feature: binary.LittleEndian.Uint16(b[1:3]), Unknown: b[3]}
		feature := result.Attributes[i].Feature
		if feature != 0xffff && feature != 0xfffe && feature != 0xfffc && uint32(feature) >= result.TileAnims {
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
	minimapHeader, err := section("minimap header", result.PtrMiniMap, 1, 8)
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
	if result.PtrMiniMap > ^uint32(0)-8 {
		return nil, fmt.Errorf("tnt: minimap offset overflows")
	}
	minimap, err := section("minimap pixels", result.PtrMiniMap+8, pixels, 1)
	if err != nil {
		return nil, err
	}
	result.Minimap = minimap
	return result, nil
}

func LoadTNTFile(fs *vfs.FS, name string) (*TNT, error) {
	data, err := readVFS(fs, name)
	if err != nil {
		return nil, err
	}
	return LoadTNT(data)
}
