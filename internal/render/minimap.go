// Package render implements minimap/radar surfaces [03 §3.4][07 §10][03 §3.6–§3.12].
package render

import (
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
)

// RadarSurface is an indexed radar surface descriptor [03 §3.6].
// W,H are RadarW/H 1..126 [07 §10][03 §3.4]. Pitch is (W+3)&~3 DWORD-aligned [03 §3.6][03 §4.1]. Bits are w*h indexed pixels (PALETTE.PAL indices).
type RadarSurface struct {
	W, H  int
	Pitch int    // (W+3)&~3 DWORD-aligned [03 §3.6]
	Bits  []byte // w*h indexed pixels (PALETTE.PAL indices), len = w*h when non-nil [03 §4.3]
	// Desc [12]uint32 // optional descriptor metadata
}

// At returns pixel at (x,y) with bounds check [03 §3.6].
func (r *RadarSurface) At(x, y int) (byte, bool) {
	if r == nil || r.Bits == nil {
		return 0, false
	}
	if x < 0 || y < 0 || x >= r.W || y >= r.H {
		return 0, false
	}
	idx := y*r.W + x
	if idx < 0 || idx >= len(r.Bits) {
		return 0, false
	}
	return r.Bits[idx], true
}

// Set writes pixel at (x,y) with bounds check [03 §3.6].
func (r *RadarSurface) Set(x, y int, v byte) bool {
	if r == nil || r.Bits == nil {
		return false
	}
	if x < 0 || y < 0 || x >= r.W || y >= r.H {
		return false
	}
	idx := y*r.W + x
	if idx < 0 || idx >= len(r.Bits) {
		return false
	}
	r.Bits[idx] = v
	return true
}

// LetterboxFill returns minimapLetterboxFill = 0 TODO(question) [03 §3.6] letterbox bars inference 0 black pending capture.
func LetterboxFill() byte {
	// TODO(question): bars beyond RadarW×RadarH retain heap bytes — inference 0 black pending capture [03 §3.6][07 §10].
	return 0
}

