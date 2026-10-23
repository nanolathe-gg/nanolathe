package content

// The frozen simulation inputs of one battle (DESIGN_MULTIPLAYER §8.7, §8.8,
// contract M2-C8). FreezeSimulationInputs takes the prepared catalog and the
// selected map, AI and extension inputs, reads everything later unit creation
// and composition will read through the battle's capture — every admitted
// unit's program and model, including units no one has built yet — computes
// the content identity from the values the battle will consume, and seals the
// capture. These are Nanolathe protocol values, not retail findings.

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// ErrSimulationInputNotCaptured reports a supplied catalog or animation table
// that this battle's capture does not reproduce: it was compiled from other
// sources, or the sources changed since. Neither a cached value nor a matching
// path is provenance for a capture (DESIGN_MULTIPLAYER §8.7).
var ErrSimulationInputNotCaptured = errors.New("nanolathe: simulation input was not compiled from this battle's capture")

// SimulationInputRequest names what a battle's frozen inputs are made from.
// Catalog is the catalog compiled from the capture and prepared once under
// the selected rules and mutators. SimArt is an optional precompiled table; it
// is validated against the capture, and nil compiles one from it. MapOTA and
// MapTNT are the logical paths map resolution selected and MapSchema the
// selected schema's index in the catalog's map header; AIProfile is the
// profile name the planner will load; CommunityDigest is the effective
// Community table's digest; Mutators are the mutators already applied to
// Catalog, recorded and never applied again. Restrictions are the unit
// restrictions already applied to Catalog, before the mutators
// (docs/DESIGN_MODS_MUTATORS.md §15.5); they too are recorded and never
// applied again. They add no manifest entry: the restricted records carry
// their effect, so an unrestricted battle's digest is unchanged.
type SimulationInputRequest struct {
	Catalog         *Catalog
	SimArt          *SimArt
	MapOTA, MapTNT  string
	MapSchema       uint32
	AIProfile       string
	CommunityDigest [32]byte
	Mutators        Mutators
	Restrictions    Restrictions
	// PreparingRuleName and PreparingRuleBase are the exact resolved rule
	// identity used by catalog preparation, not a request to apply it again.
	// Empty strings retain legacy freeze support, but cannot admit a match.
	PreparingRuleName, PreparingRuleBase string
	// ModernAI is set when a Modern computer player plays: its candidates
	// read each builder's InstalledProducts, which then join the AI family.
	ModernAI bool
}

// SimulationInputs is one battle's frozen simulation content. Its accessors
// return the immutable values composition and later unit creation consume;
// Filesystem is the sealed view of the capture, which never falls through to
// the live providers.
type SimulationInputs struct {
	checkpointInputs *checkpointInputSnapshot // detached freeze-time diagnostics; never manifest bytes
	sources          *SimulationSources
	view             vfs.FSOps
	catalog          *Catalog
	simArt           *SimArt
	models           map[string]*model.Model
	// Captured definition-loader heights complete the parsed-model identity;
	// checkpoint validation must not reload bytes (DESIGN_MULTIPLAYER §16.3.6).
	modelTops  map[string]int32
	manifest   []SimulationInput
	provenance []SimulationInputProvenance
	digest     [32]byte
	// selection is the request's map, extension, restriction and mutator
	// selection as the freeze recorded it, its catalog and animation-table
	// pointers cleared and its mutators in their canonical spelling.
	// Admission compares a match configuration with it (DESIGN_MULTIPLAYER
	// §8.8).
	selection SimulationInputRequest
}

// Digest is the content identity: SHA-256 over the domain
// `nanolathe/sim-content/1` and the manifest's positional encoding. It is
// computed once at freeze and cannot fail.
func (i *SimulationInputs) Digest() [32]byte {
	if i == nil {
		return [32]byte{}
	}
	return i.digest
}

// Manifest returns a copy of the typed manifest in family, ordinal, key order.
func (i *SimulationInputs) Manifest() []SimulationInput {
	if i == nil {
		return nil
	}
	return slices.Clone(i.manifest)
}

