// This file implements the two-stage catalog construction [02 §5] C1,
// validation [SPEC_CONFLICTS SC2], cloning, and model sorting [03 §2.4] C13.

package content

import (
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// requiredContentError keeps required-resource failures actionable at the
// content boundary. Concrete VFS overlays expose Sources; focused fixtures
// expose only Stat, so both paths are supported without widening FSOps
// [AGENTS.md diagnostics].
func requiredContentError(fs vfs.FSOps, logical, expected string, cause error) error {
	providers := searchedProviderIDs(fs, logical)
	return fmt.Errorf("nanolathe: required authored resource: logical path %s, providers searched [%s], expected %s: %w", logical, strings.Join(providers, ", "), expected, portableContentCause(cause))
}

// Resource diagnostics already carry the logical path and portable provider.
// Keep OS filenames out of their display while retaining the original cause
// for errors.Is/errors.As (DESIGN_CONTENT_VFS C13).
func portableContentCause(cause error) error {
	var pathError *fs.PathError
	if errors.As(cause, &pathError) {
		return contentReadCause{cause: cause, message: pathError.Op + ": " + portableContentCause(pathError.Err).Error()}
	}
	return cause
}

type contentReadCause struct {
	cause   error
	message string
}

func (e contentReadCause) Error() string { return e.message }
func (e contentReadCause) Unwrap() error { return e.cause }

func searchedProviderIDs(fs vfs.FSOps, logical string) []string {
	providers := make([]string, 0, 2)
	if sourceLister, ok := fs.(interface{ Sources(string) []vfs.EntryInfo }); ok {
		for _, info := range sourceLister.Sources(logical) {
			id := info.Source.ProviderID()
			if id == "" {
				id = "unknown"
			}
			providers = append(providers, id)
		}
	}
	if len(providers) == 0 {
		if info, err := fs.Stat(logical); err == nil {
			id := info.Source.ProviderID()
			if id == "" {
				id = "unknown"
			}
			providers = append(providers, id)
		}
	}
	return providers
}

func unitScriptMissingError(fs vfs.FSOps, logical string) error {
	return fmt.Errorf("nanolathe: unit script missing: logical path %s, providers searched [%s], expected COB program", logical, strings.Join(searchedProviderIDs(fs, logical), ", "))
}

// WeaponDuplicate records sections that shared one weapon record slot, for
// diagnostics [02 §5 R-CONTENT-02]. Keys are in discovery order; Winner is
// the last key — the later section's name, which owns the slot after a
// whole-record replacement. ID -1 is the shared scratch slot of the ID-less
// sections: retail writes each of them just before record 0, the last one
// wins it, and the runtime name scan never reaches that slot, so its winner
// is unreachable by name here too. The superseded name matches no record —
// an FBI link that names it resolves to the record-0 inactive sentinel
// [02 §5 R-CONTENT-02]. Determinism comes from deterministic discovery
// order (I1).
type WeaponDuplicate struct {
	ID     int32
	Keys   []string // discovery order, winner last
	Winner string   // Keys[len(Keys)-1] when Keys non-empty
}

// Catalog is the compiled, immutable content catalog [02 §5] [PLAN 02 Public API].
// Maps are keyed by CanonicalKey (case-insensitive) [02 §5]; Sides is indexed
// by SIDE ordinal [02 §6]; Maps holds headers only in this phase [02 "Map files"].
// Definitions carry DefinitionHeader with canonical key/provenance/hash.
// The catalog is read-only after Compile; sim packages take *Catalog and never mutate.
type Catalog struct {
	Units       map[string]*UnitDef // first equal name, including empty [02 R-CAT-01 §5]
	unitRecords []*UnitDef          // all retained non-sentinel records in immutable ID order
	// Categories is the sorted case-insensitive unit-membership registry
	// compiled from all UnitDef category and target fields [R-P0-03].
	Categories *CategoryRegistry
	Weapons    map[string]*WeaponDef     // key = CanonicalKey(section name) [02 §5]
	Features   map[string]*FeatureDef    // key = CanonicalKey(feature name) [02 §5]
	Movement   map[string]*MovementClass // key = CanonicalKey(class Name) [02 "Movement class record"]
	Sides      []*SideDef                // index = SIDE ordinal [02 §6] C8
	Sounds     map[string]*SoundCategory // key = CanonicalKey(category name) [02 "Sound category record"]
	Maps       map[string]*MapHeader     // key = CanonicalKey(basename) [02 "Map files"]
	LOS        *LOSTables                // compiled gamedata/los.tdf [02 §6] C15 [PLAN_02]
	Sight      *SightShapes              // compiled anims/vismask*.gaf sight shapes [03 §3.2]
	Meteor     *MeteorDefaults           // compiled gamedata/meteor.tdf [02 §6] C15 [PLAN_02]

	// AIProfiles holds ai/*.txt profiles (10 in retail, incl default.txt) [08 "Computer-controlled players"].
	// Not in the minimal PLAN_02 Public API snippet but discovery is part of WU-02-6 and consumed by phase 11.
	AIProfiles map[string]*AIProfile

	// BuildMenus holds the per-builder CANBUILD pages of sidedata.tdf
	// [02 "Build-menu catalog keys"]. The downloadable enforcement walks
	// their button names at Compile time [02 "Unit record"]; phase 8's
	// construction UI consumes pages directly.
	BuildMenus map[string]*BuildMenuPage
	// DownloadPlacements retains safely representable download/*.tdf items in
	// deterministic union-enumeration and section order [02 R-CAT-01 §8].
	// Resolution flags preserve the independent retail passes; the page accessor
	// exposes only fully resolved placements. Authored BUTTON slots remain
	// explicit because a generated page can be sparse.
	DownloadPlacements []DownloadMenuPlacement

	// Aliases holds the gamedata/allsound.tdf alias registrations (cap 255,
	// 32-byte names) [02 "Sound aliases"]. Phase 13's audio path resolves
	// sound names through them; they live here so no downstream package
	// re-parses a TDF.
	Aliases map[string]*SoundAlias
	// AliasOrder preserves gamedata/allsound.tdf section order for runtime
	// registration identity [03 §8.3].
	AliasOrder []*SoundAlias

	// Warnings collects non-fatal load diagnostics verbatim, e.g. the
	// downloadable enforcement's "Hey! Somebody forgot to set
	// downloadable=1 for %s" [02 "Unit record"]. The caller owns display.
	Warnings []string

	Manifest string // vfs.ManifestHash() [PLAN 02]
	Hash     string // sha256 over canonical definition bytes including defaults, stable across runs (I1) [02 §5] C12

	// Model catalog for [03 §2.4] C13: distinct ObjectName values sorted case-insensitively
	// before caching per-unit-type pointer. Load geometry in phase 6.
	sortedModels []string       // distinct model names sorted case-insensitively [03 §2.4] C13
	modelIndex   map[string]int // CanonicalKey(modelName) -> index in sortedModels [03 §2.4] C13

	// Weapon index compiled once for stable deterministic lookup [02 §5 R-CONTENT-02].
	// One record per slot: a same-ID section replaced the earlier record whole,
	// and ID-less sections are inert (scratch slot before record 0, never
	// enters the map). Duplicates are exposed for diagnostics.
	weaponRecords    []*WeaponDef         // immutable slot-order view for allocation-free runtime lookup
	weaponByID       map[int32]*WeaponDef // ID -> the record occupying that slot (I1)
	weaponDuplicates []WeaponDuplicate    // duplicate ID diagnostics, sorted by ID
}

// Compile builds a Catalog from the VFS [02 §5] C1 two-stage discover → parse → link.
//
// Stage 1 discovers and parses every family into typed records so enumeration
// order cannot leak into identity; Stage 2 links cross-references (weapon1..3 on
// a unit resolve only after all weapons compile) [02 §5] C1. It uses the typed
// accessor family only [02 §4] via the compile_* helpers, never a new conversion
// path. Discovery walks logical paths through VFS per plan's table; filter by
// extension, recursive for features [PLAN 02 Discovery].
//
// It also sorts the model catalog case-insensitively before caching per-unit-type
// pointer [03 §2.4] C13 and computes Catalog.Hash over canonical bytes including
// defaults, independent of map iteration, identical across two runs (I1) [02 §5] C12.
func Compile(fs vfs.FSOps) (*Catalog, error) {
	return CompileWithProgress(fs, nil)
}

// CompileWithProgress is Compile with an observer. The observer is told when
// each family finishes, and is told the running percentage inside the map
// census, which is the one family whose cost is proportional to the install.
// A nil observer makes this exactly Compile.
func CompileWithProgress(fs vfs.FSOps, report Progress) (*Catalog, error) {
	if fs == nil {
		return nil, fmt.Errorf("content: nil VFS")
	}

	// Stage 1: discover and parse every family into typed records [02 §5] C1.
	// Each compiler walks the VFS logical paths per PLAN_02 Discovery, filtering
	// by extension already (units *.fbi, weapons *.tdf — the family is exactly
	// Weapons/*.tdf; gamedata/weapons.tdf is never read [02 §5 R-CONTENT-02] —
	// features recursive features/<group>/*.tdf, etc.).
	weapons, weaponDuplicates, err := CompileWeaponsWithDuplicates(fs)
	if err != nil {
		// Weapons are required for linking but not directly part of Validate's
		// fatal trio; propagate error so whole-install compile is error-free.
		return nil, err
	}
	report.Report(FamilyWeapons, 100)
	unitResult, err := compileUnitsWithLanguage(fs, "")
	if err != nil {
		return nil, err
	}
	units := unitResult.units
	records := unitResult.records
	report.Report(FamilyUnits, 100)
	categories, err := compileCategoryRecords(records, units)
	if err != nil {
		return nil, err
	}
	features, err := CompileFeatures(fs)
	if err != nil {
		// Feature successor missing is fatal verbatim [GAP T14] C9.
		return nil, err
	}
	report.Report(FamilyFeatures, 100)
	movement, err := CompileMovement(fs)
	if err != nil {
		// MOVEINFO missing is fatal [02 §1] [SPEC_CONFLICTS SC2]; Validate also checks.
		return nil, fmt.Errorf("content: catalog movement: %w", err)
	}
	report.Report(FamilyMovement, 100)
	// Link movement footprints before any unit definition is exposed to
	// placement, build menus, or model consumers [07 §9] "The site".
	applyMovementFootprintRecords(records, movement)
	sides, err := CompileSides(fs)
	if err != nil {
		// SIDEDATA missing is fatal [02 §6] C8; missing font fatal [GAP T14].
		return nil, fmt.Errorf("content: catalog sides: %w", err)
	}
	report.Report(FamilySides, 100)
	soundData, err := CompileSounds(fs)
	if err != nil {
		// Sounds: gamedata/sound.tdf and gamedata/allsound.tdf [PLAN 02]; missing
		// sound catalog is treated as error for whole-install; fixtures that lack
		// it will see Compile error, but retail has it.
		return nil, err
	}
	var sounds map[string]*SoundCategory
	if soundData != nil {
		sounds = soundData.Categories
	}
	if sounds == nil {
		sounds = make(map[string]*SoundCategory)
	}
	var aliases map[string]*SoundAlias
	var aliasOrder []*SoundAlias
	if soundData != nil {
		aliases = soundData.Aliases
		aliasOrder = soundData.AliasOrder
	}
	report.Report(FamilySounds, 100)
	maps, mapWarnings, err := compileMapsWithDiagnostics(fs, report)
	if err != nil {
		// Maps header discovery [02 "Map files"]; retail has 275 each; allow empty
		// on a minimal fixture but whole-install expects them.
		// Keep error for strict whole-install compile; fixtures use individual compilers.
		return nil, err
	}
	report.Report(FamilyMaps, 100)
	aiProfiles, err := CompileAIProfiles(fs)
	if err != nil {
		// ai/*.txt 10 incl default.txt [PLAN 02]; missing default is fatal there.
		return nil, err
	}
	// Battle tables [02 §6] C15 [PLAN_02] WU-02-8: phases 5 and 9 only consume immutable values, never re-parse (I8).
	losTables, err := CompileLOSTables(fs)
	if err != nil {
		return nil, err
	}
	meteorDefaults, err := CompileMeteor(fs)
	if err != nil {
		return nil, err
	}
	// Authored sight shapes [03 §3.2]: the sprite-mask raster indexes these,
	// it does not synthesize a circle.
	sightShapes, err := CompileSightShapes(fs)
	if err != nil {
		return nil, err
	}
	report.Report(FamilyAIProfiles, 100)

	// Stage 2: link cross-references so enumeration order cannot leak into identity [02 §5] C1.
	// weapon1..3 on a unit resolve only after all weapons compile.
	linkUnitWeaponRecords(records, weapons)
	// Feature successors already linked inside CompileFeatures via LinkFeatureSuccessors [GAP T14] C9.
	// Build menus [02 "Build-menu catalog keys"]: the pages live in
	// gamedata/sidedata.tdf next to the sides. They must exist before the
	// downloadable enforcement, which walks their button names after all unit
	// definitions are compiled [02 "Unit record"].
	report.Report(FamilyBattleTables, 100)
	buildMenus, err := CompileBuildMenus(fs)
	if err != nil {
		return nil, err
	}
	warnings := catalogUnitWarnings(unitResult.incompatibilityWarning, units, nil)
	warnings = append(warnings, unitResult.warnings...)
	warnings = append(warnings, mapWarnings...)
	warnings = append(warnings, enforceDownloadableRecords(records, MenuButtonNames(buildMenus))...)
	// Model sorting C13: sort model catalog case-insensitively before caching per-unit-type pointer [03 §2.4].
	report.Report(FamilyBuildMenus, 100)
	sortedModels, modelIndex := buildModelRecordCatalog(records)
	if err := validateRequiredRecordModels(fs, records, weapons, features); err != nil {
		return nil, err
	}
	// The page-count byte is a per-record probe of the authored page windows,
	// step 5 of the compiler's own order [02 R-CAT-01 §5].
	fillUnitRecordBuildPages(fs, records)
	downloadPlacements, err := CompileDownloadMenus(fs, units)
	if err != nil {
		return nil, err
	}
	warnings = append(warnings, applyDownloadRecordMenus(records, units, buildMenus, downloadPlacements)...)
	warnings = append(warnings, fillUnitRecordScripts(fs, records)...)
	report.Report(FamilyModels, 100)

	// Manifest: vfs.ManifestHash() for identity [PLAN 02].
	manifest, _ := manifestHashFor(fs)

	c := &Catalog{
		Units:              units,
		unitRecords:        records,
		Categories:         categories,
		Weapons:            weapons,
		Features:           features,
		Movement:           movement,
		Sides:              sides,
		Sounds:             sounds,
		Maps:               maps,
		LOS:                losTables,
		Sight:              sightShapes,
		Meteor:             meteorDefaults,
		AIProfiles:         aiProfiles,
		Aliases:            aliases,
		AliasOrder:         aliasOrder,
		BuildMenus:         buildMenus,
		DownloadPlacements: downloadPlacements,
		Warnings:           warnings,
		Manifest:           manifest,
		sortedModels:       sortedModels,
		modelIndex:         modelIndex,
	}
	// Stable weapon index: one record per slot [02 §5 R-CONTENT-02].
	c.weaponByID, c.weaponDuplicates = buildWeaponIndex(weapons, weaponDuplicates)
	c.weaponRecords = c.uncachedWeaponRecordsByID()
	// C12 Catalog.Hash computed over canonical bytes including defaults,
	// independent of map iteration, identical across two runs (I1) [02 §5] C12.
	c.Hash = catalogHash(c)
	return c, nil
}

// catalogUnitWarnings preserves the catalog's two independent unit diagnostics:
// compatibility collection precedes the build-menu downloadable enforcement
// [02 R-MALF-01 §5][02 "Unit record"].  Keep this at the assembly seam so a
// later warning source cannot overwrite either earlier result.
func catalogUnitWarnings(incompatible bool, units map[string]*UnitDef, buildMenus map[string]*BuildMenuPage) []string {
	var warnings []string
	if incompatible {
		warnings = append(warnings, incompatibleUnitsWarning)
	}
	if len(buildMenus) > 0 {
		warnings = append(warnings, EnforceDownloadable(units, MenuButtonNames(buildMenus))...)
	}
	return warnings
}

// DownloadPlacementsForPage returns a copy of the resolved generated-page
// placements for one builder and visible page, in authored file/section order
// [02 R-CAT-01 §8][07 R-HUD-03 §6]. Retail MENU is visiblePage+1.
func (c *Catalog) DownloadPlacementsForPage(builder string, visiblePage int) []DownloadMenuPlacement {
	if c == nil || visiblePage < -1 {
		return nil
	}
	wantBuilder := CanonicalKey(builder)
	wantMenu := visiblePage + 1
	var out []DownloadMenuPlacement
	for _, placement := range c.DownloadPlacements {
		if placement.BuilderResolved && placement.ProductResolved &&
			CanonicalKey(placement.Builder) == wantBuilder && int(placement.Menu) == wantMenu {
			out = append(out, placement)
		}
	}
	return out
}

// buildWeaponIndex builds the once-compiled ID->def map and duplicate diagnostics.
// A compiled weapons map already holds exactly one record per nonnegative ID
// (same-ID sections replace each other whole; ID-less sections are inert
// [02 §5 R-CONTENT-02]), so this is a direct projection. For a hand-built map
// that still carries colliding IDs, last in sorted key order stands in for
// discovery-order last-wins.
func buildWeaponIndex(weapons map[string]*WeaponDef, duplicates []WeaponDuplicate) (map[int32]*WeaponDef, []WeaponDuplicate) {
	if weapons == nil {
		return nil, duplicates
	}
	byID := make(map[int32]*WeaponDef, len(weapons))
	// Sorted-key overwrite: deterministic, and exact for a compiled map (one
	// entry per slot). ID < 0 never enters the index — the scratch slot of the
	// ID-less sections is unreachable by construction [02 §5 R-CONTENT-02].
	keys := make([]string, 0, len(weapons))
	for k := range weapons {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		wd := weapons[k]
		if wd == nil || wd.ID < 0 {
			continue
		}
		byID[wd.ID] = wd
	}
	if duplicates == nil {
		// Fallback: compute duplicates from weapons map alone if caller didn't provide them
		// (e.g., manually constructed catalog). Group by ID in sorted order; winner is last.
		dupMap := make(map[int32][]string)
		for _, k := range keys {
			wd := weapons[k]
			if wd == nil || wd.ID < 0 {
				continue
			}
			dupMap[wd.ID] = append(dupMap[wd.ID], k)
		}
		for id, ks := range dupMap {
			if len(ks) <= 1 {
				continue
			}
			// ks already in sorted ascending order via keys iteration; winner is last (largest).
			dup := WeaponDuplicate{ID: id, Keys: append([]string(nil), ks...), Winner: ks[len(ks)-1]}
			duplicates = append(duplicates, dup)
		}
		sort.Slice(duplicates, func(i, j int) bool { return duplicates[i].ID < duplicates[j].ID })
	}
	return byID, duplicates
}

// buildModelRecordCatalog collects distinct ObjectName values from units and sorts
// them case-insensitively before caching a per-unit-type pointer [03 §2.4] C13.
// The sort makes piece/type identity independent of provider order.
func buildModelRecordCatalog(records []*UnitDef) ([]string, map[string]int) {
	if len(records) == 0 {
		return nil, nil
	}
	// Deduplicate by canonical key so "arm_3do" and "ARM_3DO" are one entry.
	// The surviving spelling is chosen deterministically: iterate units by
	// sorted canonical key and keep the lexicographically smallest original
	// among fold-equal spellings, so provider order and Go map randomization
	// cannot choose the representative (I1).
	canonToOriginal := make(map[string]string)
	for _, u := range records {
		name := trimTDFSemantic(u.ObjectName)
		ck := CanonicalKey(name)
		if existing, exists := canonToOriginal[ck]; !exists || name < existing {
			canonToOriginal[ck] = name
		}
	}
	if len(canonToOriginal) == 0 {
		return nil, nil
	}
	sorted := make([]string, 0, len(canonToOriginal))
	for _, orig := range canonToOriginal {
		sorted = append(sorted, orig)
	}
	// Sort case-insensitively [03 §2.4] — total order via lower + tie-breaker (I1).
	sort.Slice(sorted, func(i, j int) bool {
		li, lj := CanonicalKey(sorted[i]), CanonicalKey(sorted[j])
		if li != lj {
			return li < lj
		}
		return sorted[i] < sorted[j]
	})
	index := make(map[string]int, len(sorted))
	for i, name := range sorted {
		index[CanonicalKey(name)] = i
	}
	return sorted, index
}

// SortedModels returns the distinct model names sorted case-insensitively [03 §2.4] C13.
// The slice is a copy; mutations do not affect the catalog.
func (c *Catalog) SortedModels() []string {
	if c == nil || len(c.sortedModels) == 0 {
		return nil
	}
	out := make([]string, len(c.sortedModels))
	copy(out, c.sortedModels)
	return out
}

// ModelIndex returns the index of a model name in the sorted catalog [03 §2.4] C13
// and whether it exists. Lookup is case-insensitive via CanonicalKey [02 §5].
func (c *Catalog) ModelIndex(modelName string) (int, bool) {
	if c == nil || c.modelIndex == nil {
		return 0, false
	}
	idx, ok := c.modelIndex[CanonicalKey(modelName)]
	return idx, ok
}

// ModelForUnit returns the model name for a unit and its index in the sorted
// model catalog [03 §2.4] C13. The unit lookup is case-insensitive [02 §5].
func (c *Catalog) ModelForUnit(unitKey string) (string, int, bool) {
	if c == nil {
		return "", 0, false
	}
	u, ok := c.Unit(unitKey)
	if !ok {
		return "", 0, false
	}
	name := trimTDFSemantic(u.ObjectName)
	idx, ok := c.ModelIndex(name)
	if !ok {
		return "", 0, false
	}
	return name, idx, true
}

// UnitDefIndex returns the stable catalog index for a unit definition
// keyed by CanonicalKey (case-insensitive) [02 §5][05 "Build request and factory queue behavior"].
// Indices are 1-based (0 is null sentinel). Equal names select the first
// retained record in the retail sort order [02 R-CAT-01 §5].
// This replaces the invented FNV-1a product hashing (N04) with a collision-free
// stable index [P0-I05].
func (c *Catalog) UnitDefIndex(key string) (uint32, bool) {
	u, ok := c.Unit(key)
	if !ok {
		return 0, false
	}
	if c.unitRecords != nil {
		return u.UnitDefID, true
	}
	for i, candidate := range c.unitRecordView() {
		if candidate == u {
			return uint32(i + 1), true
		}
	}
	return 0, false
}

// Finalized reports whether this catalog was produced by Compile: the catalog
// hash is stamped once there, and only there [02 §5] C12. Hand-built fixture
// catalogs carry no hash. Consumers use it to tell a finalized catalog —
// whose definition identities are authoritative — from a fixture catalog
// that carries no finalized identity.
func (c *Catalog) Finalized() bool {
	return c != nil && c.Hash != "" && (len(c.unitRecords) > 0 || len(c.Units) > 0)
}

// UnitIndexOf returns a unit definition's stable catalog index and whether
// that definition is one of this catalog's own retained records
// [CNT-05][02 §5][R-P0-03]. It is the definition-identity accessor mirroring
// ModelIndex above: the index is the 1-based UnitDefID stamped once at
// category-link time (0 remains the null sentinel), so identity is derived
// from the immutable catalog position, never from use order. The pointer
// comparison rejects a same-key impostor that did not come from this catalog.
// The key falls back to the canonical UnitName for definitions whose header
// key was never stamped (hand-built fixture catalogs).
func (c *Catalog) UnitIndexOf(def *UnitDef) (uint32, bool) {
	if c == nil || def == nil {
		return 0, false
	}
	if c.unitRecords != nil {
		id := def.UnitDefID
		return id, id > 0 && uint64(id) <= uint64(len(c.unitRecords)) && c.unitRecords[id-1] == def
	}
	for _, own := range c.unitRecordView() {
		if own == def {
			return def.UnitDefID, true
		}
	}
	return 0, false
}

// UnitDefByIndex returns a retained record by its 1-based catalog position;
// zero is the null sentinel [02 R-CAT-01 §5].
func (c *Catalog) UnitDefByIndex(idx uint32) (*UnitDef, bool) {
	if c == nil || idx == 0 {
		return nil, false
	}
	records := c.unitRecordView()
	if uint64(idx) > uint64(len(records)) {
		return nil, false
	}
	u := records[idx-1]
	return u, u != nil
}

// Category returns a value copy of a named registry membership set.
func (c *Catalog) Category(name string) (CategoryMask, bool) {
	if c == nil || c.Categories == nil {
		return CategoryMask{}, false
	}
	return c.Categories.Lookup(name)
}

// ResolveCategoryMask distinguishes direct unit-name targets from category
// tokens. A direct unit name wins and produces one ID bit; otherwise the whole
// category membership set is returned [06 §3.1] [R-P0-03].
func (c *Catalog) ResolveCategoryMask(name string) (CategoryMask, bool) {
	if c == nil {
		return CategoryMask{}, false
	}
	if u, ok := c.Unit(name); ok && u != nil {
		var m CategoryMask
		_ = m.set(u.UnitDefID)
		return m, true
	}
	return c.Category(name)
}

// RestrictToCreatable applies a campaign unit restriction to the compiled
// unit table: the definitions named survive, every other definition is removed,
// and the survivors are re-sorted and renumbered [08 R-ENTRY-01 §2 step 4]
// [05 R-SHARE-01 §8].
//
// Retail expresses the restriction as a per-definition *creatable* bit — the
// same runtime bit the unit allocator's second test reads. Its loader clears
// the bit on every record from index 1 upward (the `None` sentinel at index 0
// keeps it), sets it again for the first record whose `unitname` matches each
// listed name, case-insensitively, and then the battle-entry catalog compile
// that runs afterwards compacts every bit-clear record out of the table and
// re-sorts and renumbers what is left. A compiled table therefore carries the
// bit on every record it holds, so the bit is *not* the mechanism: the
// mechanism is the removal, and the allocator's bit test stays the cheap
// invariant it is in retail. This method is that compaction. An excluded
// definition has no unit index, no build-menu button and cannot be spawned by
// name [05 R-SHARE-01 §8 "Consequence"].
//
// A listed name keeps only the first matching record. Duplicate names do
// not keep later definitions [05 R-SHARE-01 §8].
//
// The restriction lasts one battle: retail rebuilds the table from the FBI
// files before every battle, so callers apply this to a clone and never to a
// shared compiled catalog.
func (c *Catalog) RestrictToCreatable(names []string) error {
	if c == nil || c.Units == nil {
		return nil
	}
	keep := make(map[*UnitDef]bool, len(names))
	for _, name := range names {
		if u, ok := c.Unit(name); ok {
			keep[u] = true
		}
	}
	records := make([]*UnitDef, 0, len(keep))
	for _, u := range c.unitRecordView() {
		if keep[u] {
			records = append(records, u)
		}
	}
	sortUnitRecords(records)
	c.unitRecords = records
	c.Units = firstUnitNames(records)
	reg, err := compileCategoryRecords(records, c.Units)
	if err != nil {
		return err
	}
	c.Categories = reg
	// The catalog digest covers definition identity, and identity moved.
	c.Hash = catalogHash(c)
	return nil
}

// SortedUnitKeys returns unique name-index keys sorted ascending (I1).
// Use UnitRecords when every retained definition is required [02 R-CAT-01 §5].
// The slice is a copy; mutations do not affect the catalog.
func (c *Catalog) SortedUnitKeys() []string {
	if c == nil || c.Units == nil {
		return nil
	}
	keys := make([]string, 0, len(c.Units))
	for k := range c.Units {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, len(keys))
	copy(out, keys)
	return out
}

// Validate treats a missing gamedata/ directory, MOVEINFO.TDF or SIDEDATA.TDF as fatal,
// and translate.tdf as optional — NOT GAMEDATA.TDF which doesn't exist [SPEC_CONFLICTS SC2].
//
// It inspects the compiled catalog, not the filesystem, so it can be called
// after Compile without retaining the FSOps. A missing gamedata/ directory
// manifests as both movement and sides empty; we report it as gamedata/ fatal.
// translate.tdf missing yields identity (byte-exact) and is not fatal [02 §3] C7.
func (c *Catalog) Validate() error {
	if c == nil {
		return fmt.Errorf("content: nil catalog")
	}
	// Missing gamedata/ directory is fatal [SPEC_CONFLICTS SC2].
	if len(c.Movement) == 0 && len(c.Sides) == 0 {
		// Both hard requirements absent ⇒ likely missing gamedata/ entirely.
		return fmt.Errorf("content: missing gamedata/ directory is fatal [02 §1] [SPEC_CONFLICTS SC2]")
	}
	// MOVEINFO.TDF missing is fatal [02 §1]; SIDEDATA.TDF missing is fatal [02 §6].
	if len(c.Movement) == 0 {
		return fmt.Errorf("content: missing gamedata/moveinfo.tdf is fatal [02 §1] [SPEC_CONFLICTS SC2]")
	}
	if len(c.Sides) == 0 {
		return fmt.Errorf("content: missing gamedata/sidedata.tdf is fatal [02 §6] [SPEC_CONFLICTS SC2]")
	}
	// translate.tdf is optional — missing yields identity map, byte-exact [02 §3] C7 — so no check.
	// GAMEDATA.TDF does not exist in a real install [SPEC_CONFLICTS SC2] — must not be required.
	return nil
}

// Clone returns a deep copy for per-match isolation [PLAN 02].
// Maps and slices are copied; per-definition maps are deep-copied; weapon
// pointers and feature successor pointers are rewired to the cloned maps so
// the clone never shares mutable state with the original. Hash and Manifest are
// copied verbatim. Model catalog is copied.
func (c *Catalog) Clone() *Catalog {
	if c == nil {
		return nil
	}
	out := &Catalog{
		Manifest: c.Manifest,
		Hash:     c.Hash,
	}
	if c.Categories != nil {
		out.Categories = cloneCategoryRegistry(c.Categories)
	}
	// Weapons deep copy
	if c.Weapons != nil {
		out.Weapons = make(map[string]*WeaponDef, len(c.Weapons))
		for k, v := range c.Weapons {
			out.Weapons[k] = cloneWeapon(v)
		}
	}
	// Clone every retained record, then rebuild the first-match name index;
	// later equal-name records must retain their own masks and weapon links.
	if c.unitRecords != nil {
		out.unitRecords = make([]*UnitDef, len(c.unitRecords))
		for i, u := range c.unitRecords {
			out.unitRecords[i] = cloneUnit(u)
		}
		out.Units = firstUnitNames(out.unitRecords)
	} else if c.Units != nil {
		out.Units = make(map[string]*UnitDef, len(c.Units))
		for k, u := range c.Units {
			out.Units[k] = cloneUnit(u)
		}
	}
	for _, u := range out.unitRecordView() {
		if u == nil {
			continue
		}
		out.rewireWeaponLink(u.Weapon1, &u.Weapon1Def)
		out.rewireWeaponLink(u.Weapon2, &u.Weapon2Def)
		out.rewireWeaponLink(u.Weapon3, &u.Weapon3Def)
		out.rewireWeaponLink(u.ExplodeAs, &u.ExplodeAsDef)
		out.rewireWeaponLink(u.SelfDestructAs, &u.SelfDestructAsDef)
	}
	// Features deep copy without successors then rewire
	if c.Features != nil {
		out.Features = make(map[string]*FeatureDef, len(c.Features))
		for k, v := range c.Features {
			out.Features[k] = cloneFeature(v)
		}
		// Rewire successors deterministically (sorted keys I1)
		keys := make([]string, 0, len(out.Features))
		for k := range out.Features {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			f := out.Features[k]
			if trimTDFSemantic(f.FeatureDead) != "" {
				if target, ok := out.Features[CanonicalKey(f.FeatureDead)]; ok {
					f.FeatureDeadDef = target
				}
			}
			if trimTDFSemantic(f.FeatureReclamate) != "" {
				if target, ok := out.Features[CanonicalKey(f.FeatureReclamate)]; ok {
					f.FeatureReclamateDef = target
				}
			}
			if trimTDFSemantic(f.FeatureBurnt) != "" {
				if target, ok := out.Features[CanonicalKey(f.FeatureBurnt)]; ok {
					f.FeatureBurntDef = target
				}
			}
		}
	}
	// Movement deep copy
	if c.Movement != nil {
		out.Movement = make(map[string]*MovementClass, len(c.Movement))
		for k, v := range c.Movement {
			out.Movement[k] = cloneMovement(v)
		}
	}
	// Sides deep copy (slice ordered by ordinal [02 §6] C8)
	if c.Sides != nil {
		out.Sides = make([]*SideDef, len(c.Sides))
		for i, s := range c.Sides {
			out.Sides[i] = cloneSide(s)
		}
	}
	// Sounds deep copy
	if c.Sounds != nil {
		out.Sounds = make(map[string]*SoundCategory, len(c.Sounds))
		for k, v := range c.Sounds {
			out.Sounds[k] = cloneSoundCategory(v)
		}
	}
	// Maps deep copy
	if c.Maps != nil {
		out.Maps = make(map[string]*MapHeader, len(c.Maps))
		for k, v := range c.Maps {
			out.Maps[k] = cloneMapHeader(v)
		}
	}
	// AIProfiles deep copy
	if c.AIProfiles != nil {
		out.AIProfiles = make(map[string]*AIProfile, len(c.AIProfiles))
		for k, v := range c.AIProfiles {
			out.AIProfiles[k] = cloneAIProfile(v)
		}
	}
	// Aliases deep copy [02 "Sound aliases"]
	if c.Aliases != nil {
		out.Aliases = make(map[string]*SoundAlias, len(c.Aliases))
		for k, v := range c.Aliases {
			if v == nil {
				continue
			}
			cp := *v
			out.Aliases[k] = &cp
		}
	}
	if c.AliasOrder != nil {
		out.AliasOrder = make([]*SoundAlias, 0, len(c.AliasOrder))
		for _, v := range c.AliasOrder {
			if v == nil {
				continue
			}
			if cp, ok := out.Aliases[v.CanonicalKey]; ok {
				out.AliasOrder = append(out.AliasOrder, cp)
			}
		}
	}
	// BuildMenus deep copy [02 "Build-menu catalog keys"]
	if c.BuildMenus != nil {
		out.BuildMenus = make(map[string]*BuildMenuPage, len(c.BuildMenus))
		for k, v := range c.BuildMenus {
			if v == nil {
				continue
			}
			cp := *v
			if v.Buttons != nil {
				cp.Buttons = append([]string(nil), v.Buttons...)
			}
			out.BuildMenus[k] = &cp
		}
	}
	if c.DownloadPlacements != nil {
		out.DownloadPlacements = append([]DownloadMenuPlacement(nil), c.DownloadPlacements...)
	}
	// Warnings are immutable diagnostics; a slice copy suffices.
	if c.Warnings != nil {
		out.Warnings = append([]string(nil), c.Warnings...)
	}
	// Battle tables deep copy [02 §6] C15
	if c.LOS != nil {
		out.LOS = cloneLOSTables(c.LOS)
	}
	if c.Meteor != nil {
		out.Meteor = cloneMeteor(c.Meteor)
	}
	// Sight shapes deep copy [03 §3.2]. This was missing: a clone came back
	// with no sight shapes at all, and the session installs them from the
	// catalog it runs on, so any consumer of a cloned catalog lost the
	// quantized sprite masks and fell back to whatever a nil table means.
	if c.Sight != nil {
		cp := *c.Sight
		if c.Sight.Shapes != nil {
			cp.Shapes = make([]SightShape, len(c.Sight.Shapes))
			for i, sh := range c.Sight.Shapes {
				cp.Shapes[i] = sh
				if sh.Opaque != nil {
					cp.Shapes[i].Opaque = append([]bool(nil), sh.Opaque...)
				}
			}
		}
		out.Sight = &cp
	}
	// Model catalog copy
	if c.sortedModels != nil {
		out.sortedModels = append([]string(nil), c.sortedModels...)
	}
	if c.modelIndex != nil {
		out.modelIndex = make(map[string]int, len(c.modelIndex))
		for k, v := range c.modelIndex {
			out.modelIndex[k] = v
		}
	}
	// Stable weapon index: rebuild for cloned defs so pointers refer to cloned entries (I1) [02 "Weapon record"]
	if out.Weapons != nil {
		out.weaponByID, out.weaponDuplicates = buildWeaponIndex(out.Weapons, nil)
		out.weaponRecords = out.uncachedWeaponRecordsByID()
	} else if c.weaponByID != nil {
		// No weapons but index exists (empty) — copy duplicates diagnostics
		out.weaponByID = nil
		out.weaponRecords = nil
		if len(c.weaponDuplicates) > 0 {
			out.weaponDuplicates = append([]WeaponDuplicate(nil), c.weaponDuplicates...)
			for i := range out.weaponDuplicates {
				out.weaponDuplicates[i].Keys = append([]string(nil), c.weaponDuplicates[i].Keys...)
			}
		}
	}
	return out
}

// Unit returns the unit definition for a key case-insensitively [02 §5].
func (c *Catalog) Unit(key string) (*UnitDef, bool) {
	if c == nil || c.Units == nil {
		return nil, false
	}
	u, ok := c.Units[CanonicalKey(key)]
	return u, ok
}

// Weapon returns the weapon definition for a key case-insensitively [02 §5].
// The catalog map holds one record per surviving catalog name, so a hit is the
// record the runtime name scan returns; see WeaponByName for the scan contract.
func (c *Catalog) Weapon(key string) (*WeaponDef, bool) {
	if c == nil || c.Weapons == nil {
		return nil, false
	}
	w, ok := c.Weapons[CanonicalKey(key)]
	return w, ok
}

// WeaponRecordsByID returns the weapon records in slot order — the order of
// retail's fixed record table, slot 0 upward [02 §5 R-CONTENT-02]. A record's
// slot is the ID its section selected; ID-less sections filled the unreachable
// scratch slot before record 0 and never entered the catalog. The slice is a
// copy; mutations do not affect the catalog.
func (c *Catalog) WeaponRecordsByID() []*WeaponDef {
	if c != nil && c.weaponRecords != nil {
		return append([]*WeaponDef(nil), c.weaponRecords...)
	}
	return c.uncachedWeaponRecordsByID()
}

func (c *Catalog) uncachedWeaponRecordsByID() []*WeaponDef {
	if c == nil || len(c.Weapons) == 0 {
		return nil
	}
	out := make([]*WeaponDef, 0, len(c.Weapons))
	for _, w := range c.Weapons {
		if w == nil || w.ID < 0 {
			continue
		}
		out = append(out, w)
	}
	// Ascending slot order, canonical key as the total-order tiebreaker (I1).
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].CanonicalKey < out[j].CanonicalKey
	})
	return out
}

