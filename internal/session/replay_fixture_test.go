package session

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// The replay tests' asset-free battle: the authored portable install of the
// checkpoint tests (checkpoint_portable_fixture_test.go) with on/off,
// standing-order and cloak capabilities on its commanders and a plain scout
// to spawn, so every command kind has something to change. A Classic
// computer holds slot 1. Nothing here is retail content.

// replayFixtureFS mounts the authored install.
func replayFixtureFS(t *testing.T) vfs.FSOps {
	t.Helper()
	model, err := formats.EncodeThreeDO(&formats.ThreeDO{Root: 0, Objects: []formats.ThreeDOObject{{
		Version: 1, Name: "base", Selection: -1, Parent: -1, FirstChild: -1, NextSibling: -1,
		Vertices: []formats.ThreeDOVertex{{}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	frames := make([]formats.GAFWriteFrame, 10)
	for i := range frames {
		frames[i] = formats.GAFWriteFrame{Width: 3, Height: 3, XOffset: 1, YOffset: 1, Pixels: []byte{7, 7, 7, 7, 7, 7, 7, 7, 7}}
	}
	masks, err := formats.EncodeGAF([]formats.GAFWriteEntry{{Name: "vismask", Frames: frames}})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"maps/portable.tnt": string(checkpointPortableTNT()),
		"maps/portable.ota": `[GlobalHeader]{MinWindSpeed=100;MaxWindSpeed=200;Gravity=112;
[Schema 0]{Type=Network 1;SurfaceMetal=1;
[specials]{[special0]{specialwhat=StartPos1;XPos=128;ZPos=128;}
[special1]{specialwhat=StartPos2;XPos=384;ZPos=320;}}}}`,
		"gamedata/moveinfo.tdf":  `[CLASS0]{Name=portable;FootprintX=1;FootprintZ=1;MaxWaterDepth=10;MaxSlope=10;}`,
		"gamedata/los.tdf":       `[TABLEINFO]{numtables=1;}[TABLE1]{numlines=1;line1=1,0,1;}`,
		"gamedata/sidedata.tdf":  checkpointPortableSides(),
		"anims/vismasks.gaf":     string(masks),
		"objects3d/portable.3do": string(model),
		"ai/default.txt":         "plan any\n",
	}
	for _, dir := range []string{"weapons", "features", "download", "guis", "unitpics"} {
		files[dir+"/notes.txt"] = "Authored replay fixture; no records in this family.\n"
	}
	unit := func(name, side, category string, commander int) string {
		return fmt.Sprintf(`[UNITINFO]{UnitName=%s;Name=Replay %s;Side=%s;
ObjectName=portable;Category=%s;Commander=%d;BMcode=1;CanMove=1;CanStop=1;CanGuard=1;CanPatrol=1;
OnOffable=1;MobileStandOrders=1;FireStandOrders=1;CloakCost=5;
MovementClass=portable;MaxVelocity=2;Acceleration=0.25;BrakeRate=0.5;TurnRate=1024;
MaxDamage=1000;SightDistance=160;FootprintX=1;FootprintZ=1;
BuildCostMetal=1;BuildCostEnergy=1;BuildTime=1;}`, name, name, side, category, commander)
	}
	files["units/portarm.fbi"] = unit("portarm", "ARM", "COMMANDER MOBILE", 1)
	files["units/portcore.fbi"] = unit("portcore", "CORE", "COMMANDER MOBILE", 1)
	files["units/portscout.fbi"] = unit("portscout", "ARM", "MOBILE", 0)
	for _, name := range []string{"portarm", "portcore", "portscout"} {
		files["scripts/"+name+".cob"] = string(checkpointPortableCOB())
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	archiveFiles := make([]vfs.ArchiveFile, 0, len(paths))
	for _, path := range paths {
		archiveFiles = append(archiveFiles, vfs.ArchiveFile{Path: path, Data: []byte(files[path])})
	}
	var archive bytes.Buffer
	if err := vfs.WriteArchive(&archive, archiveFiles, vfs.ArchiveWriteOptions{}); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if _, err := fs.MountArchiveReader("replay.hpi", bytes.NewReader(archive.Bytes()), int64(archive.Len()), 10, vfs.ArchiveOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	return fs
}

// replayFixtureConfig is the battle's configuration as a replay header holds
// it: resolved through the local request adapter, encoded and decoded.
func replayFixtureConfig(t *testing.T, mode gameplay.Mode) EffectiveMatchConfig {
	t.Helper()
	cfg := DirectSkirmishConfig("portable")
	cfg.Gameplay, cfg.UnitLimit = mode, 20
	cfg.RNGSimSeed, cfg.RNGCrtSeed = 7, 11
	cfg.Location = 1
	zero := 0
	options := SkirmishEntryOptions{CommunitySources: CommunitySources{Player: community.Overrides{UnitLimit: &zero}}}
	return replayHeaderConfig(t, cfg, options, 0)
}

// replayHeaderConfig resolves a single-player setup through the local
// request adapter, then encodes and decodes it, as a replay header carries
// it. The room fields a single-player battle does not read are fixed.
func replayHeaderConfig(t *testing.T, cfg SkirmishConfig, options SkirmishEntryOptions, schema uint32) EffectiveMatchConfig {
	t.Helper()
	room := MatchRoomInputs{
		MapSchema:      schema,
		ContentProfile: "retail",
		PlayerView:     MatchView{MinimumScale: 1024, MaximumScale: 2048},
		SpectatorView:  MatchView{MinimumScale: 256, MaximumScale: 2048, FullMap: true},
		ReplayView:     MatchView{MinimumScale: 256, MaximumScale: 2048, FullMap: true},
		Policies: MatchPolicies{Revision: 1, Scheduling: 1, Pacing: 1, Drop: 1, Audience: 1,
			RejoinGraceMilliseconds: 90000},
	}
	for i := range room.Participants[0] {
		room.Participants[0][i] = 1
	}
	r, err := NewMatchConfigRequest(cfg, options, room)
	if err != nil {
		t.Fatal(err)
	}
	config, err := ResolveMatchConfig(r)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := EncodeMatchConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeMatchConfig(payload)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Digest() != config.Digest() {
		t.Fatal("the configuration does not survive its encoding")
	}
	return decoded
}

// replayFixtureBattle composes the battle from its own freeze of the install.
// recording battles enter through the host's first pumps; playback battles
// through PrepareRecordedBattle. Both publish the opening frame and keep
// canonical checkpoints, the full-state comparison these tests use.
func replayFixtureBattle(t *testing.T, fs vfs.FSOps, config EffectiveMatchConfig, playback bool) *Session {
	t.Helper()
	inputs, err := FreezeMatchInputs(fs, nil, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewAdmittedSkirmish(inputs, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	if playback {
		if err := s.PrepareRecordedBattle(); err != nil {
			t.Fatal(err)
		}
	} else {
		ready := false
		for range 8 {
			if plan := s.PrepareStep(s.Clock.ScaledAnchor); plan.Runs() {
				ready = true
				break
			}
		}
		if !ready {
			t.Fatal("authored entry did not reach its first pump")
		}
	}
	if digest, ok := s.SimulationContentDigest(); !ok || digest != inputs.Digest() {
		t.Fatal("the battle does not report the digest of the inputs it runs on")
	}
	if !s.PublishOpeningFrame() {
		t.Fatal("opening publication")
	}
	if err := s.EnableCheckpoints(); err != nil {
		t.Fatal(err)
	}
	return s
}

// replayEntry is one recorded command.
type replayEntry struct {
	stamp   CommandStamp
	command SeatCommand
}

// replayPump is one recorded pump end.
type replayPump struct {
	lastTick uint32
	ticks    int
}

// replaySum is one recorded unit checksum.
type replaySum struct {
	tick uint32
	sum  [32]byte
}

// replayTape is a ReplayRecorder that keeps everything in memory. Each
// command goes through the wire form, as a file would carry it.
type replayTape struct {
	t         *testing.T
	commands  []replayEntry
	pumps     []replayPump
	checksums []replaySum
	failures  []error
}

func (r *replayTape) Command(stamp CommandStamp, c SeatCommand) {
	r.t.Helper()
	if len(r.failures) != 0 {
		r.t.Error("the recorder heard a command after Failed")
	}
	payload, err := EncodeSeatCommand(SinglePlayerReplay, c)
	if err != nil {
		r.t.Errorf("recorded command %+v has no wire form: %v", stamp, err)
		return
	}
	decoded, err := DecodeSeatCommand(SinglePlayerReplay, payload)
	if err != nil {
		r.t.Errorf("recorded command %+v does not decode: %v", stamp, err)
		return
	}
	r.commands = append(r.commands, replayEntry{stamp: stamp, command: decoded})
}

func (r *replayTape) PumpEnded(lastTick uint32, ticks int) {
	if len(r.failures) != 0 {
		r.t.Error("the recorder heard a pump after Failed")
	}
	r.pumps = append(r.pumps, replayPump{lastTick: lastTick, ticks: ticks})
}

func (r *replayTape) Checksum(tick uint32, sum [32]byte) {
	if len(r.failures) != 0 {
		r.t.Error("the recorder heard a checksum after Failed")
	}
	r.checksums = append(r.checksums, replaySum{tick: tick, sum: sum})
}

func (r *replayTape) Failed(err error) { r.failures = append(r.failures, err) }

// replayCompare is what the replay tests compare: the tick, both random
// streams, the unit checksum and the full canonical checkpoint requested
// before the last pump.
func replayCompare(t *testing.T, what string, a, b *Session) {
	t.Helper()
	if a.Clock.GlobalTick != b.Clock.GlobalTick {
		t.Fatalf("%s: ticks %d / %d", what, a.Clock.GlobalTick, b.Clock.GlobalTick)
	}
	if *a.SimRNG() != *b.SimRNG() || *a.CrtRNG() != *b.CrtRNG() {
		t.Fatalf("%s: random streams differ at tick %d", what, a.Clock.GlobalTick)
	}
	if a.UnitStateChecksum() != b.UnitStateChecksum() {
		t.Fatalf("%s: unit checksums differ at tick %d", what, a.Clock.GlobalTick)
	}
	// The order queues, which the canonical checkpoint also covers, compared
	// directly so a difference names its unit.
	ua, ub := a.Units.AppendLive(nil), b.Units.AppendLive(nil)
	if len(ua) != len(ub) {
		t.Fatalf("%s: %d / %d live units", what, len(ua), len(ub))
	}
	for i := range ua {
		qa, qb := orders.QueueOfUnit(ua[i]), orders.QueueOfUnit(ub[i])
		if ua[i].Handle != ub[i].Handle || (qa == nil) != (qb == nil) {
			t.Fatalf("%s: unit %d / %d or its queue differs", what, ua[i].Handle, ub[i].Handle)
		}
		if qa == nil {
			continue
		}
		for _, pair := range [2][2][]*orders.Node{{qa.Primary(), qb.Primary()}, {qa.Secondary(), qb.Secondary()}} {
			if len(pair[0]) != len(pair[1]) {
				t.Fatalf("%s: unit %d holds %d / %d records", what, ua[i].Handle, len(pair[0]), len(pair[1]))
			}
			for j := range pair[0] {
				na, nb := pair[0][j], pair[1][j]
				if na.ID != nb.ID || na.Phase != nb.Phase || na.Owner != nb.Owner || na.Target != nb.Target ||
					na.GoalX != nb.GoalX || na.GoalY != nb.GoalY || na.GoalZ != nb.GoalZ || na.Param1 != nb.Param1 ||
					na.Param2 != nb.Param2 || na.Param3 != nb.Param3 || na.CreationTick != nb.CreationTick || na.Flags != nb.Flags ||
					na.BuildDefKey != nb.BuildDefKey || na.BuildFacing != nb.BuildFacing || na.QueuedIssue != nb.QueuedIssue ||
					na.HumanMoveSequence != nb.HumanMoveSequence {
					t.Fatalf("%s: unit %d record %d: %+v / %+v", what, ua[i].Handle, j, *na, *nb)
				}
			}
		}
	}
	ra, rb := a.CheckpointCaptureResult(), b.CheckpointCaptureResult()
	if ra.Err != nil || rb.Err != nil {
		t.Fatalf("%s: checkpoint capture: %v / %v", what, ra.Err, rb.Err)
	}
	if ra.Pending || rb.Pending || ra.Record.Position.Tick != rb.Record.Position.Tick || ra.Record.Digests != rb.Record.Digests {
		var owners []int
		for i := range ra.Record.Digests.Owners {
			if ra.Record.Digests.Owners[i] != rb.Record.Digests.Owners[i] {
				owners = append(owners, i+1)
			}
		}
		t.Fatalf("%s: canonical checkpoints differ in owner sections %v: %+v / %+v", what, owners, ra.Record.Position, rb.Record.Position)
	}
}

// replayRequestCheckpoint asks for a full canonical capture at the next
// completed tick.
func replayRequestCheckpoint(t *testing.T, s *Session) {
	t.Helper()
	if s.CheckpointCaptureResult().Pending {
		return
	}
	if err := s.RequestCheckpointCapture(io.Discard); err != nil {
		t.Fatal(err)
	}
}

// replayInstance is the publication identity the host captured for a unit
// from the committed frame.
func replayInstance(t *testing.T, s *Session, h pool.Handle) uint64 {
	t.Helper()
	for _, v := range s.Snapshot.Current().Units {
		if v.Slot == h {
			return v.InstanceID
		}
	}
	t.Fatalf("unit %d is not in the committed frame", h)
	return 0
}

// replayUnits lists an owner's live units in pool order.
func replayUnits(s *Session, owner uint8) []pool.Handle {
	var out []pool.Handle
	for _, u := range s.Units.Iter() {
		if u != nil && u.Alive && u.Owner == owner {
			out = append(out, u.Handle)
		}
	}
	return out
}
