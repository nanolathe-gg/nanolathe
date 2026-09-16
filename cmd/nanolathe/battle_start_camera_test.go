package main

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// battleStartCameraFixture is the reset camera a world rebuild leaves behind on
// a map large enough that nothing here clamps [08 R-ENTRY-01 §3 step 12].
func battleStartCameraFixture() *camera.Camera {
	cam := camera.NewFromTerrain(4096, 4096, 4096, 4096, retailScreenW, retailScreenH)
	cam.Pan(0, 0)
	return cam
}

func campaignSession(specials ...mission.Special) *session.Session {
	return &session.Session{Mission: &mission.Mission{Type: mission.TypeCampaign, Specials: specials}}
}

// The campaign camera jumps to the first start-position special with stored
// number 0 — the special authored as StartPos1 — placing the origin at
// (X - viewW/2, Z - viewH/2) of the battle viewport [08 "Campaign camera"]
// [07 R-CAM-01 §12][03 §4.1].
func TestCampaignCameraJumpsToStartPos1(t *testing.T) {
	cam := battleStartCameraFixture()
	sess := campaignSession(
		mission.Special{Kind: 0, ID: 3, Name: "SomethingElse3", X: 10, Z: 10},
		mission.Special{Kind: 1, ID: 1, Name: "StartPos2", X: 4000, Z: 4000},
		mission.Special{Kind: 1, ID: 0, Name: "StartPos1", X: 1229, Z: 2432},
	)
	centerBattleStartCamera(sess, cam)
	// 1229 - 128 - 512/2 = 845 ; 2432 - 32 - 416/2 = 2192.
	if cam.X != 845 || cam.Z != 2192 {
		t.Fatalf("camera = (%d,%d), want (845,2192)", cam.X, cam.Z)
	}
	// The start position is seen at the viewport's centre pixel.
	if got := int32(1229) - cam.X; got != 384 {
		t.Errorf("start position screen X = %d, want 384", got)
	}
	if got := int32(2432) - cam.Z; got != 240 {
		t.Errorf("start position screen Z = %d, want 240", got)
	}
}

// "First" is a scan of the authored records, not a minimum: a later StartPos1
// never displaces an earlier one, and a non-start-position special with the
// same stored number is not a candidate [08 "Campaign camera"]. Special.ID is
// the STORED number, one less than the authored label [08 R-TRIG-01 §9].
func TestCampaignCameraTakesTheFirstStartPos1InRecordOrder(t *testing.T) {
	cam := battleStartCameraFixture()
	sess := campaignSession(
		mission.Special{Kind: 0, ID: 0, Name: "NotAStart1", X: 3000, Z: 3000},
		mission.Special{Kind: 1, ID: 0, Name: "StartPos1", X: 1000, Z: 900},
		mission.Special{Kind: 1, ID: 0, Name: "StartPos1", X: 2000, Z: 1900},
	)
	centerBattleStartCamera(sess, cam)
	if cam.X != 1000-384 || cam.Z != 900-240 {
		t.Fatalf("camera = (%d,%d), want (%d,%d)", cam.X, cam.Z, 1000-384, 900-240)
	}
}

// With no such special the camera keeps the world-rebuild reset position and
// nothing is reported [08 "Campaign camera"]. Arm campaign mission 1 is the
// case this locks from the other side: it fields no commander, so a camera that
// went looking for one instead of for StartPos1 left the world viewport black.
func TestCampaignCameraWithoutStartPos1KeepsTheResetPosition(t *testing.T) {
	for _, tc := range []struct {
		name     string
		specials []mission.Special
	}{
		{"no specials at all", nil},
		{"only higher-numbered start positions", []mission.Special{
			{Kind: 1, ID: 1, Name: "StartPos2", X: 2000, Z: 2000},
			{Kind: 1, ID: 2, Name: "StartPos3", X: 3000, Z: 3000},
		}},
		{"a non-start-position special with stored number 0", []mission.Special{
			{Kind: 0, ID: 0, Name: "Whatever1", X: 2000, Z: 2000},
		}},
	} {
		cam := battleStartCameraFixture()
		reset := [2]int32{cam.X, cam.Z}
		centerBattleStartCamera(campaignSession(tc.specials...), cam)
		if cam.X != reset[0] || cam.Z != reset[1] {
			t.Errorf("%s: camera moved to (%d,%d), want the reset position (%d,%d)",
				tc.name, cam.X, cam.Z, reset[0], reset[1])
		}
	}
}

