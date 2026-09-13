package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func modeCommitVisit(s *System, u *units.Unit, tick uint32) StepResult {
	s.BeginTick(tick)
	result := s.StepUnit(u.Handle, tick)
	s.EndTick(tick)
	return result
}

// The ordinary commit owns touchdown admission. A refused request keeps its
// airborne mirror, footprint and height [04 R-AIR-01 §6][04 R-COLL-01 §1].
func TestModeCommitRefusedTouchdownPreservesAirborneMirror(t *testing.T) {
	s, w, u := takeoffFixture(t)
	u.Def.Upright = true
	s.SetMoverMode(u, 2)
	modeCommitVisit(s, u, 1)
	c := handleRow(s.Collisions, u.Handle)
	anchor, stampTick := c.CachedAnchor, c.LastStampTick
	u.Y = numeric.Fixed(40 << 16)
	blockerDef := *u.Def
	blockerDef.CanFly = false
	h, err := w.Create(&blockerDef, 0, u.X, 0, u.Z)
	if err != nil {
		t.Fatal(err)
	}
	s.EnsureUnit(w.Unit(h))
	n := &orders.Node{Phase: 2}
	if code := s.legVTOLLandIfCan(u, n, 0x20, 2); code != 5 {
		t.Fatalf("touchdown code=%d", code)
	}
	if u.Move.Mode != 1 || u.Move.ModeMirror != 2 || c.CachedMode != 2 {
		t.Fatal("touchdown eagerly committed its request")
	}
	result := modeCommitVisit(s, u, 2)
	if !result.Blocked || !c.Blocked || c.CachedMode != 2 || u.Move.ModeMirror != 2 || handleRow(s.Flights, u.Handle).ModeMirror != 2 {
		t.Fatal("refused touchdown changed its committed mirror")
	}
	if u.Y != numeric.Fixed(40<<16) || c.CachedAnchor != anchor || c.LastStampTick != stampTick || c.LastProposalTick != 2 {
		t.Fatal("refused touchdown changed height, anchor or stamp age")
	}
	ground, air := plotWords(t, s.Terrain, anchor)
	if ground != int16(h) || air != int16(u.Handle) {
		t.Fatalf("refused touchdown occupancy=(%d,%d)", ground, air)
	}
	if u.MoveTier != 0 {
		t.Fatalf("blocked callback tier=%d", u.MoveTier)
	}
	// The pending request reaches the ordinary validator again on the next
	// visit; no landing-order retry or new search is introduced.
	modeCommitVisit(s, u, 3)
	if c.LastProposalTick != 3 || c.LastStampTick != stampTick || u.Y != numeric.Fixed(40<<16) {
		t.Fatal("pending refusal lost ordinary commit behavior")
	}
}

// Two air stamps may overlap, but only the first successful touchdown can
// claim their shared ground footprint [04 R-COLL-01 §1, §2, §4].
func TestModeCommitSimultaneousTouchdownsClaimGroundInVisitOrder(t *testing.T) {
	s, w, first := takeoffFixture(t)
	first.Def.Upright = true
	s.SetMoverMode(first, 2)
	modeCommitVisit(s, first, 1)
	h, err := w.Create(first.Def, 0, first.X, numeric.Fixed(40<<16), first.Z)
	if err != nil {
		t.Fatal(err)
	}
	second := w.Unit(h)
	second.Move.Mode, second.Move.ModeMirror = 2, 2
	second.RestoredMoveMode = true
	s.EnsureUnit(second)
	first.Y = second.Y
	for _, u := range []*units.Unit{first, second} {
		if code := s.legVTOLLandIfCan(u, &orders.Node{Phase: 2}, 0x20, 2); code != 5 {
			t.Fatalf("touchdown code=%d", code)
		}
	}
	s.BeginTick(2)
	a, b := s.StepUnit(first.Handle, 2), s.StepUnit(second.Handle, 2)
	s.EndTick(2)
	if a.Blocked || !b.Blocked || first.Move.ModeMirror != 1 || second.Move.ModeMirror != 2 {
		t.Fatalf("touchdown verdicts=%+v/%+v mirrors=%d/%d", a, b, first.Move.ModeMirror, second.Move.ModeMirror)
	}
	if first.Y != s.Terrain.HeightAt(first.X, first.Z) || second.Y != numeric.Fixed(40<<16) {
		t.Fatal("ground correction did not follow accepted mirrors")
	}
	anchor := handleRow(s.Collisions, first.Handle).CachedAnchor
	ground, air := plotWords(t, s.Terrain, anchor)
	if ground != int16(first.Handle) || air == int16(first.Handle) || handleRow(s.Collisions, second.Handle).StampedPlane != PlaneAir {
		t.Fatalf("landing occupancy=(%d,%d)", ground, air)
	}
}

// Admission consumes the accepted mirror during both pending transition
// directions [04 R-AIR-01 §12].
func TestModeCommitTransportAdmissionUsesCommittedMirror(t *testing.T) {
	s, w, carrier, candidate, _, _ := transportFixture(t)
	for _, tc := range []struct {
		mode, mirror uint8
		allowed      bool
	}{{2, 1, true}, {1, 2, false}} {
		candidate.Move.Mode, candidate.Move.ModeMirror = tc.mode, tc.mirror
		handleRow(s.Collisions, candidate.Handle).Mode = tc.mode
		got := s.CanTransport(carrier.Handle, candidate.Handle, w)
		if got.Allowed != tc.allowed || (!tc.allowed && got.Reason != "moving") {
			t.Fatalf("mode/mirror=%d/%d admission=%+v", tc.mode, tc.mirror, got)
		}
	}
}
