package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// seedPool fills the pool with one record per spec in order, tagging each with a
// WeaponID so the survivors' relative order can be read back after compaction.
type anchorSpec struct {
	tag       int32
	shooter   pool.Handle
	remaining int32
}

func seedPool(t *testing.T, s *Service, specs ...anchorSpec) {
	t.Helper()
	for _, spec := range specs {
		h, ok := s.Reserve()
		if !ok {
			t.Fatalf("reserve for tag %d failed", spec.tag)
		}
		p := &s.Records[int(h)-1]
		p.WeaponID = spec.tag
		p.Shooter = spec.shooter
		p.BurstRemaining = spec.remaining
	}
}

func poolTags(s *Service) []int32 {
	tags := make([]int32, 0, s.Count())
	for i := 0; i < s.Count(); i++ {
		tags = append(tags, s.Records[i].WeaponID)
	}
	return tags
}

func sameTags(a, b []int32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestAnchorSweepKillsTheDyingShootersAnchorsOnly locks the match test of the
// unit-death burst-anchor sweep [06 §4.3] [06 §5.2]: a record matches when its
// remaining burst count is nonzero AND its shooter reference is the dying unit.
// It is not a general removal of that unit's projectiles — pellets already in
// flight (remaining count zero) survive — and another shooter's anchor is left
// alone. Compaction preserves the survivors' relative order.
func TestAnchorSweepKillsTheDyingShootersAnchorsOnly(t *testing.T) {
	var s Service
	const victim, bystander pool.Handle = 4, 9
	seedPool(t, &s,
		anchorSpec{tag: 1, shooter: bystander, remaining: 3}, // another shooter's anchor
		anchorSpec{tag: 2, shooter: victim, remaining: 2},    // the victim's anchor
		anchorSpec{tag: 3, shooter: victim, remaining: 0},    // a pellet already in flight
		anchorSpec{tag: 4, shooter: bystander, remaining: 0}, // an unrelated pellet
	)

	if killed := s.SweepBurstAnchorsForShooter(victim); killed != 1 {
		t.Fatalf("killed %d anchors, want 1", killed)
	}
	if got, want := poolTags(&s), []int32{1, 3, 4}; !sameTags(got, want) {
		t.Fatalf("survivors %v, want %v — only the victim's anchor goes, in stable order [06 §5.2]", got, want)
	}
	if s.Count() != 3 {
		t.Fatalf("count %d after one compaction, want 3", s.Count())
	}
}

// TestAnchorSweepCompactsOncePerAnchorFound locks the walk of [06 §4.3]: the
// compaction pass runs INSIDE the loop, so a unit dying with two anchors alive
// compacts twice, and the scan then ADVANCES rather than revisiting the record
// moved into the vacated slot [06 §5.2].
//
// With the two anchors separated by one survivor both are found. Tags 1 and 4
// survive in order.
func TestAnchorSweepCompactsOncePerAnchorFound(t *testing.T) {
	var s Service
	const victim pool.Handle = 4
	seedPool(t, &s,
		anchorSpec{tag: 1, shooter: 9, remaining: 0},
		anchorSpec{tag: 2, shooter: victim, remaining: 5}, // anchor A at index 1
		anchorSpec{tag: 3, shooter: 9, remaining: 0},      // shifts into index 1
		anchorSpec{tag: 4, shooter: victim, remaining: 5}, // anchor B, found after the advance
	)

	if killed := s.SweepBurstAnchorsForShooter(victim); killed != 2 {
		t.Fatalf("killed %d anchors, want 2 — the walk continues over the moved records [06 §4.3]", killed)
	}
	if got, want := poolTags(&s), []int32{1, 3}; !sameTags(got, want) {
		t.Fatalf("survivors %v, want %v", got, want)
	}
}

// TestAnchorSweepSkipsTheRecordMovedIntoTheVacatedSlot locks the one place the
// walk is deliberately lossy [06 §5.2]: "the scan then advances rather than
// revisiting the record moved into the vacated slot", so two ADJACENT matching
// anchors leave the second one alive. [06 §5.2] files whether ordinary firing
// can reach that adjacency as a Supported inference; the skip itself is the
// Established behavior and is reproduced rather than tidied away.
func TestAnchorSweepSkipsTheRecordMovedIntoTheVacatedSlot(t *testing.T) {
	var s Service
	const victim pool.Handle = 4
	seedPool(t, &s,
		anchorSpec{tag: 1, shooter: victim, remaining: 5}, // killed at index 0
		anchorSpec{tag: 2, shooter: victim, remaining: 5}, // shifts into index 0, skipped
		anchorSpec{tag: 3, shooter: 9, remaining: 0},
	)

	if killed := s.SweepBurstAnchorsForShooter(victim); killed != 1 {
		t.Fatalf("killed %d, want 1 — the adjacent second anchor is skipped by the advance [06 §5.2]", killed)
	}
	if got, want := poolTags(&s), []int32{2, 3}; !sameTags(got, want) {
		t.Fatalf("survivors %v, want %v", got, want)
	}
	if s.Records[0].BurstRemaining != 5 {
		t.Fatal("the skipped anchor must still be a live scheduler")
	}
}

// TestAnchorSweepStopsThePendingClones is the behavioral statement of the
// sweep: an anchor killed mid-burst emits no further pellets, because the
// scheduler that would have emitted them is gone before the next projectile
// phase [06 §4.3]. A burst whose shooter is still alive is untouched.
func TestAnchorSweepStopsThePendingClones(t *testing.T) {
	weapon := &content.WeaponDef{ID: 77, Burst: 4, BurstRate: 1, WeaponVelocity: 100 * 65536 / 30}
	weapons := map[int32]*content.WeaponDef{77: weapon}

	run := func(t *testing.T, killShooter bool) int {
		t.Helper()
		var s Service
		const shooter pool.Handle = 4
		seedPool(t, &s, anchorSpec{tag: 77, shooter: shooter, remaining: 4})
		s.Records[0].BurstDeadline = 0

		if killShooter {
			// The death teardown's sweep, at tick 0, before any pellet is due.
			if killed := s.SweepBurstAnchorsForShooter(shooter); killed != 1 {
				t.Fatalf("sweep killed %d anchors, want 1", killed)
			}
		}
		r := rng.NewSimulation(3)
		clones := 0
		for tick := uint32(1); tick <= 8; tick++ {
			clones += s.AdvanceBursts(tick, &r, weapons, nil)
		}
		return clones
	}

	if got := run(t, false); got != 4 {
		t.Fatalf("a live shooter's burst emitted %d pellets, want its authored 4 [06 §4.3]", got)
	}
	if got := run(t, true); got != 0 {
		t.Fatalf("a swept anchor emitted %d pellets, want 0 [06 §4.3]", got)
	}
}

// TestAnchorSweepIgnoresTheDeadBitAndTheNullShooter locks the two edges of the
// match test [06 §5.2]: the dead bit is not consulted, so an anchor retired
// this tick and not yet compacted away still matches; and a null shooter
// handle — pool slot 0 is the null slot [I5] — matches nothing at all.
func TestAnchorSweepIgnoresTheDeadBitAndTheNullShooter(t *testing.T) {
	var s Service
	const victim pool.Handle = 4
	seedPool(t, &s,
		anchorSpec{tag: 1, shooter: victim, remaining: 2},
		anchorSpec{tag: 2, shooter: 0, remaining: 2}, // no shooter reference
	)
	s.MarkDead(pool.Handle(1)) // already retired, not yet compacted

	if killed := s.SweepBurstAnchorsForShooter(victim); killed != 1 {
		t.Fatalf("killed %d, want 1 — the match test does not consult the dead bit [06 §5.2]", killed)
	}
	if got, want := poolTags(&s), []int32{2}; !sameTags(got, want) {
		t.Fatalf("survivors %v, want %v", got, want)
	}
	if killed := s.SweepBurstAnchorsForShooter(0); killed != 0 {
		t.Fatalf("a null shooter handle matched %d records, want 0", killed)
	}
}
