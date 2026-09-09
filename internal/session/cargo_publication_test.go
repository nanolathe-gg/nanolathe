package session

import (
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// Attachment order changes independently of allocation, and both committed
// ticks must remain detached from the live linkage [04 R-UNIT-06 §3][I6].
func TestCargoPublicationPreservesAttachAndReattachOrder(t *testing.T) {
	s := newLoopTestSession(t, 0)
	def := s.Catalog.Units["armcom"]
	handles := make([]pool.Handle, 3)
	for i := range handles {
		h, err := s.Units.Create(def, 0, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		handles[i] = h
	}
	carrier, low, high := handles[0], handles[1], handles[2]
	for _, child := range []pool.Handle{high, low} {
		if !movement.AttachCargo(s.Units, carrier, child, 0) {
			t.Fatal("attach failed")
		}
	}
	s.publishSnapshot(1)
	first := s.Snapshot.Current()
	check := func(f *frame.Frame, want []pool.Handle) {
		t.Helper()
		for _, u := range f.Units {
			if u.Slot == carrier {
				if !slices.Equal(u.Cargo, want) {
					t.Fatalf("cargo = %v, want %v", u.Cargo, want)
				}
				return
			}
		}
		t.Fatal("carrier missing")
	}
	check(first, []pool.Handle{low, high})
	if _, ok := movement.DetachCargo(s.Units, high); !ok {
		t.Fatal("detach failed")
	}
	if !movement.AttachCargo(s.Units, carrier, high, 0) {
		t.Fatal("reattach failed")
	}
	check(first, []pool.Handle{low, high})
	s.publishSnapshot(2)
	check(s.Snapshot.Current(), []pool.Handle{high, low})
	check(s.Snapshot.Previous(), []pool.Handle{low, high})
	// Detach compacts the live slice in place: neither published list may alias it.
	if _, ok := movement.DetachCargo(s.Units, high); !ok {
		t.Fatal("second detach failed")
	}
	check(first, []pool.Handle{low, high})
	check(s.Snapshot.Current(), []pool.Handle{high, low})
	s.publishSnapshot(3)
	check(s.Snapshot.Current(), []pool.Handle{low})
	check(s.Snapshot.Previous(), []pool.Handle{high, low})
	s.publishSnapshot(4)
	check(s.Snapshot.Current(), []pool.Handle{low})
}
