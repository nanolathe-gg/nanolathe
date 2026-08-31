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
// projectile and effect strips [03 R-RAST-01 §7][04 R-MOV-01 §8].
func TestPublishSnapshotCarriesCommittedMoverMode(t *testing.T) {
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "flyer"}, MaxDamage: 1}
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
// carries no products, the page-count byte is the maximum authored build page
// plus one, and page N carries the authored entries (N-1)*6..N*6-1. A commander
// with nineteen products has four authored pages and a count of five; a factory
// with one authored page still has a count of two, so BUILD has somewhere to go.
func TestPublishedCommandPageCountsTheOrdersPage(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}, BuildMenus: map[string]*content.BuildMenuPage{}}
	newBuilder := func(key string, products int) []string {
		def := &content.UnitDef{UnitName: key, Builder: true, MaxDamage: 100}
		def.CanonicalKey = key
		cat.Units[key] = def
		buttons := make([]string, products)
		for i := range buttons {
			buttons[i] = fmt.Sprintf("%s-p%d", key, i+1)
		}
		cat.BuildMenus[key] = &content.BuildMenuPage{Buttons: buttons}
		return buttons
	}
	commanderProducts := newBuilder("commander", 19)
	factoryProducts := newBuilder("factory", 4)

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
