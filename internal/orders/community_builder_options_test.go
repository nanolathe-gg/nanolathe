package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func bindBuilderOptions(u *units.Unit, rules Rules, features community.Features, options BuilderOptions) *QueueBinding {
	b := &QueueBinding{Rules: rules, Community: features, BuilderOptions: func(uint8) BuilderOptions { return options }}
	BindQueue(u, &Queue{binding: b})
	return b
}

func TestBuilderOptionDefaultsAndFallbacks(t *testing.T) {
	defaults := DefaultBuilderOptions()
	if defaults.Guard != [3]GuardHomeOption{GuardCavedog, GuardCavedog, GuardCavedog} {
		t.Fatalf("guard defaults = %v, want Cavedog for all movement modes", defaults.Guard)
	}
	if defaults.Patrol != [3]PatrolWorkOption{PatrolReclaimOnly, PatrolBoth, PatrolBoth} {
		t.Fatalf("patrol defaults = %v, want Reclaim Only/Both/Both", defaults.Patrol)
	}

	u := &units.Unit{Owner: 7, Flags: 3 << units.StandingMoveShift}
	bad := defaults
	bad.Guard[0] = GuardHomeOption(99)
	bad.Patrol[0] = PatrolWorkOption(99)
	b := bindBuilderOptions(u, &CommunityRules{}, community.Features{
		GuardingBuildersHold: true, PatrollingBuilderFilters: true,
	}, bad)
	if got := guardOption(u); got != GuardCavedog {
		t.Fatalf("invalid movement mode guard option = %d, want Cavedog", got)
	}
	if got := patrolOption(u); got != PatrolBoth {
		t.Fatalf("invalid movement mode patrol option = %d, want Both", got)
	}
	calledOwner := uint8(0xff)
	b.BuilderOptions = func(owner uint8) BuilderOptions {
		calledOwner = owner
		return bad
	}
	u.Flags = 0
	if got := guardOption(u); got != GuardCavedog {
		t.Fatalf("invalid stored guard option = %d, want Cavedog", got)
	}
	if got := patrolOption(u); got != PatrolBoth {
		t.Fatalf("invalid stored patrol option = %d, want Both", got)
	}
	if calledOwner != u.Owner {
		t.Fatalf("options provider owner = %d, want acting owner %d", calledOwner, u.Owner)
	}
	b.BuilderOptions = nil
	if got := guardOption(u); got != GuardCavedog {
		t.Fatalf("nil provider guard option = %d, want default Cavedog", got)
	}
	if got := patrolOption(u); got != PatrolReclaimOnly {
		t.Fatalf("nil provider patrol option = %d, want default Reclaim Only", got)
	}
}

func TestGuardHomeStrictFlagAndModernComposition(t *testing.T) {
	guard := &units.Unit{X: numeric.FixedFromInt(40), Z: numeric.FixedFromInt(60)}
	ward := &units.Unit{X: numeric.FixedFromInt(20), Z: numeric.FixedFromInt(80)}
	originalX := numeric.Fixed(int64(91)<<16 | 0x1234)
	originalZ := numeric.Fixed(int64(-73)<<16 | 0xabcd)
	req := GuardHomeRequest{Guard: guard, Ward: ward, OffsetX: originalX, OffsetZ: originalZ, Spacing: 64}
	options := DefaultBuilderOptions()
	options.Guard[0] = GuardScatter
	features := community.Features{GuardingBuildersHold: true}

	b := bindBuilderOptions(guard, StrictRules{}, features, options)
	if x, z := b.rules().GuardHome(req); x != originalX || z != originalZ {
		t.Fatalf("Strict changed guard home to (%#x,%#x), want original (%#x,%#x)", x, z, originalX, originalZ)
	}

	b.Rules = &CommunityRules{}
	b.Community.GuardingBuildersHold = false
	if x, z := b.rules().GuardHome(req); x != originalX || z != originalZ {
		t.Fatalf("flag-off Community changed guard home to (%#x,%#x)", x, z)
	}

	b.Rules = &ModernRules{}
	b.Community.GuardingBuildersHold = true
	x, z := b.rules().GuardHome(req)
	if want := numeric.Fixed(int64(64)<<16 | 0x1234); x != want {
		t.Fatalf("Modern did not inherit Community scatter X: got %#x want %#x", x, want)
	}
	if want := numeric.Fixed(int64(-64)<<16 | 0xabcd); z != want {
		t.Fatalf("Modern did not inherit Community scatter Z: got %#x want %#x", z, want)
	}
}

