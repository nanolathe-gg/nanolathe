//go:build retail

package headless

import (
	"errors"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
)

// The gameplay rule sets are locked here by fingerprint, one checked-in
// constant per scene and mode. The point of the lock is asymmetric: a Modern
// rule may be changed on purpose, but it must not move the Strict 3.1 baseline
// as a side effect, and neither may drift by accident. Every constant below is
// therefore a behavioural claim, and a diff that changes one is claiming the
// simulation now behaves differently.
//
// The fingerprint is the deliberately partial digest of
// docs/DESIGN_RUNTIME_DETERMINISM.md §4: equal digests do not prove equal
// futures, and a change confined to excluded state (projectiles, AI tasks,
// visibility, construction internals) will not show here. It is a regression
// tripwire, not a parity proof.
//
// HOW TO UPDATE A CONSTANT. Run the failing subtest, take the reported value,
// and say in the commit message which behaviour changed and why the new value
// is correct. A Strict constant moving is a retail-baseline change and needs
// the research citation that justifies it; a Modern constant moving is an
// approved-policy change and needs its design-document section. Updating a
// value with "fingerprint moved" as the whole explanation defeats the guard.
//
// PROVENANCE. Strict and ashap values were recorded on 2026-09-17 on
// u6-fingerprint-lock, branched from main 7dfa50d9, on an arm64 host. Modern
// benchmark values were updated for the authorized combat prototype described
// below. The ashap values remain the reference run of ARCHITECTURE §6.
const (
	// The reference run of docs/ARCHITECTURE.md §6 and
	// docs/DESIGN_RUNTIME_DETERMINISM.md §4: one human slot, one computer
	// slot. Through 6000 ticks no weapon is fired and no Modern policy is
	// consulted with an answer that reaches fingerprinted state, so Strict and
	// Modern agree there, and that agreement is itself locked: the 6000-tick
	// pair is the baseline drift detector and is deliberately equal.
	lockAshapMap = "ashap plateau"
	// The seed both deterministic streams take, matching the reference run.
	lockAshapSeed uint32 = 7
	// The per-player unit limit is pinned rather than read from the host
	// profile. `nanolathe-headless` takes an unset -unit-limit from the saved
	// settings file, so an unpinned run would fingerprint whatever the machine
	// happens to have configured; 250 is the value the recorded reference
	// constants were taken with.
	lockAshapUnitLimit = 250
	// The difficulty word the displayless command defaults to.
	lockDifficulty = 1

	lockAshapStrict6000 = "partial-v1:d125c21700a2db1b"
	lockAshapModern6000 = "partial-v1:d125c21700a2db1b"
	// The 54000-tick pair no longer agrees across modes. With the building
	// blocker's strict entry bounds [05 R-ECO-02 §1] the computer slot cannot
	// site on the map's edge cells, builds a different base, and reaches its
	// first attack near tick 45,800 — so the long run now contains combat, and
	// Modern's approved threat-targeting policy separates it from Strict
	// (bisected to the Combat seam alone). The 6000-tick pair stays the
	// combat-free baseline drift detector. Re-recorded 2026-09-20, arm64, on
	// the integrated ds41 branch (also carries the opportunity scan's check 4
	// and weapon-range gate [06 §3.2]).
	lockAshapStrict54000 = "partial-v1:4a62d6ab26833264"
	lockAshapModern54000 = "partial-v1:9bd2d73075a56228"
)

