package render

// Megamap raster primitives: the fog composite, recoloured icon pictures,
// rings and markers of the host megamap overview (DESIGN_INTERFACE_HUD_INPUT
// §3.15). The contracts are the community draw engine's shipped ProTA 4.8
// megamap ([draw-engine-interface](../../research/extensions/draw-engine-interface.md#prota-48-shipped-megamap));
// they are presentation-only and never read by the simulation [I6].

// MegamapFogBlack is the value the fog writes where the viewer has never
// mapped the ground: the source writes a zero byte, palette index 0.
const MegamapFogBlack byte = 0

// MegamapMarginIndex is the colour both working surfaces are filled with when
// created, which shows in the margins around the fitted image
// [draw-engine-interface "What the view shows"].
const MegamapMarginIndex byte = 95

// ComposeMegamapFog writes the fog composite of picture into dst, which must
// already hold picture's dimensions. Per pixel: palette index 0 where the
// viewer's mapped (history word) bit is clear; the gray table where current
// LOS (the byte grid) is zero; otherwise the picture byte. The grids already
// encode the viewing mode — a disabled mapping mode fills every word bit and a
// disabled LOS mode fills every byte — so the four-way flag table reduces to
// these two tests.
//
// Sampling steps by repeated floating-point additions of spanW/imageW and
// spanH/imageH, truncating each accumulated coordinate; rows start at
// `−(seaLevel / 20)` (integer quotient) and clamp negative rows to zero, the
// offset both LOS branches of the source apply
// [community-patch-rendering "Fog and draw order"].
//
// spanW × spanH is the part of the losW×losH grid the picture covers, in
// 32-pixel LOS cells: the megamap's shared play-area frame, so the fog lies on
// the terrain it darkens (host choice, DESIGN_INTERFACE_HUD_INPUT §3.15).
func ComposeMegamapFog(dst, picture []byte, w, h int, word []uint16, current []uint8, losW, losH int, spanW, spanH float32, viewer uint8, seaLevel int32, gray *[256]byte) {
	if w <= 0 || h <= 0 || len(dst) < w*h || len(picture) < w*h {
		return
	}
	if losW <= 0 || losH <= 0 || len(word) < losW*losH || len(current) < losW*losH || gray == nil || viewer >= 16 {
		copy(dst[:w*h], picture[:w*h])
		return
	}
	mask := uint16(1) << viewer
	xStep := spanW / float32(w)
	yStep := spanH / float32(h)
	fy := float32(-(seaLevel / 20))
	for y := 0; y < h; y++ {
		row := int(fy)
		if fy < 0 {
			row = 0
		}
		if row >= losH {
			row = losH - 1
		}
		fy += yStep
		base := row * losW
		src := picture[y*w : y*w+w]
		out := dst[y*w : y*w+w]
		fx := float32(0)
		for x := 0; x < w; x++ {
			col := int(fx)
			if col >= losW {
				col = losW - 1
			}
			fx += xStep
			i := base + col
			switch {
			case word[i]&mask == 0:
				out[x] = MegamapFogBlack
			case current[i] == 0:
				out[x] = gray[src[x]]
			default:
				out[x] = src[x]
			}
		}
	}
}

// MegamapIcon is one indexed icon picture. Pix holds palette indices; Role
// classifies each pixel for the per-state recolouring.
type MegamapIcon struct {
	W, H int
	Pix  []byte
	Role []MegamapPixelRole
	// Hover is the hover-art colour for the Selected-role pixels; Circle asks
	// for a hover circle instead of hover art (`UseCircleHover`).
	Hover  byte
	Circle bool
}

// MegamapPixelRole is a pixel's part in the icon-configuration contract.
type MegamapPixelRole uint8

const (
	MegamapPixelEmpty    MegamapPixelRole = iota // `TransparentColor`: never drawn
	MegamapPixelInk                              // any other colour: drawn as authored
	MegamapPixelFill                             // `FillColor`: becomes the player's dot colour
	MegamapPixelSelected                         // `SelectedColor`: selection ink
)

// MegamapIconState picks which of the three recoloured copies is drawn.
type MegamapIconState uint8

const (
	MegamapIconNormal MegamapIconState = iota
	MegamapIconSelected
	MegamapIconHovered
)

