//go:build pathbench && retail

package session

import (
	"fmt"
	"sort"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Wreck family (docs/PATH_BENCHMARK_TRAFFIC.md "Wrecks over live units"):
// authored development experiments on this engine, not findings about the
// retail executable. Each stamps a wreck over live ground units with
// production services — the ordinary death path or the feature service — and
// then orders the survivors across open ground, which exercises Modern wedge
// escape (docs/DESIGN_MOVEMENT_PATH.md "Modern wedge escape").
func init() {
	pbRegister(
		pbCase{"wreck/jam_corpses", "wreck", "Touching 2x2 block; interleaved Jammers die through the ordinary death path and their 3x3 wrecks cover the survivors beside and behind them; the survivors are ordered east", []int{16, 64}, 1500, pbWreckJamCorpses},
		pbCase{"wreck/wreck_over", "wreck", "Loose 2x2 group; the feature service stamps each unit's own 2x2 wreck exactly over every other unit, the end state of a unit dying inside another; the group is ordered east", []int{8, 32}, 1200, pbWreckOver},
	)
}

// pbWreckGrid is a size-unit block's column and row count.
func pbWreckGrid(t *testing.T, size int) (cols, rows int32) {
	switch size {
	case 8:
		return 4, 2
	case 16:
		return 4, 4
	case 32:
		return 8, 4
	case 64:
		return 8, 8
	}
	t.Fatalf("unsupported wreck case size %d", size)
	return 0, 0
}

// pbWreckKill kills a unit through the session's ordinary death path: the
// damage kind is ordinary weapon damage and the health just below zero while
// the prior health sample is still the one from before the unit's first
// 30-tick window closed, so the Killed query sees the lowest severity and the
// phase-2 finalizer stamps the definition's authored corpse at the unit's
// committed anchor [06 §12.1][05 R-FEAT-01 §13].
func pbWreckKill(t *testing.T, s *pbScene, h pool.Handle) {
	t.Helper()
	u := s.S.Units.Unit(h)
	if u == nil || !u.Alive {
		t.Fatalf("victim %d is not alive", h)
	}
	u.Health = -1
	u.LastDamageCause = uint8(combat.CauseOrdinary)
	s.S.Units.DestroyBy(h, units.DeathKilled, 0)
}

// The orders come at tick 70, after the zero request stamp's 60-tick throttle
// has run out, so the first search is admitted at once as it is in a battle;
// an earlier order would leave every unit on its synthetic line until tick 60.
func pbWreckJamCorpses(t *testing.T, rules string, size int) *pbScene {
	cols, rows := pbWreckGrid(t, size)
	sc := pbNew(t, rules, pbTerrain(t, 128, 96, 20, 0))
	const x0, z0 = 12, 30
	var survivors, victims []*pbActor
	for j := int32(0); j < rows; j++ {
		for i := int32(0); i < cols; i++ {
			key, cohort := "armflash", "survivor"
			victim := i%3 == 1 && j%2 == 1
			if victim {
				key, cohort = "armjam", "victim"
			}
			a := pbAdd(t, sc, key, cohort, 0, x0+2*i, z0+2*j)
			if victim {
				a.ExpectedRemoval = true
				victims = append(victims, a)
			} else {
				survivors = append(survivors, a)
			}
		}
	}
	gx, gz := x0+2*cols+44, z0+rows
	sc.Events = append(sc.Events,
		pbEvent{Tick: 50, Label: fmt.Sprintf("kill %d armjam through the ordinary death path (ordinary damage kind, health -1, lowest severity)", len(victims)), Apply: func(t *testing.T, s *pbScene) {
			for _, v := range victims {
				pbWreckKill(t, s, v.Handle)
			}
		}},
		pbEvent{Tick: 51, Label: "verify each victim left armjam_dead at its anchor", Apply: func(t *testing.T, s *pbScene) {
			for _, v := range victims {
				if s.S.Units.Unit(v.Handle) != nil {
					t.Fatalf("victim %d was not finalized", v.Handle)
				}
				if f := s.S.Features.InstanceAt(int(v.Start[0]), int(v.Start[1])); f == nil || f.Def == nil || f.Def.CanonicalKey != "armjam_dead" {
					t.Fatalf("victim %d left no armjam_dead at %v", v.Handle, v.Start)
				}
			}
		}},
		pbEvent{Tick: 70, Label: fmt.Sprintf("order %d survivors to (%d,%d)", len(survivors), gx, gz), Apply: func(t *testing.T, s *pbScene) {
			pbMove(t, s, survivors, gx, gz, false, false)
		}},
	)
	sc.Regions = []pbRegion{{Name: "block", X0: x0, Z0: z0, X1: x0 + 2*cols + 1, Z1: z0 + 2*rows + 1}}
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("block %dx%d of 2x2 units at (12+2i,30+2j), touching; armjam where i%%3==1 && j%%2==1, armflash elsewhere; kill tick 50, order tick 70 to (%d,%d)", cols, rows, gx, gz))
	sc.Notes = append(sc.Notes, "armjam's 3x3 wreck covers the survivor east of the victim (west column), south (north row) and south-east (north-west cell); the stamp tests no unit occupancy.")
	return sc
}

