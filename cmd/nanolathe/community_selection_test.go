package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func communityDef(name string, id uint32) *content.UnitDef {
	return &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: name},
		UnitName:         name,
		UnitDefID:        id,
		UnitMask:         content.MaskForID(id),
	}
}

func TestCommunityCycleWalksHolesWrapsAndStopsOnNoMatch(t *testing.T) {
	b := &battleSession{sess: testSelectionSession(0)}
	t.Run("builder standby heads", func(t *testing.T) {
		f := &frame.Frame{Units: []frame.UnitView{
			{Slot: 2, DefID: 1, Owner: 0, Flags: units.ClassifierEligibleStatus, PriorHealthSample: 75},
			{Slot: 7, DefID: 1, Owner: 0, Flags: units.ClassifierEligibleStatus, PriorHealthSample: 75},
			{Slot: 11, DefID: 1, Owner: 0, Flags: units.ClassifierEligibleStatus, PriorHealthSample: 75},
			{Slot: 15, DefID: 1, Owner: 0, Flags: units.ClassifierEligibleStatus, PriorHealthSample: 75, BuildRemaining: .5},
			{Slot: 19, DefID: 1, Owner: 0, Flags: units.ClassifierEligibleStatus, PriorHealthSample: 1},
		}, OrderQueues: []frame.OrderQueueView{
			{Unit: 2, Primary: []frame.OrderView{{Kind: "Standby"}}, Secondary: []frame.OrderView{{Kind: "QMove"}}},
			{Unit: 7, Primary: []frame.OrderView{{Kind: "Move_Ground"}}},
			{Unit: 11, Primary: []frame.OrderView{{Kind: "VTOL_Standby"}}},
			{Unit: 15, Primary: []frame.OrderView{{Kind: "Standby"}}},
			{Unit: 19, Primary: []frame.OrderView{{Kind: "Standby"}}},
		}}

		mask := content.MaskForID(1)
		if got, ok := b.nextCommunityCycleUnit(f, 2, mask, communityCycleBuilder); !ok || got.Slot != 11 {
			t.Fatalf("cycle after hole/busy slot = %d,%t, want slot 11", got.Slot, ok)
		}
		if got, ok := b.nextCommunityCycleUnit(f, 11, mask, communityCycleBuilder); !ok || got.Slot != 2 {
			t.Fatalf("wrapped cycle = %d,%t, want slot 2", got.Slot, ok)
		}
		if got, ok := b.nextCommunityCycleUnit(f, 0, content.MaskForID(99), communityCycleBuilder); ok {
			t.Fatalf("empty full pass returned slot %d", got.Slot)
		}
	})

	t.Run("factory production head only", func(t *testing.T) {
		f := &frame.Frame{Units: []frame.UnitView{
			{Slot: 3, DefID: 2, Owner: 0, Flags: units.ClassifierEligibleStatus},
			{Slot: 8, DefID: 2, Owner: 0, Flags: units.ClassifierEligibleStatus},
			{Slot: 12, DefID: 2, Owner: 0, Flags: units.ClassifierEligibleStatus},
		}, OrderQueues: []frame.OrderQueueView{
			{Unit: 3, Primary: []frame.OrderView{{Kind: "Standby"}}, Secondary: []frame.OrderView{{Kind: "BuildingBuild"}}},
			{Unit: 8, Primary: []frame.OrderView{{Kind: "BuildingBuild"}}},
			{Unit: 12, Secondary: []frame.OrderView{{Kind: "BuildingBuild"}}},
		}}
		mask := content.MaskForID(2)
		if got, ok := b.nextCommunityCycleUnit(f, 3, mask, communityCycleFactory); !ok || got.Slot != 12 {
			t.Fatalf("factory cycle past active production = %d,%t, want slot 12", got.Slot, ok)
		}
		if got, ok := b.nextCommunityCycleUnit(f, 12, mask, communityCycleFactory); !ok || got.Slot != 3 {
			t.Fatalf("wrapped factory cycle = %d,%t, want slot 3", got.Slot, ok)
		}
	})
}

func TestCommunityCategoryFallbackAndGroundWeaponIntersection(t *testing.T) {
	defs := map[string]*content.UnitDef{
		"mobile":  communityDef("mobile", 1),
		"factory": communityDef("factory", 2),
		"airbase": communityDef("airbase", 3),
		"command": communityDef("command", 4),
	}
	defs["mobile"].Builder, defs["mobile"].BMCode = true, 1
	defs["factory"].Builder = true
	defs["airbase"].Builder, defs["airbase"].IsAirBase, defs["airbase"].BMCode = true, true, 1
	defs["command"].Builder, defs["command"].ShowPlayerName, defs["command"].HideDamage, defs["command"].BMCode = true, true, true, 1
	b := &battleSession{cat: &content.Catalog{Units: defs}}
	if got := b.communityCycleMask(communityCycleBuilder); !got.Contains(1) || got.Contains(2) || got.Contains(3) || got.Contains(4) {
		t.Fatalf("builder fallback membership is wrong")
	}
	if got := b.communityCycleMask(communityCycleFactory); !got.Contains(2) || got.Contains(1) || got.Contains(3) || got.Contains(4) {
		t.Fatalf("factory fallback membership is wrong")
	}

	weapons := map[string]*content.UnitDef{
		"ground": {DefinitionHeader: content.DefinitionHeader{CanonicalKey: "ground"}, Category: "CTRL_W NOTAIR"},
		"alias":  {DefinitionHeader: content.DefinitionHeader{CanonicalKey: "alias"}, Category: "CTRL_W NAIR"},
		"air":    {DefinitionHeader: content.DefinitionHeader{CanonicalKey: "air"}, Category: "CTRL_W", CanFly: true},
		"civil":  {DefinitionHeader: content.DefinitionHeader{CanonicalKey: "civil"}, Category: "NOTAIR"},
	}
	reg, err := content.CompileCategories(weapons)
	if err != nil {
		t.Fatalf("compile categories: %v", err)
	}
	b.cat = &content.Catalog{Units: weapons, Categories: reg}
	got := b.communityMobileWeaponMask()
	if !got.Contains(weapons["ground"].UnitDefID) || !got.Contains(weapons["alias"].UnitDefID) || got.Contains(weapons["air"].UnitDefID) || got.Contains(weapons["civil"].UnitDefID) {
		t.Fatalf("Ctrl+S mask did not equal CTRL_W with CanFly clear")
	}
}