// WeaponByName resolves a weapon catalog name the way retail does at runtime:
// a linear scan of the record table from slot 0 upward, comparing
// case-insensitively against each record's catalog name; the FIRST matching
// slot wins [02 §5 R-CONTENT-02]. The catalog stores one record per surviving
// name, so the scan reduces to visiting records in slot order and comparing
// canonical keys.
func (c *Catalog) WeaponByName(name string) (*WeaponDef, bool) {
	ck := CanonicalKey(name)
	if c == nil || ck == "" {
		return nil, false
	}
	records := c.weaponRecords
	if records == nil {
		// Hand-built fixtures can omit compilation; never mutate the catalog
		// lazily from a simulation lookup [I6].
		records = c.uncachedWeaponRecordsByID()
	}
	for _, w := range records {
		if w.CanonicalKey == ck {
			return w, true
		}
	}
	return nil, false
}

// WeaponLink resolves a unit weapon link (weapon1..3, explodeas,
// selfdestructas) with retail's miss policy: a name that matches no record
// fills the link slot with a reference to record 0 — the inactive sentinel,
// stock [noweapon] — not an error [02 §5 R-CONTENT-02], [02 "Cross-reference
// failure policy"]. Record 0 is inactive even when the name resolves to it
// (consumers recognize the sentinel by its zero slot number), so active is
// false whenever the link points there. When the catalog holds no record 0, a
// miss returns a nil def with active false — the explicit inactive marker.
// A restored battle uses the current definition-side byte for active, while
// preserving that initial name-resolution identity [08 R-SAVE-WEAPON-01].
//
// LinkUnitWeapons (compile_unit.go) stores this resolution on the unit's
// link defs: a missed link holds the record-0 def when the family carries
// one, else nil [02 §5 R-CONTENT-02].
func (c *Catalog) WeaponLink(name string) (def *WeaponDef, active bool) {
	if w, ok := c.WeaponByName(name); ok {
		return w, !IsWeaponInactive(w)
	}
	// Miss: the link references record 0 when the table has one [02 §5 R-CONTENT-02].
	if w, ok := c.WeaponByID(0); ok {
		return w, !IsWeaponInactive(w)
	}
	return nil, false
}

