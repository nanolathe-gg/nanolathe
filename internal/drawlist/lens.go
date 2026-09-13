package drawlist

import (
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
)

// Lens is the fixed projectile refraction map [03 R-FX-01 §4]. Its center
// and scale are in recorded world pixels; Clip remains in framebuffer pixels
// so the battle viewport stays fixed during modern zoom (GPU design §16.3).
// Key is physical: captured bytes equal to it leave the destination untouched.
type Lens struct {
	X, Y  int32
	Scale camera.ViewScale
	Clip  Rect
	Key   byte
}

// LensSink is the ordered displacement extension. Both production executors
// implement it; collectors that do not inspect lenses may omit the hook.
type LensSink interface{ Lens(Lens) }

// RecordLens appends one destination-dependent command. Each replay must sample
// after all earlier commands and before any writes from this lens (C-G3).
func (l *List) RecordLens(c Lens) {
	l.order = append(l.order, tag{familyLens, len(l.lens)})
	l.lens = append(l.lens, c)
}

const lensSide = 22
const lensAnchor = 11
const lensTransparent = 32000

// Startup generation uses floating-point radial arithmetic, with separate
// truncations for admission and each source coordinate [03 R-FX-01 §4]. The
// generated table is immutable; only integer offsets cross into the draw path.
var lensOffsets = buildLensOffsets()

func buildLensOffsets() [lensSide * lensSide]int16 {
	var offsets [lensSide * lensSide]int16
	for y := 0; y < lensSide; y++ {
		for x := 0; x < lensSide; x++ {
			dx, dy := x-lensAnchor, y-lensAnchor
			radius := math.Sqrt(float64(dx*dx + dy*dy))
			i := y*lensSide + x
			if int(radius) >= lensSide/4 {
				offsets[i] = lensTransparent
				continue
			}
			factor := (lensAnchor - radius) / 8
			sx, sy := lensAnchor+int(float64(dx)/factor), lensAnchor+int(float64(dy)/factor)
			offsets[i] = int16(sy*lensSide + sx - i)
		}
	}
	return offsets
}

// Bounds returns the scaled footprint in record coordinates. The anchor uses
// the same authored-offset conversion as sprites (GPU design §14.2).
func (l Lens) Bounds() Rect {
	return Rect{X: l.X - l.Scale.Px(lensAnchor), Y: l.Y - l.Scale.Px(lensAnchor), W: l.Scale.Px(lensSide), H: l.Scale.Px(lensSide)}
}

// Source returns the record-space background pixel for a destination, or false
// for the sentinel mask. At detail scale the displacement is doubled while the
// position within the doubled pixel is retained (GPU design §14.2).
func (l Lens) Source(x, y int32) (sx, sy int32, ok bool) {
	b := l.Bounds()
	lx, ly := x-b.X, y-b.Y
	nx, ny := l.Scale.Inverse(lx), l.Scale.Inverse(ly)
	if nx < 0 || ny < 0 || nx >= lensSide || ny >= lensSide {
		return 0, 0, false
	}
	i := ny*lensSide + nx
	offset := lensOffsets[i]
	if offset == lensTransparent {
		return 0, 0, false
	}
	source := i + int32(offset)
	return x + l.Scale.Project(source%lensSide) - l.Scale.Project(nx), y + l.Scale.Project(source/lensSide) - l.Scale.Project(ny), true
}
