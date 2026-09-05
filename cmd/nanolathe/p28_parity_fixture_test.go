package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/testsupport"
)

// TestP28OBS00ParityFixture reserves the acceptance gate for the authored
// reference reproduction. It must not silently substitute the repository's
// convenient retail smoke scenario for the match shown in the two captures.
//
// TODO(T25): the reference pair the retired P28 parity ledger pinned
// (`Screenshot 2026-08-28 at 15.27.24.png`, retail, and `Screenshot 2026-08-28
// at 15.24.35.png`, Nanolathe) does not record the scenario that produced it.
// Re-examined against the captures themselves on 2026-09-04 (WU-19-174): the
// two images are demonstrably the *same* scene — the same green map region,
// the same tree and metal-patch layout, the same 10150 storage ceiling and
// metal stocks within ~150 of each other — so the operator did drive both
// engines on one map. But an image records none of the authoritative inputs:
// the map file, the skirmish/mission configuration, the player rows, the start
// positions, the RNG seeds and the command sequence are all absent, and the
// last three cannot in principle be recovered from a picture. Identifying the
// map from its terrain would therefore still leave this test unable to create
// the same match state, so this is not a map-matching exercise.
//
// What would settle it: one re-recorded reference run whose producer writes
// the map, the lobby rows, the start positions, the seed and the command log
// down beside the capture — or a retail saved game from that same session,
// which carries the map and the player table as data [08 "Save-file
// organization"]. Deriving any of it from appearance is what [P28-OBS-00] and
// the plan's hard constraints expressly forbid.
//
// client.ComposeFrameSnapshot now supplies copied indexed and RGBA planes from
// one invocation of the same committed-frame composition path. That evidence
// boundary is available and is no longer a fixture blocker [03 §1][03
// §2.4][I6].
//
// A second blocker this test used to record — "on the current Darwin host,
// importing the production client blocks in Ebitengine/GLFW initialization
// before testing.T can run" — is **no longer true**, and was removed on
// 2026-09-04 (WU-19-174). This package imports the production client
// (internal/client, through the battle HUD and the unit-information screen)
// and its whole test binary, this test included, runs on this Darwin host;
// the claim dated from the 2026-08-28 baseline the ledger pins. Scenario
// provenance is the only thing left blocking the fixture.
func TestP28OBS00ParityFixture(t *testing.T) {
	testsupport.RetailRoot(t)
	t.Skip("P28-OBS-00 blocked: the reference captures do not record the map, player rows, start positions, seeds or command sequence needed to create the same match state; see the marker above")
}
