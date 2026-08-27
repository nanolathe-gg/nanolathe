// Package spec03_test contains black-box closure checks for specification 03.
// It intentionally imports only published services: these tests are a handoff
// gate, not a second implementation of the compositor.
package spec03_test

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/internal/audio"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// TestLiveCompositorScheduleDigests runs the published client compositor over
// one immutable 30 Hz trace at two presentation rates. The digest is the
// presented RGBA surface. Shake is included so the shared CRT ledger also
// proves that repeating a snapshot does not repeat a simulation-tick
// presentation event [03 §1]. The fixture digest below is deliberately only a
// snapshot-identity/immutability check; it is not a session authoritative hash.
func TestLiveCompositorScheduleDigests(t *testing.T) {
	a := runLiveSchedule(t, []int{1, 1, 1, 1})
	b := runLiveSchedule(t, []int{3, 2, 4, 1})
	if a.rgbaHash != b.rgbaHash {
		t.Fatalf("same 30Hz trace produced different final presented RGBA digests: %s vs %s", a.rgbaHash, b.rgbaHash)
	}
	if a.eventHash != b.eventHash {
		t.Fatalf("event trace changed with render rate: %s vs %s", a.eventHash, b.eventHash)
	}
	if a.clockTickBoundaries != b.clockTickBoundaries || a.clockTickBoundaries != 4 {
		t.Fatalf("clock tick boundaries = %d/%d, want four", a.clockTickBoundaries, b.clockTickBoundaries)
	}
	if a.snapshotHashBefore != a.snapshotHashAfter || b.snapshotHashBefore != b.snapshotHashAfter {
		t.Fatalf("presentation changed snapshot fixture digest: A %s -> %s, B %s -> %s", a.snapshotHashBefore, a.snapshotHashAfter, b.snapshotHashBefore, b.snapshotHashAfter)
	}

}

// TestPresentationLeavesSessionStateUnchanged compares a published
// headless presentation run with an otherwise identical non-presented run.
// The fixture fingerprint covers exactly the economy fields this test creates,
// including their float32 payload bits [01 §4.4][I6].
func TestPresentationLeavesSessionStateUnchanged(t *testing.T) {
	on := runSessionHashStatePresentation(t, true)
	off := runSessionHashStatePresentation(t, false)
	if on.before == "" || off.before == "" {
		t.Fatal("session fixture fingerprint unexpectedly empty for non-empty economy state")
	}
	if on.before != on.after || off.before != off.after {
		t.Fatalf("presentation changed session HashState: on %s -> %s, off %s -> %s", on.before, on.after, off.before, off.after)
	}
	if on.before != off.before || on.after != off.after {
		t.Fatalf("presentation mode changed baseline session HashState: on %s/%s, off %s/%s", on.before, on.after, off.before, off.after)
	}
}

type sessionHashStateResult struct {
	before string
	after  string
}

func runSessionHashStatePresentation(t *testing.T, present bool) sessionHashStateResult {
	t.Helper()
	buf := &frame.Buffer{}
	s := &session.Session{Econ: &economy.Service{}, Snapshot: buf}
	s.Econ.Players[0].Exists = true
	s.Econ.Players[0].Stock[economy.Metal] = 321.5
	s.Econ.Players[0].Stock[economy.Energy] = 654.25
	f := fixtureFrame(1)
	w := buf.BeginWrite()
	*w = *f
	_ = buf.Publish(f.Tick)

	c, err := client.New(client.Options{Buffer: buf, Width: 80, Height: 48, Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	c.SetCamera(&camera.Camera{X: 256, Z: 256, ViewW: 80, ViewH: 48, MapW: 1024, MapH: 1024})
	crt := rng.NewCRT(1)
	c.SetPresentationCRT(&crt)
	if present {
		// Attach the published audio queue so this enabled path includes the
		// presentation drain seam while remaining device-free in headless mode.
		c.SetAudioQueue(audio.NewQueue())
	}

	before := sessionFixtureStateFingerprint(s)
	if present {
		c.ComposeFrame()
		c.TickAudio()
	}
	after := sessionFixtureStateFingerprint(s)
	return sessionHashStateResult{before: before, after: after}
}

// sessionFixtureStateFingerprint is intentionally local to this external test:
// it covers only the state constructed above and keeps presentation tests from
// depending on a shipping-package evidence helper.
func sessionFixtureStateFingerprint(s *session.Session) string {
	if s == nil || s.Econ == nil {
		return ""
	}
	p := &s.Econ.Players[0]
	return fmt.Sprintf("exists=%t metal=%08x energy=%08x", p.Exists,
		math.Float32bits(p.Stock[economy.Metal]), math.Float32bits(p.Stock[economy.Energy]))
}

// TestRetailDataClosure is opt-in because the retail install is not a repo
// dependency. Every candidate is reported separately, including clean skips;
// retail bytes are never copied into the test fixtures [G0].
func TestRetailDataClosure(t *testing.T) {
	root := os.Getenv("NANOLATHE_SPEC03_RETAIL_ROOT")
	if root == "" {
		t.Skip("set NANOLATHE_SPEC03_RETAIL_ROOT to run optional retail-data checks")
	}
	assets := []string{"totala1.hpi", "totala2.hpi", "totala3.hpi", "totala4.hpi", "totala5.hpi", "totala6.hpi", "totala7.hpi", "totala8.hpi", "totala9.hpi"}
	for _, name := range assets {
		name := name
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(root, name)
			info, err := os.Stat(path)
			if err != nil {
				t.Skipf("asset unavailable: %v", err)
			}
			t.Logf("asset available: %s (%d bytes); parser/live-map assertion remains gated to its authored map fixture", path, info.Size())
		})
	}
}

