package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestCaptureEligible_FivePredicates locks the phase-0 admission ladder of
// [05 R-WORK-01 §6] as [05 R-WORK-01 §10] closed it.
//
// Three things it pins that the build used to get wrong:
//
//   - `Remaining == 0` IS the retail predicate, not a proxy for an idleness
//     sentinel: it is a float32 compare with a literal zero whose only accepted
//     outcome is equal [05 R-WORK-01 §10]. A finished unit carries zero; a
//     nanoframe carries 1.0 and is the "cloud of vapor".
//   - the "victim immunity" the build could not locate is predicate 4, the
//     TARGET's own `cancapture` bit — the same bit predicate 3 requires of the
//     builder, so anything that can capture cannot be captured.
//   - the same-owner and dying-victim rejects the build added are NOT in the
//     ladder, which has exactly five predicates. A busy, moving or damaged
//     finished unit is captured normally.
func TestCaptureEligible_FivePredicates(t *testing.T) {
	captor := &content.UnitDef{UnitName: "captor", CanCapture: true}
	plain := &content.UnitDef{UnitName: "plain"}

	mk := func(def *content.UnitDef, owner uint8, remaining float32) *units.Unit {
		return &units.Unit{Def: def, Owner: owner, Alive: true, Remaining: remaining}
	}

	cases := []struct {
		name    string
		builder *units.Unit
		victim  *units.Unit
		want    bool
	}{
		{"finished enemy unit is captured", mk(captor, 0, 0), mk(plain, 1, 0), true},
		{"1: a null target handle rejects", mk(captor, 0, 0), nil, false},
		{"2: an unlinked builder rejects", nil, mk(plain, 1, 0), false},
		{"3: a builder without cancapture rejects", mk(plain, 0, 0), mk(plain, 1, 0), false},
		{"4: a target that can itself capture rejects", mk(captor, 0, 0), mk(captor, 1, 0), false},
		{"5: a nanoframe is a cloud of vapor", mk(captor, 0, 0), mk(plain, 1, 1), false},
		{"5: a partly built target is a cloud of vapor", mk(captor, 0, 0), mk(plain, 1, 0.5), false},
		// Not in the ladder [05 R-WORK-01 §10]: retail's phase 0 has exactly
		// the five predicates above, so neither of these is a reject here.
		{"same owner is not a phase-0 reject", mk(captor, 0, 0), mk(plain, 0, 0), true},
		{"a death-latched victim is not a phase-0 reject", mk(captor, 0, 0), func() *units.Unit {
			u := mk(plain, 1, 0)
			u.Dying = true
			return u
		}(), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CaptureEligible(tc.builder, tc.victim); got != tc.want {
				t.Fatalf("CaptureEligible = %v, want %v [05 R-WORK-01 §6][05 R-WORK-01 §10]", got, tc.want)
			}
		})
	}

	// Negative zero compares equal to the literal zero and is accepted
	// [05 R-WORK-01 §10]. The negation is a runtime one so the compiler cannot
	// fold it back to +0 the way it folds the constant -0.0.
	var zero float32
	negZero := mk(plain, 1, -zero)
	if !CaptureEligible(mk(captor, 0, 0), negZero) {
		t.Fatal("negative zero compares equal to literal zero and is accepted [05 R-WORK-01 §10]")
	}
}

// captureTransferFixture builds a world holding one two-weapon victim owned by
// player 1, plus the service that transfers it.
func captureTransferFixture(t *testing.T) (*Service, *units.Unit) {
	t.Helper()
	primary := &content.WeaponDef{Name: "stockrocket", ID: 1, Stockpile: true}
	secondary := &content.WeaponDef{Name: "stockmissile", ID: 2, Stockpile: true}
	def := &content.UnitDef{
		UnitName: "victim", FootprintX: 2, FootprintZ: 2, MaxDamage: 100,
		Weapon1Def: primary, Weapon2Def: secondary,
	}
	def.CanonicalKey = content.CanonicalKey("victim")
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	w := newConstructionFixtureWorld(20, cat)
	h, err := w.Create(def, 1, 0, 0, 0)
	if err != nil {
		t.Fatalf("create victim: %v", err)
	}
	victim := w.Unit(h)
	return &Service{World: w, Catalog: cat}, victim
}

