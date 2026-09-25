package units

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// RequiredCOBEntryPoints returns callback roots that every strict production
// unit must provide. There are none: Create is conditional on the program
// containing that entry point, and weapon/builder callbacks are required only
// by their observed consumers [R-CB-01 §2].
func RequiredCOBEntryPoints(_ *content.UnitDef) []string { return nil }

// BindCOBWithPorts is retained for the session composition test seam, whose
// caller has no owning Unit instance. New production bindings use
// BindCOBWithPortsAndVisibilityForUnit so instance-owned ports are installed
// before the D+wake Create callback. This seam retains the same strict
// model/piece checks; the extra ports are supplied before Create starts.
func BindCOBWithPorts(fs vfs.FSOps, def *content.UnitDef, mdl *model.Model, sim *rng.Simulation, sink cob.PresentationSink) (*cob.Binding, error) {
	return bindCOBWithPortsAndVisibility(fs, def, mdl, sim, sink, nil, nil, nil)
}

// BindCOBWithPortsAndVisibilityForUnit is the production binding seam for a
// live unit. Instance-owned engine ports are installed before Create runs.
func BindCOBWithPortsAndVisibilityForUnit(fs vfs.FSOps, u *Unit, mdl *model.Model, sim *rng.Simulation, sink cob.PresentationSink, visible func(piece int, sfxType int32) bool) (*cob.Binding, error) {
	return BindCOBWithPortsAndVisibilityAndContextForUnit(fs, u, mdl, sim, sink, visible, nil)
}

// BindCOBWithPortsAndVisibilityAndContextForUnit installs the session's
// world-query and mutation context before an authored Create body executes.
// The context sees a complete linked binding and may bind ports but must not
// start callbacks of its own [04 R-CB-01 §4].
func BindCOBWithPortsAndVisibilityAndContextForUnit(fs vfs.FSOps, u *Unit, mdl *model.Model, sim *rng.Simulation, sink cob.PresentationSink, visible func(piece int, sfxType int32) bool, preCreate func(*cob.Binding) error) (*cob.Binding, error) {
	if u == nil {
		return nil, fmt.Errorf("nanolathe: COB binding: nil unit")
	}
	return bindCOBWithPortsAndVisibility(fs, u.Def, mdl, sim, sink, visible, u, preCreate)
}

// PendingScriptTouched is the SCRIPT-TOUCHED MARKER: bit 2 of the unit's
// order-event word (Unit.Pending), which is order gate bit 0x4
// [04 R-COB-06]. Its only producer is the COB engine-write opcode, which
// raises it on every execution — from all six write arms and from the
// fall-through an identifier with no write arm (or no valid identifier at all)
// takes. It carries no value: it says that this unit's script executed an
// engine write, and nothing about which port or what value.
//
// Its consumers are the two order helpers that park with no deadline — the
// `INBUILDSTANCE` wait of the work orders and the ground transport's `BUSY`
// wait [04 R-AIR-01 §9] — and a record parked on the bit is re-polled by
// exactly one thing: this unit's script writing an engine port. Because the
// bit is per-unit and value-free, a write to ANY port re-polls both waits, and
// the helper's own re-test of the level byte is what decides advance or hold.
//
// The bit is raised on the UNIT, not on a record, and the pump clears from the
// word only the bits the visited record's gate names, so a write made while
// nothing is waiting persists and satisfies the next record that arms the gate
// on that record's first visit [04 R-COB-06].
const PendingScriptTouched uint32 = 0x4

// raiseScriptTouched is the unit-side hop the VM calls for the marker: the VM
// holds no unit pointer, so the raise reaches the order-event word through a
// closure over this unit [04 R-COB-06].
func (u *Unit) raiseScriptTouched() {
	if u == nil {
		return
	}
	u.Pending |= PendingScriptTouched
}

