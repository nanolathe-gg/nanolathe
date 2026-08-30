package cob

import (
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// SetSimulationRNG binds the session-owned simulation stream to the bridged
// VM. No global stream is modified [01 §7.1] I4.
func (b *CallbackBridge) SetSimulationRNG(sim *rng.Simulation) {
	if b != nil && b.VM != nil {
		b.VM.SetSimulationRNG(sim)
	}
}

// SetSFXSink binds the VM's presentation-only emit-sfx sink and visibility
// predicate [GAP T15] C19.
func (b *CallbackBridge) SetSFXSink(sink SFXSink, visible func(piece int, sfxType int32) bool) {
	if b == nil || b.VM == nil {
		return
	}
	b.VM.SetSFXSink(sink)
	b.VM.SetSFXVisible(visible)
}

// SetPresentationSink adapts a typed session event sink to emit-sfx.
func (b *CallbackBridge) SetPresentationSink(sink PresentationSink) {
	if b == nil || b.VM == nil {
		return
	}
	if sink == nil {
		b.VM.SetSFXSink(nil)
		return
	}
	b.VM.SetSFXSink(PresentationSinkAdapter{Sink: sink})
}

// BindRenderFlags binds the unit-owned render-piece record [04 §"Piece flag polarity"] [R-COB-01 §1].
// When bound, the six flag opcodes write into the unit's storage via the bridge, not the VM-local array.
// The getter and setter capture the unit's RenderPieceFlags slice; the VM delegates every show/hide,
// cache/dont-cache, shade/dont-shade write there.
func (b *CallbackBridge) BindRenderFlags(get func() []uint8, set func(piece int, mask uint8, set bool) bool) {
	if b == nil || b.VM == nil {
		return
	}
	b.VM.BindRenderFlagHandlers(get, set)
}

// BindRenderFlagsSlice is the direct slice form of BindRenderFlags [04 §"Piece flag polarity"].
// The VM shares the underlying array; writes via the VM affect the unit and vice versa.
func (b *CallbackBridge) BindRenderFlagsSlice(flags []uint8) {
	if b == nil || b.VM == nil {
		return
	}
	b.VM.BindRenderFlags(flags)
}

// CallbackMode is the three engine-to-COB adapter modes [04 §4.2].
type CallbackMode uint8

const (
	ModeDeferred CallbackMode = iota + 1 // D: allocate and return; normal drain later
	ModeQuery                            // Q: one-slot synchronous call, no piece pass
)

// CallbackReturn is delivered to a deferred callback receiver only when the
// script explicitly returns. Failed receiver-bearing starts deliver Value=0
// with Explicit=false; signal and abnormal termination deliver nothing [04
// §4.2][04 §4.3][04 §5.3].
type CallbackReturn struct {
	Name     string
	Mode     CallbackMode
	Thread   int
	Value    int32
	Explicit bool
}

// LifecycleEvent identifies one actual callback boundary. It is emitted only
// when a sink is installed; the normal bridge has no trace allocation or
// callback side effect [04 §4.2].
type LifecycleEvent struct {
	Tick   uint32
	Source uint16
	Name   string
	Mode   CallbackMode
	Thread int
	Phase  string // start, enqueue, dequeue, finish, finish-abnormal
}

// LifecycleSink observes callback boundaries in their actual execution order.
// A sink must not call back into the bridge or VM.
type LifecycleSink func(LifecycleEvent)

// CallbackReceiver is the standardized completion receiver for D/I bridge
// operations. Receivers are called synchronously by Bridge.Drain after the VM
// has observed an explicit return, in ascending thread-slot order [04 §4.2].
type CallbackReceiver func(CallbackReturn)

// CallbackResult describes a callback start and whether a D start performed
// its wake barrier. Values are the four physical callback cells; only the
// cells requested by the named operation are semantically copied back [04 §4.2].
type CallbackResult struct {
	Name      string
	Mode      CallbackMode
	Wake      bool // true when the D start performs the all-slot delta-zero barrier [04 §4.2]
	Started   bool
	Completed bool
	Thread    int
	Values    [4]int32
}

// QueryValue returns cell zero from a callback result.
func (r CallbackResult) QueryValue() int32 { return r.Values[0] }

// WeaponSlot selects the authored primary/secondary/tertiary callback names.
type WeaponSlot uint8

const (
	WeaponPrimary WeaponSlot = iota
	WeaponSecondary
	WeaponTertiary
)

// CallbackBridge is the only typed production surface for engine-to-COB
// callbacks. It owns mode dispatch and receiver polling; downstream systems
// should not call VM.Start for researched callbacks [04 §4.2][04 §5.1].
type CallbackBridge struct {
	VM *VM

	createInvoked    bool
	pending          [8]pendingCallback
	lifecyclePending [8]pendingCallback
	lifecycle        LifecycleSink
	lifecycleTick    uint32
	lifecycleSrc     uint16
}

type pendingCallback struct {
	active   bool
	name     string
	mode     CallbackMode
	receiver CallbackReceiver
}

// NewCallbackBridge returns a bridge for vm. A nil VM is retained as an
// explicit failed production binding; operations report Started=false.
func NewCallbackBridge(vm *VM) *CallbackBridge { return &CallbackBridge{VM: vm} }

// SetLifecycleSink enables opt-in VM callback observations.
func (b *CallbackBridge) SetLifecycleSink(sink LifecycleSink) {
	if b != nil {
		b.lifecycle = sink
	}
}

// SetLifecycleContext sets the selected source and authoritative tick attached
// to subsequent events. It does not affect VM execution.
func (b *CallbackBridge) SetLifecycleContext(tick uint32, source uint16) {
	if b != nil {
		b.lifecycleTick, b.lifecycleSrc = tick, source
	}
}

func (b *CallbackBridge) lifecycleEvent(name string, mode CallbackMode, thread int, phase string) {
	if b != nil && b.lifecycle != nil {
		b.lifecycle(LifecycleEvent{Tick: b.lifecycleTick, Source: b.lifecycleSrc, Name: name, Mode: mode, Thread: thread, Phase: phase})
	}
}

// Create invokes Create exactly once as a deferred start with wake=1. A
// second call is rejected and does not schedule another callback [R-CB-01 §2].
func (b *CallbackBridge) Create() CallbackResult {
	if b == nil || b.VM == nil || b.createInvoked {
		if b != nil && b.VM == nil {
			b.lifecycleEvent("Create", ModeDeferred, -1, "start-failed")
		}
		return CallbackResult{Name: "Create", Mode: ModeDeferred, Thread: -1}
	}
	b.createInvoked = true
	ok := b.VM.StartByName("Create", nil)
	thread := b.VM.LastStartedThread()
	if !ok {
		// A failed named start is an observed lifecycle boundary too; retaining
		// it makes missing scripts and exhausted slots distinguishable from a
		// callback that was never attempted [04 §4.2].
		b.lifecycleEvent("Create", ModeDeferred, -1, "start-failed")
	}
	if ok && thread >= 0 && thread < len(b.lifecyclePending) {
		b.lifecycleEvent("Create", ModeDeferred, thread, "start")
		b.lifecyclePending[thread] = pendingCallback{active: true, name: "Create", mode: ModeDeferred}
	}
	if ok {
		b.VM.Drain(0)
	}
	b.collectReturns()
	completed := ok && (thread < 0 || !b.VM.IsThreadAlive(thread))
	return CallbackResult{Name: "Create", Mode: ModeDeferred, Wake: ok, Started: ok, Completed: completed, Thread: thread}
}

// CreateInvoked reports whether this bridge has consumed its one Create slot.
func (b *CallbackBridge) CreateInvoked() bool { return b != nil && b.createInvoked }

// Drain executes the normal VM drain and delivers explicit deferred returns.
// The VM itself remains authoritative for thread and piece state [04 §4.2].
func (b *CallbackBridge) Drain(delta int) {
	if b == nil || b.VM == nil {
		return
	}
	b.VM.Drain(delta)
	b.collectReturns()
}

// Deferred starts one receiver-bearing D callback. Missing name, invalid
// identity, and thread exhaustion deliver zero to receiver immediately; no
// receiver is called for an omitted receiver [04 §4.2][04 §4.3].
func (b *CallbackBridge) Deferred(name string, args []int32, receiver CallbackReceiver) CallbackResult {
	result := CallbackResult{Name: name, Mode: ModeDeferred, Thread: -1}
	if b == nil || b.VM == nil {
		b.lifecycleEvent(name, ModeDeferred, -1, "start-failed")
		if receiver != nil {
			receiver(CallbackReturn{Name: name, Mode: ModeDeferred, Thread: -1, Value: 0})
		}
		return result
	}
	if !b.VM.StartByName(name, args) {
		b.lifecycleEvent(name, ModeDeferred, -1, "start-failed")
		if receiver != nil {
			receiver(CallbackReturn{Name: name, Mode: ModeDeferred, Thread: -1, Value: 0})
		}
		return result
	}
	thread := b.VM.LastStartedThread()
	result.Started, result.Thread = true, thread
	b.lifecycleEvent(name, ModeDeferred, thread, "start")
	if thread >= 0 && thread < len(b.lifecyclePending) {
		b.lifecyclePending[thread] = pendingCallback{active: true, name: name, mode: ModeDeferred}
	}
	if receiver != nil && thread >= 0 && thread < len(b.pending) {
		b.pending[thread] = pendingCallback{active: true, name: name, mode: ModeDeferred, receiver: receiver}
	}
	return result
}

// DeferredWake starts a deferred callback and immediately performs the
// callback's wake barrier. Retail labels these callbacks D with wake=1: the
// start is deferred in mode, while the all-slot delta-zero drain runs before
// the producer continues [04 §4.2][R-CB-01 §2].
func (b *CallbackBridge) DeferredWake(name string, args []int32, receiver CallbackReceiver) CallbackResult {
	result := CallbackResult{Name: name, Mode: ModeDeferred, Thread: -1}
	if b == nil || b.VM == nil {
		b.lifecycleEvent(name, ModeDeferred, -1, "start-failed")
		if receiver != nil {
			receiver(CallbackReturn{Name: name, Mode: ModeDeferred, Thread: -1, Value: 0})
		}
		return result
	}
	if !b.VM.StartByName(name, args) {
		b.lifecycleEvent(name, ModeDeferred, -1, "start-failed")
		if receiver != nil {
			receiver(CallbackReturn{Name: name, Mode: ModeDeferred, Thread: -1, Value: 0})
		}
		return result
	}
	thread := b.VM.LastStartedThread()
	result.Started, result.Wake, result.Thread = true, true, thread
	b.lifecycleEvent(name, ModeDeferred, thread, "start")
	if thread >= 0 && thread < len(b.lifecyclePending) {
		b.lifecyclePending[thread] = pendingCallback{active: true, name: name, mode: ModeDeferred}
	}
	if receiver != nil && thread >= 0 && thread < len(b.pending) {
		b.pending[thread] = pendingCallback{active: true, name: name, mode: ModeDeferred, receiver: receiver}
	}
	b.VM.Drain(0)
	b.collectReturns()
	result.Completed = !b.VM.IsThreadAlive(thread)
	return result
}

// Query runs a synchronous Q callback with the exact supplied four-cell
// seeds. Missing entries/full pools leave values untouched. A sleeping/waiting
// callback returns partial values and remains active, while an explicit return
// reports Completed=true; no interpolation or receiver is involved [04 §4.2].
// The query forces the allocated slot's completion receiver to none [R-COB-01
// §1]: any stale pending receiver registered against the reused slot is
// dropped, so a blocked query that later resumes can never revise the values
// the host already copied back.
func (b *CallbackBridge) Query(name string, seeds [4]int32) CallbackResult {
	result := CallbackResult{Name: name, Mode: ModeQuery, Thread: -1, Values: seeds}
	if b == nil || b.VM == nil {
		return result
	}
	pc, ok := b.VM.ScriptPC(name)
	if !ok {
		return result
	}
	values := seeds
	started, completed := b.VM.CallQuery(pc, values[:])
	result.Started, result.Completed = started, completed
	result.Values = values
	if started && b.VM.lastQueryThread >= 0 && b.VM.lastQueryThread < len(b.pending) {
		// Receiver forced none for the synchronous query [R-COB-01 §1].
		b.pending[b.VM.lastQueryThread] = pendingCallback{}
	}
	return result
}

// QueryPiece is a Q callback with one semantically copied cell and zeroed
// unused cells. It preserves the exact seed and partial-result behavior.
func (b *CallbackBridge) QueryPiece(name string, seed int32) CallbackResult {
	var seeds [4]int32
	seeds[0] = seed
	return b.Query(name, seeds)
}

// QueryBuildInfo performs the placement Q query with cell zero seeded -1 and
// cells 1..3 seeded zero [04 §5.1][04 §5.3].
func (b *CallbackBridge) QueryBuildInfo() CallbackResult { return b.QueryPiece("QueryBuildInfo", -1) }

// QueryNanoPiece performs the construction Q query with cell zero seeded 0
// [R-P0-06][04 §5.3].
func (b *CallbackBridge) QueryNanoPiece() CallbackResult { return b.QueryPiece("QueryNanoPiece", 0) }

// QueryWeapon performs QueryPrimary/Secondary/Tertiary with seed 0.
func (b *CallbackBridge) QueryWeapon(slot WeaponSlot) CallbackResult {
	name, ok := weaponCallbackName(slot, "Query")
	if !ok {
		return CallbackResult{Name: "", Mode: ModeQuery, Thread: -1}
	}
	return b.QueryPiece(name, 0)
}

// AimFromWeapon performs AimFrom* with sentinel seed -1. A missing entry
// leaves -1; callers must then invoke QueryWeapon, exactly as retail does
// [R-P0-07][04 §5.3].
func (b *CallbackBridge) AimFromWeapon(slot WeaponSlot) CallbackResult {
	name, ok := weaponCallbackName(slot, "AimFrom")
	if !ok {
		return CallbackResult{Name: "", Mode: ModeQuery, Thread: -1, Values: [4]int32{-1, 0, 0, 0}}
	}
	return b.QueryPiece(name, -1)
}

// AimPiece performs the established AimFrom→Query fallback and returns the
// selected piece in cell zero. Invalid/negative authored values remain the
// caller's normal root-piece fallback; no aim authorization is granted.
func (b *CallbackBridge) AimPiece(slot WeaponSlot) CallbackResult {
	result := b.AimFromWeapon(slot)
	if result.QueryValue() == -1 {
		result = b.QueryWeapon(slot)
	}
	return result
}

// SweetSpot performs the separate target-piece Q query with seed 0.
func (b *CallbackBridge) SweetSpot() CallbackResult { return b.QueryPiece("SweetSpot", 0) }

// Aim starts AimPrimary/Secondary/Tertiary deferred with unsigned heading and
// pitch values. A receiver is required by turret/vertical families to grant
// readiness only on an explicit nonzero return [R-P0-07].
func (b *CallbackBridge) Aim(slot WeaponSlot, heading, pitch uint16, receiver CallbackReceiver) CallbackResult {
	name, ok := weaponCallbackName(slot, "Aim")
	if !ok {
		return CallbackResult{Name: "", Mode: ModeDeferred, Thread: -1}
	}
	return b.Deferred(name, []int32{int32(heading), int32(pitch)}, receiver)
}

// Fire starts the matching Fire* callback deferred with zero arguments.
func (b *CallbackBridge) Fire(slot WeaponSlot) CallbackResult {
	name, ok := weaponCallbackName(slot, "Fire")
	if !ok {
		return CallbackResult{Name: "", Mode: ModeDeferred, Thread: -1}
	}
	return b.Deferred(name, nil, nil)
}

// RockUnit starts RockUnit deferred with the researched recoil arguments.
func (b *CallbackBridge) RockUnit(rel int16) CallbackResult {
	x, z := RockUnitArgs(rel)
	return b.Deferred("RockUnit", []int32{x, z}, nil)
}

// FireThenRock starts root Fire first and RockUnit second; burst clones must
// call neither operation [R-P0-07][04 §5.3]. Both callback attempts are
// independent, as the retail producers invoke each adapter separately.
func (b *CallbackBridge) FireThenRock(slot WeaponSlot, rel int16) (CallbackResult, CallbackResult) {
	fire := b.Fire(slot)
	rock := b.RockUnit(rel)
	return fire, rock
}

// Lifecycle callbacks are named wrappers retaining their researched modes.
func (b *CallbackBridge) Activate() CallbackResult      { return b.Deferred("Activate", nil, nil) }
func (b *CallbackBridge) Deactivate() CallbackResult    { return b.Deferred("Deactivate", nil, nil) }
func (b *CallbackBridge) StartBuilding() CallbackResult { return b.Deferred("StartBuilding", nil, nil) }

// StartBuildingHeading is the slot-form StartBuilding callback carrying the
// producer heading as one unsigned 16-bit argument [04 §5.3].
func (b *CallbackBridge) StartBuildingHeading(heading uint16) CallbackResult {
	return b.Deferred("StartBuilding", []int32{int32(heading)}, nil)
}
func (b *CallbackBridge) StopBuilding() CallbackResult { return b.Deferred("StopBuilding", nil, nil) }
func (b *CallbackBridge) StartMoving() CallbackResult  { return b.DeferredWake("StartMoving", nil, nil) }
func (b *CallbackBridge) StopMoving() CallbackResult   { return b.DeferredWake("StopMoving", nil, nil) }
func (b *CallbackBridge) MoveRate1() CallbackResult    { return b.DeferredWake("MoveRate1", nil, nil) }
func (b *CallbackBridge) MoveRate2() CallbackResult    { return b.DeferredWake("MoveRate2", nil, nil) }
func (b *CallbackBridge) MoveRate3() CallbackResult    { return b.DeferredWake("MoveRate3", nil, nil) }
func (b *CallbackBridge) SetSFXoccupy(v int32) CallbackResult {
	return b.DeferredWake("setSFXoccupy", []int32{v}, nil)
}
func (b *CallbackBridge) TargetCleared(slot int32) CallbackResult {
	return b.Deferred("TargetCleared", []int32{slot}, nil)
}
func (b *CallbackBridge) HitByWeapon(dir uint8) CallbackResult {
	x, z := HitByWeaponArgs(dir)
	return b.Deferred("HitByWeapon", []int32{x, z}, nil)
}
func (b *CallbackBridge) TakeDamage(percent int32) CallbackResult {
	return b.Deferred("TakeDamage", []int32{percent}, nil)
}

// Killed is the network-death-replay Killed start [R-COB-02 §1]: mode D with
// wake=1, one argument carrying the signed packet severity byte, fillers zero,
// no receiver. The D+wake start performs the all-slot delta-0 barrier plus one
// piece pass inline, so a deferred callback queued earlier on the same VM also
// runs at this flush point [R-COB-02 §2].
func (b *CallbackBridge) Killed(severity int32) CallbackResult {
	return b.DeferredWake("Killed", []int32{severity}, nil)
}

// KilledLocal is the local authoritative death query [R-COB-02 §1]: mode Q,
// four cells, cell 0 seeded with the computed severity input
// (KilledSeverity), copy-back to the caller. The variant cell (window word 1)
// is not pre-initialized by retail — the host cell holds allocator garbage
// and a script that assigns it has that value copied back [04 §5.1]. The
// serialized stack-history content an unassigned variant cell carries is an
// Unknown recorded in the research Missing list; Nanolathe seeds a
// deterministic zero host cell, and the death producer applies the
// remaining-work-fraction override after any query. Cause overrides (7 → 0/1
// without a query; 4/5/9 or positive health → 0/0 without a query) are the
// death producer's decision and bypass this method entirely [R-COB-02 §1].
func (b *CallbackBridge) KilledLocal(severityIn int32) CallbackResult {
	return b.Query("Killed", [4]int32{severityIn, 0, 0, 0})
}

// SetDirection is the general unit-update direction callback [R-COB-02 §1]:
// mode D, one argument carrying the zero-extended 16-bit direction word
// [04 §5.3], producer filler cells zero, no receiver.
func (b *CallbackBridge) SetDirection(dir uint16) CallbackResult {
	return b.Deferred("SetDirection", []int32{SetDirectionArg(dir)}, nil)
}

// SetSpeed is the general unit-update speed callback [R-COB-02 §1]: mode D,
// one argument carrying the signed global speed value shifted left by four
// [04 §5.3], issued immediately after SetDirection, no receiver.
func (b *CallbackBridge) SetSpeed(speed int32) CallbackResult {
	return b.Deferred("SetSpeed", []int32{SetSpeedGeneral(speed)}, nil)
}

// SetSpeedFootprint is the extractor-path SetSpeed callback [R-COB-02 §1]:
// mode D, one argument carrying the footprint metal sum reduced modulo 16 bits
// and sign-extended [R-CB-01 §5].
func (b *CallbackBridge) SetSpeedFootprint(sum int32) CallbackResult {
	return b.Deferred("SetSpeed", []int32{SetSpeedFootprint(sum)}, nil)
}

// SetMaxReloadTime reports the maximum authored reload over the three weapon
// slots converted to milliseconds [R-COB-02 §1]: mode D, one argument
// trunc(maxReload·1000/30) [04 §5.3]. The caller scans the slots; this method
// is issued after Create, so the deferred thread first runs in the visit's
// normal drain [R-CB-01 §2].
func (b *CallbackBridge) SetMaxReloadTime(maxReloadTicks int32) CallbackResult {
	return b.Deferred("SetMaxReloadTime", []int32{MaxReloadMillis(maxReloadTicks)}, nil)
}

// QueryTransport is the transport attachment query [R-COB-02 §1]: mode Q with
// cell 0 seeded −1 and the remaining outputs null (seeded 0, excluded from
// copy-back); a missing script leaves −1, which the consumer resolves to the
// root piece.
func (b *CallbackBridge) QueryTransport() CallbackResult {
	return b.Query("QueryTransport", QueryTransportSeed())
}

// QueryLandingPad is the air landing selection query [R-COB-02 §1]: mode Q
// with all four outputs seeded −1; the first candidate piece 0..3 to pass
// validity wins and all −1 keeps the order alive for retry.
func (b *CallbackBridge) QueryLandingPad() CallbackResult {
	return b.Query("QueryLandingPad", QueryLandingPadSeed())
}

func weaponCallbackName(slot WeaponSlot, prefix string) (string, bool) {
	if slot > WeaponTertiary {
		return "", false
	}
	names := [...]string{"Primary", "Secondary", "Tertiary"}
	return prefix + names[slot], true
}

func (b *CallbackBridge) collectReturns() {
	if b == nil || b.VM == nil {
		return
	}
	for i := 0; i < len(b.lifecyclePending); i++ {
		p := &b.lifecyclePending[i]
		if !p.active {
			continue
		}
		if b.VM.HasReturn(i) {
			b.lifecycleEvent(p.name, p.mode, i, "finish")
			*p = pendingCallback{}
		} else if !b.VM.IsThreadAlive(i) {
			b.lifecycleEvent(p.name, p.mode, i, "finish-abnormal")
			*p = pendingCallback{}
		}
	}
	for i := 0; i < len(b.pending); i++ {
		p := &b.pending[i]
		if !p.active {
			continue
		}
		if b.VM.HasReturn(i) {
			value, _ := b.VM.ConsumeReturn(i)
			if p.receiver != nil {
				p.receiver(CallbackReturn{Name: p.name, Mode: p.mode, Thread: i, Value: value, Explicit: true})
			}
			*p = pendingCallback{}
			continue
		}
		if !b.VM.IsThreadAlive(i) {
			// Signal/abnormal termination has no receiver callback [04 §5.3].
			*p = pendingCallback{}
		}
	}
}

// PresentationKind identifies only event families whose callback bridge can
// carry an already-resolved event. This adapter stores no lifetime, performs
// no allocation, and never fabricates nano/muzzle/smoke/trail/impact events.
type PresentationKind uint8

const (
	PresentationSFX PresentationKind = iota + 1
	PresentationNano
	PresentationMuzzle
	PresentationSmoke
	PresentationTrail
	PresentationImpact
)

// PresentationEvent is an exact, presentation-only payload supplied by an
// authoritative producer. Source/Target are fixed-point world positions; zero
// fields are meaningful only when the producer leaves them unspecified.
type PresentationEvent struct {
	Kind     PresentationKind
	Piece    int
	SFXType  int32
	SFXClass SFXKind
	Selector int32
	Source   [3]numeric.Fixed
	Target   [3]numeric.Fixed
}

// PresentationSink receives already-admitted COB-owned presentation events.
// The collector owns identity, ordering, visibility, and lifetime policy.
type PresentationSink interface{ EmitCOBEvent(PresentationEvent) }

// PresentationSinkAdapter adapts the existing emit-sfx VM sink to the typed
// session event sink. Other event methods forward only supplied event data;
// they do not create geometry or lifetimes [R-P0-06][GAP T15] C19.
type PresentationSinkAdapter struct{ Sink PresentationSink }

func (a PresentationSinkAdapter) EmitSFX(piece int, sfxType int32, kind SFXKind) {
	if a.Sink != nil {
		a.Sink.EmitCOBEvent(PresentationEvent{Kind: PresentationSFX, Piece: piece, SFXType: sfxType, SFXClass: kind})
	}
}
func (a PresentationSinkAdapter) Emit(event PresentationEvent) {
	if a.Sink != nil {
		a.Sink.EmitCOBEvent(event)
	}
}

// The typed helpers forward only an event already admitted by an authoritative
// producer. They assign the category named by the helper and do not invent
// positions, selectors, or lifetimes [R-P0-06][GAP T15] C19.
func (a PresentationSinkAdapter) EmitNano(event PresentationEvent) {
	event.Kind = PresentationNano
	a.Emit(event)
}
func (a PresentationSinkAdapter) EmitMuzzle(event PresentationEvent) {
	event.Kind = PresentationMuzzle
	a.Emit(event)
}
func (a PresentationSinkAdapter) EmitSmoke(event PresentationEvent) {
	event.Kind = PresentationSmoke
	a.Emit(event)
}
func (a PresentationSinkAdapter) EmitTrail(event PresentationEvent) {
	event.Kind = PresentationTrail
	a.Emit(event)
}
func (a PresentationSinkAdapter) EmitImpact(event PresentationEvent) {
	event.Kind = PresentationImpact
	a.Emit(event)
}

var _ SFXSink = PresentationSinkAdapter{}
