package units

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// smokeCounter records every emit-sfx the script reaches the presentation sink
// with, so the test can assert the absence of smoke rather than its rendering.
type smokeCounter struct{ puffs int }

func (s *smokeCounter) EmitSFX(_ int, _ int32, kind cob.SFXKind) {
	if kind == cob.SFXWhiteSmoke || kind == cob.SFXBlackSmoke {
		s.puffs++
	}
}

// TestSmokeUnitPortsGateDamageSmoke locks the two engine ports that decide
// whether a unit smokes, against the stock damage-smoke helper that reads
// them.
//
// The helper is retail content, shipped inside the retail archive as
// `scripts/SMOKEUNIT.H` and included by most stock unit scripts. It waits on
// `while (get BUILD_PERCENT_LEFT) sleep 400;` — "wait until the unit is
// actually built" — and then loops forever, emitting one white or black smoke
// puff whenever `get HEALTH` is below 66.
//
// Both reads are engine ports [04 §4.4]: port 17 is the remaining-build
// fraction (100 for a fresh nanoframe, 0 once complete) and port 4 is
// health×100/maxdamage. An unbound port reads zero, and zero is a meaningful
// wrong answer to each: a nanoframe walks straight through the wait loop, and
// a unit at full health then reads 0 < 66 and smokes forever. That pair is
// what this test exists to catch, because every other part of the chain keeps
// working when it breaks.
func TestSmokeUnitPortsGateDamageSmoke(t *testing.T) {
	fs := vfs.New()
	if err := fs.MountGameDirectory(testsupport.RetailRoot(t)); err != nil {
		t.Fatalf("mount: %v", err)
	}
	defer fs.Close()

	prog, ok, err := cob.LoadFromFS(fs, "armstump")
	if err != nil || !ok || prog == nil {
		t.Skipf("armstump script unavailable: %v", err)
	}
	if _, ok := prog.Scripts["SmokeUnit"]; !ok {
		t.Fatalf("armstump carries no SmokeUnit script")
	}

	// One puff every 200 ms at worst, so 600 ticks (20 s) is far past the
	// helper's own `sleep 400` wait-loop period and leaves no room for a
	// smoking unit to pass by staying asleep.
	const ticks = 600

	run := func(health, maxHealth int32, remaining float32) int {
		u := &Unit{Health: health, MaxHealth: maxHealth, Remaining: remaining}
		vm := cob.NewVM(prog)
		sink := &smokeCounter{}
		vm.SetSFXSink(sink)
		vm.SetSFXVisible(func(int, int32) bool { return true })
		for port, fn := range unitPortHandlers(vm, u) {
			vm.BindPort(port, fn)
		}
		if !vm.StartByName("SmokeUnit", nil) {
			t.Fatalf("SmokeUnit did not start")
		}
		for i := 0; i < ticks; i++ {
			vm.Drain(1)
		}
		return sink.puffs
	}

	// A nanoframe: health seeded to 0 and the remaining fraction 1.0 [04 §4.4
	// ports 4 and 17]. It is being built, not damaged, and must not smoke.
	if puffs := run(0, 100, 1.0); puffs != 0 {
		t.Errorf("nanoframe emitted %d smoke puffs; a unit under construction does not smoke", puffs)
	}
	// The same unit finished and undamaged: port 4 reads 100, which is not
	// below the helper's 66.
	if puffs := run(100, 100, 0); puffs != 0 {
		t.Errorf("undamaged unit emitted %d smoke puffs", puffs)
	}
	// Finished and damaged below the helper's threshold: this is the one case
	// that smokes.
	if puffs := run(50, 100, 0); puffs == 0 {
		t.Errorf("damaged unit emitted no smoke")
	}
}

// TestNanoframeIsUnfinishedBeforeCreateRuns locks the ordering half of the
// same contract: a nanoframe's construction state is seeded by the creation
// act itself [05 "Nanoframe allocation"], before the script bind runs
// `Create`.
//
// `Create` is a wake callback whose drain runs all eight thread slots inline
// at the creation site [04 R-CB-01 §2], so a thread it starts reads the unit's
// ports before the creation call returns. Seeding a nanoframe as built and
// demoting it afterwards leaves that thread reading a finished, undamaged
// unit — which is precisely what walks the stock damage-smoke helper past its
// build gate. The binder stands in for the `Create` drain here because it is
// the site that runs it.
func TestNanoframeIsUnfinishedBeforeCreateRuns(t *testing.T) {
	w := newFixtureWorld(4, nil)
	var atBind struct {
		remaining float32
		health    int32
	}
	w.SetCOBBinder(func(u *Unit) error {
		atBind.remaining, atBind.health = u.Remaining, u.Health
		return nil
	})
	def := &content.UnitDef{UnitName: "nanoframe", MaxDamage: 100, Limit: -1}
	if _, err := w.CreateNanoframe(def, 0, 0, 0, 0); err != nil {
		t.Fatalf("CreateNanoframe: %v", err)
	}
	if atBind.remaining != 1 || atBind.health != 0 {
		t.Errorf("script bind saw remaining=%v health=%d; a fresh nanoframe is remaining 1.0 at health 0 [04 §4.4 ports 4 and 17]",
			atBind.remaining, atBind.health)
	}

	// The already-built form is unchanged: full health, nothing remaining.
	atBind.remaining, atBind.health = -1, -1
	if _, err := w.Create(&content.UnitDef{UnitName: "complete", MaxDamage: 100, Limit: -1}, 0, 0, 0, 0); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if atBind.remaining != 0 || atBind.health != 100 {
		t.Errorf("script bind saw remaining=%v health=%d for a completed unit; want 0 and 100",
			atBind.remaining, atBind.health)
	}
}
