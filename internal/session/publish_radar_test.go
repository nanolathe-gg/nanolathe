package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestPublishSnapshotRadarSensorLookupSurvivesASkippedUnit locks review
// finding R05's O(1) replacement for the whole-slice sensor input scan: the
// lookup must still resolve each live unit's OWN sensor record by pool
// handle, not by its ordinal position in the sensor pass's output, once a
// unit between two others in that output has died and no longer appears in
// s.Units.Iter(). A position-keyed (rather than handle-keyed) lookup would
// hand the wrong record to the unit that now sits at the freed unit's old
// ordinal [03 §3.9].
func TestPublishSnapshotRadarSensorLookupSurvivesASkippedUnit(t *testing.T) {
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "contact"}, MaxDamage: 1}
	w := newSessionFixtureWorld(4, nil)
	hA, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create A: %v", err)
	}
	hB, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create B: %v", err)
	}
	hC, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create C: %v", err)
	}

	vis := visibility.New(&world.Terrain{CellW: 64, CellH: 64}, visibility.ModeHistoryEnabled|visibility.ModeCurrentEnabled)
	vis.SetLocal(0)

	var statusA, statusB, statusC uint32
	units := []visibility.SensorUnit{
		{ID: uint16(hA), Owner: 0, Status: &statusA, Alive: true, Hidden: false, Active: true, OnOffable: false},
		{ID: uint16(hB), Owner: 0, Status: &statusB, Alive: true, Hidden: true, Active: false, OnOffable: true},
		{ID: uint16(hC), Owner: 1, Status: &statusC, Alive: true, Hidden: false, Active: false, OnOffable: true},
	}
	// SensorTick's gate requires at least two players; the pass output is
	// what publishSnapshot's lookup must match by ID [R-VIS-01 §4] "Gate".
	vis.SensorTick(0, 2, units)
	inputs := vis.SensorInputs()
	if len(inputs) != 3 {
		t.Fatalf("sensor pass output = %d records, want 3", len(inputs))
	}
	// Capture the pass's own output as ground truth, keyed by handle, so the
	// assertions below do not depend on guessing the friendly-pass arithmetic.
	want := map[uint16]visibility.SensorInput{}
	for _, in := range inputs {
		want[in.ID] = in
	}

	// B dies after the sensor pass ran but before publication. Its record
	// stays in the middle of the captured sensor output, so C's live-unit
	// ordinal (1, after B drops out of Iter()) no longer matches its ordinal
	// in that output (2).
	w.Unit(hB).Alive = false

	s := &Session{Units: w, Vis: vis, Snapshot: frame.NewBuffer(), LocalOwner: 0}
	s.publishSnapshot(1)
	cur := s.Snapshot.Current()
	if cur == nil || len(cur.Radar.Contacts) != 2 {
		t.Fatalf("published contacts = %#v, want exactly A and C", cur)
	}
	vis.SetMode(visibility.ModeTerrainRay)
	if cur.Radar.MappingLOS != 3 {
		t.Fatal("radar mask did not retain the committed history/current bits")
	}
	byHandle := map[uint16]frame.RadarContactView{}
	for _, c := range cur.Radar.Contacts {
		byHandle[uint16(c.Handle)] = c
	}
	if _, ok := byHandle[uint16(hB)]; ok {
		t.Fatal("dead unit B still published a contact")
	}
	for _, h := range [2]uint16{uint16(hA), uint16(hC)} {
		contact, ok := byHandle[h]
		if !ok {
			t.Fatalf("no published contact for handle %d", h)
		}
		wantRecord := want[h]
		if contact.Status != wantRecord.Status || contact.Hidden != wantRecord.Hidden || contact.Active != wantRecord.Active || contact.OnOffable != wantRecord.OnOffable {
			t.Fatalf("contact for handle %d = %+v, want it to carry sensor record %+v", h, contact, wantRecord)
		}
	}
}

