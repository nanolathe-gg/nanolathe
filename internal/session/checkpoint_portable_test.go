package session

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// M3-C8's portable single-seat script uses explicit pump ends, not wall-clock
// scheduling (DESIGN_MULTIPLAYER §16.3.4, §16.3.78). Each run independently
// compiles/freezes the same authored install. Logged digests are comparison
// evidence for native CI runs, not a golden or a claim of native equivalence.
func TestCheckpointPortableScript(t *testing.T) {
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Modern, gameplay.Community39} {
		t.Run(string(mode), func(t *testing.T) {
			var sessions [2]*Session
			var actors [2]*units.Unit
			var identity checkpoint.Identity
			for i := range sessions {
				inputs, config := checkpointPortableInputs(t, mode)
				gotIdentity := checkpoint.Identity{Content: inputs.Digest(), Config: config.Digest()}
				if i == 0 {
					identity = gotIdentity
				} else if gotIdentity != identity {
					t.Fatal("independently compiled content/config identities differ")
				}
				s := checkpointPortableBattle(t, inputs, config)
				sessions[i] = s
				for _, u := range s.Units.Iter() {
					if u.Owner == uint8(s.LocalOwner) {
						if actors[i] != nil {
							t.Fatal("multiple local actors in authored scene")
						}
						actors[i] = u
					}
				}
				if actors[i] == nil || s.AI[1] == nil || s.AI[1].Controller != ai.ControllerClassic {
					t.Fatal("missing human commander or Classic computer")
				}
				if !s.PublishOpeningFrame() {
					t.Fatal("opening publication")
				}
				sim, crt := *s.SimRNG(), *s.CrtRNG()
				if err := s.EnableCheckpoints(); err != nil {
					t.Fatal(err)
				}
				if *s.SimRNG() != sim || *s.CrtRNG() != crt || s.Clock.GlobalTick != 0 {
					t.Fatal("entry capture advanced the simulation")
				}
			}
			if !reflect.DeepEqual(sessions[0].CheckpointHistory(), sessions[1].CheckpointHistory()) {
				t.Fatal("independent opening checkpoints differ")
			}
			entry := sessions[0].CheckpointHistory()
			if len(entry.Records) != 1 || len(entry.Ticks) != 0 || entry.Records[0].Position != (CheckpointPosition{Boundary: CheckpointEntry}) {
				t.Fatalf("opening history = %+v", entry)
			}
			t.Logf("portable-v1 mode=%s content=%x config=%x", mode, identity.Content, identity.Config)
			checkpointPortableLog(t, identity, entry.Records[0])

			// Ten running pumps total 30 ticks, exercising every batch size twice.
			// Commands enter through the same queue the local host uses. Empty
			// Stop is an accepted no-op and still consumes an input ordinal. An
			// unpaused zero-tick pump retains its input; a paused pump drains it.
			script := []struct {
				ticks   int
				paused  bool
				command string
			}{
				{0, false, "noop"},
				{1, false, "move"}, {2, false, ""}, {3, false, ""},
				{0, true, "stop-noop"},
				{4, false, "move"}, {5, false, ""},
				{0, false, "noop"},
				{1, false, "stop"}, {2, false, "move"},
				{3, false, ""}, {4, false, ""}, {5, false, ""},
			}
			var outputs [2]bytes.Buffer
			var tick uint32
			var consumed uint64
			var queued int
			startX, startZ := actors[0].X, actors[0].Z
			moved := false
			for pump, action := range script {
				for peer, s := range sessions {
					s.SetPaused(action.paused)
					commands := checkpointPortableCommands(action.command, actors[peer], startX, startZ)
					if peer == 0 {
						queued += len(commands)
						if action.ticks > 0 || action.paused {
							consumed += uint64(queued)
							queued = 0
						}
					}
					for _, command := range commands {
						if err := s.EnqueueHumanCommand(command); err != nil {
							t.Fatal(err)
						}
					}
					if !s.CheckpointCaptureResult().Pending {
						outputs[peer].Reset()
						if err := s.RequestCheckpointCapture(&outputs[peer]); err != nil {
							t.Fatal(err)
						}
					}
					before := s.CheckpointHistory()
					sim, crt := *s.SimRNG(), *s.CrtRNG()
					s.ExecuteStep(StepPlan{run: true, ticks: action.ticks})
					result := s.CheckpointCaptureResult()
					if result.Err != nil {
						t.Fatalf("pump %d peer %d: %v", pump+1, peer, result.Err)
					}
					if s.State != StateBattle || s.Clock.GlobalTick != tick+uint32(action.ticks) || len(s.PendingHumanCommands()) != queued {
						t.Fatalf("pump %d peer %d: state=%v tick=%d queued=%d", pump+1, peer, s.State, s.Clock.GlobalTick, len(s.PendingHumanCommands()))
					}
					if action.paused {
						q := orders.QueueOfUnit(actors[peer])
						if q == nil || q.Head() == nil || q.Head().ID != orders.Lookup("Stop") {
							t.Fatal("paused Stop did not reach the actor's real queue")
						}
					}
					if action.ticks == 0 {
						if !result.Pending || outputs[peer].Len() != 0 || !reflect.DeepEqual(before, s.CheckpointHistory()) || *s.SimRNG() != sim || *s.CrtRNG() != crt {
							t.Fatalf("zero-tick pump %d changed history/RNG or completed capture", pump+1)
						}
					} else {
						boundary := CheckpointInteriorTick
						if action.ticks == 1 {
							boundary = CheckpointFinalPumpTick
						}
						want := CheckpointPosition{Tick: tick + 1, Boundary: boundary, Pump: uint64(pump + 1), ConsumedInput: consumed}
						if result.Pending || result.Record.Position != want || result.Record.Digests.Full != sha256.Sum256(outputs[peer].Bytes()) {
							t.Fatalf("pump %d capture pending=%v position=%+v full=%x, want position %+v and stream hash %x", pump+1, result.Pending, result.Record.Position, result.Record.Digests.Full, want, sha256.Sum256(outputs[peer].Bytes()))
						}
						checkpointPortableHeader(t, outputs[peer].Bytes(), identity)
					}
				}
				if *sessions[0].SimRNG() != *sessions[1].SimRNG() || *sessions[0].CrtRNG() != *sessions[1].CrtRNG() ||
					sessions[0].CheckpointCaptureResult() != sessions[1].CheckpointCaptureResult() || !bytes.Equal(outputs[0].Bytes(), outputs[1].Bytes()) ||
					!reflect.DeepEqual(sessions[0].CheckpointHistory(), sessions[1].CheckpointHistory()) {
					t.Fatalf("fresh composes differ after pump %d", pump+1)
				}
				if action.ticks != 0 {
					checkpointPortableLog(t, identity, sessions[0].CheckpointCaptureResult().Record)
					rows := sessions[0].CheckpointHistory().Ticks
					for j := range action.ticks {
						boundary := CheckpointInteriorTick
						if j+1 == action.ticks {
							boundary = CheckpointFinalPumpTick
						}
						want := CheckpointPosition{Tick: tick + uint32(j) + 1, Boundary: boundary, Pump: uint64(pump + 1), ConsumedInput: consumed}
						if rows[int(tick)+j].Position != want {
							t.Fatalf("tick row position = %+v, want %+v", rows[int(tick)+j].Position, want)
						}
					}
				}
				tick += uint32(action.ticks)
				moved = moved || actors[0].X != startX || actors[0].Z != startZ
			}
			history := sessions[0].CheckpointHistory()
			if !moved || len(history.Ticks) != 30 || len(history.Records) != 2 || history.Records[1].Position.Tick != 30 {
				t.Fatalf("script did not exercise motion and cadence: moved=%v rows=%d records=%d", moved, len(history.Ticks), len(history.Records))
			}
			checkpointPortableLog(t, identity, history.Records[1])
		})
	}
}

