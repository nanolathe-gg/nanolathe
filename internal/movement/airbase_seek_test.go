package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// airBasePadDef is a completed, activated `builder`+`isairbase` structure — the
// definition shape the target registry's third list requires
// [06 §3.1 "the third list"][04 R-AIR-01 §11].
func airBasePadDef(key string) *content.UnitDef {
	return &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey(key)},
		UnitName:         key,
		Builder:          true,
		IsAirBase:        true,
		FootprintX:       1,
		FootprintZ:       1,
		MaxDamage:        1000,
	}
}

// spawnAirBasePad places one activated pad `offset` world units east of the
// aircraft and returns it.
func spawnAirBasePad(t *testing.T, w *units.World, ter *world.Terrain, key string, x, z numeric.Fixed) *units.Unit {
	t.Helper()
	return spawnAirBasePadFor(t, w, ter, key, 0, x, z)
}

// spawnAirBasePadFor is spawnAirBasePad for a named owner slot.
func spawnAirBasePadFor(t *testing.T, w *units.World, ter *world.Terrain, key string, owner uint8, x, z numeric.Fixed) *units.Unit {
	t.Helper()
	h, err := w.Create(airBasePadDef(key), owner, x, ter.HeightAt(x, z), z)
	if err != nil {
		t.Fatalf("create pad %s: %v", key, err)
	}
	pad := w.Unit(h)
	pad.Activated = true
	pad.Remaining = 0
	return pad
}

// airBaseSeekFixture is airFixture plus a seeded stream this test owns, so draw
// counts are observable [I4].
func airBaseSeekFixture(t *testing.T) (*System, *units.World, *units.Unit, *rng.Simulation) {
	t.Helper()
	sys, w, u := airFixture(t)
	sim := rng.NewSimulation(0x2f6b1c05)
	orders.QueueForUnit(u).SetBinding(&orders.QueueBinding{SimRNG: &sim, Lookup: w.Unit})
	return sys, w, u, &sim
}

// rebuildAirBases runs the registry's 30-tick rebuild once, which is the only
// thing that refills the third list [06 §3.1][04 R-AIR-01 §11]. Tests call it
// after arranging the world and NOT after mutating it, when the point is that
// the snapshot went stale.
func rebuildAirBases(sys *System, tick uint32) {
	sys.BeginTick(tick - tick%combat.AirBaseRegistryPeriod)
}

// headIsLanding reports whether the queue head is a VTOL_Landing record — the
// head insert the land branch performs [04 R-AIR-01 §11][04 R-ORD-01 §1].
func headIsLanding(u *units.Unit) bool {
	q := orders.QueueForUnit(u)
	if q == nil || q.LenPrimary() == 0 {
		return false
	}
	return q.Primary()[0].ID == orders.Lookup("VTOL_Landing")
}

// TestAirBelowThreeQuartersBoundary locks the health threshold every base-seek
// caller shares: `(uint)(int16)health < (MaxDamage >> 2) * 3`, strict, with the
// quarter formed by a truncating shift and the health read as a sign-extended
// 16-bit field compared unsigned [04 R-AIR-01 §11].
func TestAirBelowThreeQuartersBoundary(t *testing.T) {
	def := &content.UnitDef{MaxDamage: 100} // (100 >> 2) * 3 == 75
	u := &units.Unit{Def: def}

	u.Health = 75
	if airBelowThreeQuarters(u) {
		t.Fatal("health exactly at three quarters must not seek a pad: the compare is strict [04 R-AIR-01 §11]")
	}
	u.Health = 74
	if !airBelowThreeQuarters(u) {
		t.Fatal("health one below three quarters must seek a pad [04 R-AIR-01 §11]")
	}

	// The truncating shift, not a three-quarter multiply: (101>>2)*3 == 75, and
	// 101*3/4 == 75 too, so use a value where they part — (102>>2)*3 == 75 while
	// 102*3/4 == 76.
	u.Def = &content.UnitDef{MaxDamage: 102}
	u.Health = 75
	if airBelowThreeQuarters(u) {
		t.Fatal("the quarter is a truncating shift of MaxDamage, taken before the multiply [04 R-AIR-01 §11]")
	}

	// An overkilled aircraft: (uint)(int16)(-1) is 0xFFFFFFFF, which is not
	// below any real threshold, so the branch is not taken. Clamping the
	// negative to zero — what this used to do — inverted that.
	u.Def = def
	u.Health = -1
	if airBelowThreeQuarters(u) {
		t.Fatal("negative health sign-extends and compares unsigned, so it is not below three quarters [04 R-AIR-01 §11]")
	}

	// No definition word, no threshold.
	if airBelowThreeQuarters(&units.Unit{Health: 0}) {
		t.Fatal("with no MaxDamage there is no threshold [04 R-AIR-01 §11]")
	}
}

