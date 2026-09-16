package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
)

func probeFixture() (*frame.Frame, developerProbeTarget) {
	f := &frame.Frame{Tick: 42, Paused: true}
	f.Units = []frame.UnitView{{Slot: 7, InstanceID: 81, Owner: 0, BMCode: true, Health: -3, MoverMode: 1, BuildRemaining: 0.125}}
	f.Players[0] = frame.PlayerRow{Present: true, Controller: 1, Name: "Player"}
	f.Developer = &frame.DeveloperView{Tick: 42, Units: []frame.DeveloperUnit{{
		Slot: 7, InstanceID: 81, DisplayName: "Constructor", AutoTargetAvailable: true,
		AutoTarget: [3]bool{true, false, true}, BuildsAvailable: true,
		Builds: []frame.DeveloperBuildOption{{Name: "armlab", Score: 130, ScoreAvailable: true}, {Name: "armvp"}},
	}}}
	return f, developerProbeTarget{Slot: 7, InstanceID: 81, Enabled: true}
}

// Pinning is a Nanolathe presentation policy: an enabled probe must never
// silently describe a new occupant of its retained slot [I5][I6].
func TestDeveloperProbeTargetLifetime(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*frame.Frame, *developerProbeTarget)
		want string
	}{
		{"valid", func(*frame.Frame, *developerProbeTarget) {}, ""},
		{"reused slot", func(f *frame.Frame, _ *developerProbeTarget) { f.Units[0].InstanceID++ }, "reused"},
		{"missing identity", func(_ *frame.Frame, target *developerProbeTarget) { target.InstanceID = 0 }, "identity"},
		{"missing unit", func(f *frame.Frame, _ *developerProbeTarget) { f.Units = nil }, "no longer published"},
		{"old supplemental identity", func(f *frame.Frame, _ *developerProbeTarget) { f.Developer.Units[0].InstanceID-- }, "data missing"},
		{"old observation", func(f *frame.Frame, _ *developerProbeTarget) { f.Developer.Tick-- }, "stale"},
		{"unrequested", func(f *frame.Frame, _ *developerProbeTarget) { f.Developer = nil }, "awaiting publication"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, target := probeFixture()
			tc.edit(f, &target)
			u, observation, reason := developerProbeSubject(f, target)
			if tc.want == "" {
				if u == nil || observation == nil || reason != "" {
					t.Fatalf("valid subject rejected: %q", reason)
				}
			} else if u != nil || observation != nil || !strings.Contains(reason, tc.want) {
				t.Fatalf("invalid subject leaked data or wrong reason: %q", reason)
			}
		})
	}
}

// Established: numeric scores are unbounded, but bars have an upper clamp and
// inclusive fill, with signed division truncation [07 R-CAM-01 §9].
func TestDeveloperProbeScoreBarBoundaries(t *testing.T) {
	for _, tc := range []struct {
		score int32
		width int
	}{
		{-2147483648, 0}, {-1, 0}, {0, 0}, {1, 1}, {3, 1}, {4, 2}, {99, 26}, {100, 27}, {101, 27}, {2147483647, 27},
	} {
		if got := developerProbeBarWidth(tc.score); got != tc.width {
			t.Fatalf("score %d width %d, want %d", tc.score, got, tc.width)
		}
	}
	f, target := probeFixture()
	rows := developerProbeRows(f, target, true)
	var scored, unavailable bool
	for _, row := range rows {
		if row.ScoreAvailable {
			scored = row.Text == "       130 % - 'armlab'\n" && row.Score == 130
		}
		if row.Text == "       unavailable - 'armvp'\n" {
			unavailable = !row.ScoreAvailable
		}
	}
	if !scored || !unavailable {
		t.Fatalf("raw score or unavailable marker lost: %+v", rows)
	}
}

