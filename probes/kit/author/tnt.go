package author

// TNT writer — layout per research/formats/tnt.md (canonical version 0x2000).

// TNTCell is one attribute-grid cell (16×16 map pixels).
type TNTCell struct {
	Height  uint8
	Feature uint16 // 0xFFFF none, 0xFFFC void, 0xFFFE fringe, else record index
}

// TNT is an authored canonical terrain file.
type TNT struct {
	// Width and Height are attribute cells (16 px); both must be even.
	Width, Height int
	// TileMap is (Width/2)×(Height/2) tile indexes, row-major.
	TileMap []uint16
	// Tiles are 32×32 palette-index blocks, 1024 bytes each.
	Tiles [][]byte
	// Attributes are Width×Height cells, row-major.
	Attributes []TNTCell
	// Features are the feature-record names (index = record position).
	Features []string
	SeaLevel uint32
	// Minimap, when non-nil, is embedded (MinimapPresent bit 0 set). When
	// nil the header's present bit is clear and the engine generates the
	// picture from the tiles ([fmt tnt] "How the engine loads it").
	Minimap *Minimap
}

// Minimap is the optional embedded picture (w×h palette indexes).
type Minimap struct {
	Width, Height int
	Pixels        []byte
}

// SolidTile returns a 32×32 tile filled with one palette index.
func SolidTile(index byte) []byte {
	t := make([]byte, 1024)
	for i := range t {
		t[i] = index
	}
	return t
}

// EmptyCell is the no-feature cell value.
const EmptyCell = 0xFFFF

// VoidCell is the authored in-map void ("unpassable hole") value.
const VoidCell = 0xFFFC

// FringeCell marks a cell covered by a multi-cell feature anchored elsewhere.
const FringeCell = 0xFFFE

// NewTNT allocates a flat map: every attribute cell at height with no
// feature, every tile-map entry pointing at tile 0.
func NewTNT(width, height int, height0 uint8, tile0 []byte) *TNT {
	check(width%2 == 0 && height%2 == 0, "tnt: dimensions must be even")
	t := &TNT{Width: width, Height: height}
	t.TileMap = make([]uint16, (width/2)*(height/2))
	t.Tiles = [][]byte{tile0}
	t.Attributes = make([]TNTCell, width*height)
	for i := range t.Attributes {
		t.Attributes[i] = TNTCell{Height: height0, Feature: EmptyCell}
	}
	return t
}

// AddTile appends a tile and returns its index.
func (t *TNT) AddTile(tile []byte) uint16 {
	check(len(tile) == 1024, "tnt: tile must be 1024 bytes")
	t.Tiles = append(t.Tiles, tile)
	return uint16(len(t.Tiles) - 1)
}

// SetTile sets the tile index at tile-grid coordinates (32 px units).
func (t *TNT) SetTile(tx, ty int, index uint16) {
	t.TileMap[ty*(t.Width/2)+tx] = index
}

// FillTiles sets a tile-grid rectangle [tx0,tx1)×[ty0,ty1).
func (t *TNT) FillTiles(tx0, ty0, tx1, ty1 int, index uint16) {
	for ty := ty0; ty < ty1; ty++ {
		for tx := tx0; tx < tx1; tx++ {
			t.SetTile(tx, ty, index)
		}
	}
}

// Cell returns a pointer to the attribute cell at cell coordinates.
func (t *TNT) Cell(cx, cz int) *TNTCell {
	return &t.Attributes[cz*t.Width+cx]
}

// AddFeature appends a feature record name and returns its index.
func (t *TNT) AddFeature(name string) uint16 {
	check(len(name) < 128, "tnt: feature name too long")
	t.Features = append(t.Features, name)
	return uint16(len(t.Features) - 1)
}

// StampFeature writes the anchor cell with the record index and every
// other cell of the footX×footZ footprint with the fringe value, the way
// retail maps author multi-cell features ([fmt tnt] "Attribute map").
func (t *TNT) StampFeature(record uint16, cx, cz, footX, footZ int) {
	for dz := 0; dz < footZ; dz++ {
		for dx := 0; dx < footX; dx++ {
			c := t.Cell(cx+dx, cz+dz)
			if dx == 0 && dz == 0 {
				c.Feature = record
			} else {
				c.Feature = FringeCell
			}
		}
	}
}

