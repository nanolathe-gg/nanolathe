// PLAN 15 WU-15-5 / PLAN 19 WU-19-1, Gate 7 line 1: the computer player must
// beat an idle human commander in a stock skirmish, headless, inside the
// thirty-minute battle bound. It is the only test in the tree that exercises
// the whole chain — classifier, wave merge, attack wave, order resolution,
// movement, combat and the session's end latch — against retail content, so
// it is the one that notices when any single link stops carrying the others.
// Skipped when ~/TotalAnnihilation is absent.
package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/vfs"
)

// aiE2ETickCap bounds the battle. The retail bound for this proof is thirty
// minutes of authoritative time, 54000 ticks. On Ashap Plateau the computer
// player eliminates the idle human at tick 36939 on seed 7 and at tick 41378 on
// seed 1; the test runs the faster of the two and caps at 48000, comfortably
// inside the real bound and cheap enough (about fifteen seconds of wall clock)
// to stay in the ordinary package run. The displayless runner covers the other
// seed. RWU-19-1's timed retail capture, not any of these numbers, is the bar
// for how soon retail's own computer player attacks [08 R-AI-01 §4].
const aiE2ETickCap = uint32(48000)

// aiE2ESeed is the seed both this test and the displayless runs use.
const aiE2ESeed = uint32(7)

func aiE2ERetailRoot(t *testing.T) string {
	t.Helper()
	root := os.Getenv("NANOLATHE_TA_ROOT")
	if root == "" {
		if home, err := os.UserHomeDir(); err == nil {
			root = filepath.Join(home, "TotalAnnihilation")
		}
	}
	if _, err := os.Stat(filepath.Join(root, "totala1.hpi")); err != nil {
		t.Skip("retail assets not present at ~/TotalAnnihilation")
	}
	return root
}

