package aikit

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The empty catalog fixtures share only immutable admitted key identity.
var checkpointHostFixtureKeys = &content.CheckpointKeys{}

func newCheckpointHost(t *testing.T, brain Brain, persona Persona) *Host {
	t.Helper()
	m := &ai.Manager{Controller: ai.ControllerModern, BattleSeed: 37, Planner: HostPlanner{}, Catalog: &content.Catalog{}}
	var identity checkpoint.Identity
	identity.Content[0], identity.Config[0] = 0x11, 0x22
	if err := m.EnableCheckpointApplications(identity, checkpointHostFixtureKeys); err != nil {
		t.Fatal(err)
	}
	h := NewHost(m, brain, persona)
	m.Ext = h
	return h
}

func checkpointHostContext(t *testing.T, h *Host) *ai.CheckpointContext {
	t.Helper()
	c := ai.NewCheckpointContext(units.NewCheckpointContext(checkpointHostFixtureKeys), world.NewCheckpointContext(checkpointHostFixtureKeys))
	c.World.Terrain = h.m.Terrain
	if err := c.SetModernBindings(h.m, HostPlanner{}, ai.NewCheckpointModernPlanner(HostPlanner{}), CheckpointControllerSource(), checkpoint.NewBindingAuthority()); err != nil {
		t.Fatal(err)
	}
	return c
}

func checkpointHostBytes(t *testing.T, h *Host) []byte {
	t.Helper()
	return checkpointLeafBytes(t, func(e *checkpoint.Encoder) error {
		return h.WriteControllerCheckpoint(e, checkpointHostContext(t, h))
	})
}

func checkpointHostInitialHash() checkpoint.Digest {
	// Independent initial envelope for the authored identity/player/kind.
	v := append([]byte("NLCPAIST"), 2, 0)
	v = append(v, 0x11)
	v = append(v, make([]byte, 31)...)
	v = append(v, 0x22)
	v = append(v, make([]byte, 31)...)
	v = append(v, 0, 2)
	return sha256.Sum256(v)
}

func TestCheckpointHostBeforeBeginFraming(t *testing.T) {
	h := newCheckpointHost(t, &countBrain{}, Persona{ThinkEvery: 10, Reaction: 3, APM: 60, Burst: 4, Attention: 2, Skill: 7, Ambition: 8})
	if h.checkpointHistory != h.m.CheckpointApplicationHistory() {
		t.Fatal("host did not borrow its history at construction")
	}
	v := h.ControllerCheckpoint()
	wantValue := ai.ControllerCheckpoint{Present: true, NextBatchSerial: 1, ApplicationHash: checkpointHostInitialHash()}
	if v != wantValue {
		t.Fatalf("before-begin record = %+v, want %+v", v, wantValue)
	}
	// Full controller (72 bytes), Persona (29), zero executor (777), without
	// additional Host framing. Hash bytes are fixed, not length-prefixed.
	want := make([]byte, 8)
	hash := checkpointHostInitialHash()
	want = append(want, hash[:]...)
	want = append(want, make([]byte, 10)...)
	want = append(want, checkpointDecode(t, "0100000000000000000000000001")...)
	want = append(want, make([]byte, 8)...)
	want = append(want, checkpointDecode(t, "3c0000000800000002000000040000000003000000070000000a000000")...)
	want = append(want, make([]byte, 777)...)
	if got := checkpointHostBytes(t, h); !bytes.Equal(got, want) {
		t.Fatalf("host payload\ngot  %x\nwant %x", got, want)
	}
	var nilHost *Host
	if nilHost.ControllerCheckpoint() != (ai.ControllerCheckpoint{}) {
		t.Fatal("nil host was not absent")
	}
}