// BlitMegamapIcon draws icon with its top-left at (x, y) of a w×h surface.
// Selected art keeps the selection ink; normal art drops it; hover art
// (without circle hover) paints it in the hover colour; `FillColor` becomes
// dot in every state [draw-engine-interface "Custom load and colour order"].
//
// Clipping follows the shipped build: a destination start left of or above the
// surface moves to zero without skipping the matching source pixels, so an
// edge icon is drawn shifted rather than cut, and the right edge clips at the
// four-aligned pitch [draw-engine-interface "Unit icons"].
func BlitMegamapIcon(dst []byte, w, h, pitch int, icon *MegamapIcon, x, y int, state MegamapIconState, dot byte) {
	if icon == nil || icon.W <= 0 || icon.H <= 0 || len(icon.Pix) < icon.W*icon.H || len(icon.Role) < icon.W*icon.H {
		return
	}
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	right := min(pitch, w)
	for sy := 0; sy < icon.H; sy++ {
		dy := y + sy
		if dy >= h {
			return
		}
		for sx := 0; sx < icon.W; sx++ {
			dx := x + sx
			if dx >= right {
				break
			}
			i := sy*icon.W + sx
			var v byte
			switch icon.Role[i] {
			case MegamapPixelEmpty:
				continue
			case MegamapPixelFill:
				v = dot
			case MegamapPixelSelected:
				switch {
				case state == MegamapIconSelected:
					v = icon.Pix[i]
				case state == MegamapIconHovered && !icon.Circle:
					v = icon.Hover
				default:
					continue
				}
			default:
				v = icon.Pix[i]
			}
			dst[dy*w+dx] = v
		}
	}
}

// MegamapRingRadius is `distance × rowPitch / extentW` in integer division
// [draw-engine-interface "Rings"]. Every ring uses the horizontal scale.
func MegamapRingRadius(distance, rowPitch, extentW int32) int32 {
	if distance <= 0 || rowPitch <= 0 || extentW <= 0 {
		return 0
	}
	return int32(int64(distance) * int64(rowPitch) / int64(extentW))
}

// MegamapRingThresholds are the `Megamap*Minimum` filters. Each comparison is
// strictly greater-than. The radar-jammer ring compares against Radar: the
// shipped build parses a separate radar-jammer minimum and never uses it
// [draw-engine-interface "Rings"].
type MegamapRingThresholds struct {
	Radar, Sonar, SonarJam, AntiNuke int32
}

// MegamapSensorRings reports which of the four sensor rings a selected allied
// unit draws for its authored distances.
func MegamapSensorRings(t MegamapRingThresholds, radar, sonar, radarJam, sonarJam int32) (drawRadar, drawSonar, drawRadarJam, drawSonarJam bool) {
	return radar > t.Radar, sonar > t.Sonar, radarJam > t.Radar, sonarJam > t.SonarJam
}

// MegamapInterceptorRing reports whether an interceptor slot draws and its
// unscaled radius, `coverage − 512`, the retail minimap's bias that the
// shipped build keeps [draw-engine-interface "Rings"][03 §3.9].
func MegamapInterceptorRing(t MegamapRingThresholds, coverage int32) (int32, bool) {
	if coverage <= t.AntiNuke {
		return 0, false
	}
	return coverage - 512, true
}

// DrawMegamapCircle and DrawMegamapDashedCircle rasterise rings through the
// retail minimap's 32-segment circle [03 §3.10] onto a w×h indexed surface.
// The shipped build's own circle rasteriser is not recorded; sharing the
// retail one is a host choice (DESIGN_INTERFACE_HUD_INPUT §3.15).
func DrawMegamapCircle(dst []byte, w, h, cx, cy, r int, color byte) {
	drawCircle(&RadarSurface{W: w, H: h, Bits: dst[:w*h]}, cx, cy, r, color)
}

func DrawMegamapDashedCircle(dst []byte, w, h, cx, cy, r int, color byte, phase bool) {
	drawDashedCircle(&RadarSurface{W: w, H: h, Bits: dst[:w*h]}, cx, cy, r, color, phase)
}

// FillMegamapBlock writes a clipped size×size block with its top-left at
// (x, y): the 2×2 projectile marker [draw-engine-interface "Projectiles"].
func FillMegamapBlock(dst []byte, w, h, x, y, size int, color byte) {
	for dy := y; dy < y+size; dy++ {
		if dy < 0 || dy >= h {
			continue
		}
		for dx := x; dx < x+size; dx++ {
			if dx >= 0 && dx < w {
				dst[dy*w+dx] = color
			}
		}
	}
}

