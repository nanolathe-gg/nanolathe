package gpurender

import (
	"fmt"
	"math"

	"github.com/nanolathe/nanolathe/internal/drawlist"
	"github.com/nanolathe/nanolathe/internal/palette"
)

// The §13.4 comparison the device fixtures below use.
//
// The Enhanced composite is true colour: a fixture no longer reads back palette
// indices, it reads back the colours the composite wrote. For a fixture whose
// commands are all opaque the composite must equal the classic byte plane
// expanded through PAL exactly. For a fixture with blended commands, the pixels
// no blended command covers must still be exact, and a covered pixel's distance
// from the classic expansion is bounded by the §13.2 floor of the family that
// covered it plus five units.
//
// Every fixture palette in this package is the 256-entry grey ramp
// (PAL[i] = (i,i,i)) with the ALP table authored as its own builder's arithmetic
// [03 §4.3.4]. In that palette every colour the ALP, LHT and SHD builders can
// form IS a palette entry, so the nearest-palette search adds nothing and the
// family floor of §13.2 is zero: the bound these fixtures use is therefore
// 0 + 5 = compositeTolerance, and what it is really absorbing is the device's
// 8-bit blend rounding, which is under two units.
const (
	compositeFixtureFloor = 0.0
	compositeTolerance    = compositeFixtureFloor + 5
)

// fixtureALP fills a palette's ALP table with the builder's own target — the
// integer floor of each channel pair's sum divided by two [03 §4.3.4]. In the
// grey ramp that target is itself a palette entry, so the table equals the
// formula the Enhanced blend evaluates.
func fixtureALP(p *palette.Tables) {
	for src := 0; src < 256; src++ {
		for dst := 0; dst < 256; dst++ {
			p.Alpha[src*256+dst] = byte((src + dst) / 2)
		}
	}
}

// expandedIndex is one classic palette index as the software expansion wrote it:
// its PAL colour with alpha forced opaque [03 §4.3].
func expandedIndex(p *palette.Tables, idx byte) [3]float64 {
	e := p.Base[idx]
	return [3]float64{float64(e[0]), float64(e[1]), float64(e[2])}
}

// pixelDistance is the RGB distance between a device pixel and an expected
// colour, the measure §13.2 and §13.4 are stated in.
func pixelDistance(pixels []byte, at int, want [3]float64) float64 {
	dr := float64(pixels[at+0]) - want[0]
	dg := float64(pixels[at+1]) - want[1]
	db := float64(pixels[at+2]) - want[2]
	return math.Sqrt(dr*dr + dg*dg + db*db)
}

// checkExactIndex asserts one pixel equals the classic index expanded through
// PAL exactly — the §13.4 rule for a pixel no blended command covers.
func checkExactIndex(name string, pixels []byte, at int, p *palette.Tables, idx byte) error {
	want := expandedIndex(p, idx)
	got := [3]float64{float64(pixels[at]), float64(pixels[at+1]), float64(pixels[at+2])}
	if got != want || pixels[at+3] != 255 {
		return fmt.Errorf("%s: RGBA %v, want the exact expansion of index %d, %v with alpha 255",
			name, pixels[at:at+4], idx, want)
	}
	return nil
}

// checkBlendedIndex asserts one pixel is within the §13.4 bound of the classic
// index expanded through PAL. name states which family's floor is being used.
func checkBlendedIndex(name string, pixels []byte, at int, p *palette.Tables, idx byte) error {
	if d := pixelDistance(pixels, at, expandedIndex(p, idx)); d > compositeTolerance {
		return fmt.Errorf("%s: RGB %v is %.2f from the classic expansion of index %d, over the §13.2 floor %.1f plus 5",
			name, pixels[at:at+3], d, idx, compositeFixtureFloor)
	}
	return nil
}

// compositeStats accumulates the §13.4 measure over a whole surface: every
// uncovered pixel must be exact, and the covered pixels are judged by their mean
// distance from the classic expansion.
type compositeStats struct {
	covered   int
	sum       float64
	worst     float64
	worstAt   int
	worstWant byte
}

func (c *compositeStats) add(pixels []byte, at int, p *palette.Tables, idx byte) {
	d := pixelDistance(pixels, at, expandedIndex(p, idx))
	c.covered++
	c.sum += d
	if d > c.worst {
		c.worst, c.worstAt, c.worstWant = d, at, idx
	}
}

