package main

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// developerPick resolves a framebuffer pointer against the detached grid.
// Sampling and bracketing follow [03 §2.3][07 §8]; no live terrain is consulted.
func developerPick(b *battleSession, d *frame.DeveloperView, sx, sy int32) (int32, int32, bool) {
	if b == nil || b.cam == nil || d == nil || d.Width <= 0 || d.Height <= 0 || int64(len(d.Cells)) < int64(d.Width)*int64(d.Height) {
		return 0, 0, false
	}
	// As in cursorWorld, restore the beam origin removed by the world recorder
	// before applying the live camera inverse [03 §2.5]. PointerSample is in
	// framebuffer coordinates, not beam coordinates.
	fx, fz := b.cam.ScreenToWorld(sx+camera.OriginX, sy+camera.OriginY)
	px, pz := int32(fx>>16), int32(fz>>16)
	px = min(max(px, 0), d.Width*16-1)
	pz = min(max(pz, 0), d.Height*16-1)
	height := func(x, z int32) int32 {
		cx, cz := x>>4, z>>4
		if cx < 0 || cz < 0 || cx+1 >= d.Width || cz+1 >= d.Height {
			return int32(d.SeaLevel)
		}
		at := func(x, z int32) int32 { return int32(d.Cells[z*d.Width+x].Height) }
		step := func(a, b, f int32) int32 { return a + (b-a)*f/16 }
		return max(int32(d.SeaLevel), step(step(at(cx, cz), at(cx+1, cz), x&15), step(at(cx, cz+1), at(cx+1, cz+1), x&15), z&15))
	}
	z := (pz &^ 15) + 128
	for probe := 0; probe < 9; probe++ {
		north := int32(int16(z)) - (height(px, z) >> 1)
		if north <= pz {
			south := int32(int16(z+16)) - (height(px, z+16) >> 1)
			if north < south && pz <= south {
				z += (pz - north) * 16 / (south - north)
			}
			return px, z, true
		}
		if probe < 8 {
			z -= 16
		}
	}
	return px, z, true
}

func (b *battleSession) drawDeveloperStatus(c *client.Client, f *frame.Frame, font *formats.FNT) {
	if b == nil || c == nil || font == nil {
		return
	}
	d := &b.developer
	if !d.film && d.contourSpacing == 0 && !d.probes.State.Enabled && !d.probes.Builder.Enabled {
		return
	}
	_, height := c.Size()
	text := "Developer: awaiting committed observation (paused views do not step)"
	if f != nil && f.Developer != nil {
		names := [5]string{"normal", "movement/search", "occupancy", "metal", "local coverage"}
		mode := uint8(0)
		if b.sess != nil {
			mode = b.sess.DebugDisplayMode
		}
		if mode > 4 {
			mode = 0
		}
		text = fmt.Sprintf("Developer: %s | tick %d", names[mode], f.Developer.Tick)
		if mode == 1 && len(f.Developer.MovementTiers) == 0 {
			text += " | class unavailable"
		}
		if mode == 1 && len(f.Developer.Search) == 0 {
			text += " | search unavailable"
		}
	}
	c.UIText(font, text, 134, height-48, 83)
}

func (b *battleSession) drawDeveloperFooter(c *client.Client, f *frame.Frame, font *formats.FNT) {
	if c == nil || f == nil || font == nil {
		return
	}
	_, height := c.Size()
	lower := height - int(font.Height) - 1
	upper := lower - 16
	text := func(x, y int, s string) { c.UIText(font, s, x, y, 83) }
	// TODO(question): PFSTATE/PFABLE and packet-counter producer meanings need
	// their missing traces; '-' means unavailable [07 R-HUD-03 §1].
	text(130, lower, "PFSTATE -, PFABLE -")
	text(400, lower, "DELTATIME: -")
	text(520, lower, fmt.Sprintf("GAMETIME: %d", f.Tick))
	if b.cam != nil {
		text(130, upper, fmt.Sprintf("X: %d  Y: %d", b.cam.X, b.cam.Z))
	}
	// The renderer does not publish a retail on-screen unit sweep counter.
	// Expose its absence instead of relabelling a culling/visibility estimate.
	text(264, upper, fmt.Sprintf("UNITS %d\\-", len(f.Units)))
	text(400, upper, "PACKETS: - - -")
	if d := f.Developer; d != nil {
		for _, u := range d.Units {
			if u.Slot == b.footerHoverUnit {
				text(264, lower, fmt.Sprintf("MOVEORD: %d FIREORD: %d", u.MoveStance, u.FireStance))
				break
			}
		}
		p, _ := c.Input().PointerSample()
		if x, z, ok := developerPick(b, d, int32(p.X), int32(p.Y)); ok {
			cx, cz := x>>4, z>>4
			if cx >= 0 && cz >= 0 && cx < d.Width && cz < d.Height {
				text(520, upper, fmt.Sprintf("XYH: %d %d %d", cx, cz, d.Cells[cz*d.Width+cx].Height))
			}
		}
	}
}
