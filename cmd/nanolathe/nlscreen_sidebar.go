package main

import (
	"fmt"
	"image"
	"strings"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func (s *nlScreen) sidebarCard() nlCard {
	card := s.groupCard("sidebar", "Sidebar", []string{"armlab", "armvp"}, "armor", "",
		nlPart{key: "build", label: "Build items", sub: "Reserve build space first", choices: true,
			steps: []string{"6 per page", "12 per page", "Free flow"}, get: s.nlSidebarCountChoice,
			set: func(d *nlDraft, v int) {
				d.pres.ExpandedSidebar = 1
				d.pres.BuildMenuPageSize = [...]int{6, 12, 0}[v]
			}},
		nlPart{key: "orders", label: "Orders below build", sub: "Always available on the Orders page", choices: true,
			steps: []string{"When space permits", "Never"},
			get:   func(d *nlDraft) int { return onOff(d.pres.SidebarOrders == 0) },
			set:   func(d *nlDraft, v int) { d.pres.SidebarOrders = onOff(v == 0) }},
		nlPart{key: "scale", label: "UI scale", sub: "Sidebar, minimap and bars; Auto: 2x from 1440 rows", choices: true,
			steps: chromeScaleSteps(),
			get:   func(d *nlDraft) int { return d.pres.UIScale },
			set:   func(d *nlDraft, v int) { d.pres.UIScale = v }})
	card.demo, card.compare = "sidebar", nil
	card.desc = func(d *nlDraft, _ int) string {
		v := s.nlSidebarCountChoice(d)
		text := [...]string{
			"Six build items per page, combining authored pages as needed.",
			"Twelve build items per page, combining authored pages as needed. Orders-page controls hide first; if twelve still cannot fit, the page uses the fitting count.",
			"Free flow reserves at least six build slots, then fills every remaining row. This choice overrides a mod's recommended count."}[v]
		limit := s.nlSidebarBuildLimit(d)
		if limit > 0 && limit != 6 && limit != 12 {
			text = fmt.Sprintf("Current count: %d per page. This stored or recommended count stays until you choose another option.", limit)
		} else if d.pres.BuildMenuPageSize < 0 && limit > 0 {
			text = fmt.Sprintf("The content recommends %d per page. %s", limit, text)
		}
		if d.pres.SidebarOrders != 0 {
			return text + " Common command buttons stay visible. The Orders-page controls appear above the common command buttons when they fit."
		}
		return text + " Common command buttons stay visible. Orders-page controls stay on their own page."
	}
	return card
}

// Art is decoded on the picture worker through the battle's resource and
// button-frame resolvers. Draw only uploads these immutable pixels (HUD §3.17).
type nlSidebarPreview struct {
	*sidebarProductCatalog
	backdrop *image.RGBA
	controls map[sidebarGadgetSource]*image.RGBA
}

// Resolve the demonstration on the picture worker, through the HUD's ordinary
// GUI/download/art path. CANBUILD is not a source of visible cells or page
// membership [07 R-HUD-03 §6] (HUD design §3.3 and §3.17).
func loadNLSidebar(fs vfs.FSOps, cat *content.Catalog, builder *content.UnitDef, side *content.SideDef, halted func() bool) *nlSidebarPreview {
	if fs == nil || builder == nil || side == nil || builder.BuildPageCount < 2 {
		return nil
	}
	fs = nlSidebarFiles{FSOps: fs, halted: halted}
	ctx := newBattleWindowContext(&contentSet{fs: fs}, nil)
	ctx.completeTransition()
	h := &retailBattleHUD{fs: fs, cat: cat, side: side, windowContext: ctx}
	h.common, _ = formats.LoadGAFFile(fs, "anims/commongui.gaf")
	h.intGAF, _ = formats.LoadGAFFile(fs, "anims/"+strings.ToLower(side.IntGAF)+".gaf")
	h.oldMain, _ = formats.LoadGAFFile(fs, "anims/oldmain.gaf")
	h.share, _ = formats.LoadGAFFile(fs, "anims/share.gaf")
	c := h.sidebarProductCatalog(cat, builder, int(builder.BuildPageCount))
	if !c.safe || len(c.cells) == 0 {
		return nil
	}
	h.pal, _ = palette.Load(fs)
	preview := &nlSidebarPreview{sidebarProductCatalog: c, controls: make(map[sidebarGadgetSource]*image.RGBA)}
	panel, _ := battleFrame(h.intGAF, "PANELSIDE")
	preview.backdrop = nlGAFImage(panel, h.pal)
	for _, source := range append([]*sidebarCommandScaffold{c.orders}, c.pages...) {
		if source == nil {
			continue
		}
		for _, i := range source.indices {
			g := source.window.Gadgets[i]
			art := h.gadgetButtonFrame(g, source.art, int(g.Status), 0, g.GrayedOut&1 != 0)
			if commandButtonName(g.Name) != "" {
				// This demonstration shows a build page; retain its actual tab
				// stage and the native unpressed art for the other commands.
				stage := boolStage(commandButtonName(g.Name) == "BUILD")
				art = commandButtonFrame(h.gadgetArtEntry(g, source.art), g, stage, g.GrayedOut&1 != 0, false)
			}
			preview.controls[sidebarGadgetSource{source.window, source.art, i}] = nlGAFImage(art, h.pal)
		}
	}
	if halted != nil && halted() {
		return nil
	}
	return preview
}

func (a *nlArt) sidebarImage(pixels *image.RGBA) *ebiten.Image {
	if pixels == nil {
		return nil
	}
	if a.sidebarImages == nil {
		a.sidebarImages = make(map[*image.RGBA]*ebiten.Image)
	}
	if img := a.sidebarImages[pixels]; img != nil {
		return img
	}
	img := ebiten.NewImageFromImage(pixels)
	a.sidebarImages[pixels] = img
	return img
}

// The picture worker stops between asset reads, including the HUD's nested
// GUI/art lookups. Cancellation discards this private catalog; it must never
// cache cancellation as a missing asset on a battle's actual HUD.
type nlSidebarFiles struct {
	vfs.FSOps
	halted func() bool
}

func (f nlSidebarFiles) ReadFileLimit(name string, limit int64) ([]byte, error) {
	if f.halted != nil && f.halted() {
		return nil, vfs.ErrNotFound
	}
	return f.FSOps.ReadFileLimit(name, limit)
}

func (f nlSidebarFiles) Stat(name string) (vfs.EntryInfo, error) {
	if f.halted != nil && f.halted() {
		return vfs.EntryInfo{}, vfs.ErrNotFound
	}
	return f.FSOps.Stat(name)
}

// Resolve the draft content's recommendation only while the player inherits.
// Explicit Free flow is zero and wins over every recommendation (HUD §3.3).
func (s *nlScreen) nlSidebarBuildLimit(d *nlDraft) int {
	if d.pres.BuildMenuPageSize >= 0 {
		return d.pres.BuildMenuPageSize
	}
	m := s.modAt(d.mod)
	if m != nil && m.BuildMenuPageSize > 0 {
		return m.BuildMenuPageSize
	}
	if g := s.shell(); g != nil && g.cs != nil && sameMod(m, g.cs.mod) {
		return max(0, g.cs.buildMenuPageSize())
	}
	return 0
}

func (s *nlScreen) nlSidebarCountChoice(d *nlDraft) int {
	limit := s.nlSidebarBuildLimit(d)
	if limit == 0 {
		return 2
	}
	// Keep unusual saved counts until a choice changes them. The description
	// and demonstration name the exact count; the selector uses the closest
	// supported fixed choice rather than changing the saved preference.
	if abs(limit-6) <= abs(limit-12) {
		return 0
	}
	return 1
}

// Original keeps source pages. Every adaptive choice partitions the flattened
// resolved cells, using the exact battle layout for capacity and orders (HUD
// design §3.3 and §3.17).
func nlSidebarPageStarts(c *sidebarProductCatalog, original bool, height, limit int, orders bool) ([]int, sidebarPageLayout) {
	p := sidebarPageLayout{capacity: len(c.cells)}
	if !original {
		p = c.sidebarLayout(height, limit, orders)
		if p.capacity > 0 {
			return sidebarPageStarts(c.cells, p.capacity, false), p
		}
	}
	if original || p.capacity == 0 {
		for _, source := range c.pages {
			if source != nil && source.window.Rect.Y+source.window.Rect.H > retailScreenH {
				// The fitted path paginates overlapping authored row groups,
				// not normalized cells. Describe that layout instead of giving
				// a false build-page count for the selected game resolution.
				return nil, p
			}
		}
	}
	// A scaffold the adaptive helper cannot fit uses the authored path.
	return sidebarPageStarts(c.cells, len(c.cells), true), p
}

// These are drawing placements only. Capacity and the retained command set
// always come from sidebarLayout, shared with the battle (HUD §3.3/§3.17).
func nlSidebarPreviewItems(c *sidebarProductCatalog, start, end, height int, p sidebarPageLayout, original bool) (products, controls []sidebarProduct) {
	if original {
		for _, cell := range c.cells[start:end] {
			for _, product := range cell.products {
				product.rect = product.source.window.PlacedRect(product.source.index)
				products = append(products, product)
			}
		}
		if start < end {
			if source := c.pages[c.cells[start].page]; source != nil {
				for _, i := range source.indices {
					controls = append(controls, sidebarProduct{source: sidebarGadgetSource{source.window, source.art, i}, rect: source.window.PlacedRect(i)})
				}
			}
		}
		return products, controls
	}
	gridGap := int32(0)
	for _, gap := range p.spacing {
		if gap.at < 0 {
			gridGap += gap.pixels
		}
	}
	for _, tab := range c.tabs {
		tab.rect.Y += 128
		controls = append(controls, tab)
	}
	for _, item := range p.commands {
		r := item.rect
		for _, gap := range p.spacing {
			if gap.at >= 0 && gap.at <= item.rect.Y {
				r.Y += gap.pixels
			}
		}
		r.Y += p.commandTop
		controls = append(controls, sidebarProduct{source: item.source, rect: r})
	}
	for n, cell := range c.cells[start:end] {
		origin := gui.Rect{X: int32(n%2) * 64, Y: 128 + c.upperHeight + gridGap + int32(n/2)*64}
		for _, product := range cell.products {
			product.rect.X += origin.X
			product.rect.Y += origin.Y
			products = append(products, product)
		}
	}
	return products, controls
}
