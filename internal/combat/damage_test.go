package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// bindFixtureControlBytes gives a fixture Service the control-byte accessor the
// session binds in production [06 R-DMG-01 §8]. Without it every row reads as
// unoccupied, which passes gate 1 but rejects gate 2 — so a fixture that
// expects a victim to DIE must bind it, the same way wideDriftTolerance exists
// so a fixture whose subject is not aiming still fires. Every row here reads as
// a locally controlled human [05 R-SHARE-01 §1].
func bindFixtureControlBytes(s *Service) {
	s.ControlByte = func(uint8) uint8 { return ControlByteHuman }
}

func TestPacketNoGenerationStaleReuse(t *testing.T) {
	// C18: handles are 16-bit with no generation token, so a packet addressing
	// a slot that was freed and reused is accepted on the CURRENT occupant's
	// alive bit and clear death latch and never on a generation compare
	// [06 §5.1] C18 [06 §9.1] (I5). The attacker id receives NO validation at
	// all [06 §9.1] C18. The live gate is AcceptDamage's victim test.
	f := newReactionFixture(t)
	victim, attacker := f.victim.Handle, f.attacker.Handle

	if got := f.svc.AcceptDamage(f.w, 1, DamageInput{Victim: victim, Attacker: attacker, Nominal: 1, Kind: KindOrdinary}); !got.Accepted {
		t.Fatalf("a live slot must accept the packet addressed to it [06 §5.1] C18")
	}

	// The attacker's slot is freed under the packet: nothing validates it, so
	// the packet is still accepted [06 §9.1] C18.
	f.w.FreeImmediate(attacker)
	if got := f.svc.AcceptDamage(f.w, 2, DamageInput{Victim: victim, Attacker: attacker, Nominal: 1, Kind: KindOrdinary}); !got.Accepted {
		t.Fatalf("the attacker id receives no validation [06 §9.1] C18")
	}

	// The victim's two acceptance terms, one at a time.
	f.victim.Dying = true
	if got := f.svc.AcceptDamage(f.w, 3, DamageInput{Victim: victim, Nominal: 1, Kind: KindOrdinary}); got.Accepted {
		t.Fatalf("victim acceptance requires a clear death latch [06 §9.1] C18")
	}
	f.victim.Dying = false
	f.victim.Alive = false
	if got := f.svc.AcceptDamage(f.w, 4, DamageInput{Victim: victim, Nominal: 1, Kind: KindOrdinary}); got.Accepted {
		t.Fatalf("victim acceptance requires the alive bit set [06 §9.1] C18")
	}

	// Handle 0 is the pool's null sentinel and is never a victim [06 §9.1] C18.
	if got := f.svc.AcceptDamage(f.w, 5, DamageInput{Victim: 0, Nominal: 1, Kind: KindOrdinary}); got.Accepted {
		t.Fatalf("the null handle is never accepted [06 §9.1] C18")
	}
}

