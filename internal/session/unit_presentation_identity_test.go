package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"testing"
)

func TestPublishedUnitIdentityChangesOnSameSlotReplacement(t *testing.T) {
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "identity"}, MaxDamage: 1}
	w := newSessionFixtureWorld(2, nil)
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{Units: w, Snapshot: frame.NewBuffer()}
	s.publishSnapshot(1)
	first := s.Snapshot.Current().Units[0].InstanceID
	s.publishSnapshot(2)
	if first == 0 || s.Snapshot.Current().Units[0].InstanceID != first {
		t.Fatal("unchanged unit lost its identity")
	}
	prior := s.Snapshot.Current()
	w.FreeImmediate(h)
	replacement, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if replacement != h {
		t.Fatalf("fixture did not reuse slot: %d != %d", replacement, h)
	}
	s.publishSnapshot(3)
	if got := s.Snapshot.Current().Units[0].InstanceID; got == 0 || got == first {
		t.Fatal("replacement inherited retired cache identity")
	}
	if prior.Units[0].InstanceID != first {
		t.Fatal("replacement changed prior committed identity")
	}
	w.FreeImmediate(replacement)
	s.publishSnapshot(4)
	if s.publication.unitIdentities[h].unit != nil {
		t.Fatal("publication retained retired object")
	}
}
