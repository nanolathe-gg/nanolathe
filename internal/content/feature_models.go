package content

import (
	"fmt"
	"sort"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

// FeatureClosure returns the requested definitions and their death, reclaim
// and burnt successors, once each, in canonical-name order. Feature TDF files
// are discovery data: retail compiles a definition when a caller requests its
// name, then resolves that record's successor links [05 R-FEAT-01 §§1, 2].
// Keeping every discovered definition in Catalog does not make every model a
// load requirement. Blank roots are ignored; a missing named root or successor
// retains the fatal feature-name diagnostic [02 R-MAP-01 §8].
func (c *Catalog) FeatureClosure(names []string) ([]*FeatureDef, error) {
	type namedFeature struct {
		name string
		def  *FeatureDef
	}
	pending := make([]namedFeature, 0, len(names))
	for _, name := range names {
		if name = trimTDFSemantic(name); name != "" {
			pending = append(pending, namedFeature{name: name})
		}
	}
	sort.Slice(pending, func(i, j int) bool {
		a, b := CanonicalKey(pending[i].name), CanonicalKey(pending[j].name)
		if a != b {
			return a < b
		}
		return pending[i].name < pending[j].name
	})
	seen := make(map[*FeatureDef]bool)
	var closure []namedFeature
	for i := 0; i < len(pending); i++ {
		item := pending[i]
		if item.def == nil && c != nil {
			item.def = c.Features[CanonicalKey(item.name)]
		}
		if item.def == nil {
			//lint:ignore ST1005 retail diagnostic text, reproduced verbatim [02 "Feature record"]
			return nil, fmt.Errorf(`Record "%s" missing from feature files`, item.name)
		}
		if seen[item.def] {
			continue
		}
		seen[item.def] = true
		if name := CanonicalKey(item.def.CanonicalKey); name != "" {
			item.name = name
		} else {
			item.name = CanonicalKey(item.name)
		}
		closure = append(closure, item)
		for _, next := range [...]namedFeature{
			{name: item.def.FeatureDead, def: item.def.FeatureDeadDef},
			{name: item.def.FeatureReclamate, def: item.def.FeatureReclamateDef},
			{name: item.def.FeatureBurnt, def: item.def.FeatureBurntDef},
		} {
			next.name = trimTDFSemantic(next.name)
			if next.def != nil || next.name != "" {
				pending = append(pending, next)
			}
		}
	}
	sort.SliceStable(closure, func(i, j int) bool { return closure[i].name < closure[j].name })
	defs := make([]*FeatureDef, len(closure))
	for i, item := range closure {
		defs[i] = item.def
	}
	return defs, nil
}

// ValidateFeatureModels checks every named model reachable from the requested
// feature names. A requested model must load and compile, including one only
// reached through a successor; an unused section has no model-load request
// [02 "Cross-reference failure policy"][05 R-FEAT-01 §§1, 2]. The check changes
// neither definitions nor catalog identity and runs before battle publication.
func (c *Catalog) ValidateFeatureModels(fs vfs.FSOps, names []string) error {
	defs, err := c.FeatureClosure(names)
	if err != nil {
		return err
	}
	return validateRequiredRecordModels(fs, nil, nil, featureModelDefinitions(defs))
}

// unitCorpseFeatures starts with resolved corpse names only: an unknown unit
// corpse is the no-wreck sentinel, unlike a requested map feature's fatal miss
// [02 "Cross-reference failure policy"]. All compiled unit records participate,
// including retained duplicate names [02 R-CAT-01 §5].
func unitCorpseFeatures(records []*UnitDef, features map[string]*FeatureDef) (map[string]*FeatureDef, error) {
	var names []string
	for _, unit := range records {
		if unit != nil && features[CanonicalKey(unit.Corpse)] != nil {
			names = append(names, unit.Corpse)
		}
	}
	defs, err := (&Catalog{Features: features}).FeatureClosure(names)
	if err != nil {
		return nil, err
	}
	return featureModelDefinitions(defs), nil
}

// The model validator consumes values only. Keying by logical model path
// deduplicates shared geometry without requiring synthetic names for fixture
// definitions that have not been stamped with a CanonicalKey.
func featureModelDefinitions(defs []*FeatureDef) map[string]*FeatureDef {
	models := make(map[string]*FeatureDef)
	for _, def := range defs {
		if path := requiredModelPath(def.Object); path != "" {
			models[path] = def
		}
	}
	return models
}
