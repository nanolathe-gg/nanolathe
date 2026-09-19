package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// Use the installed campaign's final authored mission, but publish an authored
// terminal fixture rather than simulate a whole campaign [08 R-CAMP-01 §6].
func TestCampaignEndingMoviesRetailRoute(t *testing.T) {
	for side, name := range []string{"Arm", "Core"} {
		t.Run(name, func(t *testing.T) {
			g, cs, cl := retailAssetShell(t)
			oldClient, oldOutput := clPtr, audio.GlobalOutput()
			clPtr = cl
			audio.SetGlobalOutput(nil)
			t.Cleanup(func() { g.closeIntro(cl); clPtr = oldClient; audio.SetGlobalOutput(oldOutput) })
			cl.SetFocused(true)
			campaign, err := mission.DiscoverCampaign(cs.fs, "camps/"+name+" Campaign.tdf")
			if err != nil {
				t.Fatal(err)
			}
			last := campaign.Missions[len(campaign.Missions)-1]
			sess, err := session.NewMissionWithEntryOptions(cs.fs, nil, fmt.Sprintf("%s:MISSION%d", campaign.Path, last.Index), 0, 7, 7, session.MissionEntryOptions{SelectedSide: side, SelectedSideSet: true}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got, known := sess.SideForOwner(int(sess.LocalOwner)); !known || got != side {
				t.Fatal("campaign entry lost selected side")
			}
			sess.Latch.Bits = session.LatchBitWin1
			sess.Snapshot.BeginWrite().Result = frame.ResultView{Ended: true, Kind: "victory"}
			if err := sess.Snapshot.Publish(sess.Clock.GlobalTick + 1); err != nil {
				t.Fatal(err)
			}
			b := &battleSession{shell: g, sess: sess, fs: cs.fs}
			g.battle = b
			b.ensurePostBattleController()
			for i := 0; i < 100 && g.intro == nil; i++ {
				b.stepPostBattle(1.0/30, cl.Input(), cl)
			}
			want := fmt.Sprintf("data/%d.zrb", side+3)
			if g.intro == nil || g.intro.logical != want {
				t.Fatalf("final %s win did not open %s", name, want)
			}
			if g.battle != nil || b.shell != nil || b.sess != nil || !g.campaignProgressSet || g.campaignProgress.Thumbs[last.Index] != 'W' {
				t.Fatal("ending began before battle teardown or lost completed progress")
			}
			if g.menuBGMPending || g.intro.repeat {
				t.Fatal("ending repeated or armed menu music")
			}
			for range 180 {
				g.stepIntro(0.03333, cl)
			}
			if dir := os.Getenv("NANOLATHE_ENDING_SHOTS"); dir != "" {
				writeShellShot(t, cl, filepath.Join(dir, strings.ToLower(name)+".png"))
			}
			// A key skips just the current reel; its queued tail must not skip credits.
			cl.Input().EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyEscape})
			cl.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: 'x'})
			g.stepIntro(0, cl)
			if g.intro == nil || g.intro.logical != creditsMoviePath || cl.Input().PendingTokens() != 0 || g.menuBGMPending {
				t.Fatal("skip did not enter credits with fresh input and no menu interlude")
			}
			b.consumePostBattleEffects(101, cl)
			if len(g.movieQueue) != 0 {
				t.Fatal("drained result effects replayed the sequence")
			}
			for range 180 {
				g.stepIntro(0.03333, cl)
			}
			if dir := os.Getenv("NANOLATHE_ENDING_SHOTS"); dir != "" {
				writeShellShot(t, cl, filepath.Join(dir, "credits.png"))
			}
			g.intro.frame.Index = g.intro.decoder.FrameCount - 1
			g.stepIntro(0, cl)
			if g.intro != nil || !g.menuBGMPending {
				t.Fatal("credits completion did not restore menu")
			}
		})
	}
}

func TestCreditsCallbackAndMissingSequenceReels(t *testing.T) {
	g, _, cl := retailAssetShell(t)
	oldClient, oldOutput := clPtr, audio.GlobalOutput()
	clPtr = cl
	audio.SetGlobalOutput(nil)
	t.Cleanup(func() { g.closeIntro(cl); clPtr = oldClient; audio.SetGlobalOutput(oldOutput) })
	cl.SetFocused(true)
	cl.Input().Kbd.SetKey(input.KeyShift, true)
	g.activateGadget("Credits")
	if g.intro == nil || g.intro.logical != creditsMoviePath || g.intro.repeat {
		t.Fatal("Credits did not open its single-pass movie")
	}
	g.closeIntro(cl)
	if err := g.startMovieSequence(cl, "data/authored-absent.zrb", creditsMoviePath, "data/authored-absent.zrb"); err != nil {
		t.Fatal(err)
	}
	if g.intro == nil || g.intro.logical != creditsMoviePath {
		t.Fatal("missing first reel prevented credits")
	}
	g.intro.frame.Index = g.intro.decoder.FrameCount - 1
	g.stepIntro(0, cl)
	if g.intro != nil || len(g.movieQueue) != 0 || !g.menuBGMPending {
		t.Fatal("missing final reel stranded the sequence")
	}
	if err := g.startMovieSequence(cl, creditsMoviePath, creditsMoviePath); err != nil {
		t.Fatal(err)
	}
	g.intro.frame.Index = g.intro.decoder.FrameCount - 1
	g.stepIntro(0, cl)
	if g.intro == nil || g.intro.frame.Index != 0 || len(g.movieQueue) != 0 || g.menuBGMPending {
		t.Fatal("EOF did not enter the next reel directly")
	}
	g.closeIntro(cl)
	if err := g.startMovieSequence(cl, creditsMoviePath, creditsMoviePath); err != nil {
		t.Fatal(err)
	}
	cl.Input().Kbd.SetKey(input.KeyAlt, true)
	cl.Input().Kbd.SetKey(input.KeyF4, true)
	g.stepIntro(0, cl)
	if g.intro != nil || len(g.movieQueue) != 0 || !cl.ExitRequested() {
		t.Fatal("Alt+F4 continued the movie sequence")
	}
}

func TestMovieSequenceMalformedReelKeepsDiagnostic(t *testing.T) {
	g, cs, cl := retailAssetShell(t)
	previous := clPtr
	clPtr = cl
	t.Cleanup(func() { g.closeIntro(cl); clPtr = previous })
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "broken.zrb"), []byte("authored invalid movie"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cs.unmappedMount.MountDirectory(root, 1000); err != nil {
		t.Fatal(err)
	}
	if err := g.startMovieSequence(cl, "broken.zrb", creditsMoviePath); err != nil {
		t.Fatal(err)
	}
	modal := g.frontend.Panels.Modal()
	if g.intro != nil || len(g.movieQueue) != 0 || modal == nil || !strings.Contains(modal.Message(), "broken.zrb") {
		t.Fatal("malformed reel skipped its diagnostic or continued playback")
	}
}