// unitPortBindings is the production port surface. It makes the two COB
// dispatches explicit: reads always receive four cells and cannot invoke a
// state write [04 R-COB-03 §1][04 R-COB-03 §4].
func unitPortBindings(vm *cob.VM, u *Unit) map[cob.Port]cob.PortBinding {
	if u == nil {
		return nil
	}
	ports := make(map[cob.Port]cob.PortBinding, 10)
	// Read the current two-bit stance fields, including changes made after
	// binding. These ports have no write arm [04 R-COB-03 §3].
	ports[cob.Port(2)] = cob.PortBinding{Read: func([4]int32) int32 {
		return int32((u.Flags >> StandingMoveShift) & StandingFieldMask)
	}}
	ports[cob.Port(3)] = cob.PortBinding{Read: func([4]int32) int32 {
		return int32((u.Flags >> StandingFireShift) & StandingFieldMask)
	}}
	bindFlag := func(port cob.Port, get func() bool, set func(bool)) {
		ports[port] = cob.PortBinding{
			Read: func([4]int32) int32 {
				if get() {
					return 1
				}
				return 0
			},
			Write: func(value int32) { set(value&1 != 0) },
		}
	}
	bindFlag(cob.Port(5), func() bool { return u.InBuildStance }, func(v bool) { u.InBuildStance = v })
	bindFlag(cob.Port(6), func() bool { return u.Busy }, func(v bool) { u.Busy = v })
	ports[cob.Port(18)] = cob.PortBinding{
		Read: func([4]int32) int32 {
			if u.YardOpen {
				return 1
			}
			return 0
		},
		Write: func(value int32) {
			requested := value&1 != 0
			if u.yardTransaction != nil {
				u.yardTransaction(requested)
			} else {
				u.YardOpen = requested
			}
		},
	}
	ports[cob.Port(4)] = cob.PortBinding{Read: func([4]int32) int32 {
		return cob.HealthPercent(u.Health, u.MaxHealth)
	}}
	ports[cob.Port(17)] = cob.PortBinding{Read: func([4]int32) int32 {
		return cob.BuildPercentLeft(u.Remaining)
	}}
	bindFlag(cob.Port(19), func() bool { return u.BuggerOff }, func(v bool) { u.BuggerOff = v })
	bindFlag(cob.Port(20), func() bool { return u.Armored }, u.SetArmored)
	ports[cob.Port(1)] = cob.PortBinding{
		Read: func([4]int32) int32 {
			if u.Activated {
				return 1
			}
			return 0
		},
		Write: func(value int32) { u.setActivationEdge(value&1 != 0, vm) },
	}
	return ports
}

func bindUnitPortHandlers(vm *cob.VM, u *Unit) {
	if vm == nil {
		return
	}
	for port, binding := range unitPortBindings(vm, u) {
		vm.BindPortBinding(port, binding)
	}
	// The marker is not a port handler and must not be attached to one: retail
	// raises it from every arm of the engine-write dispatch, including the
	// fall-through no port handler can observe [04 R-COB-06]. It gets its own
	// binding on the VM for exactly that reason.
	if u != nil {
		vm.BindScriptTouched(u.raiseScriptTouched)
	}
}

// bindCOBWithPortsAndVisibility is the unit-aware strict binding path. The
// instance port handlers are installed before Create runs, matching retail's
// D+wake initialization order. The exported helper above remains a generic
// asset-binding seam for callers without an owning Unit instance.
func bindCOBWithPortsAndVisibility(fs vfs.FSOps, def *content.UnitDef, mdl *model.Model, sim *rng.Simulation, sink cob.PresentationSink, visible func(piece int, sfxType int32) bool, u *Unit, preCreate func(*cob.Binding) error) (*cob.Binding, error) {
	if def == nil {
		return nil, fmt.Errorf("nanolathe: COB binding: nil unit definition")
	}
	if mdl == nil {
		return nil, &cob.BindingError{Diagnostics: []cob.BindingDiagnostic{{
			Code: cob.BindingMissingModel, Expected: "loaded 3DO model", Detail: fmt.Sprintf("unit %q has no model", def.UnitName),
		}}}
	}
	// The catalog retains missing scripts with warnings; preflight and this
	// strict bind refuse their use. Retail faults during creation
	// when unconditional queries reach a null VM, so configured production
	// allocation must never proceed without a loadable COB [R-COB-04 §8].
	modelPieces := make([]string, len(mdl.Pieces))
	for i := range mdl.Pieces {
		modelPieces[i] = mdl.Pieces[i].Name
	}
	req := cob.BindingRequest{
		UnitName:          def.UnitName,
		ScriptPath:        "scripts/" + def.UnitName + ".cob",
		Program:           def.Script,
		ProgramProvenance: def.ScriptProvenance,
		Model:             mdl,
		ModelPieces:       modelPieces,
		RequiredScripts:   RequiredCOBEntryPoints(def),
		SimulationRNG:     sim,
		SFXVisible:        visible,
		PresentationSink:  sink,
	}
	if u != nil {
		req.PreCreate = func(binding *cob.Binding) error {
			if binding == nil || binding.VM == nil || binding.Callbacks == nil {
				return fmt.Errorf("unit creation received incomplete COB binding")
			}
			// Install the model-owned render state and all six write arms with the
			// real VM before Create can issue a piece flag or port operation.
			modelFlags := BuildRenderPieceFlags(mdl)
			u.RenderPieceFlags = modelFlags
			bindRenderFlags(binding, modelFlags)
			for port, portBinding := range unitPortBindings(binding.VM, u) {
				binding.VM.BindPortBinding(port, portBinding)
			}
			binding.VM.BindScriptTouched(u.raiseScriptTouched)
			if err := u.AttachCOBBindingPreCreate(binding); err != nil {
				return err
			}
			if preCreate != nil {
				return preCreate(binding)
			}
			return nil
		}
	}
	binding, err := cob.BindStrict(fs, req)
	if err != nil {
		return nil, err
	}
	// SetMaxReloadTime is issued after Create as a distinct deferred callback;
	// its argument is the maximum of all three linked weapon reload fields
	// [R-CB-01 §4].
	if binding.Callbacks != nil {
		initializeCreationCallbacks(u, binding.Callbacks, maxReloadTicks(def))
		// The two piece identities the slot's distance word is built from are
		// the ones just resolved, so the word is written here — the only site
		// in the build that answers both queries [06 R-WPN-05 §3].
		WriteSlotDistanceWords(u, binding)
	}
	return binding, nil
}