// TestTransferOwnershipCopyList locks [05 R-WORK-01 §11]'s exact copy list: the
// 16-bit health, the remaining fraction, the orientation triple, and per weapon
// slot — only where the REPLACEMENT's control byte carries its enabled bit —
// the stockpiled-round byte. "Nothing else is copied. The kill count is not."
//
// It is a correction test on two counts. The transfer used to copy the victim's
// kills onto the replacement (so a recaptured unit kept its veteran experience
// and its next capture timer), and used to copy SpotMetal as an admitted
// placeholder for a "cargo predicate" that turns out to be the stockpile.
func TestTransferOwnershipCopyList(t *testing.T) {
	svc, victim := captureTransferFixture(t)
	victim.Health = 37
	victim.Remaining = 0
	victim.Kills = 9
	victim.SpotMetal = 4.5
	victim.Move.Bank, victim.Move.Heading, victim.Move.Pitch = 111, 222, 333
	victim.Slots[0].Ammo = 5
	victim.Slots[1].Ammo = 7
	// Slot 2 has no weapon, so the initializer left its enabled bit clear
	// [06 R-WPN-05 §3]; a value parked there must not travel.
	victim.Slots[2].Ammo = 11

	repl, ok := svc.TransferOwnership(victim, 0)
	if !ok || repl == nil {
		t.Fatalf("transfer of a live, differently owned, unlatched victim was refused")
	}
	if repl.Owner != 0 {
		t.Fatalf("replacement owner = %d, want the new owner 0", repl.Owner)
	}
	if repl.Health != 37 || repl.Remaining != 0 {
		t.Fatalf("health/remaining = %d/%v, want 37/0 [05 R-WORK-01 §11]", repl.Health, repl.Remaining)
	}
	if repl.Move.Bank != 111 || repl.Move.Heading != 222 || repl.Move.Pitch != 333 {
		t.Fatalf("orientation triple = (%d,%d,%d), want (111,222,333) [05 R-WORK-01 §11]",
			repl.Move.Bank, repl.Move.Heading, repl.Move.Pitch)
	}
	// The stockpile follows the unit, slot by slot, wherever the new record has
	// that slot enabled.
	if !repl.Slots[0].IsEnabled() || !repl.Slots[1].IsEnabled() || repl.Slots[2].IsEnabled() {
		t.Fatalf("fixture slot enable bits = %v/%v/%v, want enabled/enabled/disabled",
			repl.Slots[0].IsEnabled(), repl.Slots[1].IsEnabled(), repl.Slots[2].IsEnabled())
	}
	if repl.Slots[0].Ammo != 5 || repl.Slots[1].Ammo != 7 {
		t.Fatalf("stockpiled rounds = %d/%d, want 5/7 [05 R-WORK-01 §11]", repl.Slots[0].Ammo, repl.Slots[1].Ammo)
	}
	if repl.Slots[2].Ammo != 0 {
		t.Fatalf("a disabled slot received %d rounds; the copy is gated on the replacement's enabled bit [05 R-WORK-01 §11]", repl.Slots[2].Ammo)
	}
	// The corrections.
	if repl.Kills != 0 {
		t.Fatalf("replacement kills = %d, want 0: the kill count is NOT copied [05 R-WORK-01 §11]", repl.Kills)
	}
	if repl.SpotMetal == 4.5 {
		t.Fatalf("SpotMetal was copied from the victim; the creator samples it for the replacement [05 R-PROD-01 §6][05 R-WORK-01 §11]")
	}
	// The old record is killed with the cause-4 packet and a null attacker.
	if !victim.Dying || victim.LastDamageCause != CaptureDeathCause || victim.LastDamageSide != units.NeutralAttackerSide {
		t.Fatalf("victim teardown = dying %v cause %d side %d, want dying/4/neutral [06 §12.1]",
			victim.Dying, victim.LastDamageCause, victim.LastDamageSide)
	}
}

// TestTransferOwnershipEntryGate locks [05 R-WORK-01 §11]'s entry gate: owner
// differing from the new owner, the alive bit set, the death latch clear — and
// "a refusal there is silent". The latch test exists nowhere earlier: the
// command resolver rejects a target lacking the alive bit but does not read the
// latch, and neither does the issue helper, so a victim killed this tick
// reaches the transfer with its latch set and its alive bit still standing.
func TestTransferOwnershipEntryGate(t *testing.T) {
	t.Run("a death-latched victim is refused silently", func(t *testing.T) {
		svc, victim := captureTransferFixture(t)
		victim.Dying = true // killed this tick; alive bit still set until the sweep
		repl, ok := svc.TransferOwnership(victim, 0)
		if ok || repl != nil {
			t.Fatalf("a latched victim was transferred: ok=%v repl=%v [05 R-WORK-01 §11]", ok, repl)
		}
		if !victim.Alive {
			t.Fatal("the refusal must not touch the victim; it is silent [05 R-WORK-01 §11]")
		}
	})
	t.Run("a same-owner target is refused", func(t *testing.T) {
		svc, victim := captureTransferFixture(t)
		if repl, ok := svc.TransferOwnership(victim, victim.Owner); ok || repl != nil {
			t.Fatalf("a same-owner transfer succeeded: ok=%v repl=%v [05 R-WORK-01 §11]", ok, repl)
		}
	})
	t.Run("a dead victim is refused", func(t *testing.T) {
		svc, victim := captureTransferFixture(t)
		victim.Alive = false
		if repl, ok := svc.TransferOwnership(victim, 0); ok || repl != nil {
			t.Fatalf("a victim without the alive bit was transferred: ok=%v repl=%v [05 R-WORK-01 §11]", ok, repl)
		}
	})
}
