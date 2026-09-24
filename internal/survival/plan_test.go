package survival

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

// testPool is an authored pool: two tiers of ground units and one air unit.
func testPool() *Pool {
	u := func(key string, tier int, d Domain, cost int64) Unit {
		return Unit{Key: key, Def: &content.UnitDef{}, Tier: tier, Domain: d, Cost: cost}
	}
	return &Pool{
		Units: []Unit{
			u("g1a", 1, Ground, 100), u("g1b", 1, Ground, 150), u("g1c", 1, Ground, 250),
			u("g2a", 2, Ground, 500), u("g2b", 2, Ground, 900),
			u("air1", 1, Air, 400),
		},
		MaxTier: 2, Tier1Median: 150,
	}
}

// The planner is a pure function of the stream: equal seeds give equal waves,
// so a Survival battle replays exactly (DESIGN_SURVIVAL §6.8).
func TestPlanIsAFunctionOfTheStream(t *testing.T) {
	tune := DefaultTuning(PaceNormal)
	for n := 1; n <= 20; n++ {
		a, b := rng.NewSimulation(99), rng.NewSimulation(99)
		tick := uint32(n) * 2 * minute
		wa := Plan(n, tick, testPool(), tune, Options{}, nil, &a)
		wb := Plan(n, tick, testPool(), tune, Options{}, nil, &b)
		if !reflect.DeepEqual(wa, wb) || a.State != b.State {
			t.Fatalf("wave %d: equal seeds planned different waves", n)
		}
	}
}

// Fill never spends more than a group's share, except for the one unit every
// group is guaranteed; tiers above the unlocked tier never appear; air waits
// for AirFrom and is refused by NoAir.
func TestPlanRespectsBudgetTiersAndSwitches(t *testing.T) {
	tune := DefaultTuning(PaceNormal)
	pool := testPool()
	for seed := uint32(1); seed < 200; seed++ {
		for _, tick := range []uint32{0, 3 * minute, 12 * minute, 30 * minute} {
			noAir := tick == 30*minute
			r := rng.NewSimulation(seed)
			w := Plan(1, tick, pool, tune, Options{NoAir: noAir}, nil, &r)
			if len(w.Groups) == 0 || w.Units() == 0 {
				t.Fatalf("seed %d tick %d: no units", seed, tick)
			}
			share := w.Budget / int64(tune.Directions(tick))
			for _, g := range w.Groups {
				var spent int64
				for _, i := range g.Picks {
					u := pool.Units[i]
					spent += u.Cost
					if u.Tier > tune.UnlockedTier(w.Budget, pool) {
						t.Fatalf("seed %d tick %d: tier %d unit before its unlock", seed, tick, u.Tier)
					}
					if u.Domain == Air && (tick < tune.AirFrom || noAir) {
						t.Fatalf("seed %d tick %d: air unit where air is not allowed", seed, tick)
					}
				}
				if spent > share && len(g.Picks) > 1 {
					t.Fatalf("seed %d tick %d: group spent %d of %d", seed, tick, spent, share)
				}
			}
		}
	}
}

// Directions of one wave are at least 60° apart.
func TestPlanSeparatesDirections(t *testing.T) {
	tune := DefaultTuning(PaceNormal)
	for seed := uint32(1); seed < 300; seed++ {
		r := rng.NewSimulation(seed)
		w := Plan(13, 30*minute, testPool(), tune, Options{}, nil, &r)
		for i := range w.Groups {
			for j := i + 1; j < len(w.Groups); j++ {
				d := int32(int16(w.Groups[i].Angle - w.Groups[j].Angle))
				if d < 0 {
					d = -d
				}
				if d < 65536/6 {
					t.Fatalf("seed %d: directions %d and %d are %d apart", seed, w.Groups[i].Angle, w.Groups[j].Angle, d)
				}
			}
		}
	}
}

