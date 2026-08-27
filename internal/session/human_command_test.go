package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/snapshot"
	"github.com/nanolathe/nanolathe/internal/units"
)

func TestHumanCommandSequenceAndDueTickAreSessionOwned(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "scout"}, UnitName: "scout", MaxDamage: 10}
	w := units.New(8, cat)
	first, _ := w.Create(def, 0, 0, 0, 0)
	second, _ := w.Create(def, 0, 0, 0, 0)
	s := &Session{Units: w, LocalOwner: 0, Clock: &clock.State{GlobalTick: 7}}
	// Caller metadata is ignored: the session is the sole owner of ordering
	// and scheduling at the local input boundary [01 §4.4].
	if err := s.EnqueueHumanCommand(HumanCommand{Sequence: 900, DueTick: 1, Kind: HumanSelectionReplace, Selection: HumanSelectionCommand{Handles: []pool.Handle{first}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.EnqueueHumanCommand(HumanCommand{Sequence: 1, DueTick: 1, Kind: HumanSelectionToggle, Selection: HumanSelectionCommand{Handles: []pool.Handle{first}}}); err != nil {
		t.Fatal(err)
	}
	s.Clock.GlobalTick = 8
	if err := s.EnqueueHumanCommand(HumanCommand{Sequence: 1, DueTick: 1, Kind: HumanSelectionReplace, Selection: HumanSelectionCommand{Handles: []pool.Handle{second}}}); err != nil {
		t.Fatal(err)
	}
	pending := s.PendingHumanCommands()
	if len(pending) != 3 || pending[0].Sequence != 1 || pending[1].Sequence != 2 || pending[2].Sequence != 3 || pending[0].DueTick != 8 || pending[1].DueTick != 8 || pending[2].DueTick != 9 {
		t.Fatalf("session metadata = %+v, want sequence 1,2,3 and due ticks 8,8,9", pending)
	}
	s.applyHumanCommands(8)
	if w.Unit(first).Flags&0x10 != 0 || w.Unit(second).Flags&0x10 != 0 {
		t.Fatalf("same-tick commands did not apply in sequence order or future command applied early: first=%x second=%x", w.Unit(first).Flags, w.Unit(second).Flags)
	}
	if got := s.PendingHumanCommands(); len(got) != 1 || got[0].Sequence != 3 {
		t.Fatalf("future queue = %+v, want sequence 3", got)
	}
	s.applyHumanCommands(9)
	if w.Unit(first).Flags&0x10 != 0 || w.Unit(second).Flags&0x10 == 0 {
		t.Fatalf("due commands did not apply in sequence order: first=%x second=%x", w.Unit(first).Flags, w.Unit(second).Flags)
	}
}

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
	_ = s.EnqueueHumanCommand(HumanCommand{Kind: HumanMobileBuild, MobileBuild: HumanMobileBuildCommand{Builder: hb, Product: "product", WX: 2 << 16, WY: 7 << 16, WZ: 3 << 16, Queued: true}})
	_ = s.EnqueueHumanCommand(HumanCommand{Kind: HumanFactoryBuild, FactoryBuild: HumanFactoryBuildCommand{Builder: hf, Product: "product", Queued: true}})
	s.applyHumanCommands(42)
	// Mobile build is queued → survivor flag set [04 §3.3]; factory products are
	// counted nodes and are no-purge per [R-P0-11][SC17] even when Queued true.
	qm := orders.QueueForUnit(w.Unit(hb))
	if qm == nil || qm.LenPrimary() == 0 {
		t.Fatalf("no build node for %d", hb)
	}
	nm := qm.Head()
	if nm.Owner != hb || nm.CreationTick != 42 || nm.GoalY != 7<<16 || nm.Flags&orders.FlagPurgeSurvivor == 0 {
		t.Fatalf("mobile build metadata for %d: owner=%d tick=%d goalY=%d flags=%x", hb, nm.Owner, nm.CreationTick, nm.GoalY, nm.Flags)
	}
	qf := orders.QueueForUnit(w.Unit(hf))
	if qf == nil || qf.LenPrimary() == 0 {
		t.Fatalf("no build node for %d", hf)
	}
	nf := qf.Head()
	if nf.Owner != hf || nf.CreationTick != 42 {
		t.Fatalf("factory build metadata for %d: owner=%d tick=%d flags=%x", hf, nf.Owner, nf.CreationTick, nf.Flags)
	}
	if nf.Flags&orders.FlagPurgeSurvivor != 0 {
		t.Fatalf("factory counted node should be no-purge even when Queued true, got flags %x [R-P0-11]", nf.Flags)
	}
}

func TestHumanBuildPageUsesAuthoritativeBuilderAndAuthoredPageGuard(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}, BuildMenus: map[string]*content.BuildMenuPage{}}
	bdef := &content.UnitDef{UnitName: "armcom", Builder: true, MaxDamage: 100}
	bdef.CanonicalKey = "armcom"
	other := &content.UnitDef{UnitName: "other", Builder: true, MaxDamage: 100}
	other.CanonicalKey = "other"
	cat.Units[bdef.CanonicalKey], cat.Units[other.CanonicalKey] = bdef, other
	buttons := []string{"armsolar", "armmex", "armlab", "armllt", "armstump", "armham", "armflash"}
	cat.BuildMenus[bdef.CanonicalKey] = &content.BuildMenuPage{Buttons: buttons}
	w := units.New(8, cat)
	h, _ := w.Create(bdef, 0, 0, 0, 0)
	ho, _ := w.Create(other, 0, 0, 0, 0)
	w.Unit(h).Flags |= 0x10
	s := &Session{Units: w, Catalog: cat, LocalOwner: 0}
	// Seven authored products make two six-button pages; page 1 is valid.
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanBuildPage, BuildPage: HumanBuildPageCommand{Builder: h, Page: 1}}); err != nil {
		t.Fatal(err)
	}
	s.applyHumanCommands(1)
	if got := hud.DecodePage(w.Unit(h).Flags); got != 1 {
		t.Fatalf("valid page command got page %d, want 1", got)
	}
	// An invalid builder identity is rejected at the authoritative boundary.
	_ = s.EnqueueHumanCommand(HumanCommand{Kind: HumanBuildPage, BuildPage: HumanBuildPageCommand{Builder: ho, Page: 1}})
	s.applyHumanCommands(2)
	if got := hud.DecodePage(w.Unit(h).Flags); got != 1 {
		t.Fatalf("invalid page command changed page to %d", got)
	}
	// A valid builder with an out-of-range request follows the established
	// page-count clamp before encoding, rather than writing an invalid page.
	_ = s.EnqueueHumanCommand(HumanCommand{Kind: HumanBuildPage, BuildPage: HumanBuildPageCommand{Builder: h, Page: 0}})
	s.applyHumanCommands(3)
	if got := hud.DecodePage(w.Unit(h).Flags); got != 0 {
		t.Fatalf("page reset got %d, want 0", got)
	}
	_ = s.EnqueueHumanCommand(HumanCommand{Kind: HumanBuildPage, BuildPage: HumanBuildPageCommand{Builder: h, Page: 9}})
	s.applyHumanCommands(4)
	if got := hud.DecodePage(w.Unit(h).Flags); got != 1 {
		t.Fatalf("page-count clamp got %d, want 1", got)
	}
}

