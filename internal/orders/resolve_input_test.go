package orders

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"testing"
)

// The hostile rejects precede variant choice and contextual delegation is
// final in both interface modes [04 R-ORD-02 §1].
func TestResolveHostileWeaponClassGates(t *testing.T) {
	for _, tc := range []struct {
		name                                string
		aa, water0, water1, enabled1, hover bool
		mode                                uint8
		y, top                              int32
		want                                bool
	}{
		{name: "ordinary surfaced", y: 20, want: true},
		{name: "AA rejects grounded", aa: true, y: 20},
		{name: "AA admits committed airborne", aa: true, mode: 2, y: 20, want: true},
		{name: "submerged needs water", y: 19},
		{name: "whole model top reaches surface", y: 19, top: 1, want: true},
		{name: "full model top exceeds byte", y: -237, top: 257, want: true},
		{name: "primary water admits submerged", water0: true, y: 19, want: true},
		{name: "secondary disabled rejects submerged", water1: true, y: 19},
		{name: "secondary enabled admits submerged", water1: true, enabled1: true, y: 19, want: true},
		{name: "hover primary water rejects equality", hover: true, water0: true, y: 20},
		{name: "hover secondary water rejects equality", hover: true, water1: true, enabled1: true, y: 20},
		{name: "hover disabled secondary permits surface", hover: true, water1: true, y: 20, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := mkUnit(1, 0, "", 100, 100, true, 0, mkDef(nil))
			a.Def.CanHover = tc.hover
			a.InstallWeapon(0, &content.WeaponDef{ToAirWeapon: tc.aa, WaterWeapon: tc.water0})
			a.InstallWeapon(1, &content.WeaponDef{WaterWeapon: tc.water1})
			if tc.enabled1 {
				a.Slots[1].Flags |= units.SlotFlagEnabled
			} else {
				a.Slots[1].Flags &^= units.SlotFlagEnabled
			}
			setTestSeaLevel(a, 20)
			target := mkUnit(2, 1, "", 100, 100, true, 0, mkDef(nil))
			target.Y = numeric.Fixed(tc.y<<16 | 65535)
			target.Def.ModelTop = tc.top & 255
			target.Def.ModelTopFixed = tc.top << 16
			target.Move.ModeMirror = tc.mode
			target.Move.Mode = 1 // pending mode cannot replace the committed word
			for _, variant := range []int{InterfaceTypeLeftClick, InterfaceTypeRightClick} {
				pos := &ResolvePos{InterfaceType: variant, HasFeature: true}
				explicit := Resolve(3, a, target, pos)
				contextual := Resolve(1, a, target, pos)
				if (explicit != 0) != tc.want || contextual != explicit {
					t.Fatalf("variant %d explicit=%d contextual=%d want admitted=%v", variant, explicit, contextual, tc.want)
				}
			}
		})
	}
}

func TestResolveFeatureCapabilitiesAndSessionVariant(t *testing.T) {
	a := mkUnit(1, 0, "", 100, 100, true, 0, mkDef(nil))
	a.Def.CanResurrect = true
	a.Def.CanAttack = false
	for _, code := range []int{1, 12} {
		if got := Resolve(code, a, nil, &ResolvePos{HasFeature: true}); got != Lookup("Resurrect") {
			t.Fatalf("code %d mapped reclaimable noncorpse=%d", code, got)
		}
	}
	if got := Resolve(12, a, nil, &ResolvePos{}); got != 0 {
		t.Fatal("empty ground admitted reclaim")
	}
	a.Def.CanReclamate = false
	if got := Resolve(12, a, nil, &ResolvePos{HasFeature: true}); got != 0 {
		t.Fatal("resurrect-only actor admitted explicit reclaim")
	}
	a.Def.CanReclamate = true
	friendly := mkUnit(2, 0, "", 50, 100, true, 0, mkDef(nil))
	friendly.Flags |= units.ClassifierEligibleStatus
	if got := Resolve(1, a, friendly, &ResolvePos{InterfaceType: InterfaceTypeRightClick}); got != Lookup("RepairUnit") {
		t.Fatalf("right-click variant=%d", got)
	}
	if got := Resolve(1, a, friendly, &ResolvePos{}); got != 0 {
		t.Fatalf("default variant contaminated by other call=%d", got)
	}
	a.Def.CanAttack = true
	if got := Resolve(3, a, friendly, nil); got != Lookup("Suppress") {
		t.Fatalf("friendly armed attack=%d", got)
	}
}
