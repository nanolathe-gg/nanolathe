package content

import (
	"fmt"
	"sort"
	"strings"
)

// CategoryMaskWords is the fixed registry width from the retail category
// compiler: sixteen 32-bit words, indexed by unit-definition ID [R-P0-03].
const CategoryMaskWords = 16

// CategoryMask is an immutable-by-convention 512-bit unit-definition
// membership set. IDs use word=id>>5 and bit=id&31 [R-P0-03].
type CategoryMask struct {
	Words [CategoryMaskWords]uint32
}

// Contains reports whether id is a member. ID zero is reserved for the
// catalog's null definition sentinel and is never assigned by the compiler.
func (m CategoryMask) Contains(id uint32) bool {
	if id >= CategoryMaskWords*32 {
		return false
	}
	return m.Words[id>>5]&(uint32(1)<<(id&31)) != 0
}

// IsZero reports whether no unit-definition IDs are members.
func (m CategoryMask) IsZero() bool {
	for _, word := range m.Words {
		if word != 0 {
			return false
		}
	}
	return true
}

// Or returns the union of two masks without mutating either input.
func (m CategoryMask) Or(other CategoryMask) CategoryMask {
	for i := range m.Words {
		m.Words[i] |= other.Words[i]
	}
	return m
}

// Intersects reports whether two membership sets share a unit-definition ID.
func (m CategoryMask) Intersects(other CategoryMask) bool {
	for i := range m.Words {
		if m.Words[i]&other.Words[i] != 0 {
			return true
		}
	}
	return false
}

func (m *CategoryMask) set(id uint32) error {
	if id == 0 || id >= CategoryMaskWords*32 {
		return fmt.Errorf("category: unit definition ID %d outside usable 1..511 domain", id)
	}
	m.Words[id>>5] |= uint32(1) << (id & 31)
	return nil
}

// CategoryEntry is one case-insensitive registry row. Membership is a value,
// so callers cannot mutate the compiler's storage through a lookup result.
type CategoryEntry struct {
	Name       string
	Membership CategoryMask
}

// CategoryRegistry is the sorted, case-insensitive category token registry
// [R-P0-03]. The empty-name sentinel is retained privately; it has no invented
// user-visible spelling [R-P0-03, "Implementation guidance"].
// TODO(question): the retail symbol's exact internal sentinel label and any
// consumers beyond category compilation remain unresolved [R-P0-03].
type CategoryRegistry struct {
	entries  []CategoryEntry
	byName   map[string]int
	sentinel CategoryMask
}

// Entries returns user-visible registry entries in case-insensitive sorted
// order. The mandatory empty/sentinel row is intentionally not named here.
func (r *CategoryRegistry) Entries() []CategoryEntry {
	if r == nil {
		return nil
	}
	out := make([]CategoryEntry, len(r.entries))
	copy(out, r.entries)
	return out
}

// Lookup returns a copy of the membership set for name. Unknown names are
// absent from a completed registry; linking creates unknown authored names.
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

// SentinelMembership returns the mandatory internal empty/sentinel membership
// without assigning it a user-visible token [R-P0-03].
func (r *CategoryRegistry) SentinelMembership() CategoryMask {
	if r == nil {
		return CategoryMask{}
	}
	return r.sentinel
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
// fields into one immutable registry. It is useful independently of Catalog
// compilation for deterministic fixture tests and later catalog extensions.
func CompileCategories(units map[string]*UnitDef) (*CategoryRegistry, error) {
	units = canonicalUnitMap(units)
	r := &CategoryRegistry{byName: make(map[string]int)}
	keys := sortedUnitKeys(units)
	// UnitDefIndex is 1-based (zero is its null sentinel). Therefore ID 511 is
	// the last usable definition ID in the 512-bit category domain [02 §5]; an
	// attempted ID 512 must be diagnosed rather than aliased/truncated [R-P0-03].
	if len(keys) >= CategoryMaskWords*32 {
		return nil, fmt.Errorf("category: %d unit definitions exceed 511 usable IDs in 512-bit domain", len(keys))
	}

	for i, key := range keys {
		u := units[key]
		if u == nil {
			continue
		}
		id := uint32(i + 1)
		u.UnitDefID = id
		var ownMask CategoryMask
		if err := ownMask.set(id); err != nil {
			return nil, err
		}
		u.UnitMask = ownMask
		for _, token := range strings.Fields(u.Category) {
			idx := r.ensure(token)
			if err := r.entries[idx].Membership.set(id); err != nil {
				return nil, err
			}
		}
		// The recovered compiler always adds the current ID to its internal
		// empty/sentinel entry, including missing/empty authored categories.
		if err := r.sentinel.set(id); err != nil {
			return nil, err
		}
	}

	// Unknown target names are retained as empty registry rows, and can be
	// populated by a later unit in the same link pass [R-P0-03].
	for _, key := range keys {
		u := units[key]
		if u == nil {
			continue
		}
		for _, target := range []string{u.BadTargetCategoryWPRI, u.BadTargetCategoryWSEC, u.BadTargetCategoryWSPE, u.NoChaseCategory} {
			if strings.TrimSpace(target) != "" && !hasUnit(units, target) {
				r.ensure(target)
			}
		}
	}

	for _, key := range keys {
		u := units[key]
		if u == nil {
			continue
		}
		u.BadTargetCategoryWPRIMask = r.resolveTarget(units, u.BadTargetCategoryWPRI)
		u.BadTargetCategoryWSECMask = r.resolveTarget(units, u.BadTargetCategoryWSEC)
		u.BadTargetCategoryWSPEMask = r.resolveTarget(units, u.BadTargetCategoryWSPE)
		u.NoChaseCategoryMask = r.resolveTarget(units, u.NoChaseCategory)
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

func (r *CategoryRegistry) resolveTarget(units map[string]*UnitDef, name string) CategoryMask {
	if u, ok := units[CanonicalKey(name)]; ok && u != nil {
		var m CategoryMask
		_ = m.set(u.UnitDefID)
		return m
	}
	m, _ := r.Lookup(name)
	return m
}

func hasUnit(units map[string]*UnitDef, name string) bool {
	_, ok := units[CanonicalKey(name)]
	return ok
}

func sortedUnitKeys(units map[string]*UnitDef) []string {
	keys := make([]string, 0, len(units))
	for k := range units {
		keys = append(keys, CanonicalKey(k))
	}
	sort.Strings(keys)
	return keys
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