// TestAirBaseSeekRadiusAndListOrder locks the movement-side scan end to end: a
// pad at exactly 0xF00 world units is admitted, one past it is not, and the
// admitted set arrives in unit-array order with nothing sorted
// [04 R-AIR-01 §11].
func TestAirBaseSeekRadiusAndListOrder(t *testing.T) {
	sys, w, u, _ := airBaseSeekFixture(t)
	r := int64(0xF00)

	near := spawnAirBasePad(t, w, sys.Terrain, "padnear", u.X+numeric.Fixed(200<<16), u.Z)
	onEdge := spawnAirBasePad(t, w, sys.Terrain, "padedge", u.X+numeric.Fixed(r<<16), u.Z)
	spawnAirBasePad(t, w, sys.Terrain, "padfar", u.X+numeric.Fixed((r+1)<<16), u.Z)
	rebuildAirBases(sys, 30)

	got := sys.airBaseCandidates(u)
	want := []uint16{uint16(near.Handle), uint16(onEdge.Handle)}
	if len(got) != len(want) {
		t.Fatalf("candidates %v, want the two within 0xF00 (%v) — the compare is inclusive [04 R-AIR-01 §11]", got, want)
	}
	for i := range want {
		if uint16(got[i]) != want[i] {
			t.Fatalf("candidates %v, want %v in unit-array order [06 §3.1](I1)", got, want)
		}
	}

	// Deactivating a pad removes it: the three flags are re-tested at scan time
	// [04 R-AIR-01 §11].
	near.Activated = false
	if got := sys.airBaseCandidates(u); len(got) != 1 || got[0] != onEdge.Handle {
		t.Fatalf("candidates %v after deactivating the near pad, want just the edge pad [04 R-AIR-01 §11]", got)
	}
}

