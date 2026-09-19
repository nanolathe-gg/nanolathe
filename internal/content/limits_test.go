package content

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content/profiles"
)

// fourDigits keeps fixture names sorted in the same order as their index, so
// the definition ID a name receives is predictable.
func fourDigits(i int) string {
	return fmt.Sprintf("%04d", i)
}

// compileCategoriesForTest links a fixture definition set over an explicit
// domain, the same way CompileWithOptions links a mounted one.
func compileCategoriesForTest(units map[string]*UnitDef, limits Limits) (*CategoryRegistry, error) {
	units = canonicalUnitMap(units)
	return compileCategoryRecords(unitMapRecords(units), units, limits)
}

// definitionSet builds n distinct definitions sharing one category token, so
// every one of them is a member of that token's membership set.
func definitionSet(n int, category string) map[string]*UnitDef {
	units := make(map[string]*UnitDef, n)
	for i := 0; i < n; i++ {
		name := "u" + fourDigits(i)
		units[name] = categoryUnit(name, category)
	}
	return units
}

// TestDefinitionDomainFollowsTheCompileLimits is the E2 contract: the same
// authored set that a retail compile refuses compiles under a content
// profile's raised domain, and the retail diagnostic is unchanged.
func TestDefinitionDomainFollowsTheCompileLimits(t *testing.T) {
	units := definitionSet(600, "boundary")

	_, err := compileCategoriesForTest(units, RetailLimits())
	if err == nil {
		t.Fatal("600 definitions must not fit the retail 512-bit domain")
	}
	const wantRetail = "category: 600 unit definitions exceed 511 usable IDs in 512-bit domain"
	if err.Error() != wantRetail {
		t.Fatalf("retail diagnostic = %q, want %q", err, wantRetail)
	}

	raised := Limits{Units: 16000, Weapons: 16000}
	r, err := compileCategoriesForTest(units, raised)
	if err != nil {
		t.Fatalf("600 definitions under a raised domain: %v", err)
	}
	m, ok := r.Lookup("boundary")
	if !ok {
		t.Fatal("authored category token missing from the registry")
	}
	// IDs are one-based and assigned in sorted name order, so the set spans
	// 1..600 — both sides of the retail domain's last usable ID.
	for _, id := range []uint32{1, 511, 512, 600} {
		if !m.Contains(id) {
			t.Fatalf("definition ID %d is not a member of the authored category", id)
		}
	}
	if m.Contains(0) || m.Contains(601) {
		t.Fatal("membership reaches past the compiled definitions")
	}
	last := units["u0599"]
	if last.UnitDefID != 600 {
		t.Fatalf("last definition ID = %d, want 600", last.UnitDefID)
	}
	if !last.DefinitionMask().Intersects(m) || !m.Intersects(last.DefinitionMask()) {
		t.Fatal("a definition above the retail domain does not intersect its own category")
	}
	if last.DefinitionMask().Intersects(units["u0000"].DefinitionMask()) {
		t.Fatal("two definitions share an identity bit")
	}
}

// TestDefinitionDigestIsIndependentOfTheCompiledDomain locks the identity rule
// the raised domain rests on: widening the domain does not move the digest of
// a definition whose members are unchanged [02 §5] C12.
func TestDefinitionDigestIsIndependentOfTheCompiledDomain(t *testing.T) {
	retailUnits := definitionSet(4, "alpha")
	if _, err := compileCategoriesForTest(retailUnits, RetailLimits()); err != nil {
		t.Fatal(err)
	}
	raisedUnits := definitionSet(4, "alpha")
	if _, err := compileCategoriesForTest(raisedUnits, Limits{Units: 16000}); err != nil {
		t.Fatal(err)
	}
	for key, u := range retailUnits {
		if raisedUnits[key].Hash != u.Hash {
			t.Fatalf("definition %s digest moved with the domain: %s vs %s", key, raisedUnits[key].Hash, u.Hash)
		}
	}
}

