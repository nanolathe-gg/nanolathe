package session

import (
	"encoding/binary"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

func featureAudioClient(t *testing.T, s *Session) (*client.Client, *sessionAudioOutputSpy) {
	t.Helper()
	if _, err := s.Audio.Cache.Put("treeburn", []byte{128, 129}); err != nil {
		t.Fatal(err)
	}
	s.Audio.Registry.SetCache(s.Audio.Cache)
	old := audio.GlobalOutput()
	spy := &sessionAudioOutputSpy{}
	audio.SetGlobalOutput(spy)
	t.Cleanup(func() { audio.SetGlobalOutput(old) })
	c, err := client.New(client.Options{Width: 32, Height: 32, Buffer: s.Snapshot})
	if err != nil {
		t.Fatal(err)
	}
	c.SetAudioService(s.Audio)
	return c, spy
}

// Live and spread ignition share the production callback and committed audio
// boundary [05 R-FEAT-01 §9, §11][I6]. Zero Y is the explicit host placeholder.
func TestFeatureIgnitionAndSpreadPublishAudio(t *testing.T) {
	fs := featureLifecycleFS(t)
	cat := featureLifecycleCatalog()
	tree := cat.Features["tree1"]
	tree.FootprintX, tree.FootprintZ, tree.SpreadChance = 2, 2, 100
	s := featureLifecycleSession(t, fs, cat)
	s.Vis.SetMode(0)
	c, spy := featureAudioClient(t, s)
	if s.Features.PlaceAt(4, 6, tree) == nil || s.Features.PlaceAt(7, 6, tree) == nil {
		t.Fatal("placement failed")
	}
	if !s.Features.Ignite(4, 6, 1, 0) || s.Features.Ignite(4, 6, 1, 0) {
		t.Fatal("initial/repeated ignition gates failed")
	}
	s.Features.InstanceAt(4, 6).BurnCountdown = 1
	s.Features.TickLifecycle(1)
	if !s.Features.InstanceAt(7, 6).IsBurning {
		t.Fatal("spread did not ignite neighbor")
	}
	if spy.plays != 0 {
		t.Fatal("ignition played before frame publication")
	}
	s.publishSnapshot(1)
	var sounds []frame.EventView
	for _, event := range s.Snapshot.Current().Events {
		if event.Kind == frame.EventKindAudio {
			sounds = append(sounds, event)
		}
	}
	if len(sounds) != 2 {
		t.Fatalf("committed audio events = %d, want successful ignition plus spread", len(sounds))
	}
	for i, x := range []int64{64, 112} {
		ev := sounds[i]
		if ev.Sound != "treeburn" || !ev.AudioAudible || !ev.AudioPositional || ev.X != numeric.FixedFromInt(x) || ev.Z != numeric.FixedFromInt(96) || ev.Y != 0 {
			t.Fatalf("sound %d = %+v, want corner (%d,96) and zero-height host placeholder", i, ev, x)
		}
	}
	// Supersede the current frame before the client draws: the retained
	// committed queue must still deliver both cues once.
	s.publishSnapshot(2)
	c.TickAudio()
	c.TickAudio()
	if spy.plays != 2 {
		t.Fatalf("client played %d cues, want each successful ignition once", spy.plays)
	}
}

// Restore must test the saved audience, not the opposite temporary composition
// audience, and retain the event across the real battle-entry tail and client
// drain [08 R-SAVE-FEATURE-01][03 §8.3][I6].
func TestRestoredFeatureAudioUsesRestoredVisibility(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		audible, noVisibility bool
	}{{name: "hidden"}, {name: "visible", audible: true}, {name: "no visibility service", noVisibility: true}} {
		t.Run(tc.name, func(t *testing.T) {
			audible := tc.audible
			fs := featureLifecycleFS(t)
			cat := featureLifecycleCatalog()
			s := featureLifecycleSession(t, fs, cat)
			s.Clock.GlobalTick = 17
			s.Vis.SetMode(visibility.ModeHistoryEnabled)
			pos := [3]numeric.Fixed{numeric.FixedFromInt(64), 0, numeric.FixedFromInt(96)}
			words := s.Vis.WordMask()
			for i := range words {
				words[i] = 0
				if !audible {
					words[i] = 1
				}
			}
			if s.IsAudibleAt(pos) == audible {
				t.Fatal("fixture did not establish opposite pre-restore audience")
			}
			mapping := make([]byte, len(words)*2)
			if audible {
				for i := range words {
					binary.LittleEndian.PutUint16(mapping[i*2:], 1)
				}
			}
			data := make([]byte, 10)
			data[9] = 0x50 // burn selector zero, saved countdown high nibble
			stage := &RetailBattleStage{Session: s, Image: &save.BattleImage{
				Features: save.FeatureImage{Animating: []save.FeatureRecord{{X: 4, Z: 6, TypeID: 0, Data: data}}},
				Metal:    make([]byte, len(s.World.Plot)), PlayerFeatures: make([]byte, len(s.World.Plot)/2), Mapping: mapping,
			}}
			c, spy := featureAudioClient(t, s)
			if tc.noVisibility {
				s.Vis = nil
			}
			beforeSim, beforeCRT := s.SimRNG().Draws(), s.CrtRNG().Draws()
			if err := RestoreRetailBattleCore(stage); err != nil {
				t.Fatal(err)
			}
			if s.SimRNG().Draws()-beforeSim != 1 || s.CrtRNG().Draws() != beforeCRT {
				t.Fatal("restored sound changed the ignition RNG budget")
			}
			if s.IsAudibleAt(pos) != audible {
				t.Fatal("restore did not install saved visibility")
			}
			if err := finishRestoredBattleEntry(s); err != nil {
				t.Fatal(err)
			}
			if spy.plays != 0 {
				t.Fatal("restore bypassed committed publication")
			}
			s.publishSnapshot(18)
			s.publishSnapshot(19)
			c.TickAudio()
			c.TickAudio()
			want := 0
			if audible {
				want = 1
			}
			if spy.plays != want {
				t.Fatalf("restored client played %d cues, want %d", spy.plays, want)
			}
		})
	}
}
