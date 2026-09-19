package content

import (
	"fmt"
	"sort"
)

// CategoryMaskWords is the registry width retail's category compiler uses:
// sixteen 32-bit words, indexed by unit-definition ID [R-P0-03]. It remains
// the retail constant — the width of the authored CTRL_F type filter and of
// the retail definition domain — while a catalog's own membership width is
// chosen from its compile limits (docs/DESIGN_CONTENT_VFS.md §5).
const CategoryMaskWords = 16

// CategoryMask is an immutable-by-convention unit-definition membership set.
// Membership is ID-indexed: word = id/64, bit = id%64 [R-P0-03]. The storage
// is only as wide as the highest member needs, and IDs past the end are
// absent, so masks of different widths mix freely.
//
// The 64-bit word is Go layout, not a retail record layout (I13): retail's
// sixteen 32-bit words describe the executable's registry entry, and the
// canonical form the definition digest hashes is still 32-bit words in
// ascending ID order, so a retail catalog's hash is unaffected.
//
// The value is a header over shared storage. Treat a mask obtained from a
// catalog as read-only: Or returns fresh storage rather than writing through
// either input, and nothing outside the compiler sets a bit.
type CategoryMask struct {
	words []uint64
}

// MaskForID returns the single-bit membership set for one definition ID. ID
// zero is the catalog's null definition sentinel and yields the empty set.
func MaskForID(id uint32) CategoryMask {
	var m CategoryMask
	if id == 0 {
		return m
	}
	m.setID(id)
	return m
}

// Contains reports whether id is a member. ID zero is reserved for the
// catalog's null definition sentinel and is never assigned by the compiler.
func (m CategoryMask) Contains(id uint32) bool {
	word := id >> 6
	if uint64(word) >= uint64(len(m.words)) {
		return false
	}
	return m.words[word]&(uint64(1)<<(id&63)) != 0
}

// IsZero reports whether no unit-definition IDs are members.
func (m CategoryMask) IsZero() bool {
	for _, word := range m.words {
		if word != 0 {
			return false
		}
	}
	return true
}

// Or returns the union of two masks. Storage is shared, so a union that adds
// members allocates rather than writing through either input; a union that
// adds none returns the wider input, which is the common case when a caller
// accumulates over units that share a definition.
func (m CategoryMask) Or(other CategoryMask) CategoryMask {
	wide, narrow := m.words, other.words
	if len(narrow) > len(wide) {
		wide, narrow = narrow, wide
	}
	if len(wide) == 0 {
		return CategoryMask{}
	}
	subset := true
	for i, word := range narrow {
		if word&^wide[i] != 0 {
			subset = false
			break
		}
	}
	if subset {
		return CategoryMask{words: wide}
	}
	out := CategoryMask{words: make([]uint64, len(wide))}
	copy(out.words, wide)
	for i, word := range narrow {
		out.words[i] |= word
	}
	return out
}

// Intersects reports whether two membership sets share a unit-definition ID.
// Words past the shorter mask's end hold no members, so the common prefix
// decides.
func (m CategoryMask) Intersects(other CategoryMask) bool {
	words := other.words
	if len(m.words) < len(words) {
		words = words[:len(m.words)]
	}
	for i, word := range words {
		if m.words[i]&word != 0 {
			return true
		}
	}
	return false
}

// Equal reports whether two masks hold the same members. Storage width is not
// identity: a mask widened by a larger domain equals the narrow mask with the
// same bits set.
func (m CategoryMask) Equal(other CategoryMask) bool {
	wide, narrow := m.words, other.words
	if len(narrow) > len(wide) {
		wide, narrow = narrow, wide
	}
	for i, word := range narrow {
		if wide[i] != word {
			return false
		}
	}
	for _, word := range wide[len(narrow):] {
		if word != 0 {
			return false
		}
	}
	return true
}

