package session

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Receiver tests of DESIGN_MULTIPLAYER §16.2 M2-C2–M2-C4. They bypass every
// interface: stamped commands are enqueued directly and the outcome, the
// random streams and the order queues are asserted. Authorization is
// Nanolathe protocol, not retail arithmetic.

// seatFixture is a battle with two human seats (0 and 1), a computer seat (2)
// added by seat 0, and a watcher row (3).
type seatFixture struct {
	s     *Session
	def   *content.UnitDef
	own0  pool.Handle // seat 0, the local seat
	own1a pool.Handle // seat 1
	own1b pool.Handle // seat 1
	comp2 pool.Handle // seat 2
	pos   uint64
}

func seatTestConfig(t *testing.T, cheats bool) EffectiveMatchConfig {
	t.Helper()
	r := validMatchRequest(t)
	r.CheatsAllowed = cheats
	r.WatchingAllowed = true
	r.Seats = append(r.Seats, MatchSeat{Role: MatchRoleWatcher, Color: 3, AllyGroup: 5, Nickname: "Wat",
		Participant: matchTestID(3), HostSeat: MatchHostNone, BuilderOptions: orders.DefaultBuilderOptions()})
	return resolveMatch(t, r)
}

func newSeatFixture(t *testing.T, online, cheats bool) *seatFixture {
	t.Helper()
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "scout"}, UnitName: "scout",
		BMCode: 1, CanMove: true, CanReclamate: true, OnOffable: true, MaxDamage: 100}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"scout": def}}
	w := newSessionFixtureWorld(64, cat)
	econ := &economy.Service{}
	for i, controller := range []uint8{1, 1, 2, 1} {
		econ.Players[i] = economy.Player{Exists: true, ControllerState: controller, Side: uint8(i % 2)}
		econ.Players[i].Stock = [2]float32{1000, 1000}
	}
	econ.Players[3].Watcher = true
	terrain := &world.Terrain{CellW: 64, CellH: 64, Plot: make([]world.PlotCell, 64*64)}
	s := &Session{Units: w, Catalog: cat, World: terrain, Econ: econ, Clock: &clock.State{}, Gameplay: gameplay.Modern}
	s.SeedSessionRNG(12345, 67890)
	s.Clock.GlobalTick = 10
	f := &seatFixture{s: s, def: def}
	create := func(owner uint8, x int32) pool.Handle {
		h, err := w.Create(def, owner, numeric.Fixed(x)<<16, 0, 64<<16)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	f.own0 = create(0, 64)
	f.own1a = create(1, 96)
	f.own1b = create(1, 128)
	f.comp2 = create(2, 160)
	if online {
		if err := setOnlineSeatCommands(s, seatTestConfig(t, cheats)); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *seatFixture) ref(h pool.Handle) pool.UnitRef { return f.s.Units.Reference(h) }

// stamp is the next stream entry for seat, bound to the next tick. Positions
// advance by two: gaps are legal.
func (f *seatFixture) stamp(seat uint8) CommandStamp {
	f.pos += 2
	return CommandStamp{Seat: seat, Tick: f.s.Clock.GlobalTick + 1, Position: f.pos}
}

// tick runs phase 1 of the next tick alone and returns its receipts.
func (f *seatFixture) tick() []CommandReceipt {
	next := f.s.Clock.GlobalTick + 1
	f.s.applyHumanCommands(next)
	f.s.Clock.GlobalTick = next
	return f.s.DrainCommandReceipts()
}

// issue enqueues one stamped command and runs its phase 1.
func (f *seatFixture) issue(t *testing.T, seat uint8, c SeatCommand) CommandReceipt {
	t.Helper()
	stamp := f.stamp(seat)
	if err := f.s.EnqueueSeatCommand(stamp, c); err != nil {
		t.Fatal(err)
	}
	rs := f.tick()
	if len(rs) != 1 || rs[0].Stamp != stamp {
		t.Fatalf("receipts %+v, want one for %+v", rs, stamp)
	}
	return rs[0]
}

func (f *seatFixture) queueLen(h pool.Handle) int {
	q := orders.QueueOfUnit(f.s.Units.Unit(h))
	if q == nil {
		return 0
	}
	return q.LenPrimary()
}

func (f *seatFixture) head(t *testing.T, h pool.Handle) *orders.Node {
	t.Helper()
	q := orders.QueueOfUnit(f.s.Units.Unit(h))
	if q == nil || q.LenPrimary() == 0 {
		t.Fatalf("unit %d has no queued order", h)
	}
	return q.Head()
}

type streamPositions struct {
	sim, crt any
}

func (f *seatFixture) streams() streamPositions {
	return streamPositions{sim: *f.s.SimRNG(), crt: *f.s.CrtRNG()}
}

func expectOutcome(t *testing.T, got CommandReceipt, want CommandOutcome) {
	t.Helper()
	if got.Outcome != want {
		t.Fatalf("outcome %d (%q), want %d", got.Outcome, got.Diagnostic, want)
	}
	if (want == CommandRejected) != (got.Diagnostic != "") {
		t.Fatalf("diagnostic %q for outcome %d", got.Diagnostic, got.Outcome)
	}
}

func moveTo(actors []pool.UnitRef, x int32) SeatCommand {
	return SeatCommand{Kind: SeatOrder, Order: OrderPayload{Actors: actors, Code: 2,
		Position: CommandPosition{X: numeric.Fixed(x) << 16, Z: 64 << 16}}}
}

// The kind numbers are protocol constants and keep the existing local
// numbers for recognition (§7.4.2).
func TestSeatCommandKindNumbers(t *testing.T) {
	pairs := []struct {
		seat  SeatCommandKind
		human HumanCommandKind
	}{
		{SeatSelectionReplace, HumanSelectionReplace}, {SeatSelectionToggle, HumanSelectionToggle},
		{SeatSelectionClear, HumanSelectionClear}, {SeatOrder, HumanOrder}, {SeatStop, HumanStop},
		{SeatActivation, HumanActivation}, {SeatMobileBuild, HumanMobileBuild}, {SeatFactoryBuild, HumanFactoryBuild},
		{SeatCancelProduction, HumanCancelProduction}, {SeatStockpile, HumanStockpile}, {SeatBuildPage, HumanBuildPage},
		{SeatGroupAssign, HumanGroupAssign}, {SeatGroupRecall, HumanGroupRecall}, {SeatStance, HumanStance},
		{SeatCloak, HumanCloak}, {SeatSelfDestruct, HumanSelfDestruct}, {SeatNoShake, HumanNoShake}, {SeatATM, HumanATM},
		{SeatSetResource, HumanSetResource}, {SeatSetLogo, HumanSetLogo}, {SeatView, HumanView}, {SeatGive, HumanGive},
		{SeatMakeSelectable, HumanMakeSelectable}, {SeatVisibility, HumanVisibility}, {SeatDoubleShot, HumanDoubleShot},
		{SeatHalfShot, HumanHalfShot}, {SeatMeteor, HumanMeteor}, {SeatBigBrother, HumanBigBrother},
		{SeatShiftState, HumanShiftState}, {SeatCancelQueuedMove, HumanCancelQueuedMove}, {SeatSpawn, HumanSpawn},
		{SeatBuilderOptions, HumanBuilderOptions}, {SeatCommunityOrderDrag, HumanCommunityOrderDrag},
		{SeatCommunityKickout, HumanCommunityKickout}, {SeatDeveloperSpawn, HumanDeveloperSpawn}, {SeatGameplay, HumanGameplay},
	}
	for i, p := range pairs {
		if uint8(p.seat) != uint8(p.human) {
			t.Fatalf("pair %d: seat kind %d, local kind %d", i, p.seat, p.human)
		}
	}
	if SeatCommunityKickout != 34 || SeatShareMetal != 35 || SeatShootAll != 45 || SeatGameplay != 255 {
		t.Fatal("reserved kind numbers moved")
	}
	if OnlineCommand != 1 || SinglePlayerReplay != 2 || CommandApplied != 1 || CommandNoOp != 2 || CommandRejected != 3 {
		t.Fatal("context or outcome numbers moved")
	}
}

// A stamp names seat 0..9, a nonzero position above every accepted one and a
// tick the session has not run. Refusals queue nothing and consume nothing;
// gaps are legal (§7.4.4).
func TestEnqueueSeatCommandStampRules(t *testing.T) {
	f := newSeatFixture(t, true, false)
	stop := SeatCommand{Kind: SeatStop, Stop: StopPayload{Actors: []pool.UnitRef{f.ref(f.own1a)}}}
	next := f.s.Clock.GlobalTick + 1
	bad := []CommandStamp{
		{Seat: 10, Tick: next, Position: 5},
		{Seat: 1, Tick: next, Position: 0},
		{Seat: 1, Tick: f.s.Clock.GlobalTick, Position: 5},
		{Seat: 1, Tick: 0, Position: 5},
	}
	for _, stamp := range bad {
		if err := f.s.EnqueueSeatCommand(stamp, stop); err == nil {
			t.Fatalf("stamp %+v admitted", stamp)
		}
	}
	if err := f.s.EnqueueSeatCommand(CommandStamp{Seat: 1, Tick: next, Position: 5}, stop); err != nil {
		t.Fatal(err)
	}
	for _, stamp := range []CommandStamp{
		{Seat: 1, Tick: next, Position: 5}, // repeat
		{Seat: 1, Tick: next, Position: 4}, // backward
		{Seat: 0, Tick: next, Position: 3}, // backward in the one stream, another seat
	} {
		if err := f.s.EnqueueSeatCommand(stamp, stop); err == nil {
			t.Fatalf("stamp %+v admitted after position 5", stamp)
		}
	}
	// A gap, and a later tick, are legal.
	if err := f.s.EnqueueSeatCommand(CommandStamp{Seat: 0, Tick: next + 3, Position: 9}, SeatCommand{Kind: SeatStop}); err != nil {
		t.Fatal(err)
	}
	f.s.humanMu.Lock()
	queued := len(f.s.pendingHuman)
	f.s.humanMu.Unlock()
	if queued != 2 {
		t.Fatalf("queued %d entries, want the two accepted", queued)
	}
	if rs := f.tick(); len(rs) != 1 || rs[0].Stamp.Position != 5 || rs[0].Outcome != CommandApplied {
		t.Fatalf("first tick receipts %+v", rs)
	}
	if rs := f.tick(); len(rs) != 0 {
		t.Fatalf("the tick-%d entry applied early: %+v", next+3, rs)
	}
	if rs := f.s.DrainCommandReceipts(); rs != nil {
		t.Fatalf("drained receipts again: %+v", rs)
	}
}

// Variable payload storage is copied at enqueue: reusing the caller's slice
// cannot change the queued command (§7.4.4).
func TestEnqueueSeatCommandCopiesPayloadStorage(t *testing.T) {
	f := newSeatFixture(t, true, false)
	actors := []pool.UnitRef{f.ref(f.own1a)}
	stamp := f.stamp(1)
	if err := f.s.EnqueueSeatCommand(stamp, SeatCommand{Kind: SeatStop, Stop: StopPayload{Actors: actors}}); err != nil {
		t.Fatal(err)
	}
	actors[0] = f.ref(f.own1b)
	expectOutcome(t, f.tick()[0], CommandApplied)
	if f.queueLen(f.own1a) == 0 || f.queueLen(f.own1b) != 0 {
		t.Fatal("the queued command followed the caller's reused slice")
	}
}

// The stamped seat owns the actors and is the source of the mutation; the
// local own and viewing slots are neither read nor moved, and the local
// selection never reaches an explicit command (§7.2, M2-C2, M2-C7).
func TestOnlineSeatCommandActsForTheStampedSeat(t *testing.T) {
	f := newSeatFixture(t, true, false)
	f.s.Units.Unit(f.own0).Flags |= 0x10 // seat 0's local selection
	f.s.Units.Unit(f.own1b).Flags |= 0x10
	r := f.issue(t, 1, moveTo([]pool.UnitRef{f.ref(f.own1a)}, 400))
	expectOutcome(t, r, CommandApplied)
	if f.head(t, f.own1a).ID != orders.Lookup("Move_Ground") {
		t.Fatal("seat 1's actor did not take the move")
	}
	if f.queueLen(f.own0) != 0 || f.queueLen(f.own1b) != 0 {
		t.Fatal("a selected unit outside the explicit actors received the order")
	}
	stance := SeatCommand{Kind: SeatStance, Stance: StancePayload{Actors: []pool.UnitRef{f.ref(f.own1a)}, Value: 1}}
	f.def.MobileStandOrders = true
	defer func() { f.def.MobileStandOrders = false }()
	expectOutcome(t, f.issue(t, 1, stance), CommandApplied)
	if f.queueLen(f.own1b) != 0 || f.queueLen(f.own0) != 0 {
		t.Fatal("the stance broadcast fell back to a selection")
	}
	// An empty actor list never means the selection, online or in replay.
	expectOutcome(t, f.issue(t, 1, SeatCommand{Kind: SeatStop}), CommandNoOp)
	if f.queueLen(f.own1b) != 0 {
		t.Fatal("an empty online actor list fell back to the selection")
	}
	if f.s.LocalOwner != 0 || f.s.ViewingOwner != 0 || f.s.seatCommands.issuing {
		t.Fatal("application moved the local slots or left an issuer bound")
	}
	replay := newSeatFixture(t, false, false)
	replay.s.Units.Unit(replay.own0).Flags |= 0x10
	expectOutcome(t, replay.issue(t, 0, SeatCommand{Kind: SeatStop}), CommandNoOp)
	if replay.queueLen(replay.own0) != 0 {
		t.Fatal("an empty replay actor list fell back to the selection")
	}
}

// The Community kickout is a supported seat command that still needs its
// owning feature (§7.4.2 kind 34).
func TestOnlineKickoutNeedsTheCommunityFeature(t *testing.T) {
	f := newSeatFixture(t, true, false)
	kick := SeatCommand{Kind: SeatCommunityKickout, CommunityKickout: CommunityKickoutPayload{Unit: f.ref(f.own1a), Destination: CommandPoint{X: 300 << 16, Z: 64 << 16}}}
	f.s.bindOrderQueue(f.s.Units.Unit(f.own1a))
	f.s.Build.Rules = construction.StrictRules{}
	expectOutcome(t, f.issue(t, 1, kick), CommandRejected)
	f.s.Build.Rules = construction.CommunityRules{}
	f.s.Build.Community.ConstructionKickout = true
	expectOutcome(t, f.issue(t, 1, kick), CommandApplied)
	kick.CommunityKickout.Unit = f.ref(f.own0)
	expectOutcome(t, f.issue(t, 1, kick), CommandRejected)
}

// Watchers, computers, removed seats, seats outside the configuration and a
// human that has become a watcher issue nothing (M2-C2).
func TestOnlineSeatRolesAreAuthorized(t *testing.T) {
	f := newSeatFixture(t, true, true)
	for _, tc := range []struct {
		name  string
		seat  uint8
		actor pool.Handle
		setup func()
	}{
		{"computer", 2, f.comp2, nil},
		{"watcher row", 3, f.own1a, nil},
		{"outside the configuration", 4, f.own1a, nil},
		{"became a watcher", 1, f.own1a, func() { f.s.Econ.Players[1].Watcher = true }},
		{"removed", 1, f.own1a, func() {
			f.s.Econ.Players[1].Watcher = false
			f.s.markSeatRemovedForCommands(1)
		}},
	} {
		if tc.setup != nil {
			tc.setup()
		}
		before := f.streams()
		r := f.issue(t, tc.seat, SeatCommand{Kind: SeatStop, Stop: StopPayload{Actors: []pool.UnitRef{f.ref(tc.actor)}}})
		expectOutcome(t, r, CommandRejected)
		if f.queueLen(tc.actor) != 0 || f.streams() != before {
			t.Fatalf("%s: a refused command mutated state", tc.name)
		}
		atm := f.s.Econ.Players[tc.seat%10].Stock
		expectOutcome(t, f.issue(t, tc.seat, SeatCommand{Kind: SeatATM}), CommandRejected)
		if f.s.Econ.Players[tc.seat%10].Stock != atm {
			t.Fatalf("%s: a refused cheat credited stock", tc.name)
		}
	}
}

// A live foreign actor refuses the whole command before any friendly actor
// mutates; a target may belong to any seat (§7.4.3).
func TestOnlineForeignActorRejectsTheWholeCommand(t *testing.T) {
	f := newSeatFixture(t, true, false)
	for _, c := range []SeatCommand{
		{Kind: SeatStop, Stop: StopPayload{Actors: []pool.UnitRef{f.ref(f.own1a), f.ref(f.own0)}}},
		moveTo([]pool.UnitRef{f.ref(f.own0), f.ref(f.own1a)}, 300),
		{Kind: SeatActivation, Activation: ActivationPayload{Unit: f.ref(f.own0), Activate: true}},
		{Kind: SeatGroupAssign, GroupAssign: GroupAssignPayload{Group: 2, Members: []pool.UnitRef{f.ref(f.own1a), f.ref(f.comp2)}}},
	} {
		r := f.issue(t, 1, c)
		expectOutcome(t, r, CommandRejected)
		if f.queueLen(f.own1a) != 0 || f.queueLen(f.own0) != 0 || f.s.Units.Unit(f.own1a).Group != 0 {
			t.Fatalf("kind %d mutated a friendly actor before refusing a foreign one", c.Kind)
		}
	}
	attack := moveTo([]pool.UnitRef{f.ref(f.own1a)}, 0)
	attack.Order.Target = f.ref(f.own0)
	expectOutcome(t, f.issue(t, 1, attack), CommandApplied)
	if n := f.head(t, f.own1a); n.Target != f.own0 {
		t.Fatalf("an order to another seat's unit lost its target: %+v", n)
	}
}

// Kinds that wait for M5's perspectives, local interface kinds, local
// preferences, the lobby-only rule switch and unlisted numbers are refused
// online, before mutation, even with the cheat permission (§7.4.2, §16.2).
func TestOnlineUnavailableKindsAreRejected(t *testing.T) {
	f := newSeatFixture(t, true, true)
	m5 := []SeatCommand{
		{Kind: SeatMobileBuild, MobileBuild: MobileBuildPayload{Builder: f.ref(f.own1a), Product: "scout", Position: CommandPoint{X: 32 << 16, Z: 32 << 16}}},
		{Kind: SeatCommunityOrderDrag, CommunityOrderDrag: CommunityOrderDragPayload{Unit: f.ref(f.own1a)}},
		{Kind: SeatView, View: ViewPayload{Player: 0}},
		{Kind: SeatVisibility, Visibility: VisibilityPayload{ToggleMask: 1}},
		{Kind: SeatDoubleShot},
		{Kind: SeatHalfShot},
	}
	for k := SeatShareMetal; k <= SeatShootAll; k++ {
		m5 = append(m5, SeatCommand{Kind: k})
	}
	for _, c := range m5 {
		r := f.issue(t, 1, c)
		expectOutcome(t, r, CommandRejected)
		if !strings.Contains(r.Diagnostic, "M5") {
			t.Fatalf("kind %d diagnostic %q does not name M5", c.Kind, r.Diagnostic)
		}
	}
	for _, c := range []SeatCommand{
		{Kind: SeatSelectionReplace}, {Kind: SeatBuildPage}, {Kind: SeatGroupRecall}, {Kind: SeatBigBrother},
		{Kind: SeatNoShake}, {Kind: SeatSetLogo, SetLogo: SetLogoPayload{Player: 1, Logo: 3}},
		{Kind: SeatGameplay, Gameplay: GameplayPayload{Mode: gameplay.Strict31}}, {Kind: 0}, {Kind: 46}, {Kind: 200},
	} {
		expectOutcome(t, f.issue(t, 1, c), CommandRejected)
	}
	if f.queueLen(f.own1a) != 0 || f.s.ViewingOwner != 0 || f.s.NoShake() || f.s.Gameplay != gameplay.Modern || f.s.Econ.Players[1].Logo != 0 {
		t.Fatal("an unavailable kind fell through to a local applier")
	}
}

// Every cheat needs the agreed permission whatever the local developer state;
// with it, the cheat acts for the stamped seat (§7.1, §15 Q8).
func TestOnlineCheatsNeedTheRoomPermission(t *testing.T) {
	f := newSeatFixture(t, true, false)
	f.s.SetDeveloperDiagnostics(true)
	cheats := []SeatCommand{
		{Kind: SeatATM},
		{Kind: SeatSetResource, SetResource: SetResourcePayload{Resource: economy.Metal, Amount: 5}},
		{Kind: SeatMakeSelectable},
		{Kind: SeatMeteor},
		{Kind: SeatSpawn, Spawn: SpawnPayload{Unit: "scout", Position: CommandPoint{X: 32 << 16, Z: 32 << 16}}},
	}
	stocks, before, live, meteor, flags := f.s.Econ.Players, f.streams(), f.s.Units.Used(), f.s.Meteor, f.s.Units.Unit(f.own1a).Flags
	for _, c := range cheats {
		expectOutcome(t, f.issue(t, 1, c), CommandRejected)
	}
	if f.s.Econ.Players != stocks || f.streams() != before || f.s.Units.Used() != live || f.s.Meteor != meteor ||
		f.s.Units.Unit(f.own1a).Flags != flags {
		t.Fatal("a cheat without the permission changed the battle")
	}

	f = newSeatFixture(t, true, true)
	expectOutcome(t, f.issue(t, 1, SeatCommand{Kind: SeatATM}), CommandApplied)
	if f.s.Econ.Players[1].Stock != [2]float32{2000, 2000} || f.s.Econ.Players[0].Stock != [2]float32{1000, 1000} {
		t.Fatalf("ATM credited %v / %v, want the issuing seat only", f.s.Econ.Players[1].Stock, f.s.Econ.Players[0].Stock)
	}
	expectOutcome(t, f.issue(t, 1, SeatCommand{Kind: SeatSetResource, SetResource: SetResourcePayload{Resource: economy.Energy, Amount: 7}}), CommandApplied)
	if f.s.Econ.Players[1].Stock[economy.Energy] != 7 || f.s.Econ.Players[0].Stock[economy.Energy] != 1000 {
		t.Fatal("SetResource did not write the issuing seat's own stock")
	}
	expectOutcome(t, f.issue(t, 1, SeatCommand{Kind: SeatSetResource, SetResource: SetResourcePayload{Player: 2, Resource: economy.Energy, Amount: 7}}), CommandRejected)
	live = f.s.Units.Used()
	expectOutcome(t, f.issue(t, 1, SeatCommand{Kind: SeatSpawn, Spawn: SpawnPayload{Unit: "scout", Position: CommandPoint{X: 32 << 16, Z: 32 << 16}}}), CommandApplied)
	if f.s.Units.Used() != live+1 || f.s.Units.LiveCountForPlayer(1) != 3 {
		t.Fatal("Spawn did not create a unit for the issuing seat")
	}
}

// Online Give: without the permission only a positive whole amount up to 2^31
// is ordinary sharing; every other amount is refused before any stock or
// production slot changes. With it, any whole amount from -2^31 to 2^31 is
// the signed retail transfer; an amount no producer generates stays refused
// (M2-C4; §7.1, §7.4.1, §15 Q8, Q28).
func TestOnlineGiveAmountPolicy(t *testing.T) {
	give := func(amount float32) SeatCommand {
		return SeatCommand{Kind: SeatGive, Give: GivePayload{Player: 0, Resource: economy.Metal, Amount: amount}}
	}
	negZero := math.Float32frombits(0x80000000)
	nan := float32(math.NaN())
	inf := float32(math.Inf(1))
	f := newSeatFixture(t, true, false)
	stocks := f.s.Econ.Players
	for _, a := range []float32{-20, 0, negZero, 0.5, 20.25, nan, inf, -inf, 4294967296, -2147483648} {
		expectOutcome(t, f.issue(t, 1, give(a)), CommandRejected)
	}
	if f.s.Econ.Players != stocks {
		t.Fatal("a refused gift changed a stock or a production slot")
	}
	expectOutcome(t, f.issue(t, 1, give(20)), CommandApplied)
	if f.s.Econ.Players[1].Stock[economy.Metal] != 980 || f.s.Econ.Players[0].Stock[economy.Metal] != 1000 ||
		f.s.Econ.Players[0].Mirror[economy.Metal].Production != 20 {
		t.Fatal("a positive whole gift to another seat did not apply the sharing transfer from the issuing seat")
	}
	expectOutcome(t, f.issue(t, 1, give(2147483648)), CommandApplied)

	f = newSeatFixture(t, true, true)
	expectOutcome(t, f.issue(t, 1, give(-20)), CommandApplied)
	if f.s.Econ.Players[1].Stock[economy.Metal] != 1020 || f.s.Econ.Players[0].Mirror[economy.Metal].Production != -20 {
		t.Fatal("a permitted negative gift did not apply the signed transfer")
	}
	expectOutcome(t, f.issue(t, 1, give(0)), CommandApplied)
	expectOutcome(t, f.issue(t, 1, give(-2147483648)), CommandApplied)
	for _, a := range []float32{0.5, nan, inf, negZero, 4294967296} {
		expectOutcome(t, f.issue(t, 1, give(a)), CommandRejected)
	}
}

// A serial mismatch is a stale actor, never the slot's new occupant; a stale
// singular actor or an entirely stale list is a no-op (M2-C3, §7.4.3).
func TestStaleReferencesNeverReachTheSlotsNewOccupant(t *testing.T) {
	f := newSeatFixture(t, true, false)
	old := f.ref(f.own1b)
	f.s.Units.FreeImmediate(f.own1b)
	h, err := f.s.Units.Create(f.def, 1, 200<<16, 0, 64<<16)
	if err != nil || h != old.Handle {
		t.Fatalf("slot reuse = %d, %v; want handle %d", h, err, old.Handle)
	}
	expectOutcome(t, f.issue(t, 1, SeatCommand{Kind: SeatStop, Stop: StopPayload{Actors: []pool.UnitRef{old}}}), CommandNoOp)
	expectOutcome(t, f.issue(t, 1, SeatCommand{Kind: SeatActivation, Activation: ActivationPayload{Unit: old, Activate: true}}), CommandNoOp)
	expectOutcome(t, f.issue(t, 1, SeatCommand{Kind: SeatStop}), CommandNoOp)
	expectOutcome(t, f.issue(t, 1, SeatCommand{Kind: SeatStop, Stop: StopPayload{Actors: []pool.UnitRef{old, f.ref(f.own1a)}}}), CommandApplied)
	if f.queueLen(h) != 0 || f.queueLen(f.own1a) == 0 {
		t.Fatal("a stale reference reached the new occupant or blocked a live actor")
	}

	// The local adapter captures explicit handles at submission, so a slot
	// reused before phase 1 is not redirected either.
	local := newSeatFixture(t, false, false)
	if err := local.s.EnqueueHumanCommand(HumanCommand{Kind: HumanStop, Stop: HumanStopCommand{Handles: []pool.Handle{local.own0}}}); err != nil {
		t.Fatal(err)
	}
	local.s.Units.FreeImmediate(local.own0)
	if h, err := local.s.Units.Create(local.def, 0, 0, 0, 0); err != nil || h != local.own0 {
		t.Fatalf("local slot reuse = %d, %v", h, err)
	}
	local.tick()
	if local.queueLen(local.own0) != 0 {
		t.Fatal("the local adapter redirected a captured handle to the slot's new occupant")
	}
}

// Online, a stale ordinary target makes the whole order a no-op and never a
// ground click. The local adapter and the single-player replay context keep
// today's single-player result: a ground order at the captured point whose
// node still carries the dead handle (§7.4.3, M2-C7).
func TestStaleOrdinaryTargetOnlineAndInSinglePlayer(t *testing.T) {
	order := func(f *seatFixture, actor pool.Handle, target pool.UnitRef) SeatCommand {
		c := moveTo([]pool.UnitRef{f.ref(actor)}, 300)
		c.Order.Target = target
		return c
	}
	online := newSeatFixture(t, true, false)
	dead := online.ref(online.own0)
	online.s.Units.FreeImmediate(online.own0)
	expectOutcome(t, online.issue(t, 1, order(online, online.own1a, dead)), CommandNoOp)
	if online.queueLen(online.own1a) != 0 {
		t.Fatal("a stale online target turned into a ground order")
	}

	replay := newSeatFixture(t, false, false)
	target := replay.ref(replay.own1a)
	replay.s.Units.FreeImmediate(replay.own1a)
	expectOutcome(t, replay.issue(t, 0, order(replay, replay.own0, target)), CommandApplied)
	n := replay.head(t, replay.own0)
	if n.ID != orders.Lookup("Move_Ground") || n.GoalX != 300<<16 {
		t.Fatalf("replay stale-target node %+v, want the single-player ground order at the captured point", n)
	}

	local := newSeatFixture(t, false, false)
	h := local.own1a
	local.s.Units.FreeImmediate(h)
	if err := local.s.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{
		Handles: []pool.Handle{local.own0}, Code: 2, Target: h, Position: orders.ResolvePos{X: 300 << 16, Z: 64 << 16}}}); err != nil {
		t.Fatal(err)
	}
	local.tick()
	if got := local.head(t, local.own0); got.ID != n.ID || got.Target != n.Target || got.GoalX != n.GoalX || got.GoalZ != n.GoalZ {
		t.Fatalf("local adapter node %+v differs from the replay node %+v", got, n)
	}
}