func bindRenderFlags(binding *cob.Binding, modelFlags []uint8) {
	if binding == nil || binding.Callbacks == nil {
		return
	}
	mf, pm := modelFlags, binding.PieceMap
	// The getter is called several times per unit per tick — every render-flag
	// read, every cache-validity test, every save or snapshot walk — and used
	// to allocate a fresh slice on each call: 3.0 million of them over a
	// 3000-tick benchmark window, the largest single object producer in the
	// simulation (docs/SIM_BENCHMARK.md). The projection is a pure function of
	// two slices that outlive the binding, so one buffer serves every call.
	// Every reader of the result treats it as read-only: the flag WRITER is
	// the setter below, which goes straight to the model record, and the one
	// caller that writes through a returned slice (internal/cob's retail
	// restore) takes that path only when no getter is bound at all.
	scratch := make([]uint8, len(pm))
	binding.Callbacks.BindRenderFlags(
		func() []uint8 {
			flags := scratch
			for i, modelPiece := range pm {
				if modelPiece >= 0 && modelPiece < len(mf) {
					flags[i] = mf[modelPiece]
				} else {
					flags[i] = 0x06
				}
			}
			return flags
		},
		func(piece int, mask uint8, set bool) bool {
			if piece < 0 || piece >= len(pm) || (mask != 0x01 && mask != 0x02 && mask != 0x04) {
				return false
			}
			modelPiece := pm[piece]
			if modelPiece < 0 {
				// A declared script piece beyond the model has no render
				// record. Retail's flag adapter writes past the table with
				// no check and the thread carries on; Nanolathe keeps the
				// thread and drops the write [04 R-COB-01 §4].
				return true
			}
			if modelPiece >= len(mf) {
				return false
			}
			if set {
				mf[modelPiece] |= mask
			} else {
				mf[modelPiece] &^= mask
			}
			return true
		},
	)
}

type creationCallbacks interface {
	QueryWeapon(cob.WeaponSlot) cob.CallbackResult
	AimFromWeapon(cob.WeaponSlot) cob.CallbackResult
	SetMaxReloadTime(int32) cob.CallbackResult
}

// initializeCreationCallbacks performs the creation-time weapon-slot
// callbacks in their established order: Query* first, then AimFrom* with a
// second Query* when AimFrom returns -1, then SetMaxReloadTime
// [R-CB-01 §4]. Query* supplies the muzzle identity; the separate AimFrom
// result is retained until the aim-origin consumer wires it. The extractor
// SetSpeed follows this sequence at a terrain-aware producer outside units.
//
// I11 divergence, deliberate. Retail runs this initializer unconditionally
// after the model bind on all three creation paths, and its query adapter
// branches on the piece argument alone: with the "ask the script" argument the
// adapter loads the VM reference off the unit record and hands it straight to
// the callback starter with no null test, so on a scriptless unit — whose
// initializer stored a null VM — the starter's read of the program pointer is
// a null dereference and retail faults while creating the unit
// [04 R-COB-04 §8][04 R-COB-01 §3]. Exactly three producers in the whole image
// test the VM for null and none of them is on this path. Nanolathe takes the
// diagnostic refusal documented in DESIGN_UNITS_ORDERS_COB §2.1: creation refuses a
// definition with no loadable program and the skirmish preflight reports a
// missing COB as fatal, so a scriptless unit is never created and this nil
// guard is unreachable in a real battle. It stays because a crash is not a
// behavior worth cloning once the state that reaches it cannot arise.
func initializeCreationCallbacks(u *Unit, bridge creationCallbacks, maxReload int32) {
	if bridge == nil {
		return
	}
	for i := 0; i < NumSlots; i++ {
		slot := cob.WeaponSlot(i)
		query := bridge.QueryWeapon(slot)
		aim := bridge.AimFromWeapon(slot)
		if aim.QueryValue() == -1 {
			aim = bridge.QueryWeapon(slot)
		}
		if u != nil {
			u.Slots[i].MuzzlePiece = query.QueryValue()
			u.Slots[i].AimOriginPiece = aim.QueryValue()
		}
	}
	bridge.SetMaxReloadTime(maxReload)
}