func TestCheckpointHostCompletedHistoryValues(t *testing.T) {
	h := newCheckpointHost(t, &countBrain{}, PersonaHard)
	serial := h.checkpointHistory.NextSerial()
	a := h.checkpointHistory.BeginAttempt(7, serial, 0, func(e *checkpoint.Encoder) error {
		e.U8(31) // authored test intent; production codecs are a separate unit
		return e.Err()
	})
	a.Finish(1, 1)
	state, err := h.checkpointHistory.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	v := h.ControllerCheckpoint()
	if v.NextBatchSerial != 2 || v.ApplicationCount != 1 || v.ApplicationHash != state.Hash || v.ApplicationHash == checkpointHostInitialHash() {
		t.Fatalf("completed history was not copied: %+v, history %+v", v, state)
	}
	data := checkpointHostBytes(t, h)
	if binary.LittleEndian.Uint64(data[:8]) != 1 || !bytes.Equal(data[8:40], state.Hash[:]) {
		t.Fatal("full host payload lost completed history")
	}
}

type checkpointDelayedBrain struct {
	entered, release, thought chan struct{}
}

func (*checkpointDelayedBrain) Name() string { return "checkpoint-delayed" }
func (b *checkpointDelayedBrain) Init(*Kit) {
	close(b.entered)
	<-b.release
}
func (b *checkpointDelayedBrain) Think(k *Kit, _ *Obs) {
	k.Rand.Uint32()
	select {
	case <-b.thought:
	default:
		close(b.thought)
	}
}

