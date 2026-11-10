package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func TestLocalTypedCommandsApplyWithoutAUnitWorld(t *testing.T) {
	econ := &economy.Service{}
	econ.Players[0].Exists = true
	s := &Session{Econ: econ, LocalOwner: 0}
	s.applyHumanCommand(HumanCommand{Kind: HumanNoShake}, 1)
	if !s.NoShake() {
		t.Fatal("HumanNoShake did not use the authoritative toggle")
	}
	s.applyHumanCommand(HumanCommand{Kind: HumanATM}, 1)
	if econ.Players[0].Stock[economy.Metal] != 1000 || econ.Players[0].Stock[economy.Energy] != 1000 {
		t.Fatalf("HumanATM stock = %v, want uncapped 1000 metal and energy", econ.Players[0].Stock)
	}
	s.Mission = &mission.Mission{Type: mission.TypeCampaign}
	s.applyHumanCommand(HumanCommand{Kind: HumanATM}, 2)
	if econ.Players[0].Stock[economy.Metal] != 1000 || econ.Players[0].Stock[economy.Energy] != 1000 {
		t.Fatal("HumanATM changed campaign stock across the authoritative mask-2 gate")
	}
}

func TestHumanSetResourceValidatesPlayerRecord(t *testing.T) {
	econ := &economy.Service{}
	for i, controller := range []uint8{1, 2, 3} {
		p := &econ.Players[i]
		p.Exists, p.ControllerState, p.Side = true, controller, uint8(i)
	}
	econ.Players[3] = economy.Player{Exists: true, ControllerState: 0, Stock: [2]float32{7, 7}}
	econ.Players[4] = economy.Player{Exists: true, ControllerState: 4, Stock: [2]float32{7, 7}}
	econ.Players[5] = economy.Player{Exists: true, ControllerState: 2, Side: 10, Stock: [2]float32{7, 7}}
	econ.Players[6] = economy.Player{ControllerState: 2, Side: 1, Stock: [2]float32{7, 7}}
	s := &Session{Econ: econ}
	for i := 0; i < 3; i++ {
		s.applyHumanCommand(HumanCommand{Kind: HumanSetResource, SetResource: HumanSetResourceCommand{Player: i, Resource: economy.Metal, Amount: float32(-10 - i)}}, 1)
		if got := econ.Players[i].Stock[economy.Metal]; got != float32(-10-i) {
			t.Fatalf("controller %d stock = %v, want %v", i+1, got, -10-i)
		}
	}
	for _, player := range []int{-1, 3, 4, 5, 6, 10} {
		s.applyHumanCommand(HumanCommand{Kind: HumanSetResource, SetResource: HumanSetResourceCommand{Player: player, Resource: economy.Energy, Amount: 99}}, 1)
	}
	for player := 3; player <= 6; player++ {
		if got := econ.Players[player].Stock[economy.Energy]; got != 7 {
			t.Fatalf("invalid player %d stock = %v, want unchanged 7", player, got)
		}
	}
}

func TestHumanSetLogoValidatesPlayerRecordInEitherSessionKind(t *testing.T) {
	econ := &economy.Service{}
	econ.Players[0] = economy.Player{Exists: true, ControllerState: 1, Side: 4, Logo: 2}
	econ.Players[1] = economy.Player{Exists: true, ControllerState: 2, Side: 5, Logo: 3}
	econ.Players[2] = economy.Player{Exists: true, ControllerState: 3, Side: 6, Logo: 4}
	econ.Players[3] = economy.Player{Exists: true, ControllerState: 0, Side: 1, Logo: 9}
	econ.Players[4] = economy.Player{Exists: true, ControllerState: 4, Side: 1, Logo: 9}
	econ.Players[5] = economy.Player{Exists: true, ControllerState: 2, Side: 10, Logo: 9}
	econ.Players[6] = economy.Player{ControllerState: 2, Side: 1, Logo: 9}
	s := &Session{Econ: econ, Mission: &mission.Mission{Type: mission.TypeCampaign}}

	for player := 0; player < 3; player++ {
		s.applyHumanCommand(HumanCommand{Kind: HumanSetLogo, SetLogo: HumanSetLogoCommand{Player: player, Logo: uint8(7 + player)}}, 1)
		if got := econ.Players[player].Logo; got != uint8(7+player) {
			t.Fatalf("controller %d Logo=%d, want %d", player+1, got, 7+player)
		}
		if got := econ.Players[player].Side; got != uint8(4+player) {
			t.Fatalf("controller %d Side changed to %d", player+1, got)
		}
	}
	for _, player := range []int{-1, 3, 4, 5, 6, 10} {
		s.applyHumanCommand(HumanCommand{Kind: HumanSetLogo, SetLogo: HumanSetLogoCommand{Player: player, Logo: 1}}, 2)
	}
	for player := 3; player <= 6; player++ {
		if got := econ.Players[player].Logo; got != 9 {
			t.Fatalf("invalid player %d Logo=%d, want unchanged 9", player, got)
		}
	}

	s.Mission = nil
	s.applyHumanCommand(HumanCommand{Kind: HumanSetLogo, SetLogo: HumanSetLogoCommand{Player: 0, Logo: 1}}, 3)
	if got := econ.Players[0].Logo; got != 1 {
		t.Fatalf("skirmish Logo=%d, want 1", got)
	}
}

