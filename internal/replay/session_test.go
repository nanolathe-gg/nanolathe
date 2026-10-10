package replay_test

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/replay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// skipUntilSessionRecords skips while the session's replay hooks are the
// stub the replay-session unit replaces, whose refusals name that unit.
func skipUntilSessionRecords(t *testing.T, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), "(replay-session unit)") {
		t.Skipf("the session's replay recording is not implemented yet: %v", err)
	}
}

// singlePlayerRecording is a recorded single-player battle and what its
// host computed.
type singlePlayerRecording struct {
	data  []byte
	final [32]byte
	ticks uint32
}

// localCommander is the local human's first unit.
func localCommander(t *testing.T, s *session.Session) *units.Unit {
	t.Helper()
	for _, u := range s.Units.Iter() {
		if u.Owner == s.LocalOwner {
			return u
		}
	}
	t.Fatal("no local unit")
	return nil
}

// recordSinglePlayer composes request as single-player entry does, records
// it as the host will — the configuration from MatchConfigForFreshBattle,
// the header, the recorder attached before the first tick — and plays pumps
// host pumps through the session's own step: one to five ticks each, a
// paused pump now and then with a command applied at its boundary, the
// commander moved and stopped through the local command queue, and a flush
// every forty pumps. The file goes through a temporary file.
func recordSinglePlayer(t *testing.T, request headless.FreshBattleRequest, pumps int) singlePlayerRecording {
	t.Helper()
	composed, err := headless.ComposeFreshBattle(request)
	if err != nil {
		t.Fatal(err)
	}
	s := composed.Session
	config, err := headless.MatchConfigForFreshBattle(request, session.MatchRoomInputs{})
	if err != nil {
		t.Fatal(err)
	}
	header, err := replay.SinglePlayerHeader(s, config)
	if err != nil {
		t.Fatal(err)
	}
	// The header's identity admits the content the battle was composed
	// from: every comparison runs before the playback battle's preparation.
	if _, err := replay.Compose(header, replay.Content{FS: request.FS, Catalog: request.Catalog}); err != nil {
		skipUntilSessionRecords(t, err)
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "battle.nlreplay")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	w, err := replay.NewWriter(file, header)
	if err != nil {
		t.Fatal(err)
	}
	_, err = replay.NewSessionRecorder(w, s)
	skipUntilSessionRecords(t, err)
	if err != nil {
		t.Fatal(err)
	}
	for range 8 {
		if plan := s.PrepareStep(s.Clock.ScaledAnchor); plan.Runs() {
			s.ExecuteStep(plan)
			break
		}
	}
	commander := localCommander(t, s)
	startX, startZ := commander.X, commander.Z
	for i := range pumps {
		ticks := int32(1 + (i*7/5)%5)
		paused := i%13 == 6
		switch {
		case i%9 == 2 || paused:
			move := session.HumanCommand{Kind: session.HumanOrder, Order: session.HumanOrderCommand{Handles: []pool.Handle{commander.Handle}, Code: 2,
				Position: orders.ResolvePos{X: commander.X + numeric.Fixed(i%80-40)<<16, Y: commander.Y, Z: commander.Z + numeric.Fixed(i%50-25)<<16}}}
			if err := s.EnqueueHumanCommand(move); err != nil {
				t.Fatal(err)
			}
		case i%17 == 5:
			if err := s.EnqueueHumanCommand(session.HumanCommand{Kind: session.HumanStop, Stop: session.HumanStopCommand{Handles: []pool.Handle{commander.Handle}}}); err != nil {
				t.Fatal(err)
			}
		}
		s.SetPaused(paused)
		s.ExecuteStep(s.PrepareStep(s.Clock.ScaledAnchor + ticks))
		s.SetPaused(false)
		if i%40 == 39 {
			if err := w.Flush(); err != nil {
				t.Fatal(err)
			}
		}
		if s.State != session.StateBattle {
			break
		}
	}
	if commander.X == startX && commander.Z == startZ {
		t.Fatal("the script moved nothing")
	}
	if err := w.Stopped(); err != nil {
		t.Fatalf("recording stopped: %v", err)
	}
	if err := w.Close(replay.EndLeft); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return singlePlayerRecording{data: data, final: s.UnitStateChecksum(), ticks: s.Clock.GlobalTick}
}

// checkSinglePlayerPlayback plays a recording back headless in several step
// sizes and requires the recorded state at the end with every checksum
// matched.
func checkSinglePlayerPlayback(t *testing.T, rec singlePlayerRecording, content replay.Content) {
	t.Helper()
	for _, step := range []int{1, 5, 64} {
		r, err := replay.NewReader(rec.data)
		if err != nil {
			t.Fatal(err)
		}
		s, err := replay.Compose(r.Header(), content)
		if err != nil {
			t.Fatal(err)
		}
		p, err := replay.NewPlayer(s, r)
		if err != nil {
			t.Fatal(err)
		}
		for {
			_, err := p.Step(step)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("step %d: %v", step, err)
			}
		}
		if p.Tick() != rec.ticks || p.FinalTick() != rec.ticks || p.End() != replay.EndLeft || p.Verified() != int(rec.ticks/30) {
			t.Fatalf("step %d: played to %d of %d, end %v, verified %d", step, p.Tick(), rec.ticks, p.End(), p.Verified())
		}
		if s.UnitStateChecksum() != rec.final {
			t.Fatalf("step %d: playback ended in another state", step)
		}
	}
}

// A single-player battle recorded through the session's recorder plays back
// through admission to the same state, every 30-tick checksum matched,
// whatever pump sizes the host ran — one to five ticks, paused pumps whose
// boundary applied a command. Asset-free: the authored portable install,
// with its Classic computer.
func TestSinglePlayerRecordingPlaysBack(t *testing.T) {
	fs := portableFS(t)
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Modern} {
		t.Run(string(mode), func(t *testing.T) {
			cfg := session.DirectSkirmishConfig(portableMap)
			cfg.Gameplay, cfg.UnitLimit, cfg.Location = mode, 20, 1
			zero := 0
			request := headless.FreshBattleRequest{Kind: headless.ScenarioSkirmish, Map: portableMap, Skirmish: cfg, Gameplay: mode,
				CommunitySources: session.CommunitySources{Player: community.Overrides{UnitLimit: &zero}},
				SimulationSeed:   7, CRTSeed: 11, FS: fs, LocalOwner: -1}
			rec := recordSinglePlayer(t, request, 300)
			t.Logf("single-player %s, %d ticks: %d bytes", mode, rec.ticks, len(rec.data))
			checkSinglePlayerPlayback(t, rec, replay.Content{FS: fs})
		})
	}
}