func checkpointPortableCommands(command string, actor *units.Unit, x, z numeric.Fixed) []HumanCommand {
	switch command {
	case "move":
		return []HumanCommand{{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{actor.Handle}, Code: 2,
			Position: orders.ResolvePos{X: x + 64<<16, Y: actor.Y, Z: z + 32<<16}}}}
	case "stop":
		return []HumanCommand{{Kind: HumanStop, Stop: HumanStopCommand{Handles: []pool.Handle{actor.Handle}}}}
	case "stop-noop":
		return []HumanCommand{{Kind: HumanStop, Stop: HumanStopCommand{Handles: []pool.Handle{actor.Handle}}}, {Kind: HumanStop}}
	case "noop":
		return []HumanCommand{{Kind: HumanStop}}
	default:
		return nil
	}
}

// Independently frame the fixed header and absent-owner domains. Successful
// complete capture must contain thirteen present sections, even when a pool is
// empty; their hashes must differ from the corresponding absent section.
func checkpointPortableHeader(t *testing.T, data []byte, id checkpoint.Identity) {
	t.Helper()
	want := append([]byte("NLCPSTAT\x02\x00"), id.Content[:]...)
	want = append(want, id.Config[:]...)
	want = append(want, 13, 0)
	if !bytes.HasPrefix(data, want) {
		t.Fatal("capture lacks the complete schema-2 header")
	}
}

func checkpointPortableLog(t *testing.T, id checkpoint.Identity, record CheckpointRecord) {
	t.Helper()
	p := record.Position
	t.Logf("portable-v1 tick=%d boundary=%d pump=%d input=%d full=%x", p.Tick, p.Boundary, p.Pump, p.ConsumedInput, record.Digests.Full)
	for i, digest := range record.Digests.Owners {
		absent := append([]byte("NLCPSECT\x02\x00"), id.Content[:]...)
		absent = append(absent, id.Config[:]...)
		absent = binary.LittleEndian.AppendUint16(absent, uint16(i+1))
		absent = append(absent, 0)
		if digest == (checkpoint.Digest{}) || digest == sha256.Sum256(absent) {
			t.Fatalf("section %d is absent", i+1)
		}
		t.Logf("portable-v1 tick=%d owner=%02d digest=%x", p.Tick, i+1, digest)
	}
}