// Budgets grow with game time, doubling every Doubling ticks, and saturate
// rather than overflow; the pause after a wave grows with its size up to the
// cap.
func TestBudgetGrowsWithTimeAndSaturates(t *testing.T) {
	tune := DefaultTuning(PaceRelentless)
	prev := int64(0)
	for tick := uint32(0); tick < 600*minute; tick += 30 {
		b := tune.Budget(tick, 250)
		if b < prev || b <= 0 {
			t.Fatalf("tick %d budget %d after %d", tick, b, prev)
		}
		prev = b
	}
	if got, want := tune.Budget(tune.Doubling, 250), 2*tune.Budget(0, 250); got != want {
		t.Fatalf("one doubling: %d, want %d", got, want)
	}
	if tune.Downtime(0, 250) != tune.DowntimeBase || tune.Downtime(1<<40, 250) != tune.DowntimeMax {
		t.Fatalf("downtime bounds wrong")
	}
}

func TestLabelFindsComponents(t *testing.T) {
	// Two islands, the right one larger, split by an impassable column.
	w, h := int32(7), int32(3)
	r := Label(w, h, func(x, z int32) bool { return x != 2 })
	if r.At(0, 0) == 0 || r.At(3, 0) == 0 || r.At(0, 0) == r.At(3, 0) || r.At(2, 1) != 0 {
		t.Fatalf("components wrong: %d %d %d", r.At(0, 0), r.At(3, 0), r.At(2, 1))
	}
	if r.Largest() != r.At(6, 2) {
		t.Fatalf("largest is %d, want the right island %d", r.Largest(), r.At(6, 2))
	}
	x, z, ok := r.Nearest(r.At(6, 2), 1, 1)
	if !ok || x != 3 || z != 1 {
		t.Fatalf("nearest right-island cell to (1,1) = (%d,%d,%v), want (3,1)", x, z, ok)
	}
}

// A direction nothing can enter from turns until something can, and a map no
// land unit can cross still gets a wave: air comes early rather than none.
func TestPlanNeverPlansAnEmptyWaveWhileAirCanEnter(t *testing.T) {
	tune := DefaultTuning(PaceNormal)
	pool := testPool()
	onlyAir := func(_ uint16, i int) bool { return pool.Units[i].Domain == Air }
	for seed := uint32(1); seed < 50; seed++ {
		r := rng.NewSimulation(seed)
		w := Plan(1, 0, pool, tune, Options{}, onlyAir, &r)
		if w.Units() == 0 || w.Groups[0].Domain != Air {
			t.Fatalf("seed %d: wave 1 on an island map planned %d units", seed, w.Units())
		}
		r = rng.NewSimulation(seed)
		if w := Plan(1, 0, pool, tune, Options{NoAir: true}, onlyAir, &r); w.Units() != 0 {
			t.Fatalf("seed %d: NoAir admitted an air wave", seed)
		}
	}
	// Only the south-east octant admits ground: every group ends up there.
	southeast := func(a uint16, i int) bool { return a >= 8192 && a < 16384 }
	for seed := uint32(1); seed < 50; seed++ {
		r := rng.NewSimulation(seed)
		for _, g := range Plan(1, 0, pool, tune, Options{}, southeast, &r).Groups {
			if g.Angle < 8192 || g.Angle >= 16384 {
				t.Fatalf("seed %d: group planned from %d", seed, g.Angle)
			}
		}
	}
}

// Income is split evenly and stock is conserved; a full teammate's share
// returns to the earner; an earner cannot give more than it holds.
func TestSplitIncomeIsEvenAndConserving(t *testing.T) {
	a := []Account{{Stock: 300, Capacity: 1000, Earned: 300}, {Stock: 0, Capacity: 1000}, {Stock: 0, Capacity: 1000}}
	SplitIncome(a)
	if a[0].Stock != 100 || a[1].Stock != 100 || a[2].Stock != 100 {
		t.Fatalf("even split: %+v", a)
	}
	b := []Account{{Stock: 300, Capacity: 1000, Earned: 300}, {Stock: 1000, Capacity: 1000}, {Stock: 950, Capacity: 1000}}
	SplitIncome(b)
	if b[1].Stock != 1000 || b[2].Stock != 1000 || b[0].Stock != 250 {
		t.Fatalf("full teammates: %+v", b)
	}
	c := []Account{{Stock: 30, Capacity: 1000, Earned: 300}, {Stock: 0, Capacity: 1000}}
	SplitIncome(c)
	if c[0].Stock != 0 || c[1].Stock != 30 {
		t.Fatalf("earner gives only what it holds: %+v", c)
	}
}
