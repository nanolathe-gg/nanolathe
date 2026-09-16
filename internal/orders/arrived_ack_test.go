package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// statusSpy records every (kind, text) pair the handlers hand to the
// session-owned status emitter. The emitter's own producer gate lives in the
// session adapter [04 R-ORD-01 §1][03 R-AUD-01 §3]; what these tests lock is
// that the three rows that carry "status 6 (`Arrived`)" raise it, once, on the
// condition their row names.
type statusSpy struct {
	kinds []uint8
	texts []string
}

func (s *statusSpy) count(kind uint8) int {
	n := 0
	for _, k := range s.kinds {
		if k == kind {
			n++
		}
	}
	return n
}

// arrivedFixture is gateFixture plus a recording presentation adapter and the
// definition flags the air row reads.
func arrivedFixture(def *content.UnitDef) (*Queue, *units.Unit, *statusSpy) {
	rng.SeedGlobal(1, 0)
	if def == nil {
		def = &content.UnitDef{}
	}
	u := &units.Unit{
		Handle: 1,
		Alive:  true,
		Def:    def,
		X:      numeric.Fixed(70 << 16),
		Y:      numeric.Fixed(40 << 16),
		Z:      numeric.Fixed(90 << 16),
	}
	for idx := 0; idx < units.NumSlots; idx++ {
		u.SlotAt(idx).Weapon = &content.WeaponDef{Range: 180}
	}
	spy := &statusSpy{}
	q := &Queue{binding: &QueueBinding{
		SimRNG: rng.Global.Sim,
		Presentation: &PresentationAdapter{
			Ready: func() bool { return true },
			Status: func(_ *units.Unit, kind uint8, text string) bool {
				spy.kinds = append(spy.kinds, kind)
				spy.texts = append(spy.texts, text)
				return true
			},
		},
	}}
	BindQueue(u, q)
	return q, u, spy
}

// TestMoveGroundArrivalRaisesTheArrivedAcknowledgement locks the half of
// `Move_Ground` phase 1 that used to be a TODO: "satisfied `0x20` → status 6
// (`Arrived`), complete" [04 R-ORD-01 §4]. The record completed but nothing
// reached presentation, so the whole ground move family was silent while the
// two other kind-6 rows spoke.
//
// The acknowledgement is raised exactly once per arrival — the row completes
// on the same visit, so a second visit cannot repeat it.
func TestMoveGroundArrivalRaisesTheArrivedAcknowledgement(t *testing.T) {
	id := Lookup("Move_Ground")
	if id == 0 {
		t.Fatal("Move_Ground descriptor unavailable")
	}
	q, u, spy := arrivedFixture(nil)
	q.Push(id, Node{Owner: u.Handle, GoalX: u.X, GoalZ: u.Z, GoalSupplied: true})
	q.Pump(u, 40)

	n := q.Primary()[0]
	if n.Phase != 1 || n.DynamicGate&0x20 == 0 {
		t.Fatalf("phase = %d gate = %#x, want phase 1 waiting on the arrival bit [04 R-ORD-01 §4]", n.Phase, n.DynamicGate)
	}
	if spy.count(statusArrived) != 0 {
		t.Fatalf("kind 6 raised %d times before arrival, want 0", spy.count(statusArrived))
	}

	n.Satisfied |= 0x20 // the follower observes arrival [04 R-ORD-01 §0]
	q.pumpPrimary(u, 41)

	if q.LenPrimary() != 0 {
		t.Fatalf("primary length = %d, want the arrived move completed [04 R-ORD-01 §4]", q.LenPrimary())
	}
	if got := spy.count(statusArrived); got != 1 {
		t.Fatalf("kind 6 raised %d times on arrival, want exactly 1 [04 R-ORD-01 §4]", got)
	}
	q.pumpPrimary(u, 42)
	if got := spy.count(statusArrived); got != 1 {
		t.Fatalf("kind 6 raised %d times after the record completed, want 1", got)
	}
}

