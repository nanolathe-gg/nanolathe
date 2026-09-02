package session

import (
	"fmt"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// The unit painter's pass selector is the committed low two bits of the mover
// mode word: mode 2 is airborne and belongs to pass B, which paints after the
// projectile and effect strips [03 R-RAST-01 §7][04 R-MOV-01 §8]. `bmcode`
// non-zero is the mobile class, the one the allocator gives a mover
// [04 R-FAC-02 §5].
func TestPublishSnapshotCarriesCommittedMoverMode(t *testing.T) {
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "flyer"}, MaxDamage: 1, BMCode: true}
	w := newSessionFixtureWorld(2, nil)
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create unit: %v", err)
	}
	w.Unit(h).Move.Mode = 2
	w.Unit(h).Group = 3
	s := &Session{Snapshot: frame.NewBuffer(), Units: w, LocalOwner: 0}
	s.publishSnapshot(1)
	cur := s.Snapshot.Current()
	if cur == nil || len(cur.Units) != 1 {
		t.Fatalf("published frame = %#v, want one unit", cur)
	}
	if cur.Units[0].MoverMode != 2 {
		t.Fatalf("published mover mode = %d, want 2 (airborne)", cur.Units[0].MoverMode)
	}
	// The health-bar pass draws '0'+Group beside a grouped unit's bar
	// [03 R-FX-01 §6].
	if cur.Units[0].Group != 3 {
		t.Fatalf("published group = %d, want 3", cur.Units[0].Group)
	}
}

// A structure and a still-unfinished nanoframe both publish the mirror value
// `1`, which is pass A — the pass that runs before the nanolathe strip, so the
// construction spray paints over the unit being built. Retail's spawn writes
// `1` into the flags word's low two bits for every unit, a building included,
// and nothing rewrites it for a unit that owns no mover
// [03 R-RAST-01 §7, correction of 2026-08-30][04 R-MOV-01 §8].
//
// This test previously asserted 0 and was named ...GivesStructuresNoMoverMode,
// on the retracted reading that the composer reads the mover object rather than
// the mirror. That published every building into pass B, behind its own
// construction spray — playtest defect PT3-01.
func TestPublishSnapshotGivesStructuresTheGroundedMirror(t *testing.T) {
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "lab"}, MaxDamage: 1}
	w := newSessionFixtureWorld(2, nil)
	if _, err := w.Create(def, 0, 0, 0, 0); err != nil {
		t.Fatalf("create unit: %v", err)
	}
	s := &Session{Snapshot: frame.NewBuffer(), Units: w, LocalOwner: 0}
	s.publishSnapshot(1)
	cur := s.Snapshot.Current()
	if cur == nil || len(cur.Units) != 1 {
		t.Fatalf("published frame = %#v, want one unit", cur)
	}
	if cur.Units[0].MoverMode != 1 {
		t.Fatalf("published structure mover mode = %d, want 1", cur.Units[0].MoverMode)
	}
}

// The nanoframe half of the same contract, and the hole the creation-time seed
// closes: a unit the movement system has not reached yet — a building
// nanoframe on the tick it is laid — must already carry the mirror, because
// the composer sorts it into a pass on that very frame. Nothing here calls
// into movement.
func TestPublishSnapshotGivesANanoframeTheGroundedMirror(t *testing.T) {
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "lab"}, MaxDamage: 1}
	w := newSessionFixtureWorld(2, nil)
	h, err := w.CreateNanoframe(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create nanoframe: %v", err)
	}
	if got := w.Unit(h).Move.Mode; got != 1 {
		t.Fatalf("nanoframe mover mode at creation = %d, want 1", got)
	}
	s := &Session{Snapshot: frame.NewBuffer(), Units: w, LocalOwner: 0}
	s.publishSnapshot(1)
	cur := s.Snapshot.Current()
	if cur == nil || len(cur.Units) != 1 {
		t.Fatalf("published frame = %#v, want one unit", cur)
	}
	if cur.Units[0].MoverMode != 1 {
		t.Fatalf("published nanoframe mover mode = %d, want 1", cur.Units[0].MoverMode)
	}
}