func TestPatrolWorkStrictFlagAndModernComposition(t *testing.T) {
	u := &units.Unit{}
	options := DefaultBuilderOptions()
	options.Patrol[0] = PatrolAssistOnly
	features := community.Features{PatrollingBuilderFilters: true}
	b := bindBuilderOptions(u, StrictRules{}, features, options)
	req := PatrolWorkRequest{Builder: u}
	if got := b.rules().PatrolWork(req); got != PatrolBoth {
		t.Fatalf("Strict patrol work = %d, want Both", got)
	}
	b.Rules = &CommunityRules{}
	b.Community.PatrollingBuilderFilters = false
	if got := b.rules().PatrolWork(req); got != PatrolBoth {
		t.Fatalf("flag-off Community patrol work = %d, want Both", got)
	}
	b.Rules = &ModernRules{}
	b.Community.PatrollingBuilderFilters = true
	if got := b.rules().PatrolWork(req); got != PatrolAssistOnly {
		t.Fatalf("Modern patrol work = %d, want inherited Assist Only", got)
	}
}

func TestGuardHomeStayArithmeticFractionsAndQuadrants(t *testing.T) {
	guard := &units.Unit{Def: &content.UnitDef{Builder: false}}
	ward := &units.Unit{}
	options := DefaultBuilderOptions()
	options.Guard[0] = GuardStay
	b := bindBuilderOptions(guard, &CommunityRules{}, community.Features{GuardingBuildersHold: true}, options)

	// A non-builder proves the approved hook scope is every ground guard that
	// reaches follow maintenance. Equality is the positive quadrant, and only
	// the whole halves are replaced: 7*63/20 = 22 after unsigned truncation.
	guard.X, guard.Z = numeric.FixedFromInt(100), numeric.FixedFromInt(200)
	ward.X, ward.Z = guard.X, guard.Z
	n := &Node{
		Owner: guard.Handle, Param1: 63,
		GoalX: numeric.Fixed(int64(-9)<<16 | 0x1357),
		GoalY: numeric.Fixed(int64(41)<<16 | 0x2468),
		GoalZ: numeric.Fixed(int64(11)<<16 | 0x9bdf),
	}
	b.Movement = &MovementGoalAdapter{InstallPoint: func(PointGoalRequest) bool { return true }}
	if code := guardFollowMaintenance(guard, n, ward, 0, 10); code != 2 {
		t.Fatalf("guard maintenance returned %d, want hold", code)
	}
	if want := numeric.Fixed(int64(22)<<16 | 0x1357); n.GoalX != want {
		t.Fatalf("equal-quadrant stay X = %#x, want %#x", n.GoalX, want)
	}
	if want := numeric.Fixed(int64(22)<<16 | 0x9bdf); n.GoalZ != want {
		t.Fatalf("equal-quadrant stay Z = %#x, want %#x", n.GoalZ, want)
	}
	if want := numeric.Fixed(int64(41)<<16 | 0x2468); n.GoalY != want {
		t.Fatalf("guard home changed vertical offset: got %#x want %#x", n.GoalY, want)
	}

	// Unsigned whole-coordinate comparison puts a negative guard coordinate
	// above a positive ward coordinate, while an ordinary smaller positive Z
	// selects the negative side.
	guard.X, ward.X = numeric.FixedFromInt(-1), numeric.FixedFromInt(1)
	guard.Z, ward.Z = numeric.FixedFromInt(4), numeric.FixedFromInt(5)
	n.GoalX, n.GoalZ = numeric.Fixed(0x1111), numeric.Fixed(0x2222)
	x, z := b.rules().GuardHome(GuardHomeRequest{
		Guard: guard, Ward: ward, OffsetX: n.GoalX, OffsetZ: n.GoalZ, Spacing: 63,
	})
	if want := numeric.Fixed(int64(22)<<16 | 0x1111); x != want {
		t.Fatalf("unsigned X quadrant = %#x, want positive %#x", x, want)
	}
	if want := numeric.Fixed(int64(-22)<<16 | 0x2222); z != want {
		t.Fatalf("ordinary negative Z quadrant = %#x, want %#x", z, want)
	}
}

