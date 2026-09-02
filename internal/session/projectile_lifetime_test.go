package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
)

// TestPublishedProjectileLifetimeIsWeaponTimer closes the open question the
// publication carried: render type 5 draws frame
// `N - ((expiry - currentTick) * N) / weapontimer`, so the lifetime the view
// hands presentation is the weapon's `weapontimer`, not its `duration`
// [06 R-WFX-01 §4]. The two are authored independently, so the test picks
// values that cannot be confused.
func TestPublishedProjectileLifetimeIsWeaponTimer(t *testing.T) {
	w := &content.WeaponDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "lifetime-weapon"},
		ID:               1,
		WeaponTimer:      45,
		Duration:         900,
		RenderType:       5,
	}
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{w.CanonicalKey: w}}
	cat.RebuildWeaponIndex()

	svc := &combat.Service{}
	h, ok := svc.Reserve()
	if !ok {
		t.Fatal("reserve projectile")
	}
	p := &svc.Records[int(h)-1]
	p.WeaponID = w.ID
	p.CreationTick, p.ExpiryTick = 1, 46

	s := &Session{Catalog: cat, Combat: svc, Snapshot: &frame.Buffer{}}
	s.publishSnapshot(2)
	f := s.Snapshot.Current()
	if f == nil || len(f.Projectiles) != 1 {
		t.Fatalf("published frame = %#v", f)
	}
	if got := f.Projectiles[0].Lifetime; got != w.WeaponTimer {
		t.Fatalf("published lifetime = %d, want the weapon timer %d (duration is %d)", got, w.WeaponTimer, w.Duration)
	}
}
