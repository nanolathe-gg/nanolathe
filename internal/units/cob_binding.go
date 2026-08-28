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
// initialization invokes it in mode I [04 §5.1]. Weapon and builder
// capabilities are deliberately not inferred from UnitDef fields: SC21 leaves
// those producer/capability gates unresolved, and retail-valid scripts may
// omit optional callbacks. Callers with an observed consumer should supply
// exact names or alternative groups through cob.BindStrict.
func RequiredCOBEntryPoints(_ *content.UnitDef) []string { return []string{"Create"} }

// BindCOBWithPorts is retained for the session composition test seam, whose
// caller has no owning Unit instance. New production bindings use
// BindCOBWithPortsAndVisibilityForUnit so instance-owned ports are installed
// before the mode-I Create callback. This seam retains the same strict
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
	ports := make(map[cob.Port]func([]int32) int32, 6)
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
	bindUnitPort(cob.Port(18), func() bool { return u.YardOpen }, func(v bool) { u.YardOpen = v })
	bindUnitPort(cob.Port(19), func() bool { return u.BuggerOff }, func(v bool) { u.BuggerOff = v })
	bindUnitPort(cob.Port(20), func() bool { return u.Armored }, func(v bool) { u.Armored = v })
	// Port 1 is the activation edge input. The callback starts the authored
	// lifecycle script only on an actual edge; engine-side activation raises
	// use the same named callbacks elsewhere in construction/economy [R-P0-10].
	ports[cob.Port(1)] = func(args []int32) int32 {
		if len(args) >= 2 {
			on := args[1]&1 != 0
			if on != u.Activated {
				u.Activated = on
				callbackVM := vm
				if callbackVM == nil {
					callbackVM = u.Script
				}
				if callbackVM != nil && on {
					_ = callbackVM.StartByName("Activate", nil)
				} else if callbackVM != nil {
					_ = callbackVM.StartByName("Deactivate", nil)
				}
			}
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
// mode-I initialization order. The exported helper above remains a generic
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
	// Scriptless check [R-COB-01 §1] UNIT-04: a missing or empty COB stores the null program.
	// The render-piece table is still built from the model, but no VM is allocated.
	// This path is exercised by fixture definitions that have no script file and by synthetic
	// missing-script cases. Production shipped content never hits it.
	if def.Script == nil {
		logical := "scripts/" + def.UnitName + ".cob"
		// Normalize to lower as VFS is case-insensitive; check both UnitName and CanonicalKey.
		found := false
		if fs != nil {
			if info, err := fs.Stat(logical); err == nil && !info.IsDir {
				found = true
			} else if info2, err2 := fs.Stat("scripts/" + def.CanonicalKey + ".cob"); err2 == nil && !info2.IsDir {
				found = true
			}
		}
		if !found {
			if u != nil {
				// Zero-filled allocation then fill walk [04 §"Piece flag polarity"].
				u.InitRenderPieceFlags(mdl)
			}
			// No VM, no Create, no diagnostic [R-COB-01 §1].
			return nil, nil
		}
		// File exists but def.Script nil (compile didn't store it) — fall through to strict bind which will load it.
	}
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

// AttachCOBBinding attaches only a fully initialized strict production
// binding. Create must already have run exactly once in mode I before the unit
// receives a playable script [04 §4.1][04 §5.1].
// A nil binding is the scriptless case [R-COB-01 §1] UNIT-04: no VM is attached, but the
// render-piece table has already been built on the unit by the binder [04 §"Piece flag polarity"].
func (u *Unit) AttachCOBBinding(binding *cob.Binding) error {
	if u == nil {
		return fmt.Errorf("nanolathe: COB attachment: nil unit")
	}
	if binding == nil {
		// Scriptless unit — no VM, flags already on unit [R-COB-01 §1] [04 §"Piece flag polarity"].
		return nil
	}
	if binding.VM == nil {
		// Scriptless via binding with nil VM — also accepted.
		return nil
	}
	if !binding.CreateInvoked || binding.Callbacks == nil || !binding.Callbacks.CreateInvoked() {
		return fmt.Errorf("nanolathe: COB attachment: Create was not invoked exactly once")
	}
	if u.GetScript() != nil {
		return fmt.Errorf("nanolathe: COB attachment: unit already has a script")
	}
	u.ScriptState = &ScriptState{VM: binding.VM, Binding: binding}
	u.Script = binding.VM
	return nil
}

// COBBinding returns the strict production binding attached to the unit, or
// nil when no strict binding is attached.
func (u *Unit) COBBinding() *cob.Binding {
	if u == nil || u.ScriptState == nil {
		return nil
	}
	return u.ScriptState.Binding
}
