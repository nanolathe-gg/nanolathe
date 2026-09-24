//go:build pathbench && retail

package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/path"
)

// Comparison rule sets for the opt-in path benchmark (docs/PATH_BENCHMARK.md
// "Comparison rule sets"). modern-no-pathfinding is Modern with every Modern
// pathfinding policy switched off; each modern-no-<policy> set switches off one,
// so a run can attribute a change to the policy that makes it.

type modernNoPathfinding struct{ movement.ModernRules }

func (*modernNoPathfinding) PathWorkBound(*movement.System) (int32, bool)      { return 0, false }
func (*modernNoPathfinding) GroupDestinationSlots(*movement.System) bool       { return false }
func (*modernNoPathfinding) AlliedPassThrough(*movement.System) bool           { return false }
func (*modernNoPathfinding) UnreachableMoves(*movement.System) (int32, uint32) { return 0, 0 }
func (*modernNoPathfinding) JamRelease(*movement.System) (uint16, uint32)      { return 0, 0 }

type modernNoBound struct{ movement.ModernRules }

func (*modernNoBound) PathWorkBound(*movement.System) (int32, bool) { return 0, false }

type modernNoSlots struct{ movement.ModernRules }

func (*modernNoSlots) GroupDestinationSlots(*movement.System) bool { return false }

type modernNoPass struct{ movement.ModernRules }

func (*modernNoPass) AlliedPassThrough(*movement.System) bool { return false }

type modernNoUnreach struct{ movement.ModernRules }

func (*modernNoUnreach) UnreachableMoves(*movement.System) (int32, uint32) { return 0, 0 }

type modernNoJam struct{ movement.ModernRules }

func (*modernNoJam) JamRelease(*movement.System) (uint16, uint32) { return 0, 0 }

func init() {
	RegisterRuleSet("modern-no-pathfinding", func() RuleSet {
		return RuleSet{Movement: &modernNoPathfinding{}, Path: path.RetailKernel{}}
	})
	RegisterRuleSet("modern-no-bound", func() RuleSet { return RuleSet{Movement: &modernNoBound{}} })
	RegisterRuleSet("modern-no-slots", func() RuleSet { return RuleSet{Movement: &modernNoSlots{}} })
	RegisterRuleSet("modern-no-pass", func() RuleSet { return RuleSet{Movement: &modernNoPass{}} })
	RegisterRuleSet("modern-no-unreach", func() RuleSet { return RuleSet{Movement: &modernNoUnreach{}} })
	RegisterRuleSet("modern-no-jam", func() RuleSet { return RuleSet{Movement: &modernNoJam{}} })
	RegisterRuleSet("modern-no-straighten", func() RuleSet { return RuleSet{Path: path.RetailKernel{}} })
}