// The campaign branch never consults units, so a session whose unit world is
// not even built still lands on StartPos1 [08 "Campaign camera"].
func TestCampaignCameraDoesNotTouchUnits(t *testing.T) {
	cam := battleStartCameraFixture()
	sess := campaignSession(mission.Special{Kind: 1, ID: 0, Name: "StartPos1", X: 1000, Z: 900})
	if sess.Units != nil {
		t.Fatal("fixture should have no unit world")
	}
	centerBattleStartCamera(sess, cam)
	if cam.X != 1000-384 || cam.Z != 900-240 {
		t.Fatalf("camera = (%d,%d), want (%d,%d)", cam.X, cam.Z, 1000-384, 900-240)
	}
}

// A skirmish takes the commander branch, and a session that cannot resolve one
// leaves the camera where the reset put it rather than guessing at a unit
// [08 R-SKIR-01 §2].
func TestSkirmishCameraWithoutAResolvableCommanderDoesNotGuess(t *testing.T) {
	cam := battleStartCameraFixture()
	sess := &session.Session{Mission: &mission.Mission{Type: mission.TypeSkirmish}}
	centerBattleStartCamera(sess, cam)
	if cam.X != 0 || cam.Z != 0 {
		t.Fatalf("camera = (%d,%d), want the reset position (0,0)", cam.X, cam.Z)
	}
	if _, ok := localCommanderUnit(sess); ok {
		t.Error("localCommanderUnit resolved a commander from a session with no units")
	}
}

// Exercise the production installation seam: the detached candidate starts at
// the menu size, but the entry jump belongs to the final battle viewport
// [07 "The loading screen"][08 "Campaign camera"].
func TestCampaignCameraInstallationUsesBattleSurface(t *testing.T) {
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	for _, size := range [][2]int{{640, 480}, {800, 600}, {1280, 720}, {1920, 1080}, {1281, 721}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			cam := battleStartCameraFixture()
			sess := campaignSession(mission.Special{Kind: 1, ID: 0, X: 2000, Z: 2000})
			sess.Snapshot = frame.NewBuffer()
			centerBattleStartCamera(sess, cam)
			b := &battleSession{sess: sess, cam: cam, hud: &retailBattleHUD{}}
			cl, err := client.New(client.Options{Width: size[0], Height: size[1]})
			if err != nil {
				t.Fatal(err)
			}
			installBattleClient(cl, b)
			// Insets are asymmetric on X and symmetric on Z. Odd spans
			// truncate before the inset is added [07 R-CAM-01 §13].
			wantX := int32(128 + (size[0]-128)/2)
			wantY := int32(32 + (size[1]-64)/2)
			if gotX, gotY := 2000-cam.X, 2000-cam.Z; gotX != wantX || gotY != wantY {
				t.Fatalf("start on screen = (%d,%d), want (%d,%d)", gotX, gotY, wantX, wantY)
			}
			if cam.Follow.Desired != (camera.Origin{X: cam.X, Z: cam.Z}) {
				t.Fatal("entry retained a glide toward the provisional origin")
			}
			// A later display edit must not jump back to the mission start.
			cam.JumpTo(500, 600)
			shell := &gameShell{cam: cam, battle: b}
			shell.applyDisplaySize(cl, size[0]+100, size[1]+100)
			if cam.X != 500 || cam.Z != 600 {
				t.Fatalf("later resize replayed entry: (%d,%d)", cam.X, cam.Z)
			}
		})
	}
}

func TestBattleEntryCameraClampsJumpAndPreservesSave(t *testing.T) {
	for _, tc := range []struct {
		name  string
		start *mission.Special
		saved *save.Camera
		want  camera.Origin
	}{
		{"northwest start", &mission.Special{Kind: 1, ID: 0, X: 10, Z: 10}, nil, camera.Origin{X: -128, Z: -32}},
		{"southeast start", &mission.Special{Kind: 1, ID: 0, X: 4000, Z: 4000}, nil, camera.Origin{X: 2816, Z: 3408}},
		{"missing start", nil, nil, camera.Origin{}},
		{"saved origin", &mission.Special{Kind: 1, ID: 0, X: 2000, Z: 2000}, &save.Camera{XPosition: 317, ZPosition: 419}, camera.Origin{X: 317, Z: 419}},
		{"saved origin at edge", &mission.Special{Kind: 1, ID: 0, X: 2000, Z: 2000}, &save.Camera{XPosition: 3400, ZPosition: 3800}, camera.Origin{X: 2816, Z: 3408}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sess := campaignSession()
			if tc.start != nil {
				sess.Mission.Specials = append(sess.Mission.Specials, *tc.start)
			}
			b := &battleSession{sess: sess, cam: battleStartCameraFixture(), entrySavedCamera: tc.saved}
			b.placeEntryCamera(640, 480)
			b.placeEntryCamera(1280, 720)
			if b.cam.X != tc.want.X || b.cam.Z != tc.want.Z || b.cam.Follow.Desired != tc.want {
				t.Fatalf("current = (%d,%d), desired = %+v, want %+v", b.cam.X, b.cam.Z, b.cam.Follow.Desired, tc.want)
			}
		})
	}
}
