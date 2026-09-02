package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// repairAdmissionPair builds an actor that satisfies every term of nano-reach
// except the health one, and a friendly target whose health the case sets. The
// actor is a mover so code 2's fall-through has a name to resolve.
func repairAdmissionPair(t *testing.T, health int32) (*units.Unit, *units.Unit) {
	t.Helper()
	const seaLevel = 20
	actor := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanReclamate = true
		d.CanMove = true
		d.CanFly = false
		d.Amphibious = false
		d.MaxWaterDepth = 12
		// The later arms of codes 1 and 2 are pickup, landing and follow; with
		// `canguard` cleared a click that falls out of the repair arm reaches
		// the move, which is the visible difference this test wants.
		d.CanGuard = false
	}))
	target := mkUnit(2, 0, "ARM", health, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.MaxDamage = 100
		d.ModelTop = 4
	}))
	// mkUnit reseeds health when the max argument is zero; it is 100 here, so
	// the case's value stands. Write it again to be explicit about what the
	// case is testing.
	target.Health = health
	target.Y = numeric.Fixed(int64(30) << 16) // top 34, clear of sea level 20
	target.Move.Mode = 1
	setTestHostility(actor, func(*units.Unit, *units.Unit) bool { return false })
	setTestSeaLevel(actor, seaLevel)
	return actor, target
}

// TestRepairAdmissionHealthTermBoundaries locks [04 R-ORD-02 §7]'s three health
// tests at the boundary. Nano-reach's term is an INEQUALITY against the
// sign-extended 16-bit health, so it admits the over-full and the death-latched
// target and refuses only the exactly-full one; code 2 adds the unsigned strict
// compare that puts those two back out; codes 1 and 8 add nothing.
func TestRepairAdmissionHealthTermBoundaries(t *testing.T) {
	cases := []struct {
		name   string
		health int32
		code8  string // code 8's resolution: "" is a reject
		code2  string // code 2's resolution
	}{
		// The boundary itself: health == maxdamage fails nano-reach, so code 8
		// rejects outright and code 2's click is an ordinary move.
		{"exactly full", 100, "", "Move_Ground"},
		// Ordinarily damaged: every test passes, both codes repair.
		{"damaged", 50, "RepairUnit", "RepairUnit"},
		// Over-full as a 16-bit value: nano-reach's inequality admits it, so
		// code 8 orders a repair; code 2's unsigned compare (150 < 100 is
		// false) refuses and the click stays a move.
		{"over-full", 150, "RepairUnit", "Move_Ground"},
		// Death-latched: the lethal packet has taken health to or below zero
		// but the alive bit still stands [06 R-DMG-01 §3]. Sign-extended, a
		// NEGATIVE health read as unsigned is far above `maxdamage`, so code 2
		// refuses while code 8 still issues the repair the next slot visit
		// finds dead. A latch at exactly zero is the arithmetic's own edge: `0
		// < maxdamage` holds, so code 2 admits it too — §7's prose says the
		// compare "excludes the latched targets", but the expression it gives
		// excludes only the negative ones, and the expression is the contract.
		{"latched at zero", 0, "RepairUnit", "RepairUnit"},
		{"latched negative", -5, "RepairUnit", "Move_Ground"},
		// The 16-bit narrowing is load-bearing: 0x10064 read back through the
		// health field IS 100, so this target is exactly full and both codes
		// treat it that way.
		{"wider than 16 bits, narrows to full", 0x10064, "", "Move_Ground"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actor, target := repairAdmissionPair(t, tc.health)
			if got := DescriptorFor(Resolve(8, actor, target, nil)).Name; got != tc.code8 {
				t.Fatalf("code 8 with health %d = %q, want %q [04 R-ORD-02 §7]", tc.health, got, tc.code8)
			}
			actor2, target2 := repairAdmissionPair(t, tc.health)
			if got := DescriptorFor(Resolve(2, actor2, target2, nil)).Name; got != tc.code2 {
				t.Fatalf("code 2 with health %d = %q, want %q [04 R-ORD-02 §7]", tc.health, got, tc.code2)
			}
		})
	}
}

// TestCode1AddsNoHealthTestOfItsOwn is §7's other half: the contextual code's
// friendly arm calls the shared admission and nothing more, so a full-health
// friendly is NOT a repair — the click falls through to the later arms, which
// for a plain mover is the move — while a death-latched one, which code 2
// refuses, still resolves a repair here.
func TestCode1AddsNoHealthTestOfItsOwn(t *testing.T) {
	full, fullTarget := repairAdmissionPair(t, 100)
	if got := DescriptorFor(Resolve(1, full, fullTarget, nil)).Name; got != "Move_Ground" {
		t.Fatalf("contextual click on a full-health friendly = %q, want Move_Ground [04 R-ORD-02 §7]", got)
	}
	latched, latchedTarget := repairAdmissionPair(t, -5)
	if got := DescriptorFor(Resolve(1, latched, latchedTarget, nil)).Name; got != "RepairUnit" {
		t.Fatalf("contextual click on a death-latched friendly = %q, want RepairUnit [04 R-ORD-02 §7]", got)
	}
}

// TestCode2RepairArmFollowsTheAssistArm locks the arm ORDER of [04 R-ORD-02 §1]
// code 2: an unfinished friendly is assistance, not repair, even though it also
// satisfies the repair arm's health compare.
func TestCode2RepairArmFollowsTheAssistArm(t *testing.T) {
	actor, target := repairAdmissionPair(t, 50)
	target.Remaining = 0.5
	if got := DescriptorFor(Resolve(2, actor, target, nil)).Name; got != "HelpBuild" {
		t.Fatalf("code 2 on an unfinished friendly = %q, want HelpBuild [04 R-ORD-02 §1]", got)
	}
}