func TestCheckpointHostWaitingDeadlineAndEmptyBatchSerial(t *testing.T) {
	b := &checkpointDelayedBrain{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	h := newCheckpointHost(t, b, Persona{ThinkEvery: 10, Reaction: 3, APM: 60, Burst: 4, Async: true})
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(b.release) }); h.Close() })
	w, econ := units.NewSliced(4, nil), computerEconomy()
	h.Step(1, w, econ)
	<-b.entered
	v := h.ControllerCheckpoint()
	if !v.Initialized || !v.NextThinkPresent || v.NextThinkTick != 10 || v.DeadlinePresent || v.NextBatchSerial != 1 {
		t.Fatalf("before first think: %+v", v)
	}
	h.Step(10, w, econ) // preparation remains blocked; the batch is assigned now
	before := h.ControllerCheckpoint()
	if !before.DeadlinePresent || before.DeadlineTick != 13 || before.NextThinkTick != 20 || before.NextBatchSerial != 2 || h.checkpointBatchSerial != 1 {
		t.Fatalf("scheduled empty batch: %+v, serial %d", before, h.checkpointBatchSerial)
	}
	if before.Tokens != h.ex.tokens || before.LastFill != h.ex.lastFill || before.Tokens != 4000 || before.LastFill != 10 {
		t.Fatalf("scheduled budget did not copy actual executor values: %+v", before)
	}
	// Capture must finish while preparation cannot. A worker join in either
	// provider path would deadlock this call until the test's timeout.
	type captured struct {
		value ai.ControllerCheckpoint
		err   error
	}
	capturedCh := make(chan captured, 1)
	go func() {
		var summary checkpoint.Summary
		capturedCh <- captured{h.ControllerCheckpoint(), h.AppendControllerCheckpointSummary(&summary)}
	}()
	select {
	case got := <-capturedCh:
		if got.err != nil || got.value != before {
			t.Fatalf("capture during preparation = %+v, %v", got.value, got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("checkpoint waited for preparation")
	}
	if err := h.WriteControllerCheckpoint(checkpoint.NewEncoder(io.Discard), checkpointHostContext(t, h)); err != nil {
		t.Fatalf("admitted full capture during blocked preparation: %v", err)
	}
	h.Step(11, w, econ)
	h.Step(12, w, econ)
	if got := h.ControllerCheckpoint(); got != before {
		t.Fatalf("waiting changed scheduling metadata: %+v", got)
	}
	release.Do(func() { close(b.release) })
	<-b.thought
	if got := h.ControllerCheckpoint(); got != before {
		t.Fatalf("worker completion changed capture: %+v", got)
	}
	h.Step(13, w, econ)
	after := h.ControllerCheckpoint()
	if after.DeadlinePresent || after.DeadlineTick != 13 || after.NextBatchSerial != 2 || after.ApplicationCount != 0 || after.ApplicationHash != before.ApplicationHash || h.checkpointBatchSerial != 1 || after.LastFill != 13 {
		t.Fatalf("applied empty batch: %+v, serial %d", after, h.checkpointBatchSerial)
	}
	h.Step(20, w, econ)
	if got := h.ControllerCheckpoint(); !got.DeadlinePresent || got.DeadlineTick != 23 || got.NextBatchSerial != 3 || h.checkpointBatchSerial != 2 {
		t.Fatalf("second empty batch: %+v, serial %d", got, h.checkpointBatchSerial)
	}
}

type checkpointSchedulingProbe struct {
	h       *Host
	atThink []ai.ControllerCheckpoint
}

func (*checkpointSchedulingProbe) StepBegin(uint8, uint32) {}
func (*checkpointSchedulingProbe) StepEnd(uint8, uint32)   {}
func (p *checkpointSchedulingProbe) ThinkBegin(uint8, uint32) {
	p.atThink = append(p.atThink, p.h.ControllerCheckpoint())
}
func (*checkpointSchedulingProbe) ThinkEnd(uint8, uint32) {}

func TestCheckpointHostReactionZero(t *testing.T) {
	b := &countBrain{}
	h := newCheckpointHost(t, b, Persona{ThinkEvery: 10, Reaction: 0, APM: 60, Burst: 3})
	defer h.Close()
	p := &checkpointSchedulingProbe{h: h}
	h.Probe = p
	w, econ := units.NewSliced(4, nil), computerEconomy()
	h.Step(0, w, econ)
	if len(p.atThink) != 1 || !p.atThink[0].DeadlinePresent || p.atThink[0].DeadlineTick != 0 || p.atThink[0].NextBatchSerial != 2 {
		t.Fatalf("zero-tick pending deadline was not present before think: %+v", p.atThink)
	}
	v := h.ControllerCheckpoint()
	if v.DeadlinePresent || v.DeadlineTick != 0 || v.NextBatchSerial != 2 || h.checkpointBatchSerial != 1 || b.thinks != 1 || h.checkpointApplying {
		t.Fatalf("reaction-zero boundary: %+v, serial %d, thinks %d", v, h.checkpointBatchSerial, b.thinks)
	}
	h.Step(10, w, econ)
	v = h.ControllerCheckpoint()
	if v.DeadlinePresent || v.DeadlineTick != 10 || v.NextBatchSerial != 3 || h.checkpointBatchSerial != 2 || b.thinks != 2 || v.Tokens != h.ex.tokens || v.LastFill != h.ex.lastFill {
		t.Fatalf("second synchronous batch: %+v, serial %d", v, h.checkpointBatchSerial)
	}
}

func TestCheckpointHostSummarySelectedAtomicAndAllocationFree(t *testing.T) {
	h := newCheckpointHost(t, &countBrain{}, PersonaHard)
	h.nextThink, h.checkpointDeadlineTick = 17, 19 // retain false-presence residuals
	h.ex.tokens, h.ex.lastFill = -23, 29
	h.ex.m, h.ex.table = h.m, &Table{} // cheap summary does not admit full bindings
	h.ex.spotCover = []bool{true, false}
	var prefix checkpoint.Summary
	prefix.Word(31)
	got, want := prefix, prefix
	hash := checkpointHostInitialHash()
	words := []uint64{0}
	for i := 0; i < 32; i += 8 {
		words = append(words, binary.LittleEndian.Uint64(hash[i:i+8]))
	}
	negativeTokens := int64(-23)
	words = append(words, 0, 17, 0, 19, uint64(negativeTokens), 29)
	for _, word := range words {
		want.Word(word)
	}
	if err := h.AppendControllerCheckpointSummary(&got); err != nil || got != want {
		t.Fatalf("summary = %v, want %v, err %v", got, want, err)
	}
	if n := testing.AllocsPerRun(100, func() {
		s := prefix
		if err := h.AppendControllerCheckpointSummary(&s); err != nil {
			panic(err)
		}
	}); n != 0 {
		t.Fatalf("summary allocations = %g", n)
	}
	h.ex.spotCover[0] = false
	got = prefix
	if err := h.AppendControllerCheckpointSummary(&got); err != nil || got != want {
		t.Fatal("summary traversed nonselected executor cache")
	}
	if err := h.AppendControllerCheckpointSummary(nil); err == nil {
		t.Fatal("nil summary accepted")
	}
}

func TestCheckpointHostBoundaryRefusals(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Host)
	}{
		{"missing", func(h *Host) { h.checkpointHistory = nil }},
		{"manager", func(h *Host) { h.m = nil }},
		{"foreign", func(h *Host) { h.checkpointHistory, _ = ai.NewApplicationHistory(checkpoint.Identity{}, 0, 2) }},
		{"player", func(h *Host) { h.m.Player = 1 }},
		{"kind", func(h *Host) { h.m.Controller = ai.ControllerClassic }},
		{"applying", func(h *Host) { h.checkpointApplying = true }},
		{"activeHistory", func(h *Host) {
			s := h.checkpointHistory.NextSerial()
			h.checkpointHistory.BeginAttempt(7, s, 0, func(*checkpoint.Encoder) error { return nil })
		}},
		{"failedHistory", func(h *Host) { h.checkpointHistory.Fail(errors.New("authored failure")) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newCheckpointHost(t, &countBrain{}, PersonaHard)
			h.inited, h.nextThink, h.ex.tokens = true, 7, -9
			tc.edit(h)
			v := h.ControllerCheckpoint()
			if !v.Present || !v.Initialized || v.NextThinkTick != 7 || v.Tokens != -9 {
				t.Fatalf("no-error value concealed invalid but present host: %+v", v)
			}
			if tc.name == "missing" || tc.name == "activeHistory" || tc.name == "failedHistory" {
				if v.NextBatchSerial != 0 || v.ApplicationCount != 0 || v.ApplicationHash != (checkpoint.Digest{}) {
					t.Fatalf("unsuccessful snapshot supplied chain values: %+v", v)
				}
			}
			var sum checkpoint.Summary
			sum.Word(47)
			before := sum
			if err := h.AppendControllerCheckpointSummary(&sum); err == nil || sum != before {
				t.Fatalf("summary refusal = %v, changed = %v", err, sum != before)
			}
			var out bytes.Buffer
			err := h.WriteControllerCheckpoint(checkpoint.NewEncoder(&out), executorCheckpointContext())
			if err == nil || out.Len() != 0 || !strings.HasPrefix(err.Error(), "nanolathe: ") || !strings.Contains(err.Error(), "logical path aikit.Host") {
				t.Fatalf("full refusal = %v, bytes = %x", err, out.Bytes())
			}
		})
	}
	var h *Host
	if err := h.AppendControllerCheckpointSummary(&checkpoint.Summary{}); err == nil {
		t.Fatal("nil host summary accepted")
	}
	if err := h.WriteControllerCheckpoint(checkpoint.NewEncoder(io.Discard), executorCheckpointContext()); err == nil {
		t.Fatal("nil host writer accepted")
	}
	h = newCheckpointHost(t, &countBrain{}, PersonaHard)
	if err := h.WriteControllerCheckpoint(nil, executorCheckpointContext()); err == nil {
		t.Fatal("nil encoder accepted")
	}
	// A history installed after construction is not silently borrowed later.
	m := &ai.Manager{Controller: ai.ControllerModern}
	h = NewHost(m, &countBrain{}, PersonaHard)
	if err := m.EnableCheckpointApplications(checkpoint.Identity{}, &content.CheckpointKeys{}); err != nil {
		t.Fatal(err)
	}
	if err := h.AppendControllerCheckpointSummary(&checkpoint.Summary{}); err == nil || h.checkpointHistory != nil {
		t.Fatal("host adopted a late history")
	}
}