// Bytes serialises the file. Section order: header, tile map, attributes,
// tile graphics, feature records, minimap — each located by its header
// pointer, as [fmt tnt] requires.
func (t *TNT) Bytes() []byte {
	check(len(t.TileMap) == (t.Width/2)*(t.Height/2), "tnt: tile map size")
	check(len(t.Attributes) == t.Width*t.Height, "tnt: attribute size")
	for _, v := range t.TileMap {
		check(int(v) < len(t.Tiles), "tnt: tile index %d out of range", v)
	}
	var b buf
	// Header (0x40 bytes).
	b.u32(0x2000)                  // 0x00 IDVersion
	b.u32(uint32(t.Width))         // 0x04 Width (16-px cells)
	b.u32(uint32(t.Height))        // 0x08 Height
	ptrMapData := b.off()          // 0x0C PtrMapData
	b.u32(0)                       //
	ptrMapAttr := b.off()          // 0x10 PtrMapAttr
	b.u32(0)                       //
	ptrTileGfx := b.off()          // 0x14 PtrTileGfx
	b.u32(0)                       //
	b.u32(uint32(len(t.Tiles)))    // 0x18 Tiles
	b.u32(uint32(len(t.Features))) // 0x1C TileAnims (feature record count)
	ptrTileAnims := b.off()        // 0x20 PtrTileAnims
	b.u32(0)                       //
	b.u32(t.SeaLevel)              // 0x24 SeaLevel
	ptrMiniMap := b.off()          // 0x28 PtrMiniMap
	b.u32(0)                       //
	if t.Minimap != nil {          // 0x2C MinimapPresent bit 0
		b.u32(1)
	} else {
		b.u32(0)
	}
	for i := 0; i < 4; i++ { // 0x30–0x3C unknown/pad, zero in retail
		b.u32(0)
	}
	check(b.off() == 0x40, "tnt: header size")

	b.patchU32(ptrMapData, b.off())
	for _, v := range t.TileMap {
		b.u16(v)
	}
	b.patchU32(ptrMapAttr, b.off())
	for _, c := range t.Attributes {
		b.u8(c.Height)
		b.u16(c.Feature) // bytes 1..2, unaligned
		b.u8(0)          // byte 3: unknown, 0 in every retail cell
	}
	b.patchU32(ptrTileGfx, b.off())
	for _, tile := range t.Tiles {
		check(len(tile) == 1024, "tnt: tile size")
		b.Write(tile)
	}
	b.patchU32(ptrTileAnims, b.off())
	for i, name := range t.Features {
		b.u32(uint32(i)) // index equals record position
		var s [128]byte
		copy(s[:], name)
		b.Write(s[:])
	}
	// A minimap section is always written so that readers which follow the
	// pointer unconditionally (Nanolathe's own loader does) find a valid
	// picture; the header's present bit above says whether retail uses it.
	m := t.Minimap
	if m == nil {
		m = t.sampledMinimap()
	}
	check(len(m.Pixels) == m.Width*m.Height, "tnt: minimap size")
	b.patchU32(ptrMiniMap, b.off())
	b.u32(uint32(m.Width))
	b.u32(uint32(m.Height))
	b.Write(m.Pixels)
	return b.Bytes()
}

// sampledMinimap builds the conventional 252×252 picture by nearest
// sampling of the tile art, scaled to fit with the retail pad index 0x64
// filling unused rows/columns ([fmt tnt] "Minimap").
func (t *TNT) sampledMinimap() *Minimap {
	const side, pad = 252, 0x64
	pxW, pxH := t.Width*16, t.Height*16
	scale := max(pxW, pxH)
	m := &Minimap{Width: side, Height: side, Pixels: make([]byte, side*side)}
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			wx, wy := x*scale/side, y*scale/side
			if wx >= pxW || wy >= pxH {
				m.Pixels[y*side+x] = pad
				continue
			}
			tile := t.Tiles[t.TileMap[(wy/32)*(t.Width/2)+wx/32]]
			m.Pixels[y*side+x] = tile[(wy%32)*32+wx%32]
		}
	}
	return m
}
