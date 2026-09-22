package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

func TestCommunityInterceptorCircleAndPoolOrder(t *testing.T) {
	s := NewServiceWithProjectileCapacity(0)
	s.Rules = CommunityRules{}
	s.Community.AntinukeCircularCoverage = true
	w := &content.WeaponDef{ID: 1, Targetable: true, Coverage: 5}
	weapons := map[int32]*content.WeaponDef{1: w}
	for _, point := range []Vec3{{X: fixedI(5), Z: fixedI(5)}, {X: fixedI(3), Z: fixedI(4)}, {X: fixedI(1)}} {
		h, _ := s.Reserve()
		s.Records[int(h)-1] = Projectile{WeaponID: 1, ShooterSide: 1, TargetPos: point}
	}
	scan := func(want pool.Handle) {
		t.Helper()
		got, _, ok := FindInterceptorTarget(s, Vec3{}, 0, 5, weapons)
		if !ok || got != want {
			t.Fatalf("target = %d/%v, want %d", got, ok, want)
		}
	}
	scan(2) // inclusive circle boundary, before the closer third record
	s.Records[0].TargetProjectile = 2
	scan(3) // an existing reservation excludes the boundary record
	s.Records[0].TargetProjectile = 0
	s.Rules = StrictRules{}
	scan(1) // Strict retains the square even with the feature value present
	if got := s.InterceptorRingRadius(w); got != -507 {
		t.Fatalf("Strict ring = %d", got)
	}
	s.Rules = CommunityRules{}
	if got := s.InterceptorRingRadius(w); got != 5 {
		t.Fatalf("Community ring = %d", got)
	}
	s.Community.AntinukeCircularCoverage = false
	scan(1)
}
