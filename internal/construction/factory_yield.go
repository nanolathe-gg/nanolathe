package construction

import (
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The three call sites below are the retail-side ones: a refused yard close, a
// blocked product exit and a blocked mobile build site. Each hands the current
// clearance rectangle to the session's Rules, which answers with nothing under
// Strict 3.1 and with the approved clearance policy under Modern
// (rules.go, rules_modern.go).

func (s *Service) yieldClosingYard(factory *units.Unit, requested bool, rect world.FootprintRect, yard []world.YardCell, tick uint32) {
	if !requested {
		s.yieldFactoryExit(factory, rect, yard, tick)
	}
}

func (s *Service) yieldFactoryExit(factory *units.Unit, clear world.FootprintRect, closingYard []world.YardCell, tick uint32) {
	if factory == nil || factory.Def == nil || factory.Def.BMCode != 0 || !factory.Def.Builder {
		return
	}
	s.rules().YieldObstruction(s, factory, clear, closingYard, tick, false)
}

// yieldConstructionSite gives an idle blocker's local route priority over the
// global search backlog, without extending the builder's blocked-site budget.
// Nanolathe Modern policy: DESIGN_ECONOMY_CONSTRUCTION, "Modern construction-site yielding".
func (s *Service) yieldConstructionSite(builder *units.Unit, clear world.FootprintRect, tick uint32) {
	s.rules().YieldObstruction(s, builder, clear, nil, tick, true)
}
