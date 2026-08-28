package session

import (
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/world"
)

// InitBattleWind is the single battle-entry wind initializer per [01 §7.3]
// [R-CORE-02].
//
// DET-03: battle entry performs NO wind draws. The earlier reading drew the
// briefing-screen speed/direction/interval here; [R-CORE-02] establishes those
// are front-end display state with no battle-side reader, and that battle
// entry only zeroes the deadline. The zeroed deadline plus phase 8's strict
// gate (not due while tick < deadline) leaves the tick-0 call unfired, so the
// battle's first wind chain runs inside the first sub-tick (tick 1 > 0) and
// consumes the documented 1 CRT + 1..2 sim draws there [R-CORE-02].
//
// The World and Mission retention of bounds is the only input; this function
// performs no filesystem or catalog work and does not reseed either stream.
// The front-end briefing display, when built, draws via world.Wind.SeedBriefing
// in a clearly-labeled front-end path only.
func InitBattleWind(bounds mission.WindBounds) *world.Wind {
	w := world.NewWind(bounds.Min, bounds.Max)
	// NextChange is the zero value: the deadline is zeroed at entry [R-CORE-02].
	return w
}

// InitBattleWindForSession creates or replaces s.Wind from s.Mission's
// retained WindBounds with the deadline zeroed and no draws [R-CORE-02]. A
// session without a mission has no authored bounds and is left uninitialized;
// strict session composition reports that missing dependency. This is the one
// production battle-entry path.
func (s *Session) InitBattleWindForSession() {
	if s == nil || s.Mission == nil {
		return
	}
	s.Wind = InitBattleWind(s.Mission.WindBounds)
}

// InitWindForSession is the legacy two-argument form of
// InitBattleWindForSession. Both arguments are ignored: battle entry consumes
// no wind draws and zeroes the deadline [R-CORE-02], so there is nothing for
// a stream or a tick value to do here. Deprecated: call
// InitBattleWindForSession(). AUDIT(parity-spine): retained only so existing
// fixture call sites compile; shrink-only.
func (s *Session) InitWindForSession(_ *rng.CRT, _ uint32) {
	s.InitBattleWindForSession()
}

// Later wind arithmetic [01 §7.3] [GAP T13] is intentionally absent here.
// The phase-8 chain — one CRT interval draw, then sim strength
// simRand(max-min)+min (exclusive span), then sim heading simRand(0x10000)
// only when strength is nonzero — lives entirely in world.Wind.Jitter
// [R-CORE-01 §4.4.1]. Any second implementation would double the draw count
// and desynchronize later CRT consumers (meteor geometry [06 §6.5], screen
// shake [03 §5.6], audio variants [03 §8.3]).