func TestHumanMakeSelectableVisitsOnlyLiveUnits(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	def := &content.UnitDef{UnitName: "unit", MaxDamage: 10}
	def.CanonicalKey = "unit"
	w := newSessionFixtureWorld(4, cat)
	live, _ := w.Create(def, 4, 0, 0, 0)
	dead, _ := w.Create(def, 5, 0, 0, 0)
	liveUnit, deadUnit := w.Unit(live), w.Unit(dead)
	liveUnit.Flags = units.ImmunityStatus | 0x1000
	deadUnit.Flags = units.ImmunityStatus | 0x1000
	deadUnit.Alive = false
	s := &Session{Units: w}
	s.applyHumanCommand(HumanCommand{Kind: HumanMakeSelectable}, 1)
	if got := liveUnit.Flags; got != units.ImmunityStatus|0x1000|units.ClassifierEligibleStatus {
		t.Fatalf("live unit flags = %#x, want selectable with existing flags preserved", got)
	}
	if got := deadUnit.Flags; got != units.ImmunityStatus|0x1000 {
		t.Fatalf("dead unit flags = %#x, want unchanged", got)
	}
}

func TestHumanVisibilityMasksAndCampaignGate(t *testing.T) {
	const all = visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled | visibility.ModeTerrainRay
	vis := visibility.New(&world.Terrain{CellW: 8, CellH: 8}, all)
	s := &Session{Vis: vis, Mission: &mission.Mission{Type: mission.TypeCampaign}}
	version := vis.MappingVersion()
	s.applyHumanCommand(HumanCommand{Kind: HumanVisibility, Visibility: HumanVisibilityCommand{ToggleMask: visibility.ModeTerrainRay}}, 1)
	if got := vis.Mode(); got != visibility.ModeHistoryEnabled|visibility.ModeCurrentEnabled || vis.MappingVersion() <= version {
		t.Fatalf("campaign LOSType mode/version = %#x/%d, want history+current and refresh", got, vis.MappingVersion())
	}
	version = vis.MappingVersion()
	s.applyHumanCommand(HumanCommand{Kind: HumanVisibility, Visibility: HumanVisibilityCommand{ToggleMask: visibility.ModeCurrentEnabled}}, 1)
	s.applyHumanCommand(HumanCommand{Kind: HumanVisibility, Visibility: HumanVisibilityCommand{ClearMask: visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled}}, 1)
	if got := vis.Mode(); got != visibility.ModeHistoryEnabled|visibility.ModeCurrentEnabled || vis.MappingVersion() != version {
		t.Fatalf("campaign admitted mask-2 visibility: mode/version=%#x/%d", got, vis.MappingVersion())
	}
	s.Mission = nil
	s.applyHumanCommand(HumanCommand{Kind: HumanVisibility, Visibility: HumanVisibilityCommand{ToggleMask: visibility.ModeCurrentEnabled}}, 2)
	if got := vis.Mode(); got != visibility.ModeHistoryEnabled {
		t.Fatalf("LOS toggle mode = %#x, want history only", got)
	}
	vis.SetMode(all)
	version = vis.MappingVersion()
	s.applyHumanCommand(HumanCommand{Kind: HumanVisibility, Visibility: HumanVisibilityCommand{ClearMask: visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled}}, 3)
	if got := vis.Mode(); got != visibility.ModeTerrainRay || vis.MappingVersion() != version+1 {
		t.Fatalf("NowISee mode/version = %#x/%d, want terrain-ray and one refresh", got, vis.MappingVersion())
	}
}

