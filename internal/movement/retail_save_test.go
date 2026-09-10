package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func TestRetailMoverImagePreservesEstablishedState(t *testing.T) {
	h := pool.Handle(7)
	s := &System{}
	s.growHandleTables(int(h))
	setHandleRow(&s.Collisions, h, &CollisionState{
		VX: 1, VY: -2, VZ: 3, LeanX: 4, LeanY: -5, LeanZ: 6,
		Speed: 7, TurnResidual: -8, LastStampTick: 9, Mode: 2,
		Blocked: true, SavedStateByte: 0xa0,
	})
	image, err := s.RetailMoverImage(h)
	if err != nil {
		t.Fatal(err)
	}
	if image[34] != 0xa6 {
		t.Fatalf("state byte %#x, want %#x", image[34], 0xa6)
	}
	terrain := syntheticTerrainForIntegrate()
	s2 := NewSystem(terrain, Profile{FootPrintX: 1, FootPrintZ: 1}, NewOccupancyGrid())
	w := newMovementFixtureWorld(10)
	def := &content.UnitDef{UnitName: "mover-save", FootprintX: 1, FootprintZ: 1, MaxVelocity: 4 << 16}
	restoredHandle, err := w.CreateWithForcedSlot(def, 0, world.CellToWorld(2), numeric.Fixed(10<<16), world.CellToWorld(2), h)
	if err != nil || restoredHandle != h {
		t.Fatalf("forced unit: handle=%d err=%v", restoredHandle, err)
	}
	s2.BindWorld(w)
	s2.EnsureUnit(w.Unit(h))
	if err := s2.RestoreMover(h, image); err != nil {
		t.Fatal(err)
	}
	again, err := s2.RetailMoverImage(h)
	if err != nil {
		t.Fatal(err)
	}
	if string(image) != string(again) {
		t.Fatalf("mover image unstable: %x != %x", image, again)
	}
}