// canonicalWords32 renders the mask as 32-bit words in ascending ID order,
// zero-padded to at least min words. Words above the highest member are
// dropped before padding, so the result depends only on which IDs are members
// and not on how wide the mask's storage is — which keeps a definition's
// digest stable when a profile widens the domain [02 §5] C12.
func (m CategoryMask) canonicalWords32(min int) []uint32 {
	significant := 0
	for i := len(m.words) - 1; i >= 0; i-- {
		if m.words[i] == 0 {
			continue
		}
		significant = (i + 1) * 2
		if m.words[i]>>32 == 0 {
			significant--
		}
		break
	}
	if significant < min {
		significant = min
	}
	out := make([]uint32, significant)
	for i := range out {
		word := i >> 1
		if word >= len(m.words) {
			break
		}
		if i&1 == 0 {
			out[i] = uint32(m.words[word])
		} else {
			out[i] = uint32(m.words[word] >> 32)
		}
	}
	return out
}

// setID adds one member, widening the storage to reach it. It is the
// compiler's only writer; callers outside the compiler treat masks as values.
func (m *CategoryMask) setID(id uint32) {
	word := int(id >> 6)
	if word >= len(m.words) {
		widened := make([]uint64, word+1)
		copy(widened, m.words)
		m.words = widened
	}
	m.words[word] |= uint64(1) << (id & 63)
}

// set adds one member after checking it against the compile's usable domain.
// capacity is the highest usable ID: ID zero is the null definition sentinel
// and the domain's last usable ID is capacity.
func (m *CategoryMask) set(id uint32, capacity int) error {
	if id == 0 || uint64(id) > uint64(capacity) {
		return fmt.Errorf("category: unit definition ID %d outside usable 1..%d domain", id, capacity)
	}
	m.setID(id)
	return nil
}

// CategoryEntry is one case-insensitive registry row. Membership is a value,
// so callers cannot mutate the compiler's storage through a lookup result.
type CategoryEntry struct {
	Name       string
	Membership CategoryMask
}

// CategoryRegistry is the sorted, case-insensitive category token registry
// [R-P0-03]. ALL is an ordinary authored token with mandatory membership for
// every compiled unit; it is not a private or empty-name sentinel.
type CategoryRegistry struct {
	entries []CategoryEntry
	byName  map[string]int
}

// Entries returns registry entries in case-insensitive sorted order.
func (r *CategoryRegistry) Entries() []CategoryEntry {
	if r == nil {
		return nil
	}
	out := make([]CategoryEntry, len(r.entries))
	copy(out, r.entries)
	return out
}

// Lookup returns the membership set for name. Unknown names are absent from a
// completed registry; linking creates unknown authored names.
func (r *CategoryRegistry) Lookup(name string) (CategoryMask, bool) {
	if r == nil {
		return CategoryMask{}, false
	}
	i, ok := r.byName[CanonicalKey(name)]
	if !ok {
		return CategoryMask{}, false
	}
	return r.entries[i].Membership, true
}

// SentinelMembership is retained as a source-compatible accessor.  Retail's
// mandatory membership is the literal ALL category, so this returns ALL.
func (r *CategoryRegistry) SentinelMembership() CategoryMask {
	if r == nil {
		return CategoryMask{}
	}
	m, _ := r.Lookup("ALL")
	return m
}

// CategoryNames returns a copy of sorted user-visible registry names.
func (r *CategoryRegistry) CategoryNames() []string {
	if r == nil {
		return nil
	}
	out := make([]string, len(r.entries))
	for i := range r.entries {
		out[i] = r.entries[i].Name
	}
	return out
}

// CompileCategories links unit IDs, unit category strings, and authored target
// fields into one immutable registry over the retail definition domain. It is
// useful independently of Catalog compilation for deterministic fixture tests
// and later catalog extensions.
func CompileCategories(units map[string]*UnitDef) (*CategoryRegistry, error) {
	units = canonicalUnitMap(units)
	return compileCategoryRecords(unitMapRecords(units), units, RetailLimits())
}

