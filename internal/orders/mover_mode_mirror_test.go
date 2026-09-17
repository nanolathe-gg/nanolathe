package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The three order-side rows below name the COMMITTED mover mode. The committed
// mode is the unit flags word's mode mirror, which the position commit
// publishes; `Move.Mode` is the request byte the mover-mode setter writes the
// instant it is called [04 R-AIR-01 §3][04 R-COLL-01 §1]. The two words differ
// for every tick between a takeoff or landing request and its commit, and a
// restored save can carry them apart by design [08 R-SAVE-02 §6, §8] — which is
// exactly the state each fixture here is put in, one word each way, so a row
// that read the wrong one cannot pass both directions.

// TestRepairUnitReadsTheCommittedMoverModeNotTheRequest pins the pre-switch test
// of [04 R-ORD-01 §12]: "The movement-mode mirror is bits 0-1, tested separately
// before the phase switch (`!= 1` -> `Repairs unsuccessful.`)".
func TestRepairUnitReadsTheCommittedMoverModeNotTheRequest(t *testing.T) {
	// A fixed slice, not a map: the rows run in one order (I1).
	for _, tc := range []struct {
		name    string
		request uint8
		mirror  uint8
		want    Code
	}{
		{"takeoff requested, still committed grounded", 2, 1, 1},
		{"landing requested, still committed airborne", 1, 2, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, builder, target := workFixture()
			target.Health = 50 // damaged and finished: the row has work to do
			var captions []string
			q.binding.Presentation = &PresentationAdapter{
				Status: func(_ *units.Unit, _ uint8, text string) bool {
					captions = append(captions, text)
					return true
				},
			}
			target.Move.Mode, target.Move.ModeMirror = tc.request, tc.mirror
			n := &Node{ID: Lookup("RepairUnit"), Owner: builder.Handle, Target: target.Handle, Deadline: -1}

			got := repairUnitHandler(builder, n, 0, 1)

			if got != tc.want {
				t.Fatalf("request %d mirror %d returned %d, want %d; the row tests the mirror [04 R-ORD-01 §12]",
					tc.request, tc.mirror, got, tc.want)
			}
			refused := false
			for _, c := range captions {
				if c == "Repairs unsuccessful." {
					refused = true
				}
			}
			if refused != (tc.want == 5) {
				t.Fatalf("request %d mirror %d emitted the refusal caption = %v, want %v [04 R-ORD-01 §12]",
					tc.request, tc.mirror, refused, tc.want == 5)
			}
		})
	}
}