// TestLimitsFromProfileKeepsRetailForUnsetCounts records that a profile which
// names no table size is a partial override, not an empty domain.
func TestLimitsFromProfileKeepsRetailForUnsetCounts(t *testing.T) {
	if got := LimitsFromProfile(profiles.Limits{}); got != RetailLimits() {
		t.Fatalf("empty profile limits = %+v, want the retail baseline %+v", got, RetailLimits())
	}
	got := LimitsFromProfile(profiles.Limits{Units: 16000})
	if got.Units != 16000 || got.Weapons != RetailWeaponSlots {
		t.Fatalf("partial profile limits = %+v, want a raised domain and the retail weapon table", got)
	}
}

// TestDefinitionDomainAboveTheRuntimeIdentityIsRefused keeps a profile from
// asking for definition IDs the 16-bit runtime identity cannot carry.
func TestDefinitionDomainAboveTheRuntimeIdentityIsRefused(t *testing.T) {
	_, err := compileCategoriesForTest(definitionSet(1, ""), Limits{Units: MaxDefinitionDomain + 1})
	if err == nil {
		t.Fatal("a domain past the runtime identity width must be refused")
	}
	if !strings.Contains(err.Error(), "nanolathe: content profile asks for a larger unit-definition domain") {
		t.Fatalf("diagnostic = %q, want the content-profile domain refusal", err)
	}
}

// BenchmarkCategoryMask measures the two membership operations on the target
// admission path at the retail domain, where a regression would cost every
// fire attempt [06 §3.1].
func BenchmarkCategoryMask(b *testing.B) {
	units := definitionSet(511, "all-of-them")
	r, err := compileCategoriesForTest(units, RetailLimits())
	if err != nil {
		b.Fatal(err)
	}
	wide, _ := r.Lookup("all-of-them")
	// The last definition's own mask is the widest single-bit mask the retail
	// domain produces, so this is the worst case for a definition-versus-
	// category test.
	narrow := units["u0510"].DefinitionMask()
	empty := MaskForID(0)
	b.Run("Intersects", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if !narrow.Intersects(wide) {
				b.Fatal("expected membership")
			}
		}
	})
	b.Run("IntersectsMiss", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if narrow.Intersects(empty) {
				b.Fatal("unexpected membership")
			}
		}
	})
	b.Run("Contains", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if !wide.Contains(uint32(i%511) + 1) {
				b.Fatal("expected membership")
			}
		}
	})
	b.Run("Or", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if wide.Or(narrow).IsZero() {
				b.Fatal("expected members")
			}
		}
	})
}

// TestWeaponRecordTableFollowsTheCompileLimits locks the other count the
// compile enforces: an ID past the end of the record table addresses no
// record, so it is refused under the retail table and admitted under a profile
// that raises it [02 "Weapon record"].
func TestWeaponRecordTableFollowsTheCompileLimits(t *testing.T) {
	fs := newFixtureFS(t, fixtureFile{
		path: "weapons/over.tdf",
		data: "[BIGSLOT]\n{\nID=256;\nname=Big Slot;\n}\n",
	})
	_, _, err := CompileWeaponsWithDuplicates(fs, RetailLimits())
	if err == nil {
		t.Fatal("weapon ID 256 must not fit the 256-record retail table")
	}
	const want = "content: weapon \"bigslot\" ID 256 is outside the 256-record weapon table"
	if err.Error() != want {
		t.Fatalf("diagnostic = %q, want %q", err, want)
	}
	weapons, _, err := CompileWeaponsWithDuplicates(fs, Limits{Weapons: 16000})
	if err != nil {
		t.Fatalf("weapon ID 256 under a raised table: %v", err)
	}
	if wd, ok := weapons["bigslot"]; !ok || wd.ID != 256 {
		t.Fatalf("raised table did not admit the section: %+v", weapons)
	}
}