// TestMoveGroundWithoutArrivalRaisesNoAcknowledgement is the negative half:
// the row's "else *re-arm* (9)" arm speaks nothing. A cue on the re-arm would
// repeat every 30..59 ticks for the life of an unreachable move.
func TestMoveGroundWithoutArrivalRaisesNoAcknowledgement(t *testing.T) {
	id := Lookup("Move_Ground")
	if id == 0 {
		t.Fatal("Move_Ground descriptor unavailable")
	}
	q, u, spy := arrivedFixture(nil)
	q.Push(id, Node{Owner: u.Handle, GoalX: u.X, GoalZ: u.Z, GoalSupplied: true})
	q.Pump(u, 40)
	n := q.Primary()[0]
	n.Satisfied |= 0x40 // the no-route bit, not arrival [04 R-ORD-01 §0]
	q.pumpPrimary(u, 41)
	if got := spy.count(statusArrived); got != 0 {
		t.Fatalf("kind 6 raised %d times without the arrival bit, want 0 [04 R-ORD-01 §4]", got)
	}
}

// TestKamikazeArrivalRaisesTheArrivedAcknowledgement locks the caption half of
// `Attack_Kamikaze` phase 1: "satisfied `0x20` → status 6 (`Arrived`), spawn
// SelfDestruct ... complete" [04 R-ORD-01 §3]. The detonation half is locked by
// TestKamikazeArrivalSpawnsTheImmediateSelfDestruct; this is the cue.
func TestKamikazeArrivalRaisesTheArrivedAcknowledgement(t *testing.T) {
	id := Lookup("Attack_Kamikaze")
	if id == 0 {
		t.Fatal("Attack_Kamikaze descriptor unavailable")
	}
	q, u, spy := arrivedFixture(nil)
	q.Push(id, Node{Owner: u.Handle, GoalX: u.X, GoalZ: u.Z, GoalSupplied: true})
	q.Pump(u, 40)
	n := q.Primary()[0]
	if spy.count(statusArrived) != 0 {
		t.Fatal("kind 6 raised before the arrival bit")
	}
	n.Satisfied |= 0x20
	q.pumpPrimary(u, 41)
	if got := spy.count(statusArrived); got != 1 {
		t.Fatalf("kind 6 raised %d times on kamikaze arrival, want exactly 1 [04 R-ORD-01 §3]", got)
	}
}

// TestVTOLMoveLastOnSegmentRaisesTheArrivedAcknowledgement locks `VTOL_Move`
// phase 2: "when this record has no successor, status 6 (`Arrived`); complete"
// [04 R-ORD-02 §2]. A queued air move mid-chain says nothing; only the last leg
// acknowledges, which is why the successor test is part of the contract.
func TestVTOLMoveLastOnSegmentRaisesTheArrivedAcknowledgement(t *testing.T) {
	id := Lookup("VTOL_Move")
	if id == 0 {
		t.Fatal("VTOL_Move descriptor unavailable")
	}
	for _, tc := range []struct {
		name       string
		successors int
		want       int
	}{
		{"last on segment acknowledges", 0, 1},
		{"a queued successor stays silent", 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, u, spy := arrivedFixture(&content.UnitDef{BMCode: 1, CanFly: true})
			q.Push(id, Node{Owner: u.Handle, Phase: 2, GoalX: u.X, GoalZ: u.Z, GoalSupplied: true})
			for i := 0; i < tc.successors; i++ {
				q.Push(id, Node{Owner: u.Handle, GoalX: u.X, GoalZ: u.Z, GoalSupplied: true})
			}
			n := q.Primary()[0]
			n.Phase, n.DynamicGate, n.Deadline = 2, 0, -1
			q.pumpPrimary(u, 41)
			if got := spy.count(statusArrived); got != tc.want {
				t.Fatalf("kind 6 raised %d times, want %d [04 R-ORD-02 §2]", got, tc.want)
			}
		})
	}
}

// TestArrivedAcknowledgementCarriesTheRowsText locks the text the three rows
// pass. Kind 6's default display text is `Arrived` [04 R-ORD-01 §1]; the rows
// name it explicitly, so a caption reaches the message ring even when the
// unit's authored sound category has no `arrived` variant.
func TestArrivedAcknowledgementCarriesTheRowsText(t *testing.T) {
	id := Lookup("Move_Ground")
	if id == 0 {
		t.Fatal("Move_Ground descriptor unavailable")
	}
	q, u, spy := arrivedFixture(nil)
	q.Push(id, Node{Owner: u.Handle, GoalX: u.X, GoalZ: u.Z, GoalSupplied: true})
	q.Pump(u, 40)
	q.Primary()[0].Satisfied |= 0x20
	q.pumpPrimary(u, 41)
	for i, k := range spy.kinds {
		if k == statusArrived && spy.texts[i] != "Arrived" {
			t.Fatalf("kind 6 text = %q, want %q [04 R-ORD-01 §1]", spy.texts[i], "Arrived")
		}
	}
}