// A frame published with no resolved builder must advertise no product keys:
// the reset at the publication boundary keeps the slice's capacity but not its
// length, so nothing from the previous frame survives into the committed one
// [I6].
func TestPublishBoundaryClearsCommandPageProductKeys(t *testing.T) {
	b := frame.NewBuffer()
	first := b.BeginWrite()
	first.CommandPage.Builder = 7
	first.CommandPage.ProductKeys = append(first.CommandPage.ProductKeys, "armsolar", "armmex")
	if err := b.Publish(1); err != nil {
		t.Fatalf("publish first: %v", err)
	}
	// Two BeginWrite calls: the buffer alternates slots, so the second one
	// returns the slot the first frame wrote.
	b.BeginWrite()
	if err := b.Publish(2); err != nil {
		t.Fatalf("publish second: %v", err)
	}
	third := b.BeginWrite()
	if len(third.CommandPage.ProductKeys) != 0 {
		t.Fatalf("reset product keys = %q, want none", third.CommandPage.ProductKeys)
	}
	if third.CommandPage.Builder != 0 {
		t.Fatalf("reset builder = %d, want 0", third.CommandPage.Builder)
	}
}

// The on/off aggregate starts at the not-applicable sentinel 3, takes the first
// onoffable unit's committed activation, and moves to the disagreement value
// when a later onoffable unit differs [07 §9][07 R-HUD-03 §6].
func TestPublishSnapshotFoldsDisagreeingOnOffSelection(t *testing.T) {
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "switchable"},
		MaxDamage:        1,
		OnOffable:        true,
	}
	w := newSessionFixtureWorld(4, nil)
	first, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create first unit: %v", err)
	}
	second, err := w.Create(def, 0, numeric.Fixed(2<<16), 0, 0)
	if err != nil {
		t.Fatalf("create second unit: %v", err)
	}
	// Selection membership is bit 0x10 of the unit status word [07 §9].
	w.Unit(first).Flags |= 0x10
	w.Unit(second).Flags |= 0x10
	w.Unit(first).Activated = true

	s := &Session{Snapshot: frame.NewBuffer(), Units: w, LocalOwner: 0}
	s.publishSnapshot(1)
	cur := s.Snapshot.Current()
	if cur == nil || cur.Selection.Count != 2 {
		t.Fatalf("published selection = %#v, want two selected units", cur)
	}
	if cur.CommandPage.OnOffState != 2 {
		t.Fatalf("disagreeing on/off aggregate = %d, want 2", cur.CommandPage.OnOffState)
	}

	// Agreement folds to the shared state, not to the disagreement value.
	w.Unit(second).Activated = true
	s.publishSnapshot(2)
	if got := s.Snapshot.Current().CommandPage.OnOffState; got != 1 {
		t.Fatalf("agreeing on/off aggregate = %d, want 1", got)
	}

	// No selected unit is onoffable: the sentinel that greys ONOFF survives.
	def.OnOffable = false
	s.publishSnapshot(3)
	if got := s.Snapshot.Current().CommandPage.OnOffState; got != 3 {
		t.Fatalf("not-applicable on/off aggregate = %d, want 3", got)
	}
}

