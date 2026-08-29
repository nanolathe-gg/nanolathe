package main

import (
	"os"
	"testing"
)

// TestP28OBS00ParityFixture reserves the acceptance gate for the authored
// reference reproduction. It must not silently substitute the repository's
// convenient retail smoke scenario for the match shown in the two captures.
//
// TODO(T25): the reference capture provenance does not identify its map,
// mission/skirmish configuration, player rows, start positions, RNG seeds, or
// authored command sequence. The screenshots establish visible differences,
// but [P28-OBS-00] and the plan's hard constraints expressly forbid deriving
// those authoritative inputs from appearance. The reference capture producer
// must record those values before this test can create the same match state.
//
// client.ComposeFrameSnapshot now supplies copied indexed and RGBA planes from
// one invocation of the same committed-frame composition path. That evidence
// boundary is available and is no longer a fixture blocker [03 §1][03
// §2.4][I6].
//
// TODO(T25): on the current Darwin host, importing the production client
// blocks in Ebitengine/GLFW initialization before testing.T can run. The
// focused test therefore cannot execute its required three repetitions here;
// a host on which the existing production client package initializes is also
// required. Do not add an alternate production headless runtime to bypass
// that platform boundary [01 §2.2][I6][I11].
func TestP28OBS00ParityFixture(t *testing.T) {
	if os.Getenv("NANOLATHE_TA_ROOT") == "" {
		t.Skip("P28-OBS-00 requires NANOLATHE_TA_ROOT")
	}
	t.Skip("P28-OBS-00 blocked: authoritative reference scenario provenance is unavailable, and this Darwin host blocks in Ebitengine/GLFW initialization before testing.T; see TODO(T25) above")
}