// floorDiv returns floor(a/b) with sign correction [03 §2.1][INVARIANTS I3].
func minimapFloorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// BuildRadarPicture builds PICTURE from terrain or baked bytes [03 §3.7][03 §3.4][07 §10].
// playW = Wpix-32, playH = Hpix-128; RadarW/H are letterboxed via camera.LayoutMinimap.
// baked == nil or len==0 uses 2× supersampled tile sampling and ALP 2×2→1 blending [03 §3.7][fmt tnt][fmt pal].
// When baked != nil, it is rescaled through the established picture path [03 §3.7].
// Letterbox bars fill 0 black pending capture TODO(question) [03 §3.6].
// Tables may be nil in tests — fallback to nearest without ALP, still indices.
func BuildRadarPicture(t *world.Terrain, playW, playH int32, m camera.Minimap, baked []byte, bakedW, bakedH int, tables *palette.Tables) *RadarSurface {
	if m.W <= 0 || m.H <= 0 {
		w := int(m.W)
		h := int(m.H)
		if w < 0 {
			w = 0
		}
		if h < 0 {
			h = 0
		}
		pitch := (w + 3) &^ 3 // [03 §3.6] DWORD-aligned
		return &RadarSurface{W: w, H: h, Pitch: pitch, Bits: make([]byte, w*h)}
	}
	w := int(m.W)
	h := int(m.H)
	pitch := (w + 3) &^ 3 // [03 §3.6][03 §4.1] pitch (w+3)&~3
	bits := make([]byte, w*h)
	fill := LetterboxFill()
	for i := range bits {
		bits[i] = fill
	}

	// Baked path: rescale through the established picture path [03 §3.7].
	if baked != nil && len(baked) > 0 && bakedW > 0 && bakedH > 0 {
		// Exact 2× supersampled baked → ALP blend when tables present [03 §3.7].
		if bakedW == 2*w && bakedH == 2*h && tables != nil {
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					// TODO(T23): verify the ALP quadrant order against an asymmetric palette probe [03 §3.7].
					p00 := baked[(y*2)*bakedW+(x*2)]
					p01 := baked[(y*2)*bakedW+(x*2+1)]
					p10 := baked[(y*2+1)*bakedW+(x*2)]
					p11 := baked[(y*2+1)*bakedW+(x*2+1)]
					a0 := tables.Alpha[int(p00)*256+int(p01)] // [fmt pal] 256×256 nearest-color blend
					a1 := tables.Alpha[int(p10)*256+int(p11)]
					blended := tables.Alpha[int(a0)*256+int(a1)]
					bits[y*w+x] = blended
				}
			}
		} else {
			// Generic TRUNC rescale [07 §10] playW/playH analog; use trunc division.
			for y := 0; y < h; y++ {
				srcY := y * bakedH / h // TRUNC [03 §3.7][07 §10]
				if srcY < 0 {
					srcY = 0
				}
				if srcY >= bakedH {
					srcY = bakedH - 1
				}
				for x := 0; x < w; x++ {
					srcX := x * bakedW / w
					if srcX < 0 {
						srcX = 0
					}
					if srcX >= bakedW {
						srcX = bakedW - 1
					}
					idx := srcY*bakedW + srcX
					if idx >= 0 && idx < len(baked) {
						bits[y*w+x] = baked[idx]
					}
				}
			}
		}
		return &RadarSurface{W: w, H: h, Pitch: pitch, Bits: bits}
	}

	if t == nil || len(t.TileSet) == 0 || len(t.TileIndices) == 0 || playW <= 0 || playH <= 0 {
		return &RadarSurface{W: w, H: h, Pitch: pitch, Bits: bits}
	}

	tw := 2 * w
	th := 2 * h
	temp := make([]byte, tw*th)

	tileW := int(t.CellW / 2) // [03 §2.2] TileW=Wcells/2 [03 §3.7]
	tileH := int(t.CellH / 2)
	if tileW <= 0 || tileH <= 0 {
		return &RadarSurface{W: w, H: h, Pitch: pitch, Bits: bits}
	}
	tileCount := len(t.TileSet)

	// 2× supersampled sampling [03 §3.7].
	for ty := 0; ty < th; ty++ {
		for tx := 0; tx < tw; tx++ {
			// worldX = PlayRight * x / (2*RadarW)    // trunc IDIV [03 §3.7]
			// worldZ = PlayBottom * y / (2*RadarH)
			worldX := int32(int64(playW) * int64(tx) / int64(tw)) // TRUNC IDIV [03 §3.7]
			worldZ := int32(int64(playH) * int64(ty) / int64(th))

			// tileX = floorDiv(worldX,32) etc sign-corrected SAR 5 [03 §2.1]
			tileX := minimapFloorDiv(int64(worldX), 32)
			tileZ := minimapFloorDiv(int64(worldZ), 32)

			var pix byte
			if tileX < 0 || tileX >= int64(tileW) || tileZ < 0 || tileZ >= int64(tileH) {
				pix = LetterboxFill() // TODO(question) OOB retains heap/inference 0 [03 §3.6]
			} else {
				idx := int(tileZ)*tileW + int(tileX)
				if idx < 0 || idx >= len(t.TileIndices) {
					pix = LetterboxFill()
				} else {
					tileIdx := t.TileIndices[idx]
					// Guard an authored tile index outside the tile set [03 §3.7].
					if int(tileIdx) >= tileCount {
						tileIdx = 0
					}
					// intra-tile offset (world &31) with sign-correct floor [03 §3.7]
					ox := int(int64(worldX) - tileX*32) // 0..31
					oz := int(int64(worldZ) - tileZ*32)
					if ox < 0 {
						ox += 32
					}
					if ox >= 32 {
						ox %= 32
					}
					if oz < 0 {
						oz += 32
					}
					if oz >= 32 {
						oz %= 32
					}
					// Tile 32×32 indexed pixels row-major [fmt tnt]
					tile := t.TileSet[tileIdx]
					pix = tile[oz*32+ox] // [03 §3.7] pix = *(u8*)(tileBase + (worldZ&31)*32 + (worldX&31))
				}
			}
			temp[ty*tw+tx] = pix // opaque indexed sample [03 §3.7]
		}
	}

	// Blend 2×2 via the 256×256 ALP table [03 §3.7][fmt pal].
	// blended = ALP[ ALP[p00*256+p01]*256 + ALP[p10*256+p11] ] (two lookups, exact quadrant order SUPPORTED-INFERENCE) TODO(T23).
	if tables != nil {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				p00 := temp[(y*2)*tw+(x*2)]
				p01 := temp[(y*2)*tw+(x*2+1)]
				p10 := temp[(y*2+1)*tw+(x*2)]
				p11 := temp[(y*2+1)*tw+(x*2+1)]
				a0 := tables.Alpha[int(p00)*256+int(p01)]
				a1 := tables.Alpha[int(p10)*256+int(p11)]
				blended := tables.Alpha[int(a0)*256+int(a1)]
				bits[y*w+x] = blended
			}
		}
	} else {
		// Tables may be nil in tests — fallback to nearest without ALP, still indices [03 §3.7].
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				bits[y*w+x] = temp[(y*2)*tw+(x*2)]
			}
		}
	}

	return &RadarSurface{W: w, H: h, Pitch: pitch, Bits: bits}
}

