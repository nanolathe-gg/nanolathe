package session

import (
	"fmt"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
)

// radarBenchWorld builds n live units, each owned by player 0 and carrying
// weaponSlots populated weapon slots (1 or 3), for the publishSnapshot radar
// contact benchmarks below (review finding R05).
func radarBenchWorld(b *testing.B, n, weaponSlots int) *Session {
	b.Helper()
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "bench-contact"},
		MaxDamage:        1, CanGuard: true, RadarDistance: 400,
	}
	if weaponSlots >= 1 {
		def.Weapon1Def = &content.WeaponDef{ID: 1, Range: 100}
	}
	if weaponSlots >= 2 {
		def.Weapon2Def = &content.WeaponDef{ID: 2, Range: 200}
	}
	if weaponSlots >= 3 {
		def.Weapon3Def = &content.WeaponDef{ID: 3, Range: 300}
	}
	// One player's slice must hold every benchmark unit [P0-16 §3.2].
	w := newSessionFixtureWorld(n+100, nil)
	for i := 0; i < n; i++ {
		if _, err := w.Create(def, 0, 0, 0, 0); err != nil {
			b.Fatalf("create unit %d: %v", i, err)
		}
	}
	return &Session{Units: w, Snapshot: frame.NewBuffer(), LocalOwner: 0}
}

func benchmarkPublishSnapshotRadar(b *testing.B, n, weaponSlots int) {
	s := radarBenchWorld(b, n, weaponSlots)
	// Warm the committed buffer's capacities (top-level contacts slice and
	// every contact's ring backing array) before timing, matching how a long
	// running session's steady-state allocation behaves after the first few
	// ticks [03 §3.9].
	for i := uint32(1); i <= 3; i++ {
		s.publishSnapshot(i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.publishSnapshot(uint32(4 + i))
	}
}

func BenchmarkPublishSnapshotRadarContacts(b *testing.B) {
	for _, n := range []int{100, 500, 1500} {
		for _, slots := range []int{1, 3} {
			b.Run(fmt.Sprintf("units=%d/slots=%d", n, slots), func(b *testing.B) {
				benchmarkPublishSnapshotRadar(b, n, slots)
			})
		}
	}
}
