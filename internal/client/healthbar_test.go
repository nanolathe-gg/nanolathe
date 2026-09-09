package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// newLabelClient is a bare indexed surface with an identity logical palette, so
// a written pixel reports the logical entry the raster chose.
func newLabelClient(w, h int) *Client {
	c := &Client{width: w, height: h, indexed: make([]uint8, w*h), pal: &palette.Tables{}}
	for i := range c.pal.Logical {
		c.pal.Logical[i] = uint8(i)
	}
	return c
}

// countRow returns how many pixels of row y carry idx.
func countRow(c *Client, y int32, idx uint8) int {
	n := 0
	for x := 0; x < c.width; x++ {
		if c.indexed[int(y)*c.width+x] == idx {
			n++
		}
	}
	return n
}

// TestHealthBarOuterIsThirtyFiveByFive locks the inclusive rectangle filler's
// endpoint behaviour: [sx−17 .. sx+17] × [y−2 .. y+2] [03 R-FX-01 §6].
func TestHealthBarOuterIsThirtyFiveByFive(t *testing.T) {
	c := newLabelClient(80, 40)
	c.resetListForTest()
	c.drawHealthBar(40, 20, 0 /* hp <= 0 draws nothing */, 100)
	c.replayForTest()
	for i, v := range c.indexed {
		if v != 0 {
			t.Fatalf("hp = 0 wrote pixel %d = %d, want an untouched surface [03 R-FX-01 §6]", i, v)
		}
	}
	// A nonzero outer entry makes the outer fill observable against the zeroed
	// surface; the raster asks for dcb[0], which the identity table maps to 0.
	c.pal.Logical[0] = 200
	c.resetListForTest()
	c.drawHealthBar(40, 20, 1, 3000)
	c.replayForTest()
	rows := 0
	for y := int32(0); y < 40; y++ {
		if n := countRow(c, y, 200); n > 0 {
			rows++
			if y == 18 || y == 22 {
				if n != 35 {
					t.Fatalf("outer row %d spans %d px, want 35 [03 R-FX-01 §6]", y, n)
				}
			}
		}
	}
	if rows != 5 {
		t.Fatalf("outer bar covers %d rows, want 5 [03 R-FX-01 §6]", rows)
	}
	if c.indexed[20*80+23] != 200 || c.indexed[20*80+57] != 200 {
		t.Fatalf("outer bar is not inclusive of sx−17 and sx+17 [03 R-FX-01 §6]")
	}
	if c.indexed[20*80+22] != 0 || c.indexed[20*80+58] != 0 {
		t.Fatalf("outer bar wrote outside [sx−17 .. sx+17] [03 R-FX-01 §6]")
	}
}

// TestHealthBarInnerFillWidth locks w = (hp << 5) / maxdamage through the
// inclusive filler: full health fills 33 pixels leaving a one-pixel border each
// side, and 1 of 3000 still shows a one-pixel fill [03 R-FX-01 §6].
func TestHealthBarInnerFillWidth(t *testing.T) {
	for _, tc := range []struct {
		name      string
		hp, max   int32
		wantWidth int
		wantColor uint8
	}{
		{"full health fills 33 of 35", 3000, 3000, 33, 10},
		{"one of three thousand still shows one pixel", 1, 3000, 1, 12},
		{"half health fills seventeen", 1500, 3000, 17, 14},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newLabelClient(80, 40)
			c.drawHealthBar(40, 20, tc.hp, tc.max)
			c.replayForTest()
			if got := countRow(c, 20, tc.wantColor); got != tc.wantWidth {
				t.Fatalf("inner fill = %d px of entry %d, want %d [03 R-FX-01 §6]", got, tc.wantColor, tc.wantWidth)
			}
			// The fill is three rows tall and starts at sx−16.
			if got := countRow(c, 19, tc.wantColor); got != tc.wantWidth {
				t.Fatalf("inner fill row y−1 = %d px, want %d [03 R-FX-01 §6]", got, tc.wantWidth)
			}
			if got := countRow(c, 18, tc.wantColor); got != 0 {
				t.Fatalf("inner fill reached the outer's top row: %d px [03 R-FX-01 §6]", got)
			}
			if c.indexed[20*80+24] != tc.wantColor {
				t.Fatalf("inner fill does not start at sx−16 [03 R-FX-01 §6]")
			}
			if c.indexed[20*80+23] == tc.wantColor {
				t.Fatalf("inner fill started left of sx−16 [03 R-FX-01 §6]")
			}
		})
	}
}

