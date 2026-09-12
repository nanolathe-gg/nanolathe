package client

import "github.com/nanolathe-gg/nanolathe/internal/content"

// WarmBattleFeatureSequences prepares feature pixels for every definition the
// composed battle can admit. admitted includes the terrain table after mission
// placement and restore. All catalog unit corpses remain candidates regardless
// of current unit presence or build restrictions; subsequent deaths, reclaim,
// burning and reproduction stay inside the resulting closure [05 R-FEAT-01
// §2][05 R-FEAT-01 §5][05 R-FEAT-01 §10][06 §12.1].
//
// This is only a presentation loading policy. It does not alter content.SimArt,
// definition admission order, or any authoritative state [I6].
func (c *Client) WarmBattleFeatureSequences(cat *content.Catalog, admitted []*content.FeatureDef) {
	if c == nil {
		return
	}
	defs := battleFeatureDefinitions(cat, admitted)
	// Whole-catalog event warming could incidentally load a bank used here
	// only for rest art. Keep every reachable rest/shadow bank ready too, so
	// narrowing the event set cannot move those decodes into Draw.
	for _, def := range defs {
		if def.SeqName != "" || def.SeqNameShad != "" {
			_, _ = c.featureGAFFor(def.Filename)
		}
	}
	c.warmFeatureSequences(defs)
}

func battleFeatureDefinitions(cat *content.Catalog, admitted []*content.FeatureDef) []*content.FeatureDef {
	var defs []*content.FeatureDef
	seen := make(map[*content.FeatureDef]bool)
	add := func(def *content.FeatureDef) {
		if def != nil && !seen[def] {
			seen[def] = true
			defs = append(defs, def)
		}
	}
	byName := func(name string) *content.FeatureDef {
		if cat == nil || name == "" {
			return nil
		}
		return cat.Features[content.CanonicalKey(name)]
	}
	for _, def := range admitted {
		add(def)
	}
	if cat != nil {
		for _, unit := range cat.UnitRecords() {
			if unit != nil {
				add(byName(unit.Corpse))
			}
		}
	}
	// Follow the growing list through every successor, including cycles and
	// definitions which have no pixels themselves. Resolved pointers take
	// precedence, matching the feature/model admission paths.
	for i := 0; i < len(defs); i++ {
		def := defs[i]
		for _, link := range [...]struct {
			def  *content.FeatureDef
			name string
		}{
			{def.FeatureDeadDef, def.FeatureDead},
			{def.FeatureReclamateDef, def.FeatureReclamate},
			{def.FeatureBurntDef, def.FeatureBurnt},
		} {
			if link.def != nil {
				add(link.def)
			} else {
				add(byName(link.name))
			}
		}
	}
	return defs
}