// BuildRadarPictureFromWorld is a helper that derives playW/H from terrain [03 §3.6][03 §2.2].
func BuildRadarPictureFromWorld(t *world.Terrain, m camera.Minimap, baked []byte, bakedW, bakedH int, tables *palette.Tables) *RadarSurface {
	if t == nil {
		return BuildRadarPicture(nil, 0, 0, m, baked, bakedW, bakedH, tables)
	}
	return BuildRadarPicture(t, t.PlayRight, t.PlayBottom, m, baked, bakedW, bakedH, tables)
}

// BuildMapped implements MAPPED composite: picture masked by authoritative LOS
// grids [03 §3.8][03 §3.3]. It is a pure presentation operation [03 §3.6].
func BuildMapped(picture *RadarSurface, wordMask []uint16, byteGrid []uint8, mapW, mapH int, localSlot uint8, dcb byte, guiRemap []byte) *RadarSurface {
	if picture == nil || picture.Bits == nil || picture.W <= 0 || picture.H <= 0 {
		return nil
	}
	w := picture.W
	h := picture.H
	pitch := (w + 3) &^ 3 // [03 §3.6]
	bits := make([]byte, w*h)
	mask := uint16(1 << (localSlot & 0x1F)) // [03 §3.8] bit 1<<(player&0x1F)
	for y := 0; y < h; y++ {
		visY := 0
		if mapH > 0 && h > 0 {
			visY = y * mapH / h // TRUNC integer scaled [03 §3.8]
		}
		if visY < 0 {
			visY = 0
		}
		if mapH > 0 && visY >= mapH {
			visY = mapH - 1
		}
		for x := 0; x < w; x++ {
			visX := 0
			if mapW > 0 && w > 0 {
				visX = x * mapW / w // TRUNC [03 §3.8]
			}
			if visX < 0 {
				visX = 0
			}
			if mapW > 0 && visX >= mapW {
				visX = mapW - 1
			}
			visIdx := visY*mapW + visX
			var word uint16
			if visIdx >= 0 && visIdx < len(wordMask) {
				word = wordMask[visIdx]
			}
			var bVal uint8
			if visIdx >= 0 && visIdx < len(byteGrid) {
				bVal = byteGrid[visIdx]
			}
			src := picture.Bits[y*w+x]
			var out byte
			// word→byte→remap→DCB gate order [03 §3.8].
			if word&mask == 0 {
				out = dcb // [03 §3.8] unexplored → configured fog fill
			} else if bVal == 0 {
				if guiRemap != nil && len(guiRemap) == 256 {
					out = guiRemap[src] // [03 §3.8] GUI remap
				} else {
					out = src // no remap when nil [03 §3.8]
				}
			} else {
				out = src
			}
			bits[y*w+x] = out
		}
	}
	return &RadarSurface{W: w, H: h, Pitch: pitch, Bits: bits}
}

