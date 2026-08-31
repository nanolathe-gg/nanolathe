package world

import "fmt"

// RetailMetalImage returns the terrain's detached Metal/Plotmap box in plot
// row-major order. It copies the raw per-cell metal byte; no schema value or
// extractor result is substituted [08 R-SAVE-02 §12].
func (t *Terrain) RetailMetalImage() ([]byte, error) {
	n, err := t.retailPlotCount()
	if err != nil {
		return nil, err
	}
	data := make([]byte, n)
	for i := range data {
		data[i] = t.Plot[i].Metal()
	}
	return data, nil
}

// RetailPlayerFeaturesImage returns the packed PlayerFeatures/Plotmap box.
// Consecutive cells are packed high nibble then low nibble, using only flag
// bits 3..6 (the placer nibble) [08 R-SAVE-02 §12].
func (t *Terrain) RetailPlayerFeaturesImage() ([]byte, error) {
	n, err := t.retailPlotCount()
	if err != nil {
		return nil, err
	}
	data := make([]byte, n/2)
	for i := range data {
		data[i] = t.Plot[i*2].PlacerNibble()<<4 | t.Plot[i*2+1].PlacerNibble()
	}
	return data, nil
}

// RestoreRetailMetal applies an exact Metal/Plotmap box to the terrain. It
// leaves all other cell fields unchanged [08 R-SAVE-02 §12].
func (t *Terrain) RestoreRetailMetal(data []byte) error {
	n, err := t.retailPlotCount()
	if err != nil {
		return err
	}
	if len(data) != n {
		return fmt.Errorf("world: retail restore: Metal box has %d bytes, expected %d", len(data), n)
	}
	for i, value := range data {
		t.Plot[i].SetMetal(value)
	}
	return nil
}

// RestoreRetailPlayerFeatures applies the exact packed placer-nibble image,
// preserving each cell's unrelated flag bits [08 R-SAVE-02 §12].
func (t *Terrain) RestoreRetailPlayerFeatures(data []byte) error {
	n, err := t.retailPlotCount()
	if err != nil {
		return err
	}
	if len(data) != n/2 {
		return fmt.Errorf("world: retail restore: PlayerFeatures box has %d bytes, expected %d", len(data), n/2)
	}
	for i, value := range data {
		setRetailPlacerNibble(&t.Plot[i*2], value>>4)
		setRetailPlacerNibble(&t.Plot[i*2+1], value&0x0f)
	}
	return nil
}

// RetailMappingImage cannot be implemented from the current Terrain runtime:
// mapping is a separate half-grid save source, not the LOS history word grid.
// Keeping the failure explicit avoids writing visibility state as Mapping
// bytes [08 R-SAVE-02 §12].
func (t *Terrain) RetailMappingImage() ([]byte, error) {
	// TODO(question): which runtime owner retains the mapping half-grid at save
	// time? Add that exact source and resolver before exposing Mapping bytes.
	return nil, fmt.Errorf("world: retail save: Mapping source is unavailable: logical path save/Mapping, providers searched [Terrain], expected exact half-grid mapping bytes")
}

func (t *Terrain) retailPlotCount() (int, error) {
	if t == nil {
		return 0, fmt.Errorf("world: retail save: nil terrain")
	}
	if t.CellW <= 0 || t.CellH <= 0 {
		return 0, fmt.Errorf("world: retail save: invalid terrain dimensions %dx%d", t.CellW, t.CellH)
	}
	n64 := int64(t.CellW) * int64(t.CellH)
	if n64 <= 0 || n64 > int64(^uint(0)>>1) || n64 > int64(len(t.Plot)) {
		return 0, fmt.Errorf("world: retail save: terrain plot has %d cells, expected at least %d", len(t.Plot), n64)
	}
	return int(n64), nil
}

func setRetailPlacerNibble(cell *PlotCell, nibble uint8) {
	flags := cell.FlagByte()
	cell.SetFlagByte((flags &^ 0x78) | ((nibble & 0x0f) << 3))
}