func checkpointProducerHost(t *testing.T) (*Host, *units.World) {
	t.Helper()
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "factory"}, UnitName: "factory", ObjectName: "fixture", MaxDamage: 100}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"factory": def}}
	m := &ai.Manager{Controller: ai.ControllerModern, Catalog: cat}
	if err := m.EnableCheckpointApplications(checkpoint.Identity{}, checkpointCommandKeys(t, cat)); err != nil {
		t.Fatal(err)
	}
	h := NewHost(m, &countBrain{}, Persona{APM: 60, Burst: 3})
	h.ex.table = BuildTable(cat, nil)
	w := fixtureWorld(cat)
	handle, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	h.ex.m = h.m
	h.b = batch{pending: true, due: 7, actors: []pool.Handle{handle}, inst: []*units.Unit{w.Unit(handle)},
		cmds: []Command{{Kind: CmdProduce, Product: h.ex.table.Of(def), count: 1, Count: 2}}}
	h.checkpointDeadlinePresent, h.checkpointDeadlineTick = true, 7
	h.checkpointBatchSerial = h.checkpointHistory.NextSerial()
	return h, w
}

func TestCheckpointHostActiveApplyMarkerAndUnwind(t *testing.T) {
	h, w := checkpointProducerHost(t)
	h.ex.checkpointBatchSerial = 37
	called := 0
	h.m.SetQueueBuildTyped(func(req ai.BuildRequest) error {
		called++
		if !h.checkpointApplying || !h.ControllerCheckpoint().DeadlinePresent || h.ex.checkpointBatchSerial != h.checkpointBatchSerial {
			t.Fatal("callback observed no active application/deadline")
		}
		var s checkpoint.Summary
		s.Word(11)
		before := s
		if err := h.AppendControllerCheckpointSummary(&s); err == nil || !strings.Contains(err.Error(), "checkpointApplying") || s != before {
			t.Fatalf("active callback summary = %v, changed = %v", err, s != before)
		}
		if err := h.WriteControllerCheckpoint(checkpoint.NewEncoder(io.Discard), executorCheckpointContext()); err == nil || !strings.Contains(err.Error(), "checkpointApplying") {
			t.Fatalf("active callback full capture = %v", err)
		}
		return ai.WithCheckpointBuildVerdict(errors.New("ordinary rejected build"), ai.CheckpointBuildProduct)
	})
	h.applyBatch(7, w)
	if called != 1 || h.checkpointApplying || h.checkpointDeadlinePresent || h.checkpointDeadlineTick != 7 || h.ex.stats.Failed != 1 || h.ex.tokens != 2000 || h.ex.checkpointBatchSerial != 37 {
		t.Fatalf("application behavior/marker: calls %d, value %+v, stats %+v", called, h.ControllerCheckpoint(), h.ex.stats)
	}
	if err := h.AppendControllerCheckpointSummary(&checkpoint.Summary{}); err != nil {
		t.Fatal(err)
	}
	for _, previous := range []bool{false, true} {
		h.checkpointApplying, h.checkpointDeadlinePresent = previous, true
		h.m.SetQueueBuildTyped(func(ai.BuildRequest) error { panic("authored producer panic") })
		func() {
			defer func() {
				if recover() == nil {
					t.Error("producer panic was changed")
				}
			}()
			h.applyBatch(8, w)
		}()
		if h.checkpointApplying != previous || !h.checkpointDeadlinePresent || h.ex.checkpointBatchSerial != 37 {
			t.Fatalf("panic unwind lost enclosing state: previous %v, applying %v, pending %v", previous, h.checkpointApplying, h.checkpointDeadlinePresent)
		}
	}
}