// MinimapContact carries world coords already in map pixels (short world>>16) plus owner, flags, def distances [03 §3.9].
type MinimapContact struct {
	WorldX, WorldZ, WorldY int32 // map pixels (short narrow already), WorldY high word for shear [03 §3.9]
	Owner                  uint8
	Palette                byte // owner palette index; zero selects fallback [03 §3.9]
	IsCommander            bool // when true draws commander GAF after blip [03 §3.9]
	Stealth                bool // when true gate on blink [03 §3.9]
	NoRadar                bool // TODO(question) alias 0x245&4 [03 §3.9]
	// Status and BlinkSuppress are copied from the immutable unit record. The
	// contact gate uses FriendlyMask/owner, while the suppress byte admits a
	// blip only during the shared blink phase. [03 §3.9]
	Status        uint32
	BlinkSuppress uint8
	Visible       bool
	LocalPlayer   uint8
	Options       uint32
	MinimapMode   uint8
	RawDistRadar  int32
	RawDistSonar  int32
	RawDistJamR   int32
	RawDistJamS   int32 // radar distances, 0 means absent [03 §3.10][07 §10]
	RingEnabled   bool
	RingDashed    bool
	RingRange     int32
}

// MinimapContactBlitter is the resolved authored-art adapter. It is called
// after the visibility/gate checks; a nil adapter means the authored blip is
// absent and therefore leaves FINAL untouched. [03 §3.9]
type MinimapContactBlitter func(dst *RadarSurface, x, y int, palette byte, commander bool)

// RebuildFinalExact wipes FINAL from MAPPED and applies the established
// contacts/circles order. It never invents a pixel for a missing blip asset;
// use RebuildFinal only as the old diagnostic fixture adapter. [03 §3.9]
func RebuildFinalExact(mapped *RadarSurface, m camera.Minimap, playW, playH int32, contacts []MinimapContact, blink BlinkState, blit MinimapContactBlitter, radarColor, jammerColor, ringColor byte) *RadarSurface {
	if mapped == nil || mapped.W <= 0 || mapped.H <= 0 || len(mapped.Bits) < mapped.W*mapped.H {
		return nil
	}
	final := &RadarSurface{W: mapped.W, H: mapped.H, Pitch: (mapped.W + 3) &^ 3, Bits: make([]byte, mapped.W*mapped.H)}
	copy(final.Bits, mapped.Bits)
	for _, c := range contacts {
		// No-radar suppresses circles, not the contact blip. [03 §3.9]
		admit := c.Visible || c.Options&(1<<9) != 0 || c.MinimapMode&3 == 0 || c.Status&0x300 != 0 || c.Owner == c.LocalPlayer
		if !admit || (c.BlinkSuppress != 0 && blink.Phase&1 == 0) || c.Stealth && !blink.IsBlinkOn() {
			continue
		}
		rx, ry := RadarProjection(c.WorldX, c.WorldZ, c.WorldY, playW, playH, m)
		if blit != nil {
			blit(final, int(rx), int(ry), c.Palette, false)
			if c.IsCommander {
				blit(final, int(rx), int(ry), c.Palette, true)
			}
		}
		// Blips and circles have separate gates: no-radar suppresses the
		// ordinary circle path, except a stealthed source still contributes its
		// established sensor circle [03 §3.9].
		if c.Stealth || !c.NoRadar {
			if c.RawDistRadar != 0 || c.RawDistSonar != 0 {
				d := c.RawDistRadar
				if c.RawDistSonar > d {
					d = c.RawDistSonar
				}
				if r := RadarRadius(d, m.W, playW); r > 0 {
					drawCircle(final, int(rx), int(ry), int(r), radarColor)
				}
			}
			if c.RawDistJamR != 0 {
				if r := RadarRadius(c.RawDistJamR, m.W, playW); r > 0 {
					drawCircle(final, int(rx), int(ry), int(r), jammerColor)
				}
			}
			if c.RawDistJamS != 0 {
				if r := RadarRadius(c.RawDistJamS, m.W, playW); r > 0 {
					drawCircle(final, int(rx), int(ry), int(r), jammerColor)
				}
			}
			if c.RingEnabled {
				r := RadarRadius(c.RingRange-512, m.W, playW)
				if r > 0 {
					if c.RingDashed {
						drawDashedCircle(final, int(rx), int(ry), int(r), ringColor, blink.Phase&1 != 0)
					} else {
						drawCircle(final, int(rx), int(ry), int(r), ringColor)
					}
				}
			}
		}
	}
	return final
}