// TestStandbyMineReadsTheCommittedMoverModeNotTheRequest pins `Standby_Mine`'s
// detonation test: "phase 1 requires the scanned target's committed mover mode
// to be **grounded** (`1`) and my own fire stance nonzero, and then spawns
// `SelfDestruct` with p1 = 1 (immediate) at the head and completes"
// [04 R-ORD-01 §3]. The same test appears in [04 R-STANCE-01 §3] as "a target
// whose state-word low two bits equal `1`", and [04 R-ORD-01 §12] says which
// field those bits are: "The movement-mode mirror is bits 0-1."
func TestStandbyMineReadsTheCommittedMoverModeNotTheRequest(t *testing.T) {
	// A fixed slice, not a map: the rows run in one order (I1).
	for _, tc := range []struct {
		name     string
		request  uint8
		mirror   uint8
		detonate bool
	}{
		{"target's takeoff requested, still committed grounded", 2, 1, true},
		{"target's landing requested, still committed airborne", 1, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, mine := standingFixture(&content.UnitDef{SightDistance: 500})
			mine.Flags |= units.BuildingClassStatus
			mine.Flags = (mine.Flags &^ (stanceFieldMask << stanceFireShift)) | 2<<stanceFireShift // fire at will: the scan searches
			victim := &units.Unit{Handle: 2, Def: &content.UnitDef{MaxDamage: 100}, Alive: true, Health: 100}
			victim.Move.Mode, victim.Move.ModeMirror = tc.request, tc.mirror
			q.binding.Weapons = &WeaponAdapter{
				Acquire: func(*units.Unit, int, uint32) (pool.Handle, bool) { return victim.Handle, true },
			}
			q.binding.Lookup = func(h pool.Handle) *units.Unit {
				if h == victim.Handle {
					return victim
				}
				return nil
			}
			n := &Node{ID: Lookup("Standby_Mine"), Owner: mine.Handle, Phase: 1, Deadline: -1}

			got := standbyMineHandler(mine, n, 0, 100)

			if tc.detonate {
				if got != 5 {
					t.Fatalf("request %d mirror %d returned %d, want 5 (*complete*) after the detonation [04 R-ORD-01 §3]",
						tc.request, tc.mirror, got)
				}
				// `SelfDestruct` carries the rear-segment flag, so the head
				// insert lands on the secondary segment [04 R-ORD-01 §1][§2].
				if q.LenSecondary() != 1 {
					t.Fatalf("request %d mirror %d spawned %d rear-segment records, want the immediate SelfDestruct [04 R-ORD-01 §3]",
						tc.request, tc.mirror, q.LenSecondary())
				}
				if head := q.Secondary()[0]; head.ID != Lookup("SelfDestruct") || head.Param1 != 1 {
					t.Fatalf("request %d mirror %d spawned %q with p1 = %d, want SelfDestruct with p1 = 1 [04 R-ORD-01 §3]",
						tc.request, tc.mirror, DescriptorFor(head.ID).Name, head.Param1)
				}
				return
			}
			if got != 2 {
				t.Fatalf("request %d mirror %d returned %d, want 2 (*hold*): the committed mode is airborne, so the mine does not fire [04 R-ORD-01 §3]",
					tc.request, tc.mirror, got)
			}
			if q.LenPrimary() != 0 || q.LenSecondary() != 0 {
				t.Fatalf("request %d mirror %d spawned %d/%d records; the mine reads the committed mirror, not the request byte",
					tc.request, tc.mirror, q.LenPrimary(), q.LenSecondary())
			}
		})
	}
}

// TestVTOLMovePhaseZeroFallbackReadsTheCommittedMoverMode pins the marker-less
// fallback in `VTOL_Move` phase 0 against the preamble step it stands in for:
// "**Only if** the committed mover mode is `1` (grounded): call the mover-mode
// setter with mode `2`; build a fresh point path marker ... OR `0xE0` into the
// record's dynamic gate word" [04 R-AIR-01 §6]. The fallback exists only
// because an isolated order fixture has no marker service, so it must apply the
// preamble's own condition rather than a different one.
func TestVTOLMovePhaseZeroFallbackReadsTheCommittedMoverMode(t *testing.T) {
	// A fixed slice, not a map: the rows run in one order (I1).
	for _, tc := range []struct {
		name    string
		request uint8
		mirror  uint8
		gated   bool
	}{
		{"takeoff requested, still committed grounded", 2, 1, true},
		{"landing requested, still committed airborne", 1, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, u := standingFixture(&content.UnitDef{BMCode: 1, CanFly: true})
			u.Move.Mode, u.Move.ModeMirror = tc.request, tc.mirror
			n := &Node{ID: Lookup("VTOL_Move"), Owner: u.Handle, Phase: 0, Deadline: -1}

			if got := vtolMoveHandler(u, n, 0, 100); got != 1 {
				t.Fatalf("request %d mirror %d returned %d, want 1 (*advance*): the preamble advances either way [04 R-AIR-01 §6]",
					tc.request, tc.mirror, got)
			}
			if gated := n.DynamicGate&gateMoveOutcomes != 0; gated != tc.gated {
				t.Fatalf("request %d mirror %d armed the 0xE0 wait = %v, want %v; the step is gated on the COMMITTED mode [04 R-AIR-01 §6]",
					tc.request, tc.mirror, gated, tc.gated)
			}
		})
	}
}
