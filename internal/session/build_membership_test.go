package session

import (
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
)

// Binding changes future build admission for both human and AI consumers. It
// must leave resources, both RNG streams and already-published frames alone.
func TestBuildMembershipSwitchPublishesOwnedListAndRebindsAI(t *testing.T) {
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "builder"}, UnitName: "builder", Builder: true, MaxDamage: 100, BuildPageCount: 2}
	menu := &content.BuildMenuPage{Buttons: []string{"ai"}, AuthoredButtons: []string{"ai", "ai", "factory"}}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"builder": def}, BuildMenus: map[string]*content.BuildMenuPage{"builder": menu}}
	w := newSessionFixtureWorld(8, cat)
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	w.Unit(h).Flags = 0x10 | hud.EncodePageBits(0, 1)
	s := &Session{Units: w, Catalog: cat, LocalOwner: 0, Snapshot: frame.NewBuffer(), Econ: &economy.Service{}}
	s.AI[1] = &ai.Manager{Catalog: cat}
	s.Econ.Players[0].Stock = [2]float32{321, 654}
	s.BindRules(StrictRuleSet())
	s.publishSnapshot(1)
	before := s.Snapshot.Current()
	sim, crt := *s.SimRNG(), *s.CrtRNG()
	s.BindRules(ModernRuleSet())
	s.RebindRules()
	s.publishSnapshot(2)
	modern := s.Snapshot.Current()
	if !slices.Contains(hud.AllowedBuildProducts(cat, modern), "factory") || slices.Contains(hud.AllowedBuildProducts(cat, before), "factory") {
		t.Fatal("published admission did not follow selected rule")
	}
	if !slices.Equal(s.AI[1].BuildProducts("builder"), menu.AuthoredButtons) {
		t.Fatal("AI did not inherit construction rule")
	}
	if !slices.Equal(before.CommandPage.AllowedProducts, menu.Buttons) {
		t.Fatal("previous frame mutated")
	}
	s.BindRules(StrictRuleSet())
	s.publishSnapshot(3)
	if slices.Contains(hud.AllowedBuildProducts(cat, s.Snapshot.Current()), "factory") || !slices.Equal(s.AI[1].BuildProducts("builder"), menu.Buttons) {
		t.Fatal("strict rebind retained extension")
	}
	if *s.SimRNG() != sim || *s.CrtRNG() != crt || s.Econ.Players[0].Stock != ([2]float32{321, 654}) {
		t.Fatal("membership selection spent RNG or resources")
	}
}

type noBuildProducts struct{ construction.StrictRules }

func (noBuildProducts) BuildProducts(*content.BuildMenuPage) []string { return nil }

func TestPublishedEmptyBuildMembershipDoesNotFallBackToCatalog(t *testing.T) {
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "builder"}, UnitName: "builder", Builder: true, MaxDamage: 100, BuildPageCount: 2}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"builder": def}, BuildMenus: map[string]*content.BuildMenuPage{"builder": {Buttons: []string{"factory"}}}}
	w := newSessionFixtureWorld(8, cat)
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	w.Unit(h).Flags = 0x10 | hud.EncodePageBits(0, 1)
	s := &Session{Units: w, Catalog: cat, LocalOwner: 0, Snapshot: frame.NewBuffer()}
	rules := StrictRuleSet()
	rules.Construction = noBuildProducts{}
	s.BindRules(rules)
	s.publishSnapshot(1)
	f := s.Snapshot.Current()
	if f.CommandPage.AllowedProducts == nil || slices.Contains(hud.AllowedBuildProducts(cat, f), "factory") {
		t.Fatal("explicit empty rule membership fell back to catalog")
	}
}
