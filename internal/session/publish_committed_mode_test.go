package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// The committed frame carries the unit-side mode mirror, not the mover's
// pending request. The two values legitimately disagree while takeoff or
// touchdown awaits its position commit [03 R-RAST-01 §7][04 R-MOV-01 §8].
func TestPublishSnapshotUsesCommittedMoverModeDuringFlightTransition(t *testing.T) {
	for _, tc := range []struct {
		name               string
		request, committed uint8
	}{
		{name: "blocked touchdown remains airborne", request: 1, committed: 2},
		{name: "pending takeoff remains grounded", request: 2, committed: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "flyer"}, MaxDamage: 1, BMCode: 1, CanFly: true}
			w := newSessionFixtureWorld(2, nil)
			h, err := w.Create(def, 0, 0, 0, 0)
			if err != nil {
				t.Fatalf("create unit: %v", err)
			}
			u := w.Unit(h)
			u.Move.Mode = tc.request
			u.Move.ModeMirror = tc.committed

			s := &Session{Snapshot: frame.NewBuffer(), Units: w, LocalOwner: 0}
			s.publishSnapshot(1)
			cur := s.Snapshot.Current()
			if cur == nil || len(cur.Units) != 1 {
				t.Fatalf("published frame = %#v, want one unit", cur)
			}
			if got := cur.Units[0].MoverMode; got != tc.committed {
				t.Fatalf("published mover mode = %d, want committed %d (request %d)", got, tc.committed, tc.request)
			}
		})
	}
}
