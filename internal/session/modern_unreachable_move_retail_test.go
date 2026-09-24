//go:build retail

package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Nanolathe Modern policy: docs/DESIGN_MOVEMENT_PATH.md "Modern unreachable
// moves". The fixture is authored here — flat terrain, a void wall and a
// retail wreck closing its only gate — and says nothing about retail beyond
// the Strict retry it locks [04 R-PATH-01 §7][04 R-ORD-01 §4].

const (
	unreachTicks  = 1500
	unreachGoalX  = 52
	unreachGoalZ  = 24
	unreachWreck  = "armwin_dead"
	unreachWreckX = 30
	unreachWreckZ = 22
)

// immediateUnreachableRules is Modern with a zero dwell: the move completes
// at its first certification. Composed only here, as the reference that
// dates the first certificate for the dwell and reopening checks.
type immediateUnreachableRules struct{ movement.ModernRules }

func (*immediateUnreachableRules) UnreachableMoves(s *movement.System) (int32, uint32) {
	frontier, _ := (&movement.ModernRules{}).UnreachableMoves(s)
	return frontier, 0
}

func unreachCell(c int32) numeric.Fixed { return world.CellToWorld(c) + numeric.FixedFromInt(8) }

// unreachScene composes a 64x48 flat battle: a void wall across x=[30,33)
// leaving one gate at z=[22,26), which an ARMWIN wreck anchored at (30,22)
// closes, and one ARMFLEA at (8,14) ordered to (52,24) beyond the wall.
func unreachScene(t *testing.T, rules string, set *RuleSet) (*Session, *units.Unit) {
	t.Helper()
	cat, fs := retailcat.Shared(t)
	const w, h = 64, 48
	attrs := make([]formats.TNTAttribute, w*h)
	for i := range attrs {
		attrs[i] = formats.TNTAttribute{Height: 20, Feature: world.PlotFeatureNone}
	}
	ter := &world.Terrain{CellW: w, CellH: h, Plot: world.ExpandPlot(attrs, w, h), Version: world.VersionCanonical, WindMin: 100, WindMax: 200}
	if err := ter.ApplySchema(nil, 0); err != nil {
		t.Fatal(err)
	}
	for z := int32(0); z < h; z++ {
		if z >= 22 && z < 26 {
			continue
		}
		for x := int32(30); x < 33; x++ {
			ter.PlotAt(x, z).SetFeature(world.PlotFeatureVoid)
		}
	}
	base, ok := LookupRuleSet(rules)
	if !ok {
		t.Fatalf("unknown rule set %q", rules)
	}
	s := &Session{Gameplay: base.Base, Catalog: cat, World: ter, Mission: syntheticMission(), Clock: &clock.State{Requested: 10, Active: 10}, Snapshot: &frame.Buffer{}}
	if err := s.SetRules(rules); err != nil {
		t.Fatal(err)
	}
	s.EntryCommunity = s.Community
	var err error
	if s.Units, err = newBattleSlicedWorldWithCOBSized(cat, fs, 0, [pool.PlayerCount]uint32{}, 64); err != nil {
		t.Fatal(err)
	}
	s.Econ = economyForTest()
	for i := range s.Econ.Players {
		p := &s.Econ.Players[i]
		p.Exists, p.ControllerState, p.EndGameCountdown = true, 2, -1
		if i == 0 {
			p.ControllerState = 1
		}
	}
	s.Econ.SeedDeadlines(0)
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatal(err)
	}
	s.RegisterAll()
	s.State = StateBattle
	if err := s.SetRules(rules); err != nil {
		t.Fatal(err)
	}
	if set != nil {
		s.BindRules(*set)
	}
	s.SeedSessionRNG(7, 11)
	wreck := cat.Features[unreachWreck]
	if wreck == nil || !wreck.Blocking {
		t.Skipf("retail feature %s is absent", unreachWreck)
	}
	if s.Features.PlaceAt(unreachWreckX, unreachWreckZ, wreck) == nil {
		t.Fatal("wreck placement refused")
	}
	flea := placeCompleteRetailUnit(t, s, "armflea", 0, unreachCell(8), unreachCell(14))
	s.Movement.EnsureUnit(flea)
	gx, gz := unreachCell(unreachGoalX), unreachCell(unreachGoalZ)
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{
		Handles: []pool.Handle{flea.Handle}, Code: 2,
		Position: orders.ResolvePos{X: gx, Y: ter.HeightAt(gx, gz), Z: gz, InterfaceType: orders.InterfaceTypeRightClick},
	}}); err != nil {
		t.Fatal(err)
	}
	return s, flea
}