// Provenance returns the local files behind the manifest's file-backed
// entries. It is diagnostic and never part of the identity.
func (i *SimulationInputs) Provenance() []SimulationInputProvenance {
	if i == nil {
		return nil
	}
	out := make([]SimulationInputProvenance, len(i.provenance))
	for k, p := range i.provenance {
		p.Files = slices.Clone(p.Files)
		out[k] = p
	}
	return out
}

// Catalog returns the prepared catalog the battle runs on. Like every
// compiled catalog it is immutable by convention; it is the same value the
// identity was computed from.
func (i *SimulationInputs) Catalog() *Catalog {
	if i == nil {
		return nil
	}
	return i.catalog
}

// SimArt returns the battle's animation table: the validated supplied table,
// or the one compiled from the capture.
func (i *SimulationInputs) SimArt() *SimArt {
	if i == nil {
		return nil
	}
	return i.simArt
}

// Model returns the frozen parsed model for a unit's authored object name,
// resolved the way unit creation names it (`objects3d/<name>.3do`, the name
// trimmed and ASCII-folded). ok is false for a model the capture could not
// load; creation then fails exactly as it would have.
func (i *SimulationInputs) Model(key string) (*model.Model, bool) {
	if i == nil {
		return nil, false
	}
	m := i.models[frozenUnitModelPath(key)]
	return m, m != nil
}

// Filesystem returns the sealed view of the capture. A path the capture never
// answered is a defined miss.
func (i *SimulationInputs) Filesystem() vfs.FSOps {
	if i == nil {
		return nil
	}
	return i.view
}

// UncapturedLookups lists the lookups made through the sealed view that the
// capture could not answer, as "<operation> <logical path>". Each was
// answered as a miss. A battle whose composition and play read only frozen
// content reports none.
func (i *SimulationInputs) UncapturedLookups() []string {
	if i == nil || i.sources == nil {
		return nil
	}
	return i.sources.snap.uncapturedLookups()
}

// The selection accessors below report what the inputs were frozen for, as
// the freeze request named it. They let match admission compare an agreed
// configuration with the content the battle will run on (DESIGN_MULTIPLAYER
// §8.8) without re-deriving the selection from the manifest's digests.

// MapName returns the map name the capture was taken for, trimmed: the name
// the lobby or the command line selected (CaptureSimulationSources).
func (i *SimulationInputs) MapName() string {
	if i == nil || i.sources == nil {
		return ""
	}
	return i.sources.mapName
}

// MapFiles returns the logical OTA and TNT paths map resolution selected, as
// the freeze request named them.
func (i *SimulationInputs) MapFiles() (ota, tnt string) {
	if i == nil {
		return "", ""
	}
	return i.selection.MapOTA, i.selection.MapTNT
}

// MapSchema returns the selected schema's index in the catalog's map header,
// as the freeze request named it. An index at or past the header's schema
// count was recorded as a defined absence.
func (i *SimulationInputs) MapSchema() uint32 {
	if i == nil {
		return 0
	}
	return i.selection.MapSchema
}

// CommunityDigest returns the effective Community table's digest the freeze
// recorded.
func (i *SimulationInputs) CommunityDigest() [32]byte {
	if i == nil {
		return [32]byte{}
	}
	return i.selection.CommunityDigest
}

// PreparingRule returns the resolved rule identity under which this catalog
// was prepared. It is admission metadata; the content digest already covers
// prepared definition values, while the match identity names the rule set
// (DESIGN_MULTIPLAYER §16.3.34). Absence remains explicit for older callers.
func (i *SimulationInputs) PreparingRule() (name, base string) {
	if i == nil {
		return "", ""
	}
	return i.selection.PreparingRuleName, i.selection.PreparingRuleBase
}

// Mutators returns the mutators the catalog had already been prepared with
// when it was frozen, each identity factor spelled as the zero value: the
// vector the manifest records, which is never applied again.
func (i *SimulationInputs) Mutators() Mutators {
	if i == nil {
		return Mutators{}
	}
	return i.selection.Mutators
}

