package construction

import (
	"math"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func TestForwardCircleIntersectionNearAxisAndTangentBoundary(t *testing.T) {
	for _, tc := range []struct {
		name      string
		direction float64
		startX    float64
	}{
		{name: "near horizontal axis", direction: 1e-12, startX: 16},
		{name: "absolute tangent equals one", direction: math.Atan(1), startX: 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x, z, ok := forwardCircleIntersection(100, 100, 24, tc.startX, 100, tc.direction)
			if !ok {
				t.Fatal("forward intersection was rejected")
			}
			if distance := math.Hypot(x-100, z-100); math.Abs(distance-24) > 1e-9 {
				t.Fatalf("intersection radius=%g, want 24", distance)
			}
			if forward := (x-tc.startX)*math.Cos(tc.direction) + (z-100)*math.Sin(tc.direction); forward < 0 {
				t.Fatalf("intersection points behind ray: dot=%g", forward)
			}
		})
	}
}

func TestCommunityKickoutAdmissionAndBudgetAreFeatureGated(t *testing.T) {
	builder := &units.Unit{Owner: 2, Alive: true}
	mobile := &units.Unit{Owner: 2, Alive: true, Def: &content.UnitDef{BMCode: 1}}
	building := &units.Unit{Owner: 2, Alive: true, Def: &content.UnitDef{BMCode: 0}}
	enemy := &units.Unit{Owner: 3, Alive: true, Def: &content.UnitDef{BMCode: 1}}

	for _, rules := range []Rules{StrictRules{}, CommunityRules{}, &ModernRules{}} {
		disabled := &Service{Rules: rules}
		if disabled.AdmitSiteOccupant(builder, mobile) || disabled.rules().BlockedSiteLimit(disabled) != 10 {
			t.Fatalf("disabled %T changed retail admission/budget", rules)
		}
		enabled := &Service{Rules: rules, Community: community.Features{ConstructionKickout: true}}
		want := rules != (Rules)(StrictRules{})
		if got := enabled.AdmitSiteOccupant(builder, mobile); got != want {
			t.Fatalf("enabled %T admission=%t want %t", rules, got, want)
		}
		wantLimit := uint32(10)
		if rules == (Rules)(CommunityRules{}) {
			wantLimit = 20
		}
		if got := enabled.rules().BlockedSiteLimit(enabled); got != wantLimit {
			t.Fatalf("enabled %T limit=%d want %d", rules, got, wantLimit)
		}
		if enabled.AdmitSiteOccupant(builder, building) || enabled.AdmitSiteOccupant(builder, enemy) {
			t.Fatalf("enabled %T admitted building or other owner", rules)
		}
	}
}

func TestCommunityKickoutDrawsBeforeProtectedWorkDecision(t *testing.T) {
	s, builder, blocker, _ := siteYieldFixture(t)
	s.Rules = CommunityRules{}
	s.Community.ConstructionKickout = true
	draws := 0
	s.CRTRandom = func(bound uint32) uint32 {
		if bound != 360 {
			t.Fatalf("CRT bound %d, want 360", bound)
		}
		draws++
		return 0
	}

	blocker.Def.BuildCostEnergy = 100
	targetHandle, err := s.World.CreateNanoframe(blocker.Def, blocker.Owner, world.CellToWorld(25), 0, world.CellToWorld(25))
	if err != nil {
		t.Fatal(err)
	}
	target := s.World.Unit(targetHandle)
	target.Remaining = .5 // 50 energy invested: protected without a second worker.
	q := s.queueForUnit(blocker)
	q.Push(orders.Lookup("RepairUnit"), orders.NewNodeForOrder(orders.Lookup("RepairUnit"), target.Handle, target.X, target.Y, target.Z, 0, blocker.Handle, false))
	before := q.Head()

	extent, _ := world.NewFootprintExtent(2, 2)
	clear, _ := world.NewFootprintRect(world.NewFootprintAnchor(world.WorldToCell(blocker.X), world.WorldToCell(blocker.Z)), extent)
	s.rules().YieldObstruction(s, builder, clear, nil, 7, true)
	if draws != 1 {
		t.Fatalf("CRT draws=%d, want one unconditional draw", draws)
	}
	if q.Head() != before {
		t.Fatal("protected sole worker was kicked")
	}

	// The manual path uses the same rewrite, but consumes no CRT draw.
	if !s.KickoutMove(blocker, numeric.FixedFromInt(40), 0, numeric.FixedFromInt(40), 8) {
		t.Fatal("enabled manual kickout refused")
	}
	if draws != 1 || q.Head() == before {
		t.Fatalf("manual kickout draws=%d head unchanged=%t", draws, q.Head() == before)
	}
}
