package content

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
)

func TestBMCodePreservesStoredByteAndCanonicalIdentity(t *testing.T) {
	var hashes [3]string
	for _, tc := range []struct {
		input int
		want  uint8
	}{{0, 0}, {1, 1}, {2, 2}, {3, 3}, {-2, 254}, {256, 0}, {257, 1}} {
		doc, err := formats.ParseTDF([]byte(fmt.Sprintf("[UNITINFO] { unitname=byte; bmcode=%d; }", tc.input)))
		if err != nil {
			t.Fatal(err)
		}
		u := compileUnitSection(doc.Root.Section("UNITINFO"), "units/byte.fbi", "", Provenance{})
		if u.BMCode != tc.want {
			t.Fatalf("BMCode(%d)=%d, want %d", tc.input, u.BMCode, tc.want)
		}
		if tc.input >= 0 && tc.input <= 2 {
			hashes[tc.input] = fmt.Sprint(u.Hash)
		}
		if tc.input == 256 && fmt.Sprint(u.Hash) != hashes[0] {
			t.Fatal("byte-equivalent zero did not share canonical identity")
		}
		if tc.input == 257 && fmt.Sprint(u.Hash) != hashes[1] {
			t.Fatal("byte-equivalent one did not share canonical identity")
		}
	}
	if hashes[1] == hashes[2] {
		t.Fatal("distinct mover byte collapsed in definition hash")
	}
}
