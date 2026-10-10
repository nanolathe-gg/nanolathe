//go:build retail

package aikit_test

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
)

// The Modern AI's online fingerprint lock. Every other fingerprint lock is
// a battle of Classic players (DESIGN_GAMEPLAY_RULES "The Modern AI
// controller"), so none of them sees the util+tac controller, its worker or
// its private generator. Online, every client runs every Modern computer, so
// the controller's decisions must come out the same on every architecture a
// seat can run on: tools/check-retail runs this test natively, from an amd64
// build and from a js/wasm build with GOMAXPROCS=1, where the worker cannot
// run beside the simulation at all (DESIGN_MULTIPLAYER §5.1, M1-C11).
//
// The scene is TestOnlineModernComputersAgreeAcrossClientsRetail's
// composition and span on one client, seat 0: two Modern computers and a
// Classic one hosted by seat 0, both humans' scripted commands, six minutes
// in which the Modern computers build and lose units in fights and the
// Classic computer and seat 0 are destroyed. The run costs about 1.3 s
// natively and 6 s under Node. The constants are Nanolathe values recorded
// from a native darwin/arm64 run and confirmed on darwin/amd64 (Rosetta) and
// js/wasm (Node 24, GOMAXPROCS=1):
//
//   - lockModernAIRun is the run's digest: every receipt, the play-test unit
//     checksum and both random streams every 30 ticks, the final partial
//     fingerprint, and each Modern controller's applied-command outcomes and
//     generator position;
//   - lockModernAIRehearsal is the pre-start rehearsal digest the relay
//     compares at Ready for the same room (DESIGN_MULTIPLAYER §16.7), which
//     internal/session's own rehearsal tests compute with a stand-in for the
//     Modern AI.
//
// HOW TO UPDATE. A change to the Modern AI's decisions, to a Modern rule or to
// anything else this battle reaches moves these values; take the reported
// ones and say in the commit message which behaviour changed and why. A value
// that differs between architectures, GOMAXPROCS settings or runs of the same
// build is a lockstep defect, never a reason to update.
const (
	lockModernAITicks     = onlineModernSpan
	lockModernAIRun       = "275c2bf3df93a8f5e69b11ce18f3811d3360c776e5049db5c763f5ae1eb6ffa1"
	lockModernAIRehearsal = "d01a66338086438e1f6cf4f9dd7cd1bf35abdbed405edf6253ed5e6b38d8cd3f"
)

func TestOnlineModernAIFingerprintIsLocked(t *testing.T) {
	cat, _ := retailcat.Shared(t)
	setup := onlineModernSetup(cat, 0, true)
	inputs, config := onlineModernRoom(t, setup)

	begin := time.Now()
	rehearsal, err := session.RehearsalDigest(inputs, config)
	if err != nil {
		t.Fatal(err)
	}
	rehearsed := time.Since(begin)

	s := onlineModernReplica(t, inputs, config, 0)
	if hosts := modernHosts(t, s, setup); len(hosts) != 2 {
		t.Fatalf("%d Modern controllers, want 2", len(hosts))
	}
	begin = time.Now()
	out := runOnlineLockstep(t, []*session.Session{s}, setup, lockModernAITicks)[0]
	elapsed := time.Since(begin)
	if out.tick != lockModernAITicks {
		t.Fatalf("the battle ended at tick %d, before the lock's span", out.tick)
	}
	for row, st := range out.stats {
		if st.Applied == 0 {
			t.Fatalf("Modern computer %d applied no command: the lock no longer sees its decisions", row)
		}
	}
	t.Logf("rehearsal %s in %v; %d ticks %s in %v", hex.EncodeToString(rehearsal[:]), rehearsed, out.tick, out.digest, elapsed)
	if got := hex.EncodeToString(rehearsal[:]); got != lockModernAIRehearsal {
		t.Errorf("rehearsal digest with Modern computers = %s, want the locked %s", got, lockModernAIRehearsal)
	}
	if out.digest != lockModernAIRun {
		t.Errorf("%d-tick online run with Modern computers = %s, want the locked %s", lockModernAITicks, out.digest, lockModernAIRun)
	}
}