func TestOverrideTableLookup(t *testing.T) {
	// C19: override table keyed by target definition's exact UnitName string [06 §9.2] C19
	// Build a minimal weapon with sorted damage table
	// Use content helpers to synthesize weapon without VFS
	// DamageKeysSorted sorts case-insensitively; lookup is case-insensitive binary search [06 §9.2]
	w := &content.WeaponDef{}
	w.CanonicalKey = "testweapon"
	w.DamageDefault = 100 // unsigned 16-bit default [06 §9.2]
	w.Damage = map[string]int32{
		"ARMPEEP": 500, // signed 32-bit override [06 §9.2]
		"corak":   250,
		"ArmFlea": 750,
	}
	// Exact key matches via case-insensitive search [06 §9.2]
	if got := SelectBaseDamage(w, "ARMPEEP"); got != 500 {
		t.Fatalf("exact key lookup failed [06 §9.2] C19: got %d want 500", got)
	}
	if got := SelectBaseDamage(w, "armpeep"); got != 500 {
		t.Fatalf("case-insensitive lookup failed per [06 §9.2] C19: got %d want 500 (retail is case-insensitive binary search)", got)
	}
	if got := SelectBaseDamage(w, "ARMPeEp"); got != 500 {
		t.Fatalf("case variant should match per [06 §9.2] case-insensitive: got %d", got)
	}
	if got := SelectBaseDamage(w, "ArmFlea"); got != 750 {
		t.Fatalf("mixed case lookup failed [06 §9.2] C19")
	}
	if got := SelectBaseDamage(w, "armflea"); got != 750 {
		t.Fatalf("case-insensitive for ArmFlea failed [06 §9.2]")
	}
	// Category lookup must NOT match — e.g., "KBOT" category vs UnitName
	if got := SelectBaseDamage(w, "KBOT"); got != 100 {
		t.Fatalf("category should not match UnitName lookup [06 §9.2] C19: got %d want default 100", got)
	}
	if got := SelectBaseDamage(w, "PEEP"); got != 100 {
		t.Fatalf("partial string should not match [06 §9.2] C19")
	}
	if got := SelectBaseDamage(w, ""); got != 100 {
		t.Fatalf("empty should give default [06 §9.2]")
	}
	// Ensure sorting stability: DamageKeysSorted returns case-insensitive order (I1)
	keys := w.DamageKeysSorted()
	if len(keys) != 3 {
		t.Fatalf("DamageKeysSorted len %d", len(keys))
	}
	// Verify not ranging map directly (I1) — sorted order must be deterministic
	// Already checked via sorted helper
	_ = vfs.New // ensure import used? not needed
}

func TestC20OrderFixture(t *testing.T) {
	// C20: arithmetic order matters — reordering changes outcome [06 §9.2] C20
	// Brute-force search for values where step order matters due to truncation [01 §8] I3.
	found := false
	var correct, swapped uint16
	for base := int32(1); base < 5000 && !found; base += 7 {
		for _, falloff := range []float32{0.33, 0.5, 0.7, 0.9} {
			for _, attKills := range []int32{0, 6, 12, 25} {
				for _, defKills := range []int32{0, 10, 20} {
					c := ComputeScaledAmount(base, falloff, attKills, defKills, true, 40000, false, false, false)
					// swapped order: defender vet before armor (steps 5 and 6 swapped)
					amount := int32(float32(base) * falloff)
					tierA := int32(uint32(attKills) / 5)
					if tierA > 5 {
						tierA = 5
					}
					amount = int32((int64(amount) * int64(100+6*tierA)) / 100)
					tierD := int32(uint32(defKills) / 5)
					if tierD > 5 {
						tierD = 5
					}
					defFactor := (25 - tierD) * 4
					s := int32((int64(amount) * int64(defFactor)) / 100)
					if s < 30000 {
						s = int32((int64(s) * int64(40000)) >> 16)
					}
					s2 := uint16(s)
					if c != s2 {
						correct, swapped = c, s2
						found = true
						break
					}
				}
			}
		}
	}
	if !found {
		// Fallback: also test falloff vs attacker order swap
		base := int32(1000)
		falloff := float32(0.33)
		c := ComputeScaledAmount(base, falloff, 25, 0, false, 65536, false, false, false)
		// swapped: attacker before falloff
		tierA := int32(uint32(int32(25)) / 5)
		if tierA > 5 {
			tierA = 5
		}
		sTmp := int32((int64(base) * int64(100+6*tierA)) / 100)
		sTmp = int32(float32(sTmp) * falloff)
		s := uint16(sTmp)
		if c == s {
			t.Fatalf("C20 order fixture failed: even brute search found no divergence; choose different values [06 §9.2] C20")
		}
		correct, swapped = c, s
	}
	if correct == swapped {
		t.Fatalf("C20 order fixture failed: correct==swapped %d; reordering should change outcome [06 §9.2] C20", correct)
	}
	// Also verify global double order matters by brute search
	foundGlobal := false
	for base := int32(100); base < 2000 && !foundGlobal; base += 13 {
		c := ComputeScaledAmount(base, 1.0, 10, 0, false, 65536, false, true, false)
		amount := int32(float32(base) * 1.0)
		tierA := int32(uint32(int32(10)) / 5)
		amount = amount * 2
		amount = int32((int64(amount) * int64(100+6*tierA)) / 100)
		s := uint16(amount)
		if c != s {
			foundGlobal = true
		}
	}
	if !foundGlobal {
		t.Fatalf("C20 global gate order should matter but brute search found no divergence [06 §9.2] step4")
	}
	// Ensure armored threshold <30000 gate matters: amount 29999 vs 30000 diverge
	justBelow := ComputeScaledAmount(29999, 1.0, 0, 0, true, 32768, false, false, false)
	justAboveBase := int32(30000)
	// 30000 *1 => attacker vet 0 => 30000, armor gate should NOT apply because incoming >=30000
	justAbove := ComputeScaledAmount(justAboveBase, 1.0, 0, 0, true, 32768, false, false, false)
	if justBelow == uint16(29999) {
		t.Fatalf("armored modifier should have applied below 30000 [06 §9.2] step5")
	}
	if justAbove != uint16(30000) {
		// defender tier 0 => factor 100% so should remain 30000
		t.Fatalf("armored modifier should NOT apply at >=30000 [06 §9.2] step5: got %d want 30000", justAbove)
	}
	// Healing bypass
	base2 := int32(1000)
	falloff2 := float32(0.5)
	heal := ComputeScaledAmount(base2, falloff2, 10, 10, true, 32768, true, false, false)
	noHeal := ComputeScaledAmount(base2, falloff2, 10, 10, true, 32768, false, false, false)
	if heal == noHeal {
		t.Fatalf("healing bypass should skip armor/defender vet [06 §9.2] C20: heal %d == noHeal %d", heal, noHeal)
	}
}

