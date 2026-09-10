package main

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

type eliminationMessageCollector struct {
	drawlist.Sink
	text   string
	glyphs []drawlist.Glyphs
	logos  []drawlist.Sprite
}

func (s *eliminationMessageCollector) Clear()                   {}
func (s *eliminationMessageCollector) Terrain(drawlist.Terrain) {}
func (s *eliminationMessageCollector) Sprite(v drawlist.Sprite) {
	if v.Kind == drawlist.BlitScaled && v.Dst.X == 138 && v.Dst.Y == 52 {
		s.logos = append(s.logos, v)
	}
}
func (s *eliminationMessageCollector) Glyphs(v drawlist.Glyphs) {
	if v.Text == s.text {
		s.glyphs = append(s.glyphs, v)
	}
}
func (s *eliminationMessageCollector) Fill(drawlist.Fill)       {}
func (s *eliminationMessageCollector) Line(drawlist.Line)       {}
func (s *eliminationMessageCollector) Points(drawlist.Points)   {}
func (s *eliminationMessageCollector) Model(drawlist.Model)     {}
func (s *eliminationMessageCollector) Fog(drawlist.Fog)         {}
func (s *eliminationMessageCollector) Surface(drawlist.Surface) {}
func (s *eliminationMessageCollector) Cursor(drawlist.Cursor)   {}
func (s *eliminationMessageCollector) Expand()                  {}
func (s *eliminationMessageCollector) Flash(drawlist.Flash)     {}
func (s *eliminationMessageCollector) Halo(drawlist.Halo)       {}

// Established: a three-player skirmish keeps playing after one opponent's
// elimination, and its accepted announcement draws that owner's LOGOS art
// and plays MessageArrived exactly once [08 R-CAMP-01 §9][07 R-HUD-03 §14.4].
func TestThreePlayerEliminationMessageRetail(t *testing.T) {
	opts := Options{Root: probeRetail(t), Map: "ashap plateau", Seed: 7}
	cs, err := openContent(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	cfg := session.SkirmishConfig{MapName: opts.Map, NumPlayers: 3}
	cfg.ApplyDefaults()
	for i := 1; i < 3; i++ {
		cfg.Players[i].Controller = 1
	}
	cfg.Players[1].Nickname = "Vermin"
	cfg.Players[1].Color = 6
	request, err := skirmishBattleRequest(opts, cs, cfg, headlessScenarioSkirmish, nil, newBattleSeedSource(opts))
	if err != nil {
		t.Fatal(err)
	}
	composed, err := composeAuthoritativeBattle(request)
	if err != nil {
		t.Fatal(err)
	}
	sess := composed.Session
	cl, err := client.New(client.Options{Width: retailScreenW, Height: retailScreenH, Buffer: &frame.Buffer{}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := composeBattleEntry(sess, sess.Catalog, cs, cl, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer detachBattleAudio(cl, sess)
	cl.ConfigureMessageLines(10, 10)
	cl.SetScreenChat(0)
	old := audio.GlobalOutput()
	spy := &compositionAudioSpy{}
	audio.SetGlobalOutput(spy)
	defer audio.SetGlobalOutput(old)
	step := func() { sess.Step(sess.Clock.ScaledAnchor + 1) }
	step()
	cl.TickAudio()
	cl.MessageRing().Clear()
	for _, u := range sess.Units.IterSliced() {
		if u != nil && u.Alive && u.Owner == 1 {
			sess.Units.Destroy(u.Handle, units.DeathKilled)
		}
	}
	var line frame.MessageLine
	for i := 0; i < 60 && line.Text == ""; i++ {
		step()
		cl.TickAudio()
		for _, candidate := range cl.MessageLines() {
			if candidate.Class == 4 && candidate.SpeakerSlot == 1 {
				line = candidate
				break
			}
		}
	}
	if line.Text == "" {
		t.Fatal("elimination announcement did not reach the message ring")
	}
	if sess.Units.LiveCountForPlayer(0) == 0 || sess.Units.LiveCountForPlayer(1) != 0 || sess.Units.LiveCountForPlayer(2) == 0 || b.isResultVisible() {
		t.Fatal("fixture did not retain two surviving players after elimination")
	}
	// The cue alias can resolve to another authored sample name, so compare
	// with the service's resolved sample rather than guessing a WAV filename.
	arrival, err := sess.Audio.Load("MessageArrived")
	if err != nil || arrival == nil {
		t.Fatalf("MessageArrived sample unavailable: %v", err)
	}
	for i := 0; i < 3; i++ {
		cl.TickAudio()
		snap := cl.ComposeFrameSnapshot()
		got := &eliminationMessageCollector{text: line.Text}
		snap.List.Replay(got)
		wantLogo := b.hud.sideLogoFrame(sess.Snapshot.Current().Players[1].Logo)
		if wantLogo == nil || len(got.logos) != 1 || got.logos[0].Frame != wantLogo || len(got.glyphs) != 1 {
			t.Fatalf("elimination composition: logos=%d glyphs=%d loadedLogo=%v", len(got.logos), len(got.glyphs), wantLogo != nil)
		}
		a := int32(float64(b.hud.primaryFont.Height) * 0.8)
		if got.logos[0].Dst != (drawlist.Rect{X: 138, Y: 52, W: a + 1, H: a + 1}) || got.glyphs[0].X != int32(138.0+1.5*float64(a)) || got.glyphs[0].Y != 52 {
			t.Fatalf("elimination geometry: logo=%+v glyph=%+v", got.logos[0].Dst, got.glyphs[0])
		}
	}
	plays := 0
	for _, alias := range spy.aliases {
		if strings.EqualFold(alias, arrival.Alias) {
			plays++
		}
	}
	if plays != 1 {
		t.Fatalf("MessageArrived played %d times across repeated drain/draw, want 1 (aliases=%v)", plays, spy.aliases)
	}
}
