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
// The ALP table is mandatory: there is no nearest-neighbor compatibility path.
// TODO(question): letterbox bar fill is outside the exact w×h picture surface
// and remains unresolved [03 §3.7].
func BuildRadarPicture(t *world.Terrain, playW, playH int32, m camera.Minimap, baked []byte, bakedW, bakedH int, tables *palette.Tables) *RadarSurface {
	if m.W <= 0 || m.H <= 0 || tables == nil {
		return nil
	}
	w := int(m.W)
	h := int(m.H)
	pitch := (w + 3) &^ 3 // [03 §3.6][03 §4.1] pitch (w+3)&~3
	bits := make([]byte, w*h)

	// Baked path: rescale through the established picture path [03 §3.7].
	if baked != nil && len(baked) > 0 {
		if bakedW <= 0 || bakedH <= 0 || bakedW > len(baked)/bakedH {
			return nil
		}
		resizeALP(bits, w, h, baked, bakedW, bakedH, tables)
		return &RadarSurface{W: w, H: h, Pitch: pitch, Bits: bits}
	}

	if t == nil || len(t.TileSet) == 0 || len(t.TileIndices) == 0 || playW <= 0 || playH <= 0 {
		return nil
	}

	tw := 2 * w
	th := 2 * h
	temp := make([]byte, tw*th)

	tileW := int(t.CellW / 2) // [03 §2.2] TileW=Wcells/2 [03 §3.7]
	tileH := int(t.CellH / 2)
	if tileW <= 0 || tileH <= 0 || len(t.TileIndices) < tileW*tileH {
		return nil
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
				// TODO(question): the source map's out-of-domain sample behavior is
				// untraced; suppress this picture rather than inventing a fill index.
				return nil
			} else {
				idx := int(tileZ)*tileW + int(tileX)
				if idx < 0 || idx >= len(t.TileIndices) {
					return nil
				}
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
			temp[ty*tw+tx] = pix // opaque indexed sample [03 §3.7]
		}
	}

	// Two-level row-first ALP blend [03 §3.7].
	resizeALP(bits, w, h, temp, tw, th, tables)

	return &RadarSurface{W: w, H: h, Pitch: pitch, Bits: bits}
}

