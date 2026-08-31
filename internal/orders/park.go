package orders

import (
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// Park is the terminal record of a factory product that inherited no rally
// [04 R-FAC-02 §4]: `GetBuilt` appends it when the builder's queue holds no
// `QMove`/`QPatrol`, and walking to its rectangle border is what carries a
// no-rally product off its factory pad. It is not an egress offset — the
// factory contributes nothing to it.
//
// Handler contract [04 R-ORD-01 §2, the `Park` row]:
//
//	Phase 0: no mover reference -> cancel-all. With `canfly`: goal = own
//	position, re-identify the record as `VTOL_Move`, restart (the air move runs
//	in the same cascade). Else s = FootPrintX (+3 when the movement class's
//	MinWaterDepth word is non-negative — template default -10000, so land
//	classes get no +3); install a rectangle goal with origin
//	(cellX - 4s, cellZ - 3s) and size (8s, 6s) in cells, where cellX/Z are the
//	unit's whole-unit position shifted to cells; gate = 0xE0; advance.
//	Phase 1: satisfied 0x20 -> complete; a record behind it exists -> complete;
//	else deadline 30, restart. Other: cancel-all.
//
// The path layer treats the rectangle as a perimeter goal: the admissible
// cells are exactly the rectangle border and arrival requires lying on it
// [04 §7.2][04 R-FAC-02 §4].

const (
	// parkGate is the exact phase-0 gate assignment of the Park row
	// [04 R-ORD-01 §2]. Bit 0x20 is the movement arrival bit [R-P0-01].
	parkGate uint32 = 0xE0
	// parkArrivalBit is the arrival bit the mover ORs into the record's
	// satisfied word on reaching the goal handle [R-P0-01].
	parkArrivalBit uint32 = 0x20
)

// ParkGoalRect reports the cell rectangle Park installed on n, Min inclusive
// and Max inclusive. The rectangle is stored on the record's three general
// parameters — origin X, origin Z, and the s term — so the movement layer can
// rebuild the perimeter goal without restating the arithmetic above.
// ok is false for a record that is not a Park record or has not run phase 0.
func ParkGoalRect(n *Node) (minX, minZ, maxX, maxZ int32, ok bool) {
	if n == nil || DescriptorFor(n.ID).Name != "Park" {
		return 0, 0, 0, 0, false
	}
	s := int32(n.Param3)
	if s <= 0 {
		return 0, 0, 0, 0, false
	}
	minX, minZ = int32(n.Param1), int32(n.Param2)
	return minX, minZ, minX + 8*s - 1, minZ + 6*s - 1, true
}

// parkHandler runs the parking rectangle. Phase 1 arms its exact thirty-tick
// deadline from the tick the pump is running (WU-18-7 retired the by-name
// `parkHandlerAtTick` special case the pump used to reach this body with; the
// tick is now the handler's fourth argument [04 R-ORD-01 §1]).
func parkHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	if u == nil || n == nil {
		return 7
	}
	switch n.Phase {
	case 0:
		// A building-class definition owns no mover: the allocator constructs
		// one only for bmcode 1 [04 R-FAC-02 §5].
		if u.Def == nil || !u.Def.BMCode {
			return 7
		}
		if u.Def.CanFly {
			// The air move handler runs in the same cascade from the product's
			// own position [04 R-FAC-02 §4][04 R-AIR-01 §6].
			n.GoalX, n.GoalY, n.GoalZ = u.X, u.Y, u.Z
			n.Param1, n.Param2, n.Param3 = 0, 0, 0
			if id := Lookup("VTOL_Move"); id != 0 {
				n.ID = id
				n.StaticGate = DescriptorFor(id).StaticGate
			}
			return 0
		}
		s := u.Def.FootprintX
		// The word read is the movement class's MinWaterDepth, not the
		// definition's yard-map width; the template default is -10000, so land
		// classes get no +3 and ship classes with a non-negative minimum depth
		// do [04 R-FAC-02 §4][04 R-FAC-02 §7].
		if u.Def.MinWaterDepth >= 0 {
			s += 3
		}
		if s <= 0 {
			// A definition with no authored footprint has no rectangle to
			// install; the record cannot steer the unit anywhere.
			return 7
		}
		// Whole units to cells, arithmetic shift, no footprint bias
		// [04 R-FAC-02 §4].
		originX := int32(int64(u.X)>>20) - 4*s
		originZ := int32(int64(u.Z)>>20) - 3*s
		n.GoalX = numeric.Fixed(int64(originX) << 20)
		n.GoalY = 0
		n.GoalZ = numeric.Fixed(int64(originZ) << 20)
		n.Param1, n.Param2, n.Param3 = uint32(originX), uint32(originZ), uint32(s)
		n.DynamicGate = parkGate
		return 1
	case 1:
		if satisfied&parkArrivalBit != 0 {
			return 5
		}
		if q := QueueForUnit(u); q != nil {
			prim := q.Primary()
			for i, rec := range prim {
				if rec != n {
					continue
				}
				if i+1 < len(prim) {
					return 5 // a record behind it exists
				}
				break
			}
		}
		n.Deadline = int32(tick + 30)
		n.DynamicGate |= 1 // the shared deadline setter always ORs bit 0 [04 R-ORD-01 §1]
		return 0
	default:
		return 7
	}
}

func ensureParkHandler() {
	if len(table) == 0 {
		return
	}
	id := Lookup("Park")
	if id == 0 || int(id) >= len(table) {
		return
	}
	if table[int(id)].Handler == nil {
		table[int(id)].Handler = parkHandler
	}
}

func init() { ensureParkHandler() }
