package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// claimTestSystem binds the same stable System method that production installs,
// while retaining the small overlap fixture's direct view of owners and bits.
func claimTestSystem(t *testing.T, rules Rules, enabled bool, ownerState func(uint8) uint8) (*System, *overlapFixture, *OccupancyGrid) {
	t.Helper()
	f := newOverlapFixture(32)
	g, _ := overlapGrid(f)
	g.AttachOverlap(f, ownerState)
	s := &System{
		Grid:      g,
		Rules:     rules,
		Community: community.Features{GridClaimTieBreak: enabled},
	}
	g.claimConflict = s.claimConflict
	return s, f, g
}

func stampClaimKind(t *testing.T, kind string, s *System, g *OccupancyGrid, c Cell, id int) bool {
	t.Helper()
	switch kind {
	case "ground":
		return g.StampPlane(PlaneGround, c, 1, 1, id)
	case "air":
		return g.StampPlane(PlaneAir, c, 1, 1, id)
	case "building":
		return s.stampBuildingGrid(c, 1, 1, []world.YardCell{0x06}, false, id)
	default:
		t.Fatalf("unknown claim kind %q", kind)
		return false
	}
}

func claimKindOccupant(g *OccupancyGrid, kind string, c Cell) (int, bool) {
	if kind == "air" {
		return g.OccupantAtPlane(PlaneAir, c)
	}
	return g.OccupantAt(c)
}

// TestCommunityClaimStampCoversAllCellKinds locks the three patched stamp
// families. The owner-state read count must stay zero: enabled CP-DMG-2 depends
// only on the unsigned unit indices, including for an inactive owner
// (community-patch-engine.md CP-DMG-2).
func TestCommunityClaimStampCoversAllCellKinds(t *testing.T) {
	for _, kind := range []string{"ground", "air", "building"} {
		t.Run(kind, func(t *testing.T) {
			ownerStateReads := 0
			s, f, g := claimTestSystem(t, &CommunityRules{}, true, func(uint8) uint8 {
				ownerStateReads++
				return 0
			})
			cell := Cell{X: 5, Z: 5}
			f.add(9, 7, cell, 1, 1) // owner 7 may have no active player row
			f.add(4, 1, cell, 1, 1)

			if !stampClaimKind(t, kind, s, g, cell, 9) {
				t.Fatal("initial claim did not take the free cell")
			}
			if !stampClaimKind(t, kind, s, g, cell, 4) {
				t.Fatal("lower-index claimant did not take the contested cell")
			}
			if got, ok := claimKindOccupant(g, kind, cell); !ok || got != 4 {
				t.Fatalf("cell holds (%d,%v), want lower claimant 4", got, ok)
			}
			if !f.intruder[9] || f.host[9] || !f.host[4] || f.intruder[4] {
				t.Fatalf("overlap effects changed: incumbent=%v/%v claimant=%v/%v", f.host[9], f.intruder[9], f.host[4], f.intruder[4])
			}
			if ownerStateReads != 0 {
				t.Fatalf("enabled Community claim read inactive owner state %d times", ownerStateReads)
			}
		})
	}
}

