package main

// Retail battle HUD composition. This file deliberately contains no Nanolathe
// layout constants for controls: the side's SIDEDATA anchors, intgaf PANEL
// frames, and authored .GUI windows are the layout. The few fixed coordinates
// below are the retail panel-shell call sites recovered from TotalA.exe.

import (
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/snapshot"
	"github.com/nanolathe/nanolathe/vfs"
)

type retailBattleHUD struct {
	side    *content.SideDef
	cat     *content.Catalog
	owner   uint8
	anchors hud.Anchors

	console *formats.FNT
	guiFont *formats.FNT
	pal     *palette.Tables

	panelTop    *formats.GAFFrame
	panelSide   *formats.GAFFrame
	panelBottom *formats.GAFFrame
	common      *formats.GAF
	oldMain     *formats.GAF
	share       *formats.GAF
	logos       *formats.GAF

	panel *hud.Panel
	fs    *vfs.FS
	pages map[string]*formats.GAF
}

// loadRetailBattleHUD binds the same side-selected resources as the retail
// battle entry path. Missing side fonts, anchors, interface GAF, or panel
// frames are data errors; there is no fallback custom HUD.
func loadRetailBattleHUD(fs *vfs.FS, sess *session.Session, cat *content.Catalog, pal *palette.Tables) (*retailBattleHUD, error) {
	if fs == nil || sess == nil || cat == nil {
		return nil, fmt.Errorf("battle HUD: missing VFS, session, or catalog")
	}
	if pal == nil {
		return nil, fmt.Errorf("battle HUD: PALETTE.PAL tables are required [03 §4.3]")
	}
	side, err := battleSide(sess, cat)
	if err != nil {
		return nil, err
	}
	anchors, err := hud.AnchorsFromSide(side)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(side.Font) == "" || strings.TrimSpace(side.FontGUI) == "" {
		return nil, fmt.Errorf("battle HUD: side %s has no console/gui font [02 §6]", side.Name)
	}
	console, err := formats.LoadFNTFile(fs, "fonts/"+strings.ToLower(side.Font)+".fnt")
	if err != nil {
		return nil, fmt.Errorf("battle HUD: side %s font %q: %w [02 §6]", side.Name, side.Font, err)
	}
	guiFont, err := formats.LoadFNTFile(fs, "fonts/"+strings.ToLower(side.FontGUI)+".fnt")
	if err != nil {
		return nil, fmt.Errorf("battle HUD: side %s GUI font %q: %w [02 §6]", side.Name, side.FontGUI, err)
	}
	intGAF, err := formats.LoadGAFFile(fs, "anims/"+strings.ToLower(side.IntGAF)+".gaf")
	if err != nil {
		return nil, fmt.Errorf("battle HUD: side %s intgaf %q: %w [02 §6]", side.Name, side.IntGAF, err)
	}
	panelTop, err := battleFrame(intGAF, "PANELTOP")
	if err != nil {
		return nil, err
	}
	panelSide, err := battleFrame(intGAF, "PANELSIDE")
	if err != nil {
		return nil, err
	}
	panelBottom, err := battleFrame(intGAF, "PANELBOT")
	if err != nil {
		return nil, err
	}
	common, err := formats.LoadGAFFile(fs, "anims/commongui.gaf")
	if err != nil {
		return nil, fmt.Errorf("battle HUD: commongui.gaf: %w", err)
	}
	oldMain, err := formats.LoadGAFFile(fs, "anims/oldmain.gaf")
	if err != nil {
		return nil, fmt.Errorf("battle HUD: oldmain.gaf: %w", err)
	}
	share, err := formats.LoadGAFFile(fs, "anims/share.gaf")
	if err != nil {
		return nil, fmt.Errorf("battle HUD: share.gaf: %w", err)
	}
	logos, err := formats.LoadGAFFile(fs, "textures/logos.gaf")
	if err != nil {
		return nil, fmt.Errorf("battle HUD: textures/logos.gaf: %w", err)
	}
	return &retailBattleHUD{
		side: side, cat: cat, owner: sess.LocalOwner, anchors: anchors, console: console, guiFont: guiFont, pal: pal,
		panelTop: panelTop, panelSide: panelSide, panelBottom: panelBottom,
		common: common, oldMain: oldMain, share: share, logos: logos,
		panel: hud.NewPanel(0x04, 640, 480, nil), fs: fs,
		pages: make(map[string]*formats.GAF),
	}, nil
}

func battleFrame(g *formats.GAF, name string) (*formats.GAFFrame, error) {
	if g == nil {
		return nil, fmt.Errorf("battle HUD: nil interface GAF while looking for %s", name)
	}
	e, ok := g.Find(name)
	if !ok || len(e.Frames) == 0 || e.Frames[0].Frame == nil {
		return nil, fmt.Errorf("battle HUD: interface GAF missing %s [02 §6]", name)
	}
	return e.Frames[0].Frame, nil
}

