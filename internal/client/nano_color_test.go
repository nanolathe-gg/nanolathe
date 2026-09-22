package client

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
)

func teamNanoFixture() (*Client, frame.StripView) {
	c := &Client{width: 64, height: 64, indexed: make([]byte, 64*64), cam: &camera.Camera{ViewW: 64, ViewH: 64}, enhanced: true}
	p := &palette.Tables{}
	for i := 0; i < 7; i++ {
		level := byte(40 + i*30)
		p.Base[0xa1+i] = [4]byte{0, level, 0, 255}
		p.Base[40+i] = [4]byte{0, 0, level, 255}
	}
	c.SetPalette(p)
	c.SetStrategicTeamArt(&formats.GAFEntry{Frames: []formats.GAFFrameRef{{Frame: &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{46}, Transparent: []bool{false}}}}})
	c.effects.TeamNanospray = true
	return c, frame.StripView{Strip: 6, Family: frame.StripFamilyNano, Fill: 0xa1, NanoOwnerColorKnown: true, X: px(20), Z: px(20)}
}

// Optional presentation must preserve phase, visibility, defaults and the
// immutable snapshot; the team selector is independent of player slot.
func TestTeamNanoRampAndFallbacks(t *testing.T) {
	c, v := teamNanoFixture()
	for i := 0; i < 7; i++ {
		v.Fill = byte(0xa1 + i)
		index, ramp, ok := c.nanoParticleColor(v)
		if !ok || index != byte(40+i) || ramp[i] != index {
			t.Fatalf("phase %d = %d %v %v", i, index, ramp, ok)
		}
	}
	for _, tc := range []struct {
		name string
		edit func(*Client, *frame.StripView)
	}{
		{"classic", func(c *Client, v *frame.StripView) { c.enhanced = false }},
		{"disabled", func(c *Client, v *frame.StripView) { c.effects.TeamNanospray = false }},
		{"unknown owner", func(c *Client, v *frame.StripView) { v.NanoOwnerColorKnown = false }},
		{"missing logo", func(c *Client, v *frame.StripView) { c.SetStrategicTeamArt(nil) }},
		{"missing selector", func(c *Client, v *frame.StripView) { v.NanoOwnerColor = 9 }},
		{"missing palette", func(c *Client, v *frame.StripView) { c.SetPalette(nil) }},
		{"sprinkle", func(c *Client, v *frame.StripView) { v.Family = frame.StripFamilySprinkle }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, v := teamNanoFixture()
			tc.edit(c, &v)
			got, _, ok := c.nanoParticleColor(v)
			if ok || got != v.Fill {
				t.Fatalf("fallback=%d,%v", got, ok)
			}
		})
	}
	c, v = teamNanoFixture()
	cur := &frame.Frame{Visibility: fullVisibility(), Strips: []frame.StripView{v}}
	c.drawStripBarrier(cur, 6)
	c.list.VisitNanoSources(func(f drawlist.Fill) {
		if !f.NanoTeam || f.Index != 40 {
			t.Fatalf("fill=%+v", f)
		}
	})
	if cur.Strips[0] != v {
		t.Fatal("mutated publication")
	}
	c.list.Reset()
	cur.Visibility = frame.VisibilityView{}
	c.drawStripBarrier(cur, 6)
	c.list.VisitNanoSources(func(drawlist.Fill) { t.Fatal("unseen spray recorded") })
	// A live toggle must restore green without waiting for a new simulation tick.
	c.effects.TeamNanospray = false
	if got, _, _ := c.nanoParticleColor(v); got != v.Fill {
		t.Fatal("toggle retained tint")
	}
	c.effects.TeamNanospray = true
	replacement := *c.pal
	replacement.Base[40], replacement.Base[90] = replacement.Base[90], replacement.Base[40]
	c.SetPalette(&replacement)
	if got, _, _ := c.nanoParticleColor(v); got != 90 {
		t.Fatalf("palette replacement retained cached ramp: %d", got)
	}
}

// The optional capture uses retail palette/logo hues through the production
// recorder. First row is green; subsequent rows follow the authored logo order.
func TestTeamNanoRetailPaletteCapture(t *testing.T) {
	fs := vfs.New()
	if err := fs.MountGameDirectory(testsupport.RetailRoot(t)); err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	pal, err := palette.Load(fs)
	if err != nil {
		t.Fatal(err)
	}
	logos, err := formats.LoadGAFFile(fs, "textures/logos.gaf")
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := logos.Find("32xlogos")
	if !ok {
		t.Fatal("missing team logos")
	}
	const w, h = 640, 480
	c := &Client{width: w, height: h, indexed: make([]byte, w*h), cam: &camera.Camera{ViewW: w, ViewH: h}, enhanced: true}
	c.SetPalette(pal)
	c.SetStrategicTeamArt(entry)
	cur := &frame.Frame{Visibility: fullVisibility()}
	rows := min(len(entry.Frames)+1, 11)
	for row := 0; row < rows; row++ {
		c.effects.TeamNanospray = row > 0
		for i := 0; i < 140; i++ {
			v := frame.StripView{Strip: 6, Family: frame.StripFamilyNano, Fill: byte(0xa1 + i%7), NanoOwnerColor: byte(max(0, row-1)), NanoOwnerColorKnown: true, X: px(int32(130 + i*3)), Z: px(int32(25 + row*40 + (i * 17 % 23) - 11))}
			cur.Strips = []frame.StripView{v}
			c.drawStripBarrier(cur, 6)
		}
	}
	c.replayForTest()
	if path := os.Getenv("NANOLATHE_TEAM_NANO_SHOT"); path != "" {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				p := pal.Base[c.indexed[y*w+x]]
				img.SetRGBA(x, y, color.RGBA{p[0], p[1], p[2], 255})
			}
		}
		// The authored logo beside each spray identifies its player colour.
		for row := 1; row < rows; row++ {
			f := entry.Frames[row-1].Frame
			for y := 0; y < int(f.Height); y++ {
				for x := 0; x < int(f.Width); x++ {
					index, ok := f.At(x, y)
					if ok {
						p := pal.Base[index]
						img.SetRGBA(40+x, 10+row*40+y, color.RGBA{p[0], p[1], p[2], 255})
					}
				}
			}
		}
		out, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer out.Close()
		if err := png.Encode(out, img); err != nil {
			t.Fatal(err)
		}
	}
}
