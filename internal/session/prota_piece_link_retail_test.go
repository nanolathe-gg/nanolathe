//go:build retail

package session

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestProTAScriptPiecesBeyondModelStillCreate locks the retail link pass on
// the two shipped ProTA 4.8 units whose compiled script declares one trailing
// piece their 3DO lacks: CORSILO's `blastpt` and CORAMPH's `launch`. Retail
// never refuses a bind over piece names; the trailing piece simply has no
// render record [04 R-COB-01 §4]. Before that contract a Core player could
// never create either unit. Neither script names its trailing piece in any
// piece operation, so Create, Aim and Fire run exactly as authored.
func TestProTAScriptPiecesBeyondModelStillCreate(t *testing.T) {
	fs, cat, limits, profile := loadProTAArchive(t)
	cfg := DirectSkirmishConfig("ashap plateau")
	cfg.ApplyDefaults()
	cfg.Gameplay = gameplay.Modern
	cfg.RNGSimSeed, cfg.RNGCrtSeed = 7, 7
	s, err := NewSkirmishWithEntryOptions(fs, cat, cfg, SkirmishEntryOptions{
		CommunitySources: CommunitySources{Content: profile.GameplaySources()},
		ContentLimits:    limits,
	})
	if err != nil {
		t.Fatalf("compose ProTA skirmish: %v", err)
	}
	advanceProTATicks(t, s, 2)

	for _, tc := range []struct {
		unit, trailing string
		x, z           int64
	}{
		{"CORSILO", "blastpt", 900, 900},
		{"CORAMPH", "launch", 1100, 900},
	} {
		u := placeCompleteRetailUnit(t, s, tc.unit, 1, numeric.FixedFromInt(tc.x), numeric.FixedFromInt(tc.z))
		binding := u.COBBinding()
		if binding == nil || !binding.CreateInvoked {
			t.Fatalf("%s created without a Create-invoked binding", tc.unit)
		}
		last := len(binding.PieceMap) - 1
		if len(binding.PieceMap) != len(binding.Model.Pieces)+1 || binding.PieceMap[last] != -1 || !strings.EqualFold(binding.Program.Pieces[last], tc.trailing) {
			t.Fatalf("%s piece map %v over %d model pieces, want only trailing %q unbacked", tc.unit, binding.PieceMap, len(binding.Model.Pieces), tc.trailing)
		}
		if len(binding.LinkNotes) != 1 || binding.LinkNotes[0].Code != cob.BindingPieceCount {
			t.Fatalf("%s link notes = %#v, want one beyond-model note", tc.unit, binding.LinkNotes)
		}

		// The aim handshake completes through the authored script: CORSILO's
		// waits on its launch-state static after opening the silo, CORAMPH's
		// turns its launcher.
		granted := false
		aim := binding.Callbacks.Aim(cob.WeaponPrimary, 0, 0, func(ret cob.CallbackReturn) {
			if ret.Value != 0 {
				granted = true
			}
		})
		if !aim.Started {
			t.Fatalf("%s AimPrimary did not start", tc.unit)
		}
		for i := 0; i < 900 && !granted; i++ {
			s.Econ.Players[1].Stock[economy.Metal] = 10000
			s.Econ.Players[1].Stock[economy.Energy] = 10000
			advanceProTATicks(t, s, 1)
		}
		if !granted || !u.Alive {
			t.Fatalf("%s AimPrimary never granted readiness (alive=%v)", tc.unit, u.Alive)
		}
		if fire := binding.Callbacks.Fire(cob.WeaponPrimary); !fire.Started {
			t.Fatalf("%s FirePrimary did not start", tc.unit)
		}
		advanceProTATicks(t, s, 2)
	}

	// The ordinary build path reaches the same binder through a nanoframe.
	builder := retailUnit(s, 1, "CORCOM")
	if builder == nil {
		t.Fatal("ProTA skirmish has no Core commander")
	}
	x, z := retailBuildSite(t, s, cat, builder, "CORSILO")
	if err := construction.QueueMobileBuild(builder, "CORSILO", x, z, 1, cat); err != nil {
		t.Fatalf("queue CORSILO: %v", err)
	}
	var frame *units.Unit
	for i := 0; i < 1200 && frame == nil; i++ {
		s.Econ.Players[1].Stock[economy.Metal] = 10000
		s.Econ.Players[1].Stock[economy.Energy] = 10000
		advanceProTATicks(t, s, 1)
		for _, u := range s.Units.IterSliced() {
			if u != nil && u.Alive && u.Owner == 1 && u.Remaining > 0 && u.Def != nil && strings.EqualFold(u.Def.UnitName, "CORSILO") {
				frame = u
			}
		}
	}
	if frame == nil || frame.COBBinding() == nil {
		t.Fatal("the Core commander never raised a bound CORSILO nanoframe")
	}
}