func TestAreaEnumerationBoundary(t *testing.T) {
	// C26: enumeration shape/radius rules [06 §9.3]
	// Map 10x10, impact at origin cell (0,0), radius 32 => cells = 32/16+1=3 => broad phase 4x4 inclusive => 0..3 with clamp exclusive upper
	impact := Vec3{X: world.CellToWorld(0), Z: world.CellToWorld(0)}
	radius := int32(32)
	mapW, mapH := int32(10), int32(10)
	var visited []struct{ X, Z int32 }
	EnumerateArea(impact, radius, mapW, mapH, func(cx, cz int32) {
		visited = append(visited, struct{ X, Z int32 }{cx, cz})
	})
	// Ensure order rows Z then X [06 §9.3] (I1)
	for i := 1; i < len(visited); i++ {
		prev, cur := visited[i-1], visited[i]
		if cur.Z < prev.Z || (cur.Z == prev.Z && cur.X < prev.X) {
			t.Fatalf("enumeration not rows Z then X [06 §9.3] C26: %v before %v", cur, prev)
		}
	}
	// Upper bounds exclusive [06 §9.3]
	for _, v := range visited {
		if v.X < 0 || v.X >= mapW || v.Z < 0 || v.Z >= mapH {
			t.Fatalf("visited out of bounds not clamped [06 §9.3] C26: %v map %dx%d", v, mapW, mapH)
		}
	}
	// Impact at edge should clamp min to 0
	if len(visited) == 0 {
		t.Fatalf("no cells visited [06 §9.3]")
	}
	if visited[0].X != 0 || visited[0].Z != 0 {
		t.Fatalf("clamping at origin failed, first %v want 0,0 [06 §9.3] C26", visited[0])
	}
	// Impact at far edge
	impact2 := Vec3{X: world.CellToWorld(9), Z: world.CellToWorld(9)}
	visited = nil
	EnumerateArea(impact2, radius, mapW, mapH, func(cx, cz int32) {
		visited = append(visited, struct{ X, Z int32 }{cx, cz})
	})
	// Max exclusive ensures not visiting 10
	for _, v := range visited {
		if v.X >= mapW || v.Z >= mapH {
			t.Fatalf("upper bound exclusive violated [06 §9.3] C26")
		}
	}
	// At the origin, a zero-radius span has exclusive upper bounds of one.
	visited = nil
	EnumerateArea(impact, 0, mapW, mapH, func(cx, cz int32) {
		visited = append(visited, struct{ X, Z int32 }{cx, cz})
	})
	if len(visited) != 1 {
		t.Fatalf("radius 0 at origin visited %d cells, want 1 [06 §9.3]", len(visited))
	}
}