// mean reports the covered pixels' mean distance and whether it is inside the
// §13.4 bound.
func (c *compositeStats) check(name string) error {
	if c.covered == 0 {
		return fmt.Errorf("%s: no blended pixel was compared, so the fixture proves nothing", name)
	}
	mean := c.sum / float64(c.covered)
	if mean > compositeTolerance {
		return fmt.Errorf("%s: %d blended pixels mean %.2f from the classic expansion (worst %.2f at byte %d, want index %d), over the §13.2 floor %.1f plus 5",
			name, c.covered, mean, c.worst, c.worstAt, c.worstWant, compositeFixtureFloor)
	}
	return nil
}

// checkRowScaleDevicePixels is the row families' device fixture (§13.3 "Row
// families"). A FillLitRect or FillShadeRect no longer looks its destination up
// in a table row: it multiplies the destination by the factor that row's builder
// multiplied by — 1 + r/30 for an LHT row and 0.06875·r for an SHD row
// [03 §4.3.4] — through the scale blend, which has to reproduce that product to
// the device's 8-bit rounding and must leave the composite opaque.
//
// The background is index 64, so SHD row 15 (factor 1.03125, the table's
// near-identity row [03 §4.3.2]) leaves it within two units of itself, and SHD
// row 5 scales it by 0.34375.
func checkRowScaleDevicePixels() error {
	const (
		w, h = 64, 8
		back = byte(64)
	)
	pal := fixturePalette()
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return fmt.Errorf("compile row-scale fixture shaders: %w", err)
	}
	// uiShadeRectRaw takes a negative level through SHD row level+32 and a
	// non-negative one through LHT row level.
	cases := []struct {
		name  string
		x     int
		style drawlist.FillStyle
		level int32
		k     float64
	}{
		{"SHD row 15 near-identity", 0, drawlist.FillShadeRect, 15 - 32, 0.06875 * 15},
		{"SHD row 5 darkens", 8, drawlist.FillShadeRect, 5 - 32, 0.06875 * 5},
		{"SHD row 31 clamps at 2", 16, drawlist.FillShadeRect, 31 - 32, 2},
		{"LHT row 30 doubles", 24, drawlist.FillLitRect, 30, 2},
		{"LHT row 8 brightens", 32, drawlist.FillLitRect, 8, 1 + 8.0/30},
		{"LHT row 0 is identity", 40, drawlist.FillLitRect, 0, 1},
	}
	var list drawlist.List
	list.RecordClear()
	list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: w, H: h}, Index: back, Style: drawlist.FillSolid})
	for _, c := range cases {
		list.RecordFill(drawlist.Fill{
			Rect:  drawlist.Rect{X: int32(c.x), Y: 2, W: 6, H: 4},
			Style: c.style, Level: c.level,
		})
	}
	list.RecordExpand()
	img := r.Execute(&list, w, h)
	if img == nil {
		return fmt.Errorf("row-scale fixture returned no image")
	}
	pixels := make([]byte, w*h*4)
	img.ReadPixels(pixels)
	// Nothing a blended command covers: the §13.4 exactness rule applies.
	if err := checkExactIndex("row-scale background", pixels, (1*w+1)*4, &pal, back); err != nil {
		return err
	}
	for _, c := range cases {
		at := (3*w + c.x + 2) * 4
		want := float64(back) * c.k
		for ch := 0; ch < 3; ch++ {
			if d := math.Abs(float64(pixels[at+ch]) - want); d > 2 {
				return fmt.Errorf("%s: channel %d is %d, want %.2f (destination %d scaled by %.5f) within 2",
					c.name, ch, pixels[at+ch], want, back, c.k)
			}
		}
		if pixels[at+3] != 255 {
			return fmt.Errorf("%s: alpha %d, want the composite to stay opaque", c.name, pixels[at+3])
		}
	}
	// The near-identity row states its own contract: it leaves a mid-grey pixel
	// where it found it [03 §4.3.2].
	if d := math.Abs(float64(pixels[(3*w+2)*4]) - float64(back)); d > 2 {
		return fmt.Errorf("SHD row 15 moved index %d to %d, want it within 2 of itself",
			back, pixels[(3*w+2)*4])
	}
	return nil
}