func TestHumanBuildPageSelectionThenPageSameBoundary(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}, BuildMenus: map[string]*content.BuildMenuPage{}}
	bdef := &content.UnitDef{UnitName: "builder", Builder: true, MaxDamage: 100}
	bdef.CanonicalKey = "builder"
	cat.Units[bdef.CanonicalKey] = bdef
	cat.BuildMenus[bdef.CanonicalKey] = &content.BuildMenuPage{Buttons: []string{"a", "b", "c", "d", "e", "f", "g"}}
	w := units.New(8, cat)
	h, _ := w.Create(bdef, 0, 0, 0, 0)
	s := &Session{Units: w, Catalog: cat, LocalOwner: 0}
	_ = s.EnqueueHumanCommand(HumanCommand{Kind: HumanSelectionReplace, Selection: HumanSelectionCommand{Handles: []pool.Handle{h}}})
	_ = s.EnqueueHumanCommand(HumanCommand{Kind: HumanBuildPage, BuildPage: HumanBuildPageCommand{Builder: h, Page: 1}})
	s.applyHumanCommands(3)
	if w.Unit(h).Flags&0x10 == 0 || hud.DecodePage(w.Unit(h).Flags) != 1 {
		t.Fatalf("selection then page did not apply in one input boundary: flags=%08x", w.Unit(h).Flags)
	}
}