func TestHumanDamageModeCommandsRespectCampaignGate(t *testing.T) {
	combatService := &combat.Service{}
	s := &Session{Combat: combatService, Mission: &mission.Mission{Type: mission.TypeCampaign}}
	s.applyHumanCommand(HumanCommand{Kind: HumanDoubleShot}, 1)
	s.applyHumanCommand(HumanCommand{Kind: HumanHalfShot}, 1)
	if !combatService.ToggleDoubleShot() || !combatService.ToggleHalfShot() {
		t.Fatal("campaign changed a mask-2 damage mode")
	}
	combatService.ToggleDoubleShot()
	combatService.ToggleHalfShot()

	s.Mission = nil
	s.applyHumanCommand(HumanCommand{Kind: HumanDoubleShot}, 2)
	s.applyHumanCommand(HumanCommand{Kind: HumanHalfShot}, 2)
	if combatService.ToggleDoubleShot() || combatService.ToggleHalfShot() {
		t.Fatal("skirmish did not toggle both damage modes")
	}
}

// `+ShootAll` is a mask-1 command: campaign applies it exactly as skirmish
// does, each application toggles the combat gate, and a second one restores
// check 2 [07 R-CAM-01 §6][06 §3.2 "The option bit of check 2"]. ToggleShootAll
// reports the value after its own flip, so it reads the gate and then puts it
// back.
func TestHumanShootAllTogglesInEverySessionKind(t *testing.T) {
	for _, m := range []*mission.Mission{nil, {Type: mission.TypeCampaign}} {
		combatService := &combat.Service{}
		s := &Session{Combat: combatService, Mission: m}
		gate := func() bool {
			on := !combatService.ToggleShootAll()
			combatService.ToggleShootAll()
			return on
		}
		s.applyHumanCommand(HumanCommand{Kind: HumanShootAll}, 1)
		if !gate() {
			t.Fatalf("mission %+v: the first +ShootAll left the gate clear", m)
		}
		s.applyHumanCommand(HumanCommand{Kind: HumanShootAll}, 2)
		if gate() {
			t.Fatalf("mission %+v: the second +ShootAll did not clear the gate", m)
		}
	}
	// A session without a combat service has nothing to toggle.
	(&Session{}).applyHumanCommand(HumanCommand{Kind: HumanShootAll}, 1)
}