// An area order drops only stale explicit-target entries, keeps targetless
// feature entries in their captured order, and keeps its actors' captured
// order (§7.4.3).
func TestAreaOrderDropsOnlyStaleTargetEntries(t *testing.T) {
	f := newSeatFixture(t, true, false)
	stale := f.ref(f.own0)
	f.s.Units.FreeImmediate(f.own0)
	feature := func(x int32) CommandTarget {
		return CommandTarget{Position: CommandPosition{X: numeric.Fixed(x) << 16, Z: 32 << 16, HasFeature: true}}
	}
	c := SeatCommand{Kind: SeatOrder, Order: OrderPayload{
		Actors:  []pool.UnitRef{f.ref(f.own1b), f.ref(f.own1a)}, // area actors keep their captured order
		Code:    12,
		Targets: []CommandTarget{feature(32), {Target: stale, Position: CommandPosition{X: 40 << 16}}, feature(48)},
	}}
	expectOutcome(t, f.issue(t, 1, c), CommandApplied)
	for _, h := range []pool.Handle{f.own1a, f.own1b} {
		got := orders.QueueOfUnit(f.s.Units.Unit(h)).Primary()
		if len(got) != 2 || got[0].GoalX != 32<<16 || got[1].GoalX != 48<<16 || got[0].ID != orders.Lookup("Reclaim") {
			t.Fatalf("actor %d queue %+v, want the two feature entries in order", h, got)
		}
	}
}

