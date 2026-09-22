package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

func TestCommunityReloadSelectsLargestTaggedAuthoredTime(t *testing.T) {
	u := &frame.UnitView{CommunityHUD: frame.UnitHUDView{Weapons: [3]frame.UnitWeaponHUDView{
		{Reload: 10, ReloadTime: 30, Tagged: true},
		{Reload: 20, ReloadTime: 60, Tagged: true},
		{Reload: 1, ReloadTime: 90, Tagged: true, Stockpile: true},
	}}}
	elapsed, total, ok := communityReload(u)
	if !ok || elapsed != 40 || total != 60 {
		t.Fatalf("communityReload = %d/%d, %v; want 40/60, true [CP-WPN-7]", elapsed, total, ok)
	}

	// Equal authored reloads retain the lower slot, while a runtime count above
	// the authored maximum produces an empty bar rather than underflowing.
	u.CommunityHUD.Weapons[0] = frame.UnitWeaponHUDView{Reload: 80, ReloadTime: 60, Tagged: true}
	elapsed, total, ok = communityReload(u)
	if !ok || elapsed != 0 || total != 60 {
		t.Fatalf("tie/overflow selection = %d/%d, %v; want first slot at 0/60", elapsed, total, ok)
	}
}

func TestCommunityReloadGatesNanoframesStockpilesAndUntaggedSlots(t *testing.T) {
	for _, tc := range []struct {
		name string
		unit frame.UnitView
	}{
		{"nanoframe", frame.UnitView{BuildRemaining: 0.25, CommunityHUD: frame.UnitHUDView{Weapons: [3]frame.UnitWeaponHUDView{{Tagged: true, ReloadTime: 30}}}}},
		{"stockpile", frame.UnitView{CommunityHUD: frame.UnitHUDView{Weapons: [3]frame.UnitWeaponHUDView{{Tagged: true, Stockpile: true, ReloadTime: 30}}}}},
		{"untagged", frame.UnitView{CommunityHUD: frame.UnitHUDView{Weapons: [3]frame.UnitWeaponHUDView{{ReloadTime: 30}}}}},
		{"zero reload", frame.UnitView{CommunityHUD: frame.UnitHUDView{Weapons: [3]frame.UnitWeaponHUDView{{Tagged: true}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, ok := communityReload(&tc.unit); ok {
				t.Fatalf("%s admitted a reload bar [CP-WPN-7]", tc.name)
			}
		})
	}
}

func TestCommunityCountersFormatsSourceState(t *testing.T) {
	u := &frame.UnitView{CommunityHUD: frame.UnitHUDView{
		StockpileCount: 3, StockpileQueued: 2,
		TransportCount: 1, TransportCapacity: 4,
	}}
	got := communityCounters(u)
	if got.stockpile != "3 +2" || got.transport != "1/4" {
		t.Fatalf("communityCounters = %+v, want stockpile and transport source formats", got)
	}

	// A one-seat flying transport is the exact excluded property: both the
	// flying classification and capacity == 1 are resolved by the publisher.
	u.CommunityHUD.TransportCapacity = 1
	u.CommunityHUD.SingleUnitFlyingTransport = true
	if got := communityCounters(u).transport; got != "" {
		t.Fatalf("single-unit flying transport label = %q, want absent", got)
	}
	u.CommunityHUD.SingleUnitFlyingTransport = false
	if got := communityCounters(u).transport; got != "1/1" {
		t.Fatalf("one-seat non-flying transport label = %q, want 1/1", got)
	}
}

func TestCommunityReloadBarRasterProgress(t *testing.T) {
	c := newLabelClient(80, 48)
	c.pal.Logical[0] = 200
	c.resetListForTest()
	c.drawCommunityReloadBar(40, 20, 30, 60)
	c.replayForTest()
	// Half progress uses an inclusive 17-pixel fill: width arithmetic returns
	// 16 and the patch's inclusive rectangle adds the endpoint.
	if got := countRow(c, 22, 141); got != 17 {
		t.Fatalf("half reload fill = %d pixels, want 17 [CP-WPN-7]", got)
	}
	if got := countRow(c, 20, 200); got != 35 {
		t.Fatalf("reload outer row = %d pixels, want 35 [CP-WPN-7]", got)
	}
}

func TestCommunityHUDCanSuppressExistingGroupDigit(t *testing.T) {
	previous := DamageBars()
	defer SetDamageBars(previous)
	SetDamageBars(false)
	font := &formats.FNT{Height: 1}
	font.Glyphs['4'] = &formats.FNTGlyph{Width: 1, Height: 1, Bits: []byte{0x80}}
	unitFrame := &frame.Frame{ViewingPlayer: 0, Units: []frame.UnitView{{
		Owner: 0, X: numeric.Fixed(40 << 16), Z: numeric.Fixed(10 << 16),
		Health: 100, MaxHealth: 100, Group: 4,
	}}}

	draw := func(disable bool) int {
		c := newLabelClient(96, 56)
		c.fnt = font
		c.cam = &camera.Camera{ViewW: 96, ViewH: 56, MapW: 256, MapH: 256}
		c.SetCommunityHUDOptions(CommunityHUDOptions{DisableGroupNumbers: disable})
		c.drawUnitLabels(unitFrame, true)
		c.replayForTest()
		pixels := 0
		for _, value := range c.indexed {
			if value == 15 {
				pixels++
			}
		}
		return pixels
	}
	if got := draw(false); got == 0 {
		t.Fatal("zero-value Community HUD option hid the existing group digit")
	}
	if got := draw(true); got != 0 {
		t.Fatalf("disabled group numbers drew %d group pixels, want none", got)
	}
}
