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

	scaled := sess.Clock.ScaledAnchor
	puffs := 0
	for i := 0; i < 60 && puffs == 0; i++ {
		scaled += 5
		sess.Step(scaled)
		f := sess.Snapshot.Current()
		if f == nil {
			continue
		}
		for _, e := range f.Effects {
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
			if e.Graphic == "" {
				t.Fatalf("the mirrored puff carries no graphic identity, so no composer can resolve it")
			}
		}
	}
	if puffs == 0 {
		t.Fatalf("the vent at (%d,%d) published no strip-4 steam in 60 ticks", ventX, ventZ)
	}
}