// Group assignment carries the group's complete membership: stale members
// drop out, the empty set clears the group among the issuer's units, and no
// other seat's group numbers change (§7.1, §7.4.3).
func TestGroupAssignReplacesMembership(t *testing.T) {
	f := newSeatFixture(t, true, false)
	f.s.Units.Unit(f.own0).Group = 3
	assign := func(members ...pool.UnitRef) SeatCommand {
		return SeatCommand{Kind: SeatGroupAssign, GroupAssign: GroupAssignPayload{Group: 3, Members: members}}
	}
	expectOutcome(t, f.issue(t, 1, assign(f.ref(f.own1a))), CommandApplied)
	if f.s.Units.Unit(f.own1a).Group != 3 || f.s.Units.Unit(f.own1b).Group != 0 {
		t.Fatal("assignment did not apply the explicit membership")
	}
	expectOutcome(t, f.issue(t, 1, assign(f.ref(f.own1b))), CommandApplied)
	if f.s.Units.Unit(f.own1a).Group != 0 || f.s.Units.Unit(f.own1b).Group != 3 {
		t.Fatal("assignment did not clear the group on a non-member")
	}
	expectOutcome(t, f.issue(t, 1, assign()), CommandApplied)
	if f.s.Units.Unit(f.own1b).Group != 0 || f.s.Units.Unit(f.own0).Group != 3 {
		t.Fatal("the empty membership did not clear the issuer's group, or touched another seat's")
	}
	if f.s.Units.Unit(f.own1a).Flags&0x10 != 0 {
		t.Fatal("assignment wrote the selection bit")
	}
}