func compileCategoryRecords(records []*UnitDef, _ map[string]*UnitDef, limits Limits) (*CategoryRegistry, error) {
	r := &CategoryRegistry{byName: make(map[string]int)}
	// UnitDefIndex is 1-based (zero is its null sentinel), so a domain of N
	// bits ends at usable ID N-1 — retail's 512-bit domain ends at 511
	// [02 §5]. One definition past the end is diagnosed rather than aliased or
	// truncated [R-P0-03]; a content profile raises the domain instead
	// (docs/DESIGN_CONTENT_VFS.md §5 "Content profiles").
	limits, err := limits.normalize()
	if err != nil {
		return nil, err
	}
	capacity := limits.definitionCapacity()
	if len(records) > capacity {
		return nil, fmt.Errorf("category: %d unit definitions exceed %d usable IDs in %d-bit domain", len(records), capacity, limits.Units)
	}

	for i, u := range records {
		if u == nil {
			continue
		}
		id := uint32(i + 1)
		u.UnitDefID = id
		// These links are written only by successful secondary parsing; an
		// unparsed record still owns its index [02 R-CAT-01 §5].
		if u.DiscoveryOnly {
			u.Hash = HashDefinition(writeUnitCanonical(u))
			continue
		}
		var ownMask CategoryMask
		if err := ownMask.set(id, capacity); err != nil {
			return nil, err
		}
		u.UnitMask = ownMask
		// Every unit belongs to the literal ALL category, in addition to each
		// authored category token [R-P0-03].
		allIdx := r.ensure("ALL")
		if err := r.entries[allIdx].Membership.set(id, capacity); err != nil {
			return nil, err
		}
		for _, token := range contentASCIIFields(u.Category) {
			idx := r.ensure(token)
			if err := r.entries[idx].Membership.set(id, capacity); err != nil {
				return nil, err
			}
		}
	}

	// FBI target fields select registry entries directly, including empty
	// names and names that also identify units. Later membership writes reach
	// the shared retail bitsets [02 R-P0-03 §5].
	for _, u := range records {
		if u == nil || u.DiscoveryOnly {
			continue
		}
		for _, target := range []string{u.BadTargetCategoryWPRI, u.BadTargetCategoryWSEC, u.BadTargetCategoryWSPE, u.NoChaseCategory} {
			r.ensure(target)
		}
	}

	for _, u := range records {
		if u == nil || u.DiscoveryOnly {
			continue
		}
		u.BadTargetCategoryWPRIMask, _ = r.Lookup(u.BadTargetCategoryWPRI)
		u.BadTargetCategoryWSECMask, _ = r.Lookup(u.BadTargetCategoryWSEC)
		u.BadTargetCategoryWSPEMask, _ = r.Lookup(u.BadTargetCategoryWSPE)
		u.NoChaseCategoryMask, _ = r.Lookup(u.NoChaseCategory)
		// Linked fields are part of definition identity; refresh the per-unit
		// digest after the masks and stable ID are known [02 §5] C12.
		u.Hash = HashDefinition(writeUnitCanonical(u))
	}
	return r, nil
}

func (r *CategoryRegistry) ensure(name string) int {
	ck := CanonicalKey(name)
	if i, ok := r.byName[ck]; ok {
		return i
	}
	r.entries = append(r.entries, CategoryEntry{Name: ck})
	// Insertion order is not identity; sort the vector and rebuild indices at
	// the end of linking, after every unknown token has been discovered.
	sort.SliceStable(r.entries, func(i, j int) bool { return r.entries[i].Name < r.entries[j].Name })
	r.byName = make(map[string]int, len(r.entries))
	for i := range r.entries {
		r.byName[r.entries[i].Name] = i
	}
	return r.byName[ck]
}

// canonicalUnitMap tolerates fixture maps whose keys differ in case from the
// UnitName/catalog key. Production CompileUnits already returns canonical keys;
// this normalization keeps independent linker use deterministic. If a fixture
// supplies case variants of one key, the lexicographically first key wins.
func canonicalUnitMap(units map[string]*UnitDef) map[string]*UnitDef {
	if len(units) == 0 {
		return nil
	}
	original := make([]string, 0, len(units))
	for key := range units {
		original = append(original, key)
	}
	sort.Strings(original)
	out := make(map[string]*UnitDef, len(units))
	for _, key := range original {
		ck := CanonicalKey(key)
		if _, exists := out[ck]; !exists {
			out[ck] = units[key]
		}
	}
	return out
}