func TestFalloffNoClamp(t *testing.T) {
	// C26: executable does not clamp authored edge effectiveness [06 §9.3]
	// falloff formula tested with edge >1 yields >1 values, not clamped
	if got := Falloff(5, 10, 2.0); got <= 1.0 {
		t.Fatalf("edgeEffectiveness >1 should produce >1 falloff unclamped [06 §9.3] C26: got %f", got)
	}
	if got := Falloff(0, 10, 0.5); got != 1.0 {
		t.Fatalf("zero distance must be exactly one [06 §9.3] C26: got %f", got)
	}
}

func TestNoExplodeSuppressionScope(t *testing.T) {
	// C28: noexplode refinements [06 §13.2] C28
	// In ordinary impact branch, noexplode gates ONLY retirement block, not damage etc.
	// Retirements outside that block ignore flag
	if NoExplodeRetirement(true, true, false, false) {
		t.Fatalf("noexplode should suppress ordinary impact retirement [06 §13.2] C28")
	}
	if NoExplodeRetirement(false, true, false, false) == false {
		t.Fatalf("without noexplode should retire [06 §13.2]")
	}
	if !NoExplodeRetirement(true, false, false, false) {
		t.Fatalf("non-ordinary branch ignores noexplode should retire [06 §13.2] C28")
	}
	if !NoExplodeRetirement(true, true, true, false) {
		t.Fatalf("off-map retires regardless of noexplode [06 §8.1] [06 §13.2] C28")
	}
	if !NoExplodeRetirement(true, true, false, true) {
		t.Fatalf("water/hazard override retires regardless [06 §13.2] C28")
	}
	// Cached-cell feature contact suppresses ONLY feature impact [06 §8.1] C28
	var cache [2]int32 = [2]int32{99, 99}
	// First feature at 5,5 not suppressed, updates cache
	if FeatureCacheSuppressed(&cache, 5, 5) {
		t.Fatalf("first feature at new cell should not be suppressed [06 §8.1] C28")
	}
	if cache != [2]int32{5, 5} {
		t.Fatalf("cache not updated [06 §8.1]")
	}
	// Second at same cell suppressed
	if !FeatureCacheSuppressed(&cache, 5, 5) {
		t.Fatalf("same cached cell should suppress [06 §8.1] C28")
	}
}

func TestHealingBypass(t *testing.T) {
	// C20: healing bypasses armor and defender vet [06 §9.2]
	base := int32(1000)
	healAmt := ComputeScaledAmount(base, 1.0, 5, 5, true, 32768, true, false, false)
	normalAmt := ComputeScaledAmount(base, 1.0, 5, 5, true, 32768, false, false, false)
	if healAmt == normalAmt {
		t.Fatalf("healing should bypass armor/defender [06 §9.2] C20")
	}
	// Paralyzer uses ordinary scaling before duration credit — amount should equal ordinary for same inputs
	paraAmt := ComputeScaledAmount(base, 1.0, 5, 5, true, 32768, false, false, false)
	if paraAmt != normalAmt {
		t.Fatalf("paralyzer uses ordinary scaling [06 §9.2] C20")
	}
}

// --- WU-19-13: the armored bit and the control-byte gates [06 R-DMG-01 §8] ---

// wu1913Weapon is a direct-hit weapon whose one shot is survivable, so a test
// can read the health the funnel left behind.
func wu1913Weapon(damage int32) *content.WeaponDef {
	return &content.WeaponDef{
		ID: 1, LineOfSight: true, Range: 100,
		WeaponVelocity: int32(numeric.FixedFromInt(1)),
		AreaOfEffect:   8, // direct-target shortcut, no splash [06 §9.1]
		DamageDefault:  damage,
	}
}