// Schema refusals: list shapes, enumerations, flag combinations, the
// required zeros, counts, keys, coordinates and the online work limits. Each
// leaves queues and both random streams where they were (M2-C4).
func TestSeatCommandSchemaRefusals(t *testing.T) {
	f := newSeatFixture(t, true, true)
	a, b := f.ref(f.own1a), f.ref(f.own1b)
	synthetic := func(n int) []pool.UnitRef {
		out := make([]pool.UnitRef, n)
		for i := range out {
			out[i] = pool.UnitRef{Handle: pool.Handle(1000 + i), Serial: 1}
		}
		return out
	}
	features := func(n int) []CommandTarget {
		return make([]CommandTarget, n)
	}
	ordinary := func(mut func(*OrderPayload)) SeatCommand {
		c := moveTo([]pool.UnitRef{a}, 300)
		mut(&c.Order)
		return c
	}
	cases := []struct {
		name string
		c    SeatCommand
	}{
		{"unsorted ordinary actors", moveTo([]pool.UnitRef{b, a}, 300)},
		{"duplicate actors", SeatCommand{Kind: SeatStop, Stop: StopPayload{Actors: []pool.UnitRef{a, a}}}},
		{"repeated handle", SeatCommand{Kind: SeatStop, Stop: StopPayload{Actors: []pool.UnitRef{a, {Handle: a.Handle, Serial: a.Serial + 9}}}}},
		{"null actor", SeatCommand{Kind: SeatStop, Stop: StopPayload{Actors: []pool.UnitRef{{}}}}},
		{"half-null actor", SeatCommand{Kind: SeatCancelProduction, CancelProduction: CancelProductionPayload{Unit: pool.UnitRef{Handle: a.Handle}}}},
		{"half-null target", ordinary(func(o *OrderPayload) { o.Target = pool.UnitRef{Serial: 3} })},
		{"code 0", ordinary(func(o *OrderPayload) { o.Code = 0 })},
		{"code 15", ordinary(func(o *OrderPayload) { o.Code = 15 })},
		{"interface type 2", ordinary(func(o *OrderPayload) { o.Position.InterfaceType = 2 })},
		{"coordinate beyond 32 bits", ordinary(func(o *OrderPayload) { o.Position.X = math.MaxInt32 + 1 })},
		{"assigned with two actors", ordinary(func(o *OrderPayload) { o.Actors = []pool.UnitRef{a, b}; o.AssignedPosition = true })},
		{"tracked and unqueued", ordinary(func(o *OrderPayload) { o.TrackQueuedMove = true })},
		{"area with outer target", ordinary(func(o *OrderPayload) { o.Targets = features(1); o.Target = b })},
		{"area over 10000 entries", ordinary(func(o *OrderPayload) { o.Targets = features(10001) })},
		{"area over 2^20 visits", ordinary(func(o *OrderPayload) { o.Actors = synthetic(105); o.Targets = features(10000) })},
		{"actors over the agreed unit limit", SeatCommand{Kind: SeatStop, Stop: StopPayload{Actors: synthetic(1001)}}},
		{"unselected record", SeatCommand{Kind: SeatStop, Stop: StopPayload{Actors: []pool.UnitRef{a}}, Give: GivePayload{Amount: 1}}},
		{"stance value 3", SeatCommand{Kind: SeatStance, Stance: StancePayload{Actors: []pool.UnitRef{a}, Value: 3}}},
		{"group 0", SeatCommand{Kind: SeatGroupAssign, GroupAssign: GroupAssignPayload{Members: []pool.UnitRef{a}}}},
		{"group 10", SeatCommand{Kind: SeatGroupAssign, GroupAssign: GroupAssignPayload{Group: 10, Members: []pool.UnitRef{a}}}},
		{"count 0", SeatCommand{Kind: SeatFactoryBuild, FactoryBuild: FactoryBuildPayload{Builder: a, Product: "scout"}}},
		{"count 32768", SeatCommand{Kind: SeatStockpile, Stockpile: StockpilePayload{Unit: a, Count: 32768}}},
		{"noncanonical key", SeatCommand{Kind: SeatFactoryBuild, FactoryBuild: FactoryBuildPayload{Builder: a, Product: "Scout", Count: 1}}},
		{"missing key", SeatCommand{Kind: SeatFactoryBuild, FactoryBuild: FactoryBuildPayload{Builder: a, Product: "corkrog", Count: 1}}},
		{"sequence 0", SeatCommand{Kind: SeatCancelQueuedMove, CancelQueuedMove: CancelQueuedMovePayload{Actors: []pool.UnitRef{a}}}},
		{"meteor enabled without argument", SeatCommand{Kind: SeatMeteor, Meteor: MeteorPayload{Enabled: true}}},
		{"builder options owner online", SeatCommand{Kind: SeatBuilderOptions, BuilderOptions: BuilderOptionsPayload{Owner: 1}}},
		{"builder option value 3", SeatCommand{Kind: SeatBuilderOptions, BuilderOptions: BuilderOptionsPayload{Guard: [3]uint8{3}}}},
		{"resource 2", SeatCommand{Kind: SeatGive, Give: GivePayload{Resource: 2, Amount: 1}}},
		{"recipient 10", SeatCommand{Kind: SeatGive, Give: GivePayload{Player: 10, Amount: 1}}},
		{"spawn beyond 32 bits", SeatCommand{Kind: SeatSpawn, Spawn: SpawnPayload{Unit: "scout", Position: CommandPoint{Y: math.MinInt32 - 1}}}},
	}
	for _, tc := range cases {
		before := f.streams()
		r := f.issue(t, 1, tc.c)
		if r.Outcome != CommandRejected {
			t.Fatalf("%s: outcome %d, want rejected", tc.name, r.Outcome)
		}
		if f.queueLen(f.own1a) != 0 || f.queueLen(f.own1b) != 0 || f.streams() != before {
			t.Fatalf("%s: a refused command mutated state", tc.name)
		}
	}
	// The limits themselves are admitted.
	at := ordinary(func(o *OrderPayload) { o.Code = 12; o.Targets = features(10000) })
	expectOutcome(t, f.issue(t, 1, at), CommandApplied)
}

