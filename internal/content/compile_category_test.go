package content

import "testing"

func categoryUnit(name, category string) *UnitDef {
	return &UnitDef{
		DefinitionHeader:      DefinitionHeader{CanonicalKey: CanonicalKey(name)},
		UnitName:              name,
		Category:              category,
		BadTargetCategoryWPRI: "none",
		BadTargetCategoryWSEC: "none",
		BadTargetCategoryWSPE: "none",
		NoChaseCategory:       "none",
	}
}

func TestCompileCategoriesRegistryAndMasks(t *testing.T) {
	units := map[string]*UnitDef{
		"zulu":  categoryUnit("zulu", "ARM tank ARM"),
		"alpha": categoryUnit("alpha", "Tank Unknown"),
		"bravo": categoryUnit("bravo", "unknown later"),
	}
	units["zulu"].NoChaseCategory = "Later"
	units["alpha"].BadTargetCategoryWPRI = "zulu" // FBI fields use the category even when a unit has this name
	units["alpha"].BadTargetCategoryWSEC = "later"
	r, err := CompileCategories(units)
	if err != nil {
		t.Fatal(err)
	}
	wantNames := []string{"all", "arm", "later", "none", "tank", "unknown", "zulu"}
	gotNames := r.CategoryNames()
	if len(gotNames) != len(wantNames) {
		t.Fatalf("names = %v, want %v", gotNames, wantNames)
	}
	for i := range wantNames {
		if gotNames[i] != wantNames[i] {
			t.Fatalf("names = %v, want %v", gotNames, wantNames)
		}
	}
	// Stable IDs are one-based: alpha=1, bravo=2, zulu=3.
	arm, _ := r.Lookup("ArM")
	if !arm.Contains(3) || arm.Contains(1) || !arm.Contains(3) {
		t.Fatalf("arm membership = %#v", arm.Words)
	}
	tank, _ := r.Lookup("tank")
	if !tank.Contains(1) || !tank.Contains(3) {
		t.Fatalf("tank membership = %#v", tank.Words)
	}
	unknown, _ := r.Lookup("UNKNOWN")
	if !unknown.Contains(1) || !unknown.Contains(2) {
		t.Fatalf("unknown membership = %#v", unknown.Words)
	}
	if !r.SentinelMembership().Contains(1) || !r.SentinelMembership().Contains(3) {
		t.Fatalf("ALL membership = %#v", r.SentinelMembership().Words)
	}
	if r.SentinelMembership().Contains(0) || units["alpha"].DefinitionMask().Contains(0) {
		t.Fatal("null definition ID 0 must remain an unassigned sentinel")
	}
	none, _ := r.Lookup("none")
	if !none.IsZero() {
		t.Fatalf("none is an ordinary empty token in this fixture, got %#v", none.Words)
	}
	if !units["alpha"].BadTargetCategoryWPRIMask.IsZero() {
		t.Fatalf("unit name gained category membership = %#v", units["alpha"].BadTargetCategoryWPRIMask.Words)
	}
	if !units["alpha"].BadTargetCategoryWSECMask.Contains(2) {
		t.Fatalf("category target mask = %#v", units["alpha"].BadTargetCategoryWSECMask.Words)
	}
}

func TestCompileCategoriesWordBoundaryAndOverflow(t *testing.T) {
	units := make(map[string]*UnitDef, 32)
	for i := 0; i < 32; i++ {
		name := "u" + twoDigits(i)
		units[name] = categoryUnit(name, "boundary")
	}
	r, err := CompileCategories(units)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := r.Lookup("BOUNDARY")
	if m.Words[0]&(1<<30) == 0 || m.Words[1]&1 == 0 {
		t.Fatalf("word boundary mask = %#v", m.Words)
	}
	tooMany := make(map[string]*UnitDef, 512)
	for i := 0; i < 512; i++ {
		name := "u" + threeDigits(i)
		tooMany[name] = categoryUnit(name, "")
	}
	if _, err := CompileCategories(tooMany); err == nil {
		t.Fatal("512 definitions should reject ID 512 rather than truncate or alias")
	}
}

func twoDigits(i int) string {
	if i < 10 {
		return "0" + string(rune('0'+i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}

func threeDigits(i int) string {
	return string(rune('0'+i/100)) + string(rune('0'+(i/10)%10)) + string(rune('0'+i%10))
}

func TestCatalogCategoryCloneAndDigest(t *testing.T) {
	units := map[string]*UnitDef{
		"unit": categoryUnit("unit", "alpha"),
	}
	r, err := CompileCategories(units)
	if err != nil {
		t.Fatal(err)
	}
	c := &Catalog{Units: units, Categories: r}
	for key, u := range c.Units {
		c.Units[key].Hash = HashDefinition(writeUnitCanonical(u))
	}
	c.Hash = catalogHash(c)
	clone := c.Clone()
	if clone.Categories == c.Categories || clone.Units["unit"] == c.Units["unit"] {
		t.Fatal("clone shares category or unit pointers")
	}
	if clone.Hash != c.Hash || clone.Categories.SentinelMembership() != c.Categories.SentinelMembership() || clone.Units["unit"].DefinitionMask() != c.Units["unit"].DefinitionMask() {
		t.Fatal("clone changed immutable category digest")
	}
}

func TestFBITargetCategoryDoesNotPreferUnitName(t *testing.T) {
	units := map[string]*UnitDef{
		"":      categoryUnit("", ""),
		"alpha": categoryUnit("alpha", "zulu"),
		"zulu":  categoryUnit("zulu", "other"),
	}
	u := units["alpha"]
	u.BadTargetCategoryWPRI = "Zulu"
	u.BadTargetCategoryWSEC = "Zulu"
	u.BadTargetCategoryWSPE = "Zulu"
	u.NoChaseCategory = "Zulu"
	r, err := CompileCategories(units)
	if err != nil {
		t.Fatal(err)
	}
	for _, mask := range []CategoryMask{u.BadTargetCategoryWPRIMask, u.BadTargetCategoryWSECMask, u.BadTargetCategoryWSPEMask, u.NoChaseCategoryMask} {
		if !mask.Contains(u.UnitDefID) || mask.Contains(units["zulu"].UnitDefID) {
			t.Fatal("FBI target field preferred a unit name over registry membership")
		}
	}
	c := &Catalog{Units: units, Categories: r}
	direct, ok := c.ResolveCategoryMask("zulu")
	if !ok || !direct.Contains(units["zulu"].UnitDefID) || direct.Contains(u.UnitDefID) {
		t.Fatal("general mask API lost its separate direct-unit precedence")
	}

	u.NoChaseCategory = ""
	r, err = CompileCategories(units)
	if err != nil {
		t.Fatal(err)
	}
	empty, exists := r.Lookup("")
	if !exists || !empty.IsZero() || !u.NoChaseCategoryMask.IsZero() {
		t.Fatal("empty FBI target did not retain the empty-name category entry")
	}
}
