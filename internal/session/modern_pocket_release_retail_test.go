//go:build retail

package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Nanolathe Modern policy: docs/DESIGN_MOVEMENT_PATH.md "Modern pocket
// release". The fixture is authored here — flat terrain, a 3x3 block of
// parked retail fleas whose centre slot is free, and one more flea standing
// against the block with an assigned move to that slot — and says nothing
// about retail beyond the Strict retry it locks [04 R-ORD-01 §4].

const (
	pocketTicks  = 600
	pocketSettle = 60 // the block stands this long first, so its search walls hold
	pocketHoleX  = 30 // the free slot's footprint anchor
	pocketHoleZ  = 24
)

// noPocketRules is Modern with the pocket release switched off, the
// path benchmark's modern-no-pocket, composed here because that set is
// registered only in the opt-in benchmark build.
type noPocketRules struct{ movement.ModernRules }

func (*noPocketRules) PocketRelease(*movement.System) (int32, uint32) { return 0, 0 }

func pocketlessModern() *RuleSet {
	set := ModernRuleSet()
	set.Movement = &noPocketRules{}
	return &set
}

type pocketRun struct {
	done        int  // tick the move left the queue, 0 if it never did
	passedRing  bool // the mover's committed footprint met a block unit's
	passedEnemy bool // ... met the hostile block unit's
	ringMoved   bool // a block unit's position changed
	endOverlap  bool // the mover still overlaps a block unit at the end
	final       movement.Cell
}

// pocketScene composes a 64x48 flat battle: eight fleas on anchors two cells
// apart around the free slot, footprints touching, and the mover on the
// block's west side, against it. "held" parks a ninth flea in the slot;
// "enemy" gives the block's west-middle flea, between the mover and the slot,
// to a hostile owner. Every unit holds fire, so the hostile case stays a
// movement case. After the settle the mover is ordered, as an assigned
// position, into the slot, and the scene runs to pocketTicks.
func pocketScene(t *testing.T, rules string, set *RuleSet, variant string) pocketRun {
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
	place := func(owner uint8, x, z int32) *units.Unit {
		u := placeCompleteRetailUnit(t, s, "armflea", owner, unreachCell(x), unreachCell(z))
		u.Flags &^= units.StandingFieldMask << units.StandingFireShift // hold fire
		s.Movement.EnsureUnit(u)
		return u
	}
	var ring []*units.Unit
	var enemy *units.Unit
	for dz := int32(-2); dz <= 2; dz += 2 {
		for dx := int32(-2); dx <= 2; dx += 2 {
			if dx == 0 && dz == 0 {
				continue
			}
			owner := uint8(0)
			if variant == "enemy" && dx == -2 && dz == 0 {
				owner = 1
			}
			u := place(owner, pocketHoleX+dx, pocketHoleZ+dz)
			ring = append(ring, u)
			if owner == 1 {
				enemy = u
			}
		}
	}
	if variant == "held" {
		ring = append(ring, place(0, pocketHoleX, pocketHoleZ))
	}
	mover := place(0, pocketHoleX-4, pocketHoleZ)
	start := make([][2]int64, len(ring))
	for i, u := range ring {
		start[i] = [2]int64{int64(u.X), int64(u.Z)}
	}
	foot := func(u *units.Unit) (movement.Cell, int32, int32) {
		a, fx, fz, _ := s.Movement.CommittedFootprint(u.Handle)
		return a, int32(fx), int32(fz)
	}
	meets := func(a, b *units.Unit) bool {
		ac, afx, afz := foot(a)
		bc, bfx, bfz := foot(b)
		return ac.X < bc.X+bfx && bc.X < ac.X+afx && ac.Z < bc.Z+bfz && bc.Z < ac.Z+afz
	}
	var run pocketRun
	move := orders.Lookup("Move_Ground")
	for tick := 1; tick <= pocketTicks; tick++ {
		if tick == pocketSettle {
			gx, gz := unreachCell(pocketHoleX), unreachCell(pocketHoleZ)
			if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{
				Handles: []pool.Handle{mover.Handle}, Code: 2, AssignedPosition: true,
				Position: orders.ResolvePos{X: gx, Y: ter.HeightAt(gx, gz), Z: gz, InterfaceType: orders.InterfaceTypeRightClick},
			}}); err != nil {
				t.Fatal(err)
			}
		}
		s.stepAuthoritativePhases(s.Clock.BeginSubTick())
		if !mover.Alive {
			t.Fatal("the mover died")
		}
		if q := orders.QueueOfUnit(mover); tick > pocketSettle+2 && run.done == 0 && (q == nil || q.Head() == nil || q.Head().ID != move) {
			run.done = tick
		}
		for i, u := range ring {
			if meets(mover, u) {
				run.passedRing = true
				if u == enemy {
					run.passedEnemy = true
				}
			}
			if int64(u.X) != start[i][0] || int64(u.Z) != start[i][1] {
				run.ringMoved = true
			}
		}
	}
	for _, u := range ring {
		run.endOverlap = run.endOverlap || meets(mover, u)
	}
	run.final, _, _ = foot(mover)
	return run
}

