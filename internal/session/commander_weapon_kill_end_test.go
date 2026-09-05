package session

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestEnemyCommanderKilledByWeaponEndsSkirmishRetail is the reported play-test
// path: an ordinary skirmish under the default rule word (1, "Game ends"), the
// computer player's commander killed by WEAPON damage after the computer player
// has built a base, and the session must reach the terminal result on the
// retail schedule — the owner sweep drives the computer slot's live count to
// zero, the local slot's next settlement due sees the victory sweep succeed,
// and the sixth consecutive true due writes the end latch [08 R-SKIR-01 §3]
// [08 R-TRIG-01 §6].
//
// cmd/nanolathe's TestCommanderKillReachesThePostBattleScreen kills the
// commander with a direct Destroy call ten ticks into the battle, before the
// computer player owns anything but its commander. This test kills it the way
// a player does — through the damage intake, with a death animation and the
// commander's death explosion — after thousands of ticks of AI construction,
// so the sweep has factories, nanoframes, extractors and units in transit to
// clear.
//
// The end is keyed on the live-unit COUNTER, never on the pool: "the end is
// never keyed on the commander itself: it is keyed on the live-unit count"
// [08 R-SKIR-01 §3] "Defeat detection", and the counter is decremented only
// by the finalizer that frees the slot [08 R-SKIR-01 §3] "Counters". Any
// path that takes a unit out of the pool without reaching that finalizer
// leaves the counter one high forever, and the slot's owner can then never be
// eliminated. That is what the failure message below reports: a positive
// counter with no live unit behind it.
func TestEnemyCommanderKilledByWeaponEndsSkirmishRetail(t *testing.T) {
	for _, buildUntil := range []uint32{6000, 12000, 18000, 24000} {
		buildUntil := buildUntil
		t.Run(fmt.Sprint(buildUntil), func(t *testing.T) {
			t.Parallel()
			enemyCommanderWeaponKillIn(t, aiE2ESkirmish(t, "ashap plateau", aiE2ESeed), buildUntil)
		})
	}
}

// enemyCommanderWeaponKillIn runs the kill in a composed session: step to
// buildUntil, hit the enemy commander with the human commander's first weapon
// once per tick until its death latches, then expect the terminal result.
func enemyCommanderWeaponKillIn(t *testing.T, sess *Session, buildUntil uint32) {
	t.Helper()
	if CommanderDeathMode(sess.Skirmish.CommanderDeath) != CommanderDeathEnds {
		t.Fatalf("rule word %d, want the default 1 (game ends)", sess.Skirmish.CommanderDeath)
	}
	scaled := sess.Clock.ScaledAnchor
	step := func() {
		scaled += 5
		sess.Step(scaled)
	}
	for sess.Clock.GlobalTick < buildUntil {
		step()
	}
	if live := sess.Units.LiveCountForPlayer(1); live < 3 {
		t.Fatalf("computer player has only %d live units at tick %d; the sweep has nothing to clear", live, buildUntil)
	}

	var human, enemy *units.Unit
	for _, u := range sess.Units.IterSliced() {
		if u == nil || !u.Alive || u.Def == nil || !sess.isCommanderForOwner(u) {
			continue
		}
		switch u.Owner {
		case 0:
			human = u
		case 1:
			enemy = u
		}
	}
	if human == nil || enemy == nil {
		t.Fatalf("commanders: human=%v enemy=%v", human != nil, enemy != nil)
	}
	weaponName := human.Def.Weapon1
	if weaponName == "" {
		t.Fatalf("%s carries no weapon1", human.Def.UnitName)
	}
	laser, ok := sess.Catalog.WeaponByName(weaponName)
	if !ok {
		t.Fatalf("weapon %q is not in the catalog", weaponName)
	}

	// Hit the enemy commander once per tick with the human commander's laser
	// until its death latches. This is the ordinary damage intake, not a kill
	// call: the killing blow goes through DestroyBy(DeathKilled) with the
	// shooter as the recorded attacker [06 §9.1].
	enemyHandle := enemy.Handle
	killTick := uint32(0)
	for i := 0; i < 600; i++ {
		u := sess.Units.Unit(enemyHandle)
		if u == nil || !u.Alive || u.Dying {
			killTick = sess.Clock.GlobalTick
			break
		}
		sess.Combat.ExplodeWeaponAt(sess.Units, sess.World, laser, combat.Vec3{X: u.X, Y: u.Y, Z: u.Z}, human.Handle, sess.Clock.GlobalTick)
		step()
	}
	if killTick == 0 {
		t.Fatalf("the enemy commander did not die under 600 laser hits (health %d)", sess.Units.Unit(enemyHandle).Health)
	}

	// The retail schedule: the death finalizes after the death animation, the
	// sweep damages every other unit that tick, those die over the following
	// ticks, and the local settlement due then needs six consecutive true
	// polls — 150 ticks after the first. Allow a generous margin for the
	// death animations on top of that.
	const bound = 30*6 + 300
	for i := 0; i < bound; i++ {
		step()
		if sess.GetResult().Ended {
			break
		}
	}
	result := sess.GetResult()
	if !result.Ended {
		var alive []string
		for _, u := range sess.Units.IterSliced() {
			if u != nil && u.Alive && u.Owner == 1 {
				name := "?"
				if u.Def != nil {
					name = u.Def.UnitName
				}
				alive = append(alive, fmt.Sprintf("%s(dying=%v hp=%d)", name, u.Dying, u.Health))
			}
		}
		t.Fatalf("no result %d ticks after the commander kill at %d: live0=%d live1=%d latch=%+v pending=%v; live units owned by the computer player=[%s] — a positive counter with no unit behind it is a record freed without the death finalizer [08 R-SKIR-01 §3] \"Counters\"",
			bound, killTick, sess.Units.LiveCountForPlayer(0), sess.Units.LiveCountForPlayer(1),
			sess.Latch, sess.resultPending, strings.Join(alive, " "))
	}
	if result.Kind != "victory" {
		t.Fatalf("result kind %q, want the human's victory", result.Kind)
	}
	if live := sess.Units.LiveCountForPlayer(1); live != 0 {
		t.Fatalf("computer live count %d at the latch, want 0", live)
	}
	t.Logf("kill at %d, latch at %d (%d ticks later)", killTick, result.Tick, result.Tick-killTick)
}

