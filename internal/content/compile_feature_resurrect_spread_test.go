package content

import "testing"

// TestFeatureResurrectionSpreadKeyDoesNotExist locks WU-19-143's finding:
// retail's feature parser has no "resurrection spread" key at all. The
// guess ladder this build used to carry (`resurrectspread`, then
// `jitterspread`, then a bare `spread`) is retired — none of the three
// spellings are consumed by the compiler, all three fall through to the
// inert Unknown bag like any other unrecognised key, and the byte that
// actually bounds the resurrection order's one simulation-RNG draw is the
// feature's ordinary, already-typed `height` field
// [05 R-FEAT-01 §1][05 "Resurrection", "Established — cost and randomness"].
func TestFeatureResurrectionSpreadKeyDoesNotExist(t *testing.T) {
	body := `[armflash_heap]
{
	height=42;
	resurrectspread=7;
	jitterspread=9;
	spread=11;
}
`
	doc := mustParseTDF(t, body)
	sec := doc.Root.Sections()[0]
	fd := compileFeatureSection(sec, "armflash_heap", Provenance{})

	if fd.Height != 42 {
		t.Fatalf("Height = %d, want 42 (the byte retail actually reads and reuses for the resurrection approach draw)", fd.Height)
	}

	// None of the three guessed spellings has a typed reader; all three must
	// land in Unknown, verbatim, like any other inert key [02 §5] C14.
	for _, want := range []string{"resurrectspread", "jitterspread", "spread"} {
		if _, ok := fd.Unknown[want]; !ok {
			t.Fatalf("expected %q to be retained as an inert Unknown key, got Unknown=%v", want, fd.Unknown)
		}
	}

	// knownFeatureKeys must not carry any of the three guesses — a positive
	// hit here would silently start consuming a key retail never reads.
	for _, guess := range []string{"resurrectspread", "jitterspread", "spread"} {
		if _, ok := knownFeatureKeys[guess]; ok {
			t.Fatalf("knownFeatureKeys must not contain the retired guess %q", guess)
		}
	}

	// The unrelated, already-established spreadchance key must be untouched
	// by the guesses above (no accidental cross-consumption).
	if fd.SpreadChance != 0 {
		t.Fatalf("SpreadChance = %d, want 0 (unauthored)", fd.SpreadChance)
	}
}
