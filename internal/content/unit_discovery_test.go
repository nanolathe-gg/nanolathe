package content

import "testing"

func TestUnitObjectNameDefaultsOnlyWhenAbsent(t *testing.T) {
	absent := compileUnitSection(mustParseTDF(t, "[UNITINFO]{ unitname=defaultmodel; }").Root.Sections()[0], "units/default.fbi", "", Provenance{})
	if absent.ObjectName != "defaultmodel" {
		t.Fatalf("absent objectname = %q, want unitname", absent.ObjectName)
	}
	empty := compileUnitSection(mustParseTDF(t, "[UNITINFO]{ unitname=emptymodel; objectname=; }").Root.Sections()[0], "units/empty.fbi", "", Provenance{})
	if empty.ObjectName != "" {
		t.Fatalf("explicit empty objectname = %q, want empty", empty.ObjectName)
	}
}

func TestUnitUnknownKeyOrderPreservesDistinctHighBytes(t *testing.T) {
	u := &UnitDef{Unknown: map[string]string{"\xc0": "one", "\xe0": "two"}}
	keys := u.UnknownKeysSorted()
	if len(keys) != 2 || keys[0] == keys[1] || CanonicalKey("\xc0") == CanonicalKey("\xe0") {
		t.Fatalf("high-byte authored keys collapsed: %q", keys)
	}
}

func TestUnitEmptyNameDoesNotUseDiscoveredFilename(t *testing.T) {
	// Absent and authored-empty identities remain empty; only an absent
	// Objectname defaults to that stored identity [02 R-CAT-01 §5].
	for _, text := range []string{
		"[UNITINFO]{}", "[UNITINFO]{unitname=;}",
		"[UNITINFO]{unitname=; objectname=explicit;}",
	} {
		section := mustParseTDF(t, text).Root.Sections()[0]
		u := compileUnitSection(section, "units/discovered.fbi", "", Provenance{})
		if u.UnitName != "" || u.CanonicalKey != "" {
			t.Fatalf("%s: identity = %q / %q, want empty", text, u.UnitName, u.CanonicalKey)
		}
		wantModel, _ := section.StringValue("objectname", "")
		if u.ObjectName != wantModel {
			t.Fatalf("%s: model = %q, want %q", text, u.ObjectName, wantModel)
		}
	}
}
