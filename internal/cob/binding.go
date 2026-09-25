package cob

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// BindingRequest describes the immutable assets needed to bind one production
// unit. ModelPieces is the depth-first 3DO piece-name list. The COB piece table
// is linked against that list before a VM is made playable [02 "Model archive
// (3DO)"][02 "Compiled script archive (COB)"][04 §4.1].
type BindingRequest struct {
	UnitName   string
	ScriptPath string
	// Program and ProgramProvenance are the catalog-linked immutable program
	// asset. When supplied, strict binding links it directly and creates only
	// mutable VM state for this unit [04 §4.1].
	Program              *Program
	ProgramProvenance    vfs.Provenance
	Model                *model.Model
	ModelPieces          []string
	RequiredScripts      []string
	RequiredScriptGroups [][]string
	SimulationRNG        *rng.Simulation
	SFXSink              SFXSink
	SFXVisible           func(piece int, sfxType int32) bool
	PresentationSink     PresentationSink
	// PortFuncs are installed before the D+wake Create callback. Production
	// unit bindings use this for instance-owned engine-port state such as
	// INBUILDSTANCE; leaving it nil preserves the existing default no-op ports.
	PortFuncs map[Port]func(args []int32) int32
	// PortBindings is the explicit production surface. It separates reads from
	// writes so a read's four argument cells cannot be interpreted as a write
	// value [04 R-COB-03 §1]. It takes precedence over PortFuncs per port.
	PortBindings map[Port]PortBinding
	// PreCreate receives the fully linked, mutable instance before an authored
	// Create callback starts. It installs all unit/session context that Create
	// can query or mutate [04 R-CB-01 §4].
	PreCreate func(*Binding) error
}

// BindingDiagnosticCode identifies one strict binding failure. Codes are
// stable so composition and unit-creation callers can classify diagnostics
// without parsing text.
type BindingDiagnosticCode string

const (
	BindingMissingCOB      BindingDiagnosticCode = "missing-cob"
	BindingMalformedCOB    BindingDiagnosticCode = "malformed-cob"
	BindingMissingModel    BindingDiagnosticCode = "missing-model"
	BindingPieceCount      BindingDiagnosticCode = "piece-count-mismatch"
	BindingUnresolvedPiece BindingDiagnosticCode = "unresolved-piece"
	BindingMissingEntry    BindingDiagnosticCode = "missing-entry-point"
	BindingCreateStart     BindingDiagnosticCode = "create-start-failed"
	BindingInvalidRequest  BindingDiagnosticCode = "invalid-request"
)

// BindingDiagnostic is one actionable production binding diagnostic. Provider
// is the portable VFS provider identity, never an absolute host path.
type BindingDiagnostic struct {
	Code     BindingDiagnosticCode
	Logical  string
	Provider string
	Expected string
	Detail   string
}

func (d BindingDiagnostic) Error() string {
	providers := d.Provider
	if providers == "" {
		providers = "<none>"
	}
	expected := d.Expected
	if expected == "" {
		expected = "valid production COB binding"
	}
	return fmt.Sprintf("nanolathe: COB binding %s: logical path %s, providers searched [%s], expected %s: %s", d.Code, d.Logical, providers, expected, d.Detail)
}

// BindingError retains all failures found by one strict binding attempt.
type BindingError struct {
	Diagnostics []BindingDiagnostic
}

func (e *BindingError) Error() string {
	if e == nil || len(e.Diagnostics) == 0 {
		return "nanolathe: COB binding failed"
	}
	parts := make([]string, len(e.Diagnostics))
	for i, d := range e.Diagnostics {
		parts[i] = d.Error()
	}
	return strings.Join(parts, "; ")
}

// Has reports whether any diagnostic has code.
func (e *BindingError) Has(code BindingDiagnosticCode) bool {
	if e == nil {
		return false
	}
	for _, d := range e.Diagnostics {
		if d.Code == code {
			return true
		}
	}
	return false
}

