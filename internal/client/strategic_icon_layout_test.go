package client

import (
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"testing"
)

func iconLayoutFixture(t *testing.T) (*Client, *frame.Frame) {
	t.Helper()
	c := zoomRecorderClient(t)
	c.SetEnhanced(true)
	c.SetStrategicBlipArt(testBlipArt())
	c.SetStrategicTeamArt(testBlipArt())
	c.SetStrategicIconCatalog(NewStrategicIconCatalog(&content.Catalog{Units: map[string]*content.UnitDef{
		"walker": {DefinitionHeader: content.DefinitionHeader{CanonicalKey: "walker"}, UnitName: "walker", BMCode: 1, Category: "KBOT", Builder: true},
		"plant":  {DefinitionHeader: content.DefinitionHeader{CanonicalKey: "plant"}, UnitName: "plant", BMCode: 0, Category: "PLANT", Builder: true},
	}}))
	c.cam.Zoom, c.cam.Scale = strategicModelCut, camera.ViewScaleNative
	f := &frame.Frame{ViewingPlayer: 0, Units: []frame.UnitView{{Slot: 7, InstanceID: 1, Owner: 0, OwnerColorKnown: true, DefName: "walker", X: numeric.FixedFromInt(600), Z: numeric.FixedFromInt(400)}}}
	f.Radar.Contacts = []frame.RadarContactView{{Kind: frame.RadarContactUnit, Handle: 7, Owner: 0, PaletteKnown: true, Visible: true, X: f.Units[0].X, Z: f.Units[0].Z}}
	return c, f
}

// Identity needs the unit gate as well as radar admission. The sensor list can
// contain true definition metadata even when the player sees only a dot.
func TestStrategicIconIdentificationAndSlotReuse(t *testing.T) {
	c, f := iconLayoutFixture(t)
	c.drawStrategicMarkers(f)
	if len(c.markerArena) != 1 || c.markerArena[0].IconAtlas == nil {
		t.Fatal("owned unit has no typed icon")
	}
	first := c.markerArena[0].IconRect
	if h, _, ok := c.PickPresentedUnit(f, 309, 209, 0); !ok || h != 7 {
		t.Fatal("icon corner does not pick its unit")
	}
	// Same handle immediately replaces its occupant; no stale descriptor survives.
	f.Units[0].DefName = "plant"
	f.Units[0].InstanceID = 2
	c.drawStrategicMarkers(f)
	if c.markerArena[0].IconRect == first {
		t.Fatal("slot replacement inherited the previous icon")
	}
	f.Units[0].Owner = 1
	f.Units[0].Cloaked = true
	f.Radar.Contacts[0].Owner = 1
	f.Radar.Contacts[0].Commander = true
	f.Radar.Contacts[0].Graphic = "commander"
	c.drawStrategicMarkers(f)
	if len(c.markerArena) != 1 || c.markerArena[0].IconAtlas != nil || c.markerArena[0].Size != strategicMarkerSize {
		t.Fatal("radar-only contact revealed identity")
	}
	if _, _, ok := c.PickPresentedUnit(f, 300, 200, 0); ok {
		t.Fatal("generic contact exposed a unit hit")
	}
	// Removing radar admission also removes the generic dot, including under fog.
	f.Radar.MappingLOS = 3
	f.Radar.Contacts[0].Visible = false
	c.drawStrategicMarkers(f)
	if len(c.markerArena) != 0 {
		t.Fatal("unadmitted contact left an icon")
	}
}

func TestStrategicIconOverlapAndProjection(t *testing.T) {
	c, f := iconLayoutFixture(t)
	u := f.Units[0]
	u.Slot = 8
	u.InstanceID = 2
	f.Units = append(f.Units, u)
	p := f.Radar.Contacts[0]
	p.Handle = 8
	f.Radar.Contacts = append(f.Radar.Contacts, p)
	f.Radar.Contacts[0].Selected = true
	f.Units[0].Flags |= 0x10
	c.drawStrategicMarkers(f)
	if h, _, ok := c.PickPresentedUnit(f, 300, 200, 0); !ok || h != 7 {
		t.Fatal("selected icon was not drawn/picked on top")
	}
	f.Radar.Contacts[0].Selected = false
	f.Units[0].Flags &^= 0x10
	c.SetStrategicHover(1)
	if h, _, ok := c.PickPresentedUnit(f, 300, 200, 0); !ok || h != 8 {
		t.Fatal("hover changed overlap order")
	}
	c.cam.X = 100
	if _, _, ok := c.PickPresentedUnit(f, 300, 200, 0); ok {
		t.Fatal("old camera hit list survived pan")
	}
	if h, _, ok := c.PickPresentedUnit(f, 250, 200, 0); !ok || h != 8 {
		t.Fatal("new projection not picked")
	}
	// A changed viewer cannot reuse the owner's identified layout.
	f.Units[0].Cloaked = true
	f.Units[1].Cloaked = true
	if _, _, ok := c.PickPresentedUnit(f, 250, 200, 1); ok {
		t.Fatal("viewer change reused identified hit list")
	}
}