func TestCommandPagePublicationIsImmutableAndUsesSelectedPage(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}, BuildMenus: map[string]*content.BuildMenuPage{}}
	bdef := &content.UnitDef{UnitName: "builder", Builder: true, MaxDamage: 100}
	bdef.CanonicalKey = "builder"
	cat.Units[bdef.CanonicalKey] = bdef
	menu := &content.BuildMenuPage{Buttons: []string{"a", "b", "c", "d", "e", "f", "g"}}
	cat.BuildMenus[bdef.CanonicalKey] = menu
	w := units.New(8, cat)
	h, _ := w.Create(bdef, 0, 0, 0, 0)
	w.Unit(h).Flags = 0x10 | hud.EncodePageBits(0, 1)
	s := &Session{Units: w, Catalog: cat, LocalOwner: 0, Snapshot: &snapshot.Buffer{}}
	s.publishSnapshot(4)
	_, frame, ok := s.Snapshot.Read()
	if !ok || frame.CommandPage.Page != 1 || len(frame.CommandPage.ProductKeys) != 1 || frame.CommandPage.ProductKeys[0] != "g" {
		t.Fatalf("published page=%d products=%v", frame.CommandPage.Page, frame.CommandPage.ProductKeys)
	}
	menu.Buttons[6] = "mutated-after-publish"
	if frame.CommandPage.ProductKeys[0] != "g" {
		t.Fatalf("published product keys alias mutable catalog: %v", frame.CommandPage.ProductKeys)
	}
}

func TestHumanBuildPageRejectsMixedAndMultiBuilderSelection(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}, BuildMenus: map[string]*content.BuildMenuPage{}}
	bdef := &content.UnitDef{UnitName: "builder", Builder: true, MaxDamage: 100}
	bdef.CanonicalKey = "builder"
	workerDef := &content.UnitDef{UnitName: "worker", MaxDamage: 100}
	workerDef.CanonicalKey = "worker"
	otherBuilder := &content.UnitDef{UnitName: "otherbuilder", Builder: true, MaxDamage: 100}
	otherBuilder.CanonicalKey = "otherbuilder"
	cat.Units[bdef.CanonicalKey] = bdef
	cat.Units[workerDef.CanonicalKey] = workerDef
	cat.Units[otherBuilder.CanonicalKey] = otherBuilder
	cat.BuildMenus[bdef.CanonicalKey] = &content.BuildMenuPage{Buttons: []string{"a", "b", "c", "d", "e", "f", "g"}}
	cat.BuildMenus[otherBuilder.CanonicalKey] = &content.BuildMenuPage{Buttons: []string{"a", "b", "c", "d", "e", "f", "g"}}
	w := units.New(8, cat)
	h, _ := w.Create(bdef, 0, 0, 0, 0)
	n, _ := w.Create(workerDef, 0, 0, 0, 0)
	o, _ := w.Create(otherBuilder, 0, 0, 0, 0)
	s := &Session{Units: w, Catalog: cat, LocalOwner: 0, Snapshot: &snapshot.Buffer{}}

	// A builder mixed with an ordinary unit has aggregate command state and
	// cannot mutate a single-builder page [07 §9].
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanSelectionReplace, Selection: HumanSelectionCommand{Handles: []pool.Handle{h, n}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanBuildPage, BuildPage: HumanBuildPageCommand{Builder: h, Page: 1}}); err != nil {
		t.Fatal(err)
	}
	s.applyHumanCommands(1)
	if got := hud.DecodePage(w.Unit(h).Flags); got != 0 {
		t.Fatalf("mixed builder/non-builder selection changed page to %d", got)
	}
	s.publishSnapshot(1)
	_, frame, ok := s.Snapshot.Read()
	if !ok || frame.CommandPage.Builder != 0 {
		t.Fatalf("mixed selection published builder page: %+v", frame.CommandPage)
	}

	// Two selected builders are likewise not a single page identity.
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanSelectionReplace, Selection: HumanSelectionCommand{Handles: []pool.Handle{h, o}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanBuildPage, BuildPage: HumanBuildPageCommand{Builder: h, Page: 1}}); err != nil {
		t.Fatal(err)
	}
	s.applyHumanCommands(2)
	if got := hud.DecodePage(w.Unit(h).Flags); got != 0 {
		t.Fatalf("multi-builder selection changed page to %d", got)
	}
	s.publishSnapshot(2)
	_, frame, ok = s.Snapshot.Read()
	if !ok || frame.CommandPage.Builder != 0 {
		t.Fatalf("multi-builder selection published builder page: %+v", frame.CommandPage)
	}
}