func battleSide(sess *session.Session, cat *content.Catalog) (*content.SideDef, error) {
	if cat == nil || len(cat.Sides) == 0 {
		return nil, fmt.Errorf("battle HUD: no compiled side definitions [02 §6]")
	}
	idx := 0
	if sess != nil && sess.Mission != nil && sess.Mission.Type == mission.TypeCampaign { // campaign missions use commander identity below
		idx = -1
	}
	if sess != nil {
		// Skirmish retains the lobby's authored SIDE ordinal. Mission sessions
		// do not have a lobby row, so their local commander selects the side.
		if sess.Mission == nil || sess.Mission.Type != mission.TypeCampaign {
			owner := int(sess.LocalOwner)
			if owner >= 0 && owner < len(sess.Skirmish.Players) {
				idx = sess.Skirmish.Players[owner].Side
			}
		}
		if idx < 0 {
			if sess.Units != nil {
				for _, u := range sess.Units.Iter() {
					if u == nil || !u.Alive || u.Owner != sess.LocalOwner || u.Def == nil {
						continue
					}
					for i, side := range cat.Sides {
						if side != nil && strings.EqualFold(side.Commander, u.Def.UnitName) {
							idx = i
							break
						}
					}
					if idx >= 0 {
						break
					}
				}
			}
		}
	}
	if idx < 0 || idx >= len(cat.Sides) || cat.Sides[idx] == nil {
		return nil, fmt.Errorf("battle HUD: local side %d is unavailable [02 §6]", idx)
	}
	return cat.Sides[idx], nil
}

func (h *retailBattleHUD) draw(c *client.Client, b *battleSession) {
	if h == nil || c == nil {
		return
	}
	// The shell call order is PANELTOP, PANELBOT, PANELSIDE. The two horizontal
	// frames are static at the authored 129-pixel rail boundary; only the side
	// strip and its GUI contents use the panel slide offset [07 §6].
	c.UIBlitAnchor(h.panelTop, 129, 0)
	c.UIBlitAnchor(h.panelBottom, 129, 480-32)
	offset := 0
	if h.panel != nil {
		h.panel.AdvanceNow(c.Input().Kbd.KeyHeld(input.KeySpace), false)
		offset = int(h.panel.Offset)
	}
	c.UIBlitAnchor(h.panelSide, 0, offset)

	prev, cur, ok := c.Buffer().Read()
	if ok && cur != nil {
		h.drawResources(c, cur)
		h.drawSelectedUnit(c, cur)
	}
	_ = prev
	h.drawSidePage(c, b, offset, cur)
}

func (h *retailBattleHUD) drawResources(c *client.Client, f *snapshot.Frame) {
	var res *snapshot.ResourceView
	for i := range f.Resources {
		if f.Resources[i].Player == h.owner {
			res = &f.Resources[i]
			break
		}
	}
	if res == nil {
		return
	}
	energy := h.side.EnergyColor
	metal := h.side.MetalColor
	if energy < 0 || energy > 255 {
		energy = 0
	}
	if metal < 0 || metal > 255 {
		metal = 0
	}
	energyBar, _ := h.anchors.ByIndex(hud.AnchorEnergyBar)
	metalBar, _ := h.anchors.ByIndex(hud.AnchorMetalBar)
	h.drawResourceBar(c, energyBar, hud.ResourceFraction(res.Energy, res.EnergyCapacity), byte(energy))
	h.drawResourceBar(c, metalBar, hud.ResourceFraction(res.Metal, res.MetalCapacity), byte(metal))
	h.drawNumber(c, hud.AnchorEnergyNum, float32(res.Energy))
	h.drawNumber(c, hud.AnchorMetalNum, float32(res.Metal))
	h.drawNumberAt(c, hud.AnchorEnergyMax, res.EnergyCapacity)
	h.drawNumberAt(c, hud.AnchorMetalMax, res.MetalCapacity)
}

func (h *retailBattleHUD) drawResourceBar(c *client.Client, r hud.Rect, fraction float32, inner byte) {
	left, top, right, bottom := r.Ordered()
	if right <= left || bottom <= top {
		return
	}
	filled := int(float32(right-left) * fraction)
	if filled <= 0 {
		return
	}
	c.UIFillRect(int(left), int(top), filled, int(bottom-top), inner)
}

func (h *retailBattleHUD) drawNumber(c *client.Client, index int, value float32) {
	r, ok := h.anchors.ByIndex(index)
	if !ok {
		return
	}
	h.drawNumberAtPoint(c, r.X1, r.Y1, value)
}