// Restrictions returns the unit restrictions the catalog had already been
// prepared with when it was frozen, before its mutators
// (docs/DESIGN_MODS_MUTATORS.md §15.5). They are recorded, never applied
// again.
func (i *SimulationInputs) Restrictions() Restrictions {
	if i == nil {
		return Restrictions{}
	}
	return i.selection.Restrictions
}

// canonicalMutators spells every identity factor as the zero value, the one
// spelling the manifest's step vector gives it.
func canonicalMutators(m Mutators) Mutators {
	for _, f := range []*Factor{
		&m.BuildSpeed, &m.BuildCost, &m.Health, &m.Damage, &m.AreaOfEffect, &m.Sight,
		&m.Radar, &m.Income, &m.Salvage, &m.FireRate, &m.UnitSpeed, &m.BuilderSpeed, &m.SmartMakers,
	} {
		if f.IsIdentity() {
			*f = Factor{}
		}
	}
	return m
}

// frozenUnitModelPath is the model path unit creation uses for an authored
// object name.
func frozenUnitModelPath(objectName string) string {
	identity := CanonicalKey(strings.TrimSpace(objectName))
	if identity == "" {
		return ""
	}
	return "objects3d/" + identity + ".3do"
}

// FreezeSimulationInputs freezes one battle's simulation content from its
// capture. It may be called once per capture; a supplied catalog or animation
// table that the capture does not reproduce is refused with
// ErrSimulationInputNotCaptured and leaves the capture unfrozen.
//
// Design reading: the prepared catalog is retained as supplied rather than
// cloned again. Preparation already cloned it whenever rules or mutators
// changed a definition, compiled catalogs are immutable by convention, and a
// battle without preparation runs on the handed-in catalog (the session's
// existing contract); the identity is computed from that same value.
func FreezeSimulationInputs(sources *SimulationSources, r SimulationInputRequest) (*SimulationInputs, error) {
	if sources == nil || sources.snap == nil {
		return nil, fmt.Errorf("nanolathe: simulation input freeze failed: logical path <capture>, providers searched [none], expected a captured source set")
	}
	sources.mu.Lock()
	defer sources.mu.Unlock()
	if sources.frozen {
		return nil, fmt.Errorf("nanolathe: simulation input freeze failed: logical path <capture>, providers searched [none], expected a capture that has not been frozen")
	}
	if r.Catalog == nil {
		return nil, fmt.Errorf("nanolathe: simulation input freeze failed: logical path <catalog>, providers searched [none], expected a catalog compiled from the capture")
	}
	vector, tail, ok := mutatorVector(r.Mutators)
	if !ok {
		return nil, fmt.Errorf("nanolathe: simulation input freeze failed: logical path <mutators>, providers searched [none], expected factors on the mutator step list, got %q", r.Mutators.String())
	}
	f := &freezer{
		view:      sources.view,
		transient: sources.snap.newView(nil, true),
		cat:       r.Catalog,
		inputs:    &SimulationInputs{sources: sources, catalog: r.Catalog, models: make(map[string]*model.Model)},
	}
	f.freezeModels()
	f.inputs.modelTops = f.modelTops
	if err := f.checkCatalogCapture(r); err != nil {
		return nil, err
	}
	f.freezeScripts()
	art := r.SimArt
	if art != nil {
		if err := art.matchesCapture(f.transient, r.Catalog); err != nil {
			return nil, err
		}
	} else {
		art = CompileSimArt(f.transient, r.Catalog)
	}
	f.inputs.simArt = art
	f.entries = append(f.entries, simArtEntries(art)...)
	f.freezeMap(sources.mapName, r)
	f.freezeAI(r.AIProfile)
	if r.ModernAI {
		f.entries = append(f.entries, installedProductsEntry(r.Catalog))
	}
	community := newSemanticDigest("nanolathe/sim-content/community/1")
	community.digest(r.CommunityDigest)
	mutators := newSemanticDigest("nanolathe/sim-content/mutators/1")
	for _, step := range vector {
		mutators.u8(step)
	}
	// Builder speed's own index, then Smart makers' switch, follow the eleven
	// fixed bytes only when set, so every earlier digest holds
	// (docs/DESIGN_MODS_MUTATORS.md "Builder speed", "Smart makers").
	for _, step := range tail {
		mutators.u8(step)
	}
	f.entries = append(f.entries,
		SimulationInput{Family: SimulationFamilyExtension, Presence: SimulationInputPresent, Key: "community", SemanticDigest: community.sum()},
		SimulationInput{Family: SimulationFamilyExtension, Presence: SimulationInputPresent, Key: "mutators", SemanticDigest: mutators.sum()},
	)
	f.entries = append(f.entries, catalogEntries(r.Catalog)...)
	sortSimulationInputs(f.entries)
	if err := validateSimulationInputs(f.entries); err != nil {
		return nil, err
	}
	f.inputs.manifest = f.entries
	f.inputs.provenance = f.provenance
	f.inputs.digest = simulationManifestDigest(f.entries)
	f.inputs.selection = r
	f.inputs.selection.Catalog, f.inputs.selection.SimArt = nil, nil
	f.inputs.selection.Mutators = canonicalMutators(r.Mutators)
	f.inputs.view = sources.snap.newView(f.inputs, false)
	sources.snap.seal()
	sources.frozen = true
	f.inputs.checkpointInputs = snapshotCheckpointInputs(f.inputs)
	return f.inputs, nil
}

