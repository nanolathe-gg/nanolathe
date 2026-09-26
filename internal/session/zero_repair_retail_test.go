//go:build retail

package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// Released definitions exercise each regular mask family against the actual
// composed caller and resource helper, without script or combat noise.
func TestZeroAlpha5PassiveRepairRates(t *testing.T) {
	f := loadZeroPackage(t)
	s := f.enter(t, "ashap plateau", 2)
	for i, tc := range []struct {
		key  string
		mask int32
		gain int32
	}{
		{"CoreCommander", 3, 64},
		{"CoreT2Gunship", 7, 32},
		{"CoreT1AATurret", 15, 16},
		{"CoreT1AATank", 31, 8},
		{"CoreT1ES", 63, 4},
		{"CoreT1Sensor", 127, 2},
	} {
		t.Run(tc.key, func(t *testing.T) {
			u := placeCompleteRetailUnit(t, s, tc.key, 0, numeric.FixedFromInt(int64(1000+i*256)), numeric.FixedFromInt(1000))
			if u.Def.HealTime != tc.mask {
				t.Fatalf("released mask=%d, want %d", u.Def.HealTime, tc.mask)
			}
			u.Health = u.Def.MaxDamage - min(100, u.Def.MaxDamage/2)
			before := u.Health
			buckets := s.Econ.UnitBuckets(u.Handle)
			energy := buckets[economy.Energy].Requested
			for tick := uint32(1); tick <= 256; tick++ {
				s.stepHealTimeSelfRepair(u, tick)
			}
			if u.Health-before != tc.gain || buckets[economy.Energy].Requested-energy != float32(tc.gain) {
				t.Fatalf("gained health=%d energy=%v, want %d each", u.Health-before, buckets[economy.Energy].Requested-energy, tc.gain)
			}
		})
	}
}
