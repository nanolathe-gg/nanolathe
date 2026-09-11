package features

import (
	"testing"
)

// A sprite stamp starts a new accumulator; teardown intentionally retains the
// cell's old word until this boundary [05 R-FEAT-01 §3, §4].
func TestSpriteStampResetsDamageAndFormerFringe(t *testing.T) {
	for _, formerFringe := range []bool{false, true} {
		name := "damaged anchor"
		if formerFringe {
			name = "former fringe"
		}
		t.Run(name, func(t *testing.T) {
			svc, terrain := newDensePackService(t, 8, 8)
			old := defP1("old", 1, 1, "", "trees")
			old.Damage = 100
			cx, cz := 2, 2
			if formerFringe {
				old.FootprintX, old.FootprintZ = 2, 2
				cx, cz = 3, 3
			}
			if svc.PlaceAt(2, 2, old) == nil {
				t.Fatal("old feature placement refused")
			}
			if !formerFringe {
				svc.DamageFeature(2, 2, 90)
			}
			if terrain.PlotAt(int32(cx), int32(cz)).AnchorWord() == 0 {
				t.Fatal("fixture lacks retained damage/fringe data")
			}
			fresh := defP1("fresh", 1, 1, "", "trees")
			fresh.Damage = 100
			if svc.PlaceAt(cx, cz, fresh) == nil {
				t.Fatal("replacement refused")
			}
			if got := terrain.PlotAt(int32(cx), int32(cz)).AnchorWord(); got != 0 {
				t.Fatalf("new sprite damage=%d, want zero", got)
			}
			if svc.DamageFeature(cx, cz, 10) {
				t.Fatal("fresh 100-health sprite died on its first 10-damage hit")
			}
			if got := terrain.PlotAt(int32(cx), int32(cz)).AnchorWord(); got != 10 {
				t.Fatalf("first hit accumulated %d, want 10", got)
			}
		})
	}
}

// Placement owns its attachment and placer state, leaving other cell controls
// intact; teardown clears only attachment [05 R-FEAT-01 §3, §4].
func TestStampAndTeardownPreserveUnrelatedControlState(t *testing.T) {
	svc, terrain := newDensePackService(t, 8, 8)
	anchor, fringe := terrain.PlotAt(2, 2), terrain.PlotAt(3, 2)
	anchor.SetFlagByte(0xff)
	fringe.SetFlagByte(0xff)
	def := defP1("sprite", 2, 1, "", "trees")
	def.Damage = 100
	if svc.PlaceAt(2, 2, def) == nil {
		t.Fatal("placement refused")
	}
	if got := anchor.FlagByte(); got != 0xd6 {
		t.Fatalf("anchor controls=%#x, want preserved controls and neutral placer", got)
	}
	if got := fringe.FlagByte(); got != 0xfe {
		t.Fatalf("fringe controls=%#x, want only attachment cleared", got)
	}
	svc.DamageFeature(2, 2, 40)
	retainedFringe := fringe.AnchorWord()
	svc.RemoveFeatureAt(2, 2, CauseDead)
	if !anchor.IsEmpty() || !fringe.IsEmpty() {
		t.Fatal("teardown left a feature")
	}
	if anchor.FlagByte() != 0xd6 || fringe.FlagByte() != 0xfe {
		t.Fatal("teardown changed unrelated controls")
	}
	if anchor.AnchorWord() != 40 || fringe.AnchorWord() != retainedFringe {
		t.Fatal("teardown erased retained damage or fringe data")
	}
}