// TestAirBaseSeekDrawsOncePerSuccessfulScan locks the draw contract: the pick is
// one simulation `RNG(count)` taken only on a non-empty list, and a count of one
// draws nothing at all [01 §7.1][04 R-AIR-01 §11][I4].
func TestAirBaseSeekDrawsOncePerSuccessfulScan(t *testing.T) {
	t.Run("empty list draws nothing", func(t *testing.T) {
		sys, _, u, sim := airBaseSeekFixture(t)
		n := &orders.Node{Owner: u.Handle}
		before := sim.Draws()
		if sys.airFindBaseAndLand(u, n, sim, 1) {
			t.Fatal("an empty third list must not take the land branch [04 R-AIR-01 §11]")
		}
		if d := sim.Draws() - before; d != 0 {
			t.Fatalf("an empty scan drew %d times, want 0 — the draw belongs to the pick [I4]", d)
		}
	})

	t.Run("one candidate lands without advancing the stream", func(t *testing.T) {
		// RNG(1) has bound < 2 and returns 0 without a step [01 §7.1].
		sys, w, u, sim := airBaseSeekFixture(t)
		only := spawnAirBasePad(t, w, sys.Terrain, "padsingle", u.X+numeric.Fixed(300<<16), u.Z)
		rebuildAirBases(sys, 30)
		n := &orders.Node{Owner: u.Handle, DynamicGate: 0xE1}
		before := sim.Draws()
		if !sys.airFindBaseAndLand(u, n, sim, 1) {
			t.Fatal("a single candidate must take the land branch [04 R-AIR-01 §11]")
		}
		if d := sim.Draws() - before; d != 0 {
			t.Fatalf("a one-candidate pick drew %d times, want 0 [01 §7.1][I4]", d)
		}
		if n.DynamicGate != 0 {
			t.Fatalf("the land branch clears the gate word, got %#x [04 R-AIR-01 §11]", n.DynamicGate)
		}
		if !headIsLanding(u) {
			t.Fatal("the land branch head-inserts a VTOL_Landing record [04 R-AIR-01 §11][04 R-ORD-01 §1]")
		}
		if head := orders.QueueForUnit(u).Primary()[0]; head.Target != only.Handle {
			t.Fatalf("the landing record targets %d, want the drawn pad %d [04 R-AIR-01 §11]", head.Target, only.Handle)
		}
	})

	t.Run("two candidates draw exactly once", func(t *testing.T) {
		sys, w, u, sim := airBaseSeekFixture(t)
		a := spawnAirBasePad(t, w, sys.Terrain, "padA", u.X+numeric.Fixed(300<<16), u.Z)
		b := spawnAirBasePad(t, w, sys.Terrain, "padB", u.X+numeric.Fixed(600<<16), u.Z)
		rebuildAirBases(sys, 30)
		n := &orders.Node{Owner: u.Handle}
		before := sim.Draws()
		if !sys.airFindBaseAndLand(u, n, sim, 1) {
			t.Fatal("two candidates must take the land branch [04 R-AIR-01 §11]")
		}
		if d := sim.Draws() - before; d != 1 {
			t.Fatalf("a two-candidate pick drew %d times, want exactly 1 [04 R-AIR-01 §11][I4]", d)
		}
		head := orders.QueueForUnit(u).Primary()[0]
		if head.Target != a.Handle && head.Target != b.Handle {
			t.Fatalf("the landing record targets %d, want one of the two pads [04 R-AIR-01 §11]", head.Target)
		}
	})
}

// TestAirToGroundPhaseFourSeeksAPad locks `AirToGround` phase 4 as an ordinary
// pad-seeking caller [04 R-AIR-01 §8][04 R-AIR-01 §11]. Below three quarters
// health it runs the base scan and, when the scan offers anything, releases the
// payload, draws one bounded value, head-inserts a `VTOL_Landing` at the drawn
// pad, clears the gate word and *restarts*. With no pad in reach it falls into
// the break leg instead, which spends its own `random below 2` and returns 1 —
// the health test alone never ends the visit.
func TestAirToGroundPhaseFourSeeksAPad(t *testing.T) {
	t.Run("a damaged strafer with pads in reach lands", func(t *testing.T) {
		sys, w, u, sim := airBaseSeekFixture(t)
		spawnAirBasePad(t, w, sys.Terrain, "padclose", u.X+numeric.Fixed(300<<16), u.Z)
		spawnAirBasePad(t, w, sys.Terrain, "padclose2", u.X+numeric.Fixed(600<<16), u.Z)
		rebuildAirBases(sys, 30)
		if got := sys.airBaseCandidates(u); len(got) != 2 {
			t.Fatalf("fixture: the scan offers %d candidates, want 2 [04 R-AIR-01 §11]", len(got))
		}

		u.Health = (u.Def.MaxDamage >> 2) * 3 // one above the strict threshold...
		u.Health--                            // ...and now below it
		n := pushAirOrder(t, u, "AirToGround", u.X+numeric.Fixed(1<<16), u.Z)
		n.Phase = 4
		before := sim.Draws()
		code := sys.legAirToGround(u, n, 1)

		if code != 0 {
			t.Fatalf("phase 4 with pads in reach returned %d, want 0 (*restart*) [04 R-AIR-01 §8]", code)
		}
		if d := sim.Draws() - before; d != 1 {
			t.Fatalf("the land branch drew %d times, want exactly 1 — the bounded pick over the "+
				"candidate count, and no break-leg draw [04 R-AIR-01 §11][I4]", d)
		}
		if !headIsLanding(u) {
			t.Fatal("the land branch head-inserts a VTOL_Landing record [04 R-AIR-01 §11]")
		}
		if n.DynamicGate != 0 {
			t.Fatalf("the land branch clears the gate word, got %#x [04 R-AIR-01 §11]", n.DynamicGate)
		}
	})

	t.Run("a damaged strafer with no pad breaks away", func(t *testing.T) {
		sys, _, u, sim := airBaseSeekFixture(t)
		rebuildAirBases(sys, 30)
		if got := sys.airBaseCandidates(u); len(got) != 0 {
			t.Fatalf("fixture: the scan offers %d candidates, want none [04 R-AIR-01 §11]", len(got))
		}

		u.Health = (u.Def.MaxDamage>>2)*3 - 1
		n := pushAirOrder(t, u, "AirToGround", u.X+numeric.Fixed(1<<16), u.Z)
		n.Phase = 4
		before := sim.Draws()
		code := sys.legAirToGround(u, n, 1)

		if code != 1 {
			t.Fatalf("phase 4 with no pad returned %d, want 1 — it falls into the break leg "+
				"[04 R-AIR-01 §8]", code)
		}
		if d := sim.Draws() - before; d != 1 {
			t.Fatalf("the break leg drew %d times, want exactly 1 (its `random below 2`); the empty "+
				"scan spends nothing [04 R-AIR-01 §11][I4]", d)
		}
		if headIsLanding(u) {
			t.Fatal("an empty scan must not land the attacker [04 R-AIR-01 §11]")
		}
	})
}

