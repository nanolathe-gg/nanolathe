package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestRetaliationOrderRunsSlotZeroAdmissionFirst locks the third admission of
// the retaliation order branch [08 R-AI-01 §11] (RWU-19-39): the slot
// admission predicate is evaluated for SLOT 0 against the attacker before the
// issuer, and a refusal skips the order branch alone.
//
// The "otherwise" arm of §11 still runs on a refusal — the standing-fire gate
// and then the per-slot offer, which re-tests each slot on its own terms — so
// a victim whose slot 0 cannot admit the attacker can still swing another slot
// onto it.
func TestRetaliationOrderRunsSlotZeroAdmissionFirst(t *testing.T) {
	f := newReactionFixture(t)
	// Slot 0 refuses the attacker; the other slots admit it.
	f.admits = func(_ *units.Unit, idx int, _ *units.Unit) bool { return idx != 0 }
	installSlotWeapon(f.victim, 1, &content.WeaponDef{ID: 1, Range: 400})

	f.svc.ReactToDamage(f.w, f.victim, f.attacker, 5)

	if f.orders != 0 {
		t.Fatalf("the issuer ran %d time(s) with slot 0 refusing the attacker; retail evaluates the slot-0 predicate before it [08 R-AI-01 §11]", f.orders)
	}
	if got := f.victim.SlotAt(1).Target; got.Kind != units.TargetUnit || got.Unit != f.attacker.Handle {
		t.Fatalf("the per-slot offer left slot 1 targeting %+v; a slot-0 refusal skips ONLY the order branch [08 R-AI-01 §11]", got)
	}
}

// TestRetaliationOrderIssuesWhenSlotZeroAdmits is the other side of the same
// gate: a victim whose slot 0 admits the attacker reaches the issuer.
func TestRetaliationOrderIssuesWhenSlotZeroAdmits(t *testing.T) {
	f := newReactionFixture(t)
	f.admits = func(*units.Unit, int, *units.Unit) bool { return true }

	f.svc.ReactToDamage(f.w, f.victim, f.attacker, 5)

	if f.orders != 1 {
		t.Fatalf("the issuer ran %d time(s), want exactly 1 when slot 0 admits the attacker [08 R-AI-01 §11]", f.orders)
	}
}

// TestRetaliationOrderFailsClosedWithoutTheAdmissionSeam records this build's
// reading of an unbound predicate: retail always evaluates it, so a build that
// cannot is not entitled to issue the order. It matches the alliance row's
// existing fail-closed reading in the same routine.
func TestRetaliationOrderFailsClosedWithoutTheAdmissionSeam(t *testing.T) {
	f := newReactionFixture(t)
	f.svc.Reaction.SlotAcquisitionAdmits = nil

	f.svc.ReactToDamage(f.w, f.victim, f.attacker, 5)

	if f.orders != 0 {
		t.Fatalf("the issuer ran %d time(s) with no slot-admission seam bound; the gate fails closed [08 R-AI-01 §11]", f.orders)
	}
}
