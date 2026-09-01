package units

import (
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/vfs"
)

// RequiredCOBEntryPoints returns only callback roots that are mandatory for
// every strict production unit. Create is always required because unit
// initialization invokes it as a deferred start with wake=1 [R-CB-01 §2]. Weapon and builder
// capabilities are deliberately not inferred from UnitDef fields: SC21 leaves
// those producer/capability gates unresolved, and retail-valid scripts may
// omit optional callbacks. Callers with an observed consumer should supply
// exact names or alternative groups through cob.BindStrict.
func RequiredCOBEntryPoints(_ *content.UnitDef) []string { return []string{"Create"} }

// BindCOBWithPorts is retained for the session composition test seam, whose
// caller has no owning Unit instance. New production bindings use
// BindCOBWithPortsAndVisibilityForUnit so instance-owned ports are installed
// before the D+wake Create callback. This seam retains the same strict
// model/piece checks; the extra ports are supplied before Create starts.
func BindCOBWithPorts(fs vfs.FSOps, def *content.UnitDef, mdl *model.Model, sim *rng.Simulation, sink cob.PresentationSink) (*cob.Binding, error) {
	return bindCOBWithPortsAndVisibility(fs, def, mdl, sim, sink, nil, nil)
}

// BindCOBWithPortsAndVisibilityForUnit is the production binding seam for a
// live unit. Instance-owned engine ports are installed before Create runs.
func BindCOBWithPortsAndVisibilityForUnit(fs vfs.FSOps, u *Unit, mdl *model.Model, sim *rng.Simulation, sink cob.PresentationSink, visible func(piece int, sfxType int32) bool) (*cob.Binding, error) {
	if u == nil {
		return nil, fmt.Errorf("nanolathe: COB binding: nil unit")
	}
	return bindCOBWithPortsAndVisibility(fs, u.Def, mdl, sim, sink, visible, u)
}

func unitPortHandlers(vm *cob.VM, u *Unit) map[cob.Port]func([]int32) int32 {
	if u == nil {
		return nil
	}
	ports := make(map[cob.Port]func([]int32) int32, 8)
	bindUnitPort := func(port cob.Port, get func() bool, set func(bool)) {
		ports[port] = func(args []int32) int32 {
			if len(args) >= 2 {
				set(args[1]&1 != 0)
				return 0
			}
			if get() {
				return 1
			}
			return 0
		}
	}
	bindUnitPort(cob.Port(5), func() bool { return u.InBuildStance }, func(v bool) { u.InBuildStance = v })
	bindUnitPort(cob.Port(6), func() bool { return u.Busy }, func(v bool) { u.Busy = v })
	ports[cob.Port(18)] = func(args []int32) int32 {
		if len(args) >= 2 {
			requested := args[1]&1 != 0
			// The installed callback owns admission, the authoritative bit commit,
			// and the later restamp as one ordered transaction. Fixtures without a
			// callback retain the direct-write path [04 §4.7 port 18][04 R-COLL-01 §4].
			if u.yardTransaction != nil {
				u.yardTransaction(requested)
			} else {
				u.YardOpen = requested
			}
			return 0
		}
		if u.YardOpen {
			return 1
		}
		return 0
	}
	// Port 4 (health) and port 17 (build percent left) are read-only engine
	// ports [04 §4.4]. They must be bound on the instance: an unbound port
	// reads zero, and zero is a meaningful — and wrong — answer to both. The
	// stock damage-smoke helper shipped in the retail archive
	// (`scripts/SMOKEUNIT.H`, included by most unit scripts) waits on
	// `while (get BUILD_PERCENT_LEFT) sleep 400;` and then puffs smoke
	// whenever `get HEALTH` is below 66, so a pair of zero reads walks every
	// nanoframe straight past the "wait until the unit is actually built"
	// loop and into a permanent smoke plume at full health.
	ports[cob.Port(4)] = func([]int32) int32 {
		return cob.HealthPercent(u.Health, u.MaxHealth) // [04 §4.4] port 4
	}
	ports[cob.Port(17)] = func([]int32) int32 {
		return cob.BuildPercentLeft(u.Remaining) // [04 §4.4] port 17
	}
	bindUnitPort(cob.Port(19), func() bool { return u.BuggerOff }, func(v bool) { u.BuggerOff = v })
	bindUnitPort(cob.Port(20), func() bool { return u.Armored }, func(v bool) { u.Armored = v })
	// Port 1 is the activation edge input. The write arm shares one edge
	// semantics with every engine-side producer, so it defers to the single
	// edge setter instead of writing the bit itself [04 R-UNIT-06 §2].
	ports[cob.Port(1)] = func(args []int32) int32 {
		if len(args) >= 2 {
			u.setActivationEdge(args[1]&1 != 0, vm)
			return 0
		}
		if u.Activated {
			return 1
		}
		return 0
	}
	return ports
}