func TestCommunitySelectionPreferencesNormalizeAndDefaultOff(t *testing.T) {
	p := settings.DefaultPresentation()
	if p.CommunitySelection != 0 || p.DoubleClickSelection != 0 {
		t.Fatalf("community selection defaults = %d/%d, want off", p.CommunitySelection, p.DoubleClickSelection)
	}
	p.CommunitySelection = -3
	p.DoubleClickSelection = 3
	p.Normalize()
	if p.CommunitySelection != 0 || p.DoubleClickSelection != 1 {
		t.Fatalf("normalized selection preferences = %d/%d, want 0/1", p.CommunitySelection, p.DoubleClickSelection)
	}
	p.CommunitySelection = 2
	p.Normalize()
	if p.CommunitySelection != 0 {
		t.Fatalf("low-bit normalization of 2 = %d, want 0", p.CommunitySelection)
	}
	b := &battleSession{shell: &gameShell{presentation: settings.DefaultPresentation()}}
	if b.communitySelectionEnabled() || b.doubleClickSelectionEnabled() {
		t.Fatal("default presentation enabled an optional selection consumer")
	}
}

func TestDoubleClickAndCtrlZKeepFullDefinitionIdentity(t *testing.T) {
	identityFrame := &frame.Frame{
		Units: []frame.UnitView{
			{Slot: 1, DefID: 700, DefName: "duplicate"},
			{Slot: 2, DefID: 701, DefName: "duplicate"},
		},
		Selection: frame.SelectionView{Handles: []pool.Handle{1}},
	}
	identityBattle := &battleSession{}
	if mask := identityBattle.selectedDefinitionMask(identityFrame); !mask.Contains(700) || mask.Contains(701) {
		t.Fatal("selected definition mask conflated equal display names")
	}

	cat := testCatalogON05()
	def, ok := cat.Unit("armcons")
	if !ok || def == nil {
		t.Fatal("fixture has no armcons")
	}
	def.UnitDefID = 700
	def.UnitMask = content.MaskForID(700)
	b := newTestBattle(cat, testWorldON05(100, 100))
	b.shell = &gameShell{presentation: settings.Presentation{DoubleClickSelection: 1}}
	b.sess.LocalOwner = 0
	one := placeUnit(b, "armcons", numeric.FixedFromInt(200), numeric.FixedFromInt(120))
	two := placeUnit(b, "armcons", numeric.FixedFromInt(260), numeric.FixedFromInt(150))
	far := placeUnit(b, "armcons", numeric.FixedFromInt(1200), numeric.FixedFromInt(1200))
	replaceSelectionForTest(t, b, one)

	sx, sy := screenPos(b.cam, two)
	in := input.NewState()
	event := input.PointerEvent{Kind: input.LeftDoubleClick, X: sx, Y: sy, Modifiers: input.Modifiers{Shift: true}, Buttons: input.MouseButtons{Left: true}, Timestamp: 1234}
	if !in.EnqueuePointer(event) || !in.PublishPointer() {
		t.Fatal("publish native double-click record")
	}
	if !b.handleCommunityDoubleClick(in, sx, sy) {
		t.Fatal("native double-click was not consumed")
	}
	applyPendingBattleCommands(b)
	got := selectedHandles(t, b)
	if len(got) != 2 || !containsHandle(got, one.Handle) || !containsHandle(got, two.Handle) || containsHandle(got, far.Handle) {
		t.Fatalf("double-click selection = %v, want two on-screen definition-700 units", got)
	}

	pressKeys(b, input.KeyCtrl, input.KeyZ)
	got = selectedHandles(t, b)
	if len(got) != 3 || !containsHandle(got, far.Handle) {
		t.Fatalf("Ctrl+Z selection = %v, want all three definition-700 units", got)
	}

	b.shell.presentation.DoubleClickSelection = 0
	if b.handleCommunityDoubleClick(in, sx, sy) {
		t.Fatal("disabled double-click option consumed the native event")
	}
}

func testSelectionSession(owner uint8) *session.Session {
	return &session.Session{LocalOwner: owner}
}