func TestHumanCommandSequenceAndDueTickAreSessionOwned(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "scout"}, UnitName: "scout", MaxDamage: 10}
	w := newSessionFixtureWorld(8, cat)
	first, _ := w.Create(def, 0, 0, 0, 0)
	second, _ := w.Create(def, 0, 0, 0, 0)
	s := &Session{Units: w, LocalOwner: 0, Clock: &clock.State{GlobalTick: 7}}
	stop := func(h pool.Handle) HumanCommand {
		return HumanCommand{Kind: HumanStop, Stop: HumanStopCommand{Handles: []pool.Handle{h}}}
	}
	// Caller metadata is ignored: the session is the sole owner of ordering
	// and scheduling at the local input boundary [01 §4.4].
	c := stop(first)
	c.Sequence, c.DueTick = 900, 1
	if err := s.EnqueueHumanCommand(c); err != nil {
		t.Fatal(err)
	}
	c = stop(second)
	c.Sequence, c.DueTick = 1, 1
	if err := s.EnqueueHumanCommand(c); err != nil {
		t.Fatal(err)
	}
	s.Clock.GlobalTick = 8
	if err := s.EnqueueHumanCommand(stop(first)); err != nil {
		t.Fatal(err)
	}
	pending := s.PendingHumanCommands()
	if len(pending) != 3 || pending[0].Sequence != 1 || pending[1].Sequence != 2 || pending[2].Sequence != 3 || pending[0].DueTick != 8 || pending[1].DueTick != 8 || pending[2].DueTick != 9 {
		t.Fatalf("session metadata = %+v, want sequence 1,2,3 and due ticks 8,8,9", pending)
	}
	s.applyHumanCommands(8)
	if got := s.PendingHumanCommands(); len(got) != 1 || got[0].Sequence != 3 {
		t.Fatalf("future queue = %+v, want sequence 3", got)
	}
	s.applyHumanCommands(9)
	if len(s.PendingHumanCommands()) != 0 {
		t.Fatal("the due command stayed queued")
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
	builderWorld := newSessionFixtureWorld(16, cat)
	hBuilder, _ := builderWorld.Create(builderDef, 0, 0, 0, 0)
	hFactory, _ := builderWorld.Create(factoryDef, 0, 4<<16, 0, 0)
	s := &Session{Units: builderWorld, Catalog: cat, LocalOwner: 0}
	cmds := []HumanCommand{
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
	if q := orders.QueueOfUnit(builderWorld.Unit(hBuilder)); q != nil && q.LenPrimary() != 0 {
		t.Fatal("enqueue mutated an order queue immediately")
	}
	s.applyHumanCommands(1)
	if len(s.PendingHumanCommands()) != 0 {
		t.Fatal("commands remained queued")
	}
	if q := orders.QueueForUnit(builderWorld.Unit(hBuilder)); q == nil || q.LenPrimary() == 0 {
		t.Fatal("canonical command application queued no builder order")
	}
}

// An order, Stop or broadcast that names no units does nothing: the session
// holds no selection to fall back to. The client resolves every order's units
// from its own selection when it sends the order (DESIGN_MULTIPLAYER §7.3).
func TestCommandsWithoutActorsHaveNoSelectionFallback(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	// A mobile unit authors bmcode 1; the resolver's live-mover test reads the
	// building-class status bit that creation derives from it [04 R-ORD-02 §1].
	def := &content.UnitDef{UnitName: "scout", BMCode: 1, CanMove: true, MaxDamage: 100, MobileStandOrders: true}
	def.CanonicalKey = "scout"
	cat.Units[def.CanonicalKey] = def
	w := newSessionFixtureWorld(8, cat)
	h, _ := w.Create(def, 0, 0, 0, 0)
	w.Unit(h).Flags |= units.SelectedStatus // a stray bit is not a selection
	s := &Session{Units: w, Catalog: cat, LocalOwner: 0}
	for _, c := range []HumanCommand{
		{Kind: HumanOrder, Order: HumanOrderCommand{Code: 2, Position: orders.ResolvePos{X: 8 << 16, Z: 8 << 16}}},
		{Kind: HumanStop}, {Kind: HumanSelfDestruct},
		{Kind: HumanStance, Stance: HumanStanceCommand{Value: 1}}, {Kind: HumanCloak, Cloak: HumanCloakCommand{Cloak: true}},
	} {
		if err := s.EnqueueHumanCommand(c); err != nil {
			t.Fatal(err)
		}
	}
	s.applyHumanCommands(7)
	if q := orders.QueueOfUnit(w.Unit(h)); q != nil && q.LenPrimary() != 0 {
		t.Fatalf("a command without actors reached unit %d: %s", h, orders.DescriptorFor(q.Head().ID).Name)
	}
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{h}, Code: 2, Position: orders.ResolvePos{X: 8 << 16, Z: 8 << 16}}}); err != nil {
		t.Fatal(err)
	}
	s.applyHumanCommands(8)
	q := orders.QueueForUnit(w.Unit(h))
	if q == nil || q.LenPrimary() == 0 || orders.DescriptorFor(q.Head().ID).Name != "Move_Ground" {
		t.Fatal("an order naming its unit did not apply")
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
	w := newSessionFixtureWorld(8, cat)
	hb, _ := w.Create(bdef, 0, 0, 0, 0)
	hf, _ := w.Create(fdef, 0, 0, 0, 0)
	s := &Session{Units: w, Catalog: cat, LocalOwner: 0}
	_ = s.EnqueueHumanCommand(HumanCommand{Kind: HumanMobileBuild, MobileBuild: HumanMobileBuildCommand{Builder: hb, Product: "product", WX: 2 << 16, WY: 7 << 16, WZ: 3 << 16, Queued: true}})
	_ = s.EnqueueHumanCommand(HumanCommand{Kind: HumanFactoryBuild, FactoryBuild: HumanFactoryBuildCommand{Builder: hf, Product: "product"}})
	s.applyHumanCommands(42)
	// Purge survivorship is the descriptor's static gate bit 2, not the queue
	// modifier [04 §3.3][04 R-MOV-03 §6]. MobileBuild does not carry it;
	// BuildingBuild does, which is what keeps a factory producing across a
	// player order that purges. These two assertions used to read the other
	// way round — the queued mobile build was expected to be protected and the
	// factory's counted node was expected to be purgeable, conflating "this
	// command does not purge" with "this record survives a purge".
	qm := orders.QueueForUnit(w.Unit(hb))
	if qm == nil || qm.LenPrimary() == 0 {
		t.Fatalf("no build node for %d", hb)
	}
	if s.Build == nil || qm.Binding() != s.Build.OrderBinding {
		t.Fatal("queued mobile build did not retain the session queue binding")
	}
	nm := qm.Head()
	if nm.Owner != hb || nm.CreationTick != 42 || nm.GoalY != 7<<16 || nm.Flags&orders.FlagPurgeSurvivor != 0 {
		t.Fatalf("mobile build metadata for %d: owner=%d tick=%d goalY=%d flags=%x", hb, nm.Owner, nm.CreationTick, nm.GoalY, nm.Flags)
	}
	qf := orders.QueueForUnit(w.Unit(hf))
	if qf == nil || qf.LenPrimary() == 0 {
		t.Fatalf("no build node for %d", hf)
	}
	nf := qf.Head()
	if nm.Param2 != 0 || nf.Param2 != 1 {
		t.Fatalf("placement/factory counts = %d/%d, want 0/1 [07 R-P0-11 §2]", nm.Param2, nf.Param2)
	}
	if nf.Owner != hf || nf.CreationTick != 42 {
		t.Fatalf("factory build metadata for %d: owner=%d tick=%d flags=%x", hf, nf.Owner, nf.CreationTick, nf.Flags)
	}
	if nf.Flags&orders.FlagPurgeSurvivor == 0 {
		t.Fatalf("BuildingBuild carries static gate bit 2 and must survive a Replace purge, got flags %x [04 R-MOV-03 §6]", nf.Flags)
	}
}

