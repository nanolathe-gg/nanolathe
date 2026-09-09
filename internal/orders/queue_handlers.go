package orders

import (
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The per-queue handler registration seam.
//
// Retail compiles a handler into every static descriptor, so this file
// describes Nanolathe's build, not retail [04 §3.1]. Some rows' bodies belong
// to a package internal/orders cannot import — the factory and mobile-build
// lifecycle and the unit-reclaim machine in internal/construction, the air
// executors in internal/movement — and the dependency runs the other way. The
// owning subsystem therefore registers the row's handler on the queue, and the
// pump dispatches that registration exactly as it dispatches a descriptor
// handler: it applies the ordinary gate test, clears the record's dynamic gate,
// calls the handler, and applies the result code [04 §3.3].
//
// The pump stays the sole dispatcher, which is the property the registration
// exists to preserve. It supersedes a `Driver` enum on the descriptor whose
// `DriverExternalMachine` value made the pump special-case six rows by table
// lookup; the routing fact is now the owner's own statement, made on the queue
// it owns, and the pump reads one seam instead of two.

// OwnedHandler is the shape a subsystem registers for an order row whose body
// that subsystem, not this package's descriptor table, owns. Its first three
// arguments are the descriptor Handler's, and the satisfied set is an argument
// for the same reason: `GetBuilt`'s phase-2 body reads it to choose its arm —
// the `0x8000` arm holds, the bit-0 arm decays [04 R-ORD-01 §11].
//
// The boolean reports whether the registration advanced the record on this
// visit:
//
//   - `(code, true)` — it ran the row's body, and the pump applies `code`
//     through the ordinary result-code epilogue [04 §3.3].
//   - `(0, false)` — the registering subsystem advances this record from its
//     own per-unit step instead, reading and writing the record's phase,
//     dynamic gate and deadline as its state machine. The pump then writes
//     none of those fields and ends the pass.
//
// The second form is not a courtesy. A result code applied to such a record
// overwrites its owner's own deadline, and a build record parked for 30 to 44
// ticks mid-build is precisely the "the plant will not build another" stall of
// PLAN 17 §0 row 3 (internal/construction gates its own step on
// `node.Deadline >= 0 && tick < node.Deadline`). Such a record is also not
// diagnosed: an owned record is not a missing handler.
type OwnedHandler func(u *units.Unit, n *Node, satisfied uint32, tick uint32) (Code, bool)

// SetOwnedHandler registers handler as the owner of order row id on this queue,
// replacing any previous registration. A zero or out-of-range id is the reject
// sentinel and registers nothing; a nil handler clears the row.
//
// Registration is per queue and carries no process-wide dispatch state
// [P0-I16].
// An owning subsystem installs it wherever it binds a queue, which is the same
// place it installs the session binding.
func (q *Queue) SetOwnedHandler(id ID, handler OwnedHandler) {
	if q == nil || int(id) <= 0 || int(id) >= len(table) {
		return
	}
	if q.ownedHandlers == nil {
		if handler == nil {
			return
		}
		q.ownedHandlers = make([]OwnedHandler, len(table))
	}
	q.ownedHandlers[id] = handler
}

// OwnedHandlerFor returns the handler registered for row id on this queue, or
// nil when the row has none.
func (q *Queue) OwnedHandlerFor(id ID) OwnedHandler {
	if q == nil || q.ownedHandlers == nil || int(id) <= 0 || int(id) >= len(q.ownedHandlers) {
		return nil
	}
	return q.ownedHandlers[id]
}

// getBuiltID is the `GetBuilt` identity, resolved once by buildTable. It cannot
// be a package-level initializer: the table is filled by init, not by a var
// initializer, so a Lookup at variable-initialization time would answer 0.
var getBuiltID ID

// SetGetBuiltHandler binds the construction-owned GetBuilt lifecycle to this
// queue. The queue pump remains the sole dispatcher and therefore preserves
// BeCarried/GetBuilt composition timing [04 R-FAC-02 §4].
//
// It is the named form of SetOwnedHandler for the one row whose owner always
// advances the record from inside the pump visit, so its registration reports
// `true` unconditionally and the pump applies the code it returns.
func (q *Queue) SetGetBuiltHandler(handler func(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code) {
	if q == nil {
		return
	}
	if handler == nil {
		q.SetOwnedHandler(getBuiltID, nil)
		return
	}
	q.SetOwnedHandler(getBuiltID, func(u *units.Unit, n *Node, satisfied uint32, tick uint32) (Code, bool) {
		return handler(u, n, satisfied, tick), true
	})
}

// SetExternallyDrivenHandler is the registration for a row the owning subsystem
// advances from its own per-unit step rather than from the pump. It is a
// convenience over SetOwnedHandler with a handler that always reports it did
// not advance the record; see OwnedHandler's second form for what the pump then
// does, and does not do.
//
// The registration is what makes the claim: a row nobody registers has no owner
// and is dispatched, or diagnosed, on its descriptor alone. When a subsystem
// later moves such a machine into the pump it replaces this call with a
// SetOwnedHandler carrying a real body, and nothing in this package changes.
func (q *Queue) SetExternallyDrivenHandler(id ID) {
	q.SetOwnedHandler(id, externallyDriven)
}

// externallyDriven is the shared body of every "my own per-unit step advances
// this record" registration. It writes nothing, which is the whole contract.
func externallyDriven(*units.Unit, *Node, uint32, uint32) (Code, bool) { return 0, false }

// ApproachWakeGate is the dynamic gate a ground work row's approach phase arms:
// `0xE0`, the three movement outcomes of [04 R-ORD-01 §0] — `0x20` the follower
// reached the goal, `0x40` an empty route was published away from it ("cannot
// get there"), `0x80` a goal object was released. [05 R-WORK-01 §13] states it
// for `MobileBuild` by name: phase 0 arms `0xE0`, so phase 1 is dispatched on
// those three bits and on nothing else.
//
// The gate is the WHOLE delivery mechanism, and the owning subsystem arms it on
// its own record. The pump's step 3 then computes `(record.satisfied |
// unit.pending) & record.gate`, clears the delivered bits out of both
// accumulating words and hands the set to the registered handler as its
// `satisfied` argument [04 §3.3] — the same argument `GetBuilt`'s phase-2 body
// reads. A handler that reports it did not advance the record (OwnedHandler's
// second form) still receives that argument, so a state machine stepped from
// its owner's per-unit pass gets its wake here and nowhere else.
//
// Retired with WU-19-225: a `DeliverApproachWake` helper stood below, repeating
// the pump's own step for a caller because the mobile-build approach parked on
// a private wake bit with a one-tick deadline instead of on retail's `0xE0`.
// The approach arms `0xE0` now, so the detour has no reason to exist.
const ApproachWakeGate uint32 = 0x20 | 0x40 | 0x80
