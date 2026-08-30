package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// The unit painter's pass selector is the committed low two bits of the mover
// mode word: mode 2 is airborne and belongs to pass B, which paints after the
// projectile and effect strips [03 R-RAST-01 §7][04 R-MOV-01 §8].
func TestPublishSnapshotCarriesCommittedMoverMode(t *testing.T) {
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "flyer"}, MaxDamage: 1}
	w := newSessionFixtureWorld(2, nil)
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create unit: %v", err)
	}
	w.Unit(h).Move.Mode = 2
	w.Unit(h).Group = 3
	s := &Session{Snapshot: frame.NewBuffer(), Units: w, LocalOwner: 0}
	s.publishSnapshot(1)
	cur := s.Snapshot.Current()
	if cur == nil || len(cur.Units) != 1 {
		t.Fatalf("published frame = %#v, want one unit", cur)
	}
	if cur.Units[0].MoverMode != 2 {
		t.Fatalf("published mover mode = %d, want 2 (airborne)", cur.Units[0].MoverMode)
	}
	// The health-bar pass draws '0'+Group beside a grouped unit's bar
	// [03 R-FX-01 §6].
	if cur.Units[0].Group != 3 {
		t.Fatalf("published group = %d, want 3", cur.Units[0].Group)
	}
}

// A frame published with no resolved builder must advertise no product keys:
// the reset at the publication boundary keeps the slice's capacity but not its
// length, so nothing from the previous frame survives into the committed one
// [I6].
func TestPublishBoundaryClearsCommandPageProductKeys(t *testing.T) {
	b := frame.NewBuffer()
	first := b.BeginWrite()
	first.CommandPage.Builder = 7
	first.CommandPage.ProductKeys = append(first.CommandPage.ProductKeys, "armsolar", "armmex")
	if err := b.Publish(1); err != nil {
		t.Fatalf("publish first: %v", err)
	}
	// Two BeginWrite calls: the buffer alternates slots, so the second one
	// returns the slot the first frame wrote.
	b.BeginWrite()
	if err := b.Publish(2); err != nil {
		t.Fatalf("publish second: %v", err)
	}
	third := b.BeginWrite()
	if len(third.CommandPage.ProductKeys) != 0 {
		t.Fatalf("reset product keys = %q, want none", third.CommandPage.ProductKeys)
	}
	if third.CommandPage.Builder != 0 {
		t.Fatalf("reset builder = %d, want 0", third.CommandPage.Builder)
	}
}

// The on/off aggregate starts at the not-applicable sentinel 3, takes the first
// onoffable unit's committed activation, and moves to the disagreement value
// when a later onoffable unit differs [07 §9][07 R-HUD-03 §6].
func TestPublishSnapshotFoldsDisagreeingOnOffSelection(t *testing.T) {
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "switchable"},
		MaxDamage:        1,
		OnOffable:        true,
	}
	w := newSessionFixtureWorld(4, nil)
	first, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create first unit: %v", err)
	}
	second, err := w.Create(def, 0, numeric.Fixed(2<<16), 0, 0)
	if err != nil {
		t.Fatalf("create second unit: %v", err)
	}
	// Selection membership is bit 0x10 of the unit status word [07 §9].
	w.Unit(first).Flags |= 0x10
	w.Unit(second).Flags |= 0x10
	w.Unit(first).Activated = true

	s := &Session{Snapshot: frame.NewBuffer(), Units: w, LocalOwner: 0}
	s.publishSnapshot(1)
	cur := s.Snapshot.Current()
	if cur == nil || cur.Selection.Count != 2 {
		t.Fatalf("published selection = %#v, want two selected units", cur)
	}
	if cur.CommandPage.OnOffState != 2 {
		t.Fatalf("disagreeing on/off aggregate = %d, want 2", cur.CommandPage.OnOffState)
	}

	// Agreement folds to the shared state, not to the disagreement value.
	w.Unit(second).Activated = true
	s.publishSnapshot(2)
	if got := s.Snapshot.Current().CommandPage.OnOffState; got != 1 {
		t.Fatalf("agreeing on/off aggregate = %d, want 1", got)
	}

	// No selected unit is onoffable: the sentinel that greys ONOFF survives.
	def.OnOffable = false
	s.publishSnapshot(3)
	if got := s.Snapshot.Current().CommandPage.OnOffState; got != 3 {
		t.Fatalf("not-applicable on/off aggregate = %d, want 3", got)
	}
}
