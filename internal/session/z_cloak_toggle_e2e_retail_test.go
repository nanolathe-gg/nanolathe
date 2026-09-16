// Retail-assets end-to-end check for the reported cloak regression: the side
// rail's CLOAK gadget produced no effect at all, because the command it
// transmits had no kind at the session boundary and no arm in the panel's
// click handler. The simulation half — the order handlers, the debit gate, the
// settlement transition and the visibility predicate — was already whole, so
// this test drives the input boundary and asserts the whole chain behind it
// [04 R-STANCE-01 §2][04 R-ORD-01 §2][05 R-ECO-01 §9][03 R-VIS-01 §6].
// Skipped when ~/TotalAnnihilation is absent.
package session

import (
	"sort"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestCloakToggleEndToEndRetail locks the four contracts the play-test report
// named, in the order the chain runs them:
//
//  1. the CLOAK press transmits `Cloak_On` through the selection broadcast and
//     the order handler sets the cloak-REQUESTED bit behind the definition's
//     can-cloak capability [04 R-STANCE-01 §2][04 R-ORD-01 §2];
//  2. a pass the owner can pay for sets the INSTANCE cloaked bit, which is
//     what hides the unit from an enemy's visibility predicate
//     [05 R-ECO-01 §9][03 §3.2];
//  3. a pass the owner cannot pay for clears that bit with no partial payment,
//     so a stalled owner's cloaked units show again [05 "Cloak debit"] step 6;
//  4. a second press transmits `Cloak_Off`, which clears the request and
//     nothing else — the unit stays hidden until the next settlement finds the
//     gate no longer due [04 R-ORD-01 §2][05 R-ECO-01 §9].
//
// settleWindowTicks is one settlement deadline plus slack: the economy settles
// a player every thirty ticks [05 "Authoritative settlement order"], and the
// cloak debit is step 2 of that pass, so a cloak state change becomes visible
// on the instance bit within one window rather than on the next tick.
const settleWindowTicks = 40

func TestCloakToggleEndToEndRetail(t *testing.T) {
	cat, fs := retailcat.Shared(t)

	mapKeys := make([]string, 0, len(cat.Maps))
	for k := range cat.Maps {
		mapKeys = append(mapKeys, k)
	}
	sort.Strings(mapKeys)
	mapKey := ""
	for _, k := range mapKeys {
		for _, sch := range cat.Maps[k].Schemas {
			if len(sch.Type) >= 7 && sch.Type[:7] == "Network" {
				mapKey = k
				break
			}
		}
		if mapKey != "" {
			break
		}
	}
	if mapKey == "" {
		t.Skip("no Network map in catalog")
	}

	cfg := SkirmishConfig{MapName: mapKey, NumPlayers: 2}
	cfg.ApplyDefaults()
	cfg.Players[0].Controller = 0 // human
	cfg.Players[1].Controller = 1 // computer
	sess, err := NewSkirmishWithProgress(fs, cat, cfg, nil)
	if err != nil {
		t.Fatalf("skirmish: %v", err)
	}
	driver := int32(1 << 20)
	stepOne := func() { sess.Step(driver); driver++ }
	for tick := 0; tick < 60 && sess.State != StateBattle; tick++ {
		stepOne()
	}
	if sess.State != StateBattle {
		t.Fatalf("shell did not enter battle")
	}

	local := int(sess.LocalOwner)
	var com *units.Unit
	enemy := -1
	for _, u := range sess.Units.IterSliced() {
		if u == nil || !u.Alive || u.Def == nil || !u.Def.Commander {
			continue
		}
		if int(u.Owner) == local {
			com = u
		} else if enemy < 0 {
			enemy = int(u.Owner)
		}
	}
	if com == nil || enemy < 0 {
		t.Skip("skirmish placed no commander pair")
	}
	// The can-cloak capability is derived at definition load as cloakcost > 0
	// [05 "which units can request cloak at all"]. A commander that does not
	// author it is not this test's subject.
	if com.Def.CloakCost <= 0 {
		t.Skipf("commander %q authors no cloakcost", com.Def.CanonicalKey)
	}

	// Whether the enemy can see the commander at all decides how much the
	// visibility half of the check can assert: the negative direction
	// ("cloaked, therefore not visible") holds at any range, but the positive
	// control only exists when the two start in sight of each other.
	seenBefore := sess.IsUnitVisible(enemy, com)

	// Select the commander through the ordinary command boundary, then press
	// CLOAK: the presentation arm resolves the direction from the published
	// pair (0 → on) and transmits one descriptor to the whole selection.
	if err := sess.EnqueueHumanCommand(HumanCommand{
		Kind: HumanSelectionReplace, Selection: HumanSelectionCommand{Handles: []pool.Handle{com.Handle}},
	}); err != nil {
		t.Fatalf("select: %v", err)
	}
	stepOne()
	if err := sess.EnqueueHumanCommand(HumanCommand{
		Kind: HumanCloak, Cloak: HumanCloakCommand{Cloak: true},
	}); err != nil {
		t.Fatalf("cloak on: %v", err)
	}

	// Contract 1: the request bit. The record is consumed on its single visit,
	// so a handful of ticks is generous.
	for i := 0; i < 8 && !com.IsCloaked; i++ {
		stepOne()
	}
	if !com.IsCloaked {
		t.Fatal("the CLOAK press never reached the cloak-requested bit [04 R-STANCE-01 §2][04 R-ORD-01 §2]")
	}

	// Contract 2: a paid pass sets the instance bit and the enemy loses sight
	// of the unit. Give the owner stock the debit can certainly afford; the
	// commander is idle, so the shared reveal/cloak-suppression deadline is
	// zero and the gate is due from the first pass [05 R-ECO-01 §9].
	// Settlement runs on the per-player deadline, one pass every thirty ticks
	// [05 "Authoritative settlement order"], so every instance-bit assertion
	// below waits for a settlement rather than a tick.
	sess.Econ.Players[local].Stock[economy.Energy] = 10000
	for i := 0; i < settleWindowTicks && !com.Hidden; i++ {
		stepOne()
	}
	if !com.Hidden {
		t.Fatal("a paid cloak pass did not set the instance cloaked bit [05 R-ECO-01 §9]")
	}
	if sess.IsUnitVisible(enemy, com) {
		t.Fatal("a cloaked unit is still visible to an enemy [03 §3.2] step 2")
	}

	// Contract 3: no partial payment. With less energy than the integerized
	// cost the debit refuses and the same pass clears the instance bit, while
	// the request survives untouched.
	sess.Econ.Players[local].Stock[economy.Energy] = 0
	for i := 0; i < settleWindowTicks && com.Hidden; i++ {
		stepOne()
	}
	if com.Hidden {
		t.Fatal("an unaffordable cloak pass left the unit hidden [05 \"Cloak debit\"] step 6")
	}
	if !com.IsCloaked {
		t.Fatal("an unaffordable pass cleared the cloak REQUEST; only Cloak_Off may [05 R-ECO-01 §9]")
	}

	// Contract 4: the second press. The pair now reads 1, so the arm sends
	// `Cloak_Off`, which clears the request; the next settlement finds the gate
	// no longer due and clears the instance bit.
	sess.Econ.Players[local].Stock[economy.Energy] = 10000
	for i := 0; i < settleWindowTicks && !com.Hidden; i++ {
		stepOne()
	}
	if !com.Hidden {
		t.Fatal("restoring the stock did not re-hide the cloaked unit [05 R-ECO-01 §9]")
	}
	if err := sess.EnqueueHumanCommand(HumanCommand{
		Kind: HumanCloak, Cloak: HumanCloakCommand{Cloak: false},
	}); err != nil {
		t.Fatalf("cloak off: %v", err)
	}
	for i := 0; i < 8 && com.IsCloaked; i++ {
		stepOne()
	}
	if com.IsCloaked {
		t.Fatal("Cloak_Off never reached the cloak-requested bit [04 R-ORD-01 §2]")
	}
	for i := 0; i < settleWindowTicks && com.Hidden; i++ {
		stepOne()
	}
	if com.Hidden {
		t.Fatal("the settlement after Cloak_Off left the instance cloaked bit set [05 R-ECO-01 §9]")
	}
	if seenBefore && !sess.IsUnitVisible(enemy, com) {
		t.Fatal("a decloaked unit is still hidden from the enemy [03 §3.2] step 2")
	}
}