// TestAirBaseRegistryIsStaleBetweenRebuilds locks the cadence and the two
// staleness consequences it buys [06 §3.1][04 R-AIR-01 §11].
//
// The third list is cleared and refilled only when the tick satisfies the
// registry's 30-tick throttle. Between rebuilds the scan reads the snapshot,
// and because it re-tests the three admission flags but never liveness:
//
//   - a pad that died inside the window is still offered — the landing order's
//     own pad query is what rejects it later [04 R-AIR-01 §6];
//   - a pad that finished building inside the window is not offered until the
//     next rebuild.
func TestAirBaseRegistryIsStaleBetweenRebuilds(t *testing.T) {
	t.Run("a pad that died inside the window is still offered", func(t *testing.T) {
		sys, w, u, _ := airBaseSeekFixture(t)
		pad := spawnAirBasePad(t, w, sys.Terrain, "paddoomed", u.X+numeric.Fixed(300<<16), u.Z)
		sys.BeginTick(30)
		if got := sys.airBaseCandidates(u); len(got) != 1 || got[0] != pad.Handle {
			t.Fatalf("after the rebuild the scan offers %v, want the one pad", got)
		}

		// The pad takes a lethal hit: its death latch is set. No rebuild runs,
		// so its handle stays on the list, and the scan re-tests the three
		// admission flags but not the latch [04 R-AIR-01 §11].
		pad.Dying = true
		for tick := uint32(31); tick < 60; tick++ {
			sys.BeginTick(tick)
		}
		if got := sys.airBaseCandidates(u); len(got) != 1 || got[0] != pad.Handle {
			t.Fatalf("inside the window the scan offers %v, want the dead pad still offered [04 R-AIR-01 §11]", got)
		}

		// The next rebuild drops it: membership at rebuild DOES test the alive
		// bit and the death latch [06 §3.1].
		sys.BeginTick(60)
		if got := sys.airBaseCandidates(u); len(got) != 0 {
			t.Fatalf("after the next rebuild the scan offers %v, want nothing [06 §3.1]", got)
		}
	})

	t.Run("a pad completed inside the window is not offered yet", func(t *testing.T) {
		sys, w, u, _ := airBaseSeekFixture(t)
		pad := spawnAirBasePad(t, w, sys.Terrain, "padlate", u.X+numeric.Fixed(300<<16), u.Z)
		pad.Remaining = 0.5 // still a nanoframe at the rebuild
		sys.BeginTick(30)
		if got := sys.airBaseCandidates(u); len(got) != 0 {
			t.Fatalf("an unfinished pad is offered %v, want nothing [06 §3.1]", got)
		}

		pad.Remaining = 0 // finishes inside the window
		for tick := uint32(31); tick < 60; tick++ {
			sys.BeginTick(tick)
		}
		if got := sys.airBaseCandidates(u); len(got) != 0 {
			t.Fatalf("a pad completed inside the window is offered %v, want nothing until the next rebuild [04 R-AIR-01 §11]", got)
		}

		sys.BeginTick(60)
		if got := sys.airBaseCandidates(u); len(got) != 1 || got[0] != pad.Handle {
			t.Fatalf("after the next rebuild the scan offers %v, want the finished pad", got)
		}
	})
}

