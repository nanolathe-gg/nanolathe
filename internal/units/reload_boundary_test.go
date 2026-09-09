package units

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// TestSlotCanFireRequiresAnExactReloadZero keeps the lightweight unit-side
// readiness helper aligned with the signed slot word restored from saves
// [06 §3.3][06 §4.2].
func TestSlotCanFireRequiresAnExactReloadZero(t *testing.T) {
	slot := &Slot{Weapon: &content.WeaponDef{}}
	for _, reload := range []int32{-32768, -1, 0, 1} {
		slot.Reload = reload
		if got, want := slot.CanFire(), reload == 0; got != want {
			t.Fatalf("reload %d CanFire=%v, want %v", reload, got, want)
		}
	}
}
