package content

import "testing"

// TestFourSectionNamesForceNoDrawUnderGray locks the name-based forcing: the
// parser ORs `nodrawundergray` on for the four wall and fortification section
// names, case-insensitively, whatever the section authored
// [02 §5 "Four section names force nodrawundergray"][05 R-FEAT-01 §1]. No stock
// section authors the key, so these names are the only producer of the flag on
// those records, and the renderer's placer-or-LOS draw gate depends on it.
func TestFourSectionNamesForceNoDrawUnderGray(t *testing.T) {
	forced := []string{
		"DragonsTeeth", "DragonsTeeth_Core", "Fortification", "Fortification_Core",
		// The comparison folds case.
		"dragonsteeth_core", "FORTIFICATION",
	}
	for _, name := range forced {
		// No authored key at all.
		f := compileFeatureSection(mustParseTDF(t, "[SEC]{}").Root.Section("SEC"), name, Provenance{})
		if !f.NoDrawUnderGray {
			t.Fatalf("section %q without the key compiled NoDrawUnderGray=false, want forced on [05 R-FEAT-01 §1]", name)
		}
		// An authored zero does not win: the bit is ORed on after the store.
		f0 := compileFeatureSection(mustParseTDF(t, "[SEC]{nodrawundergray=0;}").Root.Section("SEC"), name, Provenance{})
		if !f0.NoDrawUnderGray {
			t.Fatalf("section %q with nodrawundergray=0 compiled false, want the forced bit to win [02 §5]", name)
		}
	}
	// A different name keeps the authored value — the forcing is by name only,
	// not by a shared prefix or by footprint.
	for _, name := range []string{"Tree1", "DragonsTeeth_Corex", "Fort"} {
		f := compileFeatureSection(mustParseTDF(t, "[SEC]{}").Root.Section("SEC"), name, Provenance{})
		if f.NoDrawUnderGray {
			t.Fatalf("section %q without the key compiled NoDrawUnderGray=true, want clear", name)
		}
	}
	// The forced bit is part of the definition's identity exactly as an
	// authored bit is: the forced record hashes like one that authored the key.
	authored := compileFeatureSection(mustParseTDF(t, "[SEC]{nodrawundergray=1;}").Root.Section("SEC"), "Fortification", Provenance{})
	unauthored := compileFeatureSection(mustParseTDF(t, "[SEC]{}").Root.Section("SEC"), "Fortification", Provenance{})
	if authored.Hash != unauthored.Hash {
		t.Fatalf("forced and authored nodrawundergray hash differently (%s vs %s); the flag must reach the hash the same way [02 §5] C12",
			authored.Hash, unauthored.Hash)
	}
}