// TestPublishSnapshotRadarRingsShrinkWhenReusedSlotHadMore locks the ring
// reuse the R05 repair introduces: a contact slot's ring backing array from a
// previous tick is handed back with its length truncated to zero, not its
// old contents. A unit publishing fewer rings this tick than the slot's
// former occupant must not leak the extra stale entries past the new,
// shorter length.
func TestPublishSnapshotRadarRingsShrinkWhenReusedSlotHadMore(t *testing.T) {
	threeRings := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "three-rings"},
		MaxDamage:        1, CanGuard: true,
		Weapon1Def: &content.WeaponDef{ID: 1, Range: 100},
		Weapon2Def: &content.WeaponDef{ID: 2, Range: 200},
		Weapon3Def: &content.WeaponDef{ID: 3, Range: 300},
	}
	oneRing := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "one-ring"},
		MaxDamage:        1, CanGuard: true,
		Weapon1Def: &content.WeaponDef{ID: 4, Range: 42},
	}
	w := newSessionFixtureWorld(4, nil)
	h, err := w.Create(threeRings, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create three-ring unit: %v", err)
	}
	s := &Session{Units: w, Snapshot: frame.NewBuffer(), LocalOwner: 0}
	s.publishSnapshot(1)
	first := s.Snapshot.Current()
	if first == nil || len(first.Radar.Contacts) != 1 || len(first.Radar.Contacts[0].Rings) != 3 {
		t.Fatalf("first publication contacts = %#v, want one contact with 3 rings", first)
	}

	// Replace the unit's definition (in place, same pool slot) with one
	// authoring a single weapon slot, so the next publication's contact
	// occupies the SAME frame slot the three-ring contact used, and the
	// reused ring backing array actually had 3 old entries behind it.
	w.Unit(h).Def = oneRing
	for slot := 1; slot < 3; slot++ {
		w.Unit(h).SlotAt(slot).Weapon = nil
	}
	w.Unit(h).SlotAt(0).Weapon = oneRing.Weapon1Def

	s.publishSnapshot(2)
	second := s.Snapshot.Current()
	if second == nil || len(second.Radar.Contacts) != 1 {
		t.Fatalf("second publication contacts = %#v, want one contact", second)
	}
	rings := second.Radar.Contacts[0].Rings
	if len(rings) != 1 {
		t.Fatalf("second publication rings = %#v, want exactly the one authored ring", rings)
	}
	if rings[0].Range != 42 {
		t.Fatalf("second publication ring range = %d, want 42 (stale entry leaked past the new length)", rings[0].Range)
	}
}

// TestPublishSnapshotRadarContactsImmutableAfterNextPublish locks the
// documented publication boundary [03 §2.4][I6]: presentation samples one
// committed frame at a time, and a later publication (which writes into the
// OTHER slot of the double buffer) must never reach back and mutate a frame
// a reader may still be holding. This is the safety property the R05 ring
// reuse leans on — reuse is scoped to a slot's own next occupant, never
// shared across the two committed slots.
func TestPublishSnapshotRadarContactsImmutableAfterNextPublish(t *testing.T) {
	armed := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "armed"},
		MaxDamage:        1, CanGuard: true,
		Weapon1Def: &content.WeaponDef{ID: 1, Range: 111},
	}
	w := newSessionFixtureWorld(4, nil)
	if _, err := w.Create(armed, 0, 0, 0, 0); err != nil {
		t.Fatalf("create unit: %v", err)
	}
	s := &Session{Units: w, Snapshot: frame.NewBuffer(), LocalOwner: 0}
	s.publishSnapshot(1)
	first := s.Snapshot.Current()
	if first == nil || len(first.Radar.Contacts) != 1 || len(first.Radar.Contacts[0].Rings) != 1 {
		t.Fatalf("first publication = %#v, want one contact with one ring", first)
	}
	wantHandle := first.Radar.Contacts[0].Handle
	wantRange := first.Radar.Contacts[0].Rings[0].Range

	// A second, unrelated unit publishes into the other slot.
	other := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "other"}, MaxDamage: 1}
	if _, err := w.Create(other, 0, 0, 0, 0); err != nil {
		t.Fatalf("create second unit: %v", err)
	}
	s.publishSnapshot(2)
	second := s.Snapshot.Current()
	if second == nil || len(second.Radar.Contacts) != 2 {
		t.Fatalf("second publication contacts = %#v, want two", second)
	}

	// The frame pointer returned by the FIRST Current() call is a pointer
	// into the buffer's other slot; it must read exactly as it did when
	// captured, independent of everything the second publication wrote.
	if len(first.Radar.Contacts) != 1 {
		t.Fatalf("earlier committed frame's contact count changed to %d after a later publish", len(first.Radar.Contacts))
	}
	if first.Radar.Contacts[0].Handle != wantHandle {
		t.Fatalf("earlier committed frame's contact handle changed to %v after a later publish", first.Radar.Contacts[0].Handle)
	}
	if len(first.Radar.Contacts[0].Rings) != 1 || first.Radar.Contacts[0].Rings[0].Range != wantRange {
		t.Fatalf("earlier committed frame's rings changed to %#v after a later publish", first.Radar.Contacts[0].Rings)
	}
}