func TestStrategicIconClipAndFade(t *testing.T) {
	c, f := iconLayoutFixture(t)
	f.Units[0].X = numeric.FixedFromInt(260)
	f.Radar.Contacts[0].X = f.Units[0].X
	c.drawStrategicMarkers(f)
	if _, _, ok := c.PickPresentedUnit(f, 127, 200, 0); ok {
		t.Fatal("icon picked through HUD clip")
	}
	if _, _, ok := c.PickPresentedUnit(f, 128, 200, 0); !ok {
		t.Fatal("visible icon edge did not pick")
	}
	c.cam.Zoom = (strategicMarkerOn + strategicModelCut) / 2
	c.drawStrategicMarkers(f)
	if len(c.markerArena) != 1 || c.markerArena[0].Alpha != 128 {
		t.Fatal("icon fade changed")
	}
	c.cam.Zoom = strategicMarkerOn
	c.drawStrategicMarkers(f)
	if len(c.markerArena) != 0 {
		t.Fatal("icon remained at fade start")
	}
	c.SetStrategicBlipArt(nil)
	c.cam.Zoom = strategicModelCut
	c.drawStrategicMarkers(f)
	if len(c.markerArena) != 1 || c.markerArena[0].IconAtlas == nil {
		t.Fatal("missing radar art suppressed a visible world unit")
	}
}

func TestStrategicIconBlendUsesCommittedVisibility(t *testing.T) {
	c, f := iconLayoutFixture(t)
	buffer := frame.NewBuffer()
	dst := buffer.BeginWrite()
	*dst = *f
	if err := buffer.Publish(1); err != nil {
		t.Fatal(err)
	}
	dst = buffer.BeginWrite()
	*dst = *f
	if err := buffer.Publish(2); err != nil {
		t.Fatal(err)
	}
	c.SetSnapshot(buffer)
	c.SetInterpolation(true)
	c.camPrevX, c.camCurX, c.camSamples = 0, 100, 2
	c.SetCameraFraction(0.5)
	c.drawStrategicMarkers(buffer.Current())
	if c.markerArena[0].X != 275 {
		t.Fatalf("blended marker x=%d", c.markerArena[0].X)
	}
	if _, _, ok := c.PickPresentedUnit(buffer.Current(), 275, 200, 0); !ok {
		t.Fatal("picker did not share camera blend")
	}
	// Drawing a blended pose cannot bypass the committed cloak gate.
	committed := buffer.Current()
	committed.Units[0].Owner = 1
	committed.Units[0].Cloaked = true
	committed.Radar.Contacts[0].Owner = 1
	blended := *committed
	blended.Units = append([]frame.UnitView(nil), committed.Units...)
	blended.Units[0].Owner = 0
	c.drawStrategicMarkers(&blended)
	if c.markerArena[0].IconAtlas != nil {
		t.Fatal("blended pose bypassed committed identification")
	}
}

// A prerender accepted within the host's tolerance must retain its actual
// displayed origin even after the host samples a different camera fraction.
func TestStrategicIconAcceptedProjection(t *testing.T) {
	c, f := iconLayoutFixture(t)
	buffer := frame.NewBuffer()
	for tick := uint32(1); tick <= 2; tick++ {
		dst := buffer.BeginWrite()
		*dst = *f
		if err := buffer.Publish(tick); err != nil {
			t.Fatal(err)
		}
	}
	c.SetSnapshot(buffer)
	c.SetInterpolation(true)
	c.camPrevX, c.camCurX, c.camSamples = 0, 240, 2
	c.SetCameraFraction(0.5)
	c.drawStrategicMarkers(buffer.Current())
	c.SetCameraFraction(0.625)      // measured fraction after predicted recording
	c.CommitStrategicPresentation() // accepted list, successfully submitted
	if _, _, ok := c.PickPresentedUnit(buffer.Current(), 249, 200, 0); !ok {
		t.Fatal("accepted predicted-frame edge did not pick")
	}
	// Another speculative recording must not overwrite the displayed projection.
	c.drawStrategicMarkers(buffer.Current())
	if _, _, ok := c.PickPresentedUnit(buffer.Current(), 249, 200, 0); !ok {
		t.Fatal("speculative frame replaced displayed hit bounds")
	}
	c.camCurX = 400 // a new camera sample invalidates the old projection
	if _, _, ok := c.PickPresentedUnit(buffer.Current(), 249, 200, 0); ok {
		t.Fatal("old projection survived a camera change")
	}
}

