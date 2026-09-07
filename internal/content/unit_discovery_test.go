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
