package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// TestProjectilePublicationKeepsAdmissionIdentityAcrossCompactionAndBattle
// replacement verifies the committed-frame boundary neither substitutes a
// packed handle nor lets a new service reuse a prior subject identity [I6].
func TestProjectilePublicationKeepsAdmissionIdentityAcrossCompactionAndBattle(t *testing.T) {
	svc := &combat.Service{}
	first, _ := svc.Reserve()
	survivor, _ := svc.Reserve()
	svc.Records[int(first)-1].WeaponID = 1
	svc.Records[int(survivor)-1].WeaponID = 2
	s := &Session{Combat: svc, Snapshot: frame.NewBuffer()}
	s.publishSnapshot(1)
	before := s.Snapshot.Current().Projectiles[1]

	svc.MarkDead(first)
	svc.Compact(nil)
	svc.Records[0].Pos.X = numeric.FixedFromInt(8)
	s.publishSnapshot(2)
	after := s.Snapshot.Current().Projectiles[0]
	if after.Handle != 1 || after.PresentationID != before.PresentationID {
		t.Fatalf("compacted publication = handle %d id %d, want handle 1 id %d", after.Handle, after.PresentationID, before.PresentationID)
	}

	nextBattle := &combat.Service{}
	h, ok := nextBattle.Reserve()
	if !ok {
		t.Fatal("reserve new-battle projectile")
	}
	nextBattle.Records[int(h)-1].WeaponID = 2
	s.Combat = nextBattle
	s.publishSnapshot(3)
	fresh := s.Snapshot.Current().Projectiles[0]
	if fresh.Handle != after.Handle || fresh.PresentationID == 0 || fresh.PresentationID == after.PresentationID {
		t.Fatalf("new-battle publication = handle %d id %d after id %d", fresh.Handle, fresh.PresentationID, after.PresentationID)
	}
}
