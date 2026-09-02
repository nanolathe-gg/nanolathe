package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/headless"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/vfs"
)

// TestGeothermalVentSteamsOnGreatDivide is the play-test report's second half
// — "see the steam" — end to end, from the map's own vent to the committed
// frame a composer draws.
//
// Every link was missing at once: nothing wrote strip 4 at all
// [05 R-ECO-02 §3], the map-authored vents never reached the stamp that runs
// the producer, and the smoke family's wind drift was scaled in whole world
// units instead of raw fixed point, which threw a puff twenty cells a tick.
// The last one is why the assertion below bounds the distance: a plume that
// leaves the map is not a plume.
func TestGeothermalVentSteamsOnGreatDivide(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	composed, err := headless.ComposeFreshBattle(headless.FreshBattleRequest{
		Kind: headless.ScenarioSkirmish, Map: "Great Divide", LocalOwner: 0, FS: fs,
	})
	if err != nil {
		t.Skipf("Great Divide unavailable: %v", err)
	}
	sess := composed.Session

	// The map's own vent, found through the feature service rather than by a
	// hard-coded cell.
	var ventX, ventZ int64 = -1, -1
	for _, inst := range sess.Features.Instances() {
		if inst != nil && inst.Def != nil && inst.Def.Geothermal {
			ventX, ventZ = inst.X.Raw()>>16, inst.Z.Raw()>>16
			break
		}
	}
	if ventX < 0 {
		t.Skip("Great Divide composed without its geothermal vent")
	}

	// The frame-count seam, filled the way the composer fills it: after the
	// session exists, and therefore after the map stamp already built every
	// vent's container. Filling it must finish those containers, or the plume
	// never retires a puff — see TestSteamCreatedBeforeTheSeamStillRetires.
	sess.SetEffectEntryFrameCount(func(bank, entry string) (int, bool) {
		if entry != "smoke 1" {
			return 0, false
		}
		return 12, true // the stock `anims/fx.gaf` entry's frame count
	})

	scaled := sess.Clock.ScaledAnchor
	puffs := 0
	for i := 0; i < 60 && puffs == 0; i++ {
		scaled += 5
		sess.Step(scaled)
		f := sess.Snapshot.Current()
		if f == nil {
			continue
		}
		// The vent's puffs reach presentation on the committed frame's own
		// strip channel, in the composer's walk order [03 §1][03 R-STRIP-01 §2].
		for _, e := range f.Strips {
			if e.Strip != 4 {
				continue
			}
			puffs++
			dx, dz := e.X.Raw()>>16-ventX, e.Z.Raw()>>16-ventZ
			if dx < 0 {
				dx = -dx
			}
			if dz < 0 {
				dz = -dz
			}
			// A puff drifts by the wind word times eight of a RAW 16.16 unit
			// per tick, so it stays within a world unit or two of the vent for
			// its whole life [R-WIND-01].
			if dx > 16 || dz > 16 {
				t.Fatalf("a steam puff is %d,%d world units from its vent at (%d,%d); the drift scale is wrong again", dx, dz, ventX, ventZ)
			}
			if e.Entry == "" || e.Bank == "" {
				t.Fatalf("the mirrored puff carries no (bank, entry) identity pair, so no composer can resolve it [06 R-WFX-01 §1]")
			}
		}
	}
	if puffs == 0 {
		t.Fatalf("the vent at (%d,%d) published no strip-4 steam in 60 ticks", ventX, ventZ)
	}

	// And the plume must still be there, and still be a plume, a full minute
	// later. Two things have to hold at once and they pull against each other:
	// the container never stops emitting — its removal verdict is a constant
	// false and its spawn predicate has no deadline term
	// [03 R-FX-01 §3 addendum] — while each puff dies when its cursor reaches
	// its own last frame [06 R-WFX-01 §5]. Fail either and the play test sees
	// it: a plume that stops after five seconds, or one that piles up and slides
	// downwind forever.
	for i := 0; i < 1800; i++ {
		scaled++
		sess.Step(scaled)
	}
	late, farthest := 0, int64(0)
	if f := sess.Snapshot.Current(); f != nil {
		for _, e := range f.Strips {
			if e.Strip != 4 {
				continue
			}
			late++
			dx, dz := e.X.Raw()>>16-ventX, e.Z.Raw()>>16-ventZ
			if dx < 0 {
				dx = -dx
			}
			if dz < 0 {
				dz = -dz
			}
			if dx+dz > farthest {
				farthest = dx + dz
			}
		}
	}
	if late == 0 {
		t.Fatal("the vent stopped steaming; retail's plume runs for the whole battle [03 R-FX-01 §3 addendum]")
	}
	// One spawn every five ticks over 1800 ticks is 360 puffs. A plume that
	// retires nothing holds all of them and walks off the map with the wind.
	if late > 40 {
		t.Fatalf("the vent holds %d live puffs; they are not retiring at their last frame [06 R-WFX-01 §5]", late)
	}
	if farthest > 200 {
		t.Fatalf("a puff is %d world units from its vent after a minute; retail's plume stays anchored", farthest)
	}
}
