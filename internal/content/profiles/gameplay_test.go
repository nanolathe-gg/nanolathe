package profiles

import (
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"testing"
)

func TestProfileCommunityDefaults(t *testing.T) {
	retail, err := Lookup("retail")
	if err != nil {
		t.Fatal(err)
	}
	got, err := community.Resolve(false, retail.GameplaySources()...)
	want, _ := community.Table("prota")
	if err != nil || got != want {
		t.Fatalf("retail uses mainline, not historical limits: %+v, %v", got, err)
	}
	prota, err := Lookup("prota")
	if err != nil {
		t.Fatal(err)
	}
	got, err = community.Resolve(false, prota.GameplaySources()...)
	if err != nil || got.UnitLimit != prota.Limits.UnitLimit || got.PathStepAllowance != prota.Limits.SearchEntries {
		t.Fatalf("legacy mod parameters: %+v, %v", got, err)
	}
	limit := 700
	prota.Gameplay.UnitLimit = &limit
	got, err = community.Resolve(false, prota.GameplaySources()...)
	if err != nil || got.UnitLimit != limit {
		t.Fatalf("explicit profile field wins: %+v, %v", got, err)
	}
}
