// Package content compiles retail's authored data into immutable definitions.
// This file implements the two-stage catalog construction [02 §5] C1,
// validation [SPEC_CONFLICTS SC2], cloning, and model sorting [03 §2.4] C13.
package content

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/vfs"
)

// WeaponDuplicate records a duplicate weapon ID collision for diagnostics [02 "Weapon record"].
// Winner is the smallest canonical key (lexicographically) that wins the ID, losers are
// additional keys sharing the same ID in sorted order. Deterministic: smallest wins, not last-wins-by-canonical-order.
// TODO(question) R-P0-01 duplicate winner policy: smallest canonical key wins is a supported inference
// from [02 §5] deterministic catalog requirement (I1) and [06 §3.3] family gating; last-wins-by-canonical-order
// would be nondeterministic under map iteration and is NOT acceptable per ON-04.
type WeaponDuplicate struct {
	ID     int32
	Keys   []string // sorted ascending, winner first
	Winner string   // Keys[0] when Keys non-empty
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

	// Weapon index compiled once for stable deterministic lookup [02 "Weapon record"] ON-04.
	// Smallest canonical key wins for duplicate IDs; duplicates exposed for diagnostics.
	weaponByID       map[int32]*WeaponDef // ID -> winner def, smallest canonical key wins (I1)
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
	// by extension already (units *.fbi, weapons *.tdf + gamedata/weapons.tdf,
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
	// ON-04 stable weapon index: smallest canonical key wins per ID, duplicates exposed.
	c.weaponByID, c.weaponDuplicates = buildWeaponIndex(weapons, weaponDuplicates)
	// C12 Catalog.Hash computed over canonical bytes including defaults,
	// independent of map iteration, identical across two runs (I1) [02 §5] C12.
	c.Hash = catalogHash(c)
	return c, nil
}

// buildWeaponIndex builds the once-compiled ID->def map and duplicate diagnostics ON-04.
// Winner is smallest canonical key (I1), not last-wins-by-canonical-order.
// TODO(question) duplicate winner policy: smallest canonical key wins is a supported inference from [02 §5] I1.
func buildWeaponIndex(weapons map[string]*WeaponDef, duplicates []WeaponDuplicate) (map[int32]*WeaponDef, []WeaponDuplicate) {
	if weapons == nil {
		return nil, duplicates
	}
	byID := make(map[int32]*WeaponDef, len(weapons))
	// If duplicates already provided by CompileWeaponsWithDuplicates, we can trust them,
	// but we still need to build byID deterministically as smallest wins.
	// Iterate sorted keys to ensure smallest wins.
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
		if _, exists := byID[wd.ID]; !exists {
			byID[wd.ID] = wd
		}
	}
	if duplicates == nil {
		// Fallback: compute duplicates from weapons map alone if caller didn't provide them
		// (e.g., manually constructed catalog). Group by ID.
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
			sort.Strings(ks)
			dup := WeaponDuplicate{ID: id, Keys: append([]string(nil), ks...), Winner: ks[0]}
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
			if strings.TrimSpace(u.Weapon1) != "" {
				if w, ok := out.Weapons[CanonicalKey(u.Weapon1)]; ok {
					u.Weapon1Def = w
				}
			}
			if strings.TrimSpace(u.Weapon2) != "" {
				if w, ok := out.Weapons[CanonicalKey(u.Weapon2)]; ok {
					u.Weapon2Def = w
				}
			}
			if strings.TrimSpace(u.Weapon3) != "" {
				if w, ok := out.Weapons[CanonicalKey(u.Weapon3)]; ok {
					u.Weapon3Def = w
				}
			}
			if strings.TrimSpace(u.ExplodeAs) != "" {
				if w, ok := out.Weapons[CanonicalKey(u.ExplodeAs)]; ok {
					u.ExplodeAsDef = w
				}
			}
			if strings.TrimSpace(u.SelfDestructAs) != "" {
				if w, ok := out.Weapons[CanonicalKey(u.SelfDestructAs)]; ok {
					u.SelfDestructAsDef = w
				}
			}
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
	// ON-04 stable weapon index: rebuild for cloned defs so pointers refer to cloned entries (I1)
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
func (c *Catalog) Weapon(key string) (*WeaponDef, bool) {
	if c == nil || c.Weapons == nil {
		return nil, false
	}
	w, ok := c.Weapons[CanonicalKey(key)]
	return w, ok
}

// WeaponByID selects the weapon with the given ID case-insensitively and
// deterministically (I1) [02 "Weapon record"] C2. ID is read with default -1
// first to select the record [02 "Weapon record"] C2. The scan is over sorted
// canonical keys so duplicate IDs have a stable winner independent of map iteration.
// ON-04: once-compiled index is preferred when available for performance and determinism.
func (c *Catalog) WeaponByID(id int32) (*WeaponDef, bool) {
	if c == nil || c.Weapons == nil {
		return nil, false
	}
	// Use once-compiled index if available ON-04 (I1)
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
	for _, k := range keys {
		if c.Weapons[k].ID == id {
			return c.Weapons[k], true
		}
	}
	return nil, false
}

// StableWeaponByID is an alias for WeaponByID that explicitly documents ON-04 stable index usage [02 "Weapon record"].
func (c *Catalog) StableWeaponByID(id int32) (*WeaponDef, bool) { return c.WeaponByID(id) }

// WeaponDuplicates returns duplicate ID diagnostics ON-04 (sorted by ID, winner smallest key).
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

// WeaponIndex returns a copy of the once-compiled ID->def map ON-04 (I1).
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

// RebuildWeaponIndex rebuilds the once-compiled index from the current Weapons map ON-04.
// Useful for fixtures that manually assign Weapons without going through Compile.
func (c *Catalog) RebuildWeaponIndex() {
	if c == nil {
		return
	}
	c.weaponByID, c.weaponDuplicates = buildWeaponIndex(c.Weapons, nil)
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
		entries:  make([]CategoryEntry, len(r.entries)),
		byName:   make(map[string]int, len(r.byName)),
		sentinel: r.sentinel,
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

// fillModelTops resolves each unit's ModelTop from its 3DO, reading every
// distinct objects3d/<ObjectName>.3do once [03 §3.2].
//
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// missing or unparsable model leaves ModelTop zero — the observer then sits on
// the ground, which is what an absent model means, not an invented height.
func fillModelTops(fs vfs.FSOps, units map[string]*UnitDef) {
	if fs == nil || len(units) == 0 {
		return
	}
	tops := make(map[string]int32)
	for _, u := range units {
		name := strings.ToLower(strings.TrimSpace(u.ObjectName))
		if name == "" {
			continue
		}
		top, done := tops[name]
		if !done {
			if data, err := fs.ReadFileLimit("objects3d/"+name+".3do", 1<<22); err == nil {
				if model, perr := formats.LoadThreeDO(data); perr == nil {
					// TODO(question): Historical analysis omitted; independently worded behavior is needed.
					top = (model.ModelTop() >> 16) & 0xFF
				}
			}
			tops[name] = top
		}
		u.ModelTop = top
	}
}