func (h *retailBattleHUD) drawNumberAt(c *client.Client, index int, value float32) {
	r, ok := h.anchors.ByIndex(index)
	if !ok {
		return
	}
	h.drawNumberAtPoint(c, r.X1, r.Y1, value)
}

func (h *retailBattleHUD) drawNumberAtPoint(c *client.Client, x, y int32, value float32) {
	// The retail resource display is an integer text field; the authoritative
	// stock remains float32, and conversion here truncates toward zero [01 §8].
	c.UIText(h.console, fmt.Sprintf("%d", int(value)), int(x), int(y), h.guiColor(15))
}

func (h *retailBattleHUD) drawSelectedUnit(c *client.Client, f *snapshot.Frame) {
	var selected *snapshot.UnitView
	for i := range f.Units {
		u := &f.Units[i]
		if u.Owner == h.owner && u.Flags&client.SelectionFlag != 0 {
			selected = u
			break
		}
	}
	if selected == nil {
		return
	}
	def, ok := h.defFor(selected)
	if !ok || def == nil {
		return
	}
	name := def.Name
	if name == "" {
		name = def.UnitName
	}
	if r, ok := h.anchors.ByIndex(hud.AnchorUnitName); ok {
		c.UIText(h.console, name, int(r.X1), int(r.Y1), h.guiColor(15))
	}
	if r, ok := h.anchors.ByIndex(hud.AnchorDescription); ok && def.Description != "" {
		c.UITextWidth(h.console, def.Description, int(r.X1), int(r.Y1), int(r.X2-r.X1), h.guiColor(15))
	}
	if r, ok := h.anchors.ByIndex(hud.AnchorDamageBar); ok {
		h.drawHealthBar(c, r, selected.Health, selected.MaxHealth)
	}
}

func (h *retailBattleHUD) drawHealthBar(c *client.Client, r hud.Rect, health, max int32) {
	left, top, right, bottom := r.Ordered()
	if right <= left || bottom <= top || max <= 0 {
		return
	}
	outer := h.paletteIndex(0)
	inner := h.paletteIndex(12)
	third := max / 3
	if health > third*2 {
		inner = h.paletteIndex(10)
	} else if health > third {
		inner = h.paletteIndex(14)
	}
	c.UIFillRect(int(left), int(top), int(right-left), int(bottom-top), outer)
	width := int((int64(health) * int64(right-left)) / int64(max))
	if width > 0 {
		if width > int(right-left) {
			width = int(right - left)
		}
		c.UIFillRect(int(left), int(top), width, int(bottom-top), inner)
	}
}

func (h *retailBattleHUD) defFor(u *snapshot.UnitView) (*content.UnitDef, bool) {
	if h == nil || h.cat == nil || u == nil {
		return nil, false
	}
	return h.cat.UnitDefByIndex(uint32(u.DefID))
}

func (h *retailBattleHUD) drawSidePage(c *client.Client, b *battleSession, offset int, f *snapshot.Frame) {
	if b == nil || b.cat == nil {
		return
	}
	window, pageGAF := h.windowFor(b, f)
	if window == nil {
		return
	}
	for i, gad := range window.Gadgets {
		if i == 0 || gad.Active == 0 || gad.Kind == gui.KindFont || gad.Kind == gui.KindPanel {
			continue
		}
		r := window.PlacedRect(i)
		r.Y += int32(offset)
		pressed := false
		if c.Input() != nil && c.Input().Mouse != nil && c.Input().Mouse.Held(input.MouseButtonLeft) {
			pressed = guiRectContains(r, int32(c.Input().Mouse.X), int32(c.Input().Mouse.Y))
		}
		frame := h.gadgetFrame(gad, pageGAF, pressed)
		if frame != nil {
			// .GUI controls use the authored rectangle origin; unlike the PANEL
			// shell, their GAF offsets are not applied [07 §4].
			c.UIBlit(frame, int(r.X), int(r.Y))
		}
		if gad.Kind == gui.KindButton && gad.Text != "" {
			text := gad.Text
			if len(gad.Labels) != 0 {
				text = gad.Labels[0]
			}
			if text != "" {
				c.UITextWidth(h.guiFont, text, int(r.X)+3, int(r.Y)+(int(r.H)-int(h.guiFont.Height))/2, int(r.W), h.guiColor(byte(gad.ColorF)))
			}
		}
	}
}

