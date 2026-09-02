// Package content compiles retail's authored data into immutable definitions.
// This file implements the two-stage catalog construction [02 §5] C1,
// validation [SPEC_CONFLICTS SC2], cloning, and model sorting [03 §2.4] C13.
package content

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/vfs"
)

// requiredContentError keeps required-resource failures actionable at the
// content boundary. Concrete VFS overlays expose Sources; focused fixtures
// expose only Stat, so both paths are supported without widening FSOps
// [AGENTS.md diagnostics].
func requiredContentError(fs vfs.FSOps, logical, expected string, cause error) error {
	providers := searchedProviderIDs(fs, logical)
	return fmt.Errorf("nanolathe: required authored resource: logical path %s, providers searched [%s], expected %s: %w", logical, strings.Join(providers, ", "), expected, cause)
}

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
	Units map[string]*UnitDef // key = CanonicalKey(unitname) [02 §5]
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
	units, err := CompileUnits(fs)
	if err != nil {
		return nil, err
	}
	report.Report(FamilyUnits, 100)
	categories, err := CompileCategories(units)
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
	ApplyMovementFootprints(units, movement)
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
	maps, err := compileMapsWithProgress(fs, report)
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
	LinkUnitWeapons(units, weapons)
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
	var warnings []string
	if len(buildMenus) > 0 {
		warnings = EnforceDownloadable(units, MenuButtonNames(buildMenus))
	}
	// Model sorting C13: sort model catalog case-insensitively before caching per-unit-type pointer [03 §2.4].
	report.Report(FamilyBuildMenus, 100)
	sortedModels, modelIndex := buildModelCatalog(units)
	fillModelTops(fs, units)
	// The page-count byte is a per-record probe of the authored page windows,
	// step 5 of the compiler's own order [02 R-CAT-01 §5].
	fillBuildPages(fs, units)
	if err := fillUnitScripts(fs, units); err != nil {
		return nil, err
	}
	report.Report(FamilyModels, 100)

	// Manifest: vfs.ManifestHash() for identity [PLAN 02].
	manifest, _ := manifestHashFor(fs)

	c := &Catalog{
		Units:        units,
		Categories:   categories,
		Weapons:      weapons,
		Features:     features,
		Movement:     movement,
		Sides:        sides,
		Sounds:       sounds,
		Maps:         maps,
		LOS:          losTables,
		Sight:        sightShapes,
		Meteor:       meteorDefaults,
		AIProfiles:   aiProfiles,
		Aliases:      aliases,
		AliasOrder:   aliasOrder,
		BuildMenus:   buildMenus,
		Warnings:     warnings,
		Manifest:     manifest,
		sortedModels: sortedModels,
		modelIndex:   modelIndex,
	}
	// Stable weapon index: one record per slot [02 §5 R-CONTENT-02].
	c.weaponByID, c.weaponDuplicates = buildWeaponIndex(weapons, weaponDuplicates)
	// C12 Catalog.Hash computed over canonical bytes including defaults,
	// independent of map iteration, identical across two runs (I1) [02 §5] C12.
	c.Hash = catalogHash(c)
	return c, nil
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

