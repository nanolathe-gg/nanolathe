package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// The patch's preview colours are physical palette entries, not GUI colour
// fields (community patch engine CP-CON-6). In particular the installed GUI
// colour 6 is red; treating the clear-site selector as that index hid valid sites.
func TestBuildGhostCommunityColorsBypassGUIMapping(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		kickout, valid, clear bool
		want                  uint8
	}{
		{"clear", true, true, false, 234},
		{"needs clearance", true, true, true, 240},
		{"rejected", true, false, false, 214},
		{"retail clear", false, true, false, 17},
		{"retail rejected", false, false, false, 19},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cl, err := client.New(client.Options{Width: 640, Height: 480})
			if err != nil {
				t.Fatal(err)
			}
			pal := &palette.Tables{}
			pal.Logical[10] = 17
			pal.Logical[4] = 19
			pal.Logical[6] = 21
			cl.SetPalette(pal)
			b := &battleSession{sess: &session.Session{Community: community.Features{ConstructionKickout: tc.kickout}}, cam: &camera.Camera{ViewW: 640, ViewH: 480}}
			state := b.battleState()
			state.ArmPlacement("fixture", 2, 2)
			state.Input.BuildCellX, state.Input.BuildCellZ = 12, 10
			state.Input.PointerX, state.Input.PointerY = 200, 160
			state.Input.BuildOK, state.Input.BuildNeedsClear = tc.valid, tc.clear
			cl.SetUIStage(battleHUDUIStage{hud: &retailBattleHUD{}, battle: b})
			shot := cl.ComposeFrameSnapshot()
			left, top, _, _ := b.placementRect()
			if got := shot.Indexed[int(top)*shot.Width+int(left)]; got != tc.want {
				t.Fatalf("outline palette index = %d, want %d", got, tc.want)
			}
		})
	}
}