// BlinkState holds minimap blink countdown and phase [03 §3.9][03 §3.6].
type BlinkState struct {
	Countdown int16 // 7..0
	Phase     uint8 // bit0 blink phase, ^=1 every 8 frames [03 §3.6].
}

// Tick advances blink per host frame: if Countdown>0 dec else 7; ^=1 every 8 [03 §3.6].
func (b *BlinkState) Tick() {
	if b == nil {
		return
	}
	if b.Countdown > 0 {
		b.Countdown--
		return
	}
	b.Countdown = 7
	b.Phase ^= 1 // every 8 frames when countdown wraps [03 §3.6]
}

// IsBlinkOn reports whether stealth contacts should be visible [03 §3.9].
func (b BlinkState) IsBlinkOn() bool {
	return b.Phase&1 != 0
}

// RadarProjection projects world coords to radar pixels [03 §3.9][07 §10].
// rx=worldX*RadarW/PlayRight etc with half shear SAR 1 [03 §3.9].
func RadarProjection(worldX, worldZ, worldY int32, playW, playH int32, m camera.Minimap) (rx, ry int32) {
	if playW == 0 || playH == 0 || m.W == 0 || m.H == 0 {
		return 0, 0
	}
	// TRUNC via IDIV [03 §3.9][07 §10]; use int64 intermediate.
	rx = int32(int64(worldX) * int64(m.W) / int64(playW))
	// ry = ((short)(worldZ) - ((short)(worldY)>>1)) * RadarH / PlayBottom // SAR 1 half shear [03 §3.9]
	ry = int32((int64(worldZ-(worldY>>1)) * int64(m.H)) / int64(playH))
	return rx, ry
}

// RadarRadius computes truncated radar radius rRadar=RadarW*dist/PlayRight etc [03 §3.10][07 §10].
func RadarRadius(dist int32, radarSize int32, playSize int32) int32 {
	if playSize == 0 || radarSize == 0 || dist == 0 {
		return 0
	}
	return int32(int64(radarSize) * int64(dist) / int64(playSize)) // TRUNC [03 §3.10]
}

// RebuildFinal implements FINAL wipe + contacts (presentation-only, no LOS mutation) in the established layer order [03 §3.9][07 §10].
// It wipes FINAL from MAPPED via copy, then in pool order (slice order is caller-stable ascending) draws blip (palette byte) then commander on top,
// then circles DD5/DD7/DDA via Bresenham placeholder, then returns FINAL. Keeps layer order painter: later overwrites earlier [03 §3.9].
func RebuildFinal(mapped *RadarSurface, m camera.Minimap, playW, playH int32, contacts []MinimapContact, blink BlinkState, _ *palette.Tables) *RadarSurface {
	if mapped == nil || mapped.Bits == nil || mapped.W <= 0 || mapped.H <= 0 {
		return nil
	}
	w := mapped.W
	h := mapped.H
	pitch := (w + 3) &^ 3
	final := &RadarSurface{W: w, H: h, Pitch: pitch, Bits: make([]byte, w*h)}
	copy(final.Bits, mapped.Bits) // wipe FINAL from MAPPED via copy [03 §3.9]

	for _, c := range contacts {
		// Blink gate for stealthed contacts [03 §3.9]: only when blink==1.
		if c.Stealth && !blink.IsBlinkOn() {
			continue
		}
		rx, ry := RadarProjection(c.WorldX, c.WorldZ, c.WorldY, playW, playH, m)

		pal := c.Palette
		if pal == 0 {
			// Fallback owner palette identity [03 §3.9].
			pal = byte(0x80 + (c.Owner & 0x0F)) // TODO(question) exact fallback palette derivation
		}

		// Layer 1: authored blip [03 §3.9].
		final.Set(int(rx), int(ry), pal)

		// Layer 2: commander art when applicable [03 §3.9].
		if c.IsCommander {
			// painter: later overwrites earlier [03 §3.9]; ensure commander visibly on top by second pixel offset and overwrite.
			final.Set(int(rx), int(ry), pal)
			final.Set(int(rx+1), int(ry), pal)
		}

		// Layers 3a/b/c circles DD5/DD7/DDA via the 32-segment integer raster [03 §3.10][03 §3.9].
		// Use truncated radii rRadar=RadarW*dist/PlayRight etc [03 §3.10].
		// TODO(T23): the DDA dash palette/source is not established [03 §3.9].
		if c.RawDistRadar != 0 || c.RawDistSonar != 0 {
			outer := c.RawDistRadar
			if c.RawDistSonar > outer {
				outer = c.RawDistSonar
			}
			r := RadarRadius(outer, m.W, playW)
			if r > 0 {
				// DD5 radar outer max(radar,sonar) [03 §3.10]
				drawCircle(final, int(rx), int(ry), int(r), 0xA0) // TODO(question): exact authored radar color [03 §3.10].
			}
		}
		if c.RawDistJamR != 0 {
			r := RadarRadius(c.RawDistJamR, m.W, playW)
			if r > 0 {
				drawCircle(final, int(rx), int(ry), int(r), 0xB0) // placeholder DD7 jammer TODO(question)
			}
		}
		if c.RawDistJamS != 0 {
			r := RadarRadius(c.RawDistJamS, m.W, playW)
			if r > 0 {
				drawCircle(final, int(rx), int(ry), int(r), 0xB0)
			}
		}
	}

	return final
}

