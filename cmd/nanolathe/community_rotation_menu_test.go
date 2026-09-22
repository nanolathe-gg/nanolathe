package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func TestCommunityRotationCardinalBandAndTieOrder(t *testing.T) {
	rect := gui.Rect{X: 10, Y: 20, W: 40, H: 40}
	for _, test := range []struct {
		name string
		x, y int32
		want units.StructureFacing
		ok   bool
	}{
		{"north-west tie", 10, 20, units.FacingNorth, true},
		{"south-east tie", 49, 59, units.FacingSouth, true},
		{"east", 49, 40, units.FacingEast, true},
		{"west", 10, 40, units.FacingWest, true},
		{"last north row", 30, 32, units.FacingNorth, true},
		{"central dead zone", 30, 33, units.FacingSouth, false},
		{"outside", 9, 20, units.FacingSouth, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, ok := communityRotationCardinal(rect, test.x, test.y)
			if got != test.want || ok != test.ok {
				t.Fatalf("cardinal=(%v,%v), want (%v,%v)", got, ok, test.want, test.ok)
			}
		})
	}
}

func TestCommunityRotationChevronFallbackCapture(t *testing.T) {
	c, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 80, Height: 80})
	if err != nil {
		t.Fatal(err)
	}
	pal := &palette.Tables{}
	pal.Base[communityRotationFill] = [4]byte{255, 255, 0, 255}
	pal.Base[communityRotationOutline] = [4]byte{0, 0, 0, 255}
	c.SetPalette(pal)
	rect := gui.Rect{X: 20, Y: 20, W: 40, H: 40}
	c.SetUIStage(painterBindingStage(func(c *client.Client) {
		for facing := units.FacingSouth; facing <= units.FacingWest; facing++ {
			drawCommunityRotationChevrons(c, rect, facing, communityRotationFill, communityRotationOutline)
		}
	}))
	if dir := os.Getenv("NANOLATHE_ROTATION_OVERLAY_SHOT"); dir != "" {
		writeShellShot(t, c, filepath.Join(dir, "community-rotation-overlay.png"))
	}
}

func TestCommunityRotationMenuEdgeSelectionHonorsPreferenceAndFacings(t *testing.T) {
	def := &content.UnitDef{UnitName: "fixture", BMCode: 0, Rotations: content.FacingSouth | content.FacingEast}
	def.CanonicalKey = content.CanonicalKey(def.UnitName)
	prefs := settings.DefaultPresentation()
	b := &battleSession{
		hostPresentation: &prefs,
		sess: &session.Session{Build: &construction.Service{
			Rules:     construction.CommunityRules{},
			Community: community.Features{StructureRotation: true},
		}},
		millisSource: &fakeMillisSource{},
	}
	window := &gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel}, {Kind: gui.KindButton, Rect: gui.Rect{X: 10, Y: 20, W: 40, H: 40}}}}
	h := &retailBattleHUD{}

	if !h.selectCommunityRotationMenuFacing(b, window, 1, def, 49, 40) {
		t.Fatal("allowed east edge did not select")
	}
	if b.communityPlacement.facing != units.FacingEast || b.communityRotationMenu.feedbackFacing != units.FacingEast {
		t.Fatalf("edge facing=%v feedback=%v, want east", b.communityPlacement.facing, b.communityRotationMenu.feedbackFacing)
	}
	if h.selectCommunityRotationMenuFacing(b, window, 1, def, 30, 20) {
		t.Fatal("disallowed north edge selected")
	}
	if b.communityPlacement.facing != units.FacingEast {
		t.Fatal("disallowed edge destroyed retained facing")
	}
	prefs.BuildRotationOverlay = 0
	if h.selectCommunityRotationMenuFacing(b, window, 1, def, 30, 59) {
		t.Fatal("disabled overlay retained an active edge gesture")
	}
}