var pocketHole = movement.Cell{X: pocketHoleX, Z: pocketHoleZ}

// The pocket release takes a unit sealed out of its own free slot through the
// parked friends around it and into the slot; the friends do not move and no
// overlap is left. Modern without the pocket release leaves the same unit
// standing outside for the whole window: its searches are rejected, the
// retry installs no line to walk, and jam release never counts a unit at
// rest.
func TestModernPocketReleaseTakesASealedOutUnitIntoItsSlot(t *testing.T) {
	without := pocketScene(t, ModernRuleSetName, pocketlessModern(), "free")
	if without.final == pocketHole || without.passedRing {
		t.Fatalf("without the pocket release the mover reached %v (passed the block: %v); the scene no longer seals it out", without.final, without.passedRing)
	}
	with := pocketScene(t, ModernRuleSetName, nil, "free")
	if with.final != pocketHole || with.done == 0 {
		t.Fatalf("the pocket release left the mover at %v (move done at tick %d); want its slot %v", with.final, with.done, pocketHole)
	}
	if !with.passedRing || with.ringMoved || with.endOverlap {
		t.Fatalf("passed the block %v, block moved %v, overlap left %v; want a pass through a block that stays put", with.passedRing, with.ringMoved, with.endOverlap)
	}
	t.Logf("sealed-out mover reached its slot; move done at tick %d", with.done)
}

// Strict 3.1 and Community 3.9 never grant a pocket release: the mover never
// crosses the block and never reaches the slot.
func TestStrictAndCommunityNeverGrantAPocketRelease(t *testing.T) {
	for _, rules := range []string{StrictRuleSetName, CommunityRuleSetName} {
		run := pocketScene(t, rules, nil, "free")
		if run.passedRing || run.final == pocketHole || run.ringMoved {
			t.Fatalf("%s: passed the block %v, reached %v, block moved %v", rules, run.passedRing, run.final, run.ringMoved)
		}
	}
}

// A slot a parked friend holds is not a free pocket: the pocket release never
// takes the unit through the block, and crowded arrival finishes the move
// where the unit stands (DESIGN_MOVEMENT_PATH "Modern crowded arrival") on
// exactly the tick it does without the pocket release.
func TestPocketReleaseLeavesAHeldGoalToCrowdedArrival(t *testing.T) {
	with := pocketScene(t, ModernRuleSetName, nil, "held")
	if with.passedRing || with.ringMoved {
		t.Fatalf("passed the block %v, block moved %v; a held goal is never released into", with.passedRing, with.ringMoved)
	}
	if with.done == 0 || with.final.X > pocketHoleX-4 {
		t.Fatalf("move done at tick %d with the mover at %v; want crowded arrival to finish it outside the block", with.done, with.final)
	}
	if without := pocketScene(t, ModernRuleSetName, pocketlessModern(), "held"); without != with {
		t.Fatalf("the pocket release changed a held-goal run: %+v without, %+v with", without, with)
	}
	t.Logf("crowded arrival finished the held-goal move at tick %d at %v", with.done, with.final)
}

// A hostile unit in the ring opens the pocket (only parked friends wall it),
// so nothing is certified and no release ever takes the mover through the
// enemy, which the same block of friends does not stop
// (TestModernPocketReleaseTakesASealedOutUnitIntoItsSlot passes the same
// west-middle position when it is friendly).
func TestPocketReleaseNeverPassesAnEnemy(t *testing.T) {
	run := pocketScene(t, ModernRuleSetName, nil, "enemy")
	if run.passedEnemy || run.final == pocketHole || run.ringMoved {
		t.Fatalf("passed the enemy %v, reached %v, block moved %v", run.passedEnemy, run.final, run.ringMoved)
	}
}