// IsWeaponInactive reads the resolved definition-side active byte. Initially
// only nil links and record 0 are inactive [02 §5 R-CONTENT-02]. Battle restore
// can replace this byte without changing catalog identity [08 R-SAVE-WEAPON-01].
func IsWeaponInactive(def *WeaponDef) bool {
	return def.ActiveByte() == 0
}

// WeaponByID selects the record occupying the given slot (I1) [02 "Weapon
// record"] C2, [02 §5 R-CONTENT-02]: ID is read first with default -1 to
// select the record, so the slot number is the ID. A compiled map holds one
// record per slot; for a hand-built map with colliding IDs, last in sorted
// key order stands in for discovery-order last-wins. The once-compiled index
// is preferred when available.
func (c *Catalog) WeaponByID(id int32) (*WeaponDef, bool) {
	if c == nil || c.Weapons == nil {
		return nil, false
	}
	if c.weaponByID != nil {
		if wd, ok := c.weaponByID[id]; ok {
			return wd, true
		}
		return nil, false
	}
	keys := make([]string, 0, len(c.Weapons))
	for k := range c.Weapons {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var found *WeaponDef
	var ok bool
	for _, k := range keys {
		if c.Weapons[k].ID == id {
			found = c.Weapons[k]
			ok = true
		}
	}
	return found, ok
}

// WeaponDuplicates returns duplicate ID diagnostics (sorted by ID, winner last).
// The slice is a copy; mutations do not affect the catalog.
func (c *Catalog) WeaponDuplicates() []WeaponDuplicate {
	if c == nil || len(c.weaponDuplicates) == 0 {
		return nil
	}
	out := make([]WeaponDuplicate, len(c.weaponDuplicates))
	for i, d := range c.weaponDuplicates {
		out[i].ID = d.ID
		out[i].Winner = d.Winner
		out[i].Keys = append([]string(nil), d.Keys...)
	}
	return out
}

// WeaponIndex returns a copy of the once-compiled ID->def map (I1) [02 "Weapon record"].
// It is nil if the catalog has no compiled index (e.g., manually constructed fixture without Compile).
func (c *Catalog) WeaponIndex() map[int32]*WeaponDef {
	if c == nil || c.weaponByID == nil {
		return nil
	}
	out := make(map[int32]*WeaponDef, len(c.weaponByID))
	for k, v := range c.weaponByID {
		out[k] = v
	}
	return out
}

// RebuildWeaponIndex rebuilds the once-compiled index from the current Weapons map [02 "Weapon record"].
// Useful for fixtures that manually assign Weapons without going through Compile; last wins.
func (c *Catalog) RebuildWeaponIndex() {
	if c == nil {
		return
	}
	c.weaponByID, c.weaponDuplicates = buildWeaponIndex(c.Weapons, nil)
	c.weaponRecords = c.uncachedWeaponRecordsByID()
}

// rewireWeaponLink rewires one cloned unit weapon link onto the cloned weapon
// table, re-deriving WeaponLink's resolution and miss policy [02 §5
// R-CONTENT-02]: a name hit points at the cloned record; a miss — including an
// empty name, which the loader's record scan can never match against a
// record's blanked-out name [06 R-DMG-01 §5] — fills the slot with the
// clone's own record-0 inactive sentinel (ID 0) when the family carries one,
// else leaves the explicit nil inactive marker. So a cloned catalog preserves
// sentinel links (including for unarmed definitions) instead of dropping them
// to nil.
func (c *Catalog) rewireWeaponLink(name string, slot **WeaponDef) {
	if w, ok := c.Weapons[CanonicalKey(name)]; ok {
		*slot = w
		return
	}
	if w0, ok := c.WeaponByID(0); ok {
		*slot = w0 // record-0 inactive sentinel [02 §5 R-CONTENT-02]
	}
}

// cloneUnit deep copies a UnitDef without weapon link pointers (rewired in Clone).
func cloneUnit(u *UnitDef) *UnitDef {
	if u == nil {
		return nil
	}
	out := *u
	// DefinitionHeader is value copy (strings + Provenance struct)
	if u.Unknown != nil {
		out.Unknown = make(map[string]string, len(u.Unknown))
		for k, v := range u.Unknown {
			out.Unknown[k] = v
		}
	}
	// Clear weapon resolved pointers — rewired to cloned weapons.
	out.Weapon1Def = nil
	out.Weapon2Def = nil
	out.Weapon3Def = nil
	out.ExplodeAsDef = nil
	out.SelfDestructAsDef = nil
	return &out
}

func cloneCategoryRegistry(r *CategoryRegistry) *CategoryRegistry {
	if r == nil {
		return nil
	}
	out := &CategoryRegistry{
		entries: make([]CategoryEntry, len(r.entries)),
		byName:  make(map[string]int, len(r.byName)),
	}
	copy(out.entries, r.entries)
	for k, v := range r.byName {
		out.byName[k] = v
	}
	return out
}

// cloneWeapon deep copies a WeaponDef.
func cloneWeapon(w *WeaponDef) *WeaponDef {
	if w == nil {
		return nil
	}
	out := *w
	out.damageOrder = append([]string(nil), w.damageOrder...)
	if w.Damage != nil {
		out.Damage = make(map[string]int32, len(w.Damage))
		for k, v := range w.Damage {
			out.Damage[k] = v
		}
	}
	if w.Unknown != nil {
		out.Unknown = make(map[string]string, len(w.Unknown))
		for k, v := range w.Unknown {
			out.Unknown[k] = v
		}
	}
	return &out
}

// cloneFeature deep copies a FeatureDef without successor pointers (rewired in Clone).
func cloneFeature(f *FeatureDef) *FeatureDef {
	if f == nil {
		return nil
	}
	out := *f
	if f.Unknown != nil {
		out.Unknown = make(map[string]string, len(f.Unknown))
		for k, v := range f.Unknown {
			out.Unknown[k] = v
		}
	}
	out.FeatureDeadDef = nil
	out.FeatureReclamateDef = nil
	out.FeatureBurntDef = nil
	return &out
}

// cloneMovement deep copies a MovementClass.
func cloneMovement(m *MovementClass) *MovementClass {
	if m == nil {
		return nil
	}
	out := *m
	return &out
}

// cloneSide deep copies a SideDef.
func cloneSide(s *SideDef) *SideDef {
	if s == nil {
		return nil
	}
	out := *s
	if s.Anchors != nil {
		out.Anchors = make(map[string]Rect, len(s.Anchors))
		for k, v := range s.Anchors {
			out.Anchors[k] = v
		}
	}
	return &out
}

// cloneSoundCategory deep copies a SoundCategory [02 "Sound category record"] [03 §8.3].
func cloneSoundCategory(sc *SoundCategory) *SoundCategory {
	if sc == nil {
		return nil
	}
	out := *sc
	for i := range out.Slots {
		slot := &out.Slots[i]
		orig := sc.Slots[i]
		if orig.Variants != nil {
			slot.Variants = append([]string(nil), orig.Variants...)
		}
		if orig.Captions != nil {
			slot.Captions = append([]string(nil), orig.Captions...)
		}
	}
	return &out
}

// cloneMapHeader deep copies a MapHeader headers-only [02 "Map files"].
func cloneMapHeader(mh *MapHeader) *MapHeader {
	if mh == nil {
		return nil
	}
	out := *mh
	if mh.Schemas != nil {
		out.Schemas = append([]MapSchema(nil), mh.Schemas...)
	}
	// RawOTA is immutable after Compile [PLAN 02]; share pointer.
	// Provenance and DefinitionHeader are value copies.
	return &out
}

// cloneAIProfile deep copies an AIProfile [08 "Computer-controlled players"].
func cloneAIProfile(p *AIProfile) *AIProfile {
	if p == nil {
		return nil
	}
	out := *p
	if p.Plans != nil {
		out.Plans = make(map[string]*AIPlan, len(p.Plans))
		for k, v := range p.Plans {
			if v == nil {
				continue
			}
			cp := *v
			if v.Weights != nil {
				cp.Weights = make(map[string]int32, len(v.Weights))
				for kk, vv := range v.Weights {
					cp.Weights[kk] = vv
				}
			}
			if v.Limits != nil {
				cp.Limits = make(map[string]int32, len(v.Limits))
				for kk, vv := range v.Limits {
					cp.Limits[kk] = vv
				}
			}
			out.Plans[k] = &cp
		}
	}
	return &out
}

// cloneLOSTables deep copies LOSTables [02 §6] C15 [PLAN_02] WU-02-8.
func cloneLOSTables(lt *LOSTables) *LOSTables {
	if lt == nil {
		return nil
	}
	out := *lt
	if lt.Tables != nil {
		out.Tables = make([]LOSTable, len(lt.Tables))
		for i, t := range lt.Tables {
			out.Tables[i].TableNum = t.TableNum
			out.Tables[i].NumLines = t.NumLines
			if t.Lines != nil {
				out.Tables[i].Lines = make([][]int32, len(t.Lines))
				for j, line := range t.Lines {
					if line != nil {
						out.Tables[i].Lines[j] = append([]int32(nil), line...)
					}
				}
			}
		}
	}
	return &out
}

// cloneMeteor deep copies MeteorDefaults [02 §6] C15 [PLAN_02] WU-02-8.
func cloneMeteor(md *MeteorDefaults) *MeteorDefaults {
	if md == nil {
		return nil
	}
	out := *md
	return &out
}

// manifestHashFor returns vfs.ManifestHash when available [vfs.ManifestHash].
// Compile takes vfs.FSOps which does not include ManifestHash (only Open,
// ReadFileLimit, ReadDir, Stat, CacheStamp), but the concrete *vfs.FS does.
// Use a type assertion so Catalog.Manifest is populated when a real FS is used
// while keeping the FSOps abstraction for tests.
func manifestHashFor(fs vfs.FSOps) (string, error) {
	if fs == nil {
		return "", fmt.Errorf("content: nil VFS")
	}
	if f, ok := fs.(*vfs.FS); ok {
		return f.ManifestHash()
	}
	if mh, ok := fs.(interface{ ManifestHash() (string, error) }); ok {
		return mh.ManifestHash()
	}
	return "", nil
}

// validateRequiredModels reads each distinct named model once in logical-path
// order. A named unit objectname, weapon model, or feature object cannot
// degrade to an empty geometry record: retail sends its model-load failure to
// the fatal channel [02 "Cross-reference failure policy"][02 R-CAT-01 §5].
// Empty model fields remain their record family's distinct authored policy.
// Feature animation sequences are deliberately absent here; their GAF lookup
// has a separate silent-null recovery [02 R-MALF-01 §5].
func validateRequiredModels(fs vfs.FSOps, units map[string]*UnitDef, weapons map[string]*WeaponDef, features map[string]*FeatureDef) error {
	return validateRequiredRecordModels(fs, unitMapRecords(units), weapons, features)
}

func validateRequiredRecordModels(fs vfs.FSOps, records []*UnitDef, weapons map[string]*WeaponDef, features map[string]*FeatureDef) error {
	models := requiredRecordModelPaths(records, weapons, features)
	if len(models) == 0 {
		return nil
	}

	loaded := make(map[string]*formats.ThreeDO, len(models))
	for _, logical := range models {
		info, err := fs.Stat(logical)
		if err != nil || info.IsDir {
			if err == nil {
				err = fmt.Errorf("path is a directory")
			}
			return requiredContentError(fs, logical, "valid 3DO model", err)
		}
		data, err := fs.ReadFileLimit(logical, 1<<22)
		if err != nil {
			return requiredContentError(fs, logical, "valid 3DO model", err)
		}
		threeDO, err := formats.LoadThreeDO(data)
		if err != nil {
			return requiredContentError(fs, logical, "valid 3DO model", err)
		}
		if err := model.ValidateSource(threeDO); err != nil {
			return requiredContentError(fs, logical, "valid 3DO model", err)
		}
		loaded[logical] = threeDO
	}

	// Do not publish a partly updated set of unit heights: all required model
	// reads, parses and compilation checks complete before one definition changes [02 §5].
	for _, u := range records {
		if u == nil {
			continue
		}
		logical := requiredUnitModelPath(u.ObjectName)
		raw := loaded[logical].ModelTop() // 16.16, floored at zero [fmt 3do]
		u.ModelTopFixed = raw
		// Preserve retail's whole-word masking rather than a signed conversion.
		u.ModelTop = (raw >> 16) & 0xFF
	}
	return nil
}

// requiredRecordModelPaths collects the union across all model-owning definition
// families. The sort chooses one deterministic first failure and the map
// makes each parsed geometry result shared by every reference to its path.
func requiredRecordModelPaths(records []*UnitDef, weapons map[string]*WeaponDef, features map[string]*FeatureDef) []string {
	paths := make(map[string]struct{})
	for _, u := range records {
		if u == nil {
			continue
		}
		paths[requiredUnitModelPath(u.ObjectName)] = struct{}{}
	}
	for _, w := range weapons {
		if w == nil {
			continue
		}
		if logical := requiredModelPath(w.Model); logical != "" {
			paths[logical] = struct{}{}
		}
	}
	for _, f := range features {
		if f == nil {
			continue
		}
		if logical := requiredModelPath(f.Object); logical != "" {
			paths[logical] = struct{}{}
		}
	}
	if len(paths) == 0 {
		return nil
	}
	logical := make([]string, 0, len(paths))
	for path := range paths {
		logical = append(logical, path)
	}
	sort.Strings(logical)
	return logical
}

// Unit models are required even with an empty authored basename [02 R-CAT-01 §5].
// Weapon and feature model fields keep their separate optional-presence gates.
func requiredUnitModelPath(name string) string {
	return vfs.ResourcePath("objects3d", CanonicalKey(name), "3do")
}

func requiredModelPath(name string) string {
	name = CanonicalKey(name)
	if name == "" {
		return ""
	}
	return "objects3d/" + name + ".3do"
}

// fillUnitScripts retains unavailable programs as catalog warnings. Preflight
// and creation refuse a required missing program; unrelated definitions remain
// usable [04 R-COB-04 §8], DESIGN_CONTENT_VFS §3.4 C9.
func fillUnitScripts(fs vfs.FSOps, units map[string]*UnitDef) []string {
	return fillUnitRecordScripts(fs, unitMapRecords(units))
}

func fillUnitRecordScripts(fs vfs.FSOps, records []*UnitDef) []string {
	if fs == nil || len(records) == 0 {
		return nil
	}
	var warnings []string
	for _, u := range records {
		if u == nil {
			continue
		}
		logical := vfs.ResourcePath("scripts", CanonicalKey(u.UnitName), "cob")
		u.Script = nil
		u.ScriptProvenance = vfs.Provenance{LogicalPath: logical}
		info, statErr := fs.Stat(logical)
		if statErr != nil || info.IsDir {
			warnings = append(warnings, unitScriptMissingError(fs, logical).Error())
			continue
		}
		u.ScriptProvenance = info.Source
		u.ScriptProvenance.LogicalPath = logical
		// Read the same winning logical path that was just inspected, retaining
		// read failures instead of collapsing them into a missing-file result.
		// Keep the COB loader's existing host byte bound [fmt cob].
		data, err := fs.ReadFileLimit(logical, 4<<20)
		var prog *cob.Program
		if err == nil {
			prog, err = cob.Load(data)
		}
		if err != nil || prog == nil || len(prog.Code) == 0 {
			warning := unitScriptMissingError(fs, logical).Error()
			if err != nil {
				warning += ": " + err.Error()
			}
			warnings = append(warnings, warning)
			continue
		}
		u.Script = prog
	}
	return warnings
}

// fillUnitRecordBuildPages compiles every definition's build-menu page-count byte from
// the authored page windows, which is step 5 of the catalog compiler's per-
// record work [02 R-CAT-01 §5]: with `<n>` the unit name, `guis/<n>0.GUI`
// existing sets the page-zero bit, then `guis/<n>1.GUI`, `guis/<n>2.GUI`, …
// are probed until the first missing one, and the byte becomes the index of
// that first missing page when at least one numbered page existed, else 1 when
// page 0 exists, else 0.
//
// The probe is by existence and non-zero size, and it stops at the first gap:
// `guis/<n>1.GUI` and `guis/<n>3.GUI` with no `<n>2` is a count of 2, not 4.
func fillUnitRecordBuildPages(fs vfs.FSOps, records []*UnitDef) {
	if fs == nil || len(records) == 0 {
		return
	}
	exists := func(name string) bool {
		info, err := fs.Stat("guis/" + name + ".gui")
		return err == nil && info.Size > 0
	}
	for _, u := range records {
		name := CanonicalKey(u.UnitName)
		u.HasPageZeroGUI = exists(name + "0")
		numbered := 0
		// The page number lives in three status-word bits, so page 7 is the
		// last addressable one [07 §9]; a further authored page could not be
		// selected and is not counted.
		for page := 1; page <= 7; page++ {
			if !exists(name + strconv.Itoa(page)) {
				break
			}
			numbered = page
		}
		switch {
		case numbered > 0:
			u.BuildPageCount = int32(numbered) + 1
		case u.HasPageZeroGUI:
			u.BuildPageCount = 1
		default:
			u.BuildPageCount = 0
		}
	}
}