// TestAirBaseRegistryFilesAlliedPads locks the rebuild's friendly test: the
// candidate owner's one-directional alliance row toward the registry's ally
// group [05 R-SHARE-01 §1][06 §3.1]. An ally's pad is a candidate; a pad whose
// owner has not declared toward this group is not, even when this group has
// declared toward it.
func TestAirBaseRegistryFilesAlliedPads(t *testing.T) {
	sys, w, u, sim := airBaseSeekFixture(t)
	ally := spawnAirBasePadFor(t, w, sys.Terrain, "padally", 1, u.X+numeric.Fixed(300<<16), u.Z)
	oneWay := spawnAirBasePadFor(t, w, sys.Terrain, "padoneway", 2, u.X+numeric.Fixed(600<<16), u.Z)

	// Row A of `from` indexed by `toward`: only player 1 declares toward the
	// aircraft's group 0.
	orders.QueueForUnit(u).SetBinding(&orders.QueueBinding{
		SimRNG: sim,
		Lookup: w.Unit,
		World: &orders.WorldQueryAdapter{
			DeclaresAlliance: func(from, toward uint8) bool { return from == 1 && toward == 0 },
		},
	})
	sys.BeginTick(30)

	got := sys.airBaseCandidates(u)
	if len(got) != 1 || got[0] != ally.Handle {
		t.Fatalf("scan offers %v, want just the allied pad %d — the row read is the candidate owner's [05 R-SHARE-01 §1]", got, ally.Handle)
	}
	_ = oneWay
}

// TestStalePadIsOfferedThenRejectedByTheLandingOrder is the pair the staleness
// exists to produce [04 R-AIR-01 §11]: the scan offers a pad whose death latch
// was set inside the window, because it re-tests the three admission flags and
// not the latch, and the landing order's own pad query is what refuses it
// [04 R-AIR-01 §6] — the seek does not need to know the pad is gone.
func TestStalePadIsOfferedThenRejectedByTheLandingOrder(t *testing.T) {
	sys, w, u, sim := airBaseSeekFixture(t)
	px, pz := world.CellToWorld(18), world.CellToWorld(8)
	pad := spawnAirBasePad(t, w, sys.Terrain, "padstale", px, pz)
	sys.EnsureUnit(pad)
	sys.BeginTick(30)

	// The pad dies. The list is not refilled, so the scan still offers it.
	pad.Dying = true
	offered := sys.airBaseCandidates(u)
	if len(offered) != 1 || offered[0] != pad.Handle {
		t.Fatalf("the scan offers %v, want the death-latched pad still offered [04 R-AIR-01 §11]", offered)
	}

	n := &orders.Node{Owner: u.Handle}
	if !sys.airFindBaseAndLand(u, n, sim, 31) {
		t.Fatal("the land branch must take a stale candidate [04 R-AIR-01 §11]")
	}
	head := orders.QueueForUnit(u).Primary()[0]
	if head.Target != pad.Handle {
		t.Fatalf("the landing record targets %d, want the stale pad %d", head.Target, pad.Handle)
	}

	// The death finalizer clears the slot. The landing order's entry guard is
	// what aborts: the aircraft never parks on a pad that is gone.
	pad.Alive = false
	for tick := uint32(31); tick <= 400; tick++ {
		runMovementTick(sys, tick, w)
	}
	if u.Attachment.Carrier == pad.Handle {
		t.Fatal("the aircraft parked on a destroyed pad: the landing order's pad query did not refuse it [04 R-AIR-01 §6]")
	}
}
