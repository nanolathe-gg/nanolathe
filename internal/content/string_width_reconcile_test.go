package content

import (
	"fmt"
	"strings"
	"testing"
)

func TestContentStringCopyBoundaries(t *testing.T) {
	ordinary := strings.Repeat("U", 32)
	unit := compileUnitSection(mustParseTDF(t, fmt.Sprintf(`[UNITINFO]
{
 unitname=width;
 objectname=%s;
}
`, ordinary)).Root.Sections()[0], "units/width.fbi", "", Provenance{})
	if len(unit.ObjectName) != 31 {
		t.Fatalf("unit object string length = %d, want 31", len(unit.ObjectName))
	}

	aliasName := strings.Repeat("A", 32)
	fs := newFixtureFS(t, fixtureFile{
		path: "gamedata/allsound.tdf",
		data: fmt.Sprintf(`[%s]
{
 sound=sample;
}
`, aliasName),
	})
	aliases, ordered, err := CompileSoundAliasesOrdered(fs)
	if err != nil {
		t.Fatalf("CompileSoundAliasesOrdered: %v", err)
	}
	if len(ordered) != 1 {
		t.Fatalf("sound alias count = %d, want 1", len(ordered))
	}
	alias, ok := aliases[CanonicalKey(aliasName)]
	if !ok || alias == nil {
		t.Fatalf("sound alias %q missing from lookup", aliasName)
	}
	if len(alias.Alias) != 32 {
		t.Fatalf("sound alias length = %d, want 32", len(alias.Alias))
	}
}
