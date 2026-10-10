package replay_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/lockstep"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/relay"
	"github.com/nanolathe-gg/nanolathe/internal/replay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// onlineConfig is a two-human Modern online skirmish on the portable map,
// shaped as the online host shapes it (cmd/nanolathe onlineMatchConfig).
func onlineConfig(t *testing.T) session.EffectiveMatchConfig {
	t.Helper()
	setup := session.DirectSkirmishConfig(portableMap)
	setup.Gameplay = gameplay.Modern
	setup.UnitLimit, setup.Location = 20, 1
	setup.RNGSimSeed, setup.RNGCrtSeed = 7, 11
	zero := 0
	builder := orders.DefaultBuilderOptions()
	options := session.SkirmishEntryOptions{BuilderOptions: &builder, CommunitySources: session.CommunitySources{Player: community.Overrides{UnitLimit: &zero}}}
	view := session.MatchView{MinimumScale: 64, MaximumScale: 2048, FullMap: true}
	room := session.MatchRoomInputs{ContentProfile: "retail", PlayerView: view, SpectatorView: view, ReplayView: view,
		Policies: session.MatchPolicies{Revision: 1, Scheduling: 1, Pacing: 1, Drop: 1, Audience: 1, RejoinGraceMilliseconds: 90000}}
	room.Participants[0][0] = 1
	request, err := session.NewMatchConfigRequest(setup, options, room)
	if err != nil {
		t.Fatal(err)
	}
	second := &request.Seats[1]
	second.Role, second.HostSeat = session.MatchRoleHuman, session.MatchHostNone
	second.ComputerKind, second.Difficulty, second.AIParams = 0, 0, nil
	second.Participant[0] = 2
	second.BuilderOptions = builder
	config, err := session.ResolveMatchConfig(request)
	if err != nil {
		t.Fatal(err)
	}
	return config
}

// queueClient is a lockstep.Client serving grants the test seals, standing
// in for the relay.
type queueClient struct {
	grants []relay.LocalGrant
	acks   []uint32
}

func (c *queueClient) Submit([]byte) (uint64, error) { return 0, errors.New("unused") }
func (c *queueClient) ReadGrant() (relay.LocalGrant, error) {
	if len(c.grants) == 0 {
		return relay.LocalGrant{}, io.EOF
	}
	g := c.grants[0]
	c.grants = c.grants[1:]
	return g, nil
}
func (c *queueClient) Acknowledge(tick uint32, _ [32]byte, _, _ bool) error {
	c.acks = append(c.acks, tick)
	return nil
}
func (c *queueClient) Close() error { return nil }

// commanders are each seat's one unit.
func commanders(t *testing.T, s *session.Session) [2]*units.Unit {
	t.Helper()
	var out [2]*units.Unit
	for _, u := range s.Units.Iter() {
		if u.Owner < 2 {
			out[u.Owner] = u
		}
	}
	if out[0] == nil || out[1] == nil {
		t.Fatal("missing a commander")
	}
	return out
}

// onlineRecording is a recorded online battle and what its recording seat
// computed.
type onlineRecording struct {
	data     []byte
	final    [32]byte
	receipts []session.CommandReceipt
	ticks    uint32
}