func TestCheckpointHostCaptureIgnoresWorkerStorage(t *testing.T) {
	h := newCheckpointHost(t, &countBrain{}, PersonaHard)
	h.begin(1, units.NewSliced(4, nil))
	h.Join()
	h.inited, h.nextThink, h.checkpointDeadlinePresent, h.checkpointDeadlineTick = true, 17, true, 19
	before := checkpointHostBytes(t, h)
	value := h.ControllerCheckpoint()
	var summary checkpoint.Summary
	if err := h.AppendControllerCheckpointSummary(&summary); err != nil {
		t.Fatal(err)
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		m, o, b := &MapInfo{}, &Obs{}, &countBrain{}
		for {
			select {
			case <-stop:
				return
			default:
				h.ready = !h.ready
				h.flight = stop
				h.kit, h.obs, h.rand, h.brain = Kit{Tick: 101}, Obs{Tick: 102}, Rand{state: 103}, b
				h.ex.mapInfo, h.ex.obs = m, o
				h.b = batch{pending: true, due: 104, rowNear: 105}
				h.b = batch{}
				h.Thinks++
			}
		}
	}()
	defer func() { close(stop); <-done }()
	for i := 0; i < 20; i++ {
		var s checkpoint.Summary
		if err := h.AppendControllerCheckpointSummary(&s); err != nil || s != summary || h.ControllerCheckpoint() != value || !bytes.Equal(before, checkpointHostBytes(t, h)) {
			t.Fatalf("worker storage changed capture: %v", err)
		}
	}
}