// MegamapBoxOutlineShown is the selection box's draw gate: the outline shows
// only while both screen extents between the press point and the pointer
// exceed eight pixels [draw-engine-interface "Selection and order overlay"].
func MegamapBoxOutlineShown(x0, y0, x1, y1 int32) bool {
	dx, dy := x1-x0, y1-y0
	return max(dx, -dx) > 8 && max(dy, -dy) > 8
}

// MegamapFootprintRect is the placement ghost's geometry, also used for a
// queued build site: a `footX × 16` by `footZ × 16` world-unit footprint,
// scaled and truncated to image pixels, centred on (cx, cy) and moved back
// inside the w×h image where it would cross an edge. The result is the
// inclusive outline corners [draw-engine-interface "Selection and order
// overlay"].
func MegamapFootprintRect(cx, cy int32, footX, footZ int32, scaleX, scaleY float64, w, h int32) (x0, y0, x1, y1 int32) {
	fw := max(int32(float64(footX*16)*scaleX), 1)
	fh := max(int32(float64(footZ*16)*scaleY), 1)
	x0, y0 = cx-fw/2, cy-fh/2
	x0 = max(min(x0, w-fw), 0)
	y0 = max(min(y0, h-fh), 0)
	return x0, y0, x0 + fw - 1, y0 + fh - 1
}

// MegamapProjectileCell is a projectile's LOS cell: `x / 32` and
// `(z − y/2) / 32` in whole world units, each a signed division truncated
// toward zero with `y/2` truncated first. A negative coordinate is rejected,
// and so is one **strictly greater** than the grid's width or height, so a
// coordinate equal to the width or height passes [draw-engine-interface
// "Projectiles"].
func MegamapProjectileCell(x, y, z, gridW, gridH int32) (cx, cz int32, ok bool) {
	cx, cz = x/32, (z-y/2)/32
	return cx, cz, cx >= 0 && cz >= 0 && cx <= gridW && cz <= gridH
}

// MegamapProjectileSight is the viewing player's sight input to the megamap's
// projectile gate: the visibility mode word's two bits and the two grids.
type MegamapProjectileSight struct {
	// Mode is the visibility mode word: bit 1 is current-sight tracking,
	// bit 0 is Unmapped [03 R-VIS-01 §1].
	Mode    uint8
	W, H    int32
	Current []uint8  // the viewing player's current-sight bytes
	Mapped  []uint16 // the mapping words, one bit per player
	Viewer  uint8
}

// MegamapProjectileAdmitted is the shipped build's four-step projectile test
// for an in-range cell: the owner is the viewer or in the viewer's alliance
// row; otherwise with current sight on, the viewer's current-sight byte at
// the cell; otherwise Unmapped admits everything; otherwise (Mapped with
// Permanent sight) the viewer's bit in the mapping word at the cell
// [draw-engine-interface "Projectiles"].
//
// A cell equal to the grid width passes the bound and reads the next row's
// first cell, as the row-major index does. Host choice: an index past the
// grid's end is not read and rejects the projectile.
func MegamapProjectileAdmitted(allied bool, cx, cz int32, s MegamapProjectileSight) bool {
	if allied {
		return true
	}
	index := int(cz)*int(s.W) + int(cx)
	switch {
	case s.Mode&2 != 0:
		return index < len(s.Current) && s.Current[index] != 0
	case s.Mode&1 != 0:
		return true
	}
	return s.Viewer < 16 && index < len(s.Mapped) && s.Mapped[index]&(1<<s.Viewer) != 0
}

// StrokeMegamapRect outlines an inclusive rectangle, clipped.
func StrokeMegamapRect(dst []byte, w, h, x0, y0, x1, y1 int, color byte) {
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	set := func(x, y int) {
		if x >= 0 && y >= 0 && x < w && y < h {
			dst[y*w+x] = color
		}
	}
	for x := x0; x <= x1; x++ {
		set(x, y0)
		set(x, y1)
	}
	for y := y0; y <= y1; y++ {
		set(x0, y)
		set(x1, y)
	}
}