func bindUnitPortHandlers(vm *cob.VM, u *Unit) {
	if vm == nil {
		return
	}
	for port, fn := range unitPortHandlers(vm, u) {
		vm.BindPort(port, fn)
	}
}

// bindCOBWithPortsAndVisibility is the unit-aware strict binding path. The
// instance port handlers are installed before Create runs, matching retail's
// D+wake initialization order. The exported helper above remains a generic
// asset-binding seam for callers without an owning Unit instance.
func bindCOBWithPortsAndVisibility(fs vfs.FSOps, def *content.UnitDef, mdl *model.Model, sim *rng.Simulation, sink cob.PresentationSink, visible func(piece int, sfxType int32) bool, u *Unit) (*cob.Binding, error) {
	if def == nil {
		return nil, fmt.Errorf("nanolathe: COB binding: nil unit definition")
	}
	if mdl == nil {
		return nil, &cob.BindingError{Diagnostics: []cob.BindingDiagnostic{{
			Code: cob.BindingMissingModel, Expected: "loaded 3DO model", Detail: fmt.Sprintf("unit %q has no model", def.UnitName),
		}}}
	}
	// The catalog compiler is the primary missing-script rejection boundary.
	// BindStrict remains the asset-level defense: retail faults during creation
	// when unconditional queries reach a null VM, so configured production
	// allocation must never proceed without a loadable COB [R-COB-04 §8].
	modelPieces := make([]string, len(mdl.Pieces))
	for i := range mdl.Pieces {
		modelPieces[i] = mdl.Pieces[i].Name
	}
	// Prepare pending render-piece handler so VM's Create targets the unit record
	// from the start [04 §"Piece flag polarity"] [R-COB-01 §1]. The table is
	// geometry-derived (model walk) and Create's flag ops must operate on it.
	var pendingModelFlags []uint8
	var pendingPieceMap []int
	var pendingProg *cob.Program
	if def.Script != nil {
		pendingProg = def.Script
	} else if fs != nil {
		for _, cand := range []string{"scripts/" + strings.ToLower(def.UnitName) + ".cob", "scripts/" + strings.ToLower(def.CanonicalKey) + ".cob"} {
			if info, err := fs.Stat(cand); err == nil && !info.IsDir {
				if data, err := fs.ReadFileLimit(cand, 4<<20); err == nil {
					if p, err := cob.Load(data); err == nil {
						pendingProg = p
						break
					}
				}
			}
		}
	}
	if pendingProg != nil && len(pendingProg.Pieces) > 0 {
		pendingModelFlags = BuildRenderPieceFlags(mdl) // model-ordered [04 §"Piece flag polarity"]
		pendingPieceMap = make([]int, len(pendingProg.Pieces))
		for i, name := range pendingProg.Pieces {
			idx := -1
			for mi, mp := range modelPieces {
				if strings.EqualFold(mp, name) {
					idx = mi
					break
				}
			}
			pendingPieceMap[i] = idx
		}
		mf := pendingModelFlags
		pm := pendingPieceMap
		pp := pendingProg
		cob.SetPendingRenderHandlers(
			func() []uint8 {
				pf := make([]uint8, len(pm))
				for i, mi := range pm {
					if mi >= 0 && mi < len(mf) {
						pf[i] = mf[mi]
					} else {
						pf[i] = 0x06
					}
				}
				_ = pp
				return pf
			},
			func(piece int, mask uint8, set bool) bool {
				if piece < 0 || piece >= len(pm) {
					return false
				}
				mi := pm[piece]
				if mi < 0 || mi >= len(mf) {
					return false
				}
				if mask != 0x01 && mask != 0x02 && mask != 0x04 {
					return false
				}
				if set {
					mf[mi] |= mask
				} else {
					mf[mi] &^= mask
				}
				return true
			},
		)
	}
	req := cob.BindingRequest{
		UnitName:         def.UnitName,
		ScriptPath:       "scripts/" + def.UnitName + ".cob",
		Model:            mdl,
		ModelPieces:      modelPieces,
		RequiredScripts:  RequiredCOBEntryPoints(def),
		SimulationRNG:    sim,
		SFXVisible:       visible,
		PresentationSink: sink,
	}
	if u != nil {
		// Keep the generic and instance-aware binding paths identical. The helper
		// installs all six researched engine-write arms before Create.
		req.PortFuncs = unitPortHandlers(nil, u)
	}
	binding, err := cob.BindStrict(fs, req)
	if err != nil {
		// Clear pending on failure.
		cob.SetPendingRenderHandlers(nil, nil)
		return nil, err
	}
	// SetMaxReloadTime is issued after Create as a distinct deferred callback;
	// its argument is the maximum of all three linked weapon reload fields
	// [R-CB-01 §4].
	if binding.Callbacks != nil {
		initializeCreationCallbacks(u, binding.Callbacks, maxReloadTicks(def))
	}
	// On success, the pending handler has been consumed by the VM's SetProgram
	// inside BindStrict, and Create has already run against the unit's model-ordered
	// table via that handler. Capture it for the unit.
	if pendingModelFlags != nil && u != nil {
		u.RenderPieceFlags = pendingModelFlags
	} else if u != nil && binding != nil && binding.VM != nil {
		// Fallback when pending was not set (def.Script nil case where prog was loaded inside BindStrict).
		// Build the table now and bind via handler mapping.
		modelFlags := BuildRenderPieceFlags(mdl)
		u.RenderPieceFlags = modelFlags
		pieceMap := binding.PieceMap
		prog := binding.Program
		if binding.Callbacks != nil {
			mf := modelFlags
			pm := pieceMap
			pp := prog
			binding.Callbacks.BindRenderFlags(
				func() []uint8 {
					pf := make([]uint8, len(pm))
					for i, mi := range pm {
						if mi >= 0 && mi < len(mf) {
							pf[i] = mf[mi]
						} else {
							pf[i] = 0x06
						}
					}
					_ = pp
					return pf
				},
				func(piece int, mask uint8, set bool) bool {
					if piece < 0 || piece >= len(pm) {
						return false
					}
					mi := pm[piece]
					if mi < 0 || mi >= len(mf) {
						return false
					}
					if set {
						mf[mi] |= mask
					} else {
						mf[mi] &^= mask
					}
					return true
				},
			)
		}
	} else if u != nil && binding != nil && binding.VM == nil {
		u.InitRenderPieceFlags(mdl)
	}
	return binding, nil
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

// AttachCOBBinding attaches only a fully initialized strict production
// binding. Create must already have run exactly once as a D+wake callback
// before the unit receives a playable script [04 §4.1][R-CB-01 §2].
func (u *Unit) AttachCOBBinding(binding *cob.Binding) error {
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
	if !binding.CreateInvoked || binding.Callbacks == nil || !binding.Callbacks.CreateInvoked() {
		return fmt.Errorf("nanolathe: COB attachment: Create was not invoked exactly once")
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
