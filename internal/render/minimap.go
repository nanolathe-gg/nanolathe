package render

// Minimap and radar surfaces [03 §3.4][07 §10][03 §3.6–§3.12].

import (
	"slices"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
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

// BuildRadarPicture builds PICTURE from terrain or baked bytes [03 §3.7][03 §3.4][07 §10].
// playW = Wpix-32, playH = Hpix-128; RadarW/H are letterboxed via camera.LayoutMinimap.
// baked == nil or len==0 uses 2× supersampled tile sampling and ALP 2×2→1 blending [03 §3.7][fmt tnt][fmt pal].
// When baked != nil, its top-left used sub-rectangle (see
// bakedMinimapUsedRect [fmt tnt "Minimap"]) is rescaled through the
// established picture path [03 §3.7]; the padded remainder of the stored
// bitmap never enters the resize.
// The ALP table is mandatory: there is no nearest-neighbor compatibility path.
// The letterbox bars are not this function's to fill. No radar surface covers
// them: PICTURE, MAPPED and FINAL are all allocated at exactly the fitted
// RadarW x RadarH, and the presenter blits FINAL at (padX, padY) and paints no
// fill [03 R-MM-01 §3]. What shows in the bars is whatever the composer frame
// already holds there, and that is now settled: it is the side rail's own art.
//
// This previously carried an open-question marker, "which pixels those are —
// the side-panel shell's own art versus a cleared frame — is Unknown". Two
// established statements close it from opposite ends. The battle presenter
// fills the whole surface with palette index 0 once, stamps `PANELSIDE` at
// (0, 0), and never stamps it again; the per-frame composer repaints only the
// two horizontal strips and dirty GUI windows, so nothing touches the rail's
// columns under the radar canvas for the rest of the battle [07 R-HUD-05]. And
// the stock `PANELSIDE` raster is opaque at every pixel of its authored
// 129×480, radar area included, so the clear underneath is never what shows:
// over the 126×126 the canvas covers, ARMINT and CORINT each carry 15,876
// opaque pixels and not one transparent or index-0 pixel [fmt gaf]. The bars
// are therefore the panel art — the dithered near-black panel texture, which is
// why "cleared to 0" was a plausible reading, but the mechanism is the art and
// modded art would show through.
//
// Retail's own repaint pre-pass copies FINAL over that art each frame within
// the fitted rectangle only, which is why the bars persist rather than being
// overwritten [03 R-MM-01 §1].
func BuildRadarPicture(t *world.Terrain, playW, playH int32, m camera.Minimap, baked []byte, bakedW, bakedH int, tables *palette.Tables) *RadarSurface {
	if m.W <= 0 || m.H <= 0 || tables == nil {
		return nil
	}
	w := int(m.W)
	h := int(m.H)
	pitch := (w + 3) &^ 3 // [03 §3.6][03 §4.1] pitch (w+3)&~3
	bits := make([]byte, w*h)

	// Baked path: rescale through the established picture path [03 §3.7].
	if len(baked) > 0 {
		if bakedW <= 0 || bakedH <= 0 || bakedW > len(baked)/bakedH {
			return nil
		}
		srcW, srcH, src := bakedMinimapUsedRect(baked, bakedW, bakedH, playW, playH)
		resizeALP(bits, w, h, src, srcW, srcH, tables)
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
			tileX := numeric.FloorDiv(int64(worldX), 32)
			tileZ := numeric.FloorDiv(int64(worldZ), 32)

			var pix byte
			if tileX < 0 || tileX >= int64(tileW) || tileZ < 0 || tileZ >= int64(tileH) {
				// Unreachable by construction, and kept as an assertion rather
				// than a behavior: `worldX = PlayRight * x / (2*RadarW)` with
				// `x < 2*RadarW` lies in [0, PlayRight), and likewise for Z, so
				// the generated picture's loop never leaves the tile map and
				// there is no out-of-domain sample to define. The tile-index
				// guard below is the only real one [03 R-MM-01 §3]. Suppressing
				// the picture is the safe answer to a malformed terrain record.
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

// bakedMinimapUsedRect isolates the real terrain image inside a baked TNT
// minimap, discarding the fill that pads the short axis [fmt tnt "Minimap"].
//
// The stored bitmap is `bakedW x bakedH` (252x252 in nearly every retail map,
// 252x256 on at least one), but on a non-square map only a top-left
// sub-rectangle holds real pixels: the long play-area axis fills its full
// stored dimension and the short axis is scaled down by the same ratio, the
// rest of that axis being fill. This mirrors camera.LayoutMinimap's own
// long-side fit, with the baked bitmap's own stored dimension standing in for
// the 126-pixel canvas constant, so no dimension is hard-coded — only the
// bytes actually read from the file drive it [fmt tnt].
//
// Established (2026-09-03) against five shipped maps spanning wide, tall and
// near-square play areas: a wide map's baked image uses its full stored
// width with the bottom rows padded, a tall map's baked image uses its full
// stored height with the right columns padded, and in every case the short
// axis's used length equals `playShort*bakedLong/playLong` truncated — the
// exact figure measured in each file's byte-for-byte fill boundary. See
// [fmt tnt "Minimap"].
//
// When playW or playH is unavailable (<=0) the whole stored bitmap is
// returned unchanged rather than guessed at.
func bakedMinimapUsedRect(baked []byte, bakedW, bakedH int, playW, playH int32) (usedW, usedH int, pixels []byte) {
	if playW <= 0 || playH <= 0 {
		return bakedW, bakedH, baked
	}
	usedW, usedH = bakedW, bakedH
	if playW < playH {
		usedW = int(int64(playW) * int64(bakedW) / int64(playH)) // TRUNC IDIV, short axis
		if usedW < 1 {
			usedW = 1
		}
		if usedW > bakedW {
			usedW = bakedW
		}
	} else if playH < playW {
		usedH = int(int64(playH) * int64(bakedH) / int64(playW)) // TRUNC IDIV, short axis
		if usedH < 1 {
			usedH = 1
		}
		if usedH > bakedH {
			usedH = bakedH
		}
	}
	if usedW == bakedW && usedH == bakedH {
		return bakedW, bakedH, baked
	}
	// The used sub-rectangle sits at the top-left, but its rows are not
	// contiguous in the stored buffer unless usedW == bakedW (the stored row
	// stride is always bakedW), so a narrower crop must be copied row by row
	// into a tightly packed buffer before the generic resizer can treat it as
	// a plain srcW x srcH image.
	cropped := make([]byte, usedW*usedH)
	for y := 0; y < usedH; y++ {
		copy(cropped[y*usedW:(y+1)*usedW], baked[y*bakedW:y*bakedW+usedW])
	}
	return usedW, usedH, cropped
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

// resizeRadarBits returns a length-n byte slice reusing v's storage when it
// fits. Every caller writes all n bytes before reading any, so the retained
// tail is never observed and no clearing is owed.
func resizeRadarBits(v []byte, n int) []byte {
	if cap(v) < n {
		return slices.Grow(v[:0], n)[:n]
	}
	return v[:n]
}

// buildMappedInto implements the MAPPED composite over a caller-owned surface:
// the picture masked by the authoritative LOS grids [03 §3.8][03 §3.3]. It is a
// pure presentation operation [03 §3.6]. The composite writes every pixel of
// the result from the picture and the LOS grids, so the reused storage carries
// nothing of the previous composite; dst nil allocates.
func buildMappedInto(dst, picture *RadarSurface, wordMask []uint16, byteGrid []uint8, mapW, mapH int, localSlot uint8, dcb byte, guiRemap []byte) *RadarSurface {
	if picture == nil || picture.Bits == nil || picture.W <= 0 || picture.H <= 0 {
		return nil
	}
	w := picture.W
	h := picture.H
	pitch := (w + 3) &^ 3 // [03 §3.6]
	out := dst
	if out == nil {
		out = &RadarSurface{}
	}
	bits := resizeRadarBits(out.Bits, w*h)
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
			var value byte
			// word→byte→remap→DCB gate order [03 §3.8].
			if word&mask == 0 {
				value = dcb // [03 §3.8] unexplored → configured fog fill
			} else if bVal == 0 {
				if len(guiRemap) == 256 {
					value = guiRemap[src] // [03 §3.8] GUI remap
				} else {
					value = src // no remap when nil [03 §3.8]
				}
			} else {
				value = src
			}
			bits[y*w+x] = value
		}
	}
	*out = RadarSurface{W: w, H: h, Pitch: pitch, Bits: bits}
	return out
}

// minimapSelectedStatus is the unit status word's selected bit (bit 4), the
// gate on both the sensor circles and the weapon/interceptor rings
// [03 R-MM-01 §3].
const minimapSelectedStatus uint32 = 0x10

// MinimapContact carries world coords already in map pixels (short world>>16) plus owner, flags, def distances [03 §3.9].
type MinimapContact struct {
	WorldX, WorldZ, WorldY int32 // map pixels (short narrow already), WorldY high word for shear [03 §3.9]
	Owner                  uint8
	Palette                byte // caller-resolved owning-player palette index [03 §3.9]
	IsCommander            bool // when true draws commander GAF after blip [03 §3.9]
	Stealth                bool // when true gate on blink [03 §3.9]
	// RangeStatus is the selected-unit circle gate of [03 §3.9] "Selected-unit
	// circle gate correction": the selected/range-status bit is set AND the
	// instance is active or the definition is not on/off-capable. It governs
	// layer 4 only; the blip layer has no such term.
	RangeStatus bool
	// Status and BlinkSuppress are copied from the immutable unit record. The
	// contact gate uses FriendlyMask/owner, while the suppress byte admits a
	// blip only during the shared blink phase. [03 §3.9]
	Status        uint32
	BlinkSuppress uint8
	Visible       bool
	LocalPlayer   uint8
	// Options is the mode-flags word whose bit 9 is the blip gate's first
	// disjunct: the **full-radar bit**, which the `+Radar` cheat toggles and
	// the world rebuild clears [03 R-MM-01 §3][07 R-CAM-01 §6]
	// [08 R-ENTRY-01 §3]. Battle entry starts it clear.
	Options uint32
	// MinimapMode carries the render-flags word's mapping and LOS mask bits,
	// the `+Mapping`/`+LOS` toggles. The gate's second disjunct is both bits
	// clear, which the world-rebuild tail arranges for a watcher slot
	// [03 R-MM-01 §3][07 R-CAM-01 §14].
	MinimapMode  uint8
	RawDistRadar int32
	RawDistSonar int32
	RawDistJamR  int32
	RawDistJamS  int32 // radar distances, 0 means absent [03 §3.10][07 §10]
	RingEnabled  bool
	RingDashed   bool
	RingRange    int32
}

// MinimapContactBlitter is the resolved authored-art adapter. It is called
// after the visibility/gate checks; a nil adapter means the authored blip is
// absent and therefore leaves FINAL untouched. [03 §3.9]
type MinimapContactBlitter func(dst *RadarSurface, x, y int, palette byte, commander bool)

// MinimapContactGate is the contacts pass's visibility gate: a contact reaches
// any of the pass's layers when the full-radar option bit is set, or the
// render-flags word's mapping and LOS bits are both clear, or the unit carries
// either of the friendly-contact status bits (mask 0x300 — the seen marker a
// radar contact or the line-of-sight probe writes, and the sonar bit), or the
// unit's owner is the viewing player [03 §3.9] "Blip gate" [03 R-MM-01 §3].
//
// Visible is the publisher's own resolution of the last two disjuncts against
// authoritative state; it is folded in here rather than recomputed.
func MinimapContactGate(c MinimapContact) bool {
	return c.Visible || c.Options&(1<<9) != 0 || c.MinimapMode&3 == 0 ||
		c.Status&0x300 != 0 || c.Owner == c.LocalPlayer
}

// MinimapBlipAdmitted adds the blip layers' blink term to MinimapContactGate:
// the blip draws when the unit's per-instance blink-suppress countdown reads
// zero OR the shared blink phase bit is set [03 §3.9] layer 2.
//
// Definition `stealth` is not a term. Stealth is the sensor phase's contact
// callback reject [03 R-VIS-01 §5], which is where a stealthy unit fails to
// gain the seen bit; a stealthy unit admitted by line of sight draws a steady
// blip like any other. Nor is the selected-unit circle gate a term here: a unit
// whose blip is suppressed on a non-blink phase still runs its range branches,
// which is what [03 §3.9] "Contact layering and ring-only cases" names.
func MinimapBlipAdmitted(c MinimapContact, blink BlinkState) bool {
	return MinimapContactGate(c) && (c.BlinkSuppress == 0 || blink.Phase&1 != 0)
}

// rebuildFinalExactInto composes FINAL over a caller-owned surface. FINAL is
// wiped from MAPPED before any contact is drawn, so every byte of the reused
// storage is overwritten by that copy and a retained surface produces exactly
// the bytes a fresh one does; dst nil allocates as before.
func rebuildFinalExactInto(dst, mapped *RadarSurface, m camera.Minimap, playW, playH int32, contacts []MinimapContact, blink BlinkState, blit MinimapContactBlitter, radarColor, jammerColor, ringColor byte) *RadarSurface {
	if mapped == nil || mapped.W <= 0 || mapped.H <= 0 || len(mapped.Bits) < mapped.W*mapped.H {
		return nil
	}
	final := dst
	if final == nil {
		final = &RadarSurface{}
	}
	final.W, final.H, final.Pitch = mapped.W, mapped.H, (mapped.W+3)&^3
	final.Bits = resizeRadarBits(final.Bits, mapped.W*mapped.H)
	copy(final.Bits, mapped.Bits)
	// Unit blips precede all circles, and commander art is the next layer.
	for _, c := range contacts {
		if !MinimapBlipAdmitted(c, blink) {
			continue
		}
		rx, ry := RadarProjection(c.WorldX, c.WorldZ, c.WorldY, playW, playH, m)
		if blit != nil {
			blit(final, int(rx), int(ry), c.Palette, false)
		}
	}
	for _, c := range contacts {
		if !MinimapBlipAdmitted(c, blink) || !c.IsCommander {
			continue
		}
		rx, ry := RadarProjection(c.WorldX, c.WorldZ, c.WorldY, playW, playH, m)
		if blit != nil {
			blit(final, int(rx), int(ry), c.Palette, true)
		}
	}

	// Layer 4 — sensor circles, drawn in this same ascending walk after every
	// blit, so they overwrite earlier contact pixels [03 §3.9] "Contact
	// layering and ring-only cases".
	//
	// Two gates, and only two. The unit must pass the pass's blip gate, and the
	// selected-unit circle gate must hold: the selected/range-status bit set,
	// and the instance active or the definition not on/off-capable [03 §3.9]
	// "Selected-unit circle gate correction" (Established). The producer folds
	// the activation term into RangeStatus, so the test here is the bit alone.
	//
	// The per-unit blink countdown and the definition stealth flag are NOT
	// terms of this gate: the same section establishes that a unit whose blip
	// is suppressed on a non-blink phase still runs its range branches, which
	// is what "ring-only" names, and that the cloak/hidden bit and `stealth`
	// are not this callback gate.
	for _, c := range contacts {
		if !MinimapContactGate(c) || !c.RangeStatus {
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

	// Layer 5 — weapon/interceptor rings, a separate layer after the circles
	// [03 §3.9].
	//
	// The ring loop sits **under the selected bit**, like the circles of layer
	// 4: within one unit's iteration retail runs the sensor circles and then,
	// independently of their activation term but still under status bit 4, the
	// ring loop [03 R-MM-01 §3 "rings are gated on selection"]. A detected
	// enemy is never ringed. This loop used to run for any admitted contact
	// carrying the ring flag, which drew enemy weapon rings on the minimap.
	//
	// The blink and stealth terms are not part of it either: a unit whose blip
	// is suppressed on a non-blink phase still runs its range branches, which
	// is what §3.9 calls the ring-only case — the same reason layer 4 carries
	// no blink term.
	for _, c := range contacts {
		if !MinimapContactGate(c) || c.Status&minimapSelectedStatus == 0 || !c.RingEnabled {
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