// consumeClick applies the retail order-button latch parser to a visible
// authored side-panel button. Other GUI controls are still consumed here even
// though their callbacks are not yet in this slice; a click on the rail must
// never leak through to world selection [07 §3][07 §9].
func (h *retailBattleHUD) consumeClick(b *battleSession, x, y int32) bool {
	if h == nil || b == nil || b.sess == nil {
		return false
	}
	_, f, ok := b.sess.Snapshot.Read()
	if !ok {
		return false
	}
	window, _ := h.windowFor(b, f)
	if window == nil {
		return false
	}
	offset := int32(0)
	if h.panel != nil {
		offset = int32(h.panel.Offset)
	}
	for i, gad := range window.Gadgets {
		if i == 0 || gad.Active == 0 || gad.Kind != gui.KindButton {
			continue
		}
		r := window.PlacedRect(i)
		r.Y += offset
		if !guiRectContains(r, x, y) {
			continue
		}
		upper := strings.ToUpper(gad.Name)
		if strings.Contains(upper, "MOVE") ||
			strings.Contains(upper, "ATTACK") || strings.Contains(upper, "BLAST") ||
			strings.Contains(upper, "DEFEND") || strings.Contains(upper, "REPAIR") ||
			strings.Contains(upper, "PATROL") || strings.Contains(upper, "RECLAIM") ||
			strings.Contains(upper, "CAPTURE") || strings.Contains(upper, "LOAD") ||
			strings.Contains(upper, "UNLOAD") || strings.Contains(upper, "STOP") {
			b.latch = hud.ParseButtonLatch(gad.Name, 1)
		}
		return true
	}
	return false
}

func (h *retailBattleHUD) windowFor(b *battleSession, f *snapshot.Frame) (*gui.Window, *formats.GAF) {
	name := strings.ToLower(h.side.NamePrefix) + "main"
	var selected *snapshot.UnitView
	if f != nil {
		for i := range f.Units {
			u := &f.Units[i]
			if u.Owner == h.owner && u.Flags&client.SelectionFlag != 0 {
				selected = u
				break
			}
		}
	}
	if selected != nil {
		if def, ok := b.cat.UnitDefByIndex(uint32(selected.DefID)); ok && def != nil {
			if def.Builder {
				name = strings.ToLower(def.UnitName) + "1"
			} else {
				name = strings.ToLower(h.side.NamePrefix) + "gen"
			}
		}
	}
	window, err := gui.Load(h.fs, "guis/"+name+".gui")
	if err != nil {
		return nil, nil
	}
	var gaf *formats.GAF
	if name != strings.ToLower(h.side.NamePrefix)+"main" {
		if cached, ok := h.pages[name]; ok {
			gaf = cached
		} else if loaded, loadErr := formats.LoadGAFFile(h.fs, "anims/"+name+".gaf"); loadErr == nil {
			h.pages[name] = loaded
			gaf = loaded
		}
	}
	return window, gaf
}

func (h *retailBattleHUD) gadgetFrame(gad gui.Gadget, page *formats.GAF, pressed bool) *formats.GAFFrame {
	name := gad.Art
	if name == "" {
		name = gad.Name
	}
	var entry *formats.GAFEntry
	stockButtons := false
	for _, g := range []*formats.GAF{page, h.oldMain, h.share, h.common} {
		if g == nil {
			continue
		}
		if found, ok := g.Find(name); ok {
			entry = found
			break
		}
	}
	if entry == nil && gad.Kind == gui.KindButton && h.common != nil {
		entry, _ = h.common.Find("BUTTONS0")
		stockButtons = entry != nil
	}
	if entry == nil || len(entry.Frames) == 0 {
		return nil
	}
	idx := 0
	if stockButtons {
		// BUTTONS0 is four frames per authored stock size. Pick the group
		// whose normal frame matches the .GUI rectangle, then apply the
		// retail normal/pressed stage within that group.
		best, bestScore := -1, int(^uint(0)>>1)
		for i, ref := range entry.Frames {
			if ref.Frame == nil {
				continue
			}
			score := absInt(int(ref.Frame.Width)-int(gad.Rect.W)) + absInt(int(ref.Frame.Height)-int(gad.Rect.H))
			if score < bestScore {
				best, bestScore = i, score
			}
		}
		if best < 0 {
			return nil
		}
		idx = (best / 4) * 4
		if pressed {
			idx++
		}
	} else if pressed && len(entry.Frames) > 1 {
		idx = 1
	}
	if idx >= len(entry.Frames) {
		idx = len(entry.Frames) - 1
	}
	return entry.Frames[idx].Frame
}

func (h *retailBattleHUD) guiColor(source byte) byte {
	if h != nil && h.pal != nil {
		return h.pal.GUIColor(source)
	}
	return source
}

func (h *retailBattleHUD) paletteIndex(logical byte) byte {
	if h != nil && h.pal != nil {
		return h.pal.Logical[logical]
	}
	return logical
}

func guiRectContains(r gui.Rect, x, y int32) bool {
	left, top, right, bottom := r.X, r.Y, r.X+r.W, r.Y+r.H
	return x >= left && x < right && y >= top && y < bottom
}