// freezer accumulates one freeze. view retains what it reads; transient reads
// bytes that are folded into a compiled value at once (animation banks) and
// keeps only their metadata.
type freezer struct {
	view, transient vfs.FSOps
	cat             *Catalog
	inputs          *SimulationInputs
	entries         []SimulationInput
	provenance      []SimulationInputProvenance
	// modelTops is the definition loader's height walk per catalog model path,
	// present only where the file parsed [02 R-CAT-01 §7].
	modelTops map[string]int32
}

func (f *freezer) addProvenance(family uint8, key string, ordinal uint32, paths ...string) {
	p := SimulationInputProvenance{Family: family, Key: key, Ordinal: ordinal}
	for _, logical := range paths {
		info, err := f.view.Stat(logical)
		if err != nil || info.IsDir {
			continue
		}
		p.Files = append(p.Files, SimulationSourceFile{
			LogicalPath: info.Path, ProviderID: info.Source.ProviderID(),
			ProviderType: info.Source.ProviderType, Size: info.Size,
		})
	}
	f.provenance = append(f.provenance, p)
}

// freezeModels freezes every model the battle can bind or validate. A unit
// record's model — under both the creation path rule and the definition
// loader's resource rule — is parsed: unit creation binds that parse, and its
// identity is the piece hierarchy, origins, geometry and derived height.
// Units not yet created are included; that is the point. Feature and weapon
// models are only ever checked to load at entry, never bound, so their
// authored bytes are captured for that check and are their identity.
func (f *freezer) freezeModels() {
	unitPaths := make(map[string]struct{})
	for _, u := range f.cat.unitRecordView() {
		if u == nil {
			continue
		}
		if p := frozenUnitModelPath(u.ObjectName); p != "" {
			unitPaths[p] = struct{}{}
		}
		unitPaths[requiredUnitModelPath(u.ObjectName)] = struct{}{}
	}
	otherPaths := make(map[string]struct{})
	for _, w := range f.cat.Weapons {
		if w != nil {
			if p := requiredModelPath(w.Model); p != "" {
				otherPaths[p] = struct{}{}
			}
		}
	}
	for _, def := range f.cat.Features {
		if def != nil {
			if p := requiredModelPath(def.Object); p != "" {
				otherPaths[p] = struct{}{}
			}
		}
	}
	paths := make(map[string]bool, len(unitPaths)+len(otherPaths))
	for p := range otherPaths {
		paths[p] = false
	}
	for p := range unitPaths {
		paths[p] = true
	}
	f.modelTops = make(map[string]int32)
	for _, logical := range sortedKeys(paths) {
		entry := SimulationInput{Family: SimulationFamilyModel, Key: snapshotKey(logical), Presence: SimulationInputAbsent}
		// The formats loader's whole-file cap; smaller loader caps still
		// apply when they read the captured bytes.
		data, err := f.view.ReadFileLimit(logical, 1<<30)
		switch {
		case err != nil:
		case !paths[logical]:
			entry.Presence, entry.SemanticDigest = SimulationInputPresent, fileDigest(data)
		default:
			three, parseErr := formats.LoadThreeDO(data)
			if parseErr != nil || model.ValidateSource(three) != nil {
				break
			}
			top := three.ModelTop()
			f.modelTops[logical] = top
			if mdl, loadErr := model.Load(f.view, logical); loadErr == nil && mdl != nil {
				f.inputs.models[logical] = mdl
				entry.Presence = SimulationInputPresent
				entry.SemanticDigest = modelSemanticDigest(mdl, top)
			}
		}
		f.entries = append(f.entries, entry)
		f.addProvenance(SimulationFamilyModel, entry.Key, 0, logical)
	}
}