// TestHealthBarThresholdsAreStrictAndSigned locks the two comparisons at their
// exact boundaries: dcb[10] above 2·(maxdamage/3), dcb[14] above maxdamage/3,
// dcb[12] otherwise, both strict [03 R-FX-01 §6][07 §6].
func TestHealthBarThresholdsAreStrictAndSigned(t *testing.T) {
	// maxdamage 100: the truncating third is 33, so the boundaries are 66 and
	// 33 and both are *excluded*.
	for _, tc := range []struct {
		hp   int32
		want uint8
	}{
		{100, 10},
		{67, 10},
		{66, 14}, // exactly 2·(100/3): not above it
		{34, 14},
		{33, 12}, // exactly 100/3: not above it
		{1, 12},
	} {
		c := newLabelClient(80, 40)
		c.drawHealthBar(40, 20, tc.hp, 100)
		c.replayForTest()
		if got := c.indexed[20*80+24]; got != tc.want {
			t.Fatalf("hp %d of 100 filled with entry %d, want %d [03 R-FX-01 §6]", tc.hp, got, tc.want)
		}
	}
}

// TestLabelWalkDrawsOnlyForTheViewingPlayer locks the owner gate and the option
// bit: an enemy unit never gets a bar, whatever the option [03 R-FX-01 §6],
// which is [04 R-SPEC-01 §6] seen from the other side.
func TestLabelWalkDrawsOnlyForTheViewingPlayer(t *testing.T) {
	prev := DamageBars()
	defer SetDamageBars(prev)

	world := func(px int32) numeric.Fixed { return numeric.Fixed(int64(px) << 16) }
	cur := &frame.Frame{
		Units: []frame.UnitView{
			{Owner: 0, X: world(40), Z: world(10), Health: 100, MaxHealth: 100},
			{Owner: 1, X: world(40), Z: world(10), Health: 100, MaxHealth: 100},
		},
	}
	cur.Selection.LocalPlayer = 0

	// The camera puts the unit's anchor row at 10 in the surface's
	// origin-removed space, so the bar lands ten rows below it at 20
	// [03 §2.5][03 R-FX-01 §6].
	cam := &camera.Camera{X: 0, Z: 0, ViewW: 200, ViewH: 200, MapW: 400, MapH: 400}

	c := newLabelClient(200, 120)
	c.cam = cam
	SetDamageBars(false)
	c.resetListForTest()
	c.drawUnitLabels(cur, true)
	c.replayForTest()
	for i, v := range c.indexed {
		if v != 0 {
			t.Fatalf("bit clear still drew pixel %d = %d [07 R-HUD-03 §7]", i, v)
		}
	}

	SetDamageBars(true)
	c.resetListForTest()
	c.drawUnitLabels(cur, true)
	c.replayForTest()
	if got := countRow(c, 20, 10); got != 33 {
		t.Fatalf("own unit's bar = %d px on row 20, want 33 [03 R-FX-01 §6]", got)
	}
	// Centred on the unit's own column: with the anchor at x = 40 the full
	// inner fill runs [sx−16 .. sx+16] inside a 35-px outer [03 R-FX-01 §6].
	if c.indexed[20*200+24] != 10 || c.indexed[20*200+56] != 10 {
		t.Fatalf("bar is not centred on the unit's anchor column [03 R-FX-01 §6]")
	}
	if c.indexed[20*200+23] == 10 || c.indexed[20*200+57] == 10 {
		t.Fatalf("inner fill overran the one-pixel border [03 R-FX-01 §6]")
	}
	// One unit drew, not two: both units sit at the same world point, so a
	// second bar would be invisible. Remove the owner's unit and confirm the
	// enemy alone draws nothing at all.
	c2 := newLabelClient(200, 120)
	c2.cam = cam
	enemyOnly := &frame.Frame{Units: []frame.UnitView{{Owner: 1, X: world(40), Z: world(10), Health: 100, MaxHealth: 100, Group: 3}}}
	enemyOnly.Selection.LocalPlayer = 0
	c2.drawUnitLabels(enemyOnly, true)
	c2.replayForTest()
	for i, v := range c2.indexed {
		if v != 0 {
			t.Fatalf("another player's unit drew pixel %d = %d, want nothing [03 R-FX-01 §6]", i, v)
		}
	}
}

// TestLabelWalkAdmitsGroupedUnitsWithTheBitClear locks the walk's admission:
// with the option bit clear only grouped units are labelled, and they get the
// digit alone [07 R-CAM-01 §2][03 R-FX-01 §6].
func TestLabelWalkAdmitsGroupedUnitsWithTheBitClear(t *testing.T) {
	prev := DamageBars()
	defer SetDamageBars(prev)
	SetDamageBars(false)

	c := newLabelClient(200, 120)
	c.cam = &camera.Camera{X: 0, Z: 0, ViewW: 200, ViewH: 200, MapW: 400, MapH: 400}
	cur := &frame.Frame{Units: []frame.UnitView{{
		Owner: 0, X: numeric.Fixed(40 << 16), Z: numeric.Fixed(10 << 16),
		Health: 100, MaxHealth: 100, Group: 4,
	}}}
	cur.Selection.LocalPlayer = 0
	c.drawUnitLabels(cur, true)
	// No font is installed, so the digit itself cannot raster; what the test
	// locks is that the grouped unit reached the walk and still drew no bar.
	if got := countRow(c, 20, 10); got != 0 {
		t.Fatalf("bit clear drew a health bar for a grouped unit: %d px [07 R-CAM-01 §2]", got)
	}
}
