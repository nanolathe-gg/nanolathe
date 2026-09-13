package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// Stock factories author CanMove. The visual yard classification must follow
// the structure-builder arm instead of mistaking that key for mobile class
// [04 R-FAC-02 §1].
func TestPublishFactoryClassificationUsesStructureBuilder(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		bm                     uint8
		builder, canMove, want bool
	}{
		{"factory with CanMove", 0, true, true, true},
		{"factory without CanMove", 0, true, false, true},
		{"mobile constructor", 1, true, true, false},
		{"ordinary structure", 0, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newSessionFixtureWorld(2, nil)
			def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "subject"}, MaxDamage: 1, BMCode: tc.bm, Builder: tc.builder, CanMove: tc.canMove}
			if _, err := w.Create(def, 0, 0, 0, 0); err != nil {
				t.Fatal(err)
			}
			s := &Session{Snapshot: frame.NewBuffer(), Units: w, LocalOwner: 0}
			s.publishSnapshot(1)
			if got := s.Snapshot.Current().Units[0].IsFactory; got != tc.want {
				t.Fatalf("IsFactory=%v, want %v", got, tc.want)
			}
		})
	}
}