// capturedProgram loads a unit script from the capture the way the catalog
// compiler does: a missing, unreadable, malformed or empty program is absent
// (fillUnitRecordScripts).
func capturedProgram(fs vfs.FSOps, logical string) (*cob.Program, bool) {
	info, err := fs.Stat(logical)
	if err != nil || info.IsDir {
		return nil, false
	}
	data, err := fs.ReadFileLimit(logical, 4<<20)
	if err != nil {
		return nil, false
	}
	prog, err := cob.Load(data)
	if err != nil || prog == nil || len(prog.Code) == 0 {
		return nil, false
	}
	return prog, true
}

func usableProgram(p *cob.Program) bool { return p != nil && len(p.Code) != 0 }

// checkCatalogCapture refuses a catalog the capture does not reproduce. Every
// value the catalog carries from a file outside its definition records is
// compared with what the capture holds: each record's compiled program, each
// record's model height, and the selected map's header. A catalog compiled
// through this capture always agrees; a cached catalog compiled before a
// loose-file edit does not.
//
// Design reading: the definition records themselves are not recompiled to be
// compared — that would repeat the whole catalog compile at every battle
// entry — and need not be, because the identity is computed from the records
// as the battle consumes them.
func (f *freezer) checkCatalogCapture(r SimulationInputRequest) error {
	reject := func(logical, expected string) error {
		return fmt.Errorf("%w: logical path %s, providers searched [%s], expected %s", ErrSimulationInputNotCaptured, logical, strings.Join(searchedProviderIDs(f.view, logical), ", "), expected)
	}
	programs := make(map[string]*cob.Program)
	loaded := make(map[string]bool)
	for _, u := range f.cat.unitRecordView() {
		if u == nil {
			continue
		}
		modelPath := requiredUnitModelPath(u.ObjectName)
		top, ok := f.modelTops[modelPath]
		if !ok {
			return reject(modelPath, fmt.Sprintf("the model unit %q was compiled with", u.UnitName))
		}
		if u.ModelTopFixed != top || u.ModelTop != (top>>16)&0xFF {
			return reject(modelPath, fmt.Sprintf("model height %d for unit %q, the catalog carries %d", top, u.UnitName, u.ModelTopFixed))
		}
		scriptPath := vfs.ResourcePath("scripts", CanonicalKey(u.UnitName), "cob")
		if !loaded[scriptPath] {
			programs[scriptPath], _ = capturedProgram(f.view, scriptPath)
			loaded[scriptPath] = true
		}
		captured := programs[scriptPath]
		switch {
		case usableProgram(u.Script) != usableProgram(captured):
			return reject(scriptPath, fmt.Sprintf("the program unit %q was compiled with", u.UnitName))
		case usableProgram(captured) && programSemanticDigest(u.Script) != programSemanticDigest(captured):
			return reject(scriptPath, fmt.Sprintf("the program unit %q was compiled with", u.UnitName))
		}
	}
	if strings.TrimSpace(r.MapOTA) == "" {
		return nil
	}
	header := f.cat.Maps[CanonicalKey(baseNameWithoutExt(snapshotKey(r.MapOTA)))]
	if header == nil {
		return nil
	}
	otaData, err := f.view.ReadFileLimit(header.LogicalOTA, int64(formats.DefaultTDFLimits().MaxBytes))
	if err != nil {
		return reject(header.LogicalOTA, "the map the catalog's header was compiled from")
	}
	ota, err := formats.LoadOTA(otaData)
	if err != nil {
		return reject(header.LogicalOTA, "the map the catalog's header was compiled from")
	}
	tnt, err := loadTNTHeaderLite(f.view, header.LogicalTNT, f.cat.compileLimits().TNTBytes)
	if err != nil {
		return reject(header.LogicalTNT, "the terrain the catalog's header was compiled from")
	}
	if compileMapHeader(header.LogicalOTA, header.LogicalTNT, header.OTAProvenance, header.TNTProvenance, ota, tnt).Hash != header.Hash {
		return reject(header.LogicalOTA, "the map header the catalog carries")
	}
	return nil
}