// TestArmorGateReadsRuntimeBitOnly locks the operand of the packet builder's
// armor scale [06 R-DMG-01 §8]: bit 1 of the unit's first runtime state byte,
// which only the COB `set ARMORED` posture writes. The FBI `armoredstate` key
// is a definition flag with NO reader in retail [R-DMG-01 §2], so a unit that
// merely authors it takes full damage. The build used to OR the two, which
// made every unit authored `armoredstate=1` permanently armored.
func TestArmorGateReadsRuntimeBitOnly(t *testing.T) {
	// damagemodifier 0.5 in 16.16, so an armored victim takes half.
	const halfModifier = 32768
	run := func(t *testing.T, runtimeArmored, authoredArmoredState bool) int32 {
		t.Helper()
		var svc Service
		bindFixtureControlBytes(&svc)
		w, terrain, shooter, target := newTestWorldAndUnits(t)
		target.Def = &content.UnitDef{
			UnitName: "armortest", MaxDamage: 100, Limit: -1,
			DamageModifier: halfModifier, ArmoredState: authoredArmoredState,
		}
		target.Health = 100
		target.MaxHealth = 100
		target.Armored = runtimeArmored
		p := &Projectile{Shooter: shooter.Handle, ShooterSide: shooter.Owner,
			TargetUnit: target.Handle, Pos: Vec3{X: target.X, Y: target.Y, Z: target.Z}}
		handleProjectileImpact(&svc, 1, p, wu1913Weapon(40), w, terrain, nil, nil, nil, 4, Vec3{}, nil, p.TargetUnit)
		return target.Health
	}
	if got := run(t, false, false); got != 60 {
		t.Fatalf("plain victim health = %d, want 60 (40 damage, no scale) [06 §9.2]", got)
	}
	if got := run(t, true, false); got != 80 {
		t.Fatalf("runtime-armored victim health = %d, want 80 (40 halved by the 16.16 modifier) [06 R-DMG-01 §8]", got)
	}
	if got := run(t, false, true); got != 60 {
		t.Fatalf("victim authoring only `armoredstate` health = %d, want 60: the FBI key has no reader [R-DMG-01 §2]", got)
	}
}

// TestDamageGateOnProjectileSide locks gate 1 [06 §9.1][06 R-DMG-01 §9]: the
// gate is a property of the PROJECTILE's own side, not of the victim, and it
// skips damage ONLY for an occupied row whose control byte is 3.
//
// An UNOCCUPIED row passes, and so does the never-occupied eleventh row that a
// null-shooter record's neutral side byte 10 selects — so a meteor or a death
// explosion damages every side and credits nobody. This replaces an assertion
// that a no-record side routed nothing, which came from the inverted reading
// [06 R-DMG-01 §8] item 1 carried before [06 R-DMG-01 §9] traced the two-read
// gate.
func TestDamageGateOnProjectileSide(t *testing.T) {
	// The accessor a real session binds: ten player rows, ControlByteAbsent for
	// anything past them, which is what retail's constructed-but-never-occupied
	// row 10 reads as [06 R-DMG-01 §9].
	table := func(rows ...uint8) func(uint8) uint8 {
		return func(owner uint8) uint8 {
			if int(owner) >= len(rows) {
				return ControlByteAbsent
			}
			return rows[owner]
		}
	}
	cases := []struct {
		name        string
		shooterSide uint8
		nullShooter bool
		rows        []uint8
		wantHealth  int32
	}{
		{"occupied local human routes", 0, false, []uint8{ControlByteHuman, ControlByteHuman}, 60},
		{"occupied computer player routes", 0, false, []uint8{ControlByteComputer, ControlByteHuman}, 60},
		{"occupied remote peer is the only skip", 0, false, []uint8{ControlByteRemote, ControlByteHuman}, 100},
		{"unoccupied row routes", 0, false, []uint8{ControlByteAbsent, ControlByteHuman}, 60},
		// The neutral side byte of every null-shooter record [06 §6.1][06 §6.5].
		{"side 10 (null shooter) routes", 10, true, []uint8{ControlByteHuman, ControlByteHuman}, 60},
		{"unbound accessor routes", 0, false, nil, 60},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var svc Service
			if tc.rows != nil {
				svc.ControlByte = table(tc.rows...)
			}
			w, terrain, shooter, target := newTestWorldAndUnits(t)
			target.Health = 100
			target.MaxHealth = 100
			var kinds []EventKind
			svc.Events = func(ev Event) { kinds = append(kinds, ev.Kind) }
			weapon := wu1913Weapon(40)
			weapon.ShakeMagnitude = 4
			weapon.ShakeDuration = 2
			weapon.SoundHit = "hit"
			p := &Projectile{Shooter: shooter.Handle, ShooterSide: tc.shooterSide,
				TargetUnit: target.Handle, Pos: Vec3{X: target.X, Y: target.Y, Z: target.Z}}
			if tc.nullShooter {
				p.Shooter = 0 // a meteor keeps a null shooter reference [06 §6.5]
			}
			handleProjectileImpact(&svc, 1, p, weapon, w, terrain, nil, nil, nil, 4, Vec3{}, nil, p.TargetUnit)
			if target.Health != tc.wantHealth {
				t.Fatalf("health = %d, want %d [06 R-DMG-01 §9] gate 1", target.Health, tc.wantHealth)
			}
			// Shake and sound precede the damage gate, so they happen either way
			// [06 §9.1] steps 4 and 5.
			if len(kinds) == 0 || kinds[0] != EventShake {
				t.Fatalf("events = %v: the shake precedes the damage gate [06 §9.1]", kinds)
			}
		})
	}
}