// TestCommunityClaimOrderingSelfAndStrictBypass locks the comparison itself,
// the self fast path, and the feature-disabled/Strict owner-state answer.
func TestCommunityClaimOrderingSelfAndStrictBypass(t *testing.T) {
	communityRules := CommunityRules{}
	s := &System{Community: community.Features{GridClaimTieBreak: true}}
	if !communityRules.ClaimConflict(s, 0xffff, 0x8000) {
		t.Fatal("unsigned claimant 0x8000 should precede incumbent 0xffff")
	}
	if communityRules.ClaimConflict(s, 0x7fff, 0x8000) {
		t.Fatal("unsigned claimant 0x8000 should follow incumbent 0x7fff")
	}

	cell := Cell{X: 5, Z: 5}
	s, f, g := claimTestSystem(t, StrictRules{}, false, func(uint8) uint8 { return activeState })
	f.add(9, 1, cell, 1, 1)
	f.add(4, 2, cell, 1, 1)
	g.Stamp(cell, 1, 1, 9)
	if g.Stamp(cell, 1, 1, 4) {
		t.Fatal("Strict displaced an active incumbent")
	}
	// The callback is still the one installed above; replacing Rules is what a
	// command-boundary RebindRules does, and the next claim must see it.
	s.Rules = &CommunityRules{}
	s.Community.GridClaimTieBreak = true
	if !g.Stamp(cell, 1, 1, 4) {
		t.Fatal("stable grid callback retained the old Strict policy")
	}
	host, intruder := f.host[4], f.intruder[4]
	if !g.Stamp(cell, 1, 1, 4) {
		t.Fatal("self re-claim did not keep its cell")
	}
	if f.host[4] != host || f.intruder[4] != intruder {
		t.Fatal("self re-claim changed overlap flags")
	}

	// A Community profile with the switch off keeps the Strict branch.
	_, f2, g2 := claimTestSystem(t, &CommunityRules{}, false, func(uint8) uint8 { return activeState })
	f2.add(9, 1, cell, 1, 1)
	f2.add(4, 2, cell, 1, 1)
	g2.Stamp(cell, 1, 1, 9)
	if g2.Stamp(cell, 1, 1, 4) {
		t.Fatal("feature-disabled Community displaced an active incumbent")
	}
}

// TestCommunityClaimReclaimCoversAllCellKinds starts with Strict so two
// intruders queue behind a host, switches the same bound System to Community,
// then clears the host. The re-claim order offers 9 before 4; the lower index
// must still own the cell after both restamps for every cell kind.
func TestCommunityClaimReclaimCoversAllCellKinds(t *testing.T) {
	for _, kind := range []string{"ground", "air", "building"} {
		t.Run(kind, func(t *testing.T) {
			ownerState := activeState
			ownerStateReads := 0
			s, f, g := claimTestSystem(t, StrictRules{}, false, func(uint8) uint8 {
				ownerStateReads++
				return ownerState
			})
			cell := Cell{X: 5, Z: 5}
			// The fixture's unfiled traversal is its live order. Offer 9 before 4
			// at reclaim even though their initial Strict stamps occur 4 then 9.
			for _, id := range []int{10, 9, 4} {
				f.add(id, 1, cell, 1, 1)
			}
			f.restamp = func(id int) { stampClaimKind(t, kind, s, g, cell, id) }

			stampClaimKind(t, kind, s, g, cell, 10)
			stampClaimKind(t, kind, s, g, cell, 4)
			stampClaimKind(t, kind, s, g, cell, 9)
			if !f.intruder[4] || !f.intruder[9] || !f.host[10] {
				t.Fatal("Strict setup did not record the host and both intruders")
			}

			s.Rules = &CommunityRules{}
			s.Community.GridClaimTieBreak = true
			ownerState = 0 // the former host's player row is now inactive
			readsBeforeReclaim := ownerStateReads
			plane := PlaneGround
			if kind == "air" {
				plane = PlaneAir
			}
			g.ClearPlane(plane, cell, 1, 1, 10)
			if got, ok := claimKindOccupant(g, kind, cell); !ok || got != 4 {
				t.Fatalf("reclaimed cell holds (%d,%v), want lower claimant 4; order %v", got, ok, f.restamped)
			}
			if len(f.restamped) != 2 || f.restamped[0] != 9 || f.restamped[1] != 4 {
				t.Fatalf("reclaim order = %v, want [9 4] so the test exercises displacement", f.restamped)
			}
			if ownerStateReads != readsBeforeReclaim {
				t.Fatalf("enabled Community reclaim read inactive owner state %d times", ownerStateReads-readsBeforeReclaim)
			}
		})
	}
}