// A tracked move records its accepted stream position; cancellation names
// that position, after other seats' entries, and an unacknowledged local
// sequence never matches (§7.2, §7.4.4, M2-C3).
func TestTrackedMovesUseTheStreamPosition(t *testing.T) {
	f := newSeatFixture(t, true, false)
	tracked := func(h pool.Handle) SeatCommand {
		c := moveTo([]pool.UnitRef{f.ref(h)}, 500)
		c.Order.Queued, c.Order.TrackQueuedMove = true, true
		return c
	}
	s0, s1 := f.stamp(0), f.stamp(1)
	if err := f.s.EnqueueSeatCommand(s0, tracked(f.own0)); err != nil {
		t.Fatal(err)
	}
	if err := f.s.EnqueueSeatCommand(s1, tracked(f.own1a)); err != nil {
		t.Fatal(err)
	}
	if rs := f.tick(); len(rs) != 2 || rs[0].Stamp != s0 || rs[1].Stamp != s1 {
		t.Fatalf("same-tick receipts %+v, want stream order", rs)
	}
	if f.head(t, f.own0).HumanMoveSequence != s0.Position || f.head(t, f.own1a).HumanMoveSequence != s1.Position {
		t.Fatal("tracked moves did not record their stream positions")
	}
	cancel := func(sequence uint64, h pool.Handle) SeatCommand {
		return SeatCommand{Kind: SeatCancelQueuedMove, CancelQueuedMove: CancelQueuedMovePayload{Sequence: sequence, Actors: []pool.UnitRef{f.ref(h)}}}
	}
	expectOutcome(t, f.issue(t, 0, cancel(1, f.own0)), CommandApplied)           // a local pending sequence
	expectOutcome(t, f.issue(t, 0, cancel(s1.Position, f.own0)), CommandApplied) // another seat's receipt
	if f.queueLen(f.own0) != 1 {
		t.Fatal("a cancellation removed a move it did not name")
	}
	expectOutcome(t, f.issue(t, 0, cancel(s1.Position, f.own1a)), CommandRejected)
	expectOutcome(t, f.issue(t, 0, cancel(s0.Position, f.own0)), CommandApplied)
	if f.queueLen(f.own0) != 0 || f.queueLen(f.own1a) != 1 {
		t.Fatal("the named receipt did not cancel exactly its own move")
	}
}

