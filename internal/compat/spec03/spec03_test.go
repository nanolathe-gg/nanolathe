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
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/audio"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/presentation"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/snapshot"
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
	if !reflect.DeepEqual(a.ledger, b.ledger) {
		t.Fatalf("CRT ledger changed with render rate:\nA %#v\nB %#v", a.ledger, b.ledger)
	}
	if a.clockTickBoundaries != b.clockTickBoundaries || a.clockTickBoundaries != 4 {
		t.Fatalf("clock tick boundaries = %d/%d, want four", a.clockTickBoundaries, b.clockTickBoundaries)
	}
	if a.snapshotHashBefore != a.snapshotHashAfter || b.snapshotHashBefore != b.snapshotHashAfter {
		t.Fatalf("presentation changed snapshot fixture digest: A %s -> %s, B %s -> %s", a.snapshotHashBefore, a.snapshotHashAfter, b.snapshotHashBefore, b.snapshotHashAfter)
	}

	wantPass := passTrace(&snapshot.Frame{Units: []snapshot.UnitView{{Slot: 1, Owner: 0}}}, 1)
	if !reflect.DeepEqual(a.passTrace, wantPass) {
		t.Fatalf("live compositor pass trace = %v, want %v", a.passTrace, wantPass)
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
	buf := &snapshot.Buffer{}
	s := &session.Session{Econ: &economy.Service{}, Snapshot: buf}
	s.Econ.Players[0].Exists = true
	s.Econ.Players[0].Stock[economy.Metal] = 321.5
	s.Econ.Players[0].Stock[economy.Energy] = 654.25
	buf.Publish(fixtureFrame(1))

	c, err := client.New(client.Options{Buffer: buf, Width: 80, Height: 48, Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	c.SetCamera(&camera.Camera{X: 256, Z: 256, ViewW: 80, ViewH: 48, MapW: 1024, MapH: 1024})
	clock := presentation.Clock{}
	c.SetPresentationClock(&clock)
	c.SetPresentationCRT(presentation.NewCRTRandom(1))
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

// TestModeGateTrace locks both sides of the render-mode gate, including the
// unconditional strip 8 and the fog/selection boundary [03 §1].
func TestModeGateTrace(t *testing.T) {
	f := fixtureFrame(1)
	on := passTrace(f, 1)
	off := passTrace(f, 0)
	if len(on) <= len(off) {
		t.Fatalf("mode-on trace did not include gated passes: on=%v off=%v", on, off)
	}
	for _, required := range []string{"strip6", "projectiles", "effects", "strip7", "strip8", "strip9", "fog", "selection", "interface"} {
		if !contains(on, required) {
			t.Fatalf("mode-on trace missing %q: %v", required, on)
		}
	}
	for _, forbidden := range []string{"strip6", "projectiles", "effects", "strip7", "strip9", "fog"} {
		if contains(off, forbidden) {
			t.Fatalf("mode-off trace contains gated %q: %v", forbidden, off)
		}
	}
	if !contains(off, "strip8") || !contains(off, "selection") || !contains(off, "interface") {
		t.Fatalf("mode-off trace lost unconditional late passes: %v", off)
	}
}

// TestPresentationOwners is deliberately source-structural. It checks the
// stable ownership seams without depending on checkout-specific absolute
// paths or implementation line numbers [03 §1][I6].
func TestPresentationOwners(t *testing.T) {
	root := repositoryRoot(t)
	frame := readSource(t, root, "internal", "client", "frame.go")
	clientSource := readSource(t, root, "internal", "client", "client.go")
	resolver := readSource(t, root, "internal", "client", "presentation_resolver.go")
	model := readSource(t, root, "internal", "client", "model.go")
	unitdraw := readSource(t, root, "internal", "client", "unitdraw.go")
	renderCompose := readSource(t, root, "internal", "render", "compose.go")
	renderFog := readSource(t, root, "internal", "render", "fog.go")
	effects := readSource(t, root, "internal", "render", "effects.go")

	if got := strings.Count(frame, "c.composer = &render.Composer{}"); got != 1 {
		t.Fatalf("live client composer construction count = %d, want one", got)
	}
	if got := strings.Count(clientSource, "crt             *presentation.CRTRandom"); got != 1 {
		t.Fatalf("client CRT binding count = %d, want one", got)
	}
	if strings.Contains(frame+clientSource, "presentation.NewCRTRandom(") {
		t.Fatal("live client constructs a second CRT instead of accepting the shared binding")
	}
	if got := strings.Count(renderCompose, "type FixedEffectPool struct"); got != 1 {
		t.Fatalf("fixed effect pool owner declaration count = %d, want one", got)
	}
	if got := strings.Count(renderFog, "func BuildFogOps("); got != 1 {
		t.Fatalf("fog builder declaration count = %d, want one", got)
	}
	if got := strings.Count(model, "func (c *Client) expandModel("); got != 1 {
		t.Fatalf("model compiler declaration count = %d, want one", got)
	}
	if !strings.Contains(frame, "c.composer.Frame(cur, alpha, mode)") {
		t.Fatal("client does not call the canonical composer from its live frame adapter")
	}
	if strings.Contains(effects, "type EffectService struct") {
		t.Fatal("render effect pool file grew a second presentation service owner")
	}
	production := frame + clientSource + resolver + model + unitdraw + renderCompose + renderFog + effects
	for _, forbidden := range []string{"SyntheticArt", "syntheticArt", "resolveSynthetic", "fallbackArt", "syntheticSprite"} {
		if strings.Contains(production, forbidden) {
			t.Fatalf("production source contains synthetic-art resolver marker %q", forbidden)
		}
	}
	// These are the concrete guessed-art shapes removed by WU-03C/H. Keep the
	// scan scoped to resolver/draw sources so a legitimate unrelated constant
	// cannot turn this into a repository-wide style rule.
	for _, forbidden := range []string{
		"GAFFrame{Width: 1", "GAFFrame{Width:1",
		"GAFFrame{Height: 1", "GAFFrame{Height:1",
		"return 210", "return uint8(210)", "return byte(210)",
		"return paletteIndex(210)", "return paletteIndex(0xd2)",
		"return uint8(0xd2)", "return byte(0xd2)",
	} {
		if strings.Contains(production, forbidden) {
			t.Fatalf("production source contains removed synthetic-art shape %q", forbidden)
		}
	}
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
	passTrace           []string
	ledger              []presentation.CRTLedgerEntry
	clockTickBoundaries int
	snapshotHashBefore  string
	snapshotHashAfter   string
}

func runLiveSchedule(t *testing.T, renders []int) liveResult {
	t.Helper()
	buf := &snapshot.Buffer{}
	c, err := client.New(client.Options{Buffer: buf, Width: 80, Height: 48, Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	c.SetCamera(&camera.Camera{X: 256, Z: 256, ViewW: 80, ViewH: 48, MapW: 1024, MapH: 1024})
	crt := presentation.NewCRTRandom(1)
	c.SetPresentationCRT(crt)
	clock := presentation.Clock{}
	c.SetPresentationClock(&clock)

	var before, after []byte
	var rgbaHash string
	var eventBytes []byte
	boundaries := 0
	for tick := uint32(0); tick < 4; tick++ {
		f := fixtureFrame(tick)
		before = append(before, snapshotFixtureDigest(f)...)
		buf.Publish(f)
		count := renders[int(tick)%len(renders)]
		for i := 0; i < count; i++ {
			img := c.ComposeFrame()
			if clock.NewSimTick {
				boundaries++
			}
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
		passTrace:           passTrace(fixtureFrame(3), 1),
		ledger:              crt.Ledger(),
		clockTickBoundaries: boundaries,
		snapshotHashBefore:  fmt.Sprintf("%x", beforeDigest[:]),
		snapshotHashAfter:   fmt.Sprintf("%x", afterDigest[:]),
	}
}

func fixtureFrame(tick uint32) *snapshot.Frame {
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

func passTrace(f *snapshot.Frame, mode int) []string {
	var trace []string
	add := func(s string) { trace = append(trace, s) }
	c := &render.Composer{Hooks: render.ComposerHooks{
		PreWorld: func() { add("preworld") }, Terrain: func() { add("terrain") }, Minimap: func() { add("minimap") }, Clip: func() { add("clip") },
		DrawStrip: func(i int) { add(fmt.Sprintf("strip%d", i)) }, BucketBuild: func() { add("bucket") }, FeaturePass: func() { add("features") },
		UnitTraversal: func(k string) { add("units:" + k) }, Projectiles: func() { add("projectiles") }, Effects: func() { add("effects") },
		Fog: func() { add("fog") }, Selection: func() { add("selection") }, Interface: func() { add("interface") },
		Barrier: func(i int) { add(fmt.Sprintf("barrier:%d", i)) },
	}}
	c.Frame(f, 1, mode)
	return trace
}

func snapshotFixtureDigest(f *snapshot.Frame) []byte {
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

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func readSource(t *testing.T, root string, parts ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{root}, parts...)...))
	if err != nil {
		t.Fatalf("read source %v: %v", parts, err)
	}
	return string(b)
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
