package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The lit-disc families are recorded two ways (docs/DESIGN_GPU_RENDERER.md
// §13.11): the classic lane emits one lit point per covered screen pixel, the
// modern lane one command the executor draws as a quad. The two must describe
// the same pixels, because the classic executor replays a modern list by
// expanding the command back into points, and because the recorded gate is the
// one thing the quad path cannot re-derive per pixel.

// litDiscClient is a client with a loaded map, a camera scrolled off the map's
// corner so the terrain gate actually rejects something, and a brightening
// light table.
func litDiscClient(scale camera.ViewScale) *Client {
	return &Client{
		width: 96, height: 96,
		indexed: make([]uint8, 96*96),
		cam:     &camera.Camera{X: -12, Z: -8, Scale: scale},
		pal:     brighteningTables(),
		terrain: &world.Terrain{CellW: 4, CellH: 3},
	}
}

// litDiscPoints collects every lit point one recorded list holds, in record
// order.
func litDiscPoints(l *drawlist.List) []drawlist.Point {
	var out []drawlist.Point
	l.Replay(&litPointTrace{out: &out})
	return out
}

type litPointTrace struct {
	drawlist.Sink
	out *[]drawlist.Point
}

func (t *litPointTrace) Flash(f drawlist.Flash) { f.Expand(t.emit) }
func (t *litPointTrace) Halo(h drawlist.Halo)   { h.Expand(t.emit) }
func (t *litPointTrace) emit(x, y int32, row uint8) {
	*t.out = append(*t.out, drawlist.Point{X: x, Y: y, Index: row})
}

func (t *litPointTrace) Points(p drawlist.Points) {
	if p.Kind != drawlist.PointLit {
		return
	}
	*t.out = append(*t.out, p.Points...)
}

// TestLitDiscCommandsExpandToTheClassicPoints locks the two lanes against each
// other at every view scale: the modern lane's flash and halo commands, expanded
// the way the classic sink expands them, are exactly the points the classic lane
// recorded — same pixels, same LHT rows, same order.
func TestLitDiscCommandsExpandToTheClassicPoints(t *testing.T) {
	for _, scale := range []camera.ViewScale{camera.ViewScaleNative, camera.ViewScaleMid, camera.ViewScaleDetail} {
		record := func(modern bool) []drawlist.Point {
			c := litDiscClient(scale)
			c.recordModelGeometry = modern
			// The disc's own centre sits inside the map, so the gate rejects part
			// of it and admits the rest.
			if !c.drawCalculatedFlash(0, 3, 30, 24, c.terrainScreenCoverage) {
				t.Fatalf("scale %v modern=%v: the flash recorded nothing", scale, modern)
			}
			c.drawLHTHalo(20, 18, int(scale.Px(9)), 7, c.terrainScreenCoverage)
			return litDiscPoints(&c.list)
		}
		classic, modern := record(false), record(true)
		if len(classic) == 0 {
			t.Fatalf("scale %v: the classic lane recorded no lit points", scale)
		}
		if len(classic) != len(modern) {
			t.Fatalf("scale %v: classic lane %d lit points, modern lane expands to %d", scale, len(classic), len(modern))
		}
		for i := range classic {
			if classic[i] != modern[i] {
				t.Fatalf("scale %v: lit point %d is %+v classic, %+v expanded", scale, i, classic[i], modern[i])
			}
		}
	}
}

// The recorded gate is terrainScreenCoverage expressed as a rectangle. The
// predicate is two independent half-open range tests, so the admitted set is a
// rectangle; this locks the two readings against each other over the whole
// recording extent and a margin outside it [03 §4.3.1].
func TestTerrainScreenRectMatchesTheCoveragePredicate(t *testing.T) {
	c := litDiscClient(camera.ViewScaleNative)
	rect := c.terrainScreenRect()
	for y := -20; y < 120; y++ {
		for x := -20; x < 120; x++ {
			if got, want := rect.Contains(int32(x), int32(y)), c.terrainScreenCoverage(x, y); got != want {
				t.Fatalf("pixel (%d,%d): rectangle says %v, the predicate says %v", x, y, got, want)
			}
		}
	}
	// With no map loaded the predicate admits nothing, and so must the rectangle.
	empty := &Client{cam: &camera.Camera{}}
	if r := empty.terrainScreenRect(); r.W != 0 || r.H != 0 {
		t.Fatalf("with no terrain the rectangle is %+v, want empty", r)
	}
}

// A disc whose covered pixels all fall outside the gate records nothing in
// either lane, which is what keeps the halo count the same on both.
func TestLitDiscOutsideTheTerrainRecordsNothing(t *testing.T) {
	for _, modern := range []bool{false, true} {
		c := litDiscClient(camera.ViewScaleNative)
		c.recordModelGeometry = modern
		// Far east of the map's right edge (the map is 64 pixels wide from
		// screen x = 12).
		if c.drawCalculatedFlash(0, 0, 400, 400, c.terrainScreenCoverage) {
			t.Fatalf("modern=%v: a flash off the map recorded something", modern)
		}
		if got := len(litDiscPoints(&c.list)); got != 0 {
			t.Fatalf("modern=%v: a flash off the map covers %d pixels, want none", modern, got)
		}
	}
}

// The generated frame's Rows are its Pixels resolved to LHT rows: the ramp
// 0x4F..0x6E is rows 0..31 and the transparent key is the sentinel
// [03 §4.3.1][03 R-FX-01 §4][06 R-WFX-01 §2].
func TestFlashDiscRowsMatchTheRampMapping(t *testing.T) {
	c := &Client{pal: &palette.Tables{}}
	d, _ := c.flashFrame(0, 0)
	if d == nil || d.Side <= 0 {
		t.Fatal("table 0 frame 0 did not generate")
	}
	seen := map[uint8]bool{}
	for i, b := range d.Pixels {
		want := uint8(drawlist.FlashTransparentRow)
		if b != flashTransparent {
			want = b - flashRampBase
			if want > 31 {
				t.Fatalf("texel %d byte %#x resolves to row %d, outside the LHT", i, b, want)
			}
		}
		if d.Rows[i] != want {
			t.Fatalf("texel %d byte %#x has row %d, want %d", i, b, d.Rows[i], want)
		}
		seen[d.Rows[i]] = true
	}
	if !seen[drawlist.FlashTransparentRow] {
		t.Fatal("the generated frame has no transparent texel, so the sentinel is untested")
	}
}