// resizeALP applies the established arbitrary-source/destination ALP path.
// Each destination sample maps to a source cell by truncating the ratio. The
// adjacent source sample is blended in each axis, then those row blends are
// blended once more; every intermediate remains a palette index [03 §3.7].
func resizeALP(dst []byte, dstW, dstH int, src []byte, srcW, srcH int, tables *palette.Tables) {
	if dstW <= 0 || dstH <= 0 || srcW <= 0 || srcH <= 0 || len(dst) < dstW*dstH || len(src) < srcW*srcH {
		return
	}
	for y := 0; y < dstH; y++ {
		for x := 0; x < dstW; x++ {
			sx, sy := x*srcW/dstW, y*srcH/dstH
			sx1, sy1 := sx+1, sy+1
			if sx1 >= srcW {
				sx1 = srcW - 1
			}
			if sy1 >= srcH {
				sy1 = srcH - 1
			}
			p00 := src[sy*srcW+sx]
			p01 := src[sy*srcW+sx1]
			p10 := src[sy1*srcW+sx]
			p11 := src[sy1*srcW+sx1]
			top := tables.Alpha[int(p00)*256+int(p01)]
			bottom := tables.Alpha[int(p10)*256+int(p11)]
			dst[y*dstW+x] = tables.Alpha[int(top)*256+int(bottom)]
		}
	}
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
	Palette                byte // caller-resolved owning-player palette index [03 §3.9]
	IsCommander            bool // when true draws commander GAF after blip [03 §3.9]
	Stealth                bool // when true gate on blink [03 §3.9]
	NoRadar                bool // authored no-radar flag; affects sensor circles only [03 §3.9]
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

func rebuildFinalExact(mapped *RadarSurface, m camera.Minimap, playW, playH int32, contacts []MinimapContact, sensors []MinimapCircle, blink BlinkState, blit MinimapContactBlitter, radarColor, jammerColor, ringColor byte) *RadarSurface {
	if mapped == nil || mapped.W <= 0 || mapped.H <= 0 || len(mapped.Bits) < mapped.W*mapped.H {
		return nil
	}
	final := &RadarSurface{W: mapped.W, H: mapped.H, Pitch: (mapped.W + 3) &^ 3, Bits: make([]byte, mapped.W*mapped.H)}
	copy(final.Bits, mapped.Bits)
	// Unit blips precede all circles, and commander art is the next layer.
	for _, c := range contacts {
		admit := c.Visible || c.Options&(1<<9) != 0 || c.MinimapMode&3 == 0 || c.Status&0x300 != 0 || c.Owner == c.LocalPlayer
		if !admit || (c.BlinkSuppress != 0 && blink.Phase&1 == 0) || c.Stealth && !blink.IsBlinkOn() {
			continue
		}
		rx, ry := RadarProjection(c.WorldX, c.WorldZ, c.WorldY, playW, playH, m)
		if blit != nil {
			blit(final, int(rx), int(ry), c.Palette, false)
		}
	}
	for _, c := range contacts {
		admit := c.Visible || c.Options&(1<<9) != 0 || c.MinimapMode&3 == 0 || c.Status&0x300 != 0 || c.Owner == c.LocalPlayer
		if !admit || (c.BlinkSuppress != 0 && blink.Phase&1 == 0) || c.Stealth && !blink.IsBlinkOn() || !c.IsCommander {
			continue
		}
		rx, ry := RadarProjection(c.WorldX, c.WorldZ, c.WorldY, playW, playH, m)
		if blit != nil {
			blit(final, int(rx), int(ry), c.Palette, true)
		}
	}

	// Sensor circles follow every contact blit. The no-radar flag suppresses
	// these circles only while stealth is inactive; it never suppresses blips
	// [03 §3.9].
	for _, c := range contacts {
		admit := c.Visible || c.Options&(1<<9) != 0 || c.MinimapMode&3 == 0 || c.Status&0x300 != 0 || c.Owner == c.LocalPlayer
		if !admit || (c.BlinkSuppress != 0 && blink.Phase&1 == 0) || c.Stealth && !blink.IsBlinkOn() {
			continue
		}
		if c.NoRadar && !c.Stealth {
			continue
		}
		rx, ry := RadarProjection(c.WorldX, c.WorldZ, c.WorldY, playW, playH, m)
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
	}

	// Sensor callbacks are the final surface's circle layer and precede rings
	// [03 §3.9][03 §3.10]. Their coordinates are 128-world-unit cells.
	for _, c := range sensors {
		rx, ry := RadarProjection(c.U<<7, c.V<<7, 0, playW, playH, m)
		r := RadarRadius(c.Radius, m.W, playW)
		if r <= 0 {
			continue
		}
		color := radarColor
		if c.Kind != 0 {
			color = jammerColor
		}
		drawCircle(final, int(rx), int(ry), int(r), color)
	}

	// Rings are a separate layer after sensor circles [03 §3.9].
	for _, c := range contacts {
		admit := c.Visible || c.Options&(1<<9) != 0 || c.MinimapMode&3 == 0 || c.Status&0x300 != 0 || c.Owner == c.LocalPlayer
		if !admit || (c.BlinkSuppress != 0 && blink.Phase&1 == 0) || c.Stealth && !blink.IsBlinkOn() || !c.RingEnabled {
			continue
		}
		rx, ry := RadarProjection(c.WorldX, c.WorldZ, c.WorldY, playW, playH, m)
		r := RadarRadius(c.RingRange-512, m.W, playW)
		if r > 0 {
			if c.RingDashed {
				drawDashedCircle(final, int(rx), int(ry), int(r), ringColor, blink.Phase&1 != 0)
			} else {
				drawCircle(final, int(rx), int(ry), int(r), ringColor)
			}
		}
	}
	return final
}

// BlinkState carries the committed minimap phase consumed by contact and ring
// presentation. Countdown ownership remains in the phase-12 session state
// and is never presented here [R-CORE-03][03 §3.6].
type BlinkState struct {
	Phase uint8 // semantic bit 0 [R-CORE-03].
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
