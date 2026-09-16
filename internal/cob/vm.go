// The COB VM [04 §4.2] [04 §4.3] [04 §4.6] [fmt cob].
//
// Contracts C10–C14 plus the drain/signal piece surface are owned here.
// Engine ports and callbacks (C15–C19) live in ports.go (WU-06-7); this file
// defines only the minimal unexported hook Drain needs to call out.
// Established seams include the empty unit adapters for the legacy effect and
// shadow writes, the four-word bounds check around the reserved pop form, and
// deterministic Go termination for malformed stack/piece access. The unassigned
// Killed query variant remains an explicit zero-valued divergence in bridge.go.

package cob

import (
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

// dispatchMask is the dispatched-key mask [04 §4.3] C10.
//
// Note on bit numbering: the plan and [04 §4.3] describe the dispatch as
// "bit 28 always set; bits 16–23 select the operation; low twelve ignored",
// but the literal mask given is 0x100FF000.  0x100FF000 = (1<<28) | (0xFF<<12)
// selects bit 28 plus bits 12–19 (the hex digits "FF" at 0xFF000) and zeroes
// bits 0–11.  Every retail opcode is of the form 0x100XX000 where XX occupies
// exactly those eight bits, so the mask and the eight-bit range agree and the
// doc's "16–23" label is a numbering shift.  Low three bits (0–2) are the
// push/pop addressing mode, examined after dispatch [04 §4.3] C10, F.
const dispatchMask uint32 = 0x100FF000 // [04 §4.3] C10

// Thread status encodings [04 §4.2] [04 §4.3].
// Retail stores a status word with the state in the high byte and a sub-state
// nibble for the waiting family; we keep the logical enumeration as named
// fields (I13) while preserving the distinct states.
const (
	ThreadIdle     = 0 // [04 §4.2] idle / free slot
	ThreadRunning  = 1 // [04 §4.2] running
	ThreadSleeping = 2 // [04 §4.6] sleep timer guard [04 §4.2] sleeping
	ThreadWaitTurn = 3 // [04 §4.2] waiting for turn
	ThreadWaitMove = 4 // [04 §4.2] waiting for move
	ThreadWaitCall = 5 // [04 §4.2] waiting for called script (blocked)
)

// Thread is one of the eight 164-byte retail records [01 §6.1] C13, [04 §4.2] (I13).
// Go stores named fields; byte size is not reproduced, but capacities and
// scan order are. The wire image carries 32 physical window words. Nanolathe
// stops a malformed script before a push or local allocation would escape that
// storage; retail's behavior beyond its physical record is unknown. Bad piece
// index (<0 or >=pieceCount) also kills
// Corrupt COB/save: header offset bounds-checked vs retail no-check —
// Nanolathe rejects with diagnostic and fallback empty VM (I11 divergence)
// [P2-03]. Cycle detection via visited set, queue overflow via diagnostic drop
// and iteration limit 200, divide-by-zero via thread kill (not process #DE)
// [P2-03][04 §4.3] C14 I11.
type Thread struct {
	Status     int       // one of Thread* constants [04 §4.2]
	PC         int       // word index into Program.Code [04 §4.3] C12
	Stack      [32]int32 // complete physical argument/local/expression window
	SP         int       // logical stack count within Stack
	Sleep      int32
	WaitPiece  int
	WaitAxis   int
	WaitThread int   // -1 leaked wait [04 §4.3] C14 [P1-11] call-script wedges -1
	SignalMask int32 // per-thread signal mask [04 §4.3]
}

const threadWindowWords = len((Thread{}).Stack)

// Port is an engine port identifier 1..20 [04 §4.4] C15.
// Binding surface lives in WU-06-7; this type is defined here so vm.go can
// name the hook [PLAN_06 Public API].
type Port int

// VM is a per-unit COB instance [04 §4.1] [04 §4.2] [PLAN_06 Public API].
// Threads[8] and Pieces are the published state; remaining fields are the
// execution and animation state this package owns. Other packages compile
// against the two exported fields only.
type VM struct {
	Threads [8]Thread
	Pieces  []model.PieceState // len == len(Program.Pieces) [04 §4.1]

	prog          *Program
	statics       []int32
	anims         []pieceAnim // per-piece per-axis animation state [04 §4.6]
	pieceBusy     []bool      // per-piece animation dirty/busy reduction [04 §4.6]
	pieceFlags    []uint8     // per-piece draw/cache/shade/shadow flags [04 §4.3] — fallback for fixture VMs; production flags live on units.Unit.RenderPieceFlags [04 §"Piece flag polarity"]
	simRng        *rng.Simulation
	portFuncs     map[Port]func(args []int32) int32   // legacy combined hook; nil means default 0 [04 §4.4]
	portBindings  map[Port]PortBinding                // explicit read/write hooks [04 R-COB-03 §1]
	sfxSink       SFXSink                             // presentation-only sink for emit-sfx [GAP T15] C19; nil discards
	sfxVisible    func(piece int, sfxType int32) bool // visibility gate for emit-sfx [GAP T15] C19; nil fails closed
	explosionSink ExplosionSink                       // immediate session arena admission [04 R-COB-04 §1]; nil is pending U13
	diagnostics   []string                            // [P2-03] fallback diagnostics (divide, overflow, corrupt) not fatal

	// The two transport query opcodes read the owning unit's cargo linkage
	// [04 §4.4][04 R-COB-03 §5]. They are not engine ports and carry no port
	// identifier, so they get their own binding rather than a portFuncs entry.
	// An unbound VM owns no unit, and its answers are the established
	// empty-list and not-carried ones.
	cargoContains   func(id int32) bool // membership in this unit's own cargo list
	carrierIdentity func() int32        // this unit's carrier identifier, 0 when not carried
	transportAttach func(cargo, piece, mode int32)
	transportDrop   func(cargo int32)

	// scriptTouched raises the owning unit's SCRIPT-TOUCHED MARKER: bit 2 of
	// the unit's 16-bit order-event word, which is order gate bit 0x4
	// [04 R-COB-06]. Every arm of the engine-write opcode dispatch — all six
	// write arms AND the fall-through an identifier with no write arm takes,
	// including an identifier outside 1..20 — ORs that bit in addition to its
	// own effect. The marker carries no value; it means only "this unit's
	// script executed an engine write", and it is the sole producer of the bit
	// the INBUILDSTANCE and transport BUSY waits park on with no deadline.
	//
	// The VM owns no unit pointer, so the raise is a closure installed by
	// internal/units. A VM with no owning unit (fixtures, the generic
	// asset-binding seam) leaves it nil and the marker has nowhere to go,
	// which is all an ownerless VM can do.
	scriptTouched func()

	lastStarted     int      // last thread allocated by Start/StartByName, -1 if none [06 §3.3] ON-04 Aim dispatch
	lastQueryThread int      // last thread allocated by CallQuery, -1 if none [04 §4.2]
	lastReturnValue [8]int32 // last explicit return value per thread [04 §5.3] ON-04
	lastReturnValid [8]bool  // true if lastReturnValue holds an explicit return not yet consumed ON-04

	// A thread slot is reused the instant it goes idle, so a slot index alone
	// cannot name one execution of one callback: a signal can free slot k and a
	// child start can take slot k back inside the same drain [04 §4.2][04 §4.3].
	// threadIdentity stamps every allocation with a value that is never issued
	// twice, so a holder of a stale identity is provably not the current
	// occupant. Identity 0 is the never-allocated sentinel.
	//
	// This is not the pool generation tag I5 forbids. Allocation stays
	// lowest-free with immediate reuse and no tag on the slot index; the slot
	// index remains the only thing thread state, the save image, and every
	// consumer address, and stale 16-bit unit/projectile handles still alias
	// after reuse exactly as before. The identity is private to this VM and
	// answers the one question the slot index cannot: is this still the same
	// execution. Retail answers it by keeping the completion receiver inside the
	// thread record — which is why the save image declines to restore that word.
	threadIdentity     [8]uint64
	nextIdentity       uint64
	lastReturnIdentity [8]uint64 // the allocation that produced lastReturnValue [04 §5.3]

	// onReturn is the thread record's completion receiver. Retail keeps that
	// receiver in the thread record; a new root or child thread begins with
	// none; the explicit-return opcode is the only thing that invokes it; and
	// signal or invalid-opcode termination never does [04 §4.2][04 §4.3]
	// [04 §5.3]. Owning it here rather than in a slot-indexed table outside the
	// VM is what makes those three rules survive a reallocation.
	onReturn [8]func(int32)

	// tickDenom is latched once at VM construction from the engine's fixed
	// 30-tick configuration and is immutable afterwards; it is not re-derived
	// per tick from wall-clock or game-speed budget [04 §4.6]. All four
	// speed/deceleration divides and the sleep conversion read it.
	tickDenom int32

	DrainCalls        int    // count of Drain invocations for RS-08 one-drain invariant [04 §4.2][GAP T15]
	activeThreadCount uint32 // authoritative active-thread count [04 §4.2]
	dirty             bool   // global animation dirty flag [04 §4.6]
	// These presentation revisions preserve both invalidation causes. The
	// all-event revision changes for validity clears and image discards; the
	// validity revision changes only for full cached-body invalidation. Neither
	// is a saved retail counter [03 R-COMP-01 §4][03 R-REN-03A §4].
	cacheRevision         uint64
	cacheValidityRevision uint64

	// Render-piece flag delegation [04 §"Piece flag polarity"] [R-COB-01 §1].
	// Production units own RenderPieceFlags on units.Unit; the VM delegates its
	// show/hide, cache/dont-cache, shade/dont-shade writes there via the bridge.
	// Fixture VMs with no external binding keep their own pieceFlags.
	renderFlagsBound bool
	renderFlags      []uint8
	renderFlagGet    func() []uint8
	renderFlagSet    func(piece int, mask uint8, set bool) bool
}

// pieceAnim holds the per-piece per-axis interpolation lanes [04 §4.6].
type pieceAnim struct {
	axes [3]axisAnim
}

// axisAnim holds one axis of a piece [04 §4.6].
type axisAnim struct {
	moveTarget int32
	moveSpeed  int32
	moveBusy   bool
	turnTarget uint16
	turnSpeed  int32 // current rotation speed, shared by positional turns and spins [04 §4.6]
	turnBusy   bool
	spinTarget int32
	spinAccel  int32
	spinActive bool
}

// PortBinding keeps a port read distinct from its write arm. Both engine-read
// opcodes pass exactly four authored argument cells to Read; only the engine
// write opcode calls Write. This prevents the read's zero fillers from being
// mistaken for a write value [04 R-COB-03 §1][04 R-COB-03 §4].
type PortBinding struct {
	Read  func(args [4]int32) int32
	Write func(value int32)
}

// NewVM creates a VM bound to prog. Pieces length matches prog.Pieces and
// Nanolathe zero-initializes statics for deterministic ownership; retail's
// allocator leaves their initial bytes unspecified [R-COB-04 §7]. prog may be
// nil for fixture VMs that set program later via SetProgram.
func NewVM(prog *Program) *VM {
	v := &VM{}
	if err := v.SetProgramChecked(prog); err != nil {
		v.diagnostics = append(v.diagnostics, err.Error())
	}
	return v
}

// SetProgram binds prog to v, reallocating piece and static storage.
// Callers that construct VM as a literal may call this after.
//
// Construction contract [R-COB-01 §1]: the bind clears only each thread's
// status word and the instance's active-thread count (derived here from the
// status words) and latches the tick denominator once. Nothing else in the
// eight thread records is initialized — PC, depth, timer, signal mask, wait
// words and the 32 physical window words of an unallocated slot keep whatever the
// instance's memory held (Go's zero values on a fresh instance; a previous
// tenant's bytes on a reused one). Every field that matters is re-seeded at
// thread allocation or thread start, so construction-time state is
// unobservable except through the physical window words.
func (v *VM) SetProgram(prog *Program) {
	if err := v.SetProgramChecked(prog); err != nil {
		v.diagnostics = append(v.diagnostics, err.Error())
	}
}

// SetProgramChecked validates external program metadata before allocating the
// mutable statics and piece lanes. The compatibility SetProgram wrapper keeps
// its no-error surface for existing callers.
func (v *VM) SetProgramChecked(prog *Program) error {
	if err := ValidateProgram(prog); err != nil {
		return err
	}
	if v.prog != nil || v.cacheRevision != 0 {
		// Replacing a program retires its old presentation image as well as
		// its script state. Initial construction has no image to retire.
		v.invalidateCacheValidity()
	}
	v.prog = prog
	if prog == nil {
		v.statics = nil
		v.Pieces = nil
		v.anims = nil
		v.pieceBusy = nil
		v.activeThreadCount = 0
		v.dirty = false
		v.pieceFlags = nil
		v.renderFlags = nil
		v.renderFlagsBound = false
		v.renderFlagGet = nil
		v.renderFlagSet = nil
		return nil
	}
	// Script statics: retail's bind performs no zeroing pass; the initial bytes
	// come from its allocator and are unspecified [R-COB-04 §7]. Nanolathe
	// zeroes them for deterministic state, an explicit implementation
	// divergence rather than a retail default.
	v.statics = make([]int32, prog.Statics)
	v.Pieces = make([]model.PieceState, len(prog.Pieces)) // zero-filled by the bind [R-COB-01 §1]
	v.anims = make([]pieceAnim, len(prog.Pieces))         // piece animation words all zero [R-COB-01 §1]
	v.pieceBusy = make([]bool, len(prog.Pieces))          // piece dirty/busy flags all zero [04 §4.6]
	// The retail fill pass zero-fills the allocation and then sets bit 1
	// (cache) and bit 2 (shade) unconditionally, setting bit 0 (draw) only when
	// the piece's model object has at least three vertices [04 §4.3]. This
	// layer has no model link — the geometry-driven part of the walk belongs to
	// the unit-creation binding, which owns it [R-COB-01 §1] — so it writes the
	// two unconditional bits and leaves bit 0 to whoever knows the geometry.
	//
	// It used to write 0x07, bit 0 included, on the grounds that a fixture VM
	// with everything hidden made early snapshots fall back to squares. That
	// made this array a second, more permissive default than the one retail's
	// fill pass produces, and it is only ever read by a VM with no external
	// binding: production installs the unit-owned table before Create runs, so
	// getRenderFlags and setRenderFlag never reach pieceFlags there
	// [04 §"Piece flag polarity"].
	v.pieceFlags = make([]uint8, len(prog.Pieces))
	for i := range v.pieceFlags {
		v.pieceFlags[i] = 0x06 // cache|shade, the fill pass's unconditional bits [04 §4.3]
	}
	// Clear any prior render-piece delegation — the new program re-binds flags
	// from the unit's model-driven table via BindRenderFlags [04 §"Piece flag polarity"].
	v.renderFlags = nil
	v.renderFlagsBound = false
	v.renderFlagGet = nil
	v.renderFlagSet = nil
	// Construction clears only the status words; every other thread field
	// keeps its prior content [R-COB-01 §1].
	for i := range v.Threads {
		v.Threads[i].Status = ThreadIdle
	}
	// Latch the tick denominator once from the fixed 30-tick configuration
	// [04 §4.6]; immutable after construction.
	v.tickDenom = 30
	v.lastStarted = -1
	v.lastQueryThread = -1
	v.activeThreadCount = 0
	v.dirty = false
	for i := range v.lastReturnValid {
		v.lastReturnValid[i] = false
		v.lastReturnValue[i] = 0
		v.lastReturnIdentity[i] = 0
		// Rebinding a program retires every allocation the old program made:
		// the identities go back to the never-allocated sentinel so no holder
		// can match one, and the receivers go with them [04 §4.2].
		v.threadIdentity[i] = 0
		v.onReturn[i] = nil
	}
	return nil
}

// Program returns the bound program, or nil if none [04 §4.1].
// Exported accessor replaces reflect/unsafe inspection in construction [I13].
func (v *VM) Program() *Program {
	if v == nil {
		return nil
	}
	return v.prog
}

// ActiveThreadCount returns the VM's authoritative active-thread count.
func (v *VM) ActiveThreadCount() uint32 {
	if v == nil {
		return 0
	}
	return v.activeThreadCount
}

// ScriptDirty reports the global piece-animation dirty flag [04 §4.6].
func (v *VM) ScriptDirty() bool {
	return v != nil && v.dirty
}

// CacheRevision is the read-only presentation invalidation revision for this
// VM's cached body.  It is not part of retail script persistence; publication
// copies it to the committed UnitView [03 R-COMP-01 §4].
func (v *VM) CacheRevision() uint64 {
	if v == nil {
		return 0
	}
	return v.cacheRevision
}

// CacheValidityRevision identifies cached-body validity clears separately
// from image-reference discards. A mobile no-key subject can draw directly
// after the latter while the former requires a rebuild [03 R-REN-03A §4].
func (v *VM) CacheValidityRevision() uint64 {
	if v == nil {
		return 0
	}
	return v.cacheValidityRevision
}

// BindPort registers a port handler for WU-06-7 [PLAN_06 Public API] [04 §4.4].
// Drain consults this map; when no handler is bound the read returns 0 and
// writes only set the script-touched marker (here a no-op) [04 §4.4].
func (v *VM) BindPort(p Port, fn func(args []int32) int32) {
	if v.portFuncs == nil {
		v.portFuncs = make(map[Port]func(args []int32) int32)
	}
	v.portFuncs[p] = fn
}

// BindPortBinding registers an explicit engine-port read/write pair. It takes
// precedence over the legacy BindPort hook; BindPort remains for existing
// query-only adapters and test seams.
func (v *VM) BindPortBinding(p Port, binding PortBinding) {
	if v == nil {
		return
	}
	if v.portBindings == nil {
		v.portBindings = make(map[Port]PortBinding)
	}
	v.portBindings[p] = binding
}

func (v *VM) readPort(id int32, args [4]int32) int32 {
	if binding, ok := v.portBindings[Port(id)]; ok && binding.Read != nil {
		return binding.Read(args)
	}
	if fn, ok := v.portFuncs[Port(id)]; ok && fn != nil {
		return fn([]int32{id, args[0], args[1], args[2], args[3]})
	}
	return v.readPortDefault(id, args[:])
}

func (v *VM) writePort(id, value int32) {
	if binding, ok := v.portBindings[Port(id)]; ok && binding.Write != nil {
		binding.Write(value)
		return
	}
	if fn, ok := v.portFuncs[Port(id)]; ok && fn != nil {
		_ = fn([]int32{id, value})
		return
	}
	v.writePortDefault(id, value)
}

// BindScriptTouched attaches the owning unit's script-touched marker raise
// [04 R-COB-06]. The engine-write opcode calls it on EVERY execution — before
// the port's own effect, and regardless of whether the popped identifier has a
// write arm or is even inside 1..20 — because that is what retail's dispatch
// does: all six write arms and the fall-through share the one OR of bit 2 into
// the unit's order-event word. Binding it to a single port instead (port 5,
// say) would be a narrower rule than retail's and would deadlock the transport
// BUSY handshake, whose wake comes from port 6 [04 R-AIR-01 §9].
//
// Passing nil detaches the raise.
func (v *VM) BindScriptTouched(raise func()) {
	if v == nil {
		return
	}
	v.scriptTouched = raise
}

// raiseScriptTouched fires the marker for one engine write [04 R-COB-06].
// An ownerless VM has no word to write and the raise is dropped.
func (v *VM) raiseScriptTouched() {
	if v == nil || v.scriptTouched == nil {
		return
	}
	v.scriptTouched()
}

// BindTransportQueries attaches the owning unit's cargo linkage to the two
// transport query opcodes [04 §4.4][04 R-COB-03 §5]. inCargo answers whether a
// unit identifier is in this unit's own cargo list; carrier returns the
// identifier of the unit carrying this one, or zero when it is not carried.
// Both are read at query time, so an attach or drop between queries is visible
// without rebinding. Either may be nil, in which case the corresponding query
// keeps the established empty-list / not-carried answer.
func (v *VM) BindTransportQueries(inCargo func(id int32) bool, carrier func() int32) {
	if v == nil {
		return
	}
	v.cargoContains = inCargo
	v.carrierIdentity = carrier
}

// BindTransportMutations installs the executing unit's attach and drop
// adapters. Attach receives the COB stack's cargo identity, piece, and third
// operand; drop receives its cargo identity. The VM owns neither unit records
// nor occupancy, so a session binding must perform the shared transport commit
// [04 R-COB-03 §5][04 R-UNIT-06 §3]. Nil callbacks make the opcodes no-ops.
func (v *VM) BindTransportMutations(attach func(cargo, piece, mode int32), drop func(cargo int32)) {
	if v == nil {
		return
	}
	v.transportAttach = attach
	v.transportDrop = drop
}

// BindRenderFlags attaches the unit-owned render-piece record [04 §"Piece flag polarity"].
// When bound, show/hide (bit 0), cache/dont-cache (bit 1), shade/dont-shade (bit 2) write
// into the unit's storage via the bridge, not the VM-local array [R-COB-01 §1].
// The slice is shared memory; writes via the VM affect the unit and vice versa.
func (v *VM) BindRenderFlags(flags []uint8) {
	if v == nil {
		return
	}
	v.renderFlags = flags
	v.renderFlagsBound = true
	v.renderFlagGet = nil
	v.renderFlagSet = nil
}

// BindRenderFlagHandlers installs a handler pair for the unit's render-piece record [04 §"Piece flag polarity"].
// Get returns the current flags copy; set toggles exactly one mask bit (0x01/0x02/0x04) on the
// unit's record. When set, these handlers supersede the direct slice binding [R-COB-01 §1].
// This is the bridge path: internal/cob/bridge.go installs closures capturing units.Unit.
func (v *VM) BindRenderFlagHandlers(get func() []uint8, set func(piece int, mask uint8, set bool) bool) {
	if v == nil {
		return
	}
	v.renderFlagGet = get
	v.renderFlagSet = set
	if get != nil || set != nil {
		v.renderFlagsBound = true
	}
}

// UnbindRenderFlags clears the external render-piece binding (used in tests).
func (v *VM) UnbindRenderFlags() {
	if v == nil {
		return
	}
	v.renderFlags = nil
	v.renderFlagsBound = false
	v.renderFlagGet = nil
	v.renderFlagSet = nil
}

// renderPieceFlags returns the active flag storage: the unit-owned record when bound, otherwise the VM-local fallback [04 §"Piece flag polarity"].
func (v *VM) renderPieceFlags() []uint8 {
	if v == nil {
		return nil
	}
	if v.renderFlagGet != nil {
		return v.renderFlagGet()
	}
	if v.renderFlagsBound {
		return v.renderFlags
	}
	return v.pieceFlags
}

// setRenderFlag writes one piece flag bit via the active storage [04 §"Piece flag polarity"].
// Returns false if piece out of range or mask not one of 0x01/0x02/0x04.
func (v *VM) setRenderFlag(piece int, mask uint8, set bool) bool {
	if v == nil {
		return false
	}
	flags := v.renderPieceFlags()
	if piece < 0 || piece >= len(flags) || (mask != 0x01 && mask != 0x02 && mask != 0x04) {
		return false
	}
	before := flags[piece]
	if v.renderFlagSet != nil {
		if !v.renderFlagSet(piece, mask, set) {
			return false
		}
	} else {
		if v.renderFlagsBound {
			flags = v.renderFlags
		} else {
			flags = v.pieceFlags
		}
		if set {
			flags[piece] |= mask // lower opcode sets [04 §"Piece flag polarity"]
		} else {
			flags[piece] &^= mask // higher clears
		}
	}

	// Cache and shade setters discard the cached image on every valid call.
	// A draw-bit write clears validity only when it changed and this piece is cached
	// [03 R-COMP-01 §4].  The externally owned unit record remains the flag
	// authority; this is only the presentation invalidation publication seam.
	if mask == 0x02 || mask == 0x04 {
		v.invalidateCache()
	} else if before&0x02 != 0 && before != v.renderPieceFlags()[piece] {
		v.invalidateCacheValidity()
	}
	return true
}

func (v *VM) invalidateCache() {
	if v != nil {
		v.cacheRevision++
	}
}

func (v *VM) invalidateCacheValidity() {
	if v != nil {
		v.cacheValidityRevision++
		v.invalidateCache()
	}
}

func (v *VM) invalidateCachedPiece(piece int) {
	flags := v.renderPieceFlags()
	if piece >= 0 && piece < len(flags) && flags[piece]&0x02 != 0 {
		v.invalidateCacheValidity()
	}
}

// setPieceTrans and setPieceAngle keep the established pose arithmetic at
// their call sites while applying the script setter's exact cache gate
// [03 R-COMP-01 §4].
func (v *VM) setPieceTrans(piece, axis int, value numeric.Fixed) bool {
	if v == nil || piece < 0 || piece >= len(v.Pieces) || axis < 0 || axis >= 3 || v.Pieces[piece].GetTrans(axis) == value {
		return false
	}
	v.Pieces[piece].SetTrans(axis, value)
	v.invalidateCachedPiece(piece)
	return true
}

func (v *VM) setPieceAngle(piece, axis int, value uint16) bool {
	if v == nil || piece < 0 || piece >= len(v.Pieces) || axis < 0 || axis >= 3 || v.Pieces[piece].GetAngle(axis) == value {
		return false
	}
	v.Pieces[piece].SetAngle(axis, value)
	v.invalidateCachedPiece(piece)
	return true
}

// SetSFXSink installs the presentation-only emit-sfx sink [GAP T15] C19.
// Nil discards. Presentation-only and visibility-gated; no simulation state
// is written from this path.
func (v *VM) SetSFXSink(s SFXSink) { v.sfxSink = s }

// SetExplosionSink binds the synchronous physical/effect arena admission
// boundary. The sink owns bounded storage and phase-4 stepping; the VM keeps
// no event history and a nil sink is the explicit pending-U13 path [04
// R-COB-04 §1]–[04 R-COB-04 §4] [I5].
func (v *VM) SetExplosionSink(s ExplosionSink) {
	if v != nil {
		v.explosionSink = s
	}
}

// SetSFXVisible installs the visibility gate for emit-sfx [GAP T15] C19.
// When nil, presentation emission is suppressed (the missing dependency fails
// closed). When set, the gate is called with (piece, sfxType) and must return
// true for the effect to be emitted. The gate affects presentation only.
func (v *VM) SetSFXVisible(fn func(piece int, sfxType int32) bool) { v.sfxVisible = fn }

// SetSimulationRNG binds the session-owned simulation stream to this VM. COB
// random opcodes then consume this stream instead of the process-global
// fallback [01 §7.1] I4. A nil value preserves the unconfigured fixture fallback.
func (v *VM) SetSimulationRNG(sim *rng.Simulation) {
	if v != nil {
		v.simRng = sim
	}
}

// SimulationRNG returns the session-owned stream bound to this VM, if any.
func (v *VM) SimulationRNG() *rng.Simulation {
	if v == nil {
		return nil
	}
	return v.simRng
}

// Diagnostics returns fallback diagnostics collected for malformed COB paths
// [P2-03] (divide, corrupt input, stack overflow guard). Not fatal.
func (v *VM) Diagnostics() []string {
	if v == nil {
		return nil
	}
	return append([]string(nil), v.diagnostics...)
}

// ClearDiagnostics drops fallback diagnostics [P2-03].
func (v *VM) ClearDiagnostics() {
	if v != nil {
		v.diagnostics = nil
	}
}

// ScriptPC returns the code index for a script name, if present [fmt cob][04 §4.1].
func (v *VM) ScriptPC(name string) (int, bool) {
	if v == nil || v.prog == nil {
		return 0, false
	}
	pc, ok := v.prog.Scripts[name]
	return pc, ok
}

// StartByName starts a script by name on the lowest free thread [04 §4.1][04 §4.2].
// It is a convenience for engine→COB callbacks (Fire*, RockUnit, Aim*, TargetCleared) [GAP T15].
func (v *VM) StartByName(name string, args []int32) bool {
	pc, ok := v.ScriptPC(name)
	if !ok {
		return false
	}
	return v.Start(pc, args)
}

// LastStartedThread returns the last thread index allocated by Start/StartByName ON-04, -1 if none.
func (v *VM) LastStartedThread() int {
	if v == nil {
		return -1
	}
	return v.lastStarted
}

// ConsumeReturn retrieves and clears the explicit return value for thread idx ON-04 [04 §5.3].
// It returns true only if the thread executed an explicit return opcode; signal/abnormal termination never sets it.
func (v *VM) ConsumeReturn(threadIdx int) (int32, bool) {
	if v == nil || threadIdx < 0 || threadIdx >= 8 {
		return 0, false
	}
	if !v.lastReturnValid[threadIdx] {
		return 0, false
	}
	val := v.lastReturnValue[threadIdx]
	v.lastReturnValid[threadIdx] = false
	return val, true
}

// HasReturn reports whether thread idx has an unconsumed explicit return ON-04.
func (v *VM) HasReturn(threadIdx int) bool {
	if v == nil || threadIdx < 0 || threadIdx >= 8 {
		return false
	}
	return v.lastReturnValid[threadIdx]
}

// IsThreadAlive reports whether thread idx is not idle (running/sleeping/waiting) ON-04.
// It answers about the SLOT, not about any particular allocation of it: a slot
// freed and immediately refilled inside one drain reads alive again. A caller
// that started a callback and wants to know whether THAT callback is still
// running must use ThreadAliveAs [04 §4.2].
func (v *VM) IsThreadAlive(threadIdx int) bool {
	if v == nil || threadIdx < 0 || threadIdx >= 8 {
		return false
	}
	return v.Threads[threadIdx].Status != ThreadIdle
}

// ThreadIdentity returns the allocation identity currently occupying thread
// idx, or 0 when the slot has never been allocated by this program [04 §4.2].
// A caller reads it immediately after a successful start and presents it later;
// only that one execution of that one callback answers to it.
func (v *VM) ThreadIdentity(threadIdx int) uint64 {
	if v == nil || threadIdx < 0 || threadIdx >= 8 {
		return 0
	}
	return v.threadIdentity[threadIdx]
}

// ThreadAliveAs reports whether the allocation named by identity still occupies
// thread idx. A signal, an invalid-opcode kill, an explicit return, or a reuse
// of the slot by any other start all make it false [04 §4.2][04 §4.3].
func (v *VM) ThreadAliveAs(threadIdx int, identity uint64) bool {
	if v == nil || identity == 0 || threadIdx < 0 || threadIdx >= 8 {
		return false
	}
	return v.threadIdentity[threadIdx] == identity && v.Threads[threadIdx].Status != ThreadIdle
}

// ReturnedAs reports whether the allocation named by identity ended with the
// explicit-return opcode. Signal and abnormal termination never record one
// [04 §4.3][04 §5.3].
func (v *VM) ReturnedAs(threadIdx int, identity uint64) bool {
	if v == nil || identity == 0 || threadIdx < 0 || threadIdx >= 8 {
		return false
	}
	return v.lastReturnIdentity[threadIdx] == identity
}

// SetThreadCompletion installs the completion receiver on the allocation named
// by identity. It fails, changing nothing, when that allocation no longer owns
// the slot — a callback that has already been signalled, killed, or displaced
// by a reuse cannot acquire a receiver after the fact [04 §4.2][04 §5.3].
// The receiver is invoked by the explicit-return opcode and by nothing else.
func (v *VM) SetThreadCompletion(threadIdx int, identity uint64, fn func(int32)) bool {
	if v == nil || identity == 0 || threadIdx < 0 || threadIdx >= 8 {
		return false
	}
	if v.threadIdentity[threadIdx] != identity {
		return false
	}
	v.onReturn[threadIdx] = fn
	return true
}

// claimThread stamps a fresh allocation identity on slot idx and clears
// everything the previous occupant owned there: its completion receiver, which
// a new root or child thread never inherits, and its unconsumed explicit
// return, which belongs to an execution that is over [04 §4.2][04 §5.3].
// Every one of the four allocation sites — the two adapter starters and the two
// interpreter start forms — goes through it.
func (v *VM) claimThread(idx int) uint64 {
	v.nextIdentity++
	v.threadIdentity[idx] = v.nextIdentity
	v.onReturn[idx] = nil
	v.lastReturnValid[idx] = false
	v.lastReturnValue[idx] = 0
	v.lastReturnIdentity[idx] = 0
	return v.nextIdentity
}

// Start starts script at prog word index with args asynchronously [04 §4.2] [04 §4.3].
// It allocates the lowest clear thread slot [01 §6.1] C13; if no slot or the
// script id is not a valid entry, it returns false without consuming args
// from any caller stack (here args are kept by the caller) [04 §4.3] C14.
//
// Argument-area contract [R-COB-01 §1]: with at least one argument this is
// the argument-carrying starter — it always writes FOUR physical cells
// (window words 0..3) and only then sets the logical top to arity−1. This
// convenience wrapper uses the input length as arity and zero-fills missing
// cells through word 3; the bridge's explicit-arity adapters also allow payload
// beyond the logical top [R-CB-01 §2]. Words above the supplied inputs and word
// 3 stay stale. With no arguments this is the zero-argument name-form start:
// no window word is written and every window word stays stale.
// Engine-started threads start with mask 1 [R-P0-10].
func (v *VM) Start(script int, args []int32) bool {
	return v.start(script, args, len(args))
}

// start keeps logical arity separate from physical input cells. Argument-form
// engine adapters supply all four cells even at arity zero [R-COB-01 §1].
func (v *VM) start(script int, args []int32, arity int) bool {
	if v.prog == nil {
		return false
	}
	// The native thread record has a fixed physical window. This is a host
	// boundary for malformed callers, not a claim about native overflow.
	if len(args) > threadWindowWords || arity < 0 || arity > threadWindowWords {
		return false
	}
	if script < 0 || script >= len(v.prog.Code) {
		return false // bad script id [04 §4.3] C14
	}
	if !v.isValidEntry(script) {
		// Retail's shared thread starter admits a script id by ONE test: the
		// signed range `0 <= id < scriptCount`, the count being the compiled
		// program's own header word, and takes the new thread's PC from the
		// script entry-point table at that index [04 §4.3]. There is no
		// membership test on entry offsets anywhere.
		//
		// The two readings the retired marker here posed — "the entry offsets
		// resolved by name" versus "the raw ScriptCodeIndexArray indexed by
		// slot" — name the SAME set of word indices in this build: Load fills
		// Scripts[name] and ScriptsByID[i] from the one entry-point array, so a
		// name resolution can only ever produce an id-table entry. The marker's
		// premise was therefore vacuous (retired 2026-09-04, WU-19-155).
		//
		// This entry point takes a code word index rather than an id, so the
		// scan below is a fixture guard, not the retail admission: a production
		// start reaches it through a name resolution or through the interpreter
		// opcode, and both already index the table [04 §4.3] C14.
		return false
	}
	idx, ok := v.allocThread()
	if !ok {
		return false // pool full, no consume [04 §4.3] C14
	}
	v.claimThread(idx)
	t := &v.Threads[idx]
	t.Status = ThreadRunning
	v.activeThreadCount++
	t.PC = script
	// Engine-created root threads start with signal mask 1. Child threads
	// inherit their caller's mask below; this seed is the retail factory/COB
	// contract, not the generic format default [R-P0-10].
	t.SignalMask = 1
	t.WaitThread = -1
	t.WaitPiece = -1
	t.WaitAxis = -1
	t.Sleep = 0
	// Logical top −1 (SP 0): window words keep the slot's stale content until
	// the starter writes them [R-COB-01 §1].
	t.SP = 0
	if n := len(args); n > 0 {
		// Preserve all supplied physical cells independently of logical arity,
		// filling missing cells through word 3 with zero [R-COB-01 §1]. Engine
		// adapters supply four cells; fixtures may fill the remaining window.
		for i := 0; i < 4 && i < n; i++ {
			t.Stack[i] = args[i]
		}
		for i := n; i < 4; i++ {
			t.Stack[i] = 0 // traced producers' zero fillers [R-COB-01 §1]
		}
		for i := 4; i < n; i++ {
			t.Stack[i] = args[i]
		}
		t.SP = arity
	}
	// ON-04 Aim dispatch records thread relationship [06 §3.3]. The stale
	// return and receiver for this slot were cleared by claimThread above.
	v.lastStarted = idx
	return true
}

// Call runs script synchronously [04 §4.2] as the synchronous query helper.
// On a full pool it returns false and leaves args untouched [04 §4.3].
// On success it pushes the (up to) four inputs, forces depth to three
// (SP=4), runs the interpreter inline with delta 0 (no time, no piece
// interpolation) [04 §4.2], and copies the first four window words back into
// args, matching retail's query helper [04 §4.3] "On success it pushes its
// four inputs, forces the depth to three, runs the interpreter inline, and
// copies the first four window words back out."
func (v *VM) Call(script int, args []int32) bool {
	// A blocked query remains allocated after copy-back. Do not clean it up
	// here: Q lifetime is part of the engine contract and a later normal drain
	// may resume the thread [04 §4.2].
	started, _ := v.CallQuery(script, args)
	return started
}

// CallQuery executes one synchronous mode-Q callback and leaves a sleeping or
// waiting thread active after copying its current four cells. It returns
// started and returned separately: a missing entry/full pool/invalid identity
// reports started=false and leaves args untouched; a blocked callback reports
// started=true, returned=false, and preserves the partial output/thread for a
// later VM-wide drain [04 §4.2][04 §4.4]. No piece interpolation occurs.
func (v *VM) CallQuery(script int, args []int32) (started, returned bool) {
	if v == nil {
		return false, false
	}
	v.lastQueryThread = -1
	if v.prog == nil {
		return false, false
	}
	if script < 0 || script >= len(v.prog.Code) {
		return false, false
	}
	if !v.isValidEntry(script) {
		return false, false
	}
	idx, ok := v.allocThread()
	if !ok {
		return false, false // full pool returns failure and leaves outputs untouched [04 §4.3]
	}
	v.lastQueryThread = idx
	// Claiming the slot clears the stale explicit-return marker and the stale
	// completion receiver left by a prior callback: the current Q result must
	// observe only this invocation, and a query never carries a receiver of its
	// own, so a blocked query that later resumes can revise nothing [04 §4.2].
	v.claimThread(idx)
	t := &v.Threads[idx]
	t.Status = ThreadRunning
	v.activeThreadCount++
	t.PC = script
	// Synchronous engine-created roots use the same signal mask seed as
	// asynchronous roots [R-P0-10].
	t.SignalMask = 1
	t.WaitThread = -1
	t.WaitPiece = -1
	t.WaitAxis = -1
	// Push four inputs, garbage for missing, depth forced to three (SP=4) [04 §4.3].
	t.SP = 4
	for i := 0; i < 4; i++ {
		var val int32
		if i < len(args) {
			val = args[i]
		}
		t.Stack[i] = val
	}
	// Run inline with delta 0, no piece interpolation, to completion or block.
	// We reuse the thread-run logic but without the outer Drain scheduling.
	v.runThreadSync(idx)
	// Copy back first four window words [04 §4.3].
	for i := 0; i < 4 && i < len(args); i++ {
		args[i] = t.Stack[i]
	}
	returned = v.lastReturnValid[idx]
	if returned {
		// Explicit return already released the thread. Keep the return marker
		// available to the caller's query result until it is consumed.
		return true, true
	}
	// A sleep/wait leaves the query thread active by retail contract. It will
	// resume during a later normal drain and does not revise this host result.
	return true, false
}

// Signal kills every thread whose mask intersects mask, waking anything
// blocked on them [04 §4.3] signal opcode. Wake is transitive within the
// same scan [04 §4.2]. This external Signal mirrors the opcode semantics
// per the brief's "wake/signal semantics per [04 §4.2]/[04 §4.3]".
func (v *VM) Signal(mask int32) {
	if mask == 0 {
		return
	}
	v.signalMask(mask)
}

// Drain advances the VM by delta ticks [04 §4.6][04 R-COB-02 §2] [PLAN_06].
// It runs due threads within the tick budget in fixed slot order 0..7 [04 §4.2] C13, then one piece-interpolation pass [04 §4.6][04 R-COB-02 §2] C13.
// Sleep converts its duration using the fixed 30 denominator [04 §4.6]; a wait polls its busy word and wakes on zero [04 §4.6]; immediate move/turn wake same-tick when the issuing slot precedes the waiting slot, otherwise next tick [04 §4.6] slot-order.
// A sleep occupies its truncated tick count plus one guard decrement, so sleep 0 still costs one tick [04 §4.6]. Signals and wake are handled inside the run.
// Drain is reentrant-safe: an all-slot delta-0 wake drain can be issued from inside a starter and will run due threads with no time advance [04 §4.2][GAP T15][04 §4.6] — the same all-eight-slot delta-0 pass every immediate (wake) start performs [04 R-COB-02 §2].
func (v *VM) Drain(delta int) {
	v.DrainCalls++ // RS-08 one-drain invariant [04 §4.2][GAP T15]
	if v.prog == nil {
		// Still do piece pass if we have anim state but no code? No code to run.
		if delta != 0 {
			v.interpolate(delta)
		}
		return
	}
	// The scheduler budget is clamped to 0..5 before a VM visit [01 §4.2].
	if delta < 0 {
		delta = 0
	} else if delta > 5 {
		delta = 5
	}
	// Eight thread slots, fixed scan order [04 §4.2] C13 (I1).
	for idx := 0; idx < 8; idx++ {
		t := &v.Threads[idx]
		if t.Status == ThreadIdle {
			continue
		}
		if t.Status == ThreadSleeping {
			// Guard subtracts delta first and wakes only when <=0 [04 §4.6].
			t.Sleep -= int32(delta)
			if t.Sleep > 0 {
				continue // still sleeping, yield [04 §4.6]
			}
			t.Status = ThreadRunning // wake [04 §4.6]
		} else if t.Status == ThreadWaitTurn {
			if v.isTurnBusy(t.WaitPiece, t.WaitAxis) {
				continue
			}
			t.Status = ThreadRunning
		} else if t.Status == ThreadWaitMove {
			if v.isMoveBusy(t.WaitPiece, t.WaitAxis) {
				continue
			}
			t.Status = ThreadRunning
		} else if t.Status == ThreadWaitCall {
			// Only return and signal wake a call waiter. An idle callee may
			// have been killed by an invalid opcode [04 §4.2].
			continue
		}
		if t.Status != ThreadRunning {
			continue
		}
		v.runThread(idx)
	}
	// One piece pass [04 §4.6] C13. Delta 0 performs no interpolation but still
	// allows immediate move/turn that committed during interpretation to be
	// visible [04 §4.6].
	if delta != 0 {
		v.interpolate(delta)
	}
}

// isValidEntry reports whether word index is a script entry point [fmt cob] [04 §4.1].
// It scans the ordered ScriptsByID slice for membership to avoid map iteration
// (I1: no `range` over a map on a sim-visible path). Linear scan over ≤ a few
// hundred entries is fine and keeps the invalid-entry check deterministic.
func (v *VM) isValidEntry(pc int) bool {
	if v.prog == nil {
		return false
	}
	for _, entry := range v.prog.ScriptsByID {
		if entry == pc {
			return true
		}
	}
	return false
}

// allocThread returns lowest clear thread slot 0..7 per [01 §6.1] C13.
// Scan order is fixed ascending [04 §4.2] (I1).
func (v *VM) allocThread() (int, bool) {
	for i := 0; i < 8; i++ {
		if v.Threads[i].Status == ThreadIdle {
			// Mark allocated immediately (mask bit set) per [04 §4.2].
			// Status will be set by caller to Running.
			return i, true
		}
	}
	return -1, false
}

// killThread clears the thread status, decrements the instance active count
// without waking callers. Return and signal explicitly wake their waiters;
// invalid-opcode termination does not [04 §4.2][04 §4.3].
func (v *VM) killThread(idx int) {
	if idx < 0 || idx >= 8 {
		return
	}
	t := &v.Threads[idx]
	if t.Status == ThreadIdle {
		return
	}
	if v.activeThreadCount > 0 {
		v.activeThreadCount--
	}
	t.Status = ThreadIdle
	t.PC = 0
	t.SP = 0
	t.Sleep = 0
	t.WaitPiece = -1
	t.WaitAxis = -1
	// Termination ends the allocation's completion ownership. Signal and the
	// invalid-opcode kill both land here and neither invokes a receiver; the
	// explicit-return opcode has already taken and invoked its own before
	// calling in [04 §4.3][04 §5.3].
	v.onReturn[idx] = nil
	// The signal mask is left as it stands: an idle thread ignores it, and
	// retail clears nothing here [04 §4.3].
	t.WaitThread = -1
}

// wakeCallers implements the explicit return/signal wake, including waiters
// left behind by an earlier occupant of this slot [04 §4.2].
func (v *VM) wakeCallers(idx int) {
	// Wake: any WaitCall parked on idx flips to Running immediately and can run
	// later in this same drain scan [04 §4.2]. One pass over the eight slots is
	// enough, because idx is a single thread identity — a waiter that is itself
	// waited on becomes runnable, not woken, and the transitive case across
	// several kills is signalMask's outer loop, not this one.
	for j := 0; j < 8; j++ {
		ot := &v.Threads[j]
		if ot.Status == ThreadWaitCall && ot.WaitThread == idx {
			ot.Status = ThreadRunning
			ot.WaitThread = -1
		}
	}
}

// signalMask kills every thread whose mask intersects mask and wakes waiters.
// It repeats until no more intersections (transitive via wake) [04 §4.3].
func (v *VM) signalMask(mask int32) {
	if mask == 0 {
		return
	}
	// Repeated scan to handle transitive wake-then-kill where a woken thread
	// also matches mask. Retail's signal scans with waking; we emulate via loop.
	for {
		killedAny := false
		for i := 0; i < 8; i++ {
			t := &v.Threads[i]
			if t.Status == ThreadIdle {
				continue
			}
			if t.SignalMask&mask != 0 {
				v.killThread(i)
				v.wakeCallers(i)
				killedAny = true
			}
		}
		if !killedAny {
			break
		}
		// After kills, some WaitCall threads may have been woken to Running and
		// now also intersect mask, so they will be killed in next iteration.
		// Loop covers that.
	}
}

// isTurnBusy reports whether turn/spin is busy for this piece axis [04 §4.6].
// A wait-for-turn polls the axis's shared rotation speed and wakes as soon
// as that speed reads zero, in either rotation mode [04 §4.6].
func (v *VM) isTurnBusy(piece, axis int) bool {
	if piece < 0 || piece >= len(v.anims) || axis < 0 || axis >= 3 {
		return false
	}
	anim := &v.anims[piece].axes[axis]
	// Both rotation modes share the speed polled by wait-for-turn [04 §4.6].
	return anim.turnSpeed != 0
}

// isMoveBusy reports whether move is busy for this piece axis [04 §4.6].
// A wait-for-move polls the axis's move-speed word and wakes as soon as its
// per-tick speed truncates to zero [04 §4.6].
func (v *VM) isMoveBusy(piece, axis int) bool {
	if piece < 0 || piece >= len(v.anims) || axis < 0 || axis >= 3 {
		return false
	}
	anim := &v.anims[piece].axes[axis]
	// moveBusy mirrors the axis's per-tick move speed being nonzero [04 §4.6].
	return anim.moveBusy && anim.moveSpeed != 0
}

func (v *VM) markAnimationDirty(piece int) {
	v.dirty = true
	if piece >= 0 && piece < len(v.pieceBusy) {
		v.pieceBusy[piece] = true
	}
}

// interpolate advances all piece axes by delta [04 §4.6].
// The tick denominator is the immutable constant 30, fixed at process
// startup and not derived per tick [04 §4.6].
// Division truncates toward zero (Go int64 / matches) per I3 [04 §4.6]; no remainder carry; -100/30 is -3.
func (v *VM) interpolate(delta int) {
	if delta == 0 || !v.dirty || len(v.anims) == 0 {
		return
	}
	v.dirty = false
	// Deterministic iteration: pieces ascending (I1) [04 §4.2]; axis 0..2.
	for p := range v.anims {
		if p >= len(v.Pieces) {
			continue
		}
		if p < len(v.pieceBusy) {
			v.pieceBusy[p] = false
		}
		for axis := 0; axis < 3; axis++ {
			anim := &v.anims[p].axes[axis]
			// Move block, first of the three per-axis blocks [04 §4.6].
			// The per-tick step was already divided by the tick denominator
			// when the move was issued (speed/30, truncated); no remainder
			// carries between ticks [04 §4.6].
			if anim.moveBusy {
				// A per-tick step of zero means not busy, so a wait wakes immediately [04 §4.6].
				if anim.moveSpeed == 0 {
					// Zero per-tick step: no motion, clear busy so wait wakes; dirty still implied for one tick via the issuing handler [04 §4.6].
					anim.moveBusy = false
				} else {
					cur := int64(v.Pieces[p].Trans[axis].Raw())  // [03 §2.4] C21
					target := int64(anim.moveTarget)             // compiled [fmt cob]
					step := int64(anim.moveSpeed) * int64(delta) // already perTick = trunc(raw/30) [04 §4.6]
					diff := target - cur
					if diff == 0 {
						anim.moveBusy = false
						anim.moveSpeed = 0
					} else {
						var mag int64
						if step < 0 {
							mag = -step
						} else {
							mag = step
						}
						if diff > 0 {
							if diff <= mag {
								v.setPieceTrans(p, axis, fixedFromRaw(target)) // snap on inclusive arrival [04 §4.6]
								anim.moveBusy = false
								anim.moveSpeed = 0
							} else {
								v.setPieceTrans(p, axis, fixedFromRaw(cur+mag))
							}
						} else {
							if -diff <= mag {
								v.setPieceTrans(p, axis, fixedFromRaw(target))
								anim.moveBusy = false
								anim.moveSpeed = 0
							} else {
								v.setPieceTrans(p, axis, fixedFromRaw(cur-mag))
							}
						}
					}
				}
			}
			// Acceleration-ramp block, second of the three, then rotation/spin
			// third [04 §4.6], with inclusive clamping on reaching or crossing
			// the target.
			// The signed acceleration is added directly. Its sign selects the
			// inclusive clamp direction; it is not an approach magnitude [04 §4.6].
			if anim.spinAccel != 0 {
				// Ramp adds one signed stored-word acceleration per pass. The
				// word narrows before its signed inclusive clamp [04 §4.6].
				next := anim.turnSpeed + anim.spinAccel
				if (anim.spinAccel < 0 && next <= anim.spinTarget) || (anim.spinAccel > 0 && next >= anim.spinTarget) {
					anim.turnSpeed = anim.spinTarget
					anim.spinAccel = 0
				} else {
					anim.turnSpeed = next
				}
			}
			anim.turnBusy = !anim.spinActive && anim.turnSpeed != 0
			if anim.spinActive {
				// The angle increment is already perTick * delta [04 §4.6]; a per-tick speed truncating to zero still leaves the piece dirty for one tick, and a wait wakes immediately [04 §4.6].
				if anim.turnSpeed != 0 {
					step := int64(anim.turnSpeed) * int64(delta) // already trunc(speed/30) [04 §4.6]
					if step != 0 {
						v.setPieceAngle(p, axis, v.Pieces[p].GetAngle(axis)+uint16(step)) // wraps [03 §2.4] C22 (I2)
					}
				}
				// The spin marker survives stop-spin; only turn or turn-now replaces it [04 §4.6].
				// A zero-speed spin has no motion, and the per-piece reduction clears dirty next tick; a wait wakes immediately once the polled word reads zero [04 §4.6].
			} else if anim.turnBusy {
				// Turn uses the already-divided per-tick speed [04 §4.6]; a shortest-arc tie at exactly the half-circle boundary keeps the script's sign rather than flipping it [04 §4.6].
				if anim.turnSpeed == 0 {
					anim.turnBusy = false
				} else {
					cur := v.Pieces[p].GetAngle(axis) // [03 §2.4] C21 uint16
					target := anim.turnTarget
					if cur == target {
						anim.turnBusy = false
						anim.turnSpeed = 0
					} else {
						step := int64(anim.turnSpeed) * int64(delta) // already perTick [04 §4.6]
						mag := step
						if mag < 0 {
							mag = -mag
						}
						// The issued signed step selects the arc. In particular, a
						// 0x8000 tie retains script direction instead of becoming the
						// negative int16 difference [04 §4.6].
						var remaining int64
						if anim.turnSpeed > 0 {
							remaining = int64(uint16(target - cur))
						} else {
							remaining = int64(uint16(cur - target))
						}
						if remaining <= mag {
							v.setPieceAngle(p, axis, target) // snap on inclusive arrival [04 §4.6]
							anim.turnBusy = false
							anim.turnSpeed = 0
						} else if anim.turnSpeed > 0 {
							v.setPieceAngle(p, axis, uint16(int64(cur)+mag))
						} else {
							v.setPieceAngle(p, axis, uint16(int64(cur)-mag))
						}
					}
				}
			}
		}
		if p < len(v.pieceBusy) {
			for axis := 0; axis < 3; axis++ {
				anim := &v.anims[p].axes[axis]
				if anim.moveBusy || anim.turnSpeed != 0 || anim.spinAccel != 0 {
					v.pieceBusy[p] = true
					v.dirty = true
					break
				}
			}
		}
	}
}

// fixedFromRaw converts raw int64 to Fixed (16.16).
func fixedFromRaw(raw int64) numeric.Fixed {
	return numeric.Fixed(raw)
}

// runThreadSync runs a thread inline to yield/block/return with delta 0.
// Used by Call.
func (v *VM) runThreadSync(idx int) {
	// Drain already checked guards for this thread; we just run its code
	// until it would sleep/wait/call/return. For sync query we execute with
	// delta 0, so sleep would immediately consider timer 0? Actually sync
	// queries do not advance time, but a sleep inside them would capture timer
	// and then the query returns whatever window holds without waiting [04 §4.3].
	// We'll just call runThread which respects current status (running).
	v.runThread(idx)
}

// runThread executes the opcode stream for thread idx until it yields, blocks,
// or is killed. It assumes the thread's pre-guards have already been handled
// by Drain.
//
// There is no per-visit iteration cap: retail has none either, and a tight
// non-yielding script loop wedges the drain exactly as it wedges retail
// [04 §4.2] (PLAN_06 C14 reproduce-don't-defend).
func (v *VM) runThread(idx int) {
	t := &v.Threads[idx]
	for {
		if t.Status != ThreadRunning {
			return
		}
		if v.prog == nil || t.PC < 0 || t.PC >= len(v.prog.Code) {
			v.killThread(idx)
			return
		}
		word := v.prog.Code[t.PC]
		key := word & dispatchMask // [04 §4.3] C10
		// The switch owns the dispatch set; its default takes the same
		// unmatched-key kill path [04 §4.3] C11.
		switch key {
		case 0x10001000: // move [04 §4.3] C shape, per-tick arithmetic [04 §4.6]
			if t.PC+2 >= len(v.prog.Code) {
				v.killThread(idx)
				return
			}
			piece := int(v.prog.Code[t.PC+1])
			axis := int(v.prog.Code[t.PC+2])
			target, _ := t.stackPop()
			speed, _ := t.stackPop()
			// Bad piece index kills thread (status cleared) [P1-11] §2.4.
			if piece < 0 || piece >= len(v.anims) || axis < 0 || axis >= 3 {
				v.killThread(idx)
				return
			}
			anim := &v.anims[piece].axes[axis]
			anim.moveTarget = target
			// perTick = trunc(speedRaw/30), truncating toward zero [I3], with its sign set toward the target at issuance [04 §4.6].
			perTick := int32(int64(speed) / int64(v.tickDenom)) // latched denominator [04 §4.6]; trunc toward zero per I3.
			cur := int64(v.Pieces[piece].Trans[axis].Raw())     // [03 §2.4] C21 via GetPos
			if cur > int64(target) {
				perTick = -perTick // flip sign toward target [04 §4.6]
			}
			anim.moveSpeed = perTick     // the axis's move-speed word [04 §4.6]
			anim.moveBusy = perTick != 0 // a per-tick speed truncating to zero wakes a wait immediately [04 §4.6]
			v.markAnimationDirty(piece)  // move dirties the piece and global flag, including zero-speed issue [04 §4.6]
			t.PC += 3
		case 0x10002000: // turn [04 §4.3][04 §4.6], per-tick arithmetic and shortest-arc sign
			if t.PC+2 >= len(v.prog.Code) {
				v.killThread(idx)
				return
			}
			piece := int(v.prog.Code[t.PC+1])
			axis := int(v.prog.Code[t.PC+2])
			target, _ := t.stackPop()
			speed, _ := t.stackPop()
			if piece < 0 || piece >= len(v.anims) || axis < 0 || axis >= 3 {
				v.killThread(idx)
				return
			}
			anim := &v.anims[piece].axes[axis]
			tgt := uint16(target) // masked &0xffff [04 §4.6]
			anim.turnTarget = tgt // the axis's turn-target word [04 §4.6]
			anim.spinAccel = 0    // clears any stale spin acceleration on this axis [04 §4.6]
			// perTick = trunc(speedRaw/30), then the shortest-arc sign choice below [04 §4.6].
			perTick := int32(int64(speed) / int64(v.tickDenom)) // latched denominator [04 §4.6]; trunc toward zero per I3.
			curAng := v.Pieces[piece].GetAngle(axis)            // [03 §2.4] C21 via GetAng
			// The signed raw target delta and strict half-circle comparison select
			// the issued direction [04 §4.6].
			delta := int64(tgt) - int64(curAng) // -65535..65535, not int16-wrapped; >0x8000 triggers shortest-arc flip
			if delta == 0 {
				perTick = 0
			} else {
				absDelta := delta
				if absDelta < 0 {
					absDelta = -absDelta
				}
				if (absDelta > 0x8000) != (delta < 0) {
					perTick = -perTick
				}
			}
			anim.turnSpeed = perTick     // the axis's turn-speed word [04 §4.6]
			anim.turnBusy = perTick != 0 // a per-tick speed truncating to zero wakes a wait immediately [04 §4.6]
			anim.spinActive = false
			v.markAnimationDirty(piece) // turn dirties the piece and global flag, including zero-speed issue [04 §4.6]
			t.PC += 3
		case 0x10003000: // spin [04 §4.3][04 §4.6], per-tick arithmetic
			if t.PC+2 >= len(v.prog.Code) {
				v.killThread(idx)
				return
			}
			piece := int(v.prog.Code[t.PC+1])
			axis := int(v.prog.Code[t.PC+2])
			speed, _ := t.stackPop() // top speed [fmt cob] "spin ... speed S accelerate A" pushes accel then speed (retail order) [04 §4.3]
			accel, _ := t.stackPop()
			if piece < 0 || piece >= len(v.anims) || axis < 0 || axis >= 3 {
				v.killThread(idx)
				return
			}
			anim := &v.anims[piece].axes[axis]
			// Spin writes the turn-target/marker word with the out-of-band
			// spin sentinel, the spin target speed as trunc(speed/30), and
			// the spin acceleration as trunc(accel/30), truncating toward
			// zero [I3] with the fixed 30 denominator [04 §4.6].
			perTickSpeed := int32(int64(speed) / int64(v.tickDenom)) // trunc toward zero [04 §4.6], latched denominator
			perTickAccel := int32(int64(accel) / int64(v.tickDenom))
			// The spin sentinel is conceptual here; the spinActive bool distinguishes spin state from an ordinary turn [04 §4.6].
			anim.spinTarget = perTickSpeed // the axis's spin-target word [04 §4.6]
			anim.spinAccel = perTickAccel  // the axis's spin-acceleration word [04 §4.6]
			// With acceleration, preserve the current rotation speed even when
			// switching from a positional turn [04 §4.6].
			if perTickAccel == 0 {
				anim.turnSpeed = perTickSpeed // fast path: a zero per-tick acceleration snaps straight to the target speed [04 §4.6]
			}
			anim.spinActive = true
			anim.turnBusy = false
			v.markAnimationDirty(piece) // spin dirties the piece and global flag, including zero-speed issue [04 §4.6]
			t.PC += 3
		case 0x10004000: // stop-spin [04 §4.3][04 §4.6], per-tick arithmetic
			if t.PC+2 >= len(v.prog.Code) {
				v.killThread(idx)
				return
			}
			piece := int(v.prog.Code[t.PC+1])
			axis := int(v.prog.Code[t.PC+2])
			dec, _ := t.stackPop()
			if piece < 0 || piece >= len(v.anims) || axis < 0 || axis >= 3 {
				v.killThread(idx)
				return
			}
			anim := &v.anims[piece].axes[axis]
			// Stop-spin writes the spin target speed to zero and the spin
			// acceleration to -(decel/30); it does not set the per-piece
			// busy flag or the global dirty flag itself — it relies on the
			// prior spin's dirty to reach the interpolator [04 §4.6].
			anim.spinTarget = 0                                    // zero target speed [04 §4.6]
			perTickDecel := int32(int64(dec) / int64(v.tickDenom)) // trunc toward zero, latched denominator [04 §4.6]
			anim.spinAccel = -perTickDecel                         // -(decel/30) [04 §4.6]
			if anim.spinAccel == 0 {
				anim.turnSpeed = 0 // immediate stop, shared by both rotation modes [04 §4.6]
				anim.turnBusy = false
			}
			// Stop-spin does not replace the turn target or continuous-spin
			// marker. Its ramp runs before either rotation mode [04 §4.6].
			t.PC += 3
		case 0x10005000: // show [04 §4.3] B — set bit 0 draw via unit record when bound [04 §"Piece flag polarity"]
			if t.PC+1 >= len(v.prog.Code) {
				v.killThread(idx)
				return
			}
			piece := int(v.prog.Code[t.PC+1])
			if !v.setRenderFlag(piece, 0x01, true) {
				v.killThread(idx)
				return
			}
			t.PC += 2
		case 0x10006000: // hide [04 §4.3] — clear bit 0 draw via unit record when bound
			if t.PC+1 >= len(v.prog.Code) {
				v.killThread(idx)
				return
			}
			piece := int(v.prog.Code[t.PC+1])
			if !v.setRenderFlag(piece, 0x01, false) {
				v.killThread(idx)
				return
			}
			t.PC += 2
		case 0x10007000: // cache [04 §4.3] — set bit 1 cache via unit record when bound
			if t.PC+1 >= len(v.prog.Code) {
				v.killThread(idx)
				return
			}
			piece := int(v.prog.Code[t.PC+1])
			if !v.setRenderFlag(piece, 0x02, true) {
				v.killThread(idx)
				return
			}
			t.PC += 2
		case 0x10008000: // dont-cache [04 §4.3] — clear bit 1 cache via unit record when bound
			if t.PC+1 >= len(v.prog.Code) {
				v.killThread(idx)
				return
			}
			piece := int(v.prog.Code[t.PC+1])
			if !v.setRenderFlag(piece, 0x02, false) {
				v.killThread(idx)
				return
			}
			t.PC += 2
		case 0x10009000: // legacy two-arg effect (no-op) [04 §4.3][P1-11] — empty unit adapter stub, two-pop no-op on units
			if t.PC+1 >= len(v.prog.Code) {
				v.killThread(idx)
				return
			}
			// Pops two values [04 §4.3][P1-11] but does nothing on units (empty adapter stub).
			t.stackPop()
			t.stackPop()
			t.PC += 2
		case 0x1000a000: // dont-shadow [04 §4.3][R-COB-01 §1]
			if t.PC+1 >= len(v.prog.Code) {
				v.killThread(idx)
				return
			}
			// The disable-shadow opcode binds an EMPTY adapter on units: it
			// has no effect and there is no script-visible per-piece shadow
			// state anywhere on the unit side [R-COB-01 §1]. Shadow rendering
			// is a renderer concern (document 03); no initial shadow
			// derivation exists to reproduce. The piece operand is consumed
			// and the interpreter moves on.
			t.PC += 2
		case 0x1000b000: // move-now [04 §4.3][04 §4.6], immediate commit
			if t.PC+2 >= len(v.prog.Code) {
				v.killThread(idx)
				return
			}
			piece := int(v.prog.Code[t.PC+1])
			axis := int(v.prog.Code[t.PC+2])
			target, _ := t.stackPop()
			if piece < 0 || piece >= len(v.Pieces) || axis < 0 || axis >= 3 {
				v.killThread(idx)
				return
			}
			v.setPieceTrans(piece, axis, fixedFromRaw(int64(target))) // [03 §2.4] C22; immediate set-position commit [04 §4.6]
			if piece < len(v.anims) {
				anim := &v.anims[piece].axes[axis]
				anim.moveTarget = target // the axis's move-target word [04 §4.6]
				// Move-now owns only translation: it clears the move-speed word
				// and commits via the model adapter without setting dirty. The
				// turn and spin lanes remain live [04 §4.6].
				anim.moveBusy = false
				anim.moveSpeed = 0
			}
			t.PC += 3
		case 0x1000c000: // turn-now [04 §4.3][04 §4.6], immediate commit
			if t.PC+2 >= len(v.prog.Code) {
				v.killThread(idx)
				return
			}
			piece := int(v.prog.Code[t.PC+1])
			axis := int(v.prog.Code[t.PC+2])
			target, _ := t.stackPop()
			if piece < 0 || piece >= len(v.Pieces) || axis < 0 || axis >= 3 {
				v.killThread(idx)
				return
			}
			v.setPieceAngle(piece, axis, uint16(target)) // masked [04 §4.3][04 §4.6]; immediate set-angle commit
			if piece < len(v.anims) {
				anim := &v.anims[piece].axes[axis]
				anim.turnTarget = uint16(target) // masked &0xffff [04 §4.6]
				// The immediate angle commit clears rotation speed and acceleration,
				// preserving same-axis translation and its wait [04 §4.6].
				anim.turnBusy = false
				anim.turnSpeed = 0
				anim.spinAccel = 0
				anim.spinActive = false
			}
			t.PC += 3
		case 0x1000d000: // shade [04 §4.3] — set bit 2 shade via unit record when bound
			if t.PC+1 >= len(v.prog.Code) {
				v.killThread(idx)
				return
			}
			piece := int(v.prog.Code[t.PC+1])
			if !v.setRenderFlag(piece, 0x04, true) {
				v.killThread(idx)
				return
			}
			t.PC += 2
		case 0x1000e000: // dont-shade [04 §4.3] — clear bit 2 shade via unit record when bound
			if t.PC+1 >= len(v.prog.Code) {
				v.killThread(idx)
				return
			}
			piece := int(v.prog.Code[t.PC+1])
			if !v.setRenderFlag(piece, 0x04, false) {
				v.killThread(idx)
				return
			}
			t.PC += 2
		case 0x1000f000: // emit-sfx [04 §4.3]
			if t.PC+1 >= len(v.prog.Code) {
				v.killThread(idx)
				return
			}
			piece := int(v.prog.Code[t.PC+1]) // B shape piece operand [04 §4.3]
			effectType, _ := t.stackPop()     // pops effect type [04 §4.3]
			// Presentation-only and visibility-gated [GAP T15] C19; no sim state write.
			// Classification is SFXKind via ClassifySFX in ports.go [GAP T15] C19.
			if v.sfxSink != nil {
				kind := ClassifySFX(effectType) // [GAP T15] C19
				// A visibility dependency is required for presentation emission;
				// absent visibility fails closed. Classification and operand
				// consumption remain independent of the presentation gate.
				if kind != SFXIgnored && v.sfxVisible != nil && v.sfxVisible(piece, effectType) {
					v.sfxSink.EmitSFX(piece, effectType, kind) // [GAP T15] C19
				}
			}
			t.PC += 2
		case 0x10011000: // wait-for-turn [04 §4.3] suspends yes
			if t.PC+2 >= len(v.prog.Code) {
				v.killThread(idx)
				return
			}
			piece := int(v.prog.Code[t.PC+1])
			axis := int(v.prog.Code[t.PC+2])
			t.WaitPiece = piece
			t.WaitAxis = axis
			t.Status = ThreadWaitTurn // [04 §4.2]
			t.PC += 3
			return // yield drain [04 §4.6]
		case 0x10012000: // wait-for-move [04 §4.3]
			if t.PC+2 >= len(v.prog.Code) {
				v.killThread(idx)
				return
			}
			piece := int(v.prog.Code[t.PC+1])
			axis := int(v.prog.Code[t.PC+2])
			t.WaitPiece = piece
			t.WaitAxis = axis
			t.Status = ThreadWaitMove
			t.PC += 3
			return
		case 0x10013000: // sleep [04 §4.3][04 §4.6]
			dur, _ := t.stackPop() // milliseconds [fmt cob]
			// Convert trunc(denom * ms / 1000) with the latched denominator
			// (30 from the fixed 30-tick configuration) [04 §4.6]; trunc toward
			// zero [01 §8] I3, denominator positive, no remainder carry; 33ms→0
			// ticks, 34ms→1 tick [04 §4.6]. Sleep multiplies, it never divides
			// by the denominator [04 §4.6].
			ticks := int32((int64(dur) * int64(v.tickDenom)) / 1000)
			t.Sleep = ticks
			t.Status = ThreadSleeping
			t.PC += 1
			return
		case 0x10021000: // push [04 §4.3] F
			if t.PC+1 >= len(v.prog.Code) {
				v.killThread(idx)
				return
			}
			// Keep malformed bytecode inside the host's physical window.
			if t.SP >= len(t.Stack) {
				v.killThread(idx)
				return
			}
			mode := int(word & 0x7) // low three bits [04 §4.3] C10
			operand := v.prog.Code[t.PC+1]
			switch mode {
			case 1: // push constant [04 §4.3]
				t.stackPush(int32(operand))
			case 2: // push local [04 §4.3]
				t.stackPush(t.getLocal(int(operand)))
			case 4: // push static [04 §4.3]
				t.stackPush(v.getStatic(int(operand)))
			default:
				// Any other sub-mode pushes uninitialized scratch [04 §4.3] C14.
				// SP advances without a write, so the slot's stale frame
				// contents show through on the next pop — there is no default
				// branch pushing a fresh value.
				t.stackPushScratch()
			}
			t.PC += 2
		case 0x10022000: // alloc-local [04 §4.3]
			// Keep malformed bytecode inside the host's physical window.
			if t.SP >= len(t.Stack) {
				v.killThread(idx)
				return
			}
			// Raise depth without initializing [04 §4.3]
			t.SP++
			t.PC += 1
		case 0x10023000: // pop [04 §4.3] F C14
			if t.PC+1 >= len(v.prog.Code) {
				v.killThread(idx)
				return
			}
			mode := int(word & 0x7)
			operand := int(v.prog.Code[t.PC+1])
			switch mode {
			case 2: // pop local [04 §4.3]
				val, _ := t.stackPop()
				t.setLocal(operand, val)
			case 4: // pop static [04 §4.3]
				val, _ := t.stackPop()
				v.setStatic(operand, val)
			default:
				// Any other sub-mode pops nothing and simply advances [04 §4.3] C14
			}
			t.PC += 2
		case 0x10024000: // discard [04 §4.3]
			t.stackPop()
			t.PC += 1
		case 0x10031000: // add [04 §4.3]
			b, _ := t.stackPop()
			a, _ := t.stackPop()
			if t.SP >= len(t.Stack) { // overflow kills [P1-11] §2.4
				v.killThread(idx)
				return
			}
			t.stackPush(a + b) // wrap 32 bits
			t.PC += 1
		case 0x10032000: // subtract second-popped minus top [04 §4.3]
			b, _ := t.stackPop()
			a, _ := t.stackPop()
			if t.SP >= len(t.Stack) {
				v.killThread(idx)
				return
			}
			t.stackPush(a - b)
			t.PC += 1
		case 0x10033000: // multiply [04 §4.3]
			b, _ := t.stackPop()
			a, _ := t.stackPop()
			if t.SP >= len(t.Stack) {
				v.killThread(idx)
				return
			}
			t.stackPush(a * b)
			t.PC += 1
		case 0x10034000: // divide unguarded [04 §4.3] C14
			b, _ := t.stackPop()
			a, _ := t.stackPop()
			// [P2-03][04 §4.3] C14 I11: retail raises #DE (process death) for
			// divisor 0 or INT_MIN/-1 with no guard. Nanolathe keeps malformed
			// as explicit fallback, not crash: kill the thread and diagnostic,
			// do not push — matches kill-path stack-leak semantics (no push).
			// The question of what retail pushes is closed: it pushes nothing.
			// "A divide by zero or integer overflow raises the processor divide
			// fault and kills the retail process outright — document this as
			// policy rather than silently repairing it" [04 §4.3]. Killing the
			// host process is not a behavior we can host, so this is a named
			// divergence (INVARIANTS I11's bounds-check exception): the thread
			// dies and a diagnostic is recorded, nothing is pushed.
			if b == 0 || (a == -2147483648 && b == -1) {
				v.diagnostics = append(v.diagnostics, "cob: divide by zero or overflow") // [P2-03] fallback diagnostic
				v.killThread(idx)
				return
			}
			if t.SP >= len(t.Stack) {
				v.killThread(idx)
				return
			}
			t.stackPush(a / b) // trunc toward zero [01 §8] I3
			t.PC += 1
		case 0x10035000: // and [04 §4.3]
			b, _ := t.stackPop()
			a, _ := t.stackPop()
			if t.SP >= len(t.Stack) {
				v.killThread(idx)
				return
			}
			t.stackPush(a & b)
			t.PC += 1
		case 0x10036000: // or [04 §4.3]
			b, _ := t.stackPop()
			a, _ := t.stackPop()
			if t.SP >= len(t.Stack) {
				v.killThread(idx)
				return
			}
			t.stackPush(a | b)
			t.PC += 1
		case 0x10037000: // xor [04 §4.3]
			b, _ := t.stackPop()
			a, _ := t.stackPop()
			if t.SP >= len(t.Stack) {
				v.killThread(idx)
				return
			}
			t.stackPush(a ^ b) // raw word xor [04 §4.3][P1-11]
			t.PC += 1
		case 0x10038000: // not in place [04 §4.3]
			if t.SP > 0 {
				t.Stack[t.SP-1] = ^t.Stack[t.SP-1]
			}
			t.PC += 1
		case 0x10041000: // random [04 §4.3]
			high, _ := t.stackPop()
			low, _ := t.stackPop()
			// The bound is computed in 32 bits and wraps when high < low,
			// reproducing retail; the stream itself returns 0 without
			// advancing for a bound below two [01 §7.1] I4, so the draw is
			// unconditional here and the stream decides.
			bound := uint32(int64(high) - int64(low) + 1)
			res := low + int32(v.simRandN(bound))
			if t.SP >= len(t.Stack) {
				v.killThread(idx)
				return
			}
			t.stackPush(res)
			t.PC += 1
		case 0x10042000: // engine read, no arguments [04 §4.3][04 §4.4]
			// [04 §4.4]: "the zero-argument read opcode pops the identifier and
			// calls the port reader with four zero argument slots", so retail's
			// reader sees the identifier followed by four zeros — the same
			// shape the five-argument form builds.
			//
			id, _ := t.stackPop()
			out := v.readPort(id, [4]int32{})
			if t.SP >= len(t.Stack) {
				v.killThread(idx)
				return
			}
			t.stackPush(out)
			t.PC += 1
		case 0x10043000: // engine read 5-arg [04 §4.3] stack -4
			// pops 5 values (id +4 args) and pushes one [04 §4.3]
			vals := make([]int32, 5)
			for i := 0; i < 5; i++ {
				vv, _ := t.stackPop()
				vals[4-i] = vv // reverse to push order: first popped is last pushed (top)
			}
			// vals[0] is id (first pushed), vals[1..4] are args
			out := v.readPort(vals[0], [4]int32{vals[1], vals[2], vals[3], vals[4]})
			if t.SP >= len(t.Stack) {
				v.killThread(idx)
				return
			}
			t.stackPush(out)
			t.PC += 1
		case 0x10044000: // cargo-membership query [04 §4.4][R-COB-03 §5]
			// Not an engine port read. [fmt cob] and [04 R-COB-03 §5] settle
			// it: the one-argument query walks THIS unit's cargo list comparing
			// each entry's sixteen-bit identifier against the popped value,
			// pushing 1 on the first match and 0 if the list is empty or
			// exhausted. The popped word is a unit identifier, never a port
			// number, so the earlier port-table route was wrong.
			//
			// The list is the owning unit's own cargo linkage, bound by
			// BindTransportQueries; a VM with no owner keeps the established
			// empty-list answer. Neither this opcode nor 0x10045000 appears in
			// any of the 841 retail COBs (asset census, [fmt cob]), so no
			// shipped script observes the difference.
			cargoID, _ := t.stackPop() // the cargo identifier
			var carried int32
			if v.cargoContains != nil && v.cargoContains(cargoID) {
				carried = 1
			}
			if t.SP >= len(t.Stack) {
				v.killThread(idx)
				return
			}
			t.stackPush(carried)
			t.PC += 1
		case 0x10045000: // carrier-identity query [04 §4.4][R-COB-03 §5] +1
			// Also not a port read, and it carries no selector anywhere: it
			// pops nothing and pushes one value — this unit's carrier
			// back-pointer's identifier, or 0 when the unit is not being
			// carried [04 R-COB-03 §5]. The earlier reading, that the port
			// selector lived in the instruction's low byte, is retracted; §4.4
			// corrected its own "pushes the first cargo identifier" text at the
			// same time. The back-pointer is the owning unit's own, bound by
			// BindTransportQueries; a VM with no owner is not carried, so 0
			// remains the established answer there.
			var carrier int32
			if v.carrierIdentity != nil {
				carrier = v.carrierIdentity()
			}
			if t.SP >= len(t.Stack) {
				v.killThread(idx)
				return
			}
			t.stackPush(carrier)
			t.PC += 1
		case 0x10051000: // less-than signed [04 §4.3]
			b, _ := t.stackPop()
			a, _ := t.stackPop()
			if a < b {
				t.stackPush(1)
			} else {
				t.stackPush(0)
			}
			t.PC += 1
		case 0x10052000: // less-or-equal [04 §4.3]
			b, _ := t.stackPop()
			a, _ := t.stackPop()
			if a <= b {
				t.stackPush(1)
			} else {
				t.stackPush(0)
			}
			t.PC += 1
		case 0x10053000: // greater-than [04 §4.3]
			b, _ := t.stackPop()
			a, _ := t.stackPop()
			if a > b {
				t.stackPush(1)
			} else {
				t.stackPush(0)
			}
			t.PC += 1
		case 0x10054000: // greater-or-equal [04 §4.3]
			b, _ := t.stackPop()
			a, _ := t.stackPop()
			if a >= b {
				t.stackPush(1)
			} else {
				t.stackPush(0)
			}
			t.PC += 1
		case 0x10055000: // equal [04 §4.3]
			b, _ := t.stackPop()
			a, _ := t.stackPop()
			if a == b {
				t.stackPush(1)
			} else {
				t.stackPush(0)
			}
			t.PC += 1
		case 0x10056000: // not-equal [04 §4.3]
			b, _ := t.stackPop()
			a, _ := t.stackPop()
			if a != b {
				t.stackPush(1)
			} else {
				t.stackPush(0)
			}
			t.PC += 1
		case 0x10057000: // logical and [04 §4.3]
			b, _ := t.stackPop()
			a, _ := t.stackPop()
			if a != 0 && b != 0 {
				t.stackPush(1)
			} else {
				t.stackPush(0)
			}
			t.PC += 1
		case 0x10058000: // logical or [04 §4.3]
			b, _ := t.stackPop()
			a, _ := t.stackPop()
			if a != 0 || b != 0 {
				t.stackPush(1)
			} else {
				t.stackPush(0)
			}
			t.PC += 1
		case 0x10059000: // word xor (not boolean) [04 §4.3]
			b, _ := t.stackPop()
			a, _ := t.stackPop()
			t.stackPush(a ^ b)
			t.PC += 1
		case 0x1005a000: // logical not [04 §4.3] stack 0 in place
			if t.SP > 0 {
				if t.Stack[t.SP-1] == 0 {
					t.Stack[t.SP-1] = 1
				} else {
					t.Stack[t.SP-1] = 0
				}
			}
			t.PC += 1
		case 0x10061000: // start-script [04 §4.3] E C14
			if t.PC+2 >= len(v.prog.Code) {
				v.killThread(idx)
				return
			}
			scriptID := int(v.prog.Code[t.PC+1])
			argc := int(v.prog.Code[t.PC+2])
			// The id is an index into the entry-point table; out of range is
			// the bad-id arm [04 §4.3] C14.
			targetPC := codeIndexForID(v.prog, scriptID)
			badID := targetPC == -1
			newIdx := -1
			if !badID {
				// Lowest free thread slot, ascending 0..7 [04 §4.2] [I1].
				for i := 0; i < 8; i++ {
					if v.Threads[i].Status == ThreadIdle {
						newIdx = i
						break
					}
				}
			}
			noSlot := newIdx == -1
			if badID || noSlot {
				// Retain arguments [04 §4.3] C14: do not pop, simply advance.
				t.PC += 3
				continue
			}
			// The native record has no semantic arity cap. Avoid an out-of-window
			// host write for malformed bytecode; native overflow is unknown.
			if argc < 0 || argc > threadWindowWords {
				v.killThread(idx)
				return
			}
			// Pop argc args from caller [04 §4.3]
			args := make([]int32, argc)
			for i := argc - 1; i >= 0; i-- {
				val, _ := t.stackPop()
				args[i] = val
			}
			// Copy into new thread: first popped lands highest (window word
			// argc−1), preserving push order. The child starts at an EMPTY logical depth (SP 0)
			// with the arguments physically in window words 0..argc−1 — the
			// engine starter's depth=arity−1 shape is deliberately not used
			// here [04 §4.3][R-COB-01 §1]. The compiled alloc-local prologue
			// raises the depth over the placed words, so local addressing is
			// identical for both start forms [04 §4.3].
			// The child is a new allocation: it inherits its parent's signal
			// mask and nothing else. In particular it never inherits the
			// completion receiver or the unconsumed return of whatever last
			// occupied this slot [04 §4.2][04 §4.3].
			v.claimThread(newIdx)
			nt := &v.Threads[newIdx]
			nt.Status = ThreadRunning
			v.activeThreadCount++
			nt.PC = targetPC
			nt.SignalMask = t.SignalMask // inherited [04 §4.3]
			nt.WaitThread = -1
			nt.WaitPiece = -1
			nt.WaitAxis = -1
			nt.Sleep = 0
			nt.SP = 0 // empty logical depth [04 §4.3]
			for i := 0; i < argc && i < len(nt.Stack); i++ {
				nt.Stack[i] = args[i]
			}
			// Caller continues
			t.PC += 3
		case 0x10062000: // call-script [04 §4.3] E C14
			if t.PC+2 >= len(v.prog.Code) {
				v.killThread(idx)
				return
			}
			scriptID := int(v.prog.Code[t.PC+1])
			argc := int(v.prog.Code[t.PC+2])
			targetPC := codeIndexForID(v.prog, scriptID)
			badID := targetPC == -1
			// Find free slot
			newIdx := -1
			for i := 0; i < 8; i++ {
				if v.Threads[i].Status == ThreadIdle {
					newIdx = i
					break
				}
			}
			if badID || newIdx == -1 {
				// Retain args, record wait -1, block anyway [04 §4.3] C14
				t.WaitThread = -1
				t.Status = ThreadWaitCall
				t.PC += 3
				return // leak
			}
			// Keep malformed bytecode inside the host's physical window; native
			// behavior beyond that record is unknown.
			if argc < 0 || argc > threadWindowWords {
				v.killThread(idx)
				return
			}
			args := make([]int32, argc)
			for i := argc - 1; i >= 0; i-- {
				val, _ := t.stackPop()
				args[i] = val
			}
			// A called script is a new allocation on the same terms as a started
			// one: mask inherited, receiver and pending return not [04 §4.3].
			v.claimThread(newIdx)
			nt := &v.Threads[newIdx]
			nt.Status = ThreadRunning
			v.activeThreadCount++
			nt.PC = targetPC
			nt.SignalMask = t.SignalMask
			nt.WaitThread = -1
			nt.WaitPiece = -1
			nt.WaitAxis = -1
			nt.Sleep = 0
			nt.SP = 0 // empty logical depth; args sit in window words 0..argc−1 [04 §4.3]
			for i := 0; i < argc && i < len(nt.Stack); i++ {
				nt.Stack[i] = args[i]
			}
			// Block caller [04 §4.2]
			t.WaitThread = newIdx
			t.Status = ThreadWaitCall
			t.PC += 3
			return
		case 0x10063000: // reserved pop-N [04 §4.3][P1-11] — pop-count-and-continue, opcode E shape: PC+1 script id (ignored), PC+2 count, pop count discards, PC+=3, 0 producer 835 scripts
			if t.PC+2 >= len(v.prog.Code) {
				v.killThread(idx)
				return
			}
			argc := int(v.prog.Code[t.PC+2])
			// Retail uses a four-word temporary for this reserved form. Counts
			// above four or above the current logical window are malformed; stop
			// the thread at Nanolathe's bounds-check boundary rather than writing
			// outside Go state [R-COB-04 §6].
			if argc < 0 || argc > 4 || argc > t.SP {
				v.diagnostics = append(v.diagnostics, "cob: reserved pop count outside four-word window")
				v.killThread(idx)
				return
			}
			// Pop count values into discarded temporary [R-COB-04 §6].
			for i := 0; i < argc; i++ {
				t.stackPop()
			}
			t.PC += 3
		case 0x10064000: // jump [04 §4.3] D
			if t.PC+1 >= len(v.prog.Code) {
				v.killThread(idx)
				return
			}
			target := int(v.prog.Code[t.PC+1]) // word index absolute [04 §4.3] C12
			if target < 0 || target >= len(v.prog.Code) {
				v.killThread(idx)
				return
			}
			t.PC = target // set absolutely [04 §4.3] C12
		case 0x10065000: // return [04 §4.3] ON-04 Aim completion value
			ret, _ := t.stackPop() // popped value delivered to completion callback when set [04 §4.3] [06 §3.3] ON-04
			// ON-04: store explicit return value for Aim handshake; signal/abnormal termination never sets it [04 §5.3]
			v.lastReturnValue[idx] = ret
			v.lastReturnValid[idx] = true
			v.lastReturnIdentity[idx] = v.threadIdentity[idx]
			// The return opcode "pops the top value, delivers it to the
			// thread's completion receiver when one is set, releases the slot,
			// and wakes threads waiting for that slot" [04 §4.2]. Delivering
			// here, from the allocation that actually returned, is the whole of
			// the ownership rule: nothing that happens to this slot afterwards —
			// a reuse in this same drain included — can reach this receiver, and
			// no later occupant inherits it.
			fn := v.onReturn[idx]
			v.onReturn[idx] = nil
			if fn != nil {
				fn(ret)
			}
			// Free thread and wake blocked callers [04 §4.2]
			v.killThread(idx)
			v.wakeCallers(idx)
			return
		case 0x10066000: // jump-if-false [04 §4.3] D
			if t.PC+1 >= len(v.prog.Code) {
				v.killThread(idx)
				return
			}
			target := int(v.prog.Code[t.PC+1])
			cond, _ := t.stackPop()
			if cond == 0 {
				if target < 0 || target >= len(v.prog.Code) {
					v.killThread(idx)
					return
				}
				t.PC = target
			} else {
				t.PC += 2
			}
		case 0x10067000: // signal [04 §4.3]
			mask, _ := t.stackPop()
			// Kill every thread whose mask intersects mask, waking waiters [04 §4.3]
			selfKilled := false
			// Need to consider all threads; collect toKill first then kill to avoid modifying while iterating?
			// We'll loop repeatedly as signalMask does.
			// For opcode we can reuse signalMask helper but need self-killed flag.
			// Implement inline to capture self.
			// First pass: find intersections
			toKill := make([]int, 0, 8)
			for i := 0; i < 8; i++ {
				ot := &v.Threads[i]
				if ot.Status == ThreadIdle {
					continue
				}
				if ot.SignalMask&mask != 0 {
					toKill = append(toKill, i)
				}
			}
			for _, k := range toKill {
				if k == idx {
					selfKilled = true
				}
				v.killThread(k)
				v.wakeCallers(k)
			}
			if selfKilled {
				return // only when it kills itself does it suspend [04 §4.3]
			}
			t.PC += 1
		case 0x10068000: // set-signal-mask [04 §4.3]
			mask, _ := t.stackPop()
			t.SignalMask = mask
			t.PC += 1
		case 0x10071000: // explode [04 §4.3] [04 §4.5]
			if t.PC+1 >= len(v.prog.Code) {
				v.killThread(idx)
				return
			}
			flags, _ := t.stackPop() // flags [04 §4.3] [04 §4.5]
			// Random-draw census: bitmap-only consumes no simulation draws. A
			// physical record consumes six in order 3000, 3000, 3000, 40, 10,
			// 40 before source hiding and arena admission [04 R-COB-04 §1].
			if flags&0x20 == 0 {
				piece := int(v.prog.Code[t.PC+1])
				physical := v.physicalExplosion(flags)
				// A physical spawner hides its source before any arena allocation;
				// bitmap-only leaves it visible [04 R-COB-04 §1].
				if !v.setRenderFlag(piece, 0x01, false) {
					v.killThread(idx)
					return
				}
				physical.Source = v.explosionSource(piece)
				if v.explosionSink != nil {
					if physical.Flags&ExplosionShatter != 0 {
						v.explosionSink.AdmitShatter(ShatterExplosion{PhysicalExplosion: physical})
					} else {
						v.explosionSink.AdmitWholePiece(WholePieceExplosion{PhysicalExplosion: physical})
					}
				}
			}
			// Bitmap requests run in ascending bit order even when bitmap-only
			// suppressed physical debris. Their sink owns the fixed effect pool;
			// this VM retains no event queue [04 R-COB-04 §1] [04 R-COB-04 §4].
			if v.explosionSink != nil {
				piece := int(v.prog.Code[t.PC+1])
				source := v.explosionSource(piece)
				for bit, kind := range [...]BitmapExplosionKind{BitmapExplosionPrimary, BitmapExplode2, BitmapExplode3, BitmapExplode4, BitmapExplode5, BitmapNuke1} {
					if flags&(1<<uint(bit+8)) != 0 {
						v.explosionSink.AdmitBitmap(BitmapExplosion{Source: source, Kind: kind})
					}
				}
			}
			t.PC += 2
		case 0x10082000: // engine write [04 §4.3]
			// The shipped compiler emits the identifier first and the value
			// second, so the value sits on top of the stack and the identifier
			// is popped last: `push 5; push 1; set` is port 5 <- 1 in every
			// stock script census (armlab, armcom, and friends all follow it).
			// A reading that pushed the value first and popped the identifier
			// first inverted every engine write on retail content — `set
			// INBUILDSTANCE to 1` became a port-1 write of value 5 [R-P0-10].
			val, _ := t.stackPop()
			id, _ := t.stackPop()
			// The SCRIPT-TOUCHED MARKER, raised here and not inside any port
			// arm [04 R-COB-06]. Retail's dispatch ORs bit 2 of the owning
			// unit's order-event word — order gate bit 0x4 — from all six
			// write arms AND from the fall-through an identifier with no write
			// arm takes, which is also where the range check sends an
			// identifier outside 1..20. So the raise is unconditional and
			// precedes the dispatch: every execution of this opcode raises it,
			// valid identifier or not. The bit carries no value; it says only
			// that this unit's script executed an engine write, and the order
			// pump merging `u.Pending` is its one consumer [04 §3.3].
			v.raiseScriptTouched()
			// If id has no write arm only sets script-touched marker [04 §4.4]; we treat as no-op beyond hook.
			v.writePort(id, val)
			t.PC += 1
		case 0x10083000: // attach-unit [04 §4.3]
			extra, _ := t.stackPop()
			piece, _ := t.stackPop()
			unit, _ := t.stackPop()
			if v.transportAttach != nil {
				v.transportAttach(unit, piece, extra)
			}
			t.PC += 1
		case 0x10084000: // detach-unit [04 §4.3]
			unit, _ := t.stackPop()
			if v.transportDrop != nil {
				v.transportDrop(unit)
			}
			t.PC += 1
		default:
			// Unmatched keys release the thread without waking callers [04 §4.2][04 §4.3].
			v.killThread(idx)
			return
		}
	}
}

// readPortDefault is the default engine read when no port handler is bound.
// It returns 0 for identifiers outside 1..20 [04 §4.4] C15, else 0 as stock
// default.
//
// The QUERY ports 7 through 16 have no zero-answer default in retail — the
// engine always has a unit, a piece table and terrain bound, so there is no
// "unbound" case to compare against and no retail behavior this arm could be
// wrong about. A fixture VM built without a session falls through here and
// reads 0 for a piece position, another unit's position or height, a bearing,
// a distance, a hypotenuse or a ground height, none of which is the real
// answer and none of which is retail's off-map −0x10000 [04 §4.4]. Every
// production VM binds 7 through 16 through internal/session's per-unit binding
// (composition.go and cob_query_ports.go), so only VM fixtures built directly
// by tests can observe this default.
func (v *VM) readPortDefault(id int32, args []int32) int32 {
	if !IsEnginePort(id) {
		return 0 // [04 §4.4] outside range reads zero
	}
	// Without WU-06-7 binding, reads are 0; writes only set marker.
	return 0
}

// writePortDefault is the default engine write when no handler bound.
func (v *VM) writePortDefault(id, val int32) {
	// Nothing is written on either side of IsEnginePort's range: an unbound
	// in-range port sets only the touched marker, which the caller has already
	// done, and an out-of-range id is ignored entirely [04 §4.4].
}

// helpers for Program id mapping.

// codeIndexForID resolves a script id to the code word the new thread starts
// at. Retail admits an id by ONE test, the signed range `0 <= id <
// scriptCount` where the count is the compiled program's own header word, and
// takes the PC from the script entry-point table at that index [04 §4.3] C14.
// ScriptsByID is that table; a negative result is the bad-id arm.
//
// A second arm used to stand beside it: when a Program carried no entry-point
// table at all, the id was treated as a direct code word index. Nothing Load
// produces looks like that — it fills Scripts[name] and ScriptsByID[i] from the
// one entry-point array — so the arm existed for hand-built fixture programs,
// and it made an out-of-range id resolve to a real PC instead of reaching the
// bad-id arm the opcode is written around. Fixtures declare their table.
func codeIndexForID(prog *Program, id int) int {
	if prog == nil || id < 0 || id >= len(prog.ScriptsByID) {
		return -1
	}
	return prog.ScriptsByID[id]
}

// stack helpers operate on the complete physical thread window.

func (t *Thread) stackPush(val int32) {
	if t.SP >= len(t.Stack) {
		return // direct opcode paths stop the thread before reaching this host guard
	}
	t.Stack[t.SP] = val
	t.SP++
}

// stackPushScratch advances SP without writing so the slot's stale frame
// contents surface on the next pop [04 §4.3] C14 "uninitialized scratch".
func (t *Thread) stackPushScratch() {
	if t.SP >= len(t.Stack) {
		return // direct opcode paths stop the thread before reaching this host guard
	}
	t.SP++
}

func (t *Thread) stackPop() (int32, bool) {
	if t.SP <= 0 {
		return 0, false // underflow returns 0 [04 §4.3] no fault
	}
	t.SP--
	return t.Stack[t.SP], true
}

func (t *Thread) getLocal(idx int) int32 {
	if idx < 0 || idx >= len(t.Stack) {
		return 0
	}
	// Window word idx [04 §4.3] "local i is window word i"
	return t.Stack[idx]
}

func (t *Thread) setLocal(idx int, val int32) {
	if idx < 0 || idx >= len(t.Stack) {
		return
	}
	t.Stack[idx] = val
}

func (v *VM) getStatic(idx int) int32 {
	if idx < 0 || idx >= len(v.statics) {
		return 0
	}
	return v.statics[idx]
}

func (v *VM) setStatic(idx int, val int32) {
	if idx < 0 || idx >= len(v.statics) {
		return
	}
	v.statics[idx] = val
}

func (v *VM) simRandN(bound uint32) uint32 {
	if v.simRng != nil {
		return v.simRng.Uint32n(bound) // [01 §7.1] I4
	}
	// DET-01: no global fallback; production requires injected RNG.
	// Tests must bind a stream via SetSimulationRNG; otherwise return 0 without advancing.
	return 0
}