func pbWreckOver(t *testing.T, rules string, size int) *pbScene {
	cols, rows := pbWreckGrid(t, size)
	sc := pbNew(t, rules, pbTerrain(t, 128, 96, 20, 0))
	const x0, z0 = 12, 30
	def := sc.S.Catalog.Features["corak_dead"]
	if def == nil || !def.Blocking || def.FootprintX != 2 || def.FootprintZ != 2 {
		t.Fatal("required blocking 2x2 retail feature corak_dead absent")
	}
	var all, covered []*pbActor
	for j := int32(0); j < rows; j++ {
		for i := int32(0); i < cols; i++ {
			a := pbAdd(t, sc, "corak", "group", 0, x0+3*i, z0+3*j)
			all = append(all, a)
			if (i+j)%2 == 0 {
				covered = append(covered, a)
			}
		}
	}
	gx, gz := x0+3*cols+44, z0+rows*3/2
	sc.Events = append(sc.Events,
		pbEvent{Tick: 50, Label: fmt.Sprintf("stamp corak_dead exactly over %d standing corak through the feature service", len(covered)), Apply: func(t *testing.T, s *pbScene) {
			for _, a := range covered {
				anchor, _, _, ok := s.S.Movement.CommittedFootprint(a.Handle)
				if !ok {
					t.Fatalf("unit %d has no committed footprint", a.Handle)
				}
				if s.S.Features.PlaceAt(int(anchor.X), int(anchor.Z), def) == nil {
					t.Fatalf("stamp over %d at %v refused", a.Handle, anchor)
				}
			}
		}},
		pbEvent{Tick: 70, Label: fmt.Sprintf("order %d units to (%d,%d)", len(all), gx, gz), Apply: func(t *testing.T, s *pbScene) {
			pbMove(t, s, all, gx, gz, false, false)
		}},
	)
	sc.Regions = []pbRegion{{Name: "group", X0: x0, Z0: z0, X1: x0 + 3*cols, Z1: z0 + 3*rows}}
	sc.Inputs = append(sc.Inputs, fmt.Sprintf("group %dx%d corak at (12+3i,30+3j), one-cell gaps; corak_dead stamped at the anchor of every unit with (i+j) even at tick 50; order tick 70 to (%d,%d)", cols, rows, gx, gz))
	sc.Notes = append(sc.Notes, "The stamped wreck covers the whole footprint of the unit under it: every neighbouring anchor overlaps it, so the retail validator rejects every step.")
	return sc
}

// pbWedged reports whether the unit's committed footprint covers a cell the
// commit's static test rejects.
func pbWedged(sc *pbScene, h pool.Handle) bool {
	anchor, fx, fz, ok := sc.S.Movement.CommittedFootprint(h)
	if !ok {
		return false
	}
	prof := sc.S.Movement.ProfileFor(h)
	for z := anchor.Z; z < anchor.Z+int32(fz); z++ {
		for x := anchor.X; x < anchor.X+int32(fx); x++ {
			if !prof.IsPassableCommitCell(sc.S.World, x, z) {
				return true
			}
		}
	}
	return false
}

// In wreck/wreck_over with eight units, the four under a stamped wreck never
// leave it under Strict 3.1 or with wedge escape switched off, and all four
// leave it under Modern (docs/DESIGN_MOVEMENT_PATH.md "Modern wedge escape").
func TestPathBenchWreckEscape(t *testing.T) {
	var c pbCase
	for _, k := range pbCases {
		if k.ID == "wreck/wreck_over" {
			c = k
		}
	}
	for _, tc := range []struct {
		rules string
		left  int
	}{{"strict-3.1", 0}, {"modern-no-wedge", 0}, {"modern", 4}} {
		sc := c.Build(t, tc.rules, 8)
		sort.SliceStable(sc.Events, func(i, j int) bool { return sc.Events[i].Tick < sc.Events[j].Tick })
		wedged := 0
		for k := 1; k <= c.Ticks; k++ {
			for _, e := range sc.Events {
				if e.Tick == k {
					e.Apply(t, sc)
				}
			}
			sc.S.stepAuthoritativePhases(sc.S.Clock.BeginSubTick())
			if k == 51 {
				for _, a := range sc.Actors {
					if pbWedged(sc, a.Handle) {
						wedged++
					}
				}
			}
		}
		stillWedged := 0
		for _, a := range sc.Actors {
			if pbWedged(sc, a.Handle) {
				stillWedged++
			}
		}
		if wedged != 4 || wedged-stillWedged != tc.left {
			t.Fatalf("%s: %d wedged after the stamp, %d left by tick %d; want 4 and %d", tc.rules, wedged, wedged-stillWedged, c.Ticks, tc.left)
		}
	}
}
