package orders

import "github.com/nanolathe-gg/nanolathe/internal/units"

// Nanolathe Modern policy: DESIGN_UNITS_ORDERS_COB "Modern patrol work".
// A movement wake is not necessarily arrival, and a terminal work code is
// not necessarily success. Record the semantic outcome before normal cleanup.
func (*ModernRules) AutomaticWorkResult(u *units.Unit, n *Node, satisfied uint32, code Code, _ uint32) Code {
	q := QueueOfUnit(u)
	if q == nil || n == nil {
		return code
	}
	if modernWorkPatrol(n) && code == 6 && satisfied&gateArrived != 0 && satisfied&gateNoRoute == 0 {
		q.patrolWorkPaused = false
	}
	if n.workAssignment == nil || !modernWorkPatrol(n.workAssignment) {
		return code
	}
	if n.patrolReturn {
		if n.Phase == 0 {
			n.patrolReturnArrived = false
		}
		if n.ID == rowMoveGround && n.Phase == 1 || n.ID == Lookup("VTOL_Move") && n.Phase == 2 {
			n.patrolReturnArrived = satisfied&gateArrived != 0 && satisfied&gateNoRoute == 0
		}
		if code == 5 || code == 7 || code == 8 || code == 9 {
			if code != 5 || !n.patrolReturnArrived {
				q.patrolWorkPaused = true
				return 8
			}
		}
		return code
	}
	if !n.automaticWork || code != 5 && code != 7 && code != 8 && code != 9 {
		return code
	}
	success := false
	if code == 5 {
		target := lookupTarget(u, n.Target)
		switch n.ID {
		case rowHelpBuild, rowVTOLHelpBuild:
			success = target != nil && target.Alive && target.Remaining == 0 && satisfied&gateCancelCurrent == 0
		case rowRepairUnit, rowVTOLRepairUnit:
			success = target != nil && target.Alive && target.Def != nil && moverMode(target) == 1 && !leashBroken(u, n)
			if success {
				if n.ID == rowVTOLRepairUnit {
					success = health16(target) >= uint32(target.Def.MaxDamage)
				} else {
					success = uint32(target.Health) >= uint32(target.Def.MaxDamage)
				}
			}
		default:
			success = n.ID == Lookup("Reclaim") && n.Phase == 5 || n.ID == Lookup("VTOL_Reclaim") && n.Phase == 4
		}
	}
	if !success {
		q.patrolWorkPaused = true
		return 8
	}
	return code
}