// Enhanced icon visibility follows the unit painter, not minimap contact status
// or its damage-blink cadence. Losing LOS still removes definition information.
func TestStrategicIconsStayVisibleWithoutRadarAdmission(t *testing.T) {
	c, f := iconLayoutFixture(t)
	f.Units[0].Owner = 1
	f.Radar.Contacts[0].Owner = 1
	f.Radar.Contacts[0].Visible = false
	f.Radar.Contacts[0].BlinkSuppress = 240
	f.Radar.MappingLOS = 3
	f.Visibility = frame.VisibilityView{W: 32, H: 32, Valid: true, CoverageBytes: true, Visible: make([]byte, 32*32)}
	f.Fog = frame.FogView{W: 32, H: 32, Valid: true, Ch0: make([]byte, 32*32)}
	f.Visibility.Visible[12*32+18] = 1 // unit's committed world position, 600/32 and 400/32
	for phase := uint8(0); phase < 2; phase++ {
		f.Radar.BlinkPhase = phase
		c.drawStrategicMarkers(f)
		if len(c.markerArena) != 1 || c.markerArena[0].IconAtlas == nil || c.markerArena[0].Size != 24 {
			t.Fatalf("visible enemy vanished on damage blink phase %d", phase)
		}
		if _, _, hit := c.PickPresentedUnit(f, 311, 211, 0); !hit {
			t.Fatal("24px icon edge was not clickable")
		}
		if _, _, hit := c.PickPresentedUnit(f, 312, 211, 0); hit {
			t.Fatal("icon hit escaped its 24px bounds")
		}
	}
	f.Radar.Contacts = nil
	c.drawStrategicMarkers(f)
	if len(c.markerArena) != 1 {
		t.Fatal("missing minimap contact suppressed visible unit")
	}
	f.Fog.Ch0[12*32+18] = 15
	c.drawStrategicMarkers(f)
	if len(c.markerArena) != 0 {
		t.Fatal("enemy icon revealed an unexplored anchor")
	}
	f.Fog.Ch0[12*32+18] = 0
	clear(f.Visibility.Visible)
	c.drawStrategicMarkers(f)
	if len(c.markerArena) != 0 {
		t.Fatal("enemy definition remained after LOS loss")
	}
}

func TestStrategicOwnedDamageBlinkAndCarriedCargo(t *testing.T) {
	c, f := iconLayoutFixture(t)
	f.Units[0].Flags |= 0x10
	f.Radar.Contacts[0].BlinkSuppress = 240
	c.drawStrategicMarkers(f)
	if len(c.markerArena) != 1 || !c.markerArena[0].Selected {
		t.Fatal("damage blink hid selected own icon")
	}
	f.Units[0].Carrier, f.Units[0].CarriedPiece = 8, -1
	c.drawStrategicMarkers(f)
	if len(c.markerArena) != 0 {
		t.Fatal("piece-less cargo gained a world icon")
	}
}

func TestStrategicIconUsesPublishedTeamColour(t *testing.T) {
	c, f := iconLayoutFixture(t)
	f.Units[0].OwnerColor = 1 // deliberately differs from owner and radar selector
	want, ok := c.strategicTeamColor(1)
	if !ok {
		t.Fatal("missing team fixture")
	}
	c.drawStrategicMarkers(f)
	if len(c.markerArena) != 1 || c.markerArena[0].Index != want {
		t.Fatal("icon tint ignored owner colour")
	}
	f.Units[0].OwnerColorKnown = false
	c.SetStrategicTeamArt(nil)
	c.drawStrategicMarkers(f)
	if len(c.markerArena) != 1 {
		t.Fatal("unknown team colour hid visible unit")
	}
}

func TestStrategicAttachedIconFollowsCarrierAdmission(t *testing.T) {
	c, f := iconLayoutFixture(t)
	child := f.Units[0]
	child.Slot, child.InstanceID, child.Owner = 8, 2, 1
	child.Carrier, child.CarriedPiece = 7, 0
	child.Cloaked = true // the model child inherits carrier admission, not its own hull gate
	f.Units[0].Cargo = []pool.Handle{8}
	f.Units = append(f.Units, child)
	grandchild := child
	grandchild.Slot, grandchild.Carrier = 9, 8
	f.Units[1].Cargo = []pool.Handle{9}
	f.Units = append(f.Units, grandchild)
	c.drawStrategicMarkers(f)
	if len(c.markerArena) != 2 {
		t.Fatal("visible carrier lost its attached model's icon")
	}
	f.Units[0].Owner, f.Units[0].Cloaked = 1, true
	f.Radar.Contacts = nil
	c.drawStrategicMarkers(f)
	if len(c.markerArena) != 0 {
		t.Fatal("hidden carrier exposed attached model icon")
	}
	f.Units[0].Owner = 0
	f.Units[0].Cargo = nil
	c.drawStrategicMarkers(f)
	if len(c.markerArena) != 1 {
		t.Fatal("unlinked cargo appeared independently")
	}
}