// drawCircle joins the 32 authored angular samples with clipped integer lines.
// The angular increment is 0x800 (32 segments), and the fixed-point trig table
// is the shared retail table [03 §3.10][04 §5.1].
func drawCircle(s *RadarSurface, cx, cy, r int, color byte) {
	if s == nil || s.Bits == nil || r <= 0 {
		return
	}
	for i := 0; i < 32; i++ {
		x0, y0 := circlePoint(cx, cy, r, i)
		x1, y1 := circlePoint(cx, cy, r, i+1)
		line(s, x0, y0, x1, y1, color)
	}
}

func circlePoint(cx, cy, r, segment int) (int, int) {
	a := numeric.Angle(uint16(segment * 0x800))
	return cx + int(numeric.MulRound(int32(r), numeric.Cos(a))), cy + int(numeric.MulRound(int32(r), numeric.Sin(a)))
}

func line(s *RadarSurface, x0, y0, x1, y1 int, color byte) {
	dx := x1 - x0
	if dx < 0 {
		dx = -dx
	}
	dy := y1 - y0
	if dy < 0 {
		dy = -dy
	}
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	err := dx - dy
	for {
		s.Set(x0, y0, color)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 > -dy {
			err -= dy
			x0 += sx
		}
		if e2 < dx {
			err += dx
			y0 += sy
		}
	}
}

// drawDashedCircle emits alternating 32-segment arcs. Phase selects the
// established segment parity; there are no extra endpoint writes [03 §3.10].
func drawDashedCircle(s *RadarSurface, cx, cy, r int, color byte, phase bool) {
	if s == nil || s.Bits == nil || r <= 0 {
		return
	}
	offset := 0
	if phase {
		offset = 1
	}
	for i := 0; i < 32; i++ {
		if (i+offset)&1 == 0 {
			continue
		}
		x0, y0 := circlePoint(cx, cy, r, i)
		x1, y1 := circlePoint(cx, cy, r, i+1)
		line(s, x0, y0, x1, y1, color)
	}
}

// DrawViewportMarker paints the composer-time five-pixel cross. The caller
// supplies the camera centre in map pixels; the constants are the retained
// viewport-origin offsets, not a rectangle size. [03 §3.12]
func DrawViewportMarker(dst *RadarSurface, mode byte, cameraCenterX, cameraCenterY, cameraCenterZ, camX, camZ int32, color byte) {
	if dst == nil || mode != 2 {
		return
	}
	cx := cameraCenterX - camX
	cy := cameraCenterZ - (cameraCenterY >> 1) - camZ
	// The two retained line calls share only the crossing pixel in the
	// executable's clipped marker primitive; its observable footprint is the
	// five pixels at the centre and cardinal ±2 offsets. [03 §3.12]
	dst.Set(int(cx+128), int(cy+32), color)
	dst.Set(int(cx+126), int(cy+32), color)
	dst.Set(int(cx+130), int(cy+32), color)
	dst.Set(int(cx+128), int(cy+30), color)
	dst.Set(int(cx+128), int(cy+34), color)
}
