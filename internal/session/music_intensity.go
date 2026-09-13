package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The event captures ownership and weight before a victim can disappear or its
// slot be reused. It is consumed only after publication [03 R-AUD-01 §5][I6].
func (s *Session) emitMusicIntensity(tick uint32, victim pool.Handle, weight int32) {
	if s == nil || s.publication == nil || s.publication.events == nil {
		return
	}
	s.publication.events.Admit(frame.Event{Kind: frame.KindMusicIntensity,
		Tick: tick, Target: victim, Team: s.LocalOwner, Magnitude: weight})
}

func (s *Session) emitDeathMusicIntensity(u *units.Unit) {
	if s == nil || u == nil || s.Econ == nil || int(u.Owner) >= len(s.Econ.Players) || !s.Econ.Players[u.Owner].Exists {
		return
	}
	// Only Reclaim has the distinct/nonneutral-side entry gate. Ordinary and
	// Cargo reach the full branch directly, even for same-side deaths. The
	// tail tests stored attacker ownership, not victim ownership or unit kill
	// credit eligibility [03 R-AUD-01 §5][06 §12.1].
	cause := combat.Cause(u.LastDamageCause)
	full := cause == combat.CauseOrdinary || cause == combat.CauseCargo ||
		(cause == combat.CauseReclaim && u.LastDamageSide != 10 && u.LastDamageSide != u.Owner)
	if full && u.LastDamageSide == s.LocalOwner {
		var tick uint32
		if s.Clock != nil {
			tick = s.Clock.GlobalTick
		}
		s.emitMusicIntensity(tick, u.Handle, 5)
	}
}
