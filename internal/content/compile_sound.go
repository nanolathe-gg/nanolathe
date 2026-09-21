// The sound category and alias compilers.

package content

import (
	"fmt"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// SoundSlot is one event row of a sound category [02 "Sound category record"] [03 §8.3].
// Slot 0 is an unused sentinel; slots 1..23 are real events named by Key.
// Priority and Cooldown are static per slot and global across every category,
// compiled in here from [03 §8.3] and consumed by phase 13.
type SoundSlot struct {
	Key      string // authored key name, e.g., "select" [02 "Sound category record"]
	Priority int32  // priority from [03 §8.3] static table, global per slot
	// Cooldown is the [03 §8.3] static table's multiplier: the window is
	// multiplier x 30 frames. The earlier "seconds" reading coincides
	// numerically at 30 Hz but names the wrong mechanism.
	Cooldown int32
	Variants []string // ordered variant aliases gathered K, K1, K2… [02 "Sound category record"] C11
	Captions []string // parallel captions per variant, from <key>text companion key [02 "Sound category record"]
}

// SoundCategory is a compiled sound category [02 "Sound category record"] C11.
// DefinitionHeader must be the first field per catalog convention [02 §5].
// The engine defines 24 event slots, slot 0 unused sentinel and 1..23 named;
// each row conceptually holds variant count and two parallel 64-byte string arrays
// (352-byte category, 24 rows 12 bytes [02 "Sound category record"]).
type SoundCategory struct {
	DefinitionHeader
	Name string // section name as authored, up to 64 bytes [02 "Sound category record"]
	// Slots indexed by slot; Slots[0] is unused sentinel [02 "Sound category record"] C11.
	Slots [24]SoundSlot
}

// SoundAlias is one alias registration [02 "Sound aliases"].
// Registration is capped at 255 entries, each holding a 32-byte alias name.
type SoundAlias struct {
	DefinitionHeader
	Alias string // alias name, 32-byte name [02 "Sound aliases"] — truncated to 32 bytes if longer
	Sound string // sound key value from alias section's `sound` key [02 "Sound aliases"]
}

// soundSlotStatic is the static per-slot priority/cooldown table from [03 §8.3].
// Priorities and cooldowns are per slot and global across every category [03 §8.3] C11.
// Slot 0 is unused sentinel.
var soundSlotStatic = [24]struct {
	Key      string
	Speech   string
	Priority int32
	Cooldown int32
}{
	0:  {"", "", 0, 0},                                        // sentinel unused [02 "Sound category record"] [03 §8.3]
	1:  {"select", "", 10, 0},                                 // [03 §8.3]
	2:  {"underattack", "Under Attack", 9, 20},                // [03 §8.3]
	3:  {"activate", "", 4, 2},                                // [03 §8.3]
	4:  {"deactivate", "", 4, 2},                              // [03 §8.3]
	5:  {"ok", "", 5, 1},                                      // [03 §8.3]
	6:  {"arrived", "Arrived", 3, 4},                          // [03 §8.3]
	7:  {"cant", "Cannot Comply", 8, 1},                       // [03 §8.3]
	8:  {"unitcomplete", "Nanolathe Complete", 3, 3},          // [03 §8.3]
	9:  {"build", "", 4, 2},                                   // [03 §8.3]
	10: {"repair", "", 3, 1},                                  // [03 §8.3]
	11: {"working", "", 2, 1},                                 // [03 §8.3]
	12: {"load", "", 7, 1},                                    // [03 §8.3]
	13: {"unload", "", 7, 1},                                  // [03 §8.3]
	14: {"cloak", "Cloaked", 7, 1},                            // [03 §8.3]
	15: {"uncloak", "Visible", 7, 1},                          // [03 §8.3]
	16: {"capture", "", 4, 1},                                 // [03 §8.3]
	17: {"count5", "five", 10, 0},                             // [03 §8.3]
	18: {"count4", "four", 10, 0},                             // [03 §8.3]
	19: {"count3", "three", 10, 0},                            // [03 §8.3]
	20: {"count2", "two", 10, 0},                              // [03 §8.3]
	21: {"count1", "one", 10, 0},                              // [03 §8.3]
	22: {"count0", "zero", 10, 0},                             // [03 §8.3]
	23: {"canceldestruct", "Self destruct terminated", 10, 0}, // [03 §8.3]
}

// gatherVariants collects variants for an event key K as K, K1, K2… stopping
// at the first missing index [02 "Sound category record"] C11.
// Every successful read appends to two parallel arrays: the sound alias and
// the caption from companion key <that key>text (empty when absent) [02 "Sound category record"].
// It uses only typed accessors from formats/tdf_typed.go [02 §4].
// Retail data authors K1 without bare K (e.g., select1) so we also accept
// numbered variants when bare K is absent — starting at K1 — to match the
// installed corpus while still honoring the bare-first rule when bare is present.
func gatherVariants(section *formats.Section, base string) ([]string, []string) {
	// Use typed accessors only [02 §4].
	if base == "" {
		return nil, nil
	}
	var variants []string
	var captions []string
	// Bare K first.
	if bare, ok := section.StringValue(base, ""); ok {
		variants = append(variants, bare)
		captionKey := base + "text"
		cap, _ := section.StringValue(captionKey, "")
		captions = append(captions, cap)
		for i := 1; ; i++ {
			key := fmt.Sprintf("%s%d", base, i)
			if val, ok := section.StringValue(key, ""); ok {
				variants = append(variants, val)
				ck := key + "text"
				cv, _ := section.StringValue(ck, "")
				captions = append(captions, cv)
			} else {
				break
			}
		}
		return variants, captions
	}
	// Bare K absent — gather numbered K1, K2… anyway. Retail's loader reads
	// the bare key, discards the result, and unconditionally gathers K1, K2, …
	// until the first absent index; the bare value (when present) is variant 0
	// and <key>text supplies each caption [02 R-SND-01 §1]. SPEC_CONFLICTS SC7
	// is closed: this is retail behaviour, not a divergence.
	for i := 1; ; i++ {
		key := fmt.Sprintf("%s%d", base, i)
		if val, ok := section.StringValue(key, ""); ok {
			variants = append(variants, val)
			ck := key + "text"
			cv, _ := section.StringValue(ck, "")
			captions = append(captions, cv)
		} else {
			break
		}
	}
	return variants, captions
}

// compileSoundCategorySection compiles a single sound category section into a
// SoundCategory. It uses typed accessors only from formats/tdf_typed.go.
func compileSoundCategorySection(section *formats.Section, categoryName string, prov Provenance) *SoundCategory {
	// Truncate category name to 64 bytes as per 352-byte record [02 "Sound category record"].
	name64 := boundedString(categoryName, 63)
	sc := &SoundCategory{
		DefinitionHeader: DefinitionHeader{
			CanonicalKey: CanonicalKey(categoryName),
			Provenance:   prov,
		},
		Name: name64,
	}
	for slot := 0; slot < len(soundSlotStatic); slot++ {
		static := soundSlotStatic[slot]
		key := static.Key
		priority := static.Priority
		cooldown := static.Cooldown
		var variants []string
		var captions []string
		if slot != 0 && key != "" {
			v, c := gatherVariants(section, key)
			// Each parallel string occupies a 64-byte NUL-terminated slot [02 "Sound category record"].
			for i, alias := range v {
				v[i] = boundedString(alias, 63)
			}
			for i, caption := range c {
				c[i] = boundedString(caption, 63)
			}
			variants = v
			captions = c
		}
		sc.Slots[slot] = SoundSlot{
			Key:      key,
			Priority: priority,
			Cooldown: cooldown,
			Variants: variants,
			Captions: captions,
		}
	}
	// Hash over canonical bytes including defaults, independent of map iteration (I1) [02 §5] C12.
	// Hash includes name and all slot variant lists in slot order (deterministic).
	var b strings.Builder
	fmt.Fprintf(&b, "%s|", sc.CanonicalKey)
	for slot := 0; slot < len(sc.Slots); slot++ {
		s := sc.Slots[slot]
		fmt.Fprintf(&b, "%d:%s:%d:%d|", slot, s.Key, s.Priority, s.Cooldown)
		for i, v := range s.Variants {
			cap := ""
			if i < len(s.Captions) {
				cap = s.Captions[i]
			}
			// Preserve variant order; variant gathering K, K1… is order-sensitive [02 "Sound category record"].
			fmt.Fprintf(&b, "%s/%s|", v, cap)
		}
		b.WriteString(";")
	}
	sc.Hash = HashDefinition([]byte(b.String()))
	return sc
}

// compileSoundCategoriesOrdered compiles sound categories from
// gamedata/sound.tdf [02 "Sound category record"] C11. Each top-level section
// is one category. It returns them keyed by CanonicalKey(section name) [02 §5]
// and, in parallel, in the order the effective file authors them, one entry per
// section. That order is the ordinal domain a unit's `soundcategory` falls back
// to when its text names no category
// [02 §5 "Cross-reference failure policy"][02 R-CAT-01 §5], and the name-keyed
// map cannot express it. The VFS resolves one winning sound.tdf, so "file
// order" is that single file's section order.
func compileSoundCategoriesOrdered(fs vfs.FSOps) (map[string]*SoundCategory, []*SoundCategory, error) {
	if fs == nil {
		return nil, nil, fmt.Errorf("content: nil VFS")
	}
	data, err := fs.ReadFileLimit("gamedata/sound.tdf", 2<<20)
	if err != nil {
		// Both sound tables are optional.  Missing sound data leaves the
		// catalog with no categories [02 "Sound category record"].
		return map[string]*SoundCategory{}, nil, nil
	}
	prov := Provenance{}
	if info, statErr := fs.Stat("gamedata/sound.tdf"); statErr == nil {
		prov = ProvenanceFrom(info)
	}
	doc, err := formats.ParseTDF(data)
	if err != nil {
		return nil, nil, formats.WithTDFContext(fs, err, "gamedata/sound.tdf")
	}
	result := make(map[string]*SoundCategory)
	var ordered []*SoundCategory
	for _, section := range doc.Root.Sections() {
		name := trimTDFSemantic(section.OriginalName)
		if name == "" {
			continue
		}
		sc := compileSoundCategorySection(section, name, prov)
		key := CanonicalKey(name)
		result[key] = sc
		ordered = append(ordered, sc)
	}
	return result, ordered, nil
}

// CompileSoundAliasesOrdered returns the alias map and the same registrations
// in authored section order. The order is consumed by the session registry;
// the map remains for existing catalog lookup callers.
func CompileSoundAliasesOrdered(fs vfs.FSOps) (map[string]*SoundAlias, []*SoundAlias, error) {
	return compileSoundAliasesOrdered(fs)
}

func compileSoundAliasesOrdered(fs vfs.FSOps) (map[string]*SoundAlias, []*SoundAlias, error) {
	if fs == nil {
		return nil, nil, fmt.Errorf("content: nil VFS")
	}
	data, err := fs.ReadFileLimit("gamedata/allsound.tdf", 1<<20)
	if err != nil {
		// Alias registrations are optional; absence does not invalidate units
		// or the category table [02 "Sound aliases"].
		return map[string]*SoundAlias{}, nil, nil
	}
	prov := Provenance{}
	if info, statErr := fs.Stat("gamedata/allsound.tdf"); statErr == nil {
		prov = ProvenanceFrom(info)
	}
	doc, err := formats.ParseTDF(data)
	if err != nil {
		return nil, nil, formats.WithTDFContext(fs, err, "gamedata/allsound.tdf")
	}
	result := make(map[string]*SoundAlias)
	ordered := make([]*SoundAlias, 0, 255)
	// ReadDir order not relevant here; section order in file is the registration order.
	// Cap at 255 per [02 "Sound aliases"].
	count := 0
	for _, section := range doc.Root.Sections() {
		if count >= 255 {
			break
		}
		aliasName := trimTDFSemantic(section.OriginalName)
		if aliasName == "" {
			continue
		}
		// 32-byte names [02 "Sound aliases"] — truncate to 32 bytes if longer.
		aliasName = boundedString(aliasName, 32)
		soundVal, present := section.StringValue("sound", "")
		if !present {
			// A section without a sound key is not a registration; an authored
			// empty value is present and remains a valid empty alias [02
			// "Sound aliases"].
			continue
		}
		soundVal = boundedString(soundVal, 255)
		key := CanonicalKey(aliasName)
		// Deduplicate: the first registration owns the identity.  The runtime
		// registry is ordered and case-insensitive; later authored sections do
		// not replace an already registered alias [03 §8.3].
		if _, exists := result[key]; exists {
			continue
		}
		sa := &SoundAlias{
			DefinitionHeader: DefinitionHeader{
				CanonicalKey: key,
				Provenance:   prov,
				Hash:         HashDefinition([]byte(fmt.Sprintf("%s|%s|", key, soundVal))),
			},
			Alias: aliasName,
			Sound: soundVal,
		}
		result[key] = sa
		ordered = append(ordered, sa)
		count++
	}
	return result, ordered, nil
}

// SoundData aggregates categories and aliases as discovered from
// gamedata/sound.tdf and gamedata/allsound.tdf [02 "Sound category record"] [02 "Sound aliases"].
type SoundData struct {
	Categories map[string]*SoundCategory
	// CategoryOrder is gamedata/sound.tdf section order — the ordinal domain
	// of the `soundcategory` fallback [02 §5][02 R-CAT-01 §5].
	CategoryOrder []*SoundCategory
	Aliases       map[string]*SoundAlias
	AliasOrder    []*SoundAlias
}

// CompileSounds compiles both sound categories and aliases.
// Discovery: gamedata/sound.tdf and aliases in gamedata/allsound.tdf [PLAN 02].
func CompileSounds(fs vfs.FSOps) (*SoundData, error) {
	cats, catOrder, err := compileSoundCategoriesOrdered(fs)
	if err != nil {
		return nil, err
	}
	aliases, aliasOrder, err := compileSoundAliasesOrdered(fs)
	if err != nil {
		return nil, err
	}
	return &SoundData{Categories: cats, CategoryOrder: catOrder, Aliases: aliases, AliasOrder: aliasOrder}, nil
}

// ResolveSoundCategory maps a unit definition's authored `soundcategory` text
// to a compiled category [02 §5 "Cross-reference failure policy"]
// [02 R-CAT-01 §5]. Established: an absent key resolves to category index 0 —
// the first section of gamedata/sound.tdf — and a present value is matched
// case-insensitively against the category names; on a miss the authored text
// is put through the ordinary C-runtime decimal conversion and the result is
// used as the ordinal, so non-numeric text (`NONE`, `CORE_KBOT`) is 0 and again
// selects the first category. There is no muted placeholder record: eleven
// stock definitions reach this path and speak the first category's lines.
//
// An absent key and an authored empty value coincide here: the conversion of
// empty text is 0, which is also the absent-key index.
func (c *Catalog) ResolveSoundCategory(authored string) *SoundCategory {
	if c == nil {
		return nil
	}
	if key := CanonicalKey(authored); key != "" {
		if sc, ok := c.Sounds[key]; ok && sc != nil {
			return sc
		}
	}
	ordinal := formats.ParseTDFInteger(authored)
	if ordinal < 0 || int(ordinal) >= len(c.SoundCategoryOrder) {
		// TODO(question): retail stores the converted ordinal unbounded and
		// [03 §8.3] step 1 indexes the record table with it, so an out-of-range
		// ordinal reads past the loaded categories; nothing establishes what it
		// then plays. Settled by tracing the category-record indexing for an
		// ordinal above the loaded count. Until then an out-of-range ordinal
		// resolves to no category, which is silent.
		return nil
	}
	return c.SoundCategoryOrder[ordinal]
}