// Binding is a linked COB program and its per-unit VM. PieceMap maps each COB
// piece index to its corresponding model piece index. CreateInvoked is true
// only after the one deferred Create start with wake=1 has completed its
// delta-zero VM-wide barrier [R-CB-01 §2].
type Binding struct {
	Program          *Program
	VM               *VM
	Model            *model.Model
	ScriptPath       string
	Provider         vfs.Provenance
	PieceMap         []int
	CreateInvoked    bool
	Callbacks        *CallbackBridge
	SimulationRNG    *rng.Simulation
	SFXSink          SFXSink
	SFXVisible       func(piece int, sfxType int32) bool
	PresentationSink PresentationSink

	// LinkNotes records the piece-table entries retail links without a name
	// match: a script piece whose name the model lacks, and a script piece
	// beyond the model's piece count. Neither refuses the bind
	// [04 R-COB-01 §4]; the notes exist for tools and diagnostics only and
	// never reach simulation state.
	LinkNotes []BindingDiagnostic

	// composeScratch is ComposePiece's per-call piece-state buffer. The
	// composition reads the VM's piece words and writes model order; nothing
	// outside the call sees the slice, and ComposePiece is not re-entrant on
	// one binding, so a single buffer per binding replaces a per-call
	// allocation in the movement and construction tick paths.
	composeScratch []model.PieceState
	// composeXform and composeChain are the composition's own reusable
	// storage. ComposePiece reads the world offset out of the transform and
	// keeps nothing else, so the node chain it composes was allocated fresh
	// and dropped on every call -- 14% of everything the simulation allocated.
	// ComposeInto writes into the retained transform's node storage instead,
	// which is what that entry point exists for; the arithmetic is the same
	// [03 §2.4] C21.
	composeXform model.Transform
	composeChain model.ComposeScratch
}

// BindStrict resolves, parses, links, and initializes one production COB
// binding. Missing files are fatal here. Create is started with wake=1 when it
// is authored; its absence is valid and leaves the new VM idle [R-CB-01 §2].
// Each RequiredScriptGroups group is an explicit any-of requirement: at least
// one named entry in each group must exist. This models established fallback
// paths such as AimFromPrimary → QueryPrimary without assuming that every
// callback is authored by every valid retail script [04 §5.3].
// The VM is returned only after all checks pass, so a failed unit never becomes
// playable with a partially linked or empty script.
func BindStrict(fs vfs.FSOps, req BindingRequest) (*Binding, error) {
	logical, unitName, err := bindingPath(req)
	if err != nil {
		return nil, &BindingError{Diagnostics: []BindingDiagnostic{{Code: BindingInvalidRequest, Logical: logical, Expected: "unit name and script path", Detail: err.Error()}}}
	}
	if fs == nil && req.Program == nil {
		return nil, &BindingError{Diagnostics: []BindingDiagnostic{{Code: BindingInvalidRequest, Logical: logical, Expected: "VFS", Detail: "nil VFS"}}}
	}

	info := vfs.EntryInfo{Path: logical, Name: logical, Source: req.ProgramProvenance}
	program := req.Program
	if program == nil {
		var statErr error
		info, statErr = fs.Stat(logical)
		if statErr != nil || info.IsDir {
			return nil, &BindingError{Diagnostics: []BindingDiagnostic{{
				Code: BindingMissingCOB, Logical: logical, Provider: providersFor(fs, logical, info), Expected: "compiled COB file", Detail: "required COB is unavailable",
			}}}
		}
		data, readErr := fs.ReadFileLimit(logical, 4<<20)
		if readErr != nil {
			return nil, &BindingError{Diagnostics: []BindingDiagnostic{{
				Code: BindingMalformedCOB, Logical: logical, Provider: providersFor(fs, logical, info), Expected: "readable compiled COB", Detail: readErr.Error(),
			}}}
		}
		var loadErr error
		program, loadErr = Load(data)
		if loadErr != nil {
			return nil, &BindingError{Diagnostics: []BindingDiagnostic{{
				Code: BindingMalformedCOB, Logical: logical, Provider: providersFor(fs, logical, info), Expected: "well-formed compiled COB", Detail: loadErr.Error(),
			}}}
		}
	} else if err := ValidateProgram(program); err != nil {
		return nil, &BindingError{Diagnostics: []BindingDiagnostic{{
			Code: BindingMalformedCOB, Logical: logical, Provider: providersFor(fs, logical, info), Expected: "validated compiled COB", Detail: err.Error(),
		}}}
	}

	diagnostics := linkDiagnostics(program, req.ModelPieces, req.RequiredScripts, req.RequiredScriptGroups, logical, providersFor(fs, logical, info))
	if len(diagnostics) != 0 {
		return nil, &BindingError{Diagnostics: diagnostics}
	}

	vm := NewVM(program)
	for port, fn := range req.PortFuncs {
		if fn != nil {
			vm.BindPort(port, fn)
		}
	}
	for port, binding := range req.PortBindings {
		vm.BindPortBinding(port, binding)
	}
	bridge := NewCallbackBridge(vm)
	bridge.SetSimulationRNG(req.SimulationRNG)
	bridge.SetSFXSink(req.SFXSink, req.SFXVisible)
	if req.PresentationSink != nil {
		bridge.SetPresentationSink(req.PresentationSink)
	}
	pieceMap := LinkPieces(program.Pieces, req.ModelPieces)
	// A presentation sink may need the strict COB→model identity before the
	// Create callback emits its first event. This optional adapter is
	// presentation-only and cannot affect binding or VM state.
	if sink, ok := req.PresentationSink.(interface{ SetCOBPieceMap([]int) }); ok {
		sink.SetCOBPieceMap(pieceMap)
	}
	binding := &Binding{Program: program, VM: vm, Model: req.Model, ScriptPath: logical, Provider: info.Source, PieceMap: pieceMap, LinkNotes: linkNotes(program.Pieces, req.ModelPieces, pieceMap, logical, func() string { return providersFor(fs, logical, info) }), Callbacks: bridge, SimulationRNG: req.SimulationRNG, SFXSink: req.SFXSink, SFXVisible: req.SFXVisible, PresentationSink: req.PresentationSink}
	if req.PreCreate != nil {
		if err := req.PreCreate(binding); err != nil {
			return nil, &BindingError{Diagnostics: []BindingDiagnostic{{
				Code: BindingInvalidRequest, Logical: logical, Provider: providersFor(fs, logical, info), Expected: "complete COB creation context", Detail: err.Error(),
			}}}
		}
	}

	createInvoked := false
	if _, authored := program.Scripts["Create"]; authored {
		// Create is a deferred callback with wake=1: start it once, then drain
		// all eight slots with delta 0 and one piece pass [R-CB-01 §2].
		create := bridge.Create()
		if !create.Started {
			return nil, &BindingError{Diagnostics: []BindingDiagnostic{{
				Code: BindingCreateStart, Logical: logical, Provider: providersFor(fs, logical, info), Expected: "Create entry point runnable with wake=1", Detail: fmt.Sprintf("unit %q could not allocate its Create thread", unitName),
			}}}
		}
		createInvoked = bridge.CreateInvoked()
	}

	binding.CreateInvoked = createInvoked
	return binding, nil
}

