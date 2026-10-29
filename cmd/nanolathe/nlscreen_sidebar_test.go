package main

import (
	"slices"
	"sync/atomic"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// The demonstration uses the battle's approved host layout, including its
// build-first capacity and orders decision (HUD design §3.3 and §3.17).
func TestNLSidebarPreviewPagePolicies(t *testing.T) {
	b, _, sources := sidebarRowsFixture(t, 1080)
	sources[1].Gadgets[9].Active = 0
	def, _ := b.cat.Unit("armfav")
	f, _ := b.currentSnapshot()
	c := b.hud.sidebarProductCatalog(b.cat, def, int(f.CommandPage.PageCount))
	if c == nil || !c.safe || len(c.cells) == 0 {
		t.Fatal("no resolved fixture pages")
	}
	original, _ := nlSidebarPageStarts(c, true, 1080, 12, false)
	if !slices.Equal(original, []int{0, 6, 11, 17}) {
		t.Fatalf("Original changed authored pages: %v", original)
	}
	for _, height := range []int{480, 800, 1080} {
		for _, limit := range []int{0, 6, 12} {
			for _, orders := range []bool{false, true} {
				starts, got := nlSidebarPageStarts(c, false, height, limit, orders)
				want := c.sidebarLayout(height, limit, orders)
				if got.capacity != want.capacity || got.inlineOrders != want.inlineOrders || !slices.Equal(got.commands, want.commands) || !slices.Equal(got.spacing, want.spacing) || got.lowerHeight != want.lowerHeight || got.commandTop != want.commandTop {
					t.Fatalf("preview diverged from battle at %d/%d/%v: %+v vs %+v", height, limit, orders, got, want)
				}
				if !slices.Equal(starts, sidebarPageStarts(c.cells, want.capacity, false)) {
					t.Fatalf("adaptive pages retained authored breaks at %d/%d/%v: %v", height, limit, orders, starts)
				}
			}
		}
	}
	locked, p := nlSidebarPageStarts(c, false, 1080, 12, true)
	if p.capacity != 12 || !slices.Equal(locked, []int{0, 12}) {
		t.Fatalf("twelve cells did not span the short source page: %v, capacity %d", locked, p.capacity)
	}
	sources[0].Rect.H = 640
	if pages, _ := nlSidebarPageStarts(c, true, 480, 12, true); len(pages) != 0 {
		t.Fatalf("fitted authored pages must not claim normalized page counts: %v", pages)
	}
	if pages, _ := nlSidebarPageStarts(c, false, 1080, 12, true); !slices.Equal(pages, locked) {
		t.Fatalf("oversized source suppressed a supported adaptive preview: %v", pages)
	}
	// Preserve GUI names, duplicate entries and download cells even if the
	// catalog's membership table disagrees with the actual product controls.
	b.cat.BuildMenus = map[string]*content.BuildMenuPage{"armfav": {Buttons: []string{"wrong"}}}
	names := (&nlPicNames{sidebar: &nlSidebarPreview{sidebarProductCatalog: c}}).order(nil)
	if slices.Contains(names, "wrong") || !slices.Contains(names, "product0") || !slices.Contains(names, "product17") {
		t.Fatalf("preview pictures came from membership: %v", names)
	}
}

// The demonstration lays the rail out on the rows the battle gives it at the
// draft's resolution, UI scale and renderer, so above 1080 rows it shows the
// magnified rail's pages rather than the whole screen height's (issue #181;
// DESIGN_INTERFACE_HUD_INPUT "Modern UI scale").
func TestNLSidebarPreviewUsesTheBattleUIScale(t *testing.T) {
	cases := []struct {
		w, h, pref int
		renderer   string
		rows, k    int
	}{
		{1920, 1080, settings.ChromeScaleAuto, "modern", 1080, 1},
		{2560, 1440, settings.ChromeScaleAuto, "modern", 720, 2},
		{3840, 2160, settings.ChromeScaleAuto, "modern", 1080, 2},
		{1280, 1440, settings.ChromeScaleAuto, "modern", 1440, 1},
		{1920, 1080, 2, "modern", 540, 2},
		{3840, 2160, 1, "modern", 2160, 1},
		{2560, 1440, settings.ChromeScaleAuto, "classic", 1440, 1},
	}
	for _, c := range cases {
		d := nlDraft{pres: settings.DefaultPresentation(), resolution: retailDisplayMode{W: c.w, H: c.h}}
		d.pres.UIScale, d.pres.Renderer = c.pref, c.renderer
		rows, k := nlDraftSidebarRows(&d)
		if rows != c.rows || k != c.k {
			t.Errorf("%dx%d scale %d %s: preview rail %d rows at %dx, want %d at %dx", c.w, c.h, c.pref, c.renderer, rows, k, c.rows, c.k)
		}
	}
	b, _, _ := sidebarRowsFixture(t, 720)
	def, _ := b.cat.Unit("armfav")
	f, _ := b.currentSnapshot()
	c := b.hud.sidebarProductCatalog(b.cat, def, int(f.CommandPage.PageCount))
	d := nlDraft{pres: settings.DefaultPresentation(), resolution: retailDisplayMode{W: 2560, H: 1440}}
	d.pres.Renderer = "modern"
	rows, _ := nlDraftSidebarRows(&d)
	if _, got := nlSidebarPageStarts(c, false, rows, 0, false); got.capacity != c.sidebarLayout(720, 0, false).capacity {
		t.Fatalf("2560x1440 preview holds %d build slots, the battle's 2x rail %d", got.capacity, c.sidebarLayout(720, 0, false).capacity)
	}
}

func TestNLSidebarPreviewRetainsVisibleCommandsAndSourceIdentity(t *testing.T) {
	b, _, sources := sidebarRowsFixture(t, 1080)
	sources[3].Gadgets[9].Active = 0
	def, _ := b.cat.Unit("armfav")
	f, _ := b.currentSnapshot()
	c := b.hud.sidebarProductCatalog(b.cat, def, int(f.CommandPage.PageCount))
	for _, orders := range []bool{false, true} {
		starts, p := nlSidebarPageStarts(c, false, 1080, 12, orders)
		products, controls := nlSidebarPreviewItems(c, 0, starts[1], 1080, p, false)
		lastProducts, lastControls := nlSidebarPreviewItems(c, starts[1], len(c.cells), 1080, p, false)
		if len(products) != 12 || len(lastProducts) != 11 || !slices.Equal(controls, lastControls) {
			t.Fatalf("short final page moved controls: products %d/%d, controls %v/%v", len(products), len(lastProducts), controls, lastControls)
		}
		if len(controls) != len(c.tabs)+len(p.commands) || p.inlineOrders != orders {
			t.Fatalf("preview omitted the retained command panel: %d, inline %v", len(controls), p.inlineOrders)
		}
		for i, item := range controls[len(c.tabs):] {
			if item.source != p.commands[i].source || item.rect.Y < products[len(products)-1].rect.Y+products[len(products)-1].rect.H {
				t.Fatalf("command lost source or overlaps build rows: %+v", item)
			}
		}
		for i, item := range products {
			if item.source != c.cells[i].products[0].source {
				t.Fatal("preview replaced a resolved product identity")
			}
		}
	}
	starts, p := nlSidebarPageStarts(c, true, 1080, 0, false)
	products, controls := nlSidebarPreviewItems(c, starts[0], starts[1], 1080, p, true)
	for _, product := range products {
		if product.rect != product.source.window.PlacedRect(product.source.index) {
			t.Fatal("Original normalized an authored product")
		}
	}
	if len(controls) != len(c.pages[1].indices) {
		t.Fatal("Original did not show its source controls")
	}
}

func TestNLSidebarLoaderStopsBetweenAssetReads(t *testing.T) {
	cat := nlRenamedCatalog()
	builder, _ := cat.Unit("corehumanbuilder")
	builder.BuildPageCount = 2
	fs := &nlPicFS{t: t, files: map[string][]byte{}}
	fs.hold.Lock()
	var halted atomic.Bool
	done := make(chan *nlSidebarPreview, 1)
	go func() { done <- loadNLSidebar(fs, cat, builder, cat.Sides[0], halted.Load) }()
	nlWaitFor(t, "the first sidebar asset read", func() bool { return fs.readCount() == 1 })
	halted.Store(true)
	fs.hold.Unlock()
	if c := <-done; c != nil {
		t.Fatal("a cancelled source resolution published a catalog")
	}
	if reads := fs.readCount(); reads != 1 {
		t.Fatalf("cancellation opened more assets: %d reads", reads)
	}
}
