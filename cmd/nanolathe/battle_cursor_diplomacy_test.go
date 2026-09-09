package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Cursor hostility is a committed-frame question. The reverse row stays
// independent so this locks the actor-to-target direction [04 §3.4][05
// R-SHARE-01 §1][I6].
func TestCursorHostilityUsesCommittedDirectionalDiplomacy(t *testing.T) {
	buf := frame.NewBuffer()
	written := buf.BeginWrite()
	written.Players[0].Allies[1] = true
	written.Players[1].Allies[0] = false
	if err := buf.Publish(1); err != nil {
		t.Fatal(err)
	}
	b := &battleSession{sess: &session.Session{Snapshot: buf}}
	for _, tc := range []struct {
		name        string
		actorOwner  uint8
		targetOwner uint8
		want        bool
	}{
		{"allied different owner", 0, 1, false},
		{"enemy", 0, 2, true},
		{"asymmetric reverse declaration", 1, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actor := &units.Unit{Owner: tc.actorOwner}
			if got := b.hostile(actor, &units.Unit{Owner: tc.targetOwner}); got != tc.want {
				t.Fatalf("hostile(%d, %d) = %v, want %v", tc.actorOwner, tc.targetOwner, got, tc.want)
			}
			if actor.Orders != nil {
				t.Fatal("cursor hostility allocated an order queue on a snapshot actor")
			}
		})
	}
}