func TestHumanCancelWithoutQueueBindsLazyQueue(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	def := &content.UnitDef{UnitName: "builder", Builder: true, CanMove: true, MaxDamage: 10}
	def.CanonicalKey = "builder"
	cat.Units[def.CanonicalKey] = def
	w := newSessionFixtureWorld(4, cat)
	h, _ := w.Create(def, 0, 0, 0, 0)
	s := &Session{Units: w, Catalog: cat, LocalOwner: 0}
	s.applyHumanCommand(HumanCommand{Kind: HumanCancelProduction, CancelProduction: HumanCancelProductionCommand{Unit: h}}, 1)
	q := orders.QueueForUnit(w.Unit(h))
	if s.Build == nil || q.Binding() == nil || q.Binding() != s.Build.OrderBinding {
		t.Fatal("cancel-without-queue created an unbound queue")
	}
}

// TestHumanGroupDoesNotWriteAIUnits locks the ownership boundary between the
// local input path and AI tactical groups. A skirmish's LocalOwner is selected
// from a human lobby row; the command path then filters that owner before
// writing group numbers to live units [07 §9][08 "Skirmish configuration"].
func TestHumanGroupDoesNotWriteAIUnits(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	def := &content.UnitDef{UnitName: "scout", MaxDamage: 10}
	def.CanonicalKey = "scout"
	cat.Units[def.CanonicalKey] = def
	w := newSessionFixtureWorld(4, cat)
	human, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	aiUnit, err := w.Create(def, 1, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	w.Unit(aiUnit).Group = 4
	s := &Session{Units: w, Catalog: cat, LocalOwner: 0}
	// The membership names a foreign unit too; only the issuer's units are
	// written.
	s.applyHumanCommand(HumanCommand{Kind: HumanGroupAssign, Group: HumanGroupCommand{Group: 2, Handles: []pool.Handle{human, aiUnit}}}, 1)
	if got := w.Unit(human).Group; got != 2 {
		t.Fatalf("human group=%d, want 2", got)
	}
	if got := w.Unit(aiUnit).Group; got != 4 {
		t.Fatalf("AI group=%d, want unchanged 4", got)
	}
}

// TestViewThenGiveDebitsOwnSlot locks the Give source: the own/controlling slot
// that `Control` changes, not the viewing slot that `View` changes. A View
// queued ahead of Give in the same batch moves only the viewing slot, so Give
// still debits the own slot [07 R-CAM-01 §6]; the recipient validation and the
// staged mirror credit are unchanged [05 R-SHARE-01 §2]. The own slot is 1 so
// a source hard-wired to slot 0 would also fail.
func TestViewThenGiveDebitsOwnSlot(t *testing.T) {
	s := &Session{Econ: &economy.Service{}, LocalOwner: 1, ViewingOwner: 1}
	for i := 0; i < 4; i++ {
		s.Econ.Players[i] = economy.Player{Exists: true, ControllerState: 1}
		s.Econ.Players[i].Stock[economy.Metal] = 30
	}
	view := HumanCommand{Kind: HumanView, View: HumanViewCommand{Player: 3}}
	give := HumanCommand{Kind: HumanGive, Give: HumanGiveCommand{Player: 2, Resource: economy.Metal, Amount: 20}}
	for _, c := range []HumanCommand{view, give} {
		if err := s.EnqueueHumanCommand(c); err != nil {
			t.Fatal(err)
		}
	}
	if s.ViewingOwner != 1 || s.Econ.Players[1].Stock[economy.Metal] != 30 {
		t.Fatal("queued input mutated live state before draining")
	}
	s.applyHumanCommands(1)
	if s.ViewingOwner != 3 || s.LocalOwner != 1 {
		t.Fatalf("View moved slots view=%d own=%d, want view 3 own 1", s.ViewingOwner, s.LocalOwner)
	}
	for i, want := range [4]float32{30, 10, 30, 30} {
		if got := s.Econ.Players[i].Stock[economy.Metal]; got != want {
			t.Fatalf("slot %d metal=%v, want %v: Give must debit the own slot, not the viewing slot", i, got, want)
		}
	}
	if got := s.Econ.Players[2].Mirror[economy.Metal].Production; got != 20 {
		t.Fatalf("recipient staged credit=%v, want 20", got)
	}
	s.Mission = &mission.Mission{Type: mission.TypeCampaign}
	view.View.Player = 0
	give.Give.Amount = 5
	s.applyHumanCommand(view, 2)
	s.applyHumanCommand(give, 2)
	if s.ViewingOwner != 3 || s.Econ.Players[1].Stock[economy.Metal] != 5 || s.Econ.Players[3].Stock[economy.Metal] != 30 {
		t.Fatal("campaign gates must reject View but permit Give from the own slot")
	}
	s.Econ.Players[2].Side = 10
	s.applyHumanCommand(give, 3)
	if s.Econ.Players[1].Stock[economy.Metal] != 5 {
		t.Fatal("Give accepted a non-player destination")
	}
}

// Site orders remain individually queued but contribute no production count
// to the HUD, for both mobile-build descriptors [07 R-P0-11 §2].
func TestHumanPlacementQueueHasNoProductCountLabel(t *testing.T) {
	for _, flying := range []bool{false, true} {
		name := "ground"
		if flying {
			name = "aircraft"
		}
		t.Run(name, func(t *testing.T) {
			product := &content.UnitDef{UnitName: "product", MaxDamage: 10}
			product.CanonicalKey = "product"
			builder := &content.UnitDef{UnitName: "builder", Builder: true, CanMove: true, CanFly: flying, MaxDamage: 10}
			builder.CanonicalKey = "builder"
			cat := &content.Catalog{Units: map[string]*content.UnitDef{"product": product, "builder": builder}}
			w := newSessionFixtureWorld(8, cat)
			h, err := w.Create(builder, 0, 0, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			s := &Session{Units: w, Catalog: cat, LocalOwner: 0}
			for i := 0; i < 2; i++ {
				s.applyHumanCommand(HumanCommand{Kind: HumanMobileBuild, MobileBuild: HumanMobileBuildCommand{
					Builder: h, Product: "product", WX: numeric.Fixed((64 + i*64) << 16), WZ: 64 << 16, Queued: i != 0,
				}}, uint32(i+1))
				q := orders.QueueForUnit(w.Unit(h))
				if q.LenPrimary() != i+1 {
					t.Fatalf("queued sites = %d, want %d", q.LenPrimary(), i+1)
				}
				views := appendOrderQueueView(nil, orders.SnapshotQueueOf(q, h, nil), cat)
				for _, node := range views[0].Primary {
					if node.BuildCount != 0 || node.DescriptorID != int32(mobileBuildKind(w.Unit(h))) {
						t.Fatalf("placement publication = %+v", node)
					}
				}
				if label := hud.QueueCountLabel(views, "product"); label != "" {
					t.Fatalf("site queue label = %q, want empty", label)
				}
			}
		})
	}
}
