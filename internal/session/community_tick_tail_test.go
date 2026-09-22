package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"reflect"
	"testing"
)

type recordCommunityAreaTail struct {
	combat.Rules
	visit func(combat.AreaVictimQuery)
}

func (r recordCommunityAreaTail) AreaIndexTick(q combat.AreaVictimQuery) {
	r.visit(q)
	r.Rules.AreaIndexTick(q)
}

func TestCommunityCallbackBoundaryIsIndependentOfHostBatching(t *testing.T) {
	run := func(batch bool) []uint32 {
		s := newLoopTestSession(t, 0)
		s.SetGameplay(gameplay.Community39)
		s.EnablePhaseTrace()
		var ticks []uint32
		s.Rules.Combat = recordCommunityAreaTail{Rules: s.Rules.Combat, visit: func(q combat.AreaVictimQuery) {
			ticks = append(ticks, q.Tick)
			if len(s.PhaseTrace()) != int(q.Tick)*12 {
				t.Fatal("community callback entered retail phase sequence")
			}
			if s.PublicationCount() != q.Tick-1 {
				t.Fatal("callback ran after publication")
			}
		}}
		if batch {
			s.Step(5)
		} else {
			for now := int32(1); now <= 5; now++ {
				s.Step(now)
			}
		}
		return ticks
	}
	single, batched := run(false), run(true)
	if !reflect.DeepEqual(single, []uint32{1, 2, 3, 4, 5}) || !reflect.DeepEqual(single, batched) {
		t.Fatalf("single %v batched %v", single, batched)
	}
}
