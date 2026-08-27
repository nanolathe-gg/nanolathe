package client

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/presentation"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/snapshot"
)

// TestSpec03IndexedScheduleDigests exercises the live client compositor from
// inside its package so the indexed surface can be checked before palette
// conversion. Both schedules consume the same immutable 30 Hz trace. Frame
// serials and other per-render bookkeeping are intentionally excluded: the
// contract covers the indexed image and once-per-simulation-tick presentation
// state, not the number of repaint calls [03 §1][I4][I6].
func TestSpec03IndexedScheduleDigests(t *testing.T) {
	a := runSpec03IndexedSchedule(t, []int{1, 1, 1, 1})
	b := runSpec03IndexedSchedule(t, []int{3, 2, 4, 1})
	if a.finalHash != b.finalHash {
		t.Fatalf("same 30 Hz trace produced different final indexed digests: %s vs %s", a.finalHash, b.finalHash)
	}
	if !reflect.DeepEqual(a.ticks, b.ticks) {
		t.Fatalf("per-tick presentation state changed with render rate:\nA %#v\nB %#v", a.ticks, b.ticks)
	}
	if !reflect.DeepEqual(a.ledger, b.ledger) {
		t.Fatalf("CRT ledger changed with render rate:\nA %#v\nB %#v", a.ledger, b.ledger)
	}
}

type spec03IndexedResult struct {
	finalHash string
	ticks     []spec03TickState
	ledger    []presentation.CRTLedgerEntry
}

type spec03TickState struct {
	tick       uint32
	newSimTick bool
	crtDraws   uint64
	cameraX    int32
	cameraZ    int32
}

func runSpec03IndexedSchedule(t *testing.T, renders []int) spec03IndexedResult {
	t.Helper()
	buf := &snapshot.Buffer{}
	c, err := New(Options{Buffer: buf, Width: 80, Height: 48, Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	cam := &camera.Camera{X: 256, Z: 256, ViewW: 80, ViewH: 48, MapW: 1024, MapH: 1024}
	c.SetCamera(cam)
	crt := presentation.NewCRTRandom(1)
	clock := presentation.Clock{}
	c.SetPresentationCRT(crt)
	c.SetPresentationClock(&clock)

	result := spec03IndexedResult{}
	for tick := uint32(0); tick < 4; tick++ {
		buf.Publish(spec03IndexedFixture(tick))
		count := renders[int(tick)%len(renders)]
		for i := 0; i < count; i++ {
			c.ComposeFrame()
			if i == 0 {
				// Capture the boundary immediately. A later repaint of the same
				// snapshot correctly clears NewSimTick for the next consumer.
				result.ticks = append(result.ticks, spec03TickState{
					tick:       clock.SimTick,
					newSimTick: clock.NewSimTick,
					crtDraws:   crt.Draws(),
					cameraX:    cam.X,
					cameraZ:    cam.Z,
				})
			}
		}
	}
	digest := sha256.Sum256(c.indexed)
	result.finalHash = hex.EncodeToString(digest[:])
	result.ledger = crt.Ledger()
	return result
}

func spec03IndexedFixture(tick uint32) *snapshot.Frame {
	f := &snapshot.Frame{
		Tick: tick,
		Units: []snapshot.UnitView{
			{InstanceID: 11, Slot: 1, Owner: 0, X: numeric.Fixed(128 << 16), Y: numeric.Fixed(8 << 16), Z: numeric.Fixed(160 << 16), Health: 100, MaxHealth: 100},
			{InstanceID: 12, Slot: 2, Owner: 1, X: numeric.Fixed(192 << 16), Y: numeric.Fixed(8 << 16), Z: numeric.Fixed(160 << 16), Health: 100, MaxHealth: 100},
		},
		Selection: snapshot.SelectionView{LocalPlayer: 0},
	}
	if tick != 0 {
		f.Events = []snapshot.EventView{{ID: tick, Sequence: uint64(tick), Tick: tick, Kind: snapshot.EventKindShake, Magnitude: 8, Lifetime: 2}}
	}
	return f
}