// buildModelCatalog collects distinct ObjectName values from units and sorts
// them case-insensitively before caching a per-unit-type pointer [03 §2.4] C13.
// The sort makes piece/type identity independent of provider order.
func buildModelCatalog(units map[string]*UnitDef) ([]string, map[string]int) {
	if len(units) == 0 {
		return nil, nil
	}
	// Deduplicate by canonical key so "arm_3do" and "ARM_3DO" are one entry.
	// The surviving spelling is chosen deterministically: iterate units by
	// sorted canonical key and keep the lexicographically smallest original
	// among fold-equal spellings, so provider order and Go map randomization
	// cannot choose the representative (I1).
	canonToOriginal := make(map[string]string)
	unitKeys := make([]string, 0, len(units))
	for k := range units {
		unitKeys = append(unitKeys, k)
	}
	sort.Strings(unitKeys)
	for _, k := range unitKeys {
		name := strings.TrimSpace(units[k].ObjectName)
		if name == "" {
			continue
		}
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
		li, lj := strings.ToLower(sorted[i]), strings.ToLower(sorted[j])
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
	name := strings.TrimSpace(u.ObjectName)
	if name == "" {
		return "", 0, false
	}
	idx, ok := c.ModelIndex(name)
	if !ok {
		return "", 0, false
	}
	return name, idx, true
}

// UnitDefIndex returns the stable catalog index for a unit definition
// keyed by CanonicalKey (case-insensitive) [02 §5][05 "Build request and factory queue behavior"].
// Indices are 1-based (0 is null sentinel) and ordered by sorted canonical keys (I1),
// so they are deterministic across runs and independent of map iteration.
// This replaces the invented FNV-1a product hashing (N04) with a collision-free
// stable index [P0-I05].
func (c *Catalog) UnitDefIndex(key string) (uint32, bool) {
	if c == nil || c.Units == nil {
		return 0, false
	}
	ck := CanonicalKey(key)
	if ck == "" {
		return 0, false
	}
	keys := make([]string, 0, len(c.Units))
	for k := range c.Units {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		if k == ck {
			return uint32(i + 1), true // 1-based, 0 sentinel
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
	return c != nil && c.Hash != "" && len(c.Units) > 0
}

// UnitIndexOf returns a unit definition's stable catalog index and whether
// that definition is this catalog's own record for its canonical key
// [CNT-05][02 §5][R-P0-03]. It is the definition-identity accessor mirroring
// ModelIndex above: the index is the 1-based UnitDefID stamped once at
// category-link time (0 remains the null sentinel), so identity is derived
// from the immutable catalog position, never from use order. The pointer
// comparison rejects a same-key impostor that did not come from this catalog.
// The key falls back to the canonical UnitName for definitions whose header
// key was never stamped (hand-built fixture catalogs).
func (c *Catalog) UnitIndexOf(def *UnitDef) (uint32, bool) {
	if c == nil || def == nil || len(c.Units) == 0 {
		return 0, false
	}
	ck := def.CanonicalKey
	if ck == "" {
		ck = CanonicalKey(def.UnitName)
	}
	if ck == "" {
		return 0, false
	}
	own, ok := c.Units[ck]
	if !ok || own != def {
		return 0, false
	}
	return def.UnitDefID, true
}

// UnitDefByIndex returns the unit definition for a catalog index [02 §5][05].
// Index 0 is invalid (null sentinel).
func (c *Catalog) UnitDefByIndex(idx uint32) (*UnitDef, bool) {
	if c == nil || c.Units == nil || idx == 0 {
		return nil, false
	}
	keys := make([]string, 0, len(c.Units))
	for k := range c.Units {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if int(idx) > len(keys) {
		return nil, false
	}
	k := keys[idx-1]
	u, ok := c.Units[k]
	return u, ok
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

// SortedUnitKeys returns the unit catalog keys sorted ascending (I1) [02 §5].
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
	// Units deep copy without weapon pointers (rewired after weapons cloned)
	if c.Units != nil {
		out.Units = make(map[string]*UnitDef, len(c.Units))
		for k, v := range c.Units {
			out.Units[k] = cloneUnit(v)
		}
		// Rewire weapon links deterministically (sorted keys I1) [02 §5] C1
		keys := make([]string, 0, len(out.Units))
		for k := range out.Units {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			u := out.Units[k]
			out.rewireWeaponLink(u.Weapon1, &u.Weapon1Def)
			out.rewireWeaponLink(u.Weapon2, &u.Weapon2Def)
			out.rewireWeaponLink(u.Weapon3, &u.Weapon3Def)
			out.rewireWeaponLink(u.ExplodeAs, &u.ExplodeAsDef)
			out.rewireWeaponLink(u.SelfDestructAs, &u.SelfDestructAsDef)
		}
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
			if strings.TrimSpace(f.FeatureDead) != "" {
				if target, ok := out.Features[CanonicalKey(f.FeatureDead)]; ok {
					f.FeatureDeadDef = target
				}
			}
			if strings.TrimSpace(f.FeatureReclamate) != "" {
				if target, ok := out.Features[CanonicalKey(f.FeatureReclamate)]; ok {
					f.FeatureReclamateDef = target
				}
			}
			if strings.TrimSpace(f.FeatureBurnt) != "" {
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
	} else if c.weaponByID != nil {
		// No weapons but index exists (empty) — copy duplicates diagnostics
		out.weaponByID = nil
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
	if ck == "" {
		return nil, false
	}
	for _, w := range c.WeaponRecordsByID() {
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
//
// LinkUnitWeapons (compile_unit.go) stores this resolution on the unit's
// link defs: a missed link holds the record-0 def when the family carries
// one, else nil [02 §5 R-CONTENT-02].
func (c *Catalog) WeaponLink(name string) (def *WeaponDef, active bool) {
	if w, ok := c.WeaponByName(name); ok {
		return w, w.ID != 0
	}
	// Miss: the link references record 0 when the table has one [02 §5 R-CONTENT-02].
	if w, ok := c.WeaponByID(0); ok {
		return w, false
	}
	return nil, false
}

// IsWeaponInactive is the one inactive rule for a resolved weapon link [02 §5
// R-CONTENT-02]: the link is inactive when it is nil (no record 0 in the
// family) OR points at the record occupying slot 0 — ID 0, stock [noweapon],
// recognized by its zero slot-number byte, never by name. WeaponLink fills
// missed links with that record, so every consumer that used to treat
// non-nil as active must gate on this predicate instead.
func IsWeaponInactive(def *WeaponDef) bool {
	return def == nil || def.ID == 0
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
}

// rewireWeaponLink rewires one cloned unit weapon link onto the cloned weapon
// table, re-deriving WeaponLink's resolution and miss policy [02 §5
// R-CONTENT-02]: a name hit points at the cloned record; a miss fills the
// slot with the clone's own record-0 inactive sentinel (ID 0) when the family
// carries one, else leaves the explicit nil inactive marker — so a cloned
// catalog preserves sentinel links instead of dropping them to nil. An empty
// name leaves the slot nil, as at compile.
func (c *Catalog) rewireWeaponLink(name string, slot **WeaponDef) {
	if strings.TrimSpace(name) == "" {
		return
	}
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

// modelTopPair is one model's top extent in both the forms the definition
// carries: the full 16.16 dword and its whole-unit high word [06 R-DMG-01 §7].
type modelTopPair struct {
	fixed int32 // full 16.16 extent, floored at zero [06 R-DMG-01 §7]
	whole int32 // the byte-masked whole-unit high word the LOS writer reads [03 §3.2]
}

// fillModelTops resolves each unit's ModelTop from its 3DO, reading every
// distinct objects3d/<ObjectName>.3do once [03 §3.2].
//
// Retail computes this at model load as a 16.16 model extent and writes it as
// the definition's upper Y bound; the LOS writer consumes only its whole-unit
// component as the observer height addend [03 §3.2], while the projectile
// contact test's vertical band needs the whole dword [06 R-DMG-01 §7]. Both
// forms are stored, from one walk. A missing or unparsable model leaves both
// zero.
func fillModelTops(fs vfs.FSOps, units map[string]*UnitDef) {
	if fs == nil || len(units) == 0 {
		return
	}
	tops := make(map[string]modelTopPair)
	for _, u := range units {
		name := strings.ToLower(strings.TrimSpace(u.ObjectName))
		if name == "" {
			continue
		}
		top, done := tops[name]
		if !done {
			if data, err := fs.ReadFileLimit("objects3d/"+name+".3do", 1<<22); err == nil {
				if model, perr := formats.LoadThreeDO(data); perr == nil {
					raw := model.ModelTop() // 16.16, floored at zero [fmt 3do]
					top.fixed = raw
					// Convert the model's 16.16 extent to whole world units.
					top.whole = (raw >> 16) & 0xFF
				}
			}
			tops[name] = top
		}
		u.ModelTop = top.whole
		u.ModelTopFixed = top.fixed
	}
}

// fillUnitScripts resolves every unit's required compiled COB program at
// catalog link time. Retail cannot create a unit with a null program, so the
// catalog rejects missing, unreadable, malformed, nil, and empty programs
// before publishing any definition [04 R-COB-04 §8].
func fillUnitScripts(fs vfs.FSOps, units map[string]*UnitDef) error {
	if fs == nil || len(units) == 0 {
		return nil
	}
	keys := make([]string, 0, len(units))
	for key := range units {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		u := units[key]
		if u == nil {
			continue
		}
		logical := "scripts/" + CanonicalKey(u.UnitName) + ".cob"
		prog, found, err := cob.LoadFromFS(fs, u.UnitName)
		if err != nil || !found || prog == nil || len(prog.Code) == 0 {
			return unitScriptMissingError(fs, logical)
		}
		u.Script = prog
	}
	return nil
}

// fillBuildPages compiles every definition's build-menu page-count byte from
// the authored page windows, which is step 5 of the catalog compiler's per-
// record work [02 R-CAT-01 §5]: with `<n>` the unit name, `guis/<n>0.GUI`
// existing sets the page-zero bit, then `guis/<n>1.GUI`, `guis/<n>2.GUI`, …
// are probed until the first missing one, and the byte becomes the index of
// that first missing page when at least one numbered page existed, else 1 when
// page 0 exists, else 0.
//
// The probe is by existence and non-zero size, and it stops at the first gap:
// `guis/<n>1.GUI` and `guis/<n>3.GUI` with no `<n>2` is a count of 2, not 4.
func fillBuildPages(fs vfs.FSOps, units map[string]*UnitDef) {
	if fs == nil || len(units) == 0 {
		return
	}
	exists := func(name string) bool {
		info, err := fs.Stat("guis/" + name + ".gui")
		return err == nil && info.Size > 0
	}
	for _, u := range units {
		name := strings.ToLower(strings.TrimSpace(u.UnitName))
		if name == "" {
			continue
		}
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
