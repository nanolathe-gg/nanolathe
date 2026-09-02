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

// aiE2ETickCap bounds the battle at the retail bound for this proof: thirty
// minutes of authoritative time, 54000 ticks.
//
// Raised from 48000 by WU-19-26. That value was a cheapness margin around a
// seed-7 elimination at tick 36939, taken when the idle human never shot back:
// [06 §9.1] step 4's reaction routine did not exist, so a commander under fire
// answered nothing. With the routine in place its laser is offered its
// attacker on every hit — the per-slot offer of [06 R-WPN-04 §2 part 3], whose
// only weapon-side clause is that the weapon is not `commandfire`, which the
// D-gun is and the laser is not — so the idle human kills two of the wave and
// survives to tick 48791 on this seed. That is inside the real bound and
// outside the old margin, so the margin goes rather than the contract.
// RWU-19-1's timed retail capture, not any of these numbers, is the bar for how
// soon retail's own computer player attacks [08 R-AI-01 §4].
const aiE2ETickCap = uint32(54000)

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
// The difficulty is the lobby's missing-value default, 1 Medium
// [08 "Skirmish configuration"].
func aiE2ESkirmish(t *testing.T, mapName string, seed uint32) *Session {
	t.Helper()
	return aiE2ESkirmishAt(t, mapName, seed, SkirmishDefaultDifficulty)
}

// aiE2ESkirmishAt is aiE2ESkirmish with an explicit difficulty word. The word
// selects the AI profile's plan gate [08 R-AI-01 §12], so a test that depends
// on how strong the computer player is has to say which difficulty it means
// rather than inheriting the default.
func aiE2ESkirmishAt(t *testing.T, mapName string, seed uint32, difficulty int) *Session {
	t.Helper()
	root := aiE2ERetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Skipf("mount retail install: %v", err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	cfg := DirectSkirmishConfig(mapName)
	cfg.ApplyDefaults()
	cfg.Difficulty = difficulty
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
//
// It runs at both difficulties that decide what the wave is made of: Hard,
// whose tables build Thuds (movement class KBOTSS2, MaxSlope 32), and Medium,
// the lobby's missing-value default, whose tables build level-1/2 ground
// vehicles (TANKSH2/TANKSH3, MaxSlope 15) [08 R-AI-01 §12].
//
// Medium used to be un-runnable here and the test pinned Hard. WU-19-41
// measured why: on `ashap plateau` the computer player's start plateau was a
// closed 2735-cell pocket for TANKSH2, every rim cell rejected by the slope
// gate, so the wave formed at 20400, engaged, was issued `Attack_Chase` at the
// idle commander — and never moved, because every route request failed at
// [04 R-PATH-01 §4] step 9. That was our defect, not the map's rim. WU-19-46
// applied [04 R-SLOPE-01]: the movement classifiers test each covered cell on
// its own derived pair and take the MINIMUM tier over the footprint, where
// Nanolathe had aggregated the footprint's heights as min-of-mins/max-of-maxes
// and classified that one span. The aggregate is strictly harsher, and it was
// what walled the plateau in. The rim anchors the finding lists — (48,213),
// (34,208), (20,221) — carry per-cell slopes 9/8/9/9, 12/12/6/4 and 13/6/10/5,
// all under the authored 15, against 3×3 aggregates of 17, 16 and 18.
//
// With the per-cell rule the flood from the computer start cell (53, 231)
// reaches 53279 of the map's 53808 TANKSH2-passable anchors and includes the
// human start at (219, 19). [04 R-SLOPE-01 §4] records 53370 of 53901 for the
// same census; both pairs are this loader's own count over retail's map data,
// and the recorded pair was taken while the loader still carried the
// pre-correction south strip — WU-19-48 implemented the sweep as
// [03 R-TERR-01 §2] states it, which moves the south void band up one row and
// costs 93 anchors. Which of the two the retail executable's own layer would
// hold is the Unknown filed under [04 R-SLOPE-01 §3] item 2's correction.
// Both difficulties finish inside the bound either way.
// TestAIVehicleRetailSlopeProbe in ai_terrain_pocket_probe_test.go re-measures
// all four numbers on demand.
func TestComputerPlayerEliminatesIdleHumanRetail(t *testing.T) {
	// 2 is Hard; SkirmishDefaultDifficulty is the lobby default, 1 Medium
	// [08 "Skirmish configuration"].
	for _, tc := range []struct {
		name       string
		difficulty int
	}{
		{"Hard", 2},
		{"Medium", SkirmishDefaultDifficulty},
	} {
		t.Run(tc.name, func(t *testing.T) {
			idleHumanEliminationRetail(t, tc.difficulty)
		})
	}
}

// idleHumanEliminationRetail runs the gate at one difficulty word.
func idleHumanEliminationRetail(t *testing.T, difficulty int) {
	sess := aiE2ESkirmishAt(t, "ashap plateau", aiE2ESeed, difficulty)

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
	// Kill credit is deliberately not asserted; the arithmetic that decides it
	// belongs to the combat package's own tests, not to this unit. It used to
	// be misattributed here on this exact seed by two defects this test's
	// kills log first surfaced, both now fixed: the area-damage collector
	// admitted a shooter into its own blast and then stamped the victim's
	// provenance with the shooter's owner (WU-19-22, [06 §9.3][06 R-DMG-01
	// §9] — a shooter is unconditionally excluded from its own blast, the
	// whole of retail's self-damage policy); and a dying unit's death
	// explosion was built with that unit's own still-resolvable handle as the
	// shooter (Destroy sets Dying but leaves Alive set until FinalizeDeath
	// runs later), so a victim killed by the explosion was credited to the
	// dead unit's owner instead of nobody (WU-19-23, [06 R-WPN-02 §5] — the
	// death-explosion record carries no shooter). The engagement check above
	// is what this test owns.
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
	// This test runs at the lobby default difficulty, Medium. Compiling the
	// complete download menu in retail union order changes the weighted unit
	// candidate sequence [02 R-CAT-01 §1][08 R-AI-01 §8], so the first
	// regroup-A member on this seed now arrives at tick 15090. The budget was
	// briefly 18000 during
	// WU-19-32: the exact-versus-category matcher of [08 R-AI-01 §12] made
	// ai/default.txt's `Weight ARM 0.2` / `Weight CORE 0.2` reach every member
	// of those categories for the first time, and against the hard tables the
	// profile then fell back to that pushed formation out to 14100. Binding the
	// real difficulty word brought it back in; the complete authored candidate
	// table moves it forward again, still within the established 18000 bound.
	const formationBy = uint32(18000)
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
