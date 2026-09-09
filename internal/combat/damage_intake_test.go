package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func TestAcceptDamageFixedPacketScalingAndDeathLatch(t *testing.T) {
	f := newReactionFixture(t)
	f.victim.Health = 29000
	f.victim.MaxHealth = 29000
	f.victim.Kills = 25

	got := f.svc.AcceptDamage(f.w, 17, DamageInput{
		Victim: f.victim.Handle, Attacker: f.attacker.Handle, Nominal: 30000, Kind: uint8(CauseDeconstruction),
	})
	if !got.Accepted || got.Amount != 24000 || got.DeathLatched {
		t.Fatalf("result = %+v, want accepted fixed amount 24000 without death latch [06 §9.2]", got)
	}
	if f.victim.Health != 5000 || uint8(f.victim.BlinkSuppress) != 240 {
		t.Fatalf("health/flash = %d/%d, want 5000/240 [06 §9.1][06 §9.2]", f.victim.Health, uint8(f.victim.BlinkSuppress))
	}
	if f.victim.LastDamageCause != uint8(CauseDeconstruction) {
		t.Fatalf("cause = %d, want kind 9 [06 §9.1]", f.victim.LastDamageCause)
	}

	for _, tc := range []struct {
		nominal int32
		want    uint16
	}{
		{29999, 14999},
		{30000, 30000},
	} {
		f.victim.Health = 32000
		f.victim.Kills = 0
		f.victim.Armored = true
		f.victim.Def.DamageModifier = 32768
		result := f.svc.AcceptDamage(f.w, 18, DamageInput{Victim: f.victim.Handle, Nominal: tc.nominal, Kind: uint8(CauseCargo)})
		if result.Amount != tc.want {
			t.Fatalf("nominal %d produced %d, want %d: armor gate is strict below 30000 [06 §9.2]", tc.nominal, result.Amount, tc.want)
		}
	}
}

func TestAcceptDamageProvenanceAndOwnerGates(t *testing.T) {
	t.Run("raw and null attacker provenance", func(t *testing.T) {
		f := newReactionFixture(t)
		f.victim.LastDamageSide = 8
		f.victim.EngagementTarget = f.attacker.Handle
		h := f.attacker.Handle
		owner := f.attacker.Owner
		f.w.FreeImmediate(h)
		result := f.svc.AcceptDamage(f.w, 9, DamageInput{Victim: f.victim.Handle, Attacker: h, Nominal: 1, Kind: KindOrdinary})
		if !result.Accepted || f.victim.LastDamageSide != owner || f.victim.EngagementTarget != h {
			t.Fatalf("freed attacker provenance = side %d target %d [06 R-WPN-04 §2]", f.victim.LastDamageSide, f.victim.EngagementTarget)
		}
		f.victim.LastDamageSide = 6
		f.victim.EngagementTarget = h
		f.svc.AcceptDamage(f.w, 10, DamageInput{Victim: f.victim.Handle, Nominal: 1, Kind: KindOrdinary})
		if f.victim.LastDamageSide != 6 || f.victim.EngagementTarget != h {
			t.Fatalf("null attacker rewrote provenance: side %d target %d [06 R-WPN-04 §2]", f.victim.LastDamageSide, f.victim.EngagementTarget)
		}
	})

	for _, tc := range []struct {
		name        string
		control     uint8
		wantLatched bool
	}{
		{"local", ControlByteHuman, true},
		{"remote", ControlByteRemote, false},
		{"absent", ControlByteAbsent, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReactionFixture(t)
			f.svc.ControlByte = func(uint8) uint8 { return tc.control }
			f.victim.Health = 10
			result := f.svc.AcceptDamage(f.w, 4, DamageInput{Victim: f.victim.Handle, Nominal: 20, Kind: KindOrdinary})
			if result.DeathLatched != tc.wantLatched || f.victim.Dying != tc.wantLatched {
				t.Fatalf("latch = %v/%v, want %v [06 R-DMG-01 §8]", result.DeathLatched, f.victim.Dying, tc.wantLatched)
			}
			if tc.wantLatched {
				if f.victim.Health != -10 || !f.victim.Alive {
					t.Fatalf("local lethal state = health %d alive %v, want modular -10 and delayed finalization [06 §9.1]", f.victim.Health, f.victim.Alive)
				}
			} else if f.victim.Health != 0 {
				t.Fatalf("nonlocal health = %d, want zero clamp [06 §9.1]", f.victim.Health)
			}
		})
	}

	t.Run("dead latch refuses", func(t *testing.T) {
		f := newReactionFixture(t)
		f.victim.Dying = true
		before := f.victim.Health
		if got := f.svc.AcceptDamage(f.w, 2, DamageInput{Victim: f.victim.Handle, Nominal: 1, Kind: KindOrdinary}); got.Accepted || f.victim.Health != before {
			t.Fatalf("dead-latched intake = %+v health %d, want refusal [06 §9.1]", got, f.victim.Health)
		}
	})

	t.Run("reaction sees prior provenance after flash", func(t *testing.T) {
		f := newReactionFixture(t)
		f.victim.LastDamageSide = f.victim.Owner
		f.victim.LastDamageCause = uint8(CauseCargo)
		var sawFlash uint8
		var sawSide, sawCause uint8
		f.svc.Reaction.ObserverNotice = func(v *units.Unit) {
			sawFlash = uint8(v.BlinkSuppress)
			sawSide, sawCause = v.LastDamageSide, v.LastDamageCause
		}
		f.svc.AcceptDamage(f.w, 5, DamageInput{Victim: f.victim.Handle, Attacker: f.attacker.Handle, Nominal: 1, Kind: KindOrdinary})
		if sawFlash != 240 || sawSide != f.victim.Owner || sawCause != uint8(CauseCargo) {
			t.Fatalf("reaction saw flash/side/cause %d/%d/%d, want 240/%d/%d [06 §9.1][06 R-WPN-04 §2]", sawFlash, sawSide, sawCause, f.victim.Owner, CauseCargo)
		}
		if f.victim.LastDamageSide != f.attacker.Owner || f.victim.LastDamageCause != KindOrdinary {
			t.Fatalf("post-reaction provenance = %d/%d, want attacker side and kind 1 [06 §9.1]", f.victim.LastDamageSide, f.victim.LastDamageCause)
		}
	})

	t.Run("kind eleven skips reaction", func(t *testing.T) {
		f := newReactionFixture(t)
		observed := 0
		f.svc.Reaction.ObserverNotice = func(*units.Unit) { observed++ }
		result := f.svc.AcceptDamage(f.w, 5, DamageInput{Victim: f.victim.Handle, Nominal: 1, Kind: KindNoReaction})
		if !result.Accepted || observed != 0 || uint8(f.victim.BlinkSuppress) != 240 || f.victim.LastDamageCause != KindNoReaction {
			t.Fatalf("kind 11 result/reaction/flash/cause = %+v/%d/%d/%d [06 §9.1]", result, observed, uint8(f.victim.BlinkSuppress), f.victim.LastDamageCause)
		}
	})
}