func communityPatrolFixture(t *testing.T, air bool, option PatrolWorkOption) (*units.Unit, *Node, *rng.Simulation, *int, *int) {
	t.Helper()
	def := &content.UnitDef{
		BMCode: 1, Builder: true, CanMove: true, CanFly: air, CanReclamate: true,
		SightDistance: 96, CruiseAlt: 100, MaxDamage: 100,
	}
	u := &units.Unit{Handle: 1, Def: def, Alive: true, Health: 50, MaxHealth: 100}
	u.Move.Mode, u.Move.ModeMirror = 2, 2
	options := DefaultBuilderOptions()
	options.Patrol[0] = option
	sim := rng.SimulationFromState(1)
	unitScans, featureScans := 0, 0
	b := bindBuilderOptions(u, &CommunityRules{}, community.Features{PatrollingBuilderFilters: true}, options)
	b.SimRNG = &sim
	b.Resources = func(uint8) (ResourceView, bool) {
		return ResourceView{Stock: [2]float32{0, 50}, Capacity: [2]float32{100, 100}}, true
	}
	b.World = &WorldQueryAdapter{
		ForEachUnit: func(func(pool.Handle, *units.Unit) bool) { unitScans++ },
		LookupFeature: func(int32, int32) (FeatureView, bool) {
			featureScans++
			return FeatureView{ID: 7, Energy: 10, Reclaimable: true, Autoreclaimable: true}, true
		},
		TerrainHeight: func(numeric.Fixed, numeric.Fixed) (numeric.Fixed, bool) { return 0, true },
	}
	b.Movement = &MovementGoalAdapter{
		InstallPoint: func(PointGoalRequest) bool { return true },
		InstallAir:   func(AirGoalRequest) bool { return true },
		Release:      func(*Node) bool { return true },
		AirBases: func(uint8) []pool.Handle {
			t.Fatalf("reclaim-only patrol reached the VTOL assistance pad scan")
			return nil
		},
	}
	n := &Node{Owner: u.Handle, Phase: 1, Deadline: -1, GoalSupplied: true}
	return u, n, &sim, &unitScans, &featureScans
}

func TestPatrolWorkFiltersPreserveBranchDraws(t *testing.T) {
	for _, air := range []bool{false, true} {
		name := "ground"
		handler := repairPatrolHandler
		if air {
			name = "VTOL"
			handler = vtolRepairPatrolHandler
		}
		t.Run(name+" reclaim only", func(t *testing.T) {
			u, n, sim, unitScans, featureScans := communityPatrolFixture(t, air, PatrolReclaimOnly)
			if code := handler(u, n, 0, 100); code != 3 {
				t.Fatalf("reclaim-only visit returned %d, want wait under spawned reclaim", code)
			}
			if *unitScans != 0 {
				t.Fatalf("reclaim-only visit ran %d repair scans, want none", *unitScans)
			}
			if *featureScans == 0 {
				t.Fatal("reclaim-only visit did not enter the feature search")
			}
			if got := sim.Draws(); got != 3 {
				t.Fatalf("reclaim-only visit drew %d values, want only the three feature tournament draws", got)
			}
			want := "Reclaim"
			if air {
				want = "VTOL_Reclaim"
			}
			if q := QueueOfUnit(u); q.LenPrimary() != 1 || DescriptorFor(q.Head().ID).Name != want {
				t.Fatalf("reclaim-only visit did not spawn %s", want)
			}
		})

		t.Run(name+" reclaim only keeps storage gate", func(t *testing.T) {
			u, n, sim, unitScans, featureScans := communityPatrolFixture(t, air, PatrolReclaimOnly)
			QueueOfUnit(u).Binding().Resources = func(uint8) (ResourceView, bool) {
				return ResourceView{Stock: [2]float32{50, 50}, Capacity: [2]float32{100, 100}}, true
			}
			if code := handler(u, n, 0, 100); code != 2 {
				t.Fatalf("healthy reclaim-only visit returned %d, want hold at the stock gate", code)
			}
			if *unitScans != 0 || *featureScans != 0 {
				t.Fatalf("healthy reclaim-only visit ran repair/feature scans = %d/%d, want 0/0", *unitScans, *featureScans)
			}
			if got := sim.Draws(); got != 0 {
				t.Fatalf("healthy reclaim-only visit drew %d values, want none", got)
			}
		})

		t.Run(name+" assist only", func(t *testing.T) {
			u, n, sim, unitScans, featureScans := communityPatrolFixture(t, air, PatrolAssistOnly)
			// Keep the air builder healthy so its ordinary assistance side reaches
			// the empty repair scan and then stops at the reclaim boundary.
			u.Health = u.MaxHealth
			if code := handler(u, n, 0, 100); code != 2 {
				t.Fatalf("assist-only visit returned %d, want hold", code)
			}
			if *unitScans != 1 {
				t.Fatalf("assist-only visit ran %d repair scans, want one", *unitScans)
			}
			if *featureScans != 0 {
				t.Fatalf("assist-only visit ran %d feature lookups, want none", *featureScans)
			}
			if got := sim.Draws(); got != 0 {
				t.Fatalf("assist-only empty repair visit drew %d values, want none", got)
			}
			if q := QueueOfUnit(u); q.LenPrimary() != 0 {
				t.Fatalf("assist-only visit spawned %d orders, want none", q.LenPrimary())
			}
		})
	}
}
