package main

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
)

func sidebarRowsFixture(t *testing.T, height int) (*battleSession, *client.Client, []*gui.Window) {
	t.Helper()
	b, cl, sources := expandedSidebarFixture(t)
	pages := append([]*gui.Window(nil), sources[:2]...)
	for page := 2; page < 4; page++ {
		w := cloneGUIWindow(sources[0])
		for slot := 0; slot < 6; slot++ {
			name := fmt.Sprintf("product%d", page*6+slot)
			g := &w.Gadgets[4+slot]
			g.Name = name
			product := *b.cat.Units["product0"]
			product.UnitName, product.CanonicalKey = name, name
			b.cat.Units[name] = &product
			b.cat.BuildMenus["armfav"].Buttons = append(b.cat.BuildMenus["armfav"].Buttons, name)
		}
		b.hud.windows[fmt.Sprintf("armfav%d", page+1)] = w
		pages = append(pages, w)
	}
	paletteCallbackFrame(t, b, func(f *frame.Frame) { f.CommandPage.PageCount = 5 })
	cl.Resize(1280, height)
	return b, cl, append(pages, sources[2])
}

func sidebarVisibleProducts(w *gui.Window) []string {
	var products []string
	for _, g := range w.Gadgets {
		if g.CommonAttribs&4 != 0 {
			products = append(products, g.Name)
		}
	}
	return products
}