// ComposePiece is retail's piece locator [03 R-RAST-01 §8]: the one routine
// every simulation consumer of a piece position goes through. COB piece
// indices are not model indices: PieceMap is the link produced by the strict
// binder. The VM's current piece states are remapped into the immutable model
// before hierarchy composition [03 §2.4] C21 [04 §4.1].
//
// The returned triple is the WORLD offset, `(x, y, −z)` of the model-space
// composition. Model space is mirrored in Z against world space
// [03 R-RAST-01 §2], and the locator negates the composed Z once, on output,
// after the whole chain (the unit's own heading, pitch and bank included) has
// been composed. A caller forming a world point therefore adds the triple to
// the unit position with NO further sign change — the weapon muzzle for all
// three slots [06 §4.1], the nano spray source [03 §5.5], the factory build
// plate, the carried-cargo hang point and the piece-position COB ports
// [04 R-COB-03 §2] all do exactly that.
//
// The negation lives here and only here. Before WU-19-213 each consumer
// subtracted the composed Z for itself, which produced the same world points
// but left five copies of one rule and one caller (the transport hang offset,
// which reads the Y word alone) exempt for reasons that had to be re-derived
// at every site. [03 R-RAST-01 §8]'s implementation rule is explicit: compose,
// negate once, then add.
func (b *Binding) ComposePiece(cobPiece int, heading, pitch, bank uint16) ([3]numeric.Fixed, bool) {
	if b == nil || b.Model == nil || b.VM == nil || cobPiece < 0 || cobPiece >= len(b.PieceMap) {
		return [3]numeric.Fixed{}, false
	}
	modelPiece := b.PieceMap[cobPiece]
	if modelPiece < 0 || modelPiece >= len(b.Model.Pieces) {
		return [3]numeric.Fixed{}, false
	}
	states := b.composeScratch
	if cap(states) < len(b.Model.Pieces) {
		states = make([]model.PieceState, len(b.Model.Pieces))
		b.composeScratch = states
	} else {
		states = states[:len(b.Model.Pieces)]
		clear(states)
	}
	for cobIndex, modelIndex := range b.PieceMap {
		if cobIndex < len(b.VM.Pieces) && modelIndex >= 0 && modelIndex < len(states) {
			states[modelIndex] = b.VM.Pieces[cobIndex]
		}
	}
	model.FoldRootAngles(states, b.Model.Root, heading, pitch, bank)
	b.composeXform = model.ComposeInto(b.Model, states, modelPiece, b.composeXform, &b.composeChain)
	return b.composeXform.WorldOffset(), true
}

