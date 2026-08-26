package units

import (
	"fmt"

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
// exact names or alternative groups through BindCOBWithRequirements.
func RequiredCOBEntryPoints(_ *content.UnitDef) []string { return []string{"Create"} }

func appendUniqueEntry(entries []string, names ...string) []string {
	for _, name := range names {
		found := false
		for _, prior := range entries {
			if prior == name {
				found = true
				break
			}
		}
		if !found {
			entries = append(entries, name)
		}
	}
	return entries
}

// BindCOB strictly binds a compiled unit script to its loaded model and
// initializes its VM. The returned VM has completed the one mode-I Create
// start before this function returns [04 §4.1][04 §5.1]. A nil model or any
// missing/malformed asset is an error; use SyntheticCOBForTests for fixtures.
func BindCOB(fs vfs.FSOps, def *content.UnitDef, mdl *model.Model) (*cob.Binding, error) {
	return BindCOBWithPorts(fs, def, mdl, nil, nil)
}

// BindCOBWithPorts is the composition seam for a production VM that must see
// the session-owned simulation stream and presentation sink during its one
// mode-I Create callback. It retains the same strict model/piece checks as
// BindCOB; the extra ports are supplied before Create starts.
func BindCOBWithPorts(fs vfs.FSOps, def *content.UnitDef, mdl *model.Model, sim *rng.Simulation, sink cob.PresentationSink) (*cob.Binding, error) {
	return BindCOBWithPortsAndVisibility(fs, def, mdl, sim, sink, nil)
}

// BindCOBWithPortsAndVisibility is the full production seam. The visibility
// predicate is installed before Create so emit-sfx cannot bypass gameplay LOS.
func BindCOBWithPortsAndVisibility(fs vfs.FSOps, def *content.UnitDef, mdl *model.Model, sim *rng.Simulation, sink cob.PresentationSink, visible func(piece int, sfxType int32) bool) (*cob.Binding, error) {
	return bindCOBWithPortsAndVisibility(fs, def, mdl, sim, sink, visible, nil)
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
	modelPieces := make([]string, len(mdl.Pieces))
	for i := range mdl.Pieces {
		modelPieces[i] = mdl.Pieces[i].Name
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
		// Keep the production and fixture binding paths identical. The helper
		// installs all six researched engine-write arms before Create.
		req.PortFuncs = unitPortHandlers(nil, u)
	}
	return cob.BindStrict(fs, req)
}

// BindCOBWithEntries is the strict binding seam for callers that know a
// narrower callback set (for example a focused construction or model test).
// Create remains mandatory even when entries omits it, because production
// initialization must execute Create exactly once in mode I [04 §5.1].
func BindCOBWithEntries(fs vfs.FSOps, unitName string, mdl *model.Model, entries []string) (*cob.Binding, error) {
	return BindCOBWithRequirements(fs, unitName, mdl, entries, nil)
}

// BindCOBWithRequirements is the capability-specific strict binding seam.
// Every exact name is mandatory. Each alternative group is satisfied when at
// least one entry exists; for example, []string{"AimFromPrimary",
// "QueryPrimary"} represents the established AimFrom→Query fallback [04
// §5.3]. No capability is guessed from a unit's BMCode, CanMove, or name.
func BindCOBWithRequirements(fs vfs.FSOps, unitName string, mdl *model.Model, entries []string, alternatives [][]string) (*cob.Binding, error) {
	if mdl == nil {
		return nil, &cob.BindingError{Diagnostics: []cob.BindingDiagnostic{{Code: cob.BindingMissingModel, Expected: "loaded 3DO model", Detail: "nil model"}}}
	}
	modelPieces := make([]string, len(mdl.Pieces))
	for i := range mdl.Pieces {
		modelPieces[i] = mdl.Pieces[i].Name
	}
	entries = appendUniqueEntry(append([]string(nil), entries...), "Create")
	return cob.BindStrict(fs, cob.BindingRequest{UnitName: unitName, Model: mdl, ModelPieces: modelPieces, RequiredScripts: entries, RequiredScriptGroups: alternatives})
}

// AttachCOBBinding attaches only a fully initialized strict production
// binding. Create must already have run exactly once in mode I before the unit
// receives a playable script [04 §4.1][04 §5.1]. Synthetic fixtures should use
// SetScript or SyntheticCOBForTests explicitly.
func (u *Unit) AttachCOBBinding(binding *cob.Binding) error {
	if u == nil {
		return fmt.Errorf("nanolathe: COB attachment: nil unit")
	}
	if binding == nil || binding.VM == nil {
		return fmt.Errorf("nanolathe: COB attachment: missing strict binding")
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
// nil for synthetic/legacy script state.
func (u *Unit) COBBinding() *cob.Binding {
	if u == nil || u.ScriptState == nil {
		return nil
	}
	return u.ScriptState.Binding
}

// SyntheticCOBForTests is an explicit empty-script constructor for fixtures
// and tools. It must not be used by production composition.
func SyntheticCOBForTests() *cob.VM { return cob.NewSyntheticEmptyVM() }
