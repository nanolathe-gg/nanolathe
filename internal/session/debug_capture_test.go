package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

func TestDebugCaptureDoesNotCreateQueuesOrMintIdentities(t *testing.T) {
	w := newSessionFixtureWorld(2, nil)
	h, err := w.Create(&content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "capture-fixture"}, MaxDamage: 10}, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	u := w.Unit(h)
	u.Orders = nil
	s := &Session{Units: w, Snapshot: frame.NewBuffer()}
	s.SeedSessionRNG(7, 9)
	sim, crt := s.rngSim, s.rngCrt
	var got DebugUnit
	if err := s.VisitDebugUnits(func(d DebugUnit) error { got = d; return nil }); err != nil {
		t.Fatal(err)
	}
	if u.Orders != nil || s.publication != nil || got.PublishedIdentity != 0 {
		t.Fatal("read created queue/publication identity")
	}
	if sim != s.rngSim || crt != s.rngCrt {
		t.Fatal("read consumed RNG")
	}
	u.Health = 1
	if got.Unit.Health != 10 {
		t.Fatal("snapshot aliases unit")
	}
	s.result.Winners = []int{1}
	d := s.DebugSnapshot()
	s.result.Winners[0] = 2
	if d["result"].(Result).Winners[0] != 1 {
		t.Fatal("result aliases live state")
	}
}

func TestDebugCaptureIdentityDoesNotFollowReusedSlot(t *testing.T) {
	w := newSessionFixtureWorld(2, nil)
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "capture-reuse"}, MaxDamage: 10}
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{Units: w, publication: newPublicationState(nil)}
	oldID := s.publication.unitIdentity(w.Unit(h))
	w.Destroy(h, 0)
	w.FinalizeDeath(h, 1)
	next, err := w.CreateWithForcedSlot(def, 0, 0, 0, 0, h)
	if err != nil {
		t.Fatal(err)
	}
	if next != h {
		t.Fatal("fixture did not reuse slot")
	}
	var d DebugUnit
	if err = s.VisitDebugUnits(func(v DebugUnit) error { d = v; return nil }); err != nil {
		t.Fatal(err)
	}
	if d.PublishedIdentity != 0 || s.publication.nextUnitIdentity != oldID {
		t.Fatal("capture attributed old occupant identity to replacement or minted a new identity")
	}
}
