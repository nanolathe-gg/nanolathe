package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// The playback perspective is presentation only: a battle whose perspective
// changes between pumps — another owner, the whole map, both, back — and is
// republished while paused runs exactly as its twin that never changes it,
// with the same random streams, unit checksums and canonical checkpoints, and
// its local and viewing owners never move (DESIGN_MULTIPLAYER §10).
func TestPresentationPerspectiveChangesNoSimulation(t *testing.T) {
	fs := replayFixtureFS(t)
	config := replayFixtureConfig(t, gameplay.Modern)
	viewed := replayFixtureBattle(t, fs, config, false)
	twin := replayFixtureBattle(t, fs, config, false)
	perspectives := []PresentationPerspective{
		{Owner: 1, Override: true},
		{RevealAll: true},
		{Owner: 1, Override: true, RevealAll: true},
		{Owner: 0, Override: true},
		{},
	}
	spawn := HumanCommand{Kind: HumanDeveloperSpawn, DeveloperSpawn: HumanDeveloperSpawnCommand{Pattern: "portscout", X: 140 << 16, Y: 20 << 16, Z: 160 << 16}}
	for i := range 40 {
		for _, s := range []*Session{viewed, twin} {
			own := replayUnits(s, s.LocalOwner)
			var batch []HumanCommand
			switch i {
			case 0:
				batch = []HumanCommand{spawn, spawn}
			case 3, 17, 29:
				batch = []HumanCommand{{Kind: HumanOrder, Order: HumanOrderCommand{Handles: own, Code: 2, Position: replayPoint(int32(60+i*10), int32(400-i*8))}}}
			case 9:
				// A death the viewing player sees: temporary sight, which
				// the viewing owner decides.
				s.Units.Destroy(own[len(own)-1], units.DeathKilled)
			case 12:
				batch = []HumanCommand{{Kind: HumanVisibility, Visibility: HumanVisibilityCommand{ToggleMask: visibility.ModeCurrentEnabled}}}
			}
			for _, c := range batch {
				if err := s.EnqueueHumanCommand(c); err != nil {
					t.Fatal(err)
				}
			}
			replayRequestCheckpoint(t, s)
		}
		if i%3 == 0 {
			p := perspectives[(i/3)%len(perspectives)]
			if err := viewed.SetPresentationPerspective(p); err != nil {
				t.Fatal(err)
			}
			if !viewed.RepublishPresentation() {
				t.Fatal("the committed tick was not republished")
			}
			replayExpectPerspective(t, viewed, p)
		}
		viewed.ExecuteStep(StepPlan{run: true, ticks: 1 + i%3})
		twin.ExecuteStep(StepPlan{run: true, ticks: 1 + i%3})
		replayCompare(t, "a perspective change", viewed, twin)
		if viewed.LocalOwner != twin.LocalOwner || viewed.ViewingOwner != twin.ViewingOwner || viewed.ViewingOwner != 0 {
			t.Fatal("the perspective moved a simulation owner")
		}
		p := viewed.seatCommands.replay.perspective
		replayExpectPerspective(t, viewed, p)
	}
	replayCompareHistories(t, viewed, twin)
	if err := viewed.SetPresentationPerspective(PresentationPerspective{Owner: 10, Override: true}); err == nil {
		t.Fatal("a perspective of player 11 was accepted")
	}
}

// replayExpectPerspective checks the committed frame draws the perspective.
func replayExpectPerspective(t *testing.T, s *Session, p PresentationPerspective) {
	t.Helper()
	f := s.Snapshot.Current()
	viewer := s.ViewingOwner
	if p.Override {
		viewer = p.Owner
	}
	if f.ViewingPlayer != viewer {
		t.Fatalf("frame viewer %d, want %d", f.ViewingPlayer, viewer)
	}
	if p.RevealAll {
		if f.Fog.Valid || !f.Visibility.Valid || len(f.Visibility.Visible) == 0 {
			t.Fatal("a revealed frame draws fog or no coverage")
		}
		for _, v := range f.Visibility.Visible {
			if v == 0 {
				t.Fatal("a revealed frame hides a cell")
			}
		}
		for _, u := range f.Units {
			if !u.DirectVisibilityKnown || !u.DirectlyVisible {
				t.Fatalf("a revealed frame hides unit %d", u.Slot)
			}
		}
		for _, c := range f.Radar.Contacts {
			if !c.Visible {
				t.Fatalf("a revealed frame hides contact %d", c.Handle)
			}
		}
		return
	}
	if want := s.Vis.ByteGrid(visibility.PlayerID(viewer)); s.Vis.CurrentEnabled() && string(f.Visibility.Visible) != string(want) {
		t.Fatalf("the frame publishes another player's coverage than %d's", viewer)
	}
	if f.Fog.Valid != (viewer == s.ViewingOwner && s.Vis.FogCacheValid()) {
		t.Fatalf("fog valid %v for viewer %d", f.Fog.Valid, viewer)
	}
	for _, u := range f.Units {
		if u.Owner == viewer {
			continue
		}
		if real := s.Units.Unit(u.Slot); real != nil && u.DirectlyVisible != s.Vis.IsVisible(visibility.PlayerID(viewer), unitVisibilityTarget(real, s.sensorStatus(viewer, real))) {
			t.Fatalf("unit %d's visibility is not player %d's", u.Slot, viewer)
		}
	}
}