// freezeScripts records every admitted unit's program: the compiled program
// the binder uses, or — for a record that has none — whatever the binder and
// the allocator's pre-check would read from the capture, so those reads are
// answered from it later. Units not yet created are included.
func (f *freezer) freezeScripts() {
	for i, u := range f.cat.unitRecordView() {
		if u == nil {
			continue
		}
		entry := SimulationInput{Family: SimulationFamilyCOB, Key: "unit/" + u.CanonicalKey, Ordinal: uint32(i + 1)}
		// The strict binder's own path rule (cob.BindStrict's bindingPath).
		bindPath := strings.ToLower(strings.TrimSpace("scripts/" + u.UnitName + ".cob"))
		// The binder's provider report for this path is diagnostic text only;
		// it is captured so the frozen view reports it exactly.
		f.view.(sourceLister).Sources(bindPath)
		if usableProgram(u.Script) {
			entry.Presence = SimulationInputPresent
			entry.SemanticDigest = programSemanticDigest(u.Script)
		} else {
			// A record without a compiled program: the strict binder reads
			// its script path, and the allocator's pre-check asks the loader
			// by unit name and by catalog key (units.World.hasLoadableCOB).
			for _, name := range []string{u.UnitName, u.CanonicalKey} {
				if strings.TrimSpace(name) != "" {
					_, _, _ = cob.LoadFromFS(f.view, name)
				}
			}
			if prog, ok := capturedProgram(f.view, bindPath); ok {
				entry.Presence = SimulationInputFallback
				entry.FallbackKey = snapshotKey(bindPath)
				entry.SemanticDigest = programSemanticDigest(prog)
			}
		}
		f.entries = append(f.entries, entry)
		f.addProvenance(SimulationFamilyCOB, entry.Key, entry.Ordinal, bindPath)
	}
}

// fileDigest is the identity of an authored input file consumed whole.
func fileDigest(data []byte) [32]byte {
	d := newSemanticDigest("nanolathe/sim-content/file/1")
	d.u64(uint64(len(data)))
	d.write(data)
	return d.sum()
}

// freezeFile records one whole input file read through the capture.
func (f *freezer) freezeFile(family uint8, key, logical string) SimulationInput {
	entry := SimulationInput{Family: family, Key: key, Presence: SimulationInputAbsent}
	if data, err := f.view.ReadFileLimit(logical, captureEagerLimit); err == nil {
		entry.Presence = SimulationInputPresent
		entry.SemanticDigest = fileDigest(data)
	}
	return entry
}

