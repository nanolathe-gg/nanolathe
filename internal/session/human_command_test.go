package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
)

func TestHumanSelectionCommandDrainsAtInputBoundary(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	w := units.New(8, cat)
	def := &content.UnitDef{UnitName: "armcom", MaxDamage: 100}
	def.CanonicalKey = "armcom"
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{Units: w, LocalOwner: 0}
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanSelectionReplace, Selection: HumanSelectionCommand{Handles: []pool.Handle{h}}}); err != nil {
		t.Fatal(err)
	}
	if w.Unit(h).Flags&0x10 != 0 {
		t.Fatal("enqueue mutated authoritative flags")
	}
	s.applyHumanCommands(1)
	if w.Unit(h).Flags&0x10 == 0 {
		t.Fatal("input boundary did not apply selection")
	}
	if len(s.PendingHumanCommands()) != 0 {
		t.Fatal("input queue not drained")
	}
}

func TestHumanCommandKindsQueueAndApplyAtBoundary(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	product := &content.UnitDef{UnitName: "peewee", Builder: false, MaxDamage: 50}
	product.CanonicalKey = "peewee"
	cat.Units[product.CanonicalKey] = product
	builderDef := &content.UnitDef{UnitName: "builder", Builder: true, CanMove: true, MaxDamage: 100, OnOffable: true}
	builderDef.CanonicalKey = "builder"
	factoryDef := &content.UnitDef{UnitName: "factory", Builder: true, CanMove: false, MaxDamage: 100}
	factoryDef.CanonicalKey = "factory"
	cat.Units[builderDef.CanonicalKey] = builderDef
	cat.Units[factoryDef.CanonicalKey] = factoryDef
	builderWorld := units.New(16, cat)
	hBuilder, _ := builderWorld.Create(builderDef, 0, 0, 0, 0)
	hFactory, _ := builderWorld.Create(factoryDef, 0, 4<<16, 0, 0)
	s := &Session{Units: builderWorld, Catalog: cat, LocalOwner: 0}
	cmds := []HumanCommand{
		{Kind: HumanSelectionReplace, Selection: HumanSelectionCommand{Handles: []pool.Handle{hBuilder}}},
		{Kind: HumanSelectionToggle, Selection: HumanSelectionCommand{Handles: []pool.Handle{hBuilder}}},
		{Kind: HumanSelectionClear},
		{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{hBuilder}, Code: 2, Position: orders.ResolvePos{X: 10 << 16, Z: 10 << 16}}},
		{Kind: HumanStop, Stop: HumanStopCommand{Handles: []pool.Handle{hBuilder}}},
		{Kind: HumanActivation, Activation: HumanActivationCommand{Unit: hBuilder, Activate: true}},
		{Kind: HumanMobileBuild, MobileBuild: HumanMobileBuildCommand{Builder: hBuilder, Product: "peewee", WX: 2 << 16, WZ: 2 << 16}},
		{Kind: HumanFactoryBuild, FactoryBuild: HumanFactoryBuildCommand{Builder: hFactory, Product: "peewee"}},
		{Kind: HumanCancelProduction, CancelProduction: HumanCancelProductionCommand{Unit: hFactory}},
		{Kind: HumanStockpile, Stockpile: HumanStockpileCommand{Unit: hBuilder}},
	}
	for _, c := range cmds {
		if err := s.EnqueueHumanCommand(c); err != nil {
			t.Fatal(err)
		}
	}
	if builderWorld.Unit(hBuilder).Flags&0x10 != 0 {
		t.Fatal("enqueue mutated selection immediately")
	}
	s.applyHumanCommands(1)
	if len(s.PendingHumanCommands()) != 0 {
		t.Fatal("commands remained queued")
	}
	if q := orders.QueueForUnit(builderWorld.Unit(hBuilder)); q == nil || q.LenPrimary() == 0 {
		t.Fatal("canonical command application queued no builder order")
	}
}

func TestSelectionThenImplicitOrderAndStopUsesCurrentSelection(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	def := &content.UnitDef{UnitName: "scout", CanMove: true, MaxDamage: 100}
	def.CanonicalKey = "scout"
	cat.Units[def.CanonicalKey] = def
	w := units.New(8, cat)
	h, _ := w.Create(def, 0, 0, 0, 0)
	s := &Session{Units: w, Catalog: cat, LocalOwner: 0}
	_ = s.EnqueueHumanCommand(HumanCommand{Kind: HumanSelectionReplace, Selection: HumanSelectionCommand{Handles: []pool.Handle{h}}})
	_ = s.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Code: 2, Position: orders.ResolvePos{X: 8 << 16, Z: 8 << 16}}})
	s.applyHumanCommands(7)
	q := orders.QueueForUnit(w.Unit(h))
	if q == nil || q.LenPrimary() == 0 || orders.DescriptorFor(q.Head().ID).Name != "Move_Ground" {
		t.Fatal("implicit order did not use selection applied earlier in boundary")
	}
	_ = s.EnqueueHumanCommand(HumanCommand{Kind: HumanSelectionReplace, Selection: HumanSelectionCommand{Handles: []pool.Handle{h}}})
	_ = s.EnqueueHumanCommand(HumanCommand{Kind: HumanStop})
	s.applyHumanCommands(8)
	if q.Head() == nil || orders.DescriptorFor(q.Head().ID).Name != "Stop" {
		t.Fatal("implicit stop did not use current selection")
	}
}

func TestHumanBuildMetadataIsStampedAtInputBoundary(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	product := &content.UnitDef{UnitName: "product", MaxDamage: 10}
	product.CanonicalKey = "product"
	cat.Units[product.CanonicalKey] = product
	bdef := &content.UnitDef{UnitName: "builder", Builder: true, CanMove: true, MaxDamage: 10}
	bdef.CanonicalKey = "builder"
	cat.Units[bdef.CanonicalKey] = bdef
	fdef := &content.UnitDef{UnitName: "factory", Builder: true, CanMove: false, MaxDamage: 10}
	fdef.CanonicalKey = "factory"
	cat.Units[fdef.CanonicalKey] = fdef
	w := units.New(8, cat)
	hb, _ := w.Create(bdef, 0, 0, 0, 0)
	hf, _ := w.Create(fdef, 0, 0, 0, 0)
	s := &Session{Units: w, Catalog: cat, LocalOwner: 0}
	_ = s.EnqueueHumanCommand(HumanCommand{Kind: HumanMobileBuild, MobileBuild: HumanMobileBuildCommand{Builder: hb, Product: "product", WX: 2 << 16, WZ: 3 << 16, Queued: true}})
	_ = s.EnqueueHumanCommand(HumanCommand{Kind: HumanFactoryBuild, FactoryBuild: HumanFactoryBuildCommand{Builder: hf, Product: "product", Queued: true}})
	s.applyHumanCommands(42)
	for _, h := range []pool.Handle{hb, hf} {
		q := orders.QueueForUnit(w.Unit(h))
		if q == nil || q.LenPrimary() == 0 {
			t.Fatalf("no build node for %d", h)
		}
		n := q.Head()
		if n.Owner != h || n.CreationTick != 42 || n.Flags&orders.FlagPurgeSurvivor == 0 {
			t.Fatalf("build metadata for %d: owner=%d tick=%d flags=%x", h, n.Owner, n.CreationTick, n.Flags)
		}
	}
}