func TestDeveloperProbeOrderRowsKeepQueueOrderAndTargetNames(t *testing.T) {
	f, target := probeFixture()
	f.Developer.Units = append(f.Developer.Units, frame.DeveloperUnit{Slot: 8, InstanceID: 82, DisplayName: "Metal Extractor"})
	f.OrderQueues = []frame.OrderQueueView{{Unit: pool.Handle(7),
		Primary:   []frame.OrderView{{Kind: "Repair", State: 2, Target: 8}, {Kind: "Move", State: 1}},
		Secondary: []frame.OrderView{{Kind: "Guard", State: 3}}, SecondaryTruncated: true,
	}}
	rows := developerProbeRows(f, target, false)
	var text strings.Builder
	for _, row := range rows {
		text.WriteString(row.Text)
		text.WriteByte('|')
	}
	want := "Mission Q:|    'Repair' state: 2  tgt: 'Metal Extractor'\n|    'Move' state: 1\n|Background Mission Q:|    'Guard' state: 3\n|Remaining orders unavailable|"
	if !strings.Contains(text.String(), want) {
		t.Fatalf("ordered mission detail lost: %q", text.String())
	}
}

type developerProbeTestStage struct {
	frame     *frame.Frame
	selection developerProbeSelection
}

func (s developerProbeTestStage) DrawUI(c *client.Client, _ client.UIFrame) {
	drawDeveloperProbes(c, s.frame, s.selection)
}

func TestDeveloperProbeSharedLayoutAndDisabledPin(t *testing.T) {
	f, target := probeFixture()
	font := &formats.FNT{Height: 9}
	layout := &developerProbeLayout{}
	c, err := client.New(client.Options{Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	c.SetFNT(font)
	selection := developerProbeSelection{State: target, Font: font, Layout: layout}
	c.SetUIStage(developerProbeTestStage{f, selection})
	snap := c.ComposeFrameSnapshot()
	// The first rectangle uses 20 pitches; its outline extends one beyond
	// both the shade's right and bottom edges [07 R-CAM-01 §9].
	if got := snap.Indexed[241*snap.Width+402]; got != 5 {
		t.Fatalf("initial outline corner = %d, want 5", got)
	}
	wantBottom := 7*12 + 3 + len(developerProbeRows(f, target, false))*12
	if layout.Bottom != wantBottom {
		t.Fatalf("bottom %d, want %d", layout.Bottom, wantBottom)
	}
	firstPixels := append([]byte(nil), snap.Indexed...)
	repeat := c.ComposeFrameSnapshot()
	if !bytes.Equal(firstPixels, repeat.Indexed) {
		t.Fatal("repeated observation changed panel layout")
	}
	selection.State.Enabled = false
	selection.Builder = target
	c.SetUIStage(developerProbeTestStage{f, selection})
	snap = c.ComposeFrameSnapshot()
	if got := snap.Indexed[(wantBottom+1)*snap.Width+402]; got != 5 {
		t.Fatalf("builder did not use state's retained bottom: %d", got)
	}
	before := layout.Bottom
	selection.Builder.Enabled = false
	c.SetUIStage(developerProbeTestStage{f, selection})
	c.ComposeFrameSnapshot()
	if layout.Bottom != before {
		t.Fatal("disabled pins changed layout")
	}
}

// Opt-in visual fixture: installed COMIX and palette, with authored observation
// data. Capture files remain outside the repository.
func TestDeveloperProbeRetailCapture(t *testing.T) {
	dir := os.Getenv("NANOLATHE_PROBE_CAPTURE_DIR")
	if dir == "" {
		t.Skip("set NANOLATHE_PROBE_CAPTURE_DIR for probe visual inspection")
	}
	cs, err := openContent(Options{Root: testsupport.RetailRoot(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	font, err := formats.LoadFNTFile(cs.fs, "fonts/comix.fnt")
	if err != nil {
		t.Fatal(err)
	}
	pal, err := loadPaletteStrict(cs)
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.New(client.Options{Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	c.SetFNT(font)
	c.SetPalette(pal)
	f, target := probeFixture()
	layout := &developerProbeLayout{}
	for _, builder := range []bool{false, true} {
		selection := developerProbeSelection{Font: font, Layout: layout}
		name := "state.png"
		if builder {
			selection.Builder, name = target, "builder.png"
		} else {
			selection.State = target
		}
		c.SetUIStage(developerProbeTestStage{f, selection})
		if err := encodeShotPNG(filepath.Join(dir, name), c.ComposeFrame()); err != nil {
			t.Fatal(err)
		}
	}
}