// freezeMap records the selected map's inputs: the OTA and TNT the battle
// reads, the catalog's compiled header and the selected schema. A skirmish
// that resolved its OTA through the translated-name retry records the
// requested name as a fallback to the file it used, and the translation table
// that mapped it [08 R-CAMP-01 §11].
func (f *freezer) freezeMap(requested string, r SimulationInputRequest) {
	ota := strings.TrimSpace(r.MapOTA)
	if ota == "" {
		return
	}
	otaKey := snapshotKey(ota)
	entry := f.freezeFile(SimulationFamilyMap, otaKey, ota)
	if base := captureMapBase(requested); base != "" {
		if want := snapshotKey("maps/" + base + ".ota"); want != otaKey && entry.Presence == SimulationInputPresent {
			entry.Key, entry.Presence, entry.FallbackKey = want, SimulationInputFallback, otaKey
			table := f.freezeFile(SimulationFamilyMap, "gamedata/translate.tdf", "gamedata/translate.tdf")
			f.entries = append(f.entries, table)
			f.addProvenance(SimulationFamilyMap, table.Key, 0, "gamedata/translate.tdf")
		}
	}
	f.entries = append(f.entries, entry)
	f.addProvenance(SimulationFamilyMap, entry.Key, 0, ota)
	if tnt := strings.TrimSpace(r.MapTNT); tnt != "" {
		terrain := f.freezeFile(SimulationFamilyMap, snapshotKey(tnt), tnt)
		f.entries = append(f.entries, terrain)
		f.addProvenance(SimulationFamilyMap, terrain.Key, 0, tnt)
	}
	mapKey := CanonicalKey(baseNameWithoutExt(otaKey))
	header := f.cat.Maps[mapKey]
	headerEntry := SimulationInput{Family: SimulationFamilyMap, Key: "header/" + mapKey, Presence: SimulationInputAbsent}
	schemaEntry := SimulationInput{Family: SimulationFamilyMap, Key: "schema", Ordinal: r.MapSchema, Presence: SimulationInputAbsent}
	if header != nil {
		d := newSemanticDigest("nanolathe/sim-content/map/header/1")
		d.text(header.Hash)
		d.text(header.LogicalOTA)
		d.text(header.LogicalTNT)
		headerEntry.Presence, headerEntry.SemanticDigest = SimulationInputPresent, d.sum()
		if uint64(r.MapSchema) < uint64(len(header.Schemas)) {
			s := header.Schemas[r.MapSchema]
			sd := newSemanticDigest("nanolathe/sim-content/map/schema/1")
			sd.text(s.Name)
			sd.text(s.Type)
			sd.text(s.AIProfile)
			sd.s32(s.SurfaceMetal)
			sd.s32(s.MohoMetal)
			sd.s32(s.HumanMetal)
			sd.s32(s.HumanEnergy)
			sd.s32(s.ComputerMetal)
			sd.s32(s.ComputerEnergy)
			sd.text(s.MeteorWeapon)
			sd.s32(s.MeteorRadius)
			sd.f64(s.MeteorDensity)
			sd.f64(s.MeteorDuration)
			sd.f64(s.MeteorInterval)
			sd.s64(int64(s.StartPosCount))
			schemaEntry.Presence, schemaEntry.SemanticDigest = SimulationInputPresent, sd.sum()
		}
	}
	f.entries = append(f.entries, headerEntry, schemaEntry)
}

// freezeAI records the selected AI profile as the planner loads it: the named
// profile, or its `ai/default.txt` fallback when that file is missing, as
// authored bytes so directive and argument order are preserved
// (ai.LoadProfile, [08 "Established AI-facing data and rooted planner"]).
func (f *freezer) freezeAI(name string) {
	clean := strings.TrimSpace(name)
	if clean == "" {
		clean = "default"
	}
	if strings.HasSuffix(strings.ToLower(clean), ".txt") {
		clean = strings.TrimSpace(clean[:len(clean)-4])
		if clean == "" {
			clean = "default"
		}
	}
	primary := path.Join("ai", clean+".txt")
	const fallback = "ai/default.txt"
	entry := SimulationInput{Family: SimulationFamilyAI, Key: snapshotKey(primary), Presence: SimulationInputAbsent}
	data, err := f.view.ReadFileLimit(primary, 1<<20)
	used := primary
	switch {
	case err == nil:
		entry.Presence, entry.SemanticDigest = SimulationInputPresent, fileDigest(data)
	case errors.Is(err, vfs.ErrNotFound) && !strings.EqualFold(primary, fallback):
		if data, err = f.view.ReadFileLimit(fallback, 1<<20); err == nil {
			entry.Presence, entry.FallbackKey, entry.SemanticDigest = SimulationInputFallback, fallback, fileDigest(data)
			used = fallback
		}
	}
	f.entries = append(f.entries, entry)
	f.addProvenance(SimulationFamilyAI, entry.Key, 0, used)
}

