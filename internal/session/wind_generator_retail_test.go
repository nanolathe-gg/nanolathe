//go:build retail

package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestRetailConstructedWindGeneratorFollowsEveryRedraw is the end-to-end
// guard for issue #195. A wind generator the commander builds is paid
// `currentWindScalar × windgenerator` at every settlement pass, and the scalar
// it multiplies is the one published before that tick's economy phase, so the
// payment follows each phase-8 redraw rather than the value at completion
// [05 "Wind generation"][05 R-ECO-01 §2][05 R-PROD-01 §3]. The redraw's
// script pair (SetDirection, SetSpeed) never touches the activation bit the
// generator arm hangs off [05 R-PROD-01 §2].
//
// The ARMWIN script is the authored corpus; the test asserts the relationship
// at each pass, not a census of values. Both reserved rule sets are checked:
// no Modern policy touches this path.
func TestRetailConstructedWindGeneratorFollowsEveryRedraw(t *testing.T) {
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Modern} {
		t.Run(string(mode), func(t *testing.T) {
			f := loadRetailFixture(t)
			f.cfg.Gameplay = mode
			s := f.session(t)
			const key = "ARMWIN"
			com := retailUnit(s, 0, retailARM)
			if com == nil {
				t.Fatal("ARM commander not spawned")
			}
			x, z := retailBuildSite(t, s, f.cat, com, key)
			if err := construction.QueueMobileBuild(com, key, x, z, 1, f.cat); err != nil {
				t.Fatalf("queue %s: %v", key, err)
			}
			var gen *units.Unit
			for i := 0; i < 3000 && (gen == nil || gen.Remaining != 0); i++ {
				stepRetail(s, 1)
				gen = retailUnit(s, 0, key)
			}
			if gen == nil || gen.Remaining != 0 {
				t.Fatalf("%s was not completed", key)
			}
			if !(gen.Def.WindGenerator > 0) {
				t.Fatalf("%s authors no windgenerator", key)
			}
			generator := float64(float32(gen.Def.WindGenerator))

			redraws, paidAfterThirdRedraw := 0, 0
			for i := 0; i < 3000 && paidAfterThirdRedraw < 3; i++ {
				// The settlement of tick N reads the scalar tick N-1's
				// wind phase published [05 R-PROD-01 §3].
				scalar := s.Econ.WindScalar()
				stepRetail(s, 1)
				tick := s.Clock.GlobalTick
				if s.Wind.Changed {
					redraws++
				}
				if s.Econ.ActivityReportTick(0) != tick {
					continue // no settlement pass for the owner this tick
				}
				if !gen.Alive || !gen.Activated {
					t.Fatalf("tick %d after %d redraws: generator alive=%v activated=%v", tick, redraws, gen.Alive, gen.Activated)
				}
				want := float32(scalar)
				want = float32(float64(want) * generator)
				if got := s.Econ.UnitArchived(gen.Handle)[economy.Energy].Production; got != want {
					t.Fatalf("tick %d after %d redraws: generator paid %v, want scalar %v × %v = %v", tick, redraws, got, scalar, generator, want)
				}
				if redraws >= 3 && want > 0 {
					paidAfterThirdRedraw++
				}
			}
			if paidAfterThirdRedraw < 3 {
				t.Fatalf("observed %d redraws and %d positive passes after the third; the scenario no longer exercises repeated redraws", redraws, paidAfterThirdRedraw)
			}
		})
	}
}
