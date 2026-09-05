package formats

import (
	"encoding/binary"
	"fmt"
)

// SCTLimits bounds allocations and validated views while decoding editor
// sections. SCT sections are source material for map editors rather than the
// runtime .TNT map format, so unknown metadata is retained as an opaque slice.
type SCTLimits struct {
	MaxWidth, MaxHeight uint32
	MaxGridCells        uint64
	MaxTiles            uint32
	MaxPreviewPixels    uint64
}

// DefaultSCTLimits returns the decode bounds used when a caller states none.
func DefaultSCTLimits() SCTLimits {
	return SCTLimits{
		MaxWidth: 2048, MaxHeight: 2048, MaxGridCells: 1 << 20,
		MaxTiles: 1 << 20, MaxPreviewPixels: 16 << 20,
	}
}

// SCT is the validated source-level representation of a TA editor section.
// The seven-word header is shared by the observed version 2 and version 3
// retail sections. Tile graphics and tile indices are fully decoded; the
// version-specific section metadata between them is preserved but not
// assigned invented gameplay semantics.
type SCT struct {
	Raw                  []byte
	Version              uint32
	SectionPreviewOffset uint32
	TileCount            uint32
	TileGraphicsOffset   uint32
	Width                uint32
	Height               uint32
	TileIndexOffset      uint32

	TileIndices    []uint16
	TileGraphics   []byte
	AttributeData  []byte
	SectionPreview []byte
}

// LoadSCT decodes an editor section file under the default limits.
func LoadSCT(data []byte) (*SCT, error) {
	return LoadSCTWithLimits(data, DefaultSCTLimits())
}

// LoadSCTWithLimits decodes an editor section file under explicit bounds.
func LoadSCTWithLimits(data []byte, limits SCTLimits) (*SCT, error) {
	const headerSize = 28

	if len(data) < 28 {
		return nil, fmt.Errorf("sct: header is truncated")
	}
	if limits.MaxWidth == 0 || limits.MaxHeight == 0 || limits.MaxGridCells == 0 || limits.MaxTiles == 0 || limits.MaxPreviewPixels == 0 {
		return nil, fmt.Errorf("sct: invalid decode limits")
	}
	// Keep the validated views owned by the returned source record. Callers
	// may reuse or mutate the input buffer after loading.
	data = append([]byte(nil), data...)
	read := func(offset int) uint32 { return binary.LittleEndian.Uint32(data[offset : offset+4]) }
	result := &SCT{
		Raw:                  data,
		Version:              read(0),
		SectionPreviewOffset: read(4),
		TileCount:            read(8),
		TileGraphicsOffset:   read(12),
		Width:                read(16),
		Height:               read(20),
		TileIndexOffset:      read(24),
	}
	if result.Version != 2 && result.Version != 3 {
		return nil, fmt.Errorf("sct: unsupported version %d", result.Version)
	}
	if result.Width == 0 || result.Height == 0 || result.Width > limits.MaxWidth || result.Height > limits.MaxHeight {
		return nil, fmt.Errorf("sct: dimensions %dx%d exceed limits", result.Width, result.Height)
	}
	gridCells := uint64(result.Width) * uint64(result.Height)
	if gridCells/uint64(result.Width) != uint64(result.Height) || gridCells > limits.MaxGridCells {
		return nil, fmt.Errorf("sct: tile grid exceeds limits")
	}
	if result.TileCount == 0 || result.TileCount > limits.MaxTiles {
		return nil, fmt.Errorf("sct: tile count %d exceeds limits", result.TileCount)
	}
	section := func(name string, offset uint32, size uint64) ([]byte, error) {
		if offset < headerSize {
			return nil, fmt.Errorf("sct: %s starts inside header", name)
		}
		if uint64(offset) > uint64(len(data)) || size > uint64(len(data))-uint64(offset) {
			return nil, fmt.Errorf("sct: %s is outside file", name)
		}
		return data[int(offset):int(uint64(offset)+size)], nil
	}
	tileBytes := uint64(result.TileCount) * 1024
	if tileBytes/1024 != uint64(result.TileCount) {
		return nil, fmt.Errorf("sct: tile graphics size overflows")
	}
	previewPixels := uint64(128) * 128
	if previewPixels > limits.MaxPreviewPixels {
		return nil, fmt.Errorf("sct: section preview exceeds limits")
	}
	spans := []struct {
		name       string
		start, end uint64
	}{
		{name: "tile graphics", start: uint64(result.TileGraphicsOffset), end: uint64(result.TileGraphicsOffset) + tileBytes},
		{name: "tile index grid", start: uint64(result.TileIndexOffset), end: uint64(result.TileIndexOffset) + gridCells*2},
		{name: "section preview", start: uint64(result.SectionPreviewOffset), end: uint64(result.SectionPreviewOffset) + previewPixels},
	}
	for n := range spans {
		if spans[n].start < headerSize {
			return nil, fmt.Errorf("sct: %s starts inside header", spans[n].name)
		}
		for other := n + 1; other < len(spans); other++ {
			if spans[n].start < spans[other].end && spans[other].start < spans[n].end {
				return nil, fmt.Errorf("sct: %s overlaps %s", spans[n].name, spans[other].name)
			}
		}
	}
	graphics, err := section("tile graphics", result.TileGraphicsOffset, tileBytes)
	if err != nil {
		return nil, err
	}
	indicesBytes, err := section("tile index grid", result.TileIndexOffset, gridCells*2)
	if err != nil {
		return nil, err
	}
	result.TileGraphics = graphics
	result.TileIndices = make([]uint16, int(gridCells))
	for index := range result.TileIndices {
		value := binary.LittleEndian.Uint16(indicesBytes[index*2:])
		if uint32(value) >= result.TileCount {
			return nil, fmt.Errorf("sct: tile index %d at cell %d exceeds tile count %d", value, index, result.TileCount)
		}
		result.TileIndices[index] = value
	}
	preview, err := section("section preview", result.SectionPreviewOffset, previewPixels)
	if err != nil {
		return nil, err
	}
	result.SectionPreview = preview
	metadataStart := uint64(result.TileIndexOffset) + gridCells*2
	metadataEnd := uint64(result.SectionPreviewOffset)
	if result.Version == 3 && uint64(result.TileGraphicsOffset) > metadataStart {
		metadataEnd = uint64(result.TileGraphicsOffset)
	}
	if metadataEnd >= metadataStart && metadataEnd <= uint64(len(data)) {
		result.AttributeData = data[int(metadataStart):int(metadataEnd)]
	}
	return result, nil
}