// installedProductsEntry is the AI-family entry of the products the
// builders' authored pages install, which only the Modern AI reads
// (BuildMenuPage.InstalledProducts). It is an entry of its own, present only
// when a Modern computer plays, so a battle without one keeps the identity it
// had before the Modern AI read them.
func installedProductsEntry(c *Catalog) SimulationInput {
	d := newSemanticDigest("nanolathe/sim-content/ai/installed-products/1")
	for _, key := range sortedKeys(c.BuildMenus) {
		p := c.BuildMenus[key]
		if p == nil || len(p.InstalledProducts) == 0 {
			continue
		}
		d.text(key)
		d.texts(p.InstalledProducts)
	}
	return SimulationInput{Family: SimulationFamilyAI, Key: "modern/installed-products", Presence: SimulationInputPresent, SemanticDigest: d.sum()}
}

// validateSimulationInputs enforces the manifest's representation limits
// before a digest is formed, so Digest itself cannot fail: keys are 1..1024
// bytes without NUL, a fallback key appears exactly on fallback entries, and
// no two entries share family, ordinal and key.
func validateSimulationInputs(entries []SimulationInput) error {
	bad := func(e SimulationInput, why string) error {
		return fmt.Errorf("nanolathe: simulation input freeze failed: logical path %q, providers searched [manifest family %d], expected %s", e.Key, e.Family, why)
	}
	for i, e := range entries {
		if e.Family < SimulationFamilyCatalog || e.Family > SimulationFamilyExtension {
			return bad(e, "a published family")
		}
		if e.Presence > SimulationInputFallback {
			return bad(e, "a published presence state")
		}
		if e.Key == "" || len(e.Key) > simulationInputKeyLimit || strings.IndexByte(e.Key, 0) >= 0 {
			return bad(e, "a key of 1..1024 bytes without NUL")
		}
		if (e.Presence == SimulationInputFallback) != (e.FallbackKey != "") {
			return bad(e, "a fallback key exactly on a fallback entry")
		}
		if len(e.FallbackKey) > simulationInputKeyLimit || strings.IndexByte(e.FallbackKey, 0) >= 0 {
			return bad(e, "a fallback key of at most 1024 bytes without NUL")
		}
		if i > 0 {
			prev := entries[i-1]
			if prev.Family == e.Family && prev.Ordinal == e.Ordinal && prev.Key == e.Key {
				return bad(e, "one entry per family, ordinal and key")
			}
		}
	}
	return nil
}

// matchesCapture accepts a precompiled table only when this battle's catalog
// asks it the same questions and every file it read still has the identity it
// had: the same provider entry and archive stamp for an archive member, the
// same bytes for anything else (DESIGN_MULTIPLAYER §8.7).
func (a *SimArt) matchesCapture(view vfs.FSOps, cat *Catalog) error {
	reject := func(logical, why string) error {
		return fmt.Errorf("%w: logical path %s, providers searched [%s], expected %s", ErrSimulationInputNotCaptured, logical, strings.Join(searchedProviderIDs(view, logical), ", "), why)
	}
	if !a.compiled {
		return reject("anims", "an animation table compiled from content")
	}
	if banks, features := simArtRequests(cat); simArtRequestDigest(banks, features) != a.requests {
		return reject("anims", "an animation table compiled for this battle's catalog")
	}
	for _, src := range a.sources {
		info, err := view.Stat(src.path)
		found := err == nil && !info.IsDir
		if found != src.found {
			return reject(src.path, "the animation bank the table was compiled from")
		}
		if !found {
			continue
		}
		if src.bytes {
			digest, err := simArtStreamDigest(view, src.path)
			if err != nil || isArchiveProvider(info.Source) || digest != src.digest {
				return reject(src.path, "the animation bank bytes the table was compiled from")
			}
			continue
		}
		stamp, err := view.CacheStamp(src.path)
		if !isArchiveProvider(info.Source) || info.Source != src.provider || info.Size != src.size || err != nil || stamp != src.stamp {
			return reject(src.path, "the archived animation bank the table was compiled from")
		}
	}
	return nil
}
