package session

import (
	"math"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content/profiles"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
)

// The shipped profile must select the historical caller without selecting the
// unrelated proportional repair module. Strict retains its retail caller.
// [research/extensions/ta-zero-engine.md "Historical passive self-repair caller"]
func TestZeroPassiveRepairProfile(t *testing.T) {
	profile, err := profiles.Lookup("zero")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Community39, gameplay.Modern} {
		t.Run(string(mode), func(t *testing.T) {
			s, def := healTimeFixture(t, 3)
			s.CommunitySources = CommunitySources{Content: profile.GameplaySources()}
			s.BindRules(RuleSetForMode(mode))
			limit := 127
			if mode == gameplay.Strict31 {
				limit = 0
			}
			if s.aiCommunity().AIBuilderPlacementLimit != limit {
				t.Fatal("Zero Classic AI limit did not follow rule projection")
			}
			if s.Build.Community.RepairRate.Enabled {
				t.Fatal("Zero enabled the proportional repair helper")
			}
			u := s.Units.Unit(healTimeSpawn(t, s, def, 2800))
			u.Remaining = 0
			beforeRNG, beforeCRT := s.SimRNG().Draws(), s.CrtRNG().Draws()
			for tick := uint32(1); tick <= 240; tick++ {
				s.stepHealTimeSelfRepair(u, tick)
			}
			want := int32(2860)
			if mode == gameplay.Strict31 {
				want = 2800
			}
			buckets := s.Econ.UnitBuckets(u.Handle)
			if u.Health != want || buckets[economy.Energy].Requested != float32(want-2800) {
				t.Fatalf("health=%d energy=%v, want %d and %d", u.Health, buckets[economy.Energy].Requested, want, want-2800)
			}
			if s.SimRNG().Draws() != beforeRNG || s.CrtRNG().Draws() != beforeCRT {
				t.Fatal("passive repair consumed RNG")
			}
			if mode == gameplay.Strict31 {
				return
			}
			// Construction uses a bitwise zero test, including negative zero.
			for _, remaining := range []float32{0.5, math.Float32frombits(1 << 31)} {
				u.Remaining = remaining
				s.stepHealTimeSelfRepair(u, 244)
				if u.Health != want {
					t.Fatal("unfinished unit repaired")
				}
			}
			u.Remaining = 0
			buckets[economy.Energy].Carry = 1
			s.stepHealTimeSelfRepair(u, 244)
			if u.Health != want {
				t.Fatal("energy stall admitted passive healing")
			}
		})
	}
}
