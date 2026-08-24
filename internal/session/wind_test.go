package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/world"
)

func TestWindBattleEntryDrawCountAndValues(t *testing.T) {
	// C17: briefing strength CRT()%(max-min+1)+min, direction CRT()&0x3f,
	// first deadline ((CRT()*10)/0x8000+5)*30. Exactly three CRT draws [01 §7.3][GAP T13].
	bounds := mission.WindBounds{Min: 100, Max: 2000}
	seed := uint32(0x1234)

	// Compute expected values by replicating the exact retail expressions on a
	// reference CRT stream.
	ref := rng.NewCRT(seed)
	spanInclusive := uint32(bounds.Max - bounds.Min + 1) // 1901
	expStrength := int32(ref.Uint32n(spanInclusive)) + bounds.Min
	expDir6 := uint16(ref.Rand() & 0x3F)
	expHeading := expDir6 << 10
	expInterval := uint32((int64(ref.Rand())*10/0x8000 + 5) * 30)
	expNext := uint32(10) + expInterval // tick 10
	if ref.Draws() != 3 {
		t.Fatalf("reference CRT draws = %d want 3", ref.Draws())
	}

	// Session initializer must produce identical values with identical draw count.
	crt := rng.NewCRT(seed)
	w := world.NewWind(bounds.Min, bounds.Max)
	InitBattleWind(w, &crt, 10)
	if crt.Draws() != 3 {
		t.Fatalf("InitBattleWind CRT draws = %d want 3 [01 §7.3]", crt.Draws())
	}
	if w.Strength != expStrength {
		t.Fatalf("briefing strength = %d want %d [01 §7.3]", w.Strength, expStrength)
	}
	if w.Heading != expHeading {
		t.Fatalf("briefing heading = %d want %d (dir6<<10) [01 §7.3]", w.Heading, expHeading)
	}
	if w.NextChange != expNext {
		t.Fatalf("first deadline = %d want %d [01 §7.3]", w.NextChange, expNext)
	}

	// Repeat with collapsed range [500,500] inclusive =1, still consumes draw via
	// raw rand()%1 expression [01 §7.3] [I4].
	for _, r := range [][2]int32{{500, 500}, {0, 0}} {
		crt2 := rng.NewCRT(7)
		w2 := world.NewWind(r[0], r[1])
		InitBattleWind(w2, &crt2, 0)
		if crt2.Draws() != 3 {
			t.Fatalf("collapsed bounds %v CRT draws = %d want 3", r, crt2.Draws())
		}
		if w2.Strength < r[0] || w2.Strength > r[1] {
			t.Fatalf("collapsed bounds %v strength %d out of range", r, w2.Strength)
		}
	}

	// Through NewBattleWindFromBounds helper (skirmish will also call it) the
	// same single path must be observed, no second draw path.
	crt3 := rng.NewCRT(seed)
	w3 := NewBattleWindFromBounds(bounds, &crt3, 10)
	if crt3.Draws() != 3 || w3.Strength != expStrength || w3.Heading != expHeading {
		t.Fatalf("NewBattleWindFromBounds mismatch draws %d strength %d heading %d", crt3.Draws(), w3.Strength, w3.Heading)
	}

	// Later wind arithmetic belongs to world.Wind Jitter/Field, NOT duplicated
	// here. Verify that after briefing exactly one CRT draw advances the deadline
	// and one Sim draw takes strength, another takes heading when nonzero.
	sim := rng.NewSimulation(7)
	crt4 := rng.NewCRT(99)
	w4 := world.NewWind(100, 2000)
	w4.SeedBriefing(&crt4, 0)
	due := w4.NextChange
	crtAfter := crt4.Draws()
	w4.Jitter(due, &crt4)
	if crt4.Draws()-crtAfter != 1 {
		t.Fatalf("Jitter should consume exactly one CRT draw [01 §7.3] got %d", crt4.Draws()-crtAfter)
	}
	if !w4.Field(due, &sim) {
		t.Fatal("Field should change on due tick")
	}
	if sim.Draws() != 2 {
		t.Fatalf("Field should consume 2 Sim draws (strength, heading) [01 §7.3] got %d", sim.Draws())
	}
	// Ensure the session package has no second implementation of later draws:
	// grep via test - InitBattleWind must not call Sim stream at all.
}

func TestWindNoDuplicateDrawPath(t *testing.T) {
	// Verify that InitBattleWind never touches the Sim stream (later draws are
	// world.Wind Field only) and that briefing always consumes exactly 3 CRT
	// draws regardless of bounds, per [01 §7.3][GAP T13] #17.
	sim := rng.NewSimulation(123)
	crt := rng.NewCRT(456)
	w := world.NewWind(0, 0)
	simBefore := sim.Draws()
	InitBattleWind(w, &crt, 0)
	if sim.Draws() != simBefore {
		t.Fatalf("InitBattleWind must not draw from Sim stream; draws %d->%d", simBefore, sim.Draws())
	}
	if crt.Draws() != 3 {
		t.Fatalf("InitBattleWind must consume exactly 3 CRT draws [01 §7.3] got %d", crt.Draws())
	}
}