func TestAcceptDamageParalyzeHealAndNonordinaryCallbacks(t *testing.T) {
	t.Run("paralyze keeps preliminary effects and gates stun", func(t *testing.T) {
		prior := ParalyzeTaskPush
		t.Cleanup(func() { ParalyzeTaskPush = prior })
		f := newReactionFixture(t)
		var credit uint32
		ParalyzeTaskPush = func(v *units.Unit, got uint32, tick uint32) {
			if v != f.victim || tick != 11 {
				t.Fatalf("paralyze push recipient/tick = %v/%d [06 §10]", v, tick)
			}
			credit = got
		}
		result := f.svc.AcceptDamage(f.w, 11, DamageInput{Victim: f.victim.Handle, Attacker: f.attacker.Handle, Nominal: 20, Kind: KindParalyzer})
		if !result.Accepted || credit != 20 || f.victim.Health != 5000 || uint8(f.victim.BlinkSuppress) != 240 {
			t.Fatalf("paralyze result = %+v credit %d health/flash %d/%d [06 §10]", result, credit, f.victim.Health, uint8(f.victim.BlinkSuppress))
		}
		f.victim.Def.ImmuneToParalyzer = true
		credit = 0
		f.svc.AcceptDamage(f.w, 12, DamageInput{Victim: f.victim.Handle, Nominal: 20, Kind: KindParalyzer})
		if credit != 0 || f.victim.LastDamageCause != KindParalyzer || uint8(f.victim.BlinkSuppress) != 240 {
			t.Fatalf("immune paralyze lost preliminary effects: credit=%d cause=%d flash=%d [06 §10]", credit, f.victim.LastDamageCause, uint8(f.victim.BlinkSuppress))
		}
		f.victim.Def.ImmuneToParalyzer = false
		f.svc.Reaction.ObserverNotice = func(v *units.Unit) { v.Dying = true }
		f.svc.AcceptDamage(f.w, 13, DamageInput{Victim: f.victim.Handle, Nominal: 20, Kind: KindParalyzer})
		if credit != 0 {
			t.Fatal("paralyze task queued after reaction latched the victim [06 §10]")
		}
	})

	t.Run("heal is early unsigned signed-word store", func(t *testing.T) {
		f := newReactionFixture(t)
		f.victim.Health = -2
		f.victim.MaxHealth = 100
		f.victim.BlinkSuppress = 7
		f.victim.LastDamageSide = 4
		result := f.svc.AcceptDamage(f.w, 1, DamageInput{Victim: f.victim.Handle, Attacker: f.attacker.Handle, Nominal: 5, Kind: KindHeal})
		if !result.Accepted || result.Amount != 5 || f.victim.Health != 3 || f.victim.BlinkSuppress != 7 || f.victim.LastDamageSide != 4 {
			t.Fatalf("heal result/state = %+v/%d/%d/%d [06 §9.1]", result, f.victim.Health, f.victim.BlinkSuppress, f.victim.LastDamageSide)
		}
	})

	t.Run("legacy entry delegates signed store and unsigned clamp", func(t *testing.T) {
		f := newReactionFixture(t)
		f.victim.Health, f.victim.MaxHealth = 20000, 50000
		if !f.svc.DispatchHealingPacket(f.w, Packet{Victim: uint16(f.victim.Handle), Amount: 20000, Kind: KindHeal}) || f.victim.Health != -25536 {
			t.Fatalf("20000 + 20000 store = %d, want signed low word -25536 [06 §9.1]", f.victim.Health)
		}
		f.victim.Health, f.victim.MaxHealth = 40000, 50000
		if !f.svc.DispatchHealingPacket(f.w, Packet{Victim: uint16(f.victim.Handle), Amount: 0, Kind: KindHeal}) || f.victim.Health != -15536 {
			t.Fatalf("raw 40000 heal = %d, want signed input then unsigned clamp/store -15536 [06 §9.1]", f.victim.Health)
		}
		f.victim.Health, f.victim.MaxHealth = 2, 0
		if !f.svc.DispatchHealingPacket(f.w, Packet{Victim: uint16(f.victim.Handle), Amount: 5, Kind: KindHeal}) || f.victim.Health != 0 {
			t.Fatalf("zero maximum heal = %d, want zero unsigned clamp [06 §9.1]", f.victim.Health)
		}
		f.victim.Health, f.victim.MaxHealth = -2, 100
		if !f.svc.DispatchHealingPacket(f.w, Packet{Victim: uint16(f.victim.Handle), Amount: 1, Kind: KindHeal}) || f.victim.Health != 100 {
			t.Fatalf("negative heal intermediate = %d, want unsigned clamp to 100 [06 §9.1]", f.victim.Health)
		}
	})

	t.Run("only kind one starts callbacks", func(t *testing.T) {
		w, _, victim, _ := newTestWorldAndUnits(t)
		victim.Health, victim.MaxHealth = 32000, 32000
		code := []uint32{0x10065000}
		prog := progWithAim(code, "HitByWeapon", 0)
		prog.Scripts["TakeDamage"] = 0
		prog.ScriptsByID = []int{0, 0}
		vm := cob.NewVM(prog)
		attachTestCOB(victim, vm)
		svc := &Service{ControlByte: func(uint8) uint8 { return ControlByteHuman }}
		for _, kind := range []uint8{uint8(CauseSelfDestruct), uint8(CauseReclaim), uint8(CauseCargo), uint8(CauseDeconstruction), KindNoReaction} {
			before := vm.ActiveThreadCount()
			result := svc.AcceptDamage(w, 7, DamageInput{Victim: victim.Handle, Nominal: 1, Kind: kind})
			if !result.Accepted || vm.ActiveThreadCount() != before {
				t.Fatalf("kind %d accepted=%v callbacks %d->%d, want no callbacks [06 §9.1]", kind, result.Accepted, before, vm.ActiveThreadCount())
			}
		}
		before := vm.ActiveThreadCount()
		result := svc.AcceptDamage(w, 7, DamageInput{Victim: victim.Handle, Nominal: 1, Direction: 0x40, Kind: KindOrdinary})
		if !result.Accepted || vm.ActiveThreadCount() <= before {
			t.Fatalf("kind 1 accepted=%v callbacks %d->%d, want HitByWeapon then TakeDamage [06 §9.1]", result.Accepted, before, vm.ActiveThreadCount())
		}
	})
}