// In an online session the local adapter admits nothing: every world command
// must arrive stamped (M2-C2), and the local interface kinds are the client's
// own state, which never reaches the session (§7.3).
func TestLocalAdapterRefusesWorldCommandsOnline(t *testing.T) {
	f := newSeatFixture(t, true, true)
	for _, c := range []HumanCommand{
		{Kind: HumanATM}, {Kind: HumanNoShake}, {Kind: HumanGameplay, Gameplay: gameplay.Strict31},
		{Kind: HumanStop, Stop: HumanStopCommand{Handles: []pool.Handle{f.own0}}},
		{Kind: HumanGive, Give: HumanGiveCommand{Player: 1, Amount: 5}},
		{Kind: HumanSelectionReplace, Selection: HumanSelectionCommand{Handles: []pool.Handle{f.own0}}},
	} {
		if err := f.s.EnqueueHumanCommand(c); err == nil {
			t.Fatalf("kind %d admitted through the local adapter online", c.Kind)
		}
	}
	if !f.s.OnlineCommandContext() {
		t.Fatal("the online fixture does not report the online command context")
	}
}

// The single-player replay context acts for the local seat only, keeps the
// single-player Give source and signed amounts, applies the replay-only
// kinds, and refuses local interface kinds.
func TestSinglePlayerReplayContext(t *testing.T) {
	f := newSeatFixture(t, false, false)
	expectOutcome(t, f.issue(t, 1, SeatCommand{Kind: SeatStop, Stop: StopPayload{Actors: []pool.UnitRef{f.ref(f.own1a)}}}), CommandRejected)
	expectOutcome(t, f.issue(t, 0, SeatCommand{Kind: SeatGive, Give: GivePayload{Player: 1, Resource: economy.Metal, Amount: -5}}), CommandApplied)
	if f.s.Econ.Players[0].Stock[economy.Metal] != 1005 {
		t.Fatal("replay Give did not keep the signed transfer from the own slot")
	}
	expectOutcome(t, f.issue(t, 0, SeatCommand{Kind: SeatView, View: ViewPayload{Player: 1}}), CommandApplied)
	if f.s.ViewingOwner != 1 || f.s.LocalOwner != 0 {
		t.Fatal("replay View did not move only the viewing slot")
	}
	expectOutcome(t, f.issue(t, 0, SeatCommand{Kind: SeatNoShake}), CommandApplied)
	if !f.s.NoShake() {
		t.Fatal("replay NoShake did not toggle")
	}
	expectOutcome(t, f.issue(t, 0, SeatCommand{Kind: SeatSetResource, SetResource: SetResourcePayload{Player: 1, Resource: economy.Energy, Amount: 3}}), CommandApplied)
	if f.s.Econ.Players[1].Stock[economy.Energy] != 3 {
		t.Fatal("replay SetResource did not keep its player field")
	}
	expectOutcome(t, f.issue(t, 0, SeatCommand{Kind: SeatSelectionReplace}), CommandRejected)
	expectOutcome(t, f.issue(t, 0, SeatCommand{Kind: SeatShareMetal}), CommandRejected)
	expectOutcome(t, f.issue(t, 0, SeatCommand{Kind: SeatStockpile, Stockpile: StockpilePayload{Unit: f.ref(f.own0), Count: math.MinInt32}}), CommandRejected)
}

