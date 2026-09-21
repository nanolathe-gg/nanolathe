//go:build retail

package content

import "testing"

// TestStockSoundCategoryMissesResolveToTheFirstCategory locks the reach of the
// `soundcategory` failure policy on the reference install: eleven stock unit
// definitions name a category that no section defines, and every one of them
// resolves to the first authored category rather than to silence
// [02 §5 "Cross-reference failure policy"][02 R-CAT-01 §5].
func TestStockSoundCategoryMissesResolveToTheFirstCategory(t *testing.T) {
	c := compiledRetailCatalog(t)
	if len(c.SoundCategoryOrder) == 0 {
		t.Fatalf("no compiled sound categories; the ordinal domain is empty")
	}
	first := c.SoundCategoryOrder[0]
	// The census behind this list: six definitions author NONE, three CORE_KBOT,
	// one CORE_MEX and one COR_TANK, and sound.tdf defines none of those names.
	misses := []string{
		"armdrag", "armfdrag", "armfort",
		"cordrag", "corfdrag", "corfort",
		"corfast", "corfhlt", "corspy",
		"cormex", "corsent",
	}
	for _, name := range misses {
		def, ok := c.Units[name]
		if !ok || def == nil {
			t.Fatalf("stock definition %s missing from the catalog", name)
		}
		if _, named := c.Sounds[CanonicalKey(def.SoundCategory)]; named {
			t.Fatalf("%s soundcategory %q now matches a category by name; the census this test locks has moved",
				name, def.SoundCategory)
		}
		got := c.ResolveSoundCategory(def.SoundCategory)
		if got == nil {
			t.Fatalf("%s soundcategory %q resolved to no category; a miss converts to an ordinal, it is not silence [02 §5]",
				name, def.SoundCategory)
		}
		if got != first {
			t.Fatalf("%s soundcategory %q resolved to %s, want the first authored category %s [02 R-CAT-01 §5]",
				name, def.SoundCategory, got.Name, first.Name)
		}
	}
	// The consequence the fix exists for: those units have an under-attack line
	// (slot 2 of [03 §8.3]) instead of being voiceless.
	if len(first.Slots[2].Variants) == 0 {
		t.Fatalf("first category %s authors no underattack variant; the resolved cue would still be silent", first.Name)
	}
}