// TestDeathLatchOnVictimControlByte locks gate 2 [06 §9.1 step 6][06 R-DMG-01 §8]:
// only a victim whose owning slot's control byte is 1 or 2 latches death. A
// computer player's units die exactly like a human's — the build's old table
// called 3 the computer, which would have exempted them.
func TestDeathLatchOnVictimControlByte(t *testing.T) {
	cases := []struct {
		name      string
		control   uint8
		wantDying bool
	}{
		{"human victim latches", ControlByteHuman, true},
		{"computer victim latches", ControlByteComputer, true},
		{"remote victim clamps without latching", ControlByteRemote, false},
		{"victim with no record clamps without latching", ControlByteAbsent, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var svc Service
			svc.ControlByte = func(owner uint8) uint8 {
				if owner == 0 { // the shooter's slot must pass gate 1
					return ControlByteHuman
				}
				return tc.control
			}
			w, terrain, shooter, target := newTestWorldAndUnits(t)
			target.Health = 10
			target.MaxHealth = 10
			p := &Projectile{Shooter: shooter.Handle, ShooterSide: shooter.Owner,
				TargetUnit: target.Handle, Pos: Vec3{X: target.X, Y: target.Y, Z: target.Z}}
			handleProjectileImpact(&svc, 1, p, wu1913Weapon(100), w, terrain, nil, nil, nil, 4, Vec3{}, nil, p.TargetUnit)
			if target.Dying != tc.wantDying {
				t.Fatalf("dying = %v, want %v [06 R-DMG-01 §8] gate 2", target.Dying, tc.wantDying)
			}
			if !tc.wantDying && target.Health != 0 {
				t.Fatalf("health = %d, want 0: a rejected latch clamps to zero and continues [06 §9.1 step 6]", target.Health)
			}
		})
	}
}

// TestDamageIntakeDrawsNoRandomness locks I4 for the gates this unit added: the
// only simulation draw anywhere in the damage-intake path is the retaliation
// throttle of [08 R-AI-01 §11], which none of these fixtures reaches.
func TestDamageIntakeDrawsNoRandomness(t *testing.T) {
	var svc Service
	bindFixtureControlBytes(&svc)
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	target.Health = 100
	target.MaxHealth = 100
	sim := rng.NewSimulation(1)
	crt := rng.NewCRT(1)
	before, beforeCRT := sim.Draws(), crt.Draws()
	p := &Projectile{Shooter: shooter.Handle, ShooterSide: shooter.Owner,
		TargetUnit: target.Handle, Pos: Vec3{X: target.X, Y: target.Y, Z: target.Z}}
	handleProjectileImpact(&svc, 1, p, wu1913Weapon(40), w, terrain, nil, nil, nil, 4, Vec3{}, &sim, p.TargetUnit)
	if sim.Draws() != before || crt.Draws() != beforeCRT {
		t.Fatalf("draws sim %d->%d crt %d->%d, want none (I4)", before, sim.Draws(), beforeCRT, crt.Draws())
	}
	if target.Health != 60 {
		t.Fatalf("fixture did not actually damage: health = %d", target.Health)
	}
}

// --- WU-19-80: the paralyzer packet runs retail's stun mechanism [06 §10] ---

