package content

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
)

// TestParsedDefinitionCarriesTheUnlimitedLimitDefault locks [05 R-SHARE-01 §9]:
// the definition parser stores -1 (unlimited) into the per-definition limit
// field of every definition it parses, and marks the field written so a reader
// can tell that -1 from a hand-built fixture's Go zero value. The limit is not
// an FBI key, so an authored `limit` line changes nothing.
func TestParsedDefinitionCarriesTheUnlimitedLimitDefault(t *testing.T) {
	compile := func(body string) *UnitDef {
		t.Helper()
		doc, err := formats.ParseTDF([]byte("[UNITINFO]{\n" + body + "\n}"))
		if err != nil {
			t.Fatal(err)
		}
		section := doc.Root.Section("UNITINFO")
		if section == nil {
			t.Fatal("missing UNITINFO")
		}
		return compileUnitSection(section, "units/test.fbi", "", Provenance{})
	}

	plain := compile("UnitName=LIMITFIX;\nName=Limit Fixture;\nObjectName=limitfix;\nSide=ARM;")
	if plain == nil {
		t.Fatal("compileUnitSection returned no definition")
	}
	if !plain.LimitEnabled {
		t.Fatal("the parser writes the limit field of every definition it parses")
	}
	if plain.Limit != -1 {
		t.Fatalf("limit = %d, want the unlimited -1", plain.Limit)
	}

	authored := compile("UnitName=LIMITFIX2;\nName=Limit Fixture 2;\nObjectName=limitfix2;\nSide=ARM;\nlimit=0;")
	if authored == nil {
		t.Fatal("compileUnitSection returned no definition")
	}
	if authored.Limit != -1 {
		t.Fatalf("an authored limit key is not read: limit = %d, want -1", authored.Limit)
	}

	// A definition built without the parser is still distinguishable: nothing
	// wrote its limit field.
	var fixture UnitDef
	if fixture.LimitEnabled {
		t.Fatal("a hand-built definition must not claim a written limit field")
	}
}