type checkpointDrawCommandBrain struct {
	draws []uint32
}

func (*checkpointDrawCommandBrain) Name() string  { return "checkpoint-draw-command" }
func (b *checkpointDrawCommandBrain) Init(k *Kit) { b.draws = append(b.draws, k.Rand.Uint32()) }
func (b *checkpointDrawCommandBrain) Think(k *Kit, o *Obs) {
	b.draws = append(b.draws, k.Rand.Uint32())
	if len(o.Own) > 0 {
		k.Produce(o.Own[0].H, o.Own[0].Info, 2)
	}
}

func TestCheckpointHostSchedulingPreservesCommandsAndRandom(t *testing.T) {
	type outcome struct {
		requests []ai.BuildRequest
		upkeep   int
		draws    []uint32
		position uint64
		stats    ApplyStats
		tokens   int64
		lastFill uint32
	}
	run := func(async, diagnostics, failed bool) outcome {
		t.Helper()
		b := &checkpointDrawCommandBrain{}
		p := Persona{ThinkEvery: 10, Reaction: 3, APM: 60, Burst: 3, Async: async}
		def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "factory"}, UnitName: "factory", ObjectName: "fixture", MaxDamage: 100}
		cat := &content.Catalog{Units: map[string]*content.UnitDef{"factory": def}}
		m := &ai.Manager{Controller: ai.ControllerModern, BattleSeed: 37, Catalog: cat}
		var h *Host
		if diagnostics {
			if err := m.EnableCheckpointApplications(checkpoint.Identity{}, checkpointCommandKeys(t, cat)); err != nil {
				t.Fatal(err)
			}
			h = NewHost(m, b, p)
			if failed {
				h.checkpointHistory.Fail(errors.New("authored diagnostic failure"))
			}
		} else {
			h = NewHost(m, b, p)
		}
		defer h.Close()
		w := fixtureWorld(cat)
		if _, err := w.Create(def, 0, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
		h.m.Catalog = cat
		var result outcome
		h.m.SetQueueBuildTyped(func(req ai.BuildRequest) error { result.requests = append(result.requests, req); return nil })
		h.m.SetWeaponMaintenance(func(uint8) { result.upkeep++ })
		econ := computerEconomy()
		for tick := uint32(1); tick < 40; tick++ {
			h.Step(tick, w, econ)
			if diagnostics && !failed {
				if err := h.AppendControllerCheckpointSummary(&checkpoint.Summary{}); err != nil {
					t.Fatal(err)
				}
			}
		}
		h.Join()
		result.draws, result.position, result.stats = b.draws, h.rand.Position(), h.ex.stats
		result.tokens, result.lastFill = h.ex.tokens, h.ex.lastFill
		return result
	}
	baseline := run(false, false, false)
	if len(baseline.requests) == 0 || len(baseline.draws) < 2 || baseline.upkeep == 0 {
		t.Fatalf("comparison lacked executed work: %+v", baseline)
	}
	for _, opts := range [][3]bool{{false, true, false}, {true, false, false}, {true, true, false}, {false, true, true}, {true, true, true}} {
		if got := run(opts[0], opts[1], opts[2]); !reflect.DeepEqual(got, baseline) {
			t.Fatalf("async/diagnostics/failed %v\ngot %+v\nwant %+v", opts, got, baseline)
		}
	}
}