// AttachCOBBindingPreCreate publishes the VM to its unit before Create runs,
// so a synchronous engine port edge can start its callback through the same
// bridge. The caller must finish the one Create attempt before publishing the
// unit to other systems [04 R-CB-01 §4].
func (u *Unit) AttachCOBBindingPreCreate(binding *cob.Binding) error {
	if u == nil {
		return fmt.Errorf("nanolathe: COB attachment: nil unit")
	}
	if binding == nil {
		return fmt.Errorf("nanolathe: COB attachment: nil binding")
	}
	if binding.Program == nil || len(binding.Program.Code) == 0 {
		return fmt.Errorf("nanolathe: COB attachment: binding has no COB program")
	}
	if binding.VM == nil {
		return fmt.Errorf("nanolathe: COB attachment: binding has no VM")
	}
	if binding.Callbacks == nil {
		return fmt.Errorf("nanolathe: COB attachment: binding has no callback bridge")
	}
	if u.GetScript() != nil {
		return fmt.Errorf("nanolathe: COB attachment: unit already has a script")
	}
	// The binding already carries the bridge Create ran on, with every sink the
	// composition installed. Reuse it rather than building a second one, so a
	// synchronous script query made later through ScriptBridge sees the same
	// sinks [04 §4.1][R-COB-01 §1].
	u.ScriptState = &ScriptState{VM: binding.VM, Binding: binding, Bridge: binding.Callbacks}
	u.Script = binding.VM
	return nil
}

// AttachCOBBinding retains the fully initialized attachment seam for callers
// outside the creation barrier.
func (u *Unit) AttachCOBBinding(binding *cob.Binding) error {
	if binding == nil || binding.Program == nil || binding.Callbacks == nil {
		return fmt.Errorf("nanolathe: COB attachment: missing binding program or callback bridge")
	}
	if _, authored := binding.Program.Scripts["Create"]; authored && (!binding.CreateInvoked || !binding.Callbacks.CreateInvoked()) {
		return fmt.Errorf("nanolathe: COB attachment: Create was not invoked exactly once")
	}
	return u.AttachCOBBindingPreCreate(binding)
}

// NotifyExtractorFootprint starts the creation-time extractor `SetSpeed`
// callback [05 R-PROD-01 §6][04 R-COB-04 §9].
//
// Immediately after a creation path stores the sampled extraction rate — and
// only when the unit has a script VM — the creator starts a deferred
// `SetSpeed` carrying the footprint metal accumulator sign-extended from
// sixteen bits: the summed metal-map value under the stamped footprint, one
// metal byte plus one per covered cell. That is what stock extractor scripts
// use to size their animation rate; `armmex` stores the argument scaled and
// spins its top piece at that speed, so a unit that never receives the
// callback keeps the zero its `Create` wrote and its spin issues a per-tick
// step of zero [04 §4.6 "zero-speed and zero-decel"].
//
// Every creation path that samples the footprint must call this, and must call
// it after `Create` has run: `Create` is what zeroes the script's speed
// variable in the stock scripts, so an earlier notification would be
// overwritten.
func (u *Unit) NotifyExtractorFootprint(footprintSum uint16) {
	if u == nil {
		return
	}
	// Sign extension from sixteen bits is the callback's own contract, applied
	// by cob.SetSpeedFootprint; a sum of 0x8000 or more arrives negative
	// [04 R-COB-04 §9].
	if binding := u.COBBinding(); binding != nil && binding.Callbacks != nil {
		binding.Callbacks.SetSpeedFootprint(int32(footprintSum))
		return
	}
	if vm := u.GetScript(); vm != nil {
		_ = vm.StartByName("SetSpeed", []int32{cob.SetSpeedFootprint(int32(footprintSum))})
	}
}

// COBBinding returns the strict production binding attached to the unit, or
// nil when no strict binding is attached.
func (u *Unit) COBBinding() *cob.Binding {
	if u == nil || u.ScriptState == nil {
		return nil
	}
	return u.ScriptState.Binding
}
