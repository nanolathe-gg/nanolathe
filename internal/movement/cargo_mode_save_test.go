package movement

import (
	"encoding/binary"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// A carrier can release after the cargo's visit. Until the next cargo commit,
// its saved unit mirror remains carried while the mover is grounded
// [04 R-AIR-01 §10 item 2][08 R-SAVE-02 §6, §8].
func TestReleasedCargoSaveKeepsPendingModeCommit(t *testing.T) {
	s, w, carrier, cargo, _, _ := transportFixture(t)
	s.SetMoverMode(carrier, 2)
	if !AttachCargo(w, carrier.Handle, cargo.Handle, 0) {
		t.Fatal("attach")
	}
	s.BeginTick(1)
	s.StepUnit(cargo.Handle, 1)
	if handleRow(s.Collisions, cargo.Handle).CachedMode != 0 {
		t.Fatal("cargo did not commit carried mode")
	}
	s.releaseUnloadCargo(w, carrier, cargo)
	resolve := func(h pool.Handle) (uint16, bool) { return uint16(h), w.Unit(h) != nil }
	base, err := units.RetailUnitImage(cargo, 0, resolve, resolve, units.RetailUnitWriterScratch{})
	if err != nil {
		t.Fatal(err)
	}
	mover, err := s.RetailMoverImage(cargo.Handle)
	if err != nil {
		t.Fatal(err)
	}
	if mirror, mode := (binary.LittleEndian.Uint32(base[0xb4:])>>4)&3, mover[34]&3; mirror != 0 || mode != 1 {
		t.Fatalf("saved unit/mover mode=%d/%d, want 0/1", mirror, mode)
	}
	if err := units.RetailUnitBase(cargo, base); err != nil {
		t.Fatal(err)
	}
	if err := s.RestoreMover(cargo.Handle, mover); err != nil {
		t.Fatal(err)
	}
	c := handleRow(s.Collisions, cargo.Handle)
	if err := s.RestoreOccupancy(cargo.Handle, int16(c.CachedAnchor.X), int16(c.CachedAnchor.Z)); err != nil {
		t.Fatal(err)
	}
	if c.CachedMode != 0 || c.Mode != 1 {
		t.Fatalf("restored cached/mover mode=%d/%d, want 0/1", c.CachedMode, c.Mode)
	}
	s.BeginTick(2)
	s.StepUnit(cargo.Handle, 2)
	if cargo.Move.Mode != 1 || c.CachedMode != 1 {
		t.Fatalf("next cargo visit lost release: mover/cache=%d/%d", cargo.Move.Mode, c.CachedMode)
	}
}

// Pickup after the cargo's visit changes the live mover mode immediately, but
// leaves the published unit mode until the next carried commit
// [04 R-AIR-01 §9][08 R-SAVE-02 §6, §8].
func TestAttachedCargoSaveKeepsPendingModeCommit(t *testing.T) {
	s, w, carrier, cargo, _, _ := transportFixture(t)
	s.SetMoverMode(carrier, 2)
	s.BeginTick(1)
	s.StepUnit(cargo.Handle, 1)
	carrier.X, carrier.Z = cargo.X, cargo.Z
	if !AttachCargo(w, carrier.Handle, cargo.Handle, 0) {
		t.Fatal("attach")
	}
	resolve := func(h pool.Handle) (uint16, bool) { return uint16(h), w.Unit(h) != nil }
	base, err := units.RetailUnitImage(cargo, 0, resolve, resolve, units.RetailUnitWriterScratch{})
	if err != nil {
		t.Fatal(err)
	}
	mover, err := s.RetailMoverImage(cargo.Handle)
	if err != nil {
		t.Fatal(err)
	}
	if mirror, mode := (binary.LittleEndian.Uint32(base[0xb4:])>>4)&3, mover[34]&3; mirror != 1 || mode != 0 {
		t.Fatalf("saved unit/mover mode=%d/%d, want 1/0", mirror, mode)
	}
	if err := units.RetailUnitBase(cargo, base); err != nil {
		t.Fatal(err)
	}
	if err := s.RestoreMover(cargo.Handle, mover); err != nil {
		t.Fatal(err)
	}
	c := handleRow(s.Collisions, cargo.Handle)
	if err := s.RestoreOccupancy(cargo.Handle, int16(c.CachedAnchor.X), int16(c.CachedAnchor.Z)); err != nil {
		t.Fatal(err)
	}
	if c.CachedMode != 1 || c.Mode != 0 {
		t.Fatalf("restored cached/mover mode=%d/%d, want 1/0", c.CachedMode, c.Mode)
	}
	s.BeginTick(2)
	s.StepUnit(cargo.Handle, 2)
	if cargo.Move.Mode != 0 || cargo.Move.ModeMirror != 0 || c.CachedMode != 0 {
		t.Fatalf("next cargo visit lost pickup: mover/mirror/cache=%d/%d/%d", cargo.Move.Mode, cargo.Move.ModeMirror, c.CachedMode)
	}
}
