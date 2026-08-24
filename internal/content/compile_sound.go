// Package content compiles retail's authored data into immutable definitions.
// This file implements the sound category and alias compilers
// [02 "Sound category record"] [02 "Sound aliases"] [03 §8.3] [GAP T14].
package content

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/vfs"
)

// SoundSlot is one event row of a sound category [02 "Sound category record"] [03 §8.3].
// Slot 0 is an unused sentinel; slots 1..23 are real events named by Key.
// Priority and Cooldown are static per slot and global across every category,
// compiled in here from [03 §8.3] and consumed by phase 13.
type SoundSlot struct {
	Key      string   // authored key name, e.g., "select" [02 "Sound category record"]
	Priority int32    // priority from [03 §8.3] static table, global per slot
	Cooldown int32    // cooldown in seconds from [03 §8.3] static table
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

// SoundSlotStatic returns the static table entry for a slot index (0..23) [03 §8.3].
func SoundSlotStatic(slot int) (key, speech string, priority, cooldown int32, ok bool) {
	if slot < 0 || slot >= len(soundSlotStatic) {
		return "", "", 0, 0, false
	}
	e := soundSlotStatic[slot]
	return e.Key, e.Speech, e.Priority, e.Cooldown, true
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
	// Bare absent — gather numbered K1, K2… for retail compatibility (e.g., select1).
	// This preserves variant gathering K, K1… semantics while tolerating the
	// corpus where bare is omitted [02 "Sound category record"] [GAP T14].
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
	name64 := categoryName
	if len(name64) > 64 {
		name64 = name64[:64]
	}
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
			// Truncate each alias to 64 bytes as per parallel 64-byte strings [02 "Sound category record"].
			for i, alias := range v {
				if len(alias) > 64 {
					v[i] = alias[:64]
				}
			}
			// Captions also bounded by 64 in retail but keep full for fidelity; truncate to 64 in hash if needed.
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

// CompileSoundCategories compiles sound categories from gamedata/sound.tdf
// [02 "Sound category record"] C11. Each top-level section is one category.
// It returns a map keyed by CanonicalKey(section name) [02 §5].
func CompileSoundCategories(fs vfs.FSOps) (map[string]*SoundCategory, error) {
	if fs == nil {
		return nil, fmt.Errorf("content: nil VFS")
	}
	data, err := fs.ReadFileLimit("gamedata/sound.tdf", 2<<20)
	if err != nil {
		return nil, fmt.Errorf("content: gamedata/sound.tdf: %w", err)
	}
	prov := Provenance{}
	if info, statErr := fs.Stat("gamedata/sound.tdf"); statErr == nil {
		prov = ProvenanceFrom(info)
	}
	doc, err := formats.ParseTDF(data)
	if err != nil {
		return nil, fmt.Errorf("content: gamedata/sound.tdf: %w", err)
	}
	result := make(map[string]*SoundCategory)
	for _, section := range doc.Root.Sections() {
		name := strings.TrimSpace(section.OriginalName)
		if name == "" {
			continue
		}
		sc := compileSoundCategorySection(section, name, prov)
		key := CanonicalKey(name)
		result[key] = sc
	}
	return result, nil
}

// compileSoundCategories is an unexported alias for Catalog integration [02 §5] C1.
func compileSoundCategories(fs vfs.FSOps) (map[string]*SoundCategory, error) {
	return CompileSoundCategories(fs)
}

// CompileSoundCategoriesSorted returns categories sorted by canonical key for
// hash-stable iteration (I1).
func CompileSoundCategoriesSorted(fs vfs.FSOps) ([]*SoundCategory, error) {
	m, err := CompileSoundCategories(fs)
	if err != nil {
		return nil, err
	}
	out := make([]*SoundCategory, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CanonicalKey < out[j].CanonicalKey })
	return out, nil
}

// CompileSoundAliases compiles alias registrations from gamedata/allsound.tdf
// [02 "Sound aliases"]. Each top-level section is one alias; its `sound` key
// names the sample. Registration is capped at 255 entries, each holding a
// 32-byte alias name [02 "Sound aliases"].
// It returns a map keyed by CanonicalKey(alias name).
func CompileSoundAliases(fs vfs.FSOps) (map[string]*SoundAlias, error) {
	if fs == nil {
		return nil, fmt.Errorf("content: nil VFS")
	}
	data, err := fs.ReadFileLimit("gamedata/allsound.tdf", 1<<20)
	if err != nil {
		return nil, fmt.Errorf("content: gamedata/allsound.tdf: %w", err)
	}
	prov := Provenance{}
	if info, statErr := fs.Stat("gamedata/allsound.tdf"); statErr == nil {
		prov = ProvenanceFrom(info)
	}
	doc, err := formats.ParseTDF(data)
	if err != nil {
		return nil, fmt.Errorf("content: gamedata/allsound.tdf: %w", err)
	}
	result := make(map[string]*SoundAlias)
	// ReadDir order not relevant here; section order in file is the registration order.
	// Cap at 255 per [02 "Sound aliases"].
	count := 0
	for _, section := range doc.Root.Sections() {
		if count >= 255 {
			break
		}
		aliasName := strings.TrimSpace(section.OriginalName)
		if aliasName == "" {
			continue
		}
		// 32-byte names [02 "Sound aliases"] — truncate to 32 bytes if longer.
		if len(aliasName) > 32 {
			aliasName = aliasName[:32]
		}
		soundVal, _ := section.StringValue("sound", "")
		key := CanonicalKey(aliasName)
		// Deduplicate: last wins is deterministic within cap.
		if _, exists := result[key]; exists {
			// Duplicate alias — overwrite but do not increase count again.
			result[key] = &SoundAlias{
				DefinitionHeader: DefinitionHeader{
					CanonicalKey: key,
					Provenance:   prov,
					Hash:         HashDefinition([]byte(fmt.Sprintf("%s|%s|", key, soundVal))),
				},
				Alias: aliasName,
				Sound: soundVal,
			}
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
		count++
	}
	return result, nil
}

// compileSoundAliases is an unexported alias for Catalog integration.
func compileSoundAliases(fs vfs.FSOps) (map[string]*SoundAlias, error) {
	return CompileSoundAliases(fs)
}

// CompileSoundAliasesSorted returns aliases sorted by canonical key (I1).
func CompileSoundAliasesSorted(fs vfs.FSOps) ([]*SoundAlias, error) {
	m, err := CompileSoundAliases(fs)
	if err != nil {
		return nil, err
	}
	out := make([]*SoundAlias, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CanonicalKey < out[j].CanonicalKey })
	return out, nil
}

// SoundData aggregates categories and aliases as discovered from
// gamedata/sound.tdf and gamedata/allsound.tdf [02 "Sound category record"] [02 "Sound aliases"].
type SoundData struct {
	Categories map[string]*SoundCategory
	Aliases    map[string]*SoundAlias
}

// CompileSounds compiles both sound categories and aliases.
// Discovery: gamedata/sound.tdf and aliases in gamedata/allsound.tdf [PLAN 02].
func CompileSounds(fs vfs.FSOps) (*SoundData, error) {
	cats, err := CompileSoundCategories(fs)
	if err != nil {
		return nil, err
	}
	aliases, err := CompileSoundAliases(fs)
	if err != nil {
		return nil, err
	}
	return &SoundData{Categories: cats, Aliases: aliases}, nil
}

// compileSounds is an unexported alias for Catalog integration.
func compileSounds(fs vfs.FSOps) (*SoundData, error) {
	return CompileSounds(fs)
}
