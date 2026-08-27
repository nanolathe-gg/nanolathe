package session

import (
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/world"
)

// InitBattleWind is the single battle-entry wind initializer per [01 §7.3]
// [GAP T13] C17.
//
// It runs after mission/skirmish selection has retained the terrain bounds and
// performs exactly the three briefing-screen draws on the CRT stream:
//
//   - briefing strength: CRT() % (max-min+1) + min  // inclusive span [01 §7.3]
//   - briefing direction: CRT() & 0x3f             // six bits [01 §7.3]
//   - first next-change deadline: ((CRT()*10)/0x8000 + 5) * 30  // ticks [01 §7.3]
//
// The world.Wind holder implements the later in-sim draws directly: when the
// global tick passes the deadline one CRT draw advances the deadline and then
// one simulation draw takes the new strength, followed by a conditional heading
// draw [01 §7.3]. Session.authoritativeTick calls those methods consecutively;
// this file owns only battle-entry briefing draws.
//
// The World and Mission retention of bounds is the only input; this function
// performs no filesystem or catalog work and does not reseed either stream.
func InitBattleWind(w *world.Wind, crt *rng.CRT, tick uint32) {
	if w == nil || crt == nil {
		return
	}
	w.SeedBriefing(crt, tick)
}

// NewBattleWindFromBounds creates a Wind holder from the given inclusive
// bounds and seeds the briefing draws on the CRT stream per [01 §7.3] C17.
// It is the helper skirmish and mission setup both call after retaining the
// terrain bounds; no second draw path exists.
func NewBattleWindFromBounds(bounds mission.WindBounds, crt *rng.CRT, tick uint32) *world.Wind {
	w := world.NewWind(bounds.Min, bounds.Max)
	InitBattleWind(w, crt, tick)
	return w
}

// InitWindForSession creates or replaces s.Wind from s.Mission's retained
// WindBounds and seeds the briefing draws per [01 §7.3] C17. If s.Mission is
// nil the terrain's WindMin/WindMax are used as fallback; if s.World is also
// nil a zero range is used (still consumes the three draws per I4, see
// world/wind.go SeedBriefing comment). The caller supplies the CRT stream and
// the authoritative tick at which the battle entry occurs (normally the clock's
// GlobalTick before the first sub-tick).
func (s *Session) InitWindForSession(crt *rng.CRT, tick uint32) {
	if s == nil {
		return
	}
	var w *world.Wind
	if s.Mission != nil {
		w = NewBattleWindFromBounds(s.Mission.WindBounds, crt, tick)
	} else if s.World != nil {
		w = world.NewWind(s.World.WindMin, s.World.WindMax)
		InitBattleWind(w, crt, tick)
	} else {
		w = world.NewWind(0, 0)
		InitBattleWind(w, crt, tick)
	}
	s.Wind = w
}

// Later wind arithmetic [01 §7.3] [GAP T13] is intentionally absent here.
// The in-sim strength uses Sim(max-min)+min (exclusive span) and the heading
// uses Sim(0x10000) truncated to 16 bits, taken only when strength is nonzero.
// Those draws live in world.Wind.Jitter (one CRT draw for the interval) and
// world.Wind.Field (one or two Simulation draws). Any second implementation
// would double the draw count and desynchronize later CRT consumers (meteor
// geometry [06 §6.5], screen shake [03 §5.6], audio variants [03 §8.3]).
