package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestFinishedIsFeatureCreationConvertsOnTheFirstSweep is the end-to-end half
// of [06 R-DMG-01 §12] site 1. A campaign mission places the dragon's teeth
// and the floating forts through the ordinary already-built creator; because
// their definitions carry `isfeature`, the creator stamps damage kind 7 and
// the death latch, and the ordinary phase-2 slot sweep converts each one into
// its authored corpse feature. Entry plus one sweep therefore leaves a
// blocking feature and no live unit — that is what those placements are in a
// played mission.
//
// It also locks the draw contract: cause 7 resolves with severity 0, variant
// 1 and no `Killed` query, so the conversion costs no simulation draw and
// cannot shift the stream for anything that follows [06 §12.1].
func TestFinishedIsFeatureCreationConvertsOnTheFirstSweep(t *testing.T) {
	s := newLoopTestSession(t, 0)
	const cell = 10
	teeth := &content.FeatureDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "fixtureteeth_dead"},
		Object:           "fixtureteeth_dead",
		FootprintX:       1,
		FootprintZ:       1,
		Damage:           100,
		Blocking:         true,
	}
	s.Catalog.Features[teeth.CanonicalKey] = teeth
	def := s.Catalog.Units["armcom"]
	def.IsFeature = true
	def.Corpse = "fixtureteeth_dead"

	liveBefore := s.Units.LiveCountForPlayer(0)
	h, err := s.Units.Create(def, 0, world.CellToWorld(cell), 0, world.CellToWorld(cell))
	if err != nil {
		t.Fatalf("place the isfeature unit: %v", err)
	}
	u := s.Units.Unit(h)
	if !u.Dying || combat.Cause(u.LastDamageCause) != combat.CauseFeatureConversion {
		t.Fatalf("entry left latch=%v kind=%d, want a latched cause-7 record [06 R-DMG-01 §12]", u.Dying, u.LastDamageCause)
	}

	// The creator's own draw is the common initializer's heading draw, which
	// every allocation takes and this arm does not change
	// [R-P28-ANG-01R §2]; the conversion itself is what must cost nothing.
	drawsBefore := s.SimRNG().Draws()
	s.finalizePhase2Death(h, 1)

	if s.Units.Unit(h) != nil {
		t.Fatal("one sweep left the placement as a live unit [06 §12.1]")
	}
	if got := s.Units.LiveCountForPlayer(0); got != liveBefore {
		t.Fatalf("live unit count = %d after the conversion, want the pre-placement %d", got, liveBefore)
	}
	inst := s.Features.InstanceAt(cell, cell)
	if inst == nil || inst.Def != teeth {
		t.Fatalf("no authored corpse feature at the placement cell: %+v", inst)
	}
	if !inst.Def.Blocking {
		t.Fatal("the conversion product must be the authored blocking feature")
	}
	if got := s.SimRNG().Draws(); got != drawsBefore {
		t.Fatalf("the conversion sweep spent %d simulation draws, want 0: cause 7 makes no Killed query and no explosion [06 §12.1]", got-drawsBefore)
	}
}