// The combat-bearing scene is the simulation benchmark's own composition
// (tools/sim-bench, docs/SIM_BENCHMARK.md): three computer armies of 250 units
// each on one map, with queued factory production and scripted orders.
// Composition remains shared. Modern now diverges during the march because
// deterministic threat selection replaces sampled acquisition, and then uses
// coordinated incoming fire and observed danger responses. These intentional
// policies are documented in DESIGN_WEAPONS_PROJECTILES "Modern threat targeting
// and incoming fire" and DESIGN_UNITS_ORDERS_COB "Modern danger response".
// Hidden projectile impacts now cause anonymous withdrawal, and precise beams
// coordinate against predictable ground movers. These approved follow-up
// policies change the combat outcome after 1500 ticks; the initial and warm
// states still agree with the preceding prototype.
//
// Both FINAL constants were re-recorded on 2026-09-20 for the sight-distance
// caller's two missing clauses [06 §3.2]: the opportunity scan of
// [04 R-STANCE-01 §3] now applies check 4, "for the sight-distance caller only,
// the candidate's definition index must be clear of the `nochasecategory`
// mask", and its physical gate compares against the slot weapon's authored
// `range` instead of the caller's sight radius [06 R-WPN-05 §1] clause 5. Both
// are retail-baseline corrections, so the Strict constant moves with the Modern
// one. The stream moves because a candidate rejected by either clause no longer
// takes the scoring draw it was taking [06 §3.2 "Draw consequence"]; the two
// scenes' initial and warm values are unchanged, which is what a rejection that
// only removes draws once idle units meet an out-of-reach or no-chase contact
// looks like.
const (
	lockBenchSeed        uint32 = 7
	lockBenchWarmupTicks        = 600
	lockBenchTotalTicks         = 1500
	lockBenchInitialBoth        = "partial-v1:3e1cbf1c074f060d"
	lockBenchStrictWarm         = "partial-v1:dce20f30bcdeef34"
	lockBenchModernWarm         = "partial-v1:7a75ebe138eebade"
	// Both final values moved again on 2026-09-20 for the land-wreck smoke
	// column of [03 R-LAYER §3], on top of the sight-distance re-recording
	// above: the corpse finalizer's land path now spends the smoke family's one
	// CRT draw per stamped wreck, and that stream also carries the wind interval
	// and the meteor scheduler, so a scene with deaths reaches different wind
	// from the same seed [03 R-STRIP-01 §3][01 §7.5]. The warm values are
	// unchanged because the first casualty falls after tick 600, and the ashap
	// constants because nothing dies there. Attributed by removing the producer
	// call and nothing else: both scenes then fingerprint the values this merge
	// brought in.
	lockBenchStrictFinal = "partial-v1:d0eaf19c8a8f135b" // was 82263563d76d3284
	lockBenchModernFinal = "partial-v1:886fdb07ddea5f77" // was 0e11552b08f6b8c7
)

// TestStrictFingerprintIsLocked holds the retail baseline. Nothing in a Modern
// rule set may move any value here.
func TestStrictFingerprintIsLocked(t *testing.T) {
	runFingerprintLock(t, gameplay.Strict31, lockAshapStrict6000, lockAshapStrict54000, lockBenchStrictWarm, lockBenchStrictFinal)
}

// TestModernFingerprintIsLocked holds the approved Modern policy set the same
// way, so an unintended change to a Modern rule is as loud as a change to the
// retail path [I11].
func TestModernFingerprintIsLocked(t *testing.T) {
	runFingerprintLock(t, gameplay.Modern, lockAshapModern6000, lockAshapModern54000, lockBenchModernWarm, lockBenchModernFinal)
}

// TestLockedScenesDiscriminateTheRuleSets records what the two locks are worth
// together: the cheap scene must agree across modes, and the combat-bearing
// scene must NOT. A Modern policy that silently stopped applying would make
// the second pair equal, and the constants alone would still look plausible.
func TestLockedScenesDiscriminateTheRuleSets(t *testing.T) {
	if lockAshapStrict6000 != lockAshapModern6000 {
		t.Fatal("the first 6000 ticks of the ashap reference run consult no Modern policy that reaches fingerprinted state, so its Strict and Modern constants must be equal")
	}
	if lockBenchStrictFinal == lockBenchModernFinal {
		t.Fatal("the benchmark scene no longer separates Strict from Modern: either a Modern policy stopped applying or the scene stopped reaching it")
	}
}