// SetSimulationRNG binds a session-owned stream to the production VM and all
// COB random opcodes. It does not mutate any global RNG [01 §7.1] I4.
func (b *Binding) SetSimulationRNG(sim *rng.Simulation) {
	if b == nil {
		return
	}
	b.SimulationRNG = sim
	if b.VM != nil {
		b.VM.SetSimulationRNG(sim)
	}
}

// SetSFXSink binds a presentation-only sink and visibility predicate to the
// production VM. The sink is also retained on the binding for composition
// diagnostics; it never mutates authoritative state [GAP T15] C19.
func (b *Binding) SetSFXSink(sink SFXSink, visible func(piece int, sfxType int32) bool) {
	if b == nil {
		return
	}
	b.SFXSink, b.SFXVisible = sink, visible
	if b.VM != nil {
		b.VM.SetSFXSink(sink)
		b.VM.SetSFXVisible(visible)
	}
}

// SetPresentationSink adapts a typed session event sink to the VM's SFX
// callback without inventing lifetimes or effect records.
func (b *Binding) SetPresentationSink(sink PresentationSink) {
	if b == nil {
		return
	}
	b.PresentationSink = sink
	if b.VM == nil {
		return
	}
	if sink == nil {
		b.VM.SetSFXSink(nil)
		return
	}
	b.VM.SetSFXSink(PresentationSinkAdapter{Sink: sink})
}

func bindingPath(req BindingRequest) (logical, unitName string, err error) {
	unitName = strings.TrimSpace(req.UnitName)
	logical = strings.TrimSpace(req.ScriptPath)
	if logical == "" && unitName != "" {
		logical = "scripts/" + strings.ToLower(unitName) + ".cob"
	}
	if logical != "" {
		logical = strings.ToLower(strings.TrimSpace(logical))
		if !strings.HasPrefix(logical, "scripts/") || !strings.HasSuffix(logical, ".cob") {
			err = fmt.Errorf("cob: script path %q must be a logical scripts/*.cob path", req.ScriptPath)
		}
	}
	if unitName == "" {
		err = fmt.Errorf("cob: unit name is empty")
	}
	if logical == "" && err == nil {
		err = fmt.Errorf("cob: script path is empty")
	}
	return logical, unitName, err
}

func providersFor(fs vfs.FSOps, logical string, winner vfs.EntryInfo) string {
	if sources, ok := fs.(interface{ Sources(string) []vfs.EntryInfo }); ok {
		entries := sources.Sources(logical)
		ids := make([]string, 0, len(entries))
		for _, entry := range entries {
			id := entry.Source.ProviderID()
			if id == "" {
				id = entry.Source.ProviderType
			}
			ids = append(ids, id)
		}
		if len(ids) != 0 {
			return strings.Join(ids, ", ")
		}
	}
	if id := winner.Source.ProviderID(); id != "" {
		return id
	}
	return "<none>"
}

