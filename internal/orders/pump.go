// Package orders implements order records and the queue pump [04 §3.2, §3.3][05][GAP T3].
package orders

import (
	"fmt"

	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

// currentTick holds the tick of the current Pump dispatch for handler tick plumbing [04 §3.2].
var currentTick uint32

// Code is the handler result code [04 §3.3].
type Code uint8

const FlagActive uint32 = 0x1000 // active marker – exactly one primary node carries it [04 §3.3][plan C9]

const (
	FlagAutoOp uint32 = 1 << iota // existence established [04 §3.3][05 "Queue subtraction"]; numeric values not established
	FlagPurgeSurvivor
	FlagTombstone
	FlagRetryMark           // TODO(question) actual bit not located [04 §3.3] code 9
	FlagStopBuildingPending // TODO(question) StopBuilding pending flag lands with WU-06-7 [05 "Queue subtraction"]
)

// Node is an 86-byte retail order record identity [04 §3.2] C5 (I13).
type Node struct {
	ID           ID            // descriptor identity [04 §3.2]
	Phase        uint8         // handler-private phase byte [04 §3.2]
	DynamicGate  uint32        // dynamic gate mask [04 §3.2]
	Deadline     int32         // deadline tick, -1 for none [04 §3.2]
	Owner        pool.Handle   // owning unit [04 §3.2]
	Target       pool.Handle   // target smart-reference [04 §3.2]
	GoalX        numeric.Fixed // goal position three 16.16 [04 §3.2]
	GoalY        numeric.Fixed
	GoalZ        numeric.Fixed
	GuardX       int16 // guard/fight anchor [04 §3.2]
	GuardY       int16
	CachedX      int16 // cached target position [04 §3.2]
	CachedY      int16
	Param1       uint32 // three general parameters [04 §3.2]
	Param2       uint32
	Param3       uint32
	StaticGate   uint32 // copy of descriptor static gate [04 §3.2]
	CreationTick uint32 // creation-tick snapshot [04 §3.2]
	Satisfied    uint32 // accumulated satisfied-gate bits [04 §3.2]
	Flags        uint32 // flag bits [04 §3.3][05]
}

// Queue holds the two segments [04 §3.2] C5.
type Queue struct {
	primary   []*Node
	secondary []*Node
}

func (q *Queue) LenPrimary() int {
	if q == nil {
		return 0
	}
	return len(q.primary)
}
func (q *Queue) LenSecondary() int {
	if q == nil {
		return 0
	}
	return len(q.secondary)
}
func (q *Queue) Primary() []*Node {
	if q == nil {
		return nil
	}
	out := make([]*Node, len(q.primary))
	copy(out, q.primary)
	return out
}
func (q *Queue) Secondary() []*Node {
	if q == nil {
		return nil
	}
	out := make([]*Node, len(q.secondary))
	copy(out, q.secondary)
	return out
}

func isSecondary(id ID) bool {
	return DescriptorFor(id).StaticGate&0x40000 != 0 // [04 §3.1] rear-segment selection flag
}

func randBelow15() uint32 {
	if rng.Global.Sim == nil {
		panic("orders: rng.Global.Sim not seeded") // fail fast [01 §4.4] determinism
	}
	return rng.Global.Sim.Uint32n(15) // gameplay jitter uses simulation stream [I4][04 §3.3]
}

func findActive(q *Queue) int {
	if q == nil {
		return -1
	}
	for i, n := range q.primary {
		if n.Flags&FlagActive != 0 {
			return i
		}
	}
	return -1
}

func newNode(id ID, n Node) *Node {
	desc := DescriptorFor(id)
	nn := n
	nn.ID = id
	if nn.StaticGate == 0 {
		nn.StaticGate = desc.StaticGate
	}
	if nn.DynamicGate == 0 {
		nn.DynamicGate = desc.StaticGate
	}
	if nn.Deadline == 0 {
		nn.Deadline = -1
	}
	node := &Node{}
	*node = nn
	return node
}

var diagnostics []string

func Diagnostics() []string       { return append([]string(nil), diagnostics...) }
func ClearDiagnostics()           { diagnostics = nil }
func recordDiagnostic(msg string) { diagnostics = append(diagnostics, msg) }

func cleanupNode(n *Node) {
	// [05 "Queue subtraction"] strict order:
	// 1 restore interface identity – nop
	// 2 invoke handler with cancel-notification mask when wake byte requests it – TODO(question) requesting bit not established
	// 3 emit StopBuilding + network event when stop-building-pending flag is set – TODO(question) lands with WU-06-7
	// 4 release presentation payload – nil today
	// 5 ONLY when not tombstoned, clear weapon build targets + TargetCleared – TODO(question) lands with WU-06-7
	if n.Flags&FlagTombstone == 0 {
		// TargetCleared stub
	}
}

func (q *Queue) PurgeUnprotected() {
	if q == nil {
		return
	}
	// [05 "Queue insertion"] non-queued issue purges primary nodes lacking the protected flag
	kept := q.primary[:0]
	for _, n := range q.primary {
		if n.Flags&FlagPurgeSurvivor != 0 {
			kept = append(kept, n)
		} else {
			if n != nil {
				// non-head gets tombstone per [04 §3.3]; secondary always tombstoned via primary-anchor test
				// For purge, use primary head test
				isHead := n == q.primary[0]
				if !isHead {
					n.Flags |= FlagTombstone
				}
				cleanupNode(n)
			}
		}
	}
	q.primary = kept
	if len(q.primary) > 0 {
		q.primary[0].Flags |= FlagActive
		for i := 1; i < len(q.primary); i++ {
			q.primary[i].Flags &^= FlagActive
		}
	}
}

func (q *Queue) DropLeadingAutoOps() {
	if q == nil {
		return
	}
	// [05 "Queue insertion"] issuing any primary order drops leading auto/default-op nodes – leading RUN at front of each segment
	for len(q.primary) > 0 && q.primary[0].Flags&FlagAutoOp != 0 {
		n := q.primary[0]
		cleanupNode(n) // head not tombstoned
		q.primary = q.primary[1:]
	}
	for len(q.secondary) > 0 && q.secondary[0].Flags&FlagAutoOp != 0 {
		n := q.secondary[0]
		n.Flags |= FlagTombstone // secondary always tombstoned [04 §3.3]
		cleanupNode(n)
		q.secondary = q.secondary[1:]
	}
	if len(q.primary) > 0 {
		q.primary[0].Flags |= FlagActive
		for i := 1; i < len(q.primary); i++ {
			q.primary[i].Flags &^= FlagActive
		}
	}
}

func (q *Queue) Push(id ID, n Node) {
	if q == nil {
		return
	}
	node := newNode(id, n) // [04 §3.3][05 "Queue insertion"] C9
	act := findActive(q)
	if act >= 0 {
		pos := act + 1
		q.primary = append(q.primary, nil)
		copy(q.primary[pos+1:], q.primary[pos:])
		q.primary[pos] = node
	} else {
		q.primary = append(q.primary, node)
		if len(q.primary) == 1 {
			node.Flags |= FlagActive
		}
	}
}

func (q *Queue) PushSecondary(id ID, n Node) {
	if q == nil {
		return
	}
	node := newNode(id, n)
	if len(q.secondary) > 0 && q.secondary[0].Flags&FlagAutoOp != 0 {
		node.Flags |= FlagAutoOp // [05 "Queue insertion"] inherit old head's auto flag
	}
	q.secondary = append([]*Node{node}, q.secondary...)
}

func (q *Queue) CoalesceTail(id ID, n Node) {
	if q == nil {
		return
	}
	if isSecondary(id) {
		if len(q.secondary) > 0 {
			tail := q.secondary[len(q.secondary)-1]
			if tail.ID == id && tail.Param1 == n.Param1 { // [04 §3.3][05 "Queue insertion"] tail-only
				add := n.Param2
				if add == 0 {
					add = 1
				}
				tail.Param2 += add
				return
			}
		}
		q.PushSecondary(id, n)
		return
	}
	if len(q.primary) > 0 {
		tail := q.primary[len(q.primary)-1]
		if tail.ID == id && tail.Param1 == n.Param1 {
			add := n.Param2
			if add == 0 {
				add = 1
			}
			tail.Param2 += add
			return
		}
	}
	// tail-only fallback append [04 §3.3][05 "Queue insertion"]
	node := newNode(id, n)
	q.primary = append(q.primary, node)
	if len(q.primary) == 1 {
		node.Flags |= FlagActive
	}
}

func (q *Queue) CancelTailMost(match func(Node) bool) bool {
	if q == nil || match == nil {
		return false
	}
	for i := len(q.primary) - 1; i >= 0; i-- {
		if match(*q.primary[i]) {
			n := q.primary[i]
			if n.Param2 > 1 {
				n.Param2--
				return true
			}
			isHead := i == 0
			if !isHead {
				n.Flags |= FlagTombstone // [04 §3.3]
			}
			cleanupNode(n) // [05 "Queue subtraction"]
			copy(q.primary[i:], q.primary[i+1:])
			q.primary = q.primary[:len(q.primary)-1]
			if isHead && len(q.primary) > 0 {
				q.primary[0].Flags |= FlagActive
			}
			return true
		}
	}
	for i := len(q.secondary) - 1; i >= 0; i-- {
		if match(*q.secondary[i]) {
			n := q.secondary[i]
			if n.Param2 > 1 {
				n.Param2--
				return true
			}
			n.Flags |= FlagTombstone // secondary always effectively tombstoned [04 §3.3]
			cleanupNode(n)
			copy(q.secondary[i:], q.secondary[i+1:])
			q.secondary = q.secondary[:len(q.secondary)-1]
			return true
		}
	}
	return false
}

func (q *Queue) Pump(u *units.Unit, tick uint32) {
	if q == nil || u == nil {
		return
	}
	currentTick = tick // [04 §3.2] deadline semantics – handlers observe current tick like retail's global
	q.pumpPrimary(u, tick)
	if len(q.primary) > 0 {
		head := q.primary[0]
		sat := (head.Satisfied | u.Pending) & head.DynamicGate // [04 §3.3]
		if head.DynamicGate != 0 && sat == 0 {
			return // blocked primary front prevents ALL secondary dispatch [04 §3.3] C6
		}
	}
	q.pumpSecondary(u, tick)
}

func (q *Queue) pumpPrimary(u *units.Unit, tick uint32) {
	if q == nil || u == nil {
		return
	}
	// TODO(question) idle default-op creation when primary empty [05 "Queue pumping and result codes"] step 1: owner player-state settling byte, definition default-idle-op field
	for len(q.primary) > 0 {
		n := q.primary[0] // head-driven [05]
		if n.Deadline != -1 && tick >= uint32(n.Deadline) {
			n.Deadline = -1
			n.Satisfied |= 1 // [04 §3.3]
		}
		satisfied := (n.Satisfied | u.Pending) & n.DynamicGate // [04 §3.3] C6
		if n.DynamicGate != 0 && satisfied == 0 {
			return // blocked head stalls [04 §3.3] C6
		}
		n.Satisfied &^= satisfied
		u.Pending &^= satisfied
		n.DynamicGate = 0
		handler := DescriptorFor(n.ID).Handler
		if handler == nil {
			recordDiagnostic(fmt.Sprintf("orders: nil handler for %s", DescriptorFor(n.ID).Name)) // [docs/ORCHESTRATION.md §7] never spin
			return
		}
		code := handler(u, n, satisfied)
		switch code {
		case 0:
			n.Phase = 0 // [04 §3.3]
			continue
		case 1:
			n.Phase++ // [04 §3.3]
			continue
		case 2, 4:
			continue // [04 §3.3] re-evaluate same head
		case 3:
			n.DynamicGate = 1                             // [04 §3.3]
			n.Deadline = int32(tick + 30 + randBelow15()) // [04 §3.3][I4]
			return
		case 5, 8:
			cleanupNode(n) // [05 "Queue subtraction"]
			q.primary = q.primary[1:]
			if len(q.primary) > 0 {
				q.primary[0].Flags |= FlagActive
			}
			continue
		case 6:
			head := q.primary[0]
			q.primary = q.primary[1:]
			head.Flags &^= FlagActive
			q.primary = append(q.primary, head) // move to tail [04 §3.3]
			if len(q.primary) > 0 {
				q.primary[0].Flags |= FlagActive
				for i := 1; i < len(q.primary); i++ {
					q.primary[i].Flags &^= FlagActive
				}
			}
			continue
		case 7:
			for i, nn := range q.primary {
				if i != 0 {
					nn.Flags |= FlagTombstone
				}
				cleanupNode(nn)
			}
			for _, nn := range q.secondary {
				nn.Flags |= FlagTombstone
				cleanupNode(nn)
			}
			q.primary = nil
			q.secondary = nil // [05] via pair-removal helper
			return
		case 9:
			n.Flags |= FlagRetryMark // [05] retry mark TODO(question) value
			if len(q.primary) == 1 {
				n.Phase = 0
				n.DynamicGate = 1
				n.Deadline = int32(tick + 30 + randBelow15()) // [04 §3.3] same as code 3
				return
			}
			cleanupNode(n)
			q.primary = q.primary[1:]
			if len(q.primary) > 0 {
				q.primary[0].Flags |= FlagActive
			}
			continue
		default:
			if code > 9 {
				cleanupNode(n)
				q.primary = q.primary[1:]
				if len(q.primary) > 0 {
					q.primary[0].Flags |= FlagActive
				}
				return // [04 §3.3] delegate to expiry helper
			}
		}
	}
}

func (q *Queue) pumpSecondary(u *units.Unit, tick uint32) {
	if q == nil || u == nil {
		return
	}
	for idx := 0; idx < len(q.secondary); {
		n := q.secondary[idx]
		deadlineArrived := n.Deadline != -1 && tick >= uint32(n.Deadline)
		shouldDispatch := n.DynamicGate == 0 || deadlineArrived // [05] literal
		if !shouldDispatch {
			idx++
			continue
		}
		if deadlineArrived {
			n.Deadline = -1 // clear, do not set retry bit per literal
		}
		satisfied := (n.Satisfied | u.Pending) & n.DynamicGate // zero when mask 0
		n.Satisfied &^= satisfied
		u.Pending &^= satisfied
		n.DynamicGate = 0
		handler := DescriptorFor(n.ID).Handler
		if handler == nil {
			recordDiagnostic(fmt.Sprintf("orders: nil handler for secondary %s", DescriptorFor(n.ID).Name))
			return
		}
		code := handler(u, n, satisfied)
		switch code {
		case 0:
			n.Phase = 0
			idx++
		case 1:
			n.Phase++
			idx++
		case 2, 4:
			idx++
		case 3:
			n.DynamicGate = 1
			n.Deadline = int32(tick + 30 + randBelow15())
			idx++
		case 5, 8:
			n.Flags |= FlagTombstone
			cleanupNode(n)
			copy(q.secondary[idx:], q.secondary[idx+1:])
			q.secondary = q.secondary[:len(q.secondary)-1]
		case 6, 7:
			n.Flags |= FlagTombstone
			cleanupNode(n)
			copy(q.secondary[idx:], q.secondary[idx+1:])
			q.secondary = q.secondary[:len(q.secondary)-1]
			return // [05][GAP T3] single remove and return
		case 9:
			n.Flags |= FlagRetryMark
			if idx == len(q.secondary)-1 {
				n.Phase = 0
				n.DynamicGate = 1
				n.Deadline = int32(tick + 30 + randBelow15())
				idx++
			} else {
				n.Flags |= FlagTombstone
				cleanupNode(n)
				copy(q.secondary[idx:], q.secondary[idx+1:])
				q.secondary = q.secondary[:len(q.secondary)-1]
			}
		default:
			if code > 9 {
				n.Flags |= FlagTombstone
				cleanupNode(n)
				copy(q.secondary[idx:], q.secondary[idx+1:])
				q.secondary = q.secondary[:len(q.secondary)-1]
				return
			}
			idx++
		}
	}
}

func QueueForUnit(u *units.Unit) *Queue {
	if u == nil {
		return nil
	}
	if q, ok := u.Orders.(*Queue); ok && q != nil {
		return q
	}
	q := &Queue{}
	u.Orders = q
	return q
}

func BindQueue(u *units.Unit, q *Queue) {
	if u != nil {
		u.Orders = q
	}
}
