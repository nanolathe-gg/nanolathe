// Catalog.Hash, the compiled-catalog identity [02 §5] C12 (I1).

package content

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

// catalogHash computes Catalog.Hash over canonical bytes including defaults,
// independent of map iteration, identical across two runs (I1) [02 §5] C12.
// It never ranges a map without sorting; every map iteration is over sorted
// canonical keys with a total order (lower + tie-breaker) so provider order and
// Go map randomization cannot affect output. Model catalog [03 §2.4] C13 is
// included via the sorted model list which is itself case-insensitively sorted.
func catalogHash(c *Catalog) string {
	if c == nil {
		return ""
	}
	h := sha256.New()

	// Units — sorted canonical keys (I1) [02 §5] C12.
	if len(c.Units) > 0 {
		keys := make([]string, 0, len(c.Units))
		for k := range c.Units {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			u := c.Units[k]
			// Per-definition Hash already is sha256 over canonical bytes including defaults [02 §5] C12.
			// Including the canonical key and per-def hash preserves that while keeping catalog hash cheap.
			// Use non-map-order representation.
			fmt.Fprintf(h, "unit %s %s\n", k, u.Hash)
		}
	}
	// Categories — sorted registry names and fixed-width membership values
	// [R-P0-03]. ALL is included as an ordinary category token.
	if c.Categories != nil {
		for _, e := range c.Categories.entries {
			fmt.Fprintf(h, "category %s", e.Name)
			for _, word := range e.Membership.Words {
				fmt.Fprintf(h, " %08x", word)
			}
			fmt.Fprintln(h)
		}
	}
	// Weapons — sorted [02 §5] C12. Weapon ID selects record [02 "Weapon record"] C2 but catalog key is section name.
	if len(c.Weapons) > 0 {
		keys := make([]string, 0, len(c.Weapons))
		for k := range c.Weapons {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			w := c.Weapons[k]
			fmt.Fprintf(h, "weapon %s id=%d %s\n", k, w.ID, w.Hash)
		}
	}
	// Features — sorted [02 §5] C12. Successor links fatal verbatim [GAP T14] C9 already validated.
	if len(c.Features) > 0 {
		keys := make([]string, 0, len(c.Features))
		for k := range c.Features {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			f := c.Features[k]
			fmt.Fprintf(h, "feature %s %s\n", k, f.Hash)
		}
	}
	// Movement — sorted [02 "Movement class record"] C6 chained defaults+clamps already in per-def hash.
	if len(c.Movement) > 0 {
		keys := make([]string, 0, len(c.Movement))
		for k := range c.Movement {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			m := c.Movement[k]
			fmt.Fprintf(h, "movement %s %s\n", k, m.Hash)
		}
	}
	// Sides — slice indexed by SIDE ordinal [02 §6] C8, already deterministic; no sort needed.
	for _, s := range c.Sides {
		if s == nil {
			continue
		}
		fmt.Fprintf(h, "side %d %s %s\n", s.Index, s.CanonicalKey, s.Hash)
	}
	// Sounds — sorted categories [02 "Sound category record"] C11 [03 §8.3].
	if len(c.Sounds) > 0 {
		keys := make([]string, 0, len(c.Sounds))
		for k := range c.Sounds {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sc := c.Sounds[k]
			fmt.Fprintf(h, "sound %s %s\n", k, sc.Hash)
		}
	}
	// Maps — sorted headers only [02 "Map files"] [fmt ota] [fmt tnt].
	if len(c.Maps) > 0 {
		keys := make([]string, 0, len(c.Maps))
		for k := range c.Maps {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			mh := c.Maps[k]
			fmt.Fprintf(h, "map %s %s\n", k, mh.Hash)
		}
	}
	// AIProfiles — sorted [08 "Computer-controlled players"] [PLAN 02].
	if len(c.AIProfiles) > 0 {
		keys := make([]string, 0, len(c.AIProfiles))
		for k := range c.AIProfiles {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			ap := c.AIProfiles[k]
			fmt.Fprintf(h, "ai %s %s\n", k, ap.Hash)
		}
	}
	// BuildMenus — sorted pages, buttons in canonical order [02 "Build-menu catalog keys"].
	if len(c.BuildMenus) > 0 {
		keys := make([]string, 0, len(c.BuildMenus))
		for k := range c.BuildMenus {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			bm := c.BuildMenus[k]
			fmt.Fprintf(h, "buildmenu %s %s\n", k, bm.Hash)
		}
	}
	// Download placements retain union-enumeration and section order; no sort
	// is needed because CompileDownloadMenus produces the deterministic retail
	// order [02 R-CAT-01 §8]. Provenance distinguishes overlay winners.
	for _, placement := range c.DownloadPlacements {
		fmt.Fprintf(h, "download %d %d %s %t %d %d %s %t %s %s %d\n",
			placement.FileOrder, placement.ItemOrder,
			CanonicalKey(placement.Builder), placement.BuilderResolved, placement.Menu, placement.Button,
			CanonicalKey(placement.Product), placement.ProductResolved, placement.Provenance.LogicalPath,
			placement.Provenance.ProviderID, placement.Provenance.MountOrder)
	}
	// Sound aliases — sorted [02 "Sound aliases"].
	if len(c.Aliases) > 0 {
		keys := make([]string, 0, len(c.Aliases))
		for k := range c.Aliases {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(h, "alias %s %s\n", k, c.Aliases[k].Hash)
		}
	}
	// Battle tables [02 §6] C15 [PLAN_02] WU-02-8: included via per-definition hashes; never map-iterated unsorted.
	if c.LOS != nil && c.LOS.Hash != "" {
		fmt.Fprintf(h, "los %s\n", c.LOS.Hash)
	} else if c.LOS != nil {
		// LOS present but empty (minimal fixture without gamedata/los.tdf) — still distinguishable.
		fmt.Fprintf(h, "los empty\n")
	}
	if c.Meteor != nil && c.Meteor.Hash != "" {
		fmt.Fprintf(h, "meteor %s\n", c.Meteor.Hash)
	} else if c.Meteor != nil {
		fmt.Fprintf(h, "meteor empty\n")
	}
	// Model catalog sorted case-insensitively [03 §2.4] C13 — already sorted in c.sortedModels.
	if len(c.sortedModels) > 0 {
		for i, name := range c.sortedModels {
			fmt.Fprintf(h, "model %d %s %s\n", i, CanonicalKey(name), name)
		}
		// Also hash the canonical index mapping in sorted order for stability (I1).
		// Keys of modelIndex are canonical model names; sorted for determinism.
		mk := make([]string, 0, len(c.modelIndex))
		for k := range c.modelIndex {
			mk = append(mk, k)
		}
		sort.Strings(mk)
		for _, k := range mk {
			fmt.Fprintf(h, "modelidx %s %d\n", k, c.modelIndex[k])
		}
	}

	// Terminator: include counts so empty vs missing is distinct but stable.
	categoryCount := 0
	if c.Categories != nil {
		categoryCount = len(c.Categories.entries)
	}
	fmt.Fprintf(h, "counts u=%d w=%d f=%d m=%d sides=%d s=%d maps=%d ai=%d aliases=%d menus=%d downloads=%d models=%d categories=%d los=%d meteor=%d\n",
		len(c.Units), len(c.Weapons), len(c.Features), len(c.Movement), len(c.Sides), len(c.Sounds), len(c.Maps), len(c.AIProfiles), len(c.Aliases), len(c.BuildMenus), len(c.DownloadPlacements), len(c.sortedModels), categoryCount, lenTables(c.LOS), hasMeteor(c.Meteor))

	sum := h.Sum(nil)
	return hex.EncodeToString(sum)
}

func lenTables(lt *LOSTables) int {
	if lt == nil {
		return -1 // nil vs empty distinguishes missing field (old binary) vs fixture
	}
	return len(lt.Tables)
}
func hasMeteor(md *MeteorDefaults) int {
	if md == nil {
		return -1
	}
	if md.Hash == "" {
		return 0
	}
	return 1
}

// HashDefinition is in source.go; catalogHash is the Catalog-level aggregation [02 §5] C12 (I1).
