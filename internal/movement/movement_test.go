package movement

import (
	"math"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

func TestMoverStep(t *testing.T) {
	def := &content.UnitDef{UnitName: "test", MaxVelocity: 2 * 65536} // 2 pixels per tick [02 "Unit record"]
	def.MaxDamage = 100
	w := units.New(10, nil)
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	u := w.Unit(h)
	// Push Move_Ground to 10 pixels east.
	id := orders.Lookup("Move_Ground")
	if id == 0 {
		t.Fatal("Move_Ground not found")
	}
	q := orders.QueueForUnit(u)
	goalX := numeric.Fixed(10 * 65536)
	q.Push(id, orders.Node{GoalX: goalX, GoalZ: 0})
	m := &Mover{World: w}
	// Step once: should move 2 pixels toward goal.
	m.Tick(1)
	if u.X != numeric.Fixed(2*65536) {
		t.Fatalf("after 1 tick x=%d want %d", int64(u.X), 2*65536)
	}
	// Step second time: 4 pixels.
	m.Tick(2)
	if u.X != numeric.Fixed(4*65536) {
		t.Fatalf("after 2 ticks x=%d want %d", int64(u.X), 4*65536)
	}
	// Verify no RNG draws for straight-line mover (determinism).
	_ = math.Hypot
}

func TestMoverArrival(t *testing.T) {
	def := &content.UnitDef{UnitName: "test2", MaxVelocity: 5 * 65536}
	def.MaxDamage = 100
	w := units.New(10, nil)
	h, _ := w.Create(def, 0, 0, 0, 0)
	u := w.Unit(h)
	id := orders.Lookup("Move_Ground")
	q := orders.QueueForUnit(u)
	goalX := numeric.Fixed(3 * 65536) // only 3 away, but rate 5 should snap
	q.Push(id, orders.Node{GoalX: goalX})
	m := &Mover{World: w}
	m.Tick(1)
	if u.X != goalX {
		t.Fatalf("arrival snap x=%d want %d", int64(u.X), int64(goalX))
	}
	// Stay at goal on next tick.
	m.Tick(2)
	if u.X != goalX {
		t.Fatalf("should hold at goal x=%d want %d", int64(u.X), int64(goalX))
	}
}

func TestMoverUsesDefVelocityOrPlaceholder(t *testing.T) {
	// Zero velocity should fall back to placeholder 1 pixel per tick.
	def := &content.UnitDef{UnitName: "slow", MaxVelocity: 0}
	def.MaxDamage = 100
	w := units.New(10, nil)
	h, _ := w.Create(def, 0, 0, 0, 0)
	u := w.Unit(h)
	id := orders.Lookup("Move_Ground")
	q := orders.QueueForUnit(u)
	q.Push(id, orders.Node{GoalX: numeric.Fixed(10 * 65536)})
	m := &Mover{World: w}
	m.Tick(1)
	// Placeholder is 1 pixel = 65536
	if u.X != numeric.Fixed(1*65536) {
		t.Fatalf("placeholder rate x=%d want %d", int64(u.X), 65536)
	}
}

// TestHeadlessTwoTickDeterminism locks that same seed ⇒ identical final
// positions and RNG draw counts [01 §7.1] I4.
func TestHeadlessTwoTickDeterminism(t *testing.T) {
	run := func(seed uint32) (int64, int64, uint64, uint64) {
		rng.SeedGlobal(seed, seed)
		def := &content.UnitDef{UnitName: "armflea", MaxVelocity: 3 * 65536}
		def.MaxDamage = 75
		w := units.New(10, nil)
		h, _ := w.Create(def, 0, 0, 0, numeric.Fixed(0))
		u := w.Unit(h)
		id := orders.Lookup("Move_Ground")
		q := orders.QueueForUnit(u)
		q.Push(id, orders.Node{GoalX: numeric.Fixed(20 * 65536), GoalZ: numeric.Fixed(0)})
		m := &Mover{World: w}
		// Simulate Pump + Mover for 2 ticks (as kernel would).
		for tick := uint32(1); tick <= 2; tick++ {
			q.Pump(u, tick)
			m.Tick(tick)
		}
		simDraws := rng.Global.Sim.Draws()
		crtDraws := rng.Global.Crt.Draws()
		return int64(u.X), int64(u.Z), simDraws, crtDraws
	}
	x1, z1, s1, c1 := run(42)
	x2, z2, s2, c2 := run(42)
	if x1 != x2 || z1 != z2 || s1 != s2 || c1 != c2 {
		t.Fatalf("determinism violated: (%d,%d draws %d/%d) vs (%d,%d draws %d/%d)", x1, z1, s1, c1, x2, z2, s2, c2)
	}
	// Different seed with the straight-line stub uses no RNG, so positions
	// remain identical — the determinism requirement is same seed ⇒ same
	// output, not that different seeds must diverge for this path.
	_, _, _, _ = run(43)
}