// runFingerprintLock runs both locked scenes under mode and compares each
// fingerprint with its constant. Elapsed times are logged, never asserted: the
// test is a value comparison and must pass identically on a loaded machine.
func runFingerprintLock(t *testing.T, mode gameplay.Mode, want6000, want54000, wantBenchWarm, wantBenchFinal string) {
	catalog, fs := retailcat.Shared(t)

	for _, scene := range []struct {
		ticks uint32
		want  string
	}{
		{ticks: 6000, want: want6000},
		{ticks: 54000, want: want54000},
	} {
		start := time.Now()
		report, err := RunWithContent(Request{
			Gameplay:       mode,
			Map:            lockAshapMap,
			Difficulty:     lockDifficulty,
			SimulationSeed: lockAshapSeed,
			CRTSeed:        lockAshapSeed,
			TickLimit:      scene.ticks,
			UnitLimit:      lockAshapUnitLimit,
		}, fs, catalog)
		// The reference run is bounded by its tick limit rather than by a
		// battle result, so the limit error is the expected outcome.
		if err != nil && !errors.Is(err, ErrTickLimit) {
			t.Fatalf("%s %q for %d ticks: %v", mode, lockAshapMap, scene.ticks, err)
		}
		if report.Tick != scene.ticks {
			t.Fatalf("%s %q stopped at tick %d, want %d: the locked constant describes the full run", mode, lockAshapMap, report.Tick, scene.ticks)
		}
		if report.StateHash != scene.want {
			t.Fatalf("%s %q after %d ticks fingerprints %s, want the locked %s: a diff that intends this must say which behaviour changed",
				mode, lockAshapMap, scene.ticks, report.StateHash, scene.want)
		}
		t.Logf("%s %q %d ticks: %s in %s", mode, lockAshapMap, scene.ticks, report.StateHash, time.Since(start).Round(time.Millisecond))
	}

	start := time.Now()
	opts := SimBenchOptions{
		Gameplay:   mode,
		Map:        SimBenchDefaultMap,
		Seed:       lockBenchSeed,
		Difficulty: lockDifficulty,
		UnitLimit:  SimBenchDefaultUnitLimit,
	}
	// ComposeSimBenchBattle is the benchmark's fingerprint-only path: it
	// builds the session and places the three armies without running a tick or
	// measuring anything, so this lock shares the scene with tools/sim-bench
	// and reads no clock the simulation can see.
	composed, scene, err := ComposeSimBenchBattle(opts, fs, catalog)
	if err != nil {
		t.Fatalf("%s compose benchmark scene: %v", mode, err)
	}
	if composed.InitialFingerprint != lockBenchInitialBoth {
		t.Fatalf("%s benchmark scene composes to %s, want the locked %s: the scene itself changed, so every later constant here is about a different workload",
			mode, composed.InitialFingerprint, lockBenchInitialBoth)
	}
	if scene.Version != SimBenchSceneVersion {
		t.Fatalf("%s benchmark scene is version %d, want %d", mode, scene.Version, SimBenchSceneVersion)
	}
	sess := composed.Session
	for tick := 1; tick <= lockBenchTotalTicks; tick++ {
		simBenchStep(sess)
		if tick != lockBenchWarmupTicks && tick != lockBenchTotalTicks {
			continue
		}
		hash, hashErr := sess.PartialStateFingerprint()
		if hashErr != nil {
			t.Fatalf("%s benchmark fingerprint at step %d: %v", mode, tick, hashErr)
		}
		want := wantBenchWarm
		if tick == lockBenchTotalTicks {
			want = wantBenchFinal
		}
		if hash != want {
			t.Fatalf("%s benchmark scene after %d steps fingerprints %s, want the locked %s: a diff that intends this must say which behaviour changed",
				mode, tick, hash, want)
		}
		t.Logf("%s benchmark scene %d steps: %s in %s", mode, tick, hash, time.Since(start).Round(time.Millisecond))
	}
}