func linkDiagnostics(program *Program, modelPieces, required []string, groups [][]string, logical, providers string) []BindingDiagnostic {
	diagnostics := make([]BindingDiagnostic, 0)
	if modelPieces == nil {
		diagnostics = append(diagnostics, BindingDiagnostic{Code: BindingMissingModel, Logical: logical, Provider: providers, Expected: "loaded 3DO piece hierarchy", Detail: "model piece list is nil"})
	}
	// A script piece the model lacks, and a script piece beyond the model's
	// piece count, are not refusals: retail's linking pass never rejects a
	// program, and ProTA 4.8's CORSILO and CORAMPH each declare one trailing
	// piece their model does not have [04 R-COB-01 §4]. LinkPieces gives them
	// retail's slot identity; linkNotes keeps them visible to tools.
	for i, name := range modelPieces {
		if strings.TrimSpace(name) == "" {
			diagnostics = append(diagnostics, BindingDiagnostic{Code: BindingUnresolvedPiece, Logical: logical, Provider: providers, Expected: fmt.Sprintf("model piece %d", i), Detail: "model piece name is empty"})
		}
	}

	seen := make(map[string]struct{}, len(required))
	for _, name := range required {
		name = strings.TrimSpace(name)
		if name == "" {
			diagnostics = append(diagnostics, BindingDiagnostic{Code: BindingMissingEntry, Logical: logical, Provider: providers, Expected: "non-empty COB entry point", Detail: "required entry point is empty"})
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		if _, ok := program.Scripts[name]; !ok {
			diagnostics = append(diagnostics, BindingDiagnostic{Code: BindingMissingEntry, Logical: logical, Provider: providers, Expected: name, Detail: "required COB entry point is unavailable"})
		}
	}
	for _, group := range groups {
		names := make([]string, 0, len(group))
		found := false
		groupSeen := make(map[string]struct{}, len(group))
		for _, name := range group {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if _, ok := groupSeen[name]; ok {
				continue
			}
			groupSeen[name] = struct{}{}
			names = append(names, name)
			if _, ok := program.Scripts[name]; ok {
				found = true
			}
		}
		if found {
			continue
		}
		if len(names) == 0 {
			names = []string{"non-empty COB entry point"}
		}
		diagnostics = append(diagnostics, BindingDiagnostic{
			Code: BindingMissingEntry, Logical: logical, Provider: providers,
			Expected: strings.Join(names, " or "), Detail: "none of the alternative COB entry points is available",
		})
	}
	// Diagnostics are sorted by code/expected/detail so callers receive stable
	// output even when multiple model or entry checks fail (I1).
	sort.SliceStable(diagnostics, func(i, j int) bool {
		if diagnostics[i].Code != diagnostics[j].Code {
			return diagnostics[i].Code < diagnostics[j].Code
		}
		if diagnostics[i].Expected != diagnostics[j].Expected {
			return diagnostics[i].Expected < diagnostics[j].Expected
		}
		return diagnostics[i].Detail < diagnostics[j].Detail
	})
	return diagnostics
}

// LinkPieces is retail's script-to-model piece link [04 R-COB-01 §4]. The
// unit's render-piece table starts in model depth-first order and is permuted
// in place, one script piece at a time: script piece i searches the table from
// slot i onward for the first record whose name matches (ASCII case folded)
// and swaps that record into slot i. Script piece i then owns whatever record
// slot i holds — the matched piece, or, when nothing from slot i onward
// matches, the unclaimed model piece already sitting there. A script piece at
// or beyond the model's piece count has no record at all and maps to -1.
//
// The result is injective: every model piece is owned by at most one script
// piece. Duplicate model names therefore go to successive script entries of
// that name, and a name whose match was already claimed or moved below the
// searching slot does not find it again.
//
// Presentation draws through this same map: each committed piece view carries
// its linked model index, so an in-range alias animates on screen as it does
// here, and a piece beyond the model draws nothing.
func LinkPieces(scriptPieces, modelPieces []string) []int {
	pieceMap := make([]int, len(scriptPieces))
	slots := make([]int, len(modelPieces))
	for i := range slots {
		slots[i] = i
	}
	for i, name := range scriptPieces {
		if i >= len(slots) {
			pieceMap[i] = -1
			continue
		}
		for j := i; j < len(slots); j++ {
			if pieceNameEqual(modelPieces[slots[j]], name) {
				slots[i], slots[j] = slots[j], slots[i]
				break
			}
		}
		pieceMap[i] = slots[i]
	}
	return pieceMap
}

// linkNotes describes each script piece that LinkPieces bound without a name
// match. The codes are the historical refusal codes, kept so a tool can
// classify the note; they are informational here [04 R-COB-01 §4].
func linkNotes(scriptPieces, modelPieces []string, pieceMap []int, logical string, providersOf func() string) []BindingDiagnostic {
	var notes []BindingDiagnostic
	providers := ""
	for i, name := range scriptPieces {
		matched := pieceMap[i] >= 0 && pieceNameEqual(modelPieces[pieceMap[i]], name)
		if !matched && providers == "" {
			providers = providersOf()
		}
		switch {
		case matched:
		case pieceMap[i] < 0:
			notes = append(notes, BindingDiagnostic{Code: BindingPieceCount, Logical: logical, Provider: providers, Expected: fmt.Sprintf("%d model pieces", len(modelPieces)), Detail: fmt.Sprintf("COB piece index %d (%q) is beyond the model and has no render piece", i, name)})
		default:
			notes = append(notes, BindingDiagnostic{Code: BindingUnresolvedPiece, Logical: logical, Provider: providers, Expected: name, Detail: fmt.Sprintf("COB piece index %d is absent from the 3DO hierarchy and takes model piece %q in its slot", i, modelPieces[pieceMap[i]])})
		}
	}
	return notes
}

// pieceNameEqual is the piece-name comparison of the link pass: a
// case-insensitive byte comparison that folds only ASCII letters and trims
// nothing [04 R-COB-01 §4].
func pieceNameEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}