// recordOnline plays ticks granted ticks of the portable online battle as
// seat 0, through the recorder wrapping the relay client exactly as the
// driver uses it: read a grant, queue its commands, run its tick,
// acknowledge it with every 30th tick's checksum. Both seats move their
// commanders, and seat 1 stops its own now and then.
func recordOnline(t *testing.T, fs vfs.FSOps, ticks uint32) onlineRecording {
	t.Helper()
	config := onlineConfig(t)
	inputs, err := session.FreezeMatchInputs(fs, nil, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := session.MatchJoin{Inputs: inputs, Config: config}.Identity()
	if err != nil {
		t.Fatal(err)
	}
	s, err := session.NewPlaytestSkirmish(inputs, config, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PrepareGrantedBattle(); err != nil {
		t.Fatal(err)
	}
	header, err := replay.OnlineHeader(s, config, identity)
	if err != nil {
		t.Fatal(err)
	}
	header.Started = 1760000000000
	path := filepath.Join(t.TempDir(), "online.nlreplay")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w, err := replay.NewWriter(file, header)
	if err != nil {
		t.Fatal(err)
	}
	relayClient := &queueClient{}
	client, err := replay.NewOnlineRecorder(w, relayClient)
	if err != nil {
		t.Fatal(err)
	}
	units := commanders(t, s)
	startX, startZ := units[0].X, units[0].Z
	var out onlineRecording
	position := uint64(0)
	for tick := uint32(1); tick <= ticks; tick++ {
		g := relay.LocalGrant{Tick: tick}
		if tick%20 == 0 {
			seat := uint8(tick/20) % 2
			u := units[seat]
			human := session.HumanCommand{Kind: session.HumanOrder, Order: session.HumanOrderCommand{Handles: []pool.Handle{u.Handle}, Code: 2,
				Position: orders.ResolvePos{X: u.X + numeric.Fixed(tick%96)<<16, Y: u.Y, Z: u.Z + numeric.Fixed(tick%64)<<16}}}
			if seat == 1 && tick%100 == 0 {
				human = session.HumanCommand{Kind: session.HumanStop, Stop: session.HumanStopCommand{Handles: []pool.Handle{u.Handle}}}
			}
			c, err := s.CaptureOnlineCommand(human)
			if err != nil {
				t.Fatal(err)
			}
			payload, err := session.EncodeSeatCommand(session.OnlineCommand, c)
			if err != nil {
				t.Fatal(err)
			}
			position++
			g.Commands = append(g.Commands, relay.LocalCommand{Seat: seat, Sequence: position, Position: position, Payload: payload})
		}
		g.Position = position
		relayClient.grants = append(relayClient.grants, g)
		got, err := client.ReadGrant()
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range got.Commands {
			command, err := session.DecodeSeatCommand(session.OnlineCommand, c.Payload)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.EnqueueSeatCommand(session.CommandStamp{Seat: c.Seat, Tick: got.Tick, Position: c.Position}, command); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.StepGranted(got.Tick); err != nil {
			t.Fatal(err)
		}
		out.receipts = append(out.receipts, s.DrainCommandReceipts()...)
		var sum [32]byte
		if tick%30 == 0 {
			sum = s.UnitStateChecksum()
		}
		if err := client.Acknowledge(tick, sum, false, false); err != nil {
			t.Fatal(err)
		}
		if tick%200 == 0 {
			if err := w.Flush(); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(relayClient.acks) != int(ticks) || w.Stopped() != nil {
		t.Fatalf("relay saw %d acknowledgements; recording stopped: %v", len(relayClient.acks), w.Stopped())
	}
	if units[0].X == startX && units[0].Z == startZ {
		t.Fatal("the script moved nothing")
	}
	if err := w.Close(replay.EndFinished); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	out.data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out.final, out.ticks = s.UnitStateChecksum(), ticks
	return out
}

// play composes a recording and plays it to its end in steps of at most
// maxTicks, returning the player, the session and the receipts it drained.
func play(t *testing.T, fs vfs.FSOps, data []byte, maxTicks int) (*replay.Player, *session.Session, []session.CommandReceipt, error) {
	t.Helper()
	r, err := replay.NewReader(data)
	if err != nil {
		t.Fatal(err)
	}
	s, err := replay.Compose(r.Header(), replay.Content{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	p, err := replay.NewPlayer(s, r)
	if err != nil {
		t.Fatal(err)
	}
	var receipts []session.CommandReceipt
	for {
		n, err := p.Step(maxTicks)
		receipts = append(receipts, s.DrainCommandReceipts()...)
		if err == io.EOF {
			return p, s, receipts, nil
		}
		if err != nil {
			return p, s, receipts, err
		}
		if n == 0 || n > maxTicks && n > 1 {
			t.Fatalf("step ran %d ticks for at most %d", n, maxTicks)
		}
	}
}

// rewrite re-records a replay entry by entry, letting edit change entries
// or drop them.
func rewrite(t *testing.T, data []byte, edit func(*replay.Entry) bool) []byte {
	t.Helper()
	return rewriteHeader(t, data, func(*replay.Header) {}, edit)
}

// rewriteHeader is rewrite with an edited header.
func rewriteHeader(t *testing.T, data []byte, header func(*replay.Header), edit func(*replay.Entry) bool) []byte {
	t.Helper()
	r, err := replay.NewReader(data)
	if err != nil {
		t.Fatal(err)
	}
	h := r.Header()
	header(&h)
	var out bytes.Buffer
	w, err := replay.NewWriter(&out, h)
	if err != nil {
		t.Fatal(err)
	}
	for {
		e, err := r.Next()
		if err == io.EOF {
			return out.Bytes()
		}
		if err != nil {
			t.Fatal(err)
		}
		if !edit(&e) {
			continue
		}
		switch e.Kind {
		case replay.EntryCommand:
			err = w.Command(e.Tick, e.Seat, e.Position, e.Payload)
		case replay.EntryChecksum:
			err = w.Checksum(e.Tick, e.Sum)
		case replay.EntryPumps:
			tick := e.Tick - e.Pumps*e.PumpTicks
			for range e.Pumps {
				tick += e.PumpTicks
				if err = w.Pump(tick, int(e.PumpTicks)); err != nil {
					break
				}
			}
		case replay.EntryEnd:
			err = w.Close(e.Reason)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

// An online seat's recording of the relay stream plays back headless to the
// same state, matching every acknowledged checksum; a recording cut short
// plays to its last whole chunk; a changed checksum stops playback at its
// tick; and an install with other content refuses to compose it. Asset-free:
// the battle is the authored portable install.
func TestOnlineRecordingPlaysBack(t *testing.T) {
	fs := portableFS(t)
	rec := recordOnline(t, fs, 600)
	t.Logf("online, 2 seats, %d ticks: %d bytes", rec.ticks, len(rec.data))

	sum, err := replay.Summarize(rec.data)
	if err != nil {
		t.Fatal(err)
	}
	if sum.FinalTick != rec.ticks || sum.End != replay.EndFinished || sum.Header.Kind != replay.KindOnlineSkirmish ||
		len(sum.Header.Seats) != 2 || sum.Header.Seats[1].Side != "CORE" || sum.Header.MapName != portableMap {
		t.Fatalf("summary = %+v", sum)
	}

	for _, step := range []int{1, 7, 45} {
		p, s, receipts, err := play(t, fs, rec.data, step)
		if err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		if p.Mismatch() != nil || p.Tick() != rec.ticks || p.FinalTick() != rec.ticks || p.End() != replay.EndFinished || p.Truncated() {
			t.Fatalf("step %d: played to tick %d of %d, end %v, mismatch %v", step, p.Tick(), p.FinalTick(), p.End(), p.Mismatch())
		}
		if p.Verified() != int(rec.ticks/30) {
			t.Fatalf("step %d: verified %d checksums", step, p.Verified())
		}
		if s.UnitStateChecksum() != rec.final || !reflect.DeepEqual(receipts, rec.receipts) {
			t.Fatalf("step %d: playback ended in another state or applied other commands", step)
		}
	}

	// Cut inside the last chunks: everything before plays.
	cut := rec.data[:len(rec.data)*2/3]
	cutSummary, err := replay.Summarize(cut)
	if err != nil {
		t.Fatal(err)
	}
	p, _, _, err := play(t, fs, cut, 30)
	if err != nil || !p.Truncated() || p.Tick() != cutSummary.FinalTick || p.Tick() == 0 || p.Tick() >= rec.ticks || p.Mismatch() != nil {
		t.Fatalf("cut recording played to %d (summary %d), truncated %v: %v", p.Tick(), cutSummary.FinalTick, p.Truncated(), err)
	}

	// A checksum the playback cannot reproduce stops it at that tick.
	tampered := rewrite(t, rec.data, func(e *replay.Entry) bool {
		if e.Kind == replay.EntryChecksum && e.Tick == 300 {
			e.Sum[0] ^= 1
		}
		return true
	})
	p, _, _, err = play(t, fs, tampered, 45)
	var m *replay.Mismatch
	if !errors.As(err, &m) || !errors.Is(err, replay.ErrMismatch) || m.Tick != 300 || p.Mismatch() != m || p.Tick() != 300 || p.Verified() != 9 {
		t.Fatalf("tampered checksum: %v at tick %d", err, p.Tick())
	}
	if _, err := p.Step(45); err != m {
		t.Fatalf("playback went on after a mismatch: %v", err)
	}

	// Without seat 0's first move the battle diverges at the next checksum.
	dropped := rewrite(t, rec.data, func(e *replay.Entry) bool {
		return e.Kind != replay.EntryCommand || e.Tick != 40
	})
	p, _, _, err = play(t, fs, dropped, 45)
	if !errors.As(err, &m) || m.Tick != 60 || p.Tick() < 60 {
		t.Fatalf("dropped command: %v", err)
	}

	// A battle composed to another opening state is refused before a tick.
	opening := rewriteHeader(t, rec.data, func(h *replay.Header) { h.InitialChecksum[0] ^= 1 }, func(*replay.Entry) bool { return true })
	r0, err := replay.NewReader(opening)
	if err != nil {
		t.Fatal(err)
	}
	s0, err := replay.Compose(r0.Header(), replay.Content{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replay.NewPlayer(s0, r0); !errors.As(err, &m) || m.Tick != 0 {
		t.Fatalf("other opening state: %v", err)
	}

	// Another install refuses to compose the recording.
	r, err := replay.NewReader(rec.data)
	if err != nil {
		t.Fatal(err)
	}
	h := r.Header()
	if _, err := replay.Compose(h, replay.Content{FS: fs, Mod: session.MatchMod{ID: "other", Version: "1", Archive: [32]byte{1}}}); !errors.Is(err, replay.ErrIncompatible) {
		t.Fatalf("other mod: %v", err)
	}
	h.Identity.Content[0] ^= 1
	if _, err := replay.Compose(h, replay.Content{FS: fs}); !errors.Is(err, replay.ErrIncompatible) {
		t.Fatalf("other content: %v", err)
	}
	h = r.Header()
	h.Kind = replay.KindSkirmish
	if _, err := replay.Compose(h, replay.Content{FS: fs}); err == nil {
		t.Fatal("a single-player kind composed an online configuration")
	}
}

// pacedRelay seals a grant whenever the driver's reader asks, up to limit
// ticks, stamping the commands submitted since the last grant for seat 0.
type pacedRelay struct {
	mu        sync.Mutex
	tick      uint32
	limit     uint32
	position  uint64
	sequence  uint64
	pending   []relay.LocalCommand
	acks      []uint32
	closed    chan struct{}
	closeOnce sync.Once
}

func (r *pacedRelay) Submit(payload []byte) (uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sequence++
	r.pending = append(r.pending, relay.LocalCommand{Seat: 0, Sequence: r.sequence, Payload: append([]byte(nil), payload...)})
	return r.sequence, nil
}

func (r *pacedRelay) ReadGrant() (relay.LocalGrant, error) {
	r.mu.Lock()
	if r.tick == r.limit {
		r.mu.Unlock()
		<-r.closed
		return relay.LocalGrant{}, errors.New("relay closed")
	}
	r.tick++
	g := relay.LocalGrant{Tick: r.tick}
	for _, c := range r.pending {
		r.position++
		c.Position = r.position
		g.Commands = append(g.Commands, c)
	}
	r.pending = nil
	g.Position = r.position
	r.mu.Unlock()
	return g, nil
}

func (r *pacedRelay) Acknowledge(tick uint32, _ [32]byte, _, _ bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.acks = append(r.acks, tick)
	return nil
}

func (r *pacedRelay) Close() error {
	r.closeOnce.Do(func() { close(r.closed) })
	return nil
}

// The online recorder works under the real paced driver, which reads
// grants on its own goroutine ahead of the ticks the host runs and
// acknowledges each after it: the recording holds exactly the executed
// ticks, the local seat's submitted commands among them, and plays back.
func TestOnlineRecorderUnderThePacedDriver(t *testing.T) {
	fs := portableFS(t)
	config := onlineConfig(t)
	inputs, err := session.FreezeMatchInputs(fs, nil, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := session.MatchJoin{Inputs: inputs, Config: config}.Identity()
	if err != nil {
		t.Fatal(err)
	}
	s, err := session.NewPlaytestSkirmish(inputs, config, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PrepareGrantedBattle(); err != nil {
		t.Fatal(err)
	}
	header, err := replay.OnlineHeader(s, config, identity)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	w, err := replay.NewWriter(&out, header)
	if err != nil {
		t.Fatal(err)
	}
	const limit = 60
	fake := &pacedRelay{limit: limit, closed: make(chan struct{})}
	client, err := replay.NewOnlineRecorder(w, fake)
	if err != nil {
		t.Fatal(err)
	}
	driver, err := lockstep.NewPacedDriver(s, client)
	if err != nil {
		t.Fatal(err)
	}
	commander := commanders(t, s)[0]
	submitted := map[uint32]bool{}
	deadline := time.Now().Add(20 * time.Second)
	for s.Clock.GlobalTick < limit {
		if time.Now().After(deadline) {
			t.Fatalf("the driver stalled at tick %d", s.Clock.GlobalTick)
		}
		if _, err := driver.Pump(); err != nil {
			t.Fatal(err)
		}
		if tick := s.Clock.GlobalTick; tick%10 == 0 && !submitted[tick] {
			submitted[tick] = true
			move := session.HumanCommand{Kind: session.HumanOrder, Order: session.HumanOrderCommand{Handles: []pool.Handle{commander.Handle}, Code: 2,
				Position: orders.ResolvePos{X: commander.X + numeric.Fixed(16+tick)<<16, Y: commander.Y, Z: commander.Z}}}
			if _, err := driver.Submit(move); err != nil {
				t.Fatal(err)
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	if err := driver.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(replay.EndLeft); err != nil || w.Stopped() != nil {
		t.Fatalf("close: %v, stopped: %v", err, w.Stopped())
	}
	final := s.UnitStateChecksum()
	p, played, _, err := play(t, fs, out.Bytes(), 9)
	if err != nil {
		t.Fatal(err)
	}
	if p.Tick() != limit || p.Verified() != limit/30 || played.UnitStateChecksum() != final {
		t.Fatalf("played to %d with %d checksums", p.Tick(), p.Verified())
	}
	commands := 0
	r, err := replay.NewReader(out.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	for {
		e, err := r.Next()
		if err != nil {
			break
		}
		if e.Kind == replay.EntryCommand {
			commands++
		}
	}
	if commands == 0 {
		t.Fatal("no submitted command reached the recording")
	}
}