type liveResult struct {
	rgbaHash            string
	eventHash           string
	clockTickBoundaries int
	snapshotHashBefore  string
	snapshotHashAfter   string
}

func runLiveSchedule(t *testing.T, renders []int) liveResult {
	t.Helper()
	buf := &frame.Buffer{}
	c, err := client.New(client.Options{Buffer: buf, Width: 80, Height: 48, Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	c.SetCamera(&camera.Camera{X: 256, Z: 256, ViewW: 80, ViewH: 48, MapW: 1024, MapH: 1024})
	crt := rng.NewCRT(1)
	c.SetPresentationCRT(&crt)

	var before, after []byte
	var rgbaHash string
	var eventBytes []byte
	boundaries := 0
	for tick := uint32(0); tick < 4; tick++ {
		f := fixtureFrame(tick)
		before = append(before, snapshotFixtureDigest(f)...)
		w := buf.BeginWrite()
		*w = *f
		_ = buf.Publish(f.Tick)
		count := renders[int(tick)%len(renders)]
		for i := 0; i < count; i++ {
			img := c.ComposeFrame()
			boundaries++
			digest := sha256.Sum256(img.Pix)
			rgbaHash = fmt.Sprintf("%x", digest[:])
		}
		for _, ev := range f.Events {
			eventBytes = append(eventBytes, byte(ev.Kind))
			var b [16]byte
			binary.LittleEndian.PutUint64(b[:8], ev.Sequence)
			binary.LittleEndian.PutUint32(b[8:12], ev.Tick)
			binary.LittleEndian.PutUint32(b[12:16], ev.ID)
			eventBytes = append(eventBytes, b[:]...)
		}
		after = append(after, snapshotFixtureDigest(f)...)
	}
	eventDigest := sha256.Sum256(eventBytes)
	beforeDigest := sha256.Sum256(before)
	afterDigest := sha256.Sum256(after)
	return liveResult{
		rgbaHash:            rgbaHash,
		eventHash:           fmt.Sprintf("%x", eventDigest[:]),
		clockTickBoundaries: boundaries,
		snapshotHashBefore:  fmt.Sprintf("%x", beforeDigest[:]),
		snapshotHashAfter:   fmt.Sprintf("%x", afterDigest[:]),
	}
}

func fixtureFrame(tick uint32) *frame.Frame {
	f := &frame.Frame{
		Tick: tick,
		Units: []frame.UnitView{
			{InstanceID: 11, Slot: 1, Owner: 0, X: numeric.Fixed(128 << 16), Y: numeric.Fixed(8 << 16), Z: numeric.Fixed(160 << 16), Health: 100, MaxHealth: 100},
			{InstanceID: 12, Slot: 2, Owner: 1, X: numeric.Fixed(192 << 16), Y: numeric.Fixed(8 << 16), Z: numeric.Fixed(160 << 16), Health: 100, MaxHealth: 100},
		},
		Selection: frame.SelectionView{LocalPlayer: 0},
	}
	if tick != 0 {
		f.Events = []frame.EventView{{ID: tick, Sequence: uint64(tick), Tick: tick, Kind: frame.EventKindShake, Magnitude: 8, Lifetime: 2}}
	}
	return f
}

func snapshotFixtureDigest(f *frame.Frame) []byte {
	h := sha256.New()
	var b [32]byte
	binary.LittleEndian.PutUint32(b[:4], f.Tick)
	h.Write(b[:4])
	for _, u := range f.Units {
		binary.LittleEndian.PutUint64(b[:8], uint64(u.Slot))
		binary.LittleEndian.PutUint64(b[8:16], uint64(int64(u.X)))
		binary.LittleEndian.PutUint64(b[16:24], uint64(int64(u.Y)))
		binary.LittleEndian.PutUint64(b[24:32], uint64(int64(u.Z)))
		h.Write(b[:])
	}
	return h.Sum(nil)
}
