//go:build retail

package headless

import (
	"errors"
	"fmt"
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
// Strict values retain the pre-Community retail baseline. Community and Modern
// values were recorded after DESIGN_COMMUNITY_PATCH §4.1 entry parameters:
// 1500 units per player, 66650 path steps, and larger effect/projectile pools.
// Unit identities change at composition and the path allowance changes the
// computer player's progress. The long ashap runs now finish before the 54000
// tick bound; both their terminal tick and fingerprint are locked. The battle
// fixture warm/final Community and Modern locks also include CP-DMG-2: lower
// unit indices take contested cells, changing movement and collision victims.
// Community's long lock additionally includes CP-CON-1's inclusive blocked-site
// limit of 20 rather than 10. Modern retains its existing limit of 10 under D3
// (DESIGN_COMMUNITY_PATCH §4.3, §11), restoring the pre-kickout terminal lock.
// The 6000-tick and benchmark locks remain unchanged.
//
// Modern re-route staggering (DESIGN_MOVEMENT_PATH "Modern re-route
// staggering") moves the Modern long lock and the benchmark warm/final locks:
// re-routes arrive 0–7 ticks later, so the long run no longer ends before
// its 54000-tick bound. The Modern benchmark initial and 6000-tick locks, and
// every Strict and Community lock, are unchanged by it.
//
// Modern group-order spreading (DESIGN_MOVEMENT_PATH "Modern group-order
// spreading") moves the Modern benchmark warm/final locks again: the
// computer players' group orders admit their farther members one or two
// ticks later. The ashap scene forms no group of sixteen same-tick first
// requests (its largest is seven), so its Modern locks are unchanged, as are
// the Modern benchmark initial lock and every Strict and Community lock.
//
// Modern bounded path work (DESIGN_MOVEMENT_PATH "Modern bounded path work")
// moves the Modern benchmark warm/final locks once more: a player's carried
// search work is capped at four shares and a whole sweep that admits nothing
// ends its polling for the call, which changes the poll cursor's position and
// so the order later requests are admitted in. The ashap scene's Modern locks,
// the benchmark initial lock and every Strict and Community lock are
// unchanged by it.
//
// Modern allied pass-through (DESIGN_MOVEMENT_PATH "Modern allied
// pass-through") moves the Modern benchmark warm/final locks: the computer
// armies' opposed movers now pass through each other mid-route instead of
// blocking. The ashap locks, the benchmark initial lock and every Strict and
// Community lock are unchanged by it.
//
// Retail's `MobileBuild` weapon-slot release [04 R-ORD-01 §5] moves the Strict
// and Community ashap 6000-tick locks and nothing else: at that tick the Core
// commander is building with slots 0 and 2 taken from autonomy, and setting
// that one bit back reproduces the previous values exactly. The trajectories
// do not diverge, so the 54000-tick and benchmark locks hold; the Modern
// commander is still approaching its site at tick 6000, so its lock holds too.
const (
	lockAshapMap                   = "ashap plateau"
	lockAshapSeed           uint32 = 7
	lockAshapUnitLimit             = 250 // Strict setting; Community's table overrides it.
	lockDifficulty                 = 1
	lockAshapStrict6000            = "partial-v1:bfc3b98e66838d54"
	lockAshapCommunity6000         = "partial-v1:3ee37e7e5104c034"
	lockAshapModern6000            = "partial-v1:320cbaa11e9fd28a"
	lockAshapStrict54000           = "partial-v1:4a62d6ab26833264"
	lockAshapCommunity54000        = "partial-v1:ae0cc2ee810199ec"
	lockAshapModern54000           = "partial-v1:d7f48d704d2eb5d3"
	lockAshapCommunityEnd   uint32 = 53430
	lockAshapModernEnd      uint32 = 54000

	lockBenchSeed             uint32 = 7
	lockBenchWarmupTicks             = 600
	lockBenchTotalTicks              = 1500
	lockBenchStrictInitial           = "partial-v1:3e1cbf1c074f060d"
	lockBenchCommunityInitial        = "partial-v1:f6cbc51b5ef4deff"
	lockBenchModernInitial           = "partial-v1:f6cbc51b5ef4deff"
	lockBenchStrictWarm              = "partial-v1:dce20f30bcdeef34"
	lockBenchCommunityWarm           = "partial-v1:f907fb371a053c87"
	lockBenchModernWarm              = "partial-v1:020c5588af463a71"
	lockBenchStrictFinal             = "partial-v1:d0eaf19c8a8f135b"
	lockBenchCommunityFinal          = "partial-v1:81b03660538b0eac"
	lockBenchModernFinal             = "partial-v1:8566bce851e216f7"
)

// TestStrictFingerprintIsLocked holds the retail baseline. Nothing in a Modern
// rule set may move any value here.
func TestStrictFingerprintIsLocked(t *testing.T) {
	runFingerprintLock(t, gameplay.Strict31, lockAshapStrict6000, lockAshapStrict54000, 54000, lockBenchStrictInitial, lockBenchStrictWarm, lockBenchStrictFinal)
}

// TestCommunityFingerprintIsLocked holds the approved mainline feature table.
func TestCommunityFingerprintIsLocked(t *testing.T) {
	runFingerprintLock(t, gameplay.Community39, lockAshapCommunity6000, lockAshapCommunity54000, lockAshapCommunityEnd, lockBenchCommunityInitial, lockBenchCommunityWarm, lockBenchCommunityFinal)
}

// TestModernFingerprintIsLocked holds the approved Modern policy set the same
// way, so an unintended change to a Modern rule is as loud as a change to the
// retail path [I11].
func TestModernFingerprintIsLocked(t *testing.T) {
	runFingerprintLock(t, gameplay.Modern, lockAshapModern6000, lockAshapModern54000, lockAshapModernEnd, lockBenchModernInitial, lockBenchModernWarm, lockBenchModernFinal)
}

// The combat scene distinguishes each reserved set. This catches a disabled
// Community layer or a Modern policy silently absorbed by its base.
func TestLockedScenesDiscriminateTheRuleSets(t *testing.T) {
	if lockBenchStrictFinal == lockBenchCommunityFinal || lockBenchCommunityFinal == lockBenchModernFinal || lockBenchStrictFinal == lockBenchModernFinal {
		t.Fatal("combat scene no longer distinguishes all three reserved sets")
	}
}

// runFingerprintLock runs both locked scenes under mode and compares each
// fingerprint with its constant. Elapsed times are logged, never asserted: the
// test is a value comparison and must pass identically on a loaded machine.
func runFingerprintLock(t *testing.T, mode gameplay.Mode, want6000, want54000 string, wantEnd uint32, wantBenchInitial, wantBenchWarm, wantBenchFinal string) {
	catalog, fs := retailcat.Shared(t)

	for _, scene := range []struct {
		ticks uint32
		end   uint32
		want  string
	}{
		{ticks: 6000, end: 6000, want: want6000},
		{ticks: 54000, end: wantEnd, want: want54000},
	} {
		t.Run(fmt.Sprintf("ashap-%d", scene.ticks), func(t *testing.T) {
			if testing.Short() && scene.ticks > 6000 {
				t.Skip("long fingerprint trajectory: run tools/check-retail --full")
			}
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
			// A scene either reaches its bound or ends the battle at its locked tick.
			if err != nil && !errors.Is(err, ErrTickLimit) {
				t.Fatalf("%s %q for %d ticks: %v", mode, lockAshapMap, scene.ticks, err)
			}
			if report.Tick != scene.end {
				t.Errorf("%s %q stopped at tick %d, want %d: the locked constant describes the full run", mode, lockAshapMap, report.Tick, scene.end)
			}
			if report.StateHash != scene.want {
				t.Errorf("%s %q after %d ticks fingerprints %s, want the locked %s: a diff that intends this must say which behaviour changed",
					mode, lockAshapMap, scene.ticks, report.StateHash, scene.want)
			}
			t.Logf("%s %q %d ticks: %s in %s", mode, lockAshapMap, report.Tick, report.StateHash, time.Since(start).Round(time.Millisecond))
		})
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
	if composed.InitialFingerprint != wantBenchInitial {
		t.Errorf("%s benchmark scene composes to %s, want the locked %s: the scene itself changed, so every later constant here is about a different workload",
			mode, composed.InitialFingerprint, wantBenchInitial)
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
			t.Errorf("%s benchmark scene after %d steps fingerprints %s, want the locked %s: a diff that intends this must say which behaviour changed",
				mode, tick, hash, want)
		}
		t.Logf("%s benchmark scene %d steps: %s in %s", mode, tick, hash, time.Since(start).Round(time.Millisecond))
	}
}
