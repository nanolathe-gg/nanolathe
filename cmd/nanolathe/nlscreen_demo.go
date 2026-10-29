package main

import (
	"fmt"
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/platform/screenkit"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// Small demonstrations drawn over the live preview for settings a battle
// alone cannot show: the side panel's build pages and the window on a display
// (docs/DESIGN_INTERFACE_HUD_INPUT.md §3.17).

// demoStage is the part of the screen a demonstration may use: right of the
// hero and above the cards.
func (s *nlScreen) demoStage() screenkit.Rect {
	u := s.u()
	x := 800 * u
	return screenkit.Rect{X: x, Y: 120 * u, W: s.w() - x - 40*u, H: float64(s.carouselTop()) - 150*u}
}

// caption is a small tag under a demonstration.
func (s *nlScreen) caption(screen *ebiten.Image, text string, x, y, a float64) {
	u := s.u()
	st := screenkit.Style{Size: 12 * u, Tracking: 0.16, Top: alphaC(nlGreenText, a), Upper: true}
	tw := s.fonts.Display.Measure(text, st) + 24*u
	r := screenkit.Rect{X: x - tw/2, Y: y, W: tw, H: 30 * u}
	screenkit.Fill(screen, r, color.RGBA{6, 12, 6, uint8(215 * a)})
	screenkit.Outline(screen, r, 1*u, alphaC(color.RGBA{60, 100, 60, 255}, a))
	s.fonts.Display.Draw(screen, text, r.X+12*u, r.Y+20*u, st)
}

// drawDemo runs the focused card's demonstration, if it has one.
func (s *nlScreen) drawDemo(screen *ebiten.Image, card nlCard, v int) {
	if card.demo == "" || s.compare || s.preview.frame == nil {
		return
	}
	switch card.demo {
	case "sidebar":
		s.demoSidebar(screen)
	case "fullscreen":
		s.demoFullscreen(screen, v == 1)
	}
}

// nlDraftSidebarRows is the height of the surface a battle at the draft's
// resolution lays the rail out on, and the UI scale it magnifies it by:
// Classic never magnifies it (DESIGN_INTERFACE_HUD_INPUT "Modern UI scale").
func nlDraftSidebarRows(d *nlDraft) (rows, k int) {
	k = 1
	if d.pres.Renderer != "classic" {
		k = int(hud.SurfaceChromeScale(d.pres.UIScale, int32(d.resolution.W), int32(d.resolution.H), settings.MaxChromeScale))
	}
	return d.resolution.H / k, k
}

// demoSidebar draws the resolved products and retained controls on the rows
// the rail has at the selected game's logical resolution and UI scale, as the
// battle lays it out. Both sidebar choices use this same draft.
func (s *nlScreen) demoSidebar(screen *ebiten.Image) {
	u := s.u()
	stage := s.demoStage()
	var preview *nlSidebarPreview
	if names := s.art.pictureNames(); names != nil {
		preview = names.sidebar
	}
	if preview == nil {
		s.fonts.Body.Draw(screen, "No resolved build pages in this content.", stage.X, stage.Y+28*u, screenkit.Style{Size: 11 * u, Top: nlDim})
		return
	}
	c := preview.sidebarProductCatalog
	height, k := nlDraftSidebarRows(&s.draft)
	original := s.draft.pres.ExpandedSidebar == 0
	limit := s.nlSidebarBuildLimit(&s.draft)
	starts, layout := nlSidebarPageStarts(c, original, height, limit, s.draft.pres.SidebarOrders != 0)
	if len(starts) == 0 {
		s.fonts.Body.Draw(screen, "Uses the authored layout; oversized pages fit rows to the available height.", stage.X, stage.Y+28*u, screenkit.Style{Size: 11 * u, Top: nlDim})
		return
	}
	original = original || layout.capacity == 0
	page := int(s.demoT/2.2) % len(starts)
	start, end := starts[page], len(c.cells)
	if page+1 < len(starts) {
		end = starts[page+1]
	}
	products, controls := nlSidebarPreviewItems(c, start, end, height, layout, original)
	// Scale the whole rail uniformly. Rows and the complete orders panel keep
	// their actual logical geometry, even on a short last page.
	scale := min(1.1*u, (stage.H-104*u)/float64(max(height, 1)))
	panel := screenkit.Rect{X: stage.X + stage.W*0.16, Y: stage.Y + 40*u, W: 128 * scale, H: float64(height) * scale}
	screenkit.Shade(screen, panel.Inset(-20*u), 0.6)
	screenkit.Fill(screen, panel, color.RGBA{0, 0, 0, 255})
	if pic := s.art.sidebarImage(preview.backdrop); pic != nil {
		r := panel
		if original {
			r.H = float64(pic.Bounds().Dy()) * scale
		}
		screenkit.Image(screen, pic, r, 1, false)
	}
	project := func(x, y, w, h int32) screenkit.Rect {
		return screenkit.Rect{X: panel.X + float64(x)*scale, Y: panel.Y + float64(y)*scale, W: float64(w) * scale, H: float64(h) * scale}
	}
	minimap := project(1, 1, 126, 126)
	screenkit.Fill(screen, minimap, color.RGBA{16, 30, 22, 255})
	s.fonts.Body.Draw(screen, "Minimap", minimap.X+minimap.W/2, minimap.Y+minimap.H/2, screenkit.Style{Size: 10 * scale, Top: nlDim, Align: 1})
	heading := s.ui.text(nlTextKey{kind: "build pages", i: page + 1, j: len(starts)}, func() string { return fmt.Sprintf("Build  %d / %d", page+1, len(starts)) })
	s.fonts.Display.Draw(screen, heading, panel.X, panel.Y-12*u, screenkit.Style{Size: 13 * u, Tracking: 0.2, Top: nlKicker, Upper: true})
	for _, product := range products {
		r := project(product.rect.X, product.rect.Y, product.rect.W, product.rect.H)
		name := product.source.window.Gadgets[product.source.index].Name
		if pic := s.art.pic(name); pic != nil {
			screenkit.Image(screen, pic, r, 1, false)
		}
		screenkit.Bevel(screen, r, max(0.5, scale), color.RGBA{190, 190, 182, 255}, color.RGBA{20, 20, 18, 255}, false)
	}
	for _, item := range controls {
		if pic := s.art.sidebarImage(preview.controls[item.source]); pic != nil {
			r := project(item.rect.X, item.rect.Y, int32(pic.Bounds().Dx()), int32(pic.Bounds().Dy()))
			screenkit.Image(screen, pic, r, 1, false)
		}
	}
	info := "Authored pages"
	if !original {
		info = fmt.Sprintf("Capacity: %d build slots", layout.capacity)
	}
	orders := "Authored controls"
	if !original {
		orders = "Orders-page controls on own page"
		if layout.inlineOrders {
			orders = "Orders-page controls below build"
		}
	}
	type infoLine struct {
		font  *screenkit.Font
		text  string
		style screenkit.Style
		gap   float64 // to the next line
	}
	lines := []infoLine{
		{s.fonts.Display, info, screenkit.Style{Size: 14 * u, Top: nlGreenText}, 26 * u},
		{s.fonts.Body, fmt.Sprintf("Page items: %d", end-start), screenkit.Style{Size: 12 * u, Top: nlBody}, 24 * u},
		{s.fonts.Body, orders, screenkit.Style{Size: 12 * u, Top: nlBody}, 28 * u},
	}
	if !original && limit > 0 && limit != 6 && limit != 12 {
		lines = append(lines, infoLine{s.fonts.Body, fmt.Sprintf("Current count: %d per page", limit), screenkit.Style{Size: 12 * u, Top: nlAmber}, 28 * u})
	}
	// The live preview behind the demonstration can flash bright (explosions,
	// fire), so its text sits on the same dark backing as a caption.
	infoX, infoY := panel.X+panel.W+32*u, panel.Y+12*u
	textW, textH := 0.0, 0.0
	for i, line := range lines {
		textW = max(textW, line.font.Measure(line.text, line.style))
		if i < len(lines)-1 {
			textH += line.gap
		}
	}
	s.demoBacking(screen, screenkit.Rect{X: infoX, Y: infoY - 18*u, W: textW, H: textH + 24*u})
	for _, line := range lines {
		line.font.Draw(screen, line.text, infoX, infoY, line.style)
		infoY += line.gap
	}
	resolution := s.ui.text(nlTextKey{kind: "sidebar resolution", a: fmt.Sprint(k), i: s.draft.resolution.W, j: s.draft.resolution.H}, func() string {
		if k > 1 {
			return fmt.Sprintf("Game resolution: %d x %d, UI scale %dx", s.draft.resolution.W, s.draft.resolution.H, k)
		}
		return fmt.Sprintf("Game resolution: %d x %d", s.draft.resolution.W, s.draft.resolution.H)
	})
	style := screenkit.Style{Size: 11 * u, Top: nlDim}
	y := panel.Y + panel.H + 28*u
	s.demoBacking(screen, screenkit.Rect{X: panel.X, Y: y - 14*u, W: s.fonts.Body.Measure(resolution, style), H: 18 * u})
	s.fonts.Body.Draw(screen, resolution, panel.X, y, style)
}

// demoBacking is the translucent dark box a caption sits on, padded round r.
func (s *nlScreen) demoBacking(screen *ebiten.Image, r screenkit.Rect) {
	u := s.u()
	screenkit.Fill(screen, screenkit.Rect{X: r.X - 10*u, Y: r.Y - 6*u, W: r.W + 20*u, H: r.H + 12*u}, color.RGBA{6, 12, 6, 215})
}

// demoFullscreen is a display with the game's window on it.
func (s *nlScreen) demoFullscreen(screen *ebiten.Image, full bool) {
	u := s.u()
	stage := s.demoStage()
	mw := min(stage.W*0.7, 520*u)
	mh := mw * 10 / 16
	mon := screenkit.Rect{X: stage.X + (stage.W-mw)/2, Y: stage.Y + 20*u, W: mw, H: mh}
	screenkit.Shade(screen, screenkit.Rect{X: mon.X - 30*u, Y: mon.Y - 20*u, W: mon.W + 60*u, H: mon.H + 90*u}, 0.6)
	screenkit.Fill(screen, mon.Inset(-10*u), color.RGBA{24, 24, 22, 255})
	screenkit.Bevel(screen, mon.Inset(-10*u), 2*u, color.RGBA{120, 120, 112, 255}, color.RGBA{10, 10, 8, 255}, false)
	screenkit.Fill(screen, mon, color.RGBA{16, 30, 44, 255})
	screenkit.Fill(screen, screenkit.Rect{X: mon.X + mon.W/2 - 30*u, Y: mon.Y + mon.H + 10*u, W: 60 * u, H: 26 * u}, color.RGBA{40, 40, 36, 255})
	screenkit.Fill(screen, screenkit.Rect{X: mon.X + mon.W/2 - 70*u, Y: mon.Y + mon.H + 34*u, W: 140 * u, H: 8 * u}, color.RGBA{50, 50, 46, 255})
	target := 0.0
	if full {
		target = 1
	}
	k := s.ease("fullscreen", target, 6)
	ww, wh := mon.W*(0.62+0.38*k), mon.H*(0.62+0.38*k)
	win := screenkit.Rect{X: mon.X + (mon.W-ww)/2, Y: mon.Y + (mon.H-wh)/2, W: ww, H: wh}
	bar := 18 * u * (1 - k)
	if bar > 0.5 {
		screenkit.Fill(screen, screenkit.Rect{X: win.X, Y: win.Y, W: win.W, H: bar}, color.RGBA{200, 200, 196, 255})
		dots := []color.RGBA{{230, 90, 80, 255}, {230, 190, 70, 255}, {90, 200, 90, 255}}
		for i, c := range dots {
			screenkit.Disc(screen, win.X+10*u+float64(i)*12*u, win.Y+bar/2, 3.5*u*(1-k), c)
		}
	}
	body := screenkit.Rect{X: win.X, Y: win.Y + bar, W: win.W, H: win.H - bar}
	if s.preview.frame != nil {
		screenkit.Cover(screen, s.preview.frame, body, 1)
	}
	screenkit.Outline(screen, win, 1*u, color.RGBA{0, 0, 0, 255})
	label := "A window you can move"
	if full {
		label = "The whole display"
	}
	s.caption(screen, label, mon.X+mon.W/2, mon.Y+mon.H+54*u, 1)
}
