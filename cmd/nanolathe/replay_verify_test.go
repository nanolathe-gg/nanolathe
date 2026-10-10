package main

import (
	"io"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/replay"
)

// The replay flags take only the runs they describe: a replay plays or
// verifies alone, and --record-replay records a fresh --headless or --shot
// --map battle.
func TestReplayFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--replay", "a.nlreplay"},
		{"--verify-replay", "a.nlreplay", "--root", "x"},
		{"--replay-dir", "replays", "--map", "ashap plateau"},
		{"--map", "ashap plateau", "--headless", "--record-replay", "a.nlreplay"},
		{"--map", "ashap plateau", "--survival", "--shot", "a.png", "--record-replay", "a.nlreplay"},
	} {
		if _, err := parseFlags(args, io.Discard); err != nil {
			t.Errorf("%v refused: %v", args, err)
		}
	}
	for _, args := range [][]string{
		{"--replay", "a.nlreplay", "--verify-replay", "b.nlreplay"},
		{"--replay", "a.nlreplay", "--map", "ashap plateau"},
		{"--verify-replay", "a.nlreplay", "--headless"},
		{"--verify-replay", "a.nlreplay", "--record-replay", "b.nlreplay"},
		{"--record-replay", "a.nlreplay", "--map", "ashap plateau"},
		{"--record-replay", "a.nlreplay", "--headless", "--mission", "camps/Arm Campaign.tdf:MISSION0"},
		{"--record-replay", "a.nlreplay", "--map", "ashap plateau", "--headless", "--shot", "a.png"},
		{"--record-replay", "a.nlreplay", "--map", "ashap plateau", "--shot", "a.png", "--film", "f.json"},
		{"--local-mp-listen", "127.0.0.1:39031", "--map", "ashap plateau", "--replay", "a.nlreplay"},
		{"--local-mp-listen", "127.0.0.1:39031", "--map", "ashap plateau", "--record-replay", "a.nlreplay"},
	} {
		var out strings.Builder
		if _, err := parseFlags(args, &out); err == nil {
			t.Errorf("%v admitted", args)
		} else if out.Len() == 0 {
			t.Errorf("%v refused without saying why", args)
		}
	}
}

// Game time reads as m:ss or h:mm:ss, and the verifier's summary names the
// kind, map, ticks, checksums and how the recording ends.
func TestReplayGameTime(t *testing.T) {
	for tick, want := range map[uint32]string{0: "0:00", 29: "0:00", 30: "0:01", 1800: "1:00", 108000 + 61*30: "1:01:01"} {
		if got := replayGameTime(tick); got != want {
			t.Errorf("tick %d: %q, want %q", tick, got, want)
		}
	}
	v := replayVerification{Header: replay.Header{Kind: replay.KindSurvival, MapName: "The Pass"}, Ticks: 90, Verified: 3, End: replay.EndFinished}
	if got := v.String(); got != "nanolathe: replay verified: survival on The Pass, 90 ticks, 3 checksums matched, complete, finished" {
		t.Fatalf("summary %q", got)
	}
	v.Truncated = true
	if got := v.String(); !strings.HasSuffix(got, "truncated at its last whole chunk") {
		t.Fatalf("summary %q", got)
	}
}
