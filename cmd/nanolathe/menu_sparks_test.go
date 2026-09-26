package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

func menuSparksOver(index byte) *menuSparks {
	bg := &formats.PCX{Width: menuSparkW, Height: menuSparkH, Pixels: make([]byte, menuSparkW*menuSparkH)}
	for i := range bg.Pixels {
		bg.Pixels[i] = index
	}
	s := newMenuSparks()
	s.bind(&ui.Panel{}, bg)
	return s
}

// A spark is invisible in its spawn frame and then steps three pixels along
// one axis per frame, drawing index 0xAA [07 §5].
func TestMenuSparksSpawnThenStep(t *testing.T) {
	s := menuSparksOver(0x0F)
	s.tick()
	for i, sp := range s.sparks {
		if !sp.active || sp.drawn || s.plane[sp.off] != 0x0F {
			t.Fatalf("record %d after spawn: %+v, pixel %#x", i, sp, s.plane[sp.off])
		}
		if (sp.dx == 0) == (sp.dy == 0) || sp.y >= menuSparkSpawnRows {
			t.Fatalf("record %d spawn direction/position: %+v", i, sp)
		}
	}
	before := s.sparks
	s.tick()
	for i, sp := range s.sparks {
		if !sp.active {
			continue
		}
		b := before[i]
		if sp.x != b.x+int16(b.dx) || sp.y != b.y+int16(b.dy) || s.plane[sp.off] != menuSparkIndex {
			t.Fatalf("record %d did not step from %+v to %+v", i, b, sp)
		}
	}
}

// Only indices with a low nibble of 13 or more admit a spark, at spawn and
// at every step [07 §5].
func TestMenuSparksRejectLowNibble(t *testing.T) {
	s := menuSparksOver(0x0C)
	for range 50 {
		s.tick()
	}
	for i, sp := range s.sparks {
		if sp.active {
			t.Fatalf("record %d spawned over index 0x0C", i)
		}
	}
	s = menuSparksOver(0x0D)
	s.tick()
	sp := &s.sparks[0]
	*sp = menuSpark{x: 100, y: 50, active: true, dx: 3, life: 9, timer: 5, off: 50*menuSparkW + 100}
	s.backup = append([]byte(nil), s.backup...)
	s.backup[50*menuSparkW+103] = 0x0C
	s.plane[50*menuSparkW+103] = 0x0C
	s.tick()
	if sp.active {
		t.Fatalf("spark stepped onto index 0x0C: %+v", *sp)
	}
}

// The turn takes its sign from the parity of the coordinate on the new axis:
// odd y turns +3 vertically, even x turns +3 horizontally. The spawn choice
// uses the opposite convention on its y arm, so the two must stay separate
// [07 §5].
func TestMenuSparksTurnParity(t *testing.T) {
	cases := []struct {
		name           string
		x, y           int16
		dx, dy         int8
		wantDX, wantDY int8
	}{
		{"horizontal at odd y", 100, 51, 3, 0, 0, 3},
		{"horizontal at even y", 100, 50, -3, 0, 0, -3},
		{"vertical at even x", 100, 50, 0, 3, 3, 0},
		{"vertical at odd x", 101, 50, 0, -3, -3, 0},
	}
	for _, tc := range cases {
		s := menuSparksOver(0x0F)
		s.tick()
		sp := &s.sparks[0]
		// Start one step back so the move lands on (x, y) with the timer spent.
		start := menuSpark{x: tc.x - int16(tc.dx), y: tc.y - int16(tc.dy), active: true, dx: tc.dx, dy: tc.dy, life: 9}
		start.off = int32(start.y)*menuSparkW + int32(start.x)
		*sp = start
		s.tick()
		if !sp.active || sp.dx != tc.wantDX || sp.dy != tc.wantDY || sp.timer < 1 || sp.timer > 16 {
			t.Fatalf("%s: got %+v, want dx %d dy %d", tc.name, *sp, tc.wantDX, tc.wantDY)
		}
	}
}

// Sparks draw above the authored MAINMENU gadgets, as in retail's window
// surface, and below the Nanolathe-owned MODS button [07 §5].
func TestMenuSparksLayerBetweenAuthoredAndModsGadgets(t *testing.T) {
	root := probeRetail(t)
	cs, err := openContent(Options{Root: root})
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer cs.Close()
	assets := loadMenuAssets(cs)
	shell := &gameShell{cs: cs, assets: assets, frontend: ui.NewFrontend(modeMenuMain), font: assets.font}
	shell.openMenu(modeMenuMain)
	shell.stepMenuSparks(0)
	panel := shell.activePanel()
	if panel.Window.GadgetIndex("MODS") < 0 {
		t.Fatal("MODS gadget not installed")
	}
	centre := func(name string) (int16, int16) {
		r := panel.Window.Gadgets[panel.Window.GadgetIndex(name)].Rect
		return int16(r.X + r.W/2), int16(r.Y + r.H/2)
	}
	plant := func(i int, x, y int16) {
		off := int32(y)*menuSparkW + int32(x)
		shell.menuSparks.sparks[i] = menuSpark{x: x, y: y, active: true, drawn: true, off: off}
		shell.menuSparks.plane[off] = menuSparkIndex
	}
	mx, my := centre("MODS")
	sx, sy := centre("SINGLE")
	plant(0, mx, my)
	plant(1, sx, sy)

	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetPalette(assets.pal)
	cl.SetUIStage(gameShellUIStage{shell: shell})
	img := cl.ComposeFrame()
	spark := assets.pal.Base[menuSparkIndex]
	at := func(x, y int16) [4]byte {
		r, g, b, a := img.At(int(x), int(y)).RGBA()
		return [4]byte{byte(r >> 8), byte(g >> 8), byte(b >> 8), byte(a >> 8)}
	}
	if got := at(sx, sy); got != spark {
		t.Fatalf("spark over SINGLE drew %v, want the spark colour %v", got, spark)
	}
	if got := at(mx, my); got == spark {
		t.Fatalf("spark drew over the MODS button")
	}
}
