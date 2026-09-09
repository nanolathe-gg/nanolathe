package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

func TestCachePublicationCopiesFlagAndRevisionWithoutConsuming(t *testing.T) {
	prog := &cob.Program{
		Code: []uint32{
			0x10008000, 0, 0x10065000, // Create: dont-cache
			0x10007000, 0, 0x10065000, // Cache: cache
			0x10021001, 9, 0x1000b000, 0, 0, 0x10065000, // Move: changed cached X
		},
		Pieces:      []string{"base"},
		Scripts:     map[string]int{"Create": 0, "Cache": 3, "Move": 6},
		ScriptsByID: []int{0, 3, 6},
	}
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "cache-publication"}, MaxDamage: 1, Script: prog}
	w := newSessionFixtureWorld(2, nil)
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	u := w.Unit(h)
	vm := u.GetScript()
	if vm == nil || vm.CacheRevision() != 1 {
		t.Fatalf("Create cache revision = %v, want 1", vm)
	}
	s := &Session{Snapshot: frame.NewBuffer(), Units: w, LocalOwner: 0}
	s.publishSnapshot(1)
	first := s.Snapshot.Current()
	if first == nil || len(first.Units) != 1 || len(first.Units[0].Pieces) != 1 {
		t.Fatalf("first publication = %#v", first)
	}
	firstUnit := first.Units[0]
	if !firstUnit.Pieces[0].DontCache || firstUnit.CacheRevision != 1 || firstUnit.CacheValidityRevision != 0 || vm.CacheRevision() != 1 {
		t.Fatalf("first cache publication = %#v, vm revision %d", firstUnit, vm.CacheRevision())
	}
	if !vm.StartByName("Cache", nil) {
		t.Fatal("start Cache")
	}
	vm.Drain(1)
	if vm.CacheRevision() != 2 {
		t.Fatalf("Cache revision after script = %d, want 2", vm.CacheRevision())
	}
	// The first committed frame is immutable after the source changes.
	if !firstUnit.Pieces[0].DontCache || firstUnit.CacheRevision != 1 {
		t.Fatalf("first frame changed after VM mutation: %#v", firstUnit)
	}
	s.publishSnapshot(2)
	second := s.Snapshot.Current()
	if second == nil || len(second.Units) != 1 || second.Units[0].Pieces[0].DontCache || second.Units[0].CacheRevision != 2 {
		t.Fatalf("second cache publication = %#v", second)
	}
	if !vm.StartByName("Move", nil) {
		t.Fatal("start Move")
	}
	vm.Drain(1)
	s.publishSnapshot(3)
	third := s.Snapshot.Current()
	if third.Units[0].CacheValidityRevision != 1 || third.Units[0].CacheRevision != 3 {
		t.Fatal("cached pose validity cause not published")
	}
	if firstUnit.CacheValidityRevision != 0 {
		t.Fatal("old publication validity changed")
	}

}
