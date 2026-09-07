package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
)

// publishedPlayerRows publishes one snapshot and returns the committed rows.
func publishedPlayerRows(t *testing.T, s *Session, tick uint32) [frame.PlayerRowSlots]frame.PlayerRow {
	t.Helper()
	s.publishSnapshot(tick)
	cur := s.Snapshot.Current()
	if cur == nil {
		t.Fatalf("no committed frame at tick %d", tick)
	}
	return cur.Players
}

// The rows carry the row filter's terms for every one of the ten slots, on
// every tick of a live battle — not only once a result latches
// [07 R-HUD-04 §1].
func TestPlayerRowsPublishTheRowFilterTermsEveryTick(t *testing.T) {
	s := newLoopTestSession(t, 2)
	// The helper builds the two player records directly rather than through a
	// registration path, so the rank half of registration is applied here
	// [08 R-SKIR-01 §2].
	for i := 0; i < 2; i++ {
		s.Econ.Players[i].SeedScorePanelRank(i)
	}
	for tick := uint32(1); tick <= 3; tick++ {
		rows := publishedPlayerRows(t, s, tick)
		if !rows[0].Present || !rows[1].Present {
			t.Fatalf("tick %d: occupied slots not published as present: %+v", tick, rows[:2])
		}
		// newLoopTestSession gives slot 0 controller 1 and slot 1 controller 2.
		if rows[0].Controller != 1 || rows[1].Controller != 2 {
			t.Fatalf("tick %d: controller bytes = %d/%d, want 1/2", tick, rows[0].Controller, rows[1].Controller)
		}
		if rows[0].LiveUnits != 1 || rows[1].LiveUnits != 1 {
			t.Fatalf("tick %d: live-unit counts = %d/%d, want 1/1", tick, rows[0].LiveUnits, rows[1].LiveUnits)
		}
		for i := 2; i < frame.PlayerRowSlots; i++ {
			// An unoccupied slot publishes its zero row. It was never
			// registered, so its rank byte is the zero its record was allocated
			// with, not the slot index [08 R-SKIR-01 §2]; the candidate scan's
			// present test is what keeps that zero out of the ranking
			// [06 §12.1 R-WPN-02 §9].
			if rows[i] != (frame.PlayerRow{}) {
				t.Fatalf("tick %d: unoccupied slot %d published %+v, want the zero row", tick, i, rows[i])
			}
		}
		// The published byte is the record's, and no kill has been credited, so
		// it is still the registration value [08 R-SKIR-01 §2][07 R-HUD-04 §1].
		if rows[0].Rank != 0 || rows[1].Rank != 1 {
			t.Fatalf("tick %d: rank bytes = %d/%d, want the registration values 0/1", tick, rows[0].Rank, rows[1].Rank)
		}
	}
}

// A death credited through the finalizer moves the crediting slot's kill
// counter and the victim slot's loss counter, and the rows carry both on the
// next publication [06 §12.1][08 R-CAMP-01 §7][07 R-HUD-04 §1].
func TestPlayerRowsTrackKillsAndLossesAcrossAScriptedKill(t *testing.T) {
	s := newLoopTestSession(t, 2)
	before := publishedPlayerRows(t, s, 1)
	if before[0].Kills != 0 || before[1].Losses != 0 {
		t.Fatalf("counters were not zero before the kill: %+v", before[:2])
	}

	victim := s.Units.IterSliced()[1]
	if victim.Owner != 1 {
		t.Fatalf("fixture victim owner = %d, want 1", victim.Owner)
	}
	// A packet from slot 0 with an ordinary cause is the full credit path.
	victim.LastDamageCause = uint8(combat.CauseOrdinary)
	victim.LastDamageSide = 0
	s.Units.Destroy(pool.Handle(victim.Handle), units.DeathKilled)
	s.stepUnitPhase(2)

	after := publishedPlayerRows(t, s, 2)
	if after[0].Kills != 1 {
		t.Fatalf("crediting slot kills = %d, want 1", after[0].Kills)
	}
	if after[1].Losses != 1 {
		t.Fatalf("victim slot losses = %d, want 1", after[1].Losses)
	}
	if after[1].LiveUnits != 0 {
		t.Fatalf("victim slot live-unit count = %d, want 0", after[1].LiveUnits)
	}
	// The rows are a copy of the authoritative counters, never a rescan.
	for i := 0; i < frame.PlayerRowSlots; i++ {
		p := s.Econ.Players[i]
		if after[i].Kills != int(p.Kills) || after[i].Losses != int(p.Losses) ||
			after[i].CommandersKilled != int(p.CommanderKills) || after[i].CommandersLost != int(p.CommanderLosses) {
			t.Fatalf("slot %d row counters %+v do not match the player record %d/%d/%d/%d",
				i, after[i], p.Kills, p.Losses, p.CommanderKills, p.CommanderLosses)
		}
	}
}

// Two identical runs publish identical rows: the publication reads only
// authoritative state and iterates slots 0..9 ascending [I1][I6].
func TestPlayerRowsAreStableAcrossTwoIdenticalRuns(t *testing.T) {
	run := func() [frame.PlayerRowSlots]frame.PlayerRow {
		s := newLoopTestSession(t, 2)
		var rows [frame.PlayerRowSlots]frame.PlayerRow
		for tick := uint32(1); tick <= 4; tick++ {
			rows = publishedPlayerRows(t, s, tick)
		}
		return rows
	}
	a, b := run(), run()
	if a != b {
		t.Fatalf("identical runs published different rows:\n a=%+v\n b=%+v", a, b)
	}
}

// This checks publication against the partial fingerprint and both RNG draw
// counts; the fingerprint does not cover every simulation owner [I4][I6].
func TestPlayerRowPublicationPreservesPartialFingerprintAndDrawCounts(t *testing.T) {
	s := newLoopTestSession(t, 2)
	s.publishSnapshot(1)
	hashBefore, err := s.PartialStateFingerprint()
	if err != nil {
		t.Fatalf("PartialStateFingerprint: %v", err)
	}
	simBefore, crtBefore := s.SimRNG().Draws(), s.CrtRNG().Draws()
	published := s.Snapshot.BeginWrite()
	publishPlayerRows(s, published)
	publishPlayerRows(s, published)
	hashAfter, err := s.PartialStateFingerprint()
	if err != nil {
		t.Fatalf("PartialStateFingerprint: %v", err)
	}
	if hashAfter != hashBefore {
		t.Fatalf("partial fingerprint changed across the publication: %s -> %s", hashBefore, hashAfter)
	}
	if s.SimRNG().Draws() != simBefore || s.CrtRNG().Draws() != crtBefore {
		t.Fatalf("draw counts changed: sim %d->%d, crt %d->%d",
			simBefore, s.SimRNG().Draws(), crtBefore, s.CrtRNG().Draws())
	}
}
