package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// TestUnitViewPublishesTheTwoCloakInputs locks the publication half of step 2
// of the visibility gate [03 §3.2][03 §3.4]: the committed unit view carries
// the instance cloak bit and the decloak timer as their own fields, so
// presentation never reconstructs cloak state from the instance flag word.
//
// It used to reconstruct it, reading flag bit 2 as "cloaked" and bit 12 as the
// timer. Bit 2 is the construction layer's start-building edge, so every enemy
// builder disappeared the moment it began building [R-VIS-01 §4].
func TestUnitViewPublishesTheTwoCloakInputs(t *testing.T) {
	unitsPool := newSessionFixtureWorld(4, nil)
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "foreign"}, MaxDamage: 1}
	h, err := unitsPool.Create(def, 1, 0, 0, 0)
	if err != nil {
		t.Fatalf("create foreign unit: %v", err)
	}
	u := unitsPool.Unit(h)
	u.Hidden = true
	// The start-building edge is on the same word and must not read as cloak.
	u.Flags |= 1 << 2

	s := &Session{Snapshot: frame.NewBuffer(), Units: unitsPool, LocalOwner: 0}
	u.Flags |= visibility.DecloakBit
	s.publishSnapshot(1)

	got := s.Snapshot.Current()
	if got == nil || len(got.Units) != 1 {
		t.Fatalf("publication produced %v", got)
	}
	v := got.Units[0]
	if !v.Cloaked {
		t.Fatal("the instance cloak bit did not reach the committed frame")
	}
	if !v.Decloaking {
		t.Fatal("the decloak timer did not reach the committed frame")
	}

	// With the timer cleared the same unit publishes as cloaked and not
	// decloaking, which is what makes the gate hide it.
	u.Flags &^= visibility.DecloakBit
	s.publishSnapshot(2)
	if v := s.Snapshot.Current().Units[0]; !v.Cloaked || v.Decloaking {
		t.Fatalf("cloaked/decloaking = %v/%v, want true/false", v.Cloaked, v.Decloaking)
	}
}
