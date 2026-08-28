// Package orders implements order records and the queue pump [04 §3.2, §3.3][05][GAP T3].
package orders

import (
	"fmt"

	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

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

const (
	MoveNone    uint8 = 0
	MoveEnRoute uint8 = 1
	MoveArrived uint8 = 2
	MoveBlocked uint8 = 3
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
	// Nanolathe path status extension [P0-I03][04 §7][04 §3.5]: published back from
	// the movement scheduler/route lifecycle so the pump and HUD can observe
	// en route / arrived / blocked without re-reading the movement grid.
	MoveState  uint8  // 0 none, 1 en route, 2 arrived, 3 blocked [P0-I03]
	PathStatus uint32 // copy of path.Status (0 success, 0x100 already, 0x200 rejected) [04 §7.2]
	// P0-I05 authoritative construction payloads [05 "Factory production lifecycle"][05 "Construction arithmetic"].
	// BuildDefKey is the canonical catalog key for factory/mobile products; it
	// survives save/load and maps to a stable catalog index in Param1 via
	// Catalog.UnitDefIndex. Using string+index avoids FNV-1a collisions (N04)
	// and provides the established name→index table at load [P0-I05].
	// Factory product: BuildDefKey+Param1(index)+Param2(count)+Phase progress [05].
	// Mobile build: BuildDefKey+Param1(index)+GoalX/Z site + Param3 orientation [05].
	// Assist/repair/reclaim/capture/resurrection: Target + operation-specific progress in Param2/3 [05].
	BuildDefKey string // canonical unit key for build products [P0-I05][02 §5]
}

// Queue holds the two segments [04 §3.2] C5.
type Queue struct {
	primary   []*Node
	secondary []*Node

	// diagnostics records dispatch failures for this unit's queue. It is per
	// queue rather than package-global so two worlds in one process cannot
	// interleave their logs and so a queue's diagnostics die with it
	// [AGENTS.md §Diagnostics].
	diagnostics []string

	// P0-I16: authoritative hooks moved onto the owning queue/service.
	// Hostility and Lookup were package globals; now per-queue to avoid shared mutable.
	Hostility func(actor *units.Unit, target *units.Unit) bool `json:"-"` // per-queue hostility [P0-I16]
	Lookup    func(pool.Handle) *units.Unit                    `json:"-"` // per-queue target lookup [P0-I16]

	// StockpileEconomy is the per-queue economy service for BuildWeapon admission
	// [06 §11.1][P1-09] I16: per-queue to avoid shared mutable global [RS-P0-018][INVARIANTS I1].
	StockpileEconomy interface {
		UnitBuckets(pool.Handle) *[2]economy.Bucket
	} `json:"-"`

	// SecondaryTick is the per-queue tick for BuildWeapon handler deadlines [06 §11.1][RS-P0-018].
	// Was package-global currentSecondaryTick; now per-queue for session isolation [INVARIANTS I1].
	SecondaryTick uint32 `json:"-"`
}

// [P2-03][P1-I09] Queue storage is dynamic, matching retail's heap-linked list
// (NEGATIVE-BOUNDED 3901 boundaries found no cap). The previous 64/32 caps
// were inside stock-reachable behavior: corpus measurement over 275 maps /
// 278 units / 175 campaign missions shows a retail InitialMission can queue
// 105 raw tokens (Silent Slayers carry1: g ms1,g ms2,m...w...) and would
// require >64 primary nodes uncapped; the capped run truncated to 64.
// The secondary max in corpus is 1, but 32 is an arbitrary divergence.
// Retail has no located cap, so Nanolathe uses dynamic slice growth with an
// OOM guard only at a very large threshold far outside stock (OOMGuardQueue
// below, applied at content admission in Push/PushSecondary/CoalesceTail).
//
// There is deliberately NO pump-iteration cap and NO queue-code guard that
// changes behavior mid-walk (ORD-02): retail can wedge on a tight
// script/order loop, and a defensive cap would alter queue state, RNG use,
// and later updates — reproducing the wedge is the contract [04 §3.3][I11].
// Memory safety belongs at admission, not in the running queue.
// Corpus: TestCorpusQueueCaps_Retail (internal/orders/corpus_caps_test.go)
// measures maxPrimary 105+ uncapped and maxSecondary 1.
// TODO(T23): exact allocator zero-fill byte count for order nodes (retail
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// used a different memset length but observable effect is zeroed.
const OOMGuardQueue = 10000

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

// SetPrimary replaces the primary segment [P0-I16][P0-I05].
// Exported accessor replaces reflect/unsafe inspection (ON-02).
func (q *Queue) SetPrimary(primary []*Node) {
	if q == nil {
		return
	}
	q.primary = primary
}

// SetSecondary replaces the secondary segment [P0-I16][04 §3.2].
// Exported accessor replaces reflect/unsafe inspection (ON-02).
func (q *Queue) SetSecondary(secondary []*Node) {
	if q == nil {
		return
	}
	q.secondary = secondary
}

// NewQueueWith constructs a queue with the given segments [04 §3.2] C5.
// Exported constructor for construction service to avoid reflect/unsafe (ON-02).
func NewQueueWith(primary []*Node, secondary []*Node) *Queue {
	return &Queue{primary: primary, secondary: secondary}
}

// Pump is the per-unit order pump, replacing PumpAll-style global sweeps [04 §3.3][05 "Queue pumping and result codes"] (ON-02).
// It operates on a single handle per call to preserve worker/economy bucket isolation:
// stepping builder A does not advance builder B. The existing Queue.Pump remains
// for compatibility but is non-authoritative in new session code (ON-02).
type Pump struct {
	World *units.World // authoritative unit pool (fixed pools, slot 0 null) [01 §6.1][P0-16]
}

// PumpResult reports the outcome of a single-unit pump [04 §3.3][05 "Queue pumping and result codes"] (ON-02).
type PumpResult struct {
	Handle       pool.Handle // requested handle
	Found        bool        // unit existed and was alive
	HadQueue     bool        // queue had at least one node before pumping
	PrimaryLen   int         // primary length after pump
	SecondaryLen int         // secondary length after pump
	Err          error       // explicit error for missing unit or other failure, nil on success
	Diagnostics  []string    // queue diagnostics captured during pump
}

// PumpUnit advances only the named unit's queue/work state [04 §3.3][05 "Queue pumping and result codes"] (ON-02).
// It preserves the primary head-blocking restart-from-head and secondary skip-not-due contracts:
// primary restarts from the head after each dispatch, secondary scans front-to-back skipping not-due.
// Only the named unit's queue advances; other builders are untouched.
func (p *Pump) PumpUnit(handle pool.Handle, tick uint32) PumpResult {
	if p == nil || p.World == nil {
		return PumpResult{Handle: handle, Err: fmt.Errorf("orders: nil pump or world")}
	}
	u := p.World.Unit(handle)
	if u == nil {
		return PumpResult{Handle: handle, Found: false, Err: fmt.Errorf("orders: unit %d not found or dead", handle)}
	}
	q := QueueForUnit(u)
	if q == nil {
		return PumpResult{Handle: handle, Found: true, HadQueue: false, PrimaryLen: 0, SecondaryLen: 0}
	}
	had := q.LenPrimary()+q.LenSecondary() > 0
	// Preserve existing Queue.Pump semantics exactly: primary head-blocking, secondary skip-not-due.
	q.Pump(u, tick)
	prim := q.LenPrimary()
	sec := q.LenSecondary()
	// Order-guard float [07 §8/§9]: nonzero (a clamped 0..1 ratio) while the
	// unit is mid-order, zero at order completion — i.e. when the primary
	// queue empties (completion, cancellation, or expiry all land here). The
	// exact ratio source is unattested; only the 0/nonzero distinction is
	// established, so the ratio is written as 1.0 TODO(question).
	if prim > 0 {
		u.OrderGuard = 1.0
	} else {
		u.OrderGuard = 0.0
	}
	diags := q.Diagnostics()
	return PumpResult{Handle: handle, Found: true, HadQueue: had, PrimaryLen: prim, SecondaryLen: sec, Diagnostics: append([]string(nil), diags...)}
}

func isSecondary(id ID) bool {
	return DescriptorFor(id).StaticGate&0x40000 != 0 // [04 §3.1] rear-segment selection flag
}

// DET-01: session-owned RNG injection — no rng.Global fallback.
var injectedSim *rng.Simulation

// SetSimulationRNG binds the session-owned simulation stream for order jitter draws [01 §7.1][I4].
func SetSimulationRNG(sim *rng.Simulation) { injectedSim = sim }

func simForJitter() *rng.Simulation { return injectedSim }

func randBelow15() uint32 {
	if simForJitter() == nil {
		panic("orders: simulation RNG not injected [DET-01]")
	}
	return simForJitter().Uint32n(15) // gameplay jitter uses simulation stream [I4][04 §3.3]
}

func randBelow30() uint32 {
	if simForJitter() == nil {
		panic("orders: simulation RNG not injected [DET-01]")
	}
	return simForJitter().Uint32n(30) // [R-P0-01] code 9's last re-arm draws RNG(30), a distinct draw site from code 3's RNG(15)
}

// moveGroundHandler implements the Move_Ground-family handler [R-P0-01].
// Phase 0 arms gate 0xE0 and returns 1; phase 1 tests satisfied&0x20 -> 5 else 9.
// The attach check returns 7 while the unit's carrier handle is nonzero [R-P0-01].
func moveGroundHandler(u *units.Unit, n *Node, satisfied uint32) Code {
	if u != nil && u.Attachment.Carrier != 0 {
		return 7 // reject while attached [R-P0-01]
	}
	if n.Phase == 0 {
		n.DynamicGate = 0xE0 // [R-P0-01] phase 0 arms gate 0xE0
		return 1
	}
	if satisfied&0x20 != 0 { // [R-P0-01] combined&0x20 -> ack + return 5
		// TODO(question): acknowledgement emission (kind 6) is not yet wired to
		// presentation; return 5 drives the pump unlink
		return 5
	}
	return 9 // [R-P0-01] drop when further records else 30+RNG30 wait
}

func ensureMoveHandlers() {
	for _, name := range []string{"Move_Ground", "VTOL_Move", "QMove", "Patrol", "QPatrol", "VTOL_Patrol", "RepairPatrol", "VTOL_RepairPatrol"} {
		id := Lookup(name)
		if id != 0 && int(id) < len(table) && table[int(id)].Handler == nil {
			table[int(id)].Handler = moveGroundHandler
		}
	}
}

func init() {
	// Attempt early install; if table not yet built (init order) the lazy ensure will retry on first pump.
	ensureMoveHandlers()
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

// ensureSingleActive keeps the active-marker invariant: exactly one primary
// node carries FlagActive [04 §3.3]. When none does the head takes it; when
// several do (possible while the marker travels) later duplicates are cleared.
// Insertion moves the mark to the inserted node and removal hands it to the
// removed node's successor [04 §3.3][05 "Queue insertion"].
func (q *Queue) ensureSingleActive() {
	if q == nil || len(q.primary) == 0 {
		return
	}
	first := -1
	for i, n := range q.primary {
		if n.Flags&FlagActive != 0 {
			if first == -1 {
				first = i
			} else {
				n.Flags &^= FlagActive
			}
		}
	}
	if first == -1 {
		q.primary[0].Flags |= FlagActive
	}
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

// Diagnostics returns this queue's dispatch failures.
func (q *Queue) Diagnostics() []string {
	if q == nil {
		return nil
	}
	return append([]string(nil), q.diagnostics...)
}

// ClearDiagnostics drops the recorded dispatch failures.
func (q *Queue) ClearDiagnostics() {
	if q != nil {
		q.diagnostics = nil
	}
}

func (q *Queue) recordDiagnostic(msg string) {
	if q == nil {
		return
	}
	//  bound diagnostics to 256 entries to prevent per-tick unbounded growth when descriptors have nil handlers by design.
	const maxDiagnostics = 256
	if len(q.diagnostics) >= maxDiagnostics {
		copy(q.diagnostics, q.diagnostics[1:])
		q.diagnostics = q.diagnostics[:maxDiagnostics-1]
	}
	q.diagnostics = append(q.diagnostics, msg)
}

// cancelAll frees every record on both segments [04 §3.3] result code 7 and
// [05 "Queue pumping and result codes"]. Non-head primary records and every
// secondary record are tombstoned, which is what suppresses their
// weapon-target-clear notification [05 "Queue subtraction"].
func (q *Queue) cancelAll() {
	for i, n := range q.primary {
		if i != 0 {
			n.Flags |= FlagTombstone
		}
		cleanupNode(n)
	}
	for _, n := range q.secondary {
		n.Flags |= FlagTombstone
		cleanupNode(n)
	}
	q.primary = nil
	q.secondary = nil // via the pair-removal helper [05]
}

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
	// [P1-I09] dynamic storage: retail has no cap (NEGATIVE-BOUNDED); previous
	// 64/32 caps were inside stock (corpus max 105 raw tokens -> 64 truncated).
	// Now unbounded with OOM guard far outside stock (10000 >> 105).
	if len(q.primary) >= OOMGuardQueue {
		q.recordDiagnostic(fmt.Sprintf("orders: primary queue OOM guard (%d), dropping %s", len(q.primary), DescriptorFor(id).Name))
		return
	}
	node := newNode(id, n) // [04 §3.3][05 "Queue insertion"] C9
	act := findActive(q)
	if act >= 0 {
		pos := act + 1
		q.primary = append(q.primary, nil)
		copy(q.primary[pos+1:], q.primary[pos:])
		q.primary[pos] = node
		// The marker moves to the inserted node, so repeated interface adds
		// queue FIFO directly behind the running order [04 §3.3][05 "Queue
		// insertion"]; the decompile confirms the mark relocates
		// (notes/construction/04_factory_lifecycle.md).
		q.primary[act].Flags &^= FlagActive
		node.Flags |= FlagActive
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
	// [P1-I09] dynamic: OOM guard far outside stock (maxSecondary 1 in corpus >> 32 old cap not hit but dynamic is correct retail).
	if len(q.secondary) >= OOMGuardQueue {
		q.recordDiagnostic(fmt.Sprintf("orders: secondary queue OOM guard (%d), dropping %s", len(q.secondary), DescriptorFor(id).Name))
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
				// [P2-03] arithmetic overflow: tail Param2 wraps int32 low32 like retail add/sub.
				tail.Param2 += add
				return
			}
		}
		if len(q.secondary) >= OOMGuardQueue {
			q.recordDiagnostic(fmt.Sprintf("orders: secondary coalesce OOM guard (%d), dropping %s", len(q.secondary), DescriptorFor(id).Name))
			return
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
	// tail-only fallback append [04 §3.3][05 "Queue insertion"] [P1-I09] dynamic with OOM guard
	if len(q.primary) >= OOMGuardQueue {
		q.recordDiagnostic(fmt.Sprintf("orders: primary coalesce OOM guard (%d), dropping %s", len(q.primary), DescriptorFor(id).Name))
		return
	}
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
			q.ensureSingleActive() // mark moves to the successor [04 §3.3]
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

// Pump is the legacy per-queue pump for a single unit [04 §3.3][05 "Queue pumping and result codes"].
// Non-authoritative compatibility wrapper (ON-02): new code should use Pump.PumpUnit per handle.
func (q *Queue) Pump(u *units.Unit, tick uint32) {
	if q == nil || u == nil {
		return
	}
	// Order-guard float [07 §8/§9]: nonzero (a clamped 0..1 ratio) while the
	// unit is mid-order, zero at order completion — i.e. when the primary
	// queue empties (completion, cancellation, or expiry all land here). The
	// defer covers every pump exit. The exact ratio source is unattested; only
	// the 0/nonzero distinction is established, so the ratio is written as 1.0
	// TODO(question).
	defer func() {
		if len(q.primary) > 0 {
			u.OrderGuard = 1.0
		} else {
			u.OrderGuard = 0.0
		}
	}()

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
	// No iteration cap here (ORD-02): a handler looping through the continue
	// codes wedges exactly as retail's does [04 §3.3][I11].
	// TODO(question) idle default-op creation when primary empty [05 "Queue pumping and result codes"] step 1: owner player-state settling byte, definition default-idle-op field
	for len(q.primary) > 0 {
		n := q.primary[0] // head-driven; the walk restarts here after every dispatch [04 §3.3]
		if n.Deadline != -1 && tick >= uint32(n.Deadline) {
			n.Deadline = -1
			n.Satisfied |= 1 // [04 §3.3]
		}
		ensureMoveHandlers()
		ensureTransportHandlers()
		// For transport and other wired handlers, the initial static gate (0x200/0x400 etc) is satisfied by construction (target/goal present) [04 §3.1] TODO(question) exact gate semantics.
		// Clear it for phase 0 so the first dispatch is not blocked, mirroring the move arrival handle's clearing [R-P0-01].
		// BeCarried is intentionally left blocked (gate 0x24) to keep cargo stalled while carried without RNG [04 §3.1] TODO(question).
		if n.Phase == 0 && n.DynamicGate != 0 {
			if h := DescriptorFor(n.ID).Handler; h != nil {
				name := DescriptorFor(n.ID).Name
				if name != "BeCarried" && n.DynamicGate == DescriptorFor(n.ID).StaticGate {
					n.DynamicGate = 0
					n.Satisfied = 0
					n.Deadline = -1
				}
			}
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
			// [P0-I03] path-backed move orders have no dedicated handler yet; the
			// movement scheduler owns the route lifecycle [04 §7]. Synthesize a
			// wait so the pump does not spin and the handler is re-dispatched
			// after 30+rand15 [04 §3.3] C3, while the loop's path-submit and
			// movement-integrate drive the route [P0-I03].
			name := DescriptorFor(n.ID).Name
			if name == "Move_Ground" || name == "VTOL_Move" || name == "QMove" || name == "Patrol" || name == "QPatrol" || name == "VTOL_Patrol" || name == "RepairPatrol" || name == "VTOL_RepairPatrol" {
				n.DynamicGate = 1
				n.Deadline = int32(tick + 30 + randBelow15()) // [04 §3.3][I4]
				n.MoveState = MoveEnRoute
				return
			}
			q.recordDiagnostic(fmt.Sprintf("orders: nil handler for %s", DescriptorFor(n.ID).Name)) // [AGENTS.md §Diagnostics] never spin
			return
		}
		code := handler(u, n, satisfied)
		if !q.applyPrimaryResultCode(n, code, tick) {
			return
		}
	}
}

// applyPrimaryResultCode is the PRIMARY result-code table [04 §3.3] C7: it
// maps the handler's return code to queue effects for the head record n.
// Deliberately distinct from applySecondaryResultCode — the segments share
// the code values but not the effects, and re-merging them reintroduces the
// ORD-03 mismatches (secondary code 9 would re-arm, secondary 6/7 would
// tail-yield or cancel-all). Primary specifics here: code 6 rotates to the
// segment tail, code 7 is the exclusive whole-queue cancel, code 9's
// last-record arm re-arms with RNG(30) [R-P0-01].
// Returns false when the walk stops for this pump.
func (q *Queue) applyPrimaryResultCode(n *Node, code Code, tick uint32) bool {
	switch code {
	case 0:
		n.Phase = 0 // [04 §3.3]
	case 1:
		n.Phase++ // [04 §3.3]
	case 2, 4:
		// [04 §3.3] continue walking unchanged
	case 3:
		n.DynamicGate = 1                             // [04 §3.3] lowest gate bit
		n.Deadline = int32(tick + 30 + randBelow15()) // [04 §3.3][I4] wait 30..44; the only arm drawing RNG(15)
		return false
	case 5, 8:
		cleanupNode(n) // [05 "Queue subtraction"]
		q.primary = q.primary[1:]
		q.ensureSingleActive() // mark moves to the successor [04 §3.3]
	case 6:
		q.primary = q.primary[1:]
		n.Flags &^= FlagActive
		q.primary = append(q.primary, n) // move to segment tail and continue [04 §3.3]
		q.ensureSingleActive()           // exactly one marker remains
	case 7:
		q.cancelAll() // [04 §3.3] free every record on both segments and return; whole-queue cancel is exclusively primary code 7
		return false
	case 9:
		n.Flags |= FlagRetryMark // [05] completion flag; TODO(question) actual bit not located [04 §3.3] code 9
		if len(q.primary) == 1 {
			// [R-P0-01][04 §3.3] last record re-arms: phase reset, wait
			// 30..59 — the distinct RNG(30) arm, not code 3's RNG(15).
			n.Phase = 0
			n.DynamicGate = 1
			n.Deadline = int32(tick + 30 + randBelow30())
			return false
		}
		cleanupNode(n) // [04 §3.3] otherwise unlink and free
		q.primary = q.primary[1:]
		q.ensureSingleActive() // mark moves to the successor [04 §3.3]
	default:
		if code > 9 {
			// [04 §3.3] above 9: single-node expiry helper — unlink, clean,
			// free, and return; no draw, no whole-queue cancel [P0-08].
			// Whole-queue cancel is exclusively code 7 [P0-08] A09.
			cleanupNode(n)
			q.primary = q.primary[1:]
			q.ensureSingleActive()
			return false
		}
		return false
	}
	return true
}

func (q *Queue) pumpSecondary(u *units.Unit, tick uint32) {
	if q == nil || u == nil {
		return
	}
	for idx := 0; idx < len(q.secondary); {
		n := q.secondary[idx]
		// Fresh BuildWeapon nodes are created with DynamicGate = StaticGate
		// (0xc0140) and Deadline -1 via newNode. For secondary, DynamicGate 0
		// means ready [05] literal, so fresh nodes would never dispatch.
		// Normalize fresh BuildWeapon nodes to ready on first tick [06 §11.1] C29.
		if n.Deadline == -1 && n.DynamicGate != 0 && DescriptorFor(n.ID).Name == "BuildWeapon" {
			n.DynamicGate = 0
		}
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
		// Publish tick for BuildWeapon stockpile handler's retry deadlines
		// [06 §11.1] C29 (5/10/300) without changing Handler signature [RS-P0-018].
		q.SecondaryTick = tick
		setSecondaryTick(tick)
		handler := DescriptorFor(n.ID).Handler
		if handler == nil {
			q.recordDiagnostic(fmt.Sprintf("orders: nil handler for secondary %s", DescriptorFor(n.ID).Name))
			return
		}
		code := handler(u, n, satisfied)
		advance, walking := q.applySecondaryResultCode(n, code, tick)
		if !walking {
			return // codes 6 and 7: remove the single record and return [04 §3.3] C8
		}
		idx += advance
	}
}

// applySecondaryResultCode is the SECONDARY result-code table [04 §3.3] C8.
// Deliberately distinct from applyPrimaryResultCode (ORD-03): codes 6 and 7
// remove the single record and return — no tail-yield, no cancel-all — while
// codes 5, 8, 9 and above 9 are plain unlink-and-free removals that continue
// the front-to-back walk. Secondary code 9 sets the completion flag and then
// plainly unlinks and frees with NO re-arm and NO draw, regardless of whether
// the record is last or first [04 §3.3] "Audit note — completion-wait
// ranges"; the above-9 expiry delegate never draws either.
// Returns the index advance (0 when the record was removed) and whether the
// walk continues.
func (q *Queue) applySecondaryResultCode(n *Node, code Code, tick uint32) (advance int, walking bool) {
	switch code {
	case 0:
		n.Phase = 0 // [04 §3.3]
		return 1, true
	case 1:
		n.Phase++ // [04 §3.3]
		return 1, true
	case 2, 4:
		return 1, true // [04 §3.3] continue unchanged
	case 3:
		n.DynamicGate = 1                             // [04 §3.3] lowest gate bit
		n.Deadline = int32(tick + 30 + randBelow15()) // [04 §3.3][I4] wait 30..44; the only arm drawing RNG(15)
		return 1, true
	case 5, 8:
		q.removeSecondaryRecord(n) // [04 §3.3] C8 plain unlink+free, walk continues
		return 0, true
	case 6:
		q.removeSecondaryRecord(n) // [04 §3.3] C8 remove the single record and return; no tail-yield
		return 0, false
	case 7:
		q.removeSecondaryRecord(n) // [04 §3.3] C8 remove the single record and return; no cancel-all
		return 0, false
	case 9:
		n.Flags |= FlagRetryMark // [05] completion flag; TODO(question) actual bit not located [04 §3.3] code 9
		// [04 §3.3] plain unlink+free — no re-arm, no draw, regardless of
		// last/first position (the primary-only last-record re-arm [R-P0-01]
		// does not apply to the secondary pump).
		q.removeSecondaryRecord(n)
		return 0, true
	default:
		if code > 9 {
			// [04 §3.3] C8 expiry delegate: plain unlink+free, no draw, and
			// the walk continues like the other plain removals.
			q.removeSecondaryRecord(n)
			return 0, true
		}
		return 0, false
	}
}

// removeSecondaryRecord unlinks and frees one secondary record [04 §3.3]
// [05 "Queue subtraction"]: the record is always tombstoned because the
// tombstone test compares against the front anchor regardless of segment, so
// BuildWeapon/SelfDestruct removals never emit the weapon-target-clear
// notification.
func (q *Queue) removeSecondaryRecord(n *Node) {
	n.Flags |= FlagTombstone
	cleanupNode(n)
	for i, m := range q.secondary {
		if m == n {
			copy(q.secondary[i:], q.secondary[i+1:])
			q.secondary = q.secondary[:len(q.secondary)-1]
			return
		}
	}
}

func (q *Queue) RemoveHead() *Node {
	if q == nil || len(q.primary) == 0 {
		return nil
	}
	n := q.primary[0]
	cleanupNode(n)
	q.primary = q.primary[1:]
	q.ensureSingleActive()
	if n != nil {
		n.MoveState = MoveArrived
	}
	return n
}

func (q *Queue) Head() *Node {
	if q == nil || len(q.primary) == 0 {
		return nil
	}
	return q.primary[0]
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

// RemovePrimaryNode removes one primary node in place, preserving queue
// identity and every queue-owned service binding (Hostility, Lookup,
// StockpileEconomy, SecondaryTick, diagnostics). Callers that rebuilt the
// segment into a fresh Queue silently dropped those hooks, so successor
// orders lost target lookup and stockpile admission after a construction
// removal.
//
// Removal follows the established subtraction order [04 §3.3][05 "Queue
// subtraction"]: the node is marked per tombstone rules, cleanup runs exactly
// once, the segment is spliced, and the active marker is handed to the
// successor.
//
// tombstone selects the marker applied before cleanup. Retail exempts the
// primary head from the tombstone [04 §3.3]; callers that must preserve an
// older unconditional marking pass true explicitly.
//
// The node is matched by pointer identity; when that fails the head is
// accepted if it carries the same order ID and first parameter. Returns the
// removed node, or nil when nothing matched.
func (q *Queue) RemovePrimaryNode(node *Node, tombstone bool) *Node {
	if q == nil || len(q.primary) == 0 {
		return nil
	}
	idx := -1
	for i, n := range q.primary {
		if n == node {
			idx = i
			break
		}
	}
	if idx == -1 {
		head := q.primary[0]
		if node != nil && head != nil && head.ID == node.ID && head.Param1 == node.Param1 {
			idx = 0
		} else {
			return nil
		}
	}
	removed := q.primary[idx]
	if removed != nil {
		if tombstone || idx != 0 {
			removed.Flags |= FlagTombstone
		}
		removed.Flags &^= FlagActive
		cleanupNode(removed)
	}
	q.primary = append(q.primary[:idx], q.primary[idx+1:]...)
	q.ensureSingleActive()
	return removed
}

// CancelAll is the exported entry to result code 7's whole-queue cancel
// [04 §3.3][05 "Queue pumping and result codes"]. It preserves queue identity
// and every queue-owned service binding; callers must never express a cancel
// by rebinding a fresh Queue to the unit.
func (q *Queue) CancelAll() {
	if q == nil {
		return
	}
	q.cancelAll()
}