// unreachRun steps the scene, calling event before the tick at (never when
// zero), and returns the tick the flea's move left its queue (zero if it
// never did) and the flea's final cell.
func unreachRun(t *testing.T, rules string, set *RuleSet, at int, event func(*testing.T, *Session)) (done int, x, z int32) {
	t.Helper()
	s, flea := unreachScene(t, rules, set)
	move := orders.Lookup("Move_Ground")
	for tick := 1; tick <= unreachTicks; tick++ {
		if tick == at {
			event(t, s)
		}
		s.stepAuthoritativePhases(s.Clock.BeginSubTick())
		if !flea.Alive {
			t.Fatal("the flea died")
		}
		q := orders.QueueOfUnit(flea)
		if tick > 2 && done == 0 && (q == nil || q.Head() == nil || q.Head().ID != move) {
			done = tick
		}
	}
	return done, world.WorldToCell(flea.X), world.WorldToCell(flea.Z)
}

// unreachReopen reclaims the wreck that closes the gate.
func unreachReopen(t *testing.T, s *Session) {
	if _, _, ok := s.Features.ReclaimAt(unreachWreckX, unreachWreckZ); !ok {
		t.Fatal("wreck reclaim refused")
	}
}

func unreachNearGoal(x, z int32) bool {
	dx, dz := x-unreachGoalX, z-unreachGoalZ
	return max(dx, -dx, dz, -dz) <= 2
}

// Strict 3.1 and Community 3.9 keep retail's endless retry at a sealed goal;
// Modern finishes the move at the wall once the 90-tick dwell from the first
// certificate has passed, and a goal that reopens during the dwell is reached
// by the ordinary retry instead.
func TestModernUnreachableMoveFinishesAfterDwell(t *testing.T) {
	for _, rules := range []string{StrictRuleSetName, CommunityRuleSetName} {
		if done, x, z := unreachRun(t, rules, nil, 0, nil); done != 0 || unreachNearGoal(x, z) {
			t.Fatalf("%s: move left the queue at tick %d at cell (%d,%d); retail retries for ever", rules, done, x, z)
		}
	}

	immediate := ModernRuleSet()
	immediate.Movement = &immediateUnreachableRules{}
	certified, cx, cz := unreachRun(t, ModernRuleSetName, &immediate, 0, nil)
	if certified == 0 || unreachNearGoal(cx, cz) || cx > 30 {
		t.Fatalf("zero-dwell reference: done at tick %d at cell (%d,%d); want a certificate at the wall", certified, cx, cz)
	}

	// The reference leaves the queue on the follower visit after the
	// publication that certified it, so that publication was at tick
	// certified-1 or earlier; the dwell counts 90 ticks from it.
	done, x, z := unreachRun(t, ModernRuleSetName, nil, 0, nil)
	if done == 0 || done < certified-1+90 {
		t.Fatalf("Modern finished at tick %d; want at least 90 ticks after the first certificate before %d", done, certified)
	}
	if unreachNearGoal(x, z) || x > 30 {
		t.Fatalf("Modern finished at cell (%d,%d); want the frontier west of the wall", x, z)
	}

	reopened, rx, rz := unreachRun(t, ModernRuleSetName, nil, certified+30, unreachReopen)
	if !unreachNearGoal(rx, rz) {
		t.Fatalf("a goal reopened during the dwell was not reached: move done at tick %d, flea at (%d,%d)", reopened, rx, rz)
	}
	t.Logf("first certificate %d; Modern done %d at (%d,%d); reopened at %d, done %d at (%d,%d)", certified, done, x, z, certified+30, reopened, rx, rz)

	// A switch to Strict during the dwell drops the certificate at the next
	// follower visit; the order keeps retail's retry from then on.
	switched, sx, sz := unreachRun(t, ModernRuleSetName, nil, certified+30, func(_ *testing.T, s *Session) { s.SetGameplay(gameplay.Strict31) })
	if switched != 0 {
		t.Fatalf("after a switch to Strict the move left the queue at tick %d at (%d,%d)", switched, sx, sz)
	}
}