// userReportedSkirmish composes the battle the reporting player's settings
// file starts: a 1v1 on Great Divide at difficulty 0, the human row with ally
// group 0 (promoted to the 5 sentinel by ApplyDefaults) and 10000/10000
// starting resources, the computer row at the 1000/1000 default, every rule
// word at its retail default. Seed 1234 at 15000 ticks is the case that first
// reproduced the report: a nanoframe the computer commander was building was
// cancelled when the commander came under fire, and the cancel path cleared
// the record's alive bit itself after latching the kind-9 death, so the
// phase-2 finalizer never visited it and the computer slot's live count stayed
// at one with nothing alive.
func userReportedSkirmish(t *testing.T, seed uint32) *Session {
	t.Helper()
	cat, fs := retailcat.Shared(t)
	cfg := SkirmishConfig{MapName: "Great Divide"}
	cfg.NumPlayers = 2
	cfg.Players[0] = SkirmishPlayer{Controller: SkirmishControllerHuman, Side: 0, Color: 0, AllyGroup: 0, Metal: 10000, Energy: 10000}
	cfg.Players[1] = SkirmishPlayer{Controller: SkirmishControllerComputer, Side: 1, Color: 1, AllyGroup: 5, Metal: 1000, Energy: 1000}
	cfg.Difficulty = 0
	cfg.Location = 1
	cfg.CommanderDeath = 1
	cfg.Mapping = 1
	cfg.LineOfSight = 1
	cfg.LOSType = 1
	cfg.UnitLimit = 250
	cfg.ApplyDefaults()
	cfg.RNGSimSeed = seed
	cfg.RNGCrtSeed = seed
	sess, err := NewSkirmishWithProgress(fs, cat, cfg, nil)
	if err != nil {
		t.Fatalf("compose Great Divide: %v", err)
	}
	return sess
}

// TestEnemyCommanderKilledByWeaponEndsUserSkirmishRetail is the same kill in
// the reporting player's own configuration, across seeds.
func TestEnemyCommanderKilledByWeaponEndsUserSkirmishRetail(t *testing.T) {
	for _, seed := range []uint32{7, 1234, 98765} {
		for _, buildUntil := range []uint32{6000, 15000} {
			seed, buildUntil := seed, buildUntil
			t.Run(fmt.Sprintf("seed%d-%d", seed, buildUntil), func(t *testing.T) {
				t.Parallel()
				enemyCommanderWeaponKillIn(t, userReportedSkirmish(t, seed), buildUntil)
			})
		}
	}
}