// The published page model [07 R-HUD-03 §6]: page 0 is the orders state and
// carries no products, the page-count byte is the definition's compiled
// page-count byte — one more than its last authored page window
// [02 R-CAT-01 §5 step 5] — and page N carries the authored entries
// (N-1)*6..N*6-1. A commander with four authored page windows has a count of
// five; a factory with one authored page still has a count of two, so BUILD has
// somewhere to go.
func TestPublishedCommandPageCountsTheOrdersPage(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}, BuildMenus: map[string]*content.BuildMenuPage{}}
	newBuilder := func(key string, products, pages int) []string {
		def := &content.UnitDef{UnitName: key, Builder: true, MaxDamage: 100, BuildPageCount: int32(pages) + 1}
		def.CanonicalKey = key
		cat.Units[key] = def
		buttons := make([]string, products)
		for i := range buttons {
			buttons[i] = fmt.Sprintf("%s-p%d", key, i+1)
		}
		cat.BuildMenus[key] = &content.BuildMenuPage{Buttons: buttons, BaseButtonCount: len(buttons)}
		return buttons
	}
	commanderProducts := newBuilder("commander", 19, 4)
	factoryProducts := newBuilder("factory", 4, 1)

	publish := func(key string, page int) *frame.CommandPageView {
		w := newSessionFixtureWorld(8, cat)
		h, err := w.Create(cat.Units[key], 0, 0, 0, 0)
		if err != nil {
			t.Fatalf("create %s: %v", key, err)
		}
		w.Unit(h).Flags = 0x10 | hud.EncodePageBits(0, page)
		s := &Session{Units: w, Catalog: cat, LocalOwner: 0, Snapshot: frame.NewBuffer()}
		s.publishSnapshot(1)
		cur := s.Snapshot.Current()
		if cur == nil {
			t.Fatalf("%s published no frame", key)
		}
		return &cur.CommandPage
	}
	sameKeys := func(t *testing.T, got, want []string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("page products = %v, want %v", got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("page products = %v, want %v", got, want)
			}
		}
	}

	orders := publish("commander", 0)
	if orders.PageCount != 5 {
		t.Fatalf("commander page count = %d, want 4 authored pages plus the orders page", orders.PageCount)
	}
	if orders.Page != 0 || len(orders.ProductKeys) != 0 {
		t.Fatalf("commander orders page = %d with products %v, want page 0 and none", orders.Page, orders.ProductKeys)
	}
	first := publish("commander", 1)
	if first.Page != 1 {
		t.Fatalf("commander build page = %d, want 1", first.Page)
	}
	sameKeys(t, first.ProductKeys, commanderProducts[0:6])
	last := publish("commander", 4)
	sameKeys(t, last.ProductKeys, commanderProducts[18:19])

	plant := publish("factory", 0)
	if plant.PageCount != 2 {
		t.Fatalf("factory page count = %d, want its one authored page plus the orders page", plant.PageCount)
	}
	if len(plant.ProductKeys) != 0 {
		t.Fatalf("factory orders page carried %v, want no products", plant.ProductKeys)
	}
	sameKeys(t, publish("factory", 1).ProductKeys, factoryProducts)
}

func TestPublishedCommandPageUnionsBasePageWithExplicitDownloads(t *testing.T) {
	base := []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7"}
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "lab"},
		UnitName:         "lab",
		Builder:          true,
		MaxDamage:        100,
		BuildPageCount:   3,
	}
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{"lab": def},
		BuildMenus: map[string]*content.BuildMenuPage{"lab": {
			Buttons:         append(append([]string(nil), base...), "download-a", "download-b", "P7"),
			BaseButtonCount: len(base),
		}},
		DownloadPlacements: []content.DownloadMenuPlacement{
			{Builder: "lab", Product: "download-a", Menu: 3, Button: 4, BuilderResolved: true, ProductResolved: true},
			{Builder: "lab", Product: "download-b", Menu: 3, Button: 1, BuilderResolved: true, ProductResolved: true},
			{Builder: "lab", Product: "P7", Menu: 3, Button: 5, BuilderResolved: true, ProductResolved: true},
		},
	}
	w := newSessionFixtureWorld(3, cat)
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	w.Unit(h).Flags = 0x10 | hud.EncodePageBits(0, 2)
	s := &Session{Units: w, Catalog: cat, LocalOwner: 0, Snapshot: frame.NewBuffer()}
	s.publishSnapshot(1)
	page := s.Snapshot.Current().CommandPage
	wantKeys := []string{"p7", "download-a", "download-b"}
	if fmt.Sprint(page.ProductKeys) != fmt.Sprint(wantKeys) {
		t.Fatalf("page products = %v, want base-page union %v", page.ProductKeys, wantKeys)
	}
	wantSlots := []frame.GeneratedProductPlacement{
		{ProductKey: "download-a", Button: 4},
		{ProductKey: "download-b", Button: 1},
		{ProductKey: "P7", Button: 5},
	}
	if fmt.Sprint(page.GeneratedProducts) != fmt.Sprint(wantSlots) {
		t.Fatalf("generated placements = %v, want authored order %v", page.GeneratedProducts, wantSlots)
	}
	// Publication owns both slices; changing catalog storage after commit must
	// not mutate the committed frame [I6].
	cat.DownloadPlacements[0].Product = "changed"
	cat.BuildMenus["lab"].Buttons[6] = "changed"
	if fmt.Sprint(page.ProductKeys) != fmt.Sprint(wantKeys) || fmt.Sprint(page.GeneratedProducts) != fmt.Sprint(wantSlots) {
		t.Fatalf("committed page aliases catalog storage: %+v", page)
	}
}