// aiE2ESkirmish composes the ordinary two-slot direct skirmish the displayless
// runner composes: slot 0 human, slot 1 computer, hostile alliance groups
// [08 R-SKIR-01 §2]. The human slot receives no command for the whole battle.
func aiE2ESkirmish(t *testing.T, mapName string, seed uint32) *Session {
	t.Helper()
	root := aiE2ERetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Skipf("mount retail install: %v", err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	cfg := DirectSkirmishConfig(mapName)
	cfg.ApplyDefaults()
	cfg.RNGSimSeed = seed
	cfg.RNGCrtSeed = seed
	sess, err := NewSkirmishWithProgress(fs, nil, cfg, nil)
	if err != nil {
		t.Fatalf("compose skirmish %q: %v", mapName, err)
	}
	if sess == nil || sess.Clock == nil || sess.Units == nil || sess.Econ == nil {
		t.Fatalf("skirmish %q composed without a runnable session", mapName)
	}
	return sess
}

// TestComputerPlayerEliminatesIdleHumanRetail is Gate 7 line 1. The human slot
// stands still; the computer player must build, form an attack wave, engage
// and kill, and the session must latch its end through the live-unit predicate
// of [08 R-SKIR-01 §3] — commander death clears the storage bonus and runs the
// owner sweep, the sweep drives the local live count to zero, and five 30-tick
// dues later the skirmish end latch is written.
//
// Before WU-19-1 this could not happen on any seed. The classifier's regroup-B
// row read the definition's MaxSlope word instead of its MinWaterDepth word,
// and because every stock land definition carries a positive slope and the
// movement template's MinWaterDepth, every armed ground unit was routed away
// from regroup A; wave A stayed empty for the whole battle while wave B — whose
// merge leash is 50,000 rather than 20,000 — never held the six members its
// engage threshold needs [08 R-P0-04 §3][08 R-AI-01 §4].
func TestComputerPlayerEliminatesIdleHumanRetail(t *testing.T) {
	sess := aiE2ESkirmish(t, "ashap plateau", aiE2ESeed)

	if local := int(sess.LocalOwner); local != 0 {
		t.Fatalf("direct skirmish resolved local owner %d, want the human slot 0", local)
	}
	// "Engages": at least one weapon packet from the computer player has to
	// land on the human before the battle ends. The victim's stored provenance
	// is the cheapest observation of that and costs no draw [06 §12.1].
	engaged := false
	scaled := sess.Clock.ScaledAnchor
	for sess.State != StatePostBattle && sess.Clock.GlobalTick < aiE2ETickCap {
		scaled += 5
		sess.Step(scaled)
		if engaged {
			continue
		}
		for _, u := range sess.Units.IterSliced() {
			if u == nil || u.Owner != 0 {
				continue
			}
			if u.LastDamageSide == 1 && combat.Cause(u.LastDamageCause) == combat.CauseOrdinary {
				engaged = true
				break
			}
		}
	}

	result := sess.GetResult()
	if !result.Ended {
		t.Fatalf("no gameplay result by tick %d: state=%v human live=%d computer live=%d",
			sess.Clock.GlobalTick, sess.State, sess.Units.LiveCountForPlayer(0), sess.Units.LiveCountForPlayer(1))
	}
	// The idle human is the one that loses; a "victory" here would mean the
	// computer player eliminated itself.
	if result.Kind != "defeat" {
		t.Fatalf("result kind %q at tick %d, want the idle human's defeat", result.Kind, result.Tick)
	}
	// The elimination path, not a timeout or a trigger: the local player's live
	// unit count is the predicate, and it must be zero [08 R-SKIR-01 §3].
	if live := sess.Units.LiveCountForPlayer(0); live != 0 {
		t.Fatalf("human live unit count %d at the latch, want the sweep to have emptied it [08 R-SKIR-01 §3]", live)
	}
	if live := sess.Units.LiveCountForPlayer(1); live == 0 {
		t.Fatal("computer player has no live units at the latch: this is a mutual wipe, not a win")
	}
	if !engaged {
		t.Fatal("the human was eliminated without ever taking a weapon packet from the computer player [08 R-AI-01 §4]")
	}
	if result.Tick == 0 || result.Tick > aiE2ETickCap {
		t.Fatalf("result latched at tick %d, outside the bounded battle", result.Tick)
	}
	// Kill credit is deliberately not asserted, even though this seed does
	// credit it. On seed 1 the human commander's last packet was its own
	// weapon's blast — the area-damage collector admits the shooter itself and
	// then stamps the victim's provenance with the shooter's owner — so the
	// credited kill landed on player 0 although the computer player's wave
	// drove the battle. That belongs to the combat package, not to this unit;
	// the engagement check above is what this test owns.
	t.Logf("computer player won at tick %d (its kills=%d, losses=%d, live=%d)",
		result.Tick, sess.Econ.Players[1].Kills, sess.Econ.Players[1].Losses, sess.Units.LiveCountForPlayer(1))
}

// TestComputerPlayerFormsAnAttackWaveRetail is the cheap half of the gate: it
// does not run the battle to its end, only far enough to prove that armed
// ground units reach regroup A and that the wave-A merge bootstraps from it
// [08 R-P0-04 §3 "Wave merge"][08 R-AI-01 §4]. When the long test above fails,
// this one says whether the classifier or the engagement is at fault.
func TestComputerPlayerFormsAnAttackWaveRetail(t *testing.T) {
	sess := aiE2ESkirmish(t, "ashap plateau", aiE2ESeed)
	manager := sess.AI[1]
	if manager == nil {
		t.Fatal("the computer slot composed without a manager")
	}
	scaled := sess.Clock.ScaledAnchor
	const formationBy = uint32(12000)
	for sess.Clock.GlobalTick < formationBy && sess.State != StatePostBattle {
		scaled += 5
		sess.Step(scaled)
		if len(manager.GroupWaveA) > 0 {
			break
		}
	}
	if len(manager.GroupWaveA)+len(manager.GroupRegroupA) == 0 {
		armed := 0
		for _, u := range sess.Units.IterSliced() {
			if u != nil && u.Alive && u.Owner == 1 && u.Def != nil && !u.Def.CanFly && !u.Def.Builder && u.Flags&units.ArmedStatus != 0 {
				armed++
			}
		}
		t.Fatalf("by tick %d wave A and regroup A are both empty with %d armed ground units built [08 R-P0-04 §3]",
			sess.Clock.GlobalTick, armed)
	}
}
