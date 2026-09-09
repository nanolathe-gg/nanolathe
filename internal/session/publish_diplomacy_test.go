package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The published row and the order binding agree for allied, hostile, and
// asymmetric rows. This is the resolver predicate the cursor mirrors [04
// §3.4][05 R-SHARE-01 §1].
func TestPublishedDiplomacyRowsMatchOrderHostility(t *testing.T) {
	s := newLoopTestSession(t, 3)
	s.Econ.Players[0].Allies[1] = true
	s.Econ.Players[1].Allies[0] = false
	binding := s.newOrderBinding()
	if binding == nil || binding.Hostility == nil {
		t.Fatal("missing order hostility binding")
	}

	for _, tc := range []struct {
		name        string
		actorOwner  uint8
		targetOwner uint8
		want        bool
	}{
		{"allied different owner", 0, 1, false},
		{"enemy", 0, 2, true},
		{"asymmetric actor declaration", 1, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actor := &units.Unit{Owner: tc.actorOwner}
			target := &units.Unit{Owner: tc.targetOwner}
			if got := binding.Hostility(actor, target); got != tc.want {
				t.Fatalf("authoritative hostility = %v, want %v", got, tc.want)
			}
			rows := publishedPlayerRows(t, s, uint32(10+tc.actorOwner*3+tc.targetOwner))
			got := actor.Owner != target.Owner && !rows[actor.Owner].Allies[target.Owner]
			if got != tc.want {
				t.Fatalf("published hostility = %v, want %v", got, tc.want)
			}
		})
	}
}