// The local adapter and a stamped single-player command reach one payload
// implementation: the same script leaves the same queues (§7.4.4). The replay
// recorder's converter turns each local command into exactly the stamped one
// (replay_convert.go); TestReplayCommandsApplyAsTheLocalCommands covers every
// replay kind on a composed battle.
func TestLocalAndStampedCommandsShareOneImplementation(t *testing.T) {
	local, stamped := newSeatFixture(t, false, false), newSeatFixture(t, false, false)
	var extra pool.Handle
	for _, f := range []*seatFixture{local, stamped} {
		h, err := f.s.Units.Create(f.def, 0, 300<<16, 0, 64<<16)
		if err != nil || (extra != 0 && h != extra) {
			t.Fatalf("second local unit = %d, %v", h, err)
		}
		extra = h
	}
	both := []pool.Handle{local.own0, extra}
	script := []struct {
		human HumanCommand
		seat  func(f *seatFixture) SeatCommand
	}{
		{HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{extra, local.own0}, Code: 2, Position: orders.ResolvePos{X: 800 << 16, Z: 64 << 16}}},
			func(f *seatFixture) SeatCommand {
				return moveTo([]pool.UnitRef{f.ref(f.own0), f.ref(extra)}, 800)
			}},
		{HumanCommand{Kind: HumanActivation, Activation: HumanActivationCommand{Unit: local.own0, Activate: true, Queued: true}},
			func(f *seatFixture) SeatCommand {
				return SeatCommand{Kind: SeatActivation, Activation: ActivationPayload{Unit: f.ref(f.own0), Activate: true, Queued: true}}
			}},
		{HumanCommand{Kind: HumanSelfDestruct, SelfDestruct: HumanSelfDestructCommand{Handles: both, Queued: true}},
			func(f *seatFixture) SeatCommand {
				return SeatCommand{Kind: SeatSelfDestruct, SelfDestruct: SelfDestructPayload{Actors: []pool.UnitRef{f.ref(f.own0), f.ref(extra)}, Queued: true}}
			}},
		{HumanCommand{Kind: HumanStop, Stop: HumanStopCommand{Handles: []pool.Handle{extra}}},
			func(f *seatFixture) SeatCommand {
				return SeatCommand{Kind: SeatStop, Stop: StopPayload{Actors: []pool.UnitRef{f.ref(extra)}}}
			}},
	}
	for _, step := range script {
		if err := local.s.EnqueueHumanCommand(step.human); err != nil {
			t.Fatal(err)
		}
		pending := local.s.PendingHumanCommands()
		converted, err := local.s.replayCommand(pending[len(pending)-1])
		if want := step.seat(stamped); err != nil || !reflect.DeepEqual(converted, want) {
			t.Fatalf("kind %d converts to %+v (%v), want %+v", step.human.Kind, converted, err, want)
		}
		local.tick()
		expectOutcome(t, stamped.issue(t, 0, step.seat(stamped)), CommandApplied)
	}
	for _, h := range both {
		lq, sq := orders.QueueOfUnit(local.s.Units.Unit(h)).Primary(), orders.QueueOfUnit(stamped.s.Units.Unit(h)).Primary()
		if len(lq) != len(sq) {
			t.Fatalf("unit %d queue lengths %d / %d", h, len(lq), len(sq))
		}
		for i := range lq {
			l, s := lq[i], sq[i]
			if l.ID != s.ID || l.Target != s.Target || l.GoalX != s.GoalX || l.GoalZ != s.GoalZ || l.CreationTick != s.CreationTick || l.Flags != s.Flags {
				t.Fatalf("unit %d node %d: local %+v, stamped %+v", h, i, l, s)
			}
		}
	}
	if *local.s.SimRNG() != *stamped.s.SimRNG() || *local.s.CrtRNG() != *stamped.s.CrtRNG() {
		t.Fatal("the two adapters left different random-stream positions")
	}
}

