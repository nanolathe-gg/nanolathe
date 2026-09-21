package content

import "testing"

// soundOrdinalFixture authors three categories in a deliberate file order:
// the ordinal domain is that order, not the sorted or canonical-key order the
// name map keeps [02 §5 "Cross-reference failure policy"][02 R-CAT-01 §5].
const soundOrdinalFixture = `[ZETA_FIRST]
{
	select1=ZetaSel;
}
[ALPHA_SECOND]
{
	select1=AlphaSel;
}
[MID_THIRD]
{
	select1=MidSel;
}
`

func ordinalTestCatalog(t *testing.T) *Catalog {
	t.Helper()
	fs := newFixtureFS(t, fixtureFile{path: "gamedata/sound.tdf", data: soundOrdinalFixture})
	cats, order, err := compileSoundCategoriesOrdered(fs)
	if err != nil {
		t.Fatalf("compileSoundCategoriesOrdered: %v", err)
	}
	if len(order) != 3 {
		t.Fatalf("category order length %d want 3", len(order))
	}
	// File order, not sorted order: the map alone could not tell them apart.
	if order[0].Name != "ZETA_FIRST" || order[1].Name != "ALPHA_SECOND" || order[2].Name != "MID_THIRD" {
		t.Fatalf("category order %s/%s/%s want ZETA_FIRST/ALPHA_SECOND/MID_THIRD [02 R-CAT-01 §5]",
			order[0].Name, order[1].Name, order[2].Name)
	}
	return &Catalog{Sounds: cats, SoundCategoryOrder: order}
}

// TestSoundCategoryOrdinalFallback locks the `soundcategory` failure policy:
// absent → index 0, a name miss → the C-runtime decimal conversion of the
// authored text used as an ordinal, so non-numeric text is 0 and again the
// first authored category [02 §5][02 R-CAT-01 §5]. There is no placeholder
// record, so none of these resolve to nothing.
func TestSoundCategoryOrdinalFallback(t *testing.T) {
	c := ordinalTestCatalog(t)
	cases := []struct {
		authored string
		want     string
	}{
		// Eleven stock definitions author exactly this shape of miss.
		{"NONE", "ZETA_FIRST"},
		{"CORE_KBOT", "ZETA_FIRST"},
		{"", "ZETA_FIRST"},
		{"1", "ALPHA_SECOND"},
		{"2", "MID_THIRD"},
		// The conversion is the ordinary CRT one: leading space, sign and a
		// digit prefix with trailing junk ignored.
		{"  1junk", "ALPHA_SECOND"},
		{"+2", "MID_THIRD"},
		// A name match wins over any numeric reading of the same text.
		{"alpha_second", "ALPHA_SECOND"},
	}
	for _, tc := range cases {
		got := c.ResolveSoundCategory(tc.authored)
		if got == nil {
			t.Fatalf("soundcategory %q resolved to no category, want %s [02 §5]", tc.authored, tc.want)
		}
		if got.Name != tc.want {
			t.Fatalf("soundcategory %q resolved to %s, want %s [02 R-CAT-01 §5]", tc.authored, got.Name, tc.want)
		}
	}
	// Out of range is the documented open question: retail stores the ordinal
	// unbounded and indexes past the loaded records. Nanolathe resolves nothing
	// rather than inventing a wrap or a clamp (TODO(question) at the resolver).
	if got := c.ResolveSoundCategory("9"); got != nil {
		t.Fatalf("out-of-range ordinal resolved to %s, want no category until the retail behavior is settled", got.Name)
	}
}
