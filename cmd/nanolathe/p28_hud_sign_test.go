//go:build retail

package main

import (
	"image"
	"image/color"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
)

// p28PanelResourceStage limits the asset-backed assertion to the battle shell
// and resource pass. This keeps the artifact focused on the authored panel
// minus and avoids making unrelated world/UI state part of the oracle.
type p28PanelResourceStage struct {
	hud       *retailBattleHUD
	resources *frame.Frame
}

func (s p28PanelResourceStage) DrawUI(c *client.Client, _ client.UIFrame) {
	if s.hud == nil || c == nil {
		return
	}
	blitBattlePanel(c, s.hud.panelTop, 129, 0)
	blitBattlePanel(c, s.hud.panelBottom, 129, 480-32)
	blitBattlePanel(c, s.hud.panelSide, 0, 0)
	if s.resources != nil {
		s.hud.drawResources(c, s.resources, client.DisplayedResources{Energy: 50, Metal: 50})
	}
}

func TestRetailResourceConsumptionUsesAuthoredPanelMinus(t *testing.T) {
	root := testsupport.RetailRoot(t)
	opts := Options{Root: root, Map: "ashap plateau", Seed: 1}
	cs, err := openContent(opts)
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer cs.Close()
	rng.SeedGlobal(1, 1)
	sess, cat, err := newBattleSession(opts, cs)
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	pal := retailPaletteForTest(t, cs)
	if pal == nil {
		t.Skip("retail palette unavailable")
	}
	h, err := loadRetailBattleHUD(cs.fs, sess, cat, pal, nil, newBattleWindowContext(cs, nil))
	if err != nil {
		t.Skipf("retail HUD assets unavailable: %v", err)
	}
	resources := &frame.Frame{Economy: []frame.EconomyView{{
		Player:         h.owner,
		Metal:          50,
		MetalCapacity:  100,
		Energy:         50,
		EnergyCapacity: 100,
		MetalConsumed:  8.7,
		EnergyConsumed: 42,
	}}}

	buf := frame.NewBuffer()
	w := buf.BeginWrite()
	*w = *resources
	if err := buf.Publish(1); err != nil {
		t.Fatal(err)
	}
	c, err := client.New(client.Options{Buffer: buf, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	c.SetPalette(pal)
	c.SetFNT(h.console)
	stage := p28PanelResourceStage{hud: h, resources: resources}
	c.SetUIStage(stage)

	// First capture the authored panel without text. The baseline retains the
	// panel's own minus glyph, so the expected image overlays only "8.7" at the
	// consumption anchor. A signed "-8.7" implementation differs by exactly
	// the extra text-minus glyph and fails this pixel-level comparison.
	stage.resources = nil
	c.SetUIStage(stage)
	panelOnly := c.ComposeFrame()
	stage.resources = resources
	c.SetUIStage(stage)
	withResources := c.ComposeFrame()

	for _, tc := range []struct {
		name       string
		anchor     int
		text       string
		anchorName string
	}{
		{name: "metal consumption", anchor: hud.AnchorMetalConsumed, text: hud.FormatMetalConsumed(resources.Economy[0].MetalConsumed), anchorName: "METALCONSUMED"},
		{name: "energy consumption", anchor: hud.AnchorEnergyConsumed, text: hud.FormatEnergyConsumed(resources.Economy[0].EnergyConsumed), anchorName: "ENERGYCONSUMED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			anchor, ok := h.anchors.ByIndex(tc.anchor)
			if !ok {
				t.Fatalf("retail %s anchor is missing", tc.anchorName)
			}
			want := cloneRGBA(panelOnly)
			mask := make([]byte, 640*480)
			client.DrawText(mask, 640, 480, h.console, tc.text, int(anchor.X1), int(anchor.Y1), 0, 1)
			// guiColor already resolves the semantic entry through the
			// logical→physical map; present time then reads PALETTE.PAL alone
			// [03 §4.3][07 "Retail palette contract"]. Applying the map a
			// second time here was invisible only while it held identity.
			idx := h.guiColor(hud.PaletteConsumption)
			ink := color.RGBA{R: pal.Base[idx][0], G: pal.Base[idx][1], B: pal.Base[idx][2], A: pal.Base[idx][3]}
			if ink.A == 0 {
				ink.A = 255
			}
			for y := 0; y < 480; y++ {
				for x := 0; x < 640; x++ {
					if mask[y*640+x] != 0 {
						want.SetRGBA(x, y, ink)
					}
				}
			}

			// Restrict comparison to the authored text rectangle. Other resource
			// fields are intentionally outside this focused assertion.
			desc := int(h.console.Baseline)
			r := image.Rect(int(anchor.X1), int(anchor.Y1)-desc, int(anchor.X1)+client.MeasureText(h.console, tc.text), int(anchor.Y1)-desc+int(h.console.Height))
			r = r.Intersect(image.Rect(0, 0, 640, 480))
			if r.Empty() {
				t.Fatalf("%s anchor text rectangle is empty: %+v", tc.anchorName, anchor)
			}
			for y := r.Min.Y; y < r.Max.Y; y++ {
				for x := r.Min.X; x < r.Max.X; x++ {
					if got, expected := withResources.RGBAAt(x, y), want.RGBAAt(x, y); got != expected {
						t.Fatalf("%s composition differs at (%d,%d): got %v want %v; text=%q (an extra text '-' would produce '--' with the authored panel sign)", tc.anchorName, x, y, got, expected, tc.text)
					}
				}
			}
		})
	}
}

func cloneRGBA(src *image.RGBA) *image.RGBA {
	dst := image.NewRGBA(src.Bounds())
	copy(dst.Pix, src.Pix)
	return dst
}
