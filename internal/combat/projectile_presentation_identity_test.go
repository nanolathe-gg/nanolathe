package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// TestProjectilePresentationIDsSurviveCompactionAndSplitClones keeps the
// presentation-only identity parallel to, but separate from, the compacting
// retail record array [06 §5.1][06 §5.2][I5][I6].
func TestProjectilePresentationIDsSurviveCompactionAndSplitClones(t *testing.T) {
	var svc Service
	first, ok := svc.Reserve()
	if !ok {
		t.Fatal("reserve first projectile")
	}
	survivor, ok := svc.Reserve()
	if !ok {
		t.Fatal("reserve survivor")
	}
	before := svc.PresentationID(survivor)
	if before == 0 || before == svc.PresentationID(first) {
		t.Fatalf("root identities = %d/%d, want distinct nonzero values", svc.PresentationID(first), before)
	}
	svc.MarkDead(first)
	svc.Compact(nil)
	if got := svc.PresentationID(1); got != before {
		t.Fatalf("compacted survivor identity = %d, want %d", got, before)
	}

	root, ok := svc.Reserve()
	if !ok {
		t.Fatal("reserve burst root")
	}
	p := &svc.Records[int(root)-1]
	p.WeaponID, p.BurstRemaining, p.BurstDeadline = 7, 1, 1
	rootID := svc.PresentationID(root)
	weapon := &content.WeaponDef{ID: 7, WeaponTimer: 10}
	if clones := svc.AdvanceBursts(1, nil, func(id int32) (*content.WeaponDef, bool) {
		return weapon, id == weapon.ID
	}, nil); clones != 1 {
		t.Fatalf("burst clones = %d, want 1", clones)
	}
	cloneID := svc.PresentationID(3)
	if cloneID == 0 || cloneID == rootID {
		t.Fatalf("root/clone identities = %d/%d, want distinct nonzero values", rootID, cloneID)
	}
}