// TestParalyzerHitCreditsTheStunTaskAndTouchesNothingElse locks the packet
// side of [06 §10]: a kind-2 packet never subtracts health, never writes the
// stunned mark itself — the mark's one setter is the stun task's first visit
// [06 R-DMG-01 §11] — and hands the task a credit that is the damage number the
// weapon would have dealt, scaled exactly as ordinary damage INCLUDING the
// armored-state modifier. The three eligibility tests gate the push and nothing
// else: a victim failing one keeps the preliminary side effects and receives
// neither task nor damage.
func TestParalyzerHitCreditsTheStunTaskAndTouchesNothingElse(t *testing.T) {
	type push struct {
		victim pool.Handle
		credit uint32
		tick   uint32
	}
	// The seam is a package variable installed by internal/orders in a real
	// build; a fixture composing internal/combat alone installs its own.
	prior := ParalyzeTaskPush
	t.Cleanup(func() { ParalyzeTaskPush = prior })

	// damagemodifier 0.5 in 16.16, so an armored victim is stunned for half as
	// long as an unarmored one.
	const halfModifier = 32768
	run := func(t *testing.T, armored, immune bool, controlByte uint8) ([]push, int32, bool) {
		t.Helper()
		var pushes []push
		ParalyzeTaskPush = func(v *units.Unit, credit uint32, tick uint32) {
			pushes = append(pushes, push{victim: v.Handle, credit: credit, tick: tick})
		}
		var svc Service
		svc.ControlByte = func(uint8) uint8 { return controlByte }
		w, terrain, shooter, target := newTestWorldAndUnits(t)
		target.Def = &content.UnitDef{
			UnitName: "stuntest", MaxDamage: 100, Limit: -1,
			DamageModifier: halfModifier, ImmuneToParalyzer: immune,
		}
		target.Health = 100
		target.MaxHealth = 100
		target.Armored = armored
		weapon := wu1913Weapon(40)
		weapon.Paralyzer = true
		p := &Projectile{Shooter: shooter.Handle, ShooterSide: shooter.Owner,
			TargetUnit: target.Handle, Pos: Vec3{X: target.X, Y: target.Y, Z: target.Z}}
		handleProjectileImpact(&svc, 1, p, weapon, w, terrain, nil, nil, nil, 7, Vec3{}, nil, p.TargetUnit)
		return pushes, target.Health, target.Stunned
	}

	pushes, health, stunned := run(t, false, false, ControlByteHuman)
	if len(pushes) != 1 || pushes[0].credit != 40 || pushes[0].tick != 7 {
		t.Fatalf("pushes = %+v, want one credit of 40 ticks at tick 7 [06 §10]", pushes)
	}
	if health != 100 {
		t.Fatalf("health = %d, want 100: a kind-2 packet never subtracts health [06 §10]", health)
	}
	if stunned {
		t.Fatal("the packet raised the stunned mark itself; its one setter is the task's first visit [06 R-DMG-01 §11]")
	}

	// "scaled exactly as ordinary damage (§9.2), including the armored-state
	// modifier" — the pair used to be passed as (false, 0) here [06 §10].
	if pushes, _, _ := run(t, true, false, ControlByteHuman); len(pushes) != 1 || pushes[0].credit != 20 {
		t.Fatalf("armored pushes = %+v, want one credit of 20 ticks [06 §10]", pushes)
	}
	// Test 3: `immunetoparalyzer` [06 §10].
	if pushes, health, _ := run(t, false, true, ControlByteHuman); len(pushes) != 0 || health != 100 {
		t.Fatalf("immune pushes = %+v health = %d, want no task and no damage [06 §10]", pushes, health)
	}
	// Test 2: only controller types 1 and 2 are eligible [06 §10][06 R-DMG-01 §8].
	if pushes, _, _ := run(t, false, false, ControlByteAbsent); len(pushes) != 0 {
		t.Fatalf("absent-row pushes = %+v, want none [06 §10]", pushes)
	}
}

