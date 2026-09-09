package economy

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestActivationBitIsTheEconomyBranchGate locks the separation that
// units.InitEconomyState used to conflate: the engine-state activation bit is
// one thing, written only by the edge machine [04 R-UNIT-06 §2], and the
// economy's building branch reads that same bit rather than a stand-in
// [05 R-ECO-01 §2].
//
// Everything asserted here is Established, not inference:
//
//   - A unit is created inactive whatever it authors. `activatewhenbuilt` is
//     not a creation-time copy of the bit — it raises the edge through the
//     shared setter at pre-built creation and at build completion
//     [04 R-SPEC-01 §12] — and `onoffable` gates only the Activate/Deactivate
//     order handlers [04 R-SPEC-01 §11].
//   - The building branch of the per-unit gather "runs only when the unit's
//     activated bit is set" [05 R-ECO-01 §2], so a definition that authors
//     neither key "never activates and never runs any generator"
//     [05 R-PROD-01 §2].
//   - The passive block hangs off completion, not activation, so `energymake`
//     and `metalmake` are paid on a completed unit regardless of the bit
//     [05 R-ECO-01 §2], [05 R-PROD-01 §2].
//
// The regression it exists to catch is the pinning that was here before:
// `Activated` forced true at creation for any definition authoring neither
// key. That silently made every stock factory active before its first product
// and swallowed the state-0 activate edge the script-owned yard-door handshake
// depends on [05 "Factory production lifecycle"].
func TestActivationBitIsTheEconomyBranchGate(t *testing.T) {
	// A stock factory authors neither key. Nothing may activate it at creation.
	factoryDef := economyFixtureDef(&content.UnitDef{
		UnitName: "edgelab", BuildTime: 100, MaxDamage: 100,
	})
	factoryDef.CanonicalKey = content.CanonicalKey("edgelab")

	// A solar collector produces through negative `energyuse`, which is an arm
	// of the building branch and therefore gated on the bit; it authors both
	// keys, as every stock solar does, so completion activates it.
	solarDef := economyFixtureDef(&content.UnitDef{
		UnitName: "armsolar", EnergyUse: -20, BuildTime: 100, MaxDamage: 100,
		OnOffable: true, ActivateWhenBuilt: true,
	})
	solarDef.CanonicalKey = content.CanonicalKey("armsolar")

	w := units.NewSliced(10, &content.Catalog{})
	svc := &Service{}
	svc.Players[0].Exists = true
	svc.Players[0].ControllerState = 1

	facH, err := w.Create(factoryDef, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create factory: %v", err)
	}
	solH, err := w.Create(solarDef, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create solar: %v", err)
	}
	factory, solar := w.Unit(facH), w.Unit(solH)

	// The creation state, per [04 R-SPEC-01 §12]: a definition authoring
	// neither key is created inactive and nothing raises it.
	if factory.Activated {
		t.Fatal("a factory authoring neither onoffable nor activatewhenbuilt was created active: the state-0 activate edge will be swallowed")
	}
	// The same call is already-built creation for a definition that DOES author
	// `activatewhenbuilt`, so site 1 raised its edge.
	if !solar.Activated {
		t.Fatal("an already-built activatewhenbuilt unit was not activated at creation")
	}

	svc.PerUnitProductionFills(0, w)

	// The relationship: the completed solar collector's negative-energyuse
	// refund reaches energy production, and it does so because its bit is set.
	if got := svc.UnitBuckets(solH)[Energy].Production; got != 20 {
		t.Fatalf("completed solar energy production = %v, want 20", got)
	}
	// The inactive factory contributes nothing from the building branch.
	if got := svc.UnitBuckets(facH)[Energy].Production; got != 0 {
		t.Fatalf("inactive factory produced %v energy, want 0", got)
	}

	// Lowering the solar's bit stops the branch arm, and only that arm: this is
	// the same bit both sides read, not two fields that can drift.
	solar.SetActivationEdge(false)
	if solar.Activated {
		t.Fatal("SetActivationEdge(false) left the bit set")
	}
	*svc.UnitBuckets(solH) = [2]Bucket{}
	svc.PerUnitProductionFills(0, w)
	if got := svc.UnitBuckets(solH)[Energy].Production; got != 0 {
		t.Fatalf("deactivated solar still refunded %v energy: the economy is not reading the engine bit", got)
	}
}

// TestPassiveProductionIgnoresTheActivationBit locks the other half of
// [05 R-PROD-01 §2]: `energymake` and `metalmake` hang off the completion gate,
// never the activation bit, so removing the creation-time pinning cannot
// silence a passive producer. "Switching a solar collector off does not stop
// its energymake" [05 R-PROD-01 §2].
func TestPassiveProductionIgnoresTheActivationBit(t *testing.T) {
	def := economyFixtureDef(&content.UnitDef{
		UnitName: "passive", EnergyMake: 25, MetalMake: 3,
		BuildTime: 100, MaxDamage: 100,
	})
	def.CanonicalKey = content.CanonicalKey("passive")

	w := units.NewSliced(10, &content.Catalog{})
	svc := &Service{}
	svc.Players[0].Exists = true
	svc.Players[0].ControllerState = 1

	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	u := w.Unit(h)
	if u.Activated {
		t.Fatal("a definition authoring neither key was created active")
	}
	if u.Remaining != 0 {
		t.Fatalf("already-built creation left remaining=%v, want 0", u.Remaining)
	}

	svc.PerUnitProductionFills(0, w)
	if got := svc.UnitBuckets(h)[Energy].Production; got != 25 {
		t.Fatalf("passive energy production = %v, want 25 with the activation bit low", got)
	}
	if got := svc.UnitBuckets(h)[Metal].Production; got != 3 {
		t.Fatalf("passive metal production = %v, want 3 with the activation bit low", got)
	}
}