// The paused-input boundary belongs to the local adapter: it stops at a
// stamped entry and leaves it, and everything behind it, for phase 1.
func TestPausedBoundaryLeavesStampedEntries(t *testing.T) {
	f := newSeatFixture(t, false, false)
	tick := f.s.Clock.GlobalTick + 1
	_ = f.s.EnqueueHumanCommand(HumanCommand{Kind: HumanView, View: HumanViewCommand{Player: 1}})
	if err := f.s.EnqueueSeatCommand(f.stamp(0), SeatCommand{Kind: SeatStop, Stop: StopPayload{Actors: []pool.UnitRef{f.ref(f.own0)}}}); err != nil {
		t.Fatal(err)
	}
	_ = f.s.EnqueueHumanCommand(HumanCommand{Kind: HumanView, View: HumanViewCommand{Player: 0}})
	if n := f.s.applyPausedHumanCommands(tick, nil); n != 1 {
		t.Fatalf("paused boundary applied %d entries, want the local prefix of 1", n)
	}
	if f.queueLen(f.own0) != 0 || f.s.ViewingOwner != 1 || len(f.s.DrainCommandReceipts()) != 0 {
		t.Fatal("the paused boundary applied a stamped entry")
	}
	if rs := f.tick(); len(rs) != 1 || rs[0].Outcome != CommandApplied {
		t.Fatalf("phase 1 receipts %+v", rs)
	}
	if f.s.ViewingOwner != 0 {
		t.Fatal("the entry behind the stamped one did not apply in order")
	}
}