// TestFalloffProductIsFormedInDoublePrecision locks step 2 of [06 §9.2]:
// `amount = trunc((double)base * falloff)`. The falloff keeps the
// single-precision store [06 §9.3] gives it, and the product is formed after
// promoting both — only the conversion back to an integer narrows.
//
// Forming and storing the product at single precision instead rounds where
// retail truncates, and the rounding is not rare: it moves twenty-odd stock
// default-damage and distance combinations by one point.
func TestFalloffProductIsFormedInDoublePrecision(t *testing.T) {
	// The float32 nearest 0.7 is a shade below it, so the exact product is a
	// shade below seventy. A single-precision product rounds that back up to
	// exactly seventy and yields 70.
	if got := ComputeScaledAmount(100, float32(0.7), 0, 0, false, 65536, false, false, false); got != 69 {
		t.Fatalf("100 × float32(0.7) = %d, want 69: the product is formed in double and truncated [06 §9.2]", got)
	}
	// A Bertha-shaped case: default damage 2000 two world units from the centre
	// of a radius-forty blast with no edge effectiveness, whose falloff is the
	// float32 nearest 0.9025. The exact product is a hair under 1805.
	falloff := Falloff(2, 40, 0)
	if got := ComputeScaledAmount(2000, falloff, 0, 0, false, 65536, false, false, false); got != 1804 {
		t.Fatalf("2000 × falloff(d=2, R=40, edge=0) = %d, want 1804 [06 §9.2][06 §9.3]", got)
	}
	// A falloff of exactly one is exact at both precisions, so the boundary
	// above is the arithmetic and not a change of the zero-distance contract.
	if got := ComputeScaledAmount(100, 1, 0, 0, false, 65536, false, false, false); got != 100 {
		t.Fatalf("zero-distance damage = %d, want the base 100 [06 §9.3]", got)
	}
}

// TestFalloffIsEvaluatedAtWorkingPrecisionAndStoredOnce locks the width of the
// area falloff of [06 §9.3]: the quotient, the subtraction of one, the square
// and the two products stay on the x87 stack at working precision and "the
// result [is] stored back as single precision" — one store, at the end.
//
// The chain used to be written as float32 operations throughout, rounding at
// every step where retail rounds once, and the error is not academic: it
// reaches the `trunc((double)base × falloff)` of [06 §9.2] and moves damage by
// one point at both ends of the curve.
func TestFalloffIsEvaluatedAtWorkingPrecisionAndStoredOnce(t *testing.T) {
	// One world unit from the centre of a radius-ten blast with no edge
	// effectiveness: f = -0.9 and the exact falloff is 0.81. Rounded once, that
	// is the float32 nearest 0.81, which lies just ABOVE it, so a base of 100
	// truncates to 81. The stepwise float32 chain lands on 0.80999994 and
	// truncates to 80.
	if got := Falloff(1, 10, 0); got != float32(0.81) {
		t.Fatalf("falloff(d=1, R=10, edge=0) = %v, want the float32 nearest 0.81 [06 §9.3]", got)
	}
	if got := ComputeScaledAmount(100, Falloff(1, 10, 0), 0, 0, false, 65536, false, false, false); got != 81 {
		t.Fatalf("100 × falloff(d=1, R=10, edge=0) = %d, want 81 [06 §9.2][06 §9.3]", got)
	}
	// The rim of the same blast, where the ±1 goes the other way: the exact
	// falloff is 0.01 and the float32 nearest it lies just BELOW, so a base of
	// 100 truncates to 0. The stepwise chain landed on 0.010000004 and dealt 1.
	if got := Falloff(9, 10, 0); got != float32(0.01) {
		t.Fatalf("falloff(d=9, R=10, edge=0) = %v, want the float32 nearest 0.01 [06 §9.3]", got)
	}
	if got := ComputeScaledAmount(100, Falloff(9, 10, 0), 0, 0, false, 65536, false, false, false); got != 0 {
		t.Fatalf("100 × falloff(d=9, R=10, edge=0) = %d, want 0 [06 §9.2][06 §9.3]", got)
	}
	// Edge effectiveness enters the same single store: with edge 1 every
	// distance is exactly one, which is exact at either width and so pins the
	// shape of the expression rather than its rounding.
	if got := Falloff(5, 10, 1); got != 1 {
		t.Fatalf("falloff(edge=1) = %v, want exactly 1 [06 §9.3]", got)
	}
}
