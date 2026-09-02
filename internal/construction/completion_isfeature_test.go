package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestCompletionMarksIsFeatureProductWithCause7 locks the completion
// transition's capability-bit-24 arm: `isfeature` marks the product a feature
// stand-in with a DIRECT store of death-cause byte 7 plus the death latch, not
// a damage packet [04 R-SPEC-01 §12][06 §12.1]. Dragon's teeth and the two
// floating forts are the six stock definitions that take this arm.
//
// The arm previously read `init_cloaked` and wrote a cloak posture, from doc 04
// §3.8's mislabel of bit 24; that reading was corrected by RWU-19-26.
func TestCompletionMarksIsFeatureProductWithCause7(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	def := newProductDef("armdrag", 1, 1, 1, 100)
	def.IsFeature = true
	cat.Units[def.CanonicalKey] = def
	w := newConstructionFixtureWorld(4, cat)
	h, _ := w.Create(def, 0, 0, 0, 0)
	product := w.Unit(h)
	product.Remaining = 0.5

	svc := NewService(nil, cat, w, nil)
	svc.applyCompletionPosture(product)

	if !product.Dying {
		t.Fatal("isfeature completion left the death latch clear [04 R-SPEC-01 §12]")
	}
	if got := combat.Cause(product.LastDamageCause); got != combat.CauseFeatureConversion {
		t.Fatalf("isfeature completion stamped cause %d, want 7 [06 §12.1]", got)
	}
	if product.Flags&FlagCompleted == 0 || product.Remaining != 0 {
		t.Fatalf("isfeature completion skipped the ordinary posture: flags=%x remaining=%v", product.Flags, product.Remaining)
	}
}

// TestCompletionLeavesTheCloakBitAlone is the negative half: the completion
// transition never reads `init_cloaked` and never writes either cloak bit. The
// cloak-requested bit a product carries is the one its constructor seeded
// [05 R-ECO-01 §9][03 R-VIS-01 §6] (RWU-19-26).
func TestCompletionLeavesTheCloakBitAlone(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	def := newProductDef("armmine1", 1, 1, 1, 100)
	def.InitCloaked = true
	cat.Units[def.CanonicalKey] = def
	w := newConstructionFixtureWorld(4, cat)
	h, _ := w.Create(def, 0, 0, 0, 0)
	product := w.Unit(h)
	// Whatever the constructor seeded, completion is not a second writer: clear
	// the bit here and it must still be clear afterwards.
	product.IsCloaked = false

	svc := NewService(nil, cat, w, nil)
	svc.applyCompletionPosture(product)

	if product.IsCloaked {
		t.Fatal("completion set the cloak bit from init_cloaked [04 R-SPEC-01 §12]")
	}
	if product.Dying {
		t.Fatal("completion latched death on a product that is not isfeature [04 R-SPEC-01 §12]")
	}
	if product.LastDamageCause != 0 {
		t.Fatalf("completion stamped death cause %d on a non-isfeature product", product.LastDamageCause)
	}
	// And the constructor is the writer that does seed it [05 R-ECO-01 §9].
	fresh := &units.Unit{Def: def}
	fresh.InitEconomyState()
	if !fresh.IsCloaked {
		t.Fatal("the constructor did not seed the cloak-requested bit from init_cloaked [05 R-ECO-01 §9]")
	}
}
