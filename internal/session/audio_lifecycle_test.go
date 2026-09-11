package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// The shell's audio service survives battles, but its cue deadlines belong
// to one session tick domain (DESIGN_PRESENTATION_CLIENT §5).
func TestInitAudioSharedOwnerStartsNewBattleCueLifetime(t *testing.T) {
	for _, tc := range []struct {
		name  string
		start uint32
	}{{"new battle", 0}, {"earlier saved battle", 120}} {
		t.Run(tc.name, func(t *testing.T) {
			start := tc.start
			old := &Session{Clock: &clock.State{GlobalTick: 900}}
			old.InitAudio(nil)
			owner := old.Audio
			category := &audio.Category{}
			category.Rows[audio.SlotUnderAttack].Variants = []string{"attack"}
			category.Rows[audio.SlotSelect].Variants = []string{"select"}
			bindFixtureResolver := func() {
				owner.Queue.SetResolver(func(pool.Handle) (*audio.Category, string, bool) {
					return category, "unit", true
				})
			}
			bindFixtureResolver()
			var plays int
			owner.Queue.OnPlay(func(string, audio.Slot, pool.Handle) { plays++ })
			if !old.EmitUnderAttack(1) {
				t.Fatal("old battle cue rejected")
			}
			owner.DrainEvents(900, nil)
			if plays != 1 || !old.EmitSelect(1) {
				t.Fatal("old battle did not establish deadline and pending cue")
			}
			old.InitAudio(nil)
			if owner.Queue.Count != 1 || owner.Queue.BaseTime != 900 || old.EmitUnderAttack(1) {
				t.Fatal("same-session initialization lost pending cue or cooldown")
			}
			// A detached candidate can share the service before adoption without
			// resetting the live battle. Binding happens at successful adoption.
			next := &Session{Clock: &clock.State{GlobalTick: start}, Audio: owner}
			if owner.Queue.Count != 1 || owner.Queue.BaseTime != 900 {
				t.Fatal("candidate assignment changed live cue state")
			}
			next.InitAudio(nil)
			if owner.Queue.Count != 0 || owner.Queue.BaseTime != 0 || owner.Frame() != 0 {
				t.Errorf("new battle retained queue state: count=%d base=%d frame=%d", owner.Queue.Count, owner.Queue.BaseTime, owner.Frame())
			}
			bindFixtureResolver()
			if !next.EmitUnderAttack(1) {
				t.Fatal("old battle cooldown suppressed new battle cue")
			}
			if start == 0 {
				owner.DrainEvents(29, nil)
				if plays != 1 {
					t.Fatal("reset bypassed initial 30-tick voice window [03 §8.3]")
				}
				next.Clock.GlobalTick = 30
				if !next.EmitUnderAttack(1) {
					t.Fatal("silent initial resolve rearmed cooldown")
				}
			}
			owner.DrainEvents(next.Clock.GlobalTick, nil)
			if plays != 2 {
				t.Fatalf("new battle voice suppressed by old drain window: plays=%d", plays)
			}
			if next.EmitUnderAttack(1) {
				t.Fatal("new battle audible cue failed to rearm its cooldown [03 §8.3]")
			}
		})
	}
}

func TestInitAudioSameSessionPreservesPendingCuesAndRandomHistory(t *testing.T) {
	s := &Session{}
	s.InitAudio(nil)
	q := s.Audio.Queue
	q.InsertAt(900, audio.SlotSelect, 1, "pending")
	q.CRTRandom().Rand()
	state := q.CRTRandom().State
	s.InitAudio(nil)
	if s.Audio.Queue != q || q.Count != 1 || q.Entries[0].Text != "pending" {
		t.Fatal("repeated initialization discarded same-session pending cue")
	}
	if q.CRTRandom().State != state {
		t.Fatal("repeated initialization rewound presentation random history")
	}
}
