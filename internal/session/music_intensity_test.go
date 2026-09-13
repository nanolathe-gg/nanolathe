package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"testing"
)

func TestMusicIntensityDamageParticipantsAndHealing(t *testing.T) {
	for _, tc := range []struct {
		name             string
		victim, attacker uint8
		kind             uint8
		null             bool
		want             int
	}{
		{"local victim", 0, 1, combat.KindOrdinary, false, 1},
		{"local attacker", 1, 0, combat.KindOrdinary, false, 1},
		{"same owner", 0, 0, combat.KindOrdinary, false, 1},
		{"other participants", 1, 1, combat.KindOrdinary, false, 0},
		{"healing", 0, 1, combat.KindHeal, false, 0},
		{"environment", 0, 1, combat.KindNoReaction, true, 0},
		{"paralyzer", 1, 0, combat.KindParalyzer, false, 1},
		{"no reaction", 0, 1, combat.KindNoReaction, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newLoopTestSession(t, 2)
			s.LocalOwner, s.ViewingOwner = 0, 1
			v, a := s.Units.IterSliced()[0], s.Units.IterSliced()[1]
			v.Owner, a.Owner = tc.victim, tc.attacker
			s.publication.events.Reset()
			attacker := a.Handle
			if tc.null {
				attacker = 0
			}
			s.Combat.AcceptDamage(s.Units, 9, combat.DamageInput{Victim: v.Handle, Attacker: attacker, Kind: tc.kind, Nominal: 0})
			count := 0
			for _, ev := range s.publication.events.SnapshotEvents() {
				if ev.Kind == frame.EventKindMusicIntensity {
					count++
					if ev.Team != 0 || ev.Magnitude != 1 || ev.Tick != 9 {
						t.Fatalf("event=%+v", ev)
					}
				}
			}
			if count != tc.want {
				t.Fatalf("events=%d want %d", count, tc.want)
			}
		})
	}
}

func TestMusicIntensityDeathCauseAndStoredAttacker(t *testing.T) {
	for _, tc := range []struct {
		name             string
		cause            combat.Cause
		victim, attacker uint8
		present          bool
		want             int
	}{
		{"ordinary same-side", combat.CauseOrdinary, 0, 0, true, 5},
		{"cargo same-side", combat.CauseCargo, 0, 0, true, 5},
		{"reclaim same-side", combat.CauseReclaim, 0, 0, true, 0},
		{"reclaim enemy", combat.CauseReclaim, 1, 0, true, 5},
		{"local victim remote attacker", combat.CauseOrdinary, 0, 1, true, 0},
		{"missing victim player", combat.CauseOrdinary, 1, 0, false, 0},
		{"self destruct", combat.CauseSelfDestruct, 1, 0, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newLoopTestSession(t, 1)
			s.LocalOwner, s.ViewingOwner = 0, 1
			u := s.Units.IterSliced()[0]
			u.Owner, u.LastDamageSide, u.LastDamageCause = tc.victim, tc.attacker, uint8(tc.cause)
			u.Remaining = 0.5      // completion does not gate the activity weight
			u.EngagementTarget = 0 // stored owner survives attacker removal
			s.Econ.Players[u.Owner].Exists = tc.present
			s.publication.events.Reset()
			s.Units.OnDeath(u.Handle, 0, u)
			points := int32(0)
			for _, ev := range s.publication.events.SnapshotEvents() {
				if ev.Kind == frame.EventKindMusicIntensity {
					points += ev.Magnitude
				}
			}
			if int(points) != tc.want {
				t.Fatalf("points=%d want %d", points, tc.want)
			}
		})
	}
}
