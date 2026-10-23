package session

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func runtimeCheckpointHex(t *testing.T, parts ...string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.Join(strings.Fields(strings.Join(parts, "")), ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func runtimeCheckpointBytes(t *testing.T, s *Session) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := s.writeCheckpointRuntime(checkpoint.NewEncoder(&out), &content.CheckpointKeys{}, CheckpointFinalPumpTick); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func runtimeCheckpointVectorFixture(t *testing.T) *Session {
	t.Helper()
	s := &Session{
		CampaignSlot: -0x100000001, Clock: &clock.State{GlobalTick: 0x12345678},
		DefeatDone: true, EnemyOwner: 9, Gameplay: "g", Latch: EndLatch{Bits: 0x8123, Countdown: -2, Pending: 2}, LocalOwner: 3,
		Progress: BankProgress{BetweenMissions: -1}, RNGCrtSeed: 0x11223344, RNGSimSeed: 0xaabbccdd,
		Rules: RuleSet{Base: "b", Name: "N"}, State: 7, VictoryDone: true, ViewingOwner: 8,
		battleEntryTailDone: true, builderOptionsReady: true, deathmatchActive: true,
		deathmatchAttempts: 0x1234, deathmatchExhausted: true, deathsWithNoRecordedCause: -2, noShake: true,
		resultArmedTick: 0x87654321, resultPending: true, resultPendingDraw: true,
		resultPendingLosers: []int{2, -1, 2}, resultPendingReason: "\xff", resultPendingWinner: -3,
		rngInitialized: true, shakeActive: true, shakeAmpX: -1, shakeAmpY: 2, shakeDuration: -3, shakeOffsetX: 4, shakeOffsetY: -5, shakeRemaining: 6,
	}
	if err := s.Restrictions.Set("z", 5); err != nil {
		t.Fatal(err)
	}
	if err := s.Restrictions.Set("a", 0); err != nil {
		t.Fatal(err)
	}
	s.Community.AIApplianceEnergy = true
	s.Community.AIBuilderPlacementLimit = -1
	s.EntryCommunity.AIBuilderPlacementLimit = 2
	s.Wind = &world.Wind{Changed: true, DirX: -1, DirZ: 2, Heading: 0x1234, Max: 3, Min: -4, NextChange: 5, Scalar: math.Float32frombits(1 << 31), Strength: -6}
	s.campaignPlayerSide[9] = -128
	s.campaignPlayerSideKnown[0] = true
	s.pendingCommanderDeaths[9] = true
	s.playerBuilderOptions[0].Guard[0] = 0x81
	s.playerBuilderOptions[9].Patrol[2] = 0x82
	s.Progress.Thumbs[24] = 0xaa
	s.Progress.WL[9] = 0xbb
	s.rngCrt.State, s.rngSim.State = 0x55667788, 0x99aabbcc
	return s
}

// Authored byte groups fix section-prefix framing, all top-level field widths
// and lexical order. Nested nonzero vectors below do not call the codec to
// construct expectations. Zero Community tables have 32 bools and 14 i64s.
func TestCheckpointRuntimeVector(t *testing.T) {
	s := runtimeCheckpointVectorFixture(t)
	zero := func(n int) string { return strings.Repeat("00", n) }
	want := runtimeCheckpointHex(t,
		"03 ffffffff feffffff 78563412", // boundary, campaign, tick
		"01 ffffffffffffffff", zero(135), "01 09", "00 0200000000000000", zero(135), "01000000 67 2381 feff 02 03",
		zero(52),                                          // Meteor: 3 bools, eleven i32/u32, absent weapon, empty name
		zero(26),                                          // thirteen raw factors
		"ffffffffffffffff", zero(24), "aa", zero(9), "bb", // Progress
		"44332211 ddccbbaa 02000000 00 01000000 61 05 01000000 7a 01000000 62 01000000 4e", // seeds, restrictions, Rules
		zero(16), "00000000", zero(24), "00000000", zero(16), // Skirmish before Players
		zero(410), zero(8), zero(4), zero(8), // Players, seeds, Survival, UnitLimit
		"07 01 08 01 01 ffffffff 02000000 3412 03000000 fcffffff 05000000 00000080 faffffff 01 01", // state, victory, viewer, Wind presence/payload, entry, builder ready
		zero(9), "80 01", zero(9), "01 3412 01 feffffffffffffff 01",
		zero(9), "01 81", zero(58), "82", // commander deaths, builder options
		zero(24), // zero Result, including three length words
		"21436587 01 01 03000000 0200000000000000 ffffffffffffffff 0200000000000000",
		"01000000 ff fdffffffffffffff 88776655 0000000000000000 01 ccbbaa99 0000000000000000",
		zero(10), "01 ffffffff 02000000 fdffffff 04000000 fbffffff 06000000",
	)
	before := runtimeCheckpointVectorFixture(t)
	got := runtimeCheckpointBytes(t, s)
	if !bytes.Equal(got, want) {
		t.Fatalf("runtime bytes (%d)=%x\nwant (%d)=%x", len(got), got, len(want), want)
	}
	if !reflect.DeepEqual(s, before) {
		t.Fatal("capture changed session or RNG")
	}
}

func TestCheckpointRuntimeNestedVectors(t *testing.T) {
	t.Run("meteor", func(t *testing.T) {
		m := MeteorState{Active: true, DurationTicks: -2, Enabled: true, Initialized: true, IntervalTicks: -3, NextHit: 4, NextStrike: 5, OriginX: -6, OriginZ: 7, PerHitDelay: -8, Radius: 9, StrikeEnds: 10, TargetX: -11, TargetZ: 12, WeaponName: "\xff"}
		var out bytes.Buffer
		e := checkpoint.NewEncoder(&out)
		writeCheckpointMeteor(e, &content.CheckpointKeys{}, m)
		want := runtimeCheckpointHex(t, "01 feffffff 01 01 fdffffff 04000000 05000000 faffffff 07000000 f8ffffff 09000000 0a000000 f5ffffff 0c000000 00 01000000 ff")
		if e.Err() != nil || !bytes.Equal(out.Bytes(), want) {
			t.Fatalf("meteor=%x err=%v want=%x", out.Bytes(), e.Err(), want)
		}
	})
	t.Run("result", func(t *testing.T) {
		r := Result{ArmedTick: 1, Countdown: -2, Draw: true, Ended: true, Losers: []int{3, -4, 3}, Reason: "x", Tick: 5, Winners: []int{-6}}
		var out bytes.Buffer
		e := checkpoint.NewEncoder(&out)
		writeCheckpointResult(e, r)
		want := runtimeCheckpointHex(t, "01000000 feff 01 01 03000000 0300000000000000 fcffffffffffffff 0300000000000000 01000000 78 05000000 01000000 faffffffffffffff")
		if e.Err() != nil || !bytes.Equal(out.Bytes(), want) {
			t.Fatalf("result=%x err=%v want=%x", out.Bytes(), e.Err(), want)
		}
	})
	t.Run("skirmish", func(t *testing.T) {
		c := SkirmishConfig{CommanderDeath: -1, Difficulty: 2, Gameplay: "g", LOSType: -3, LineOfSight: 4, Location: -5, MapName: "m", Mapping: 6, NumPlayers: -7, RNGCrtSeed: 8, RNGSimSeed: 9, Survival: SurvivalOptions{Enabled: true, NoAir: true, NoNaval: true, Pace: 0x81}, UnitLimit: -10}
		c.Players[0] = SkirmishPlayer{AI: 0x82, AllyGroup: -11, Controller: 12, Energy: -13, Metal: 14, Side: -15}
		var out bytes.Buffer
		e := checkpoint.NewEncoder(&out)
		writeCheckpointSkirmish(e, c)
		want := runtimeCheckpointHex(t, "ffffffffffffffff 0200000000000000 01000000 67 fdffffffffffffff 0400000000000000 fbffffffffffffff 01000000 6d 0600000000000000 f9ffffffffffffff",
			"82 f5ffffffffffffff 0c00000000000000 f3ffffffffffffff 0e00000000000000 f1ffffffffffffff", strings.Repeat("00", 9*41),
			"08000000 09000000 01 01 01 81 f6ffffffffffffff")
		if e.Err() != nil || !bytes.Equal(out.Bytes(), want) {
			t.Fatalf("skirmish=%x err=%v want=%x", out.Bytes(), e.Err(), want)
		}
	})
	t.Run("raw factors", func(t *testing.T) {
		m := content.Mutators{AreaOfEffect: content.Factor{Num: 1, Den: 2}, BuildCost: content.Factor{Num: 3, Den: 4}, BuildSpeed: content.Factor{Num: 5, Den: 6}, Damage: content.Factor{Num: 7, Den: 8}, FireRate: content.Factor{Num: 9, Den: 10}, Health: content.Factor{Num: 11, Den: 12}, Income: content.Factor{Num: 13, Den: 14}, Radar: content.Factor{Num: 15, Den: 16}, Salvage: content.Factor{Num: 17, Den: 18}, Sight: content.Factor{Num: 19, Den: 20}, UnitSpeed: content.Factor{Num: 21, Den: 22}, BuilderSpeed: content.Factor{Num: 23, Den: 24}, SmartMakers: content.Factor{Num: 25, Den: 26}}
		var out bytes.Buffer
		e := checkpoint.NewEncoder(&out)
		writeCheckpointMutators(e, m)
		want := runtimeCheckpointHex(t, "0201 0403 0605 1817 0807 0a09 0c0b 0e0d 100f 1211 1413 1a19 1615")
		if e.Err() != nil || !bytes.Equal(out.Bytes(), want) {
			t.Fatalf("factors=%x err=%v", out.Bytes(), e.Err())
		}
	})
}

func TestCheckpointRuntimeMutationsAndExclusions(t *testing.T) {
	newFixture := func() *Session {
		return &Session{Clock: &clock.State{GlobalTick: 3}, Wind: &world.Wind{Scalar: math.Float32frombits(1 << 31)}}
	}
	base := newFixture()
	want := runtimeCheckpointBytes(t, base)
	for _, tc := range []struct {
		name string
		edit func(*Session)
	}{
		{"community", func(s *Session) { s.Community.AIBuilderPlacementLimit = -1 }},
		{"entry community", func(s *Session) { s.EntryCommunity.WreckSnap = true }},
		{"meteor initialized", func(s *Session) { s.Meteor.Initialized = true }},
		{"raw identity factor", func(s *Session) { s.Mutators.Health = content.Factor{Num: 1, Den: 1} }},
		{"builder speed", func(s *Session) { s.Mutators.BuilderSpeed = content.Factor{Num: 5, Den: 1} }},
		{"smart makers", func(s *Session) { s.Mutators.SmartMakers = content.SwitchOn() }},
		{"builder fallback", func(s *Session) { s.builderOptionsReady = true }},
		{"pending draw", func(s *Session) { s.resultPendingDraw = true }},
		{"removed seat", func(s *Session) { s.seatCommands.removed[9] = true }},
		{"wind signed zero", func(s *Session) { w := *s.Wind; w.Scalar = 0; s.Wind = &w }},
		{"sim draws", func(s *Session) { old := s.rngSim.State; s.rngSim.Uint32n(2); s.rngSim.State = old }},
		{"crt draws", func(s *Session) { old := s.rngCrt.State; s.rngCrt.Rand(); s.rngCrt.State = old }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newFixture()
			tc.edit(s)
			if bytes.Equal(runtimeCheckpointBytes(t, s), want) {
				t.Fatal("retained mutation disappeared")
			}
		})
	}
	s := newFixture()
	s.Skirmish.Players[0].Nickname = "presentation"
	s.Skirmish.Players[0].Color = 8
	s.Skirmish.rulesDefaultsApplied = true
	s.result.Kind = "draw"
	s.result.WinnerTeam = 100
	s.result.Scores = []frame.ResultScore{{}}
	s.result.ColumnMaxima[3] = 99
	s.result.Survival = &frame.SurvivalResult{}
	w := *s.Wind
	w.LastChange = 33
	w.BriefingCountdown = 44
	s.Wind = &w
	s.seatCommands.issuer = 9
	s.seatCommands.lastPosition = 123
	// This binding must be rejected by parent preflight. The payload leaf
	// neither blesses it nor invokes its nil promoted implementation.
	s.Rules.UnitLimit = struct{ UnitLimitRules }{}
	s.publicationObserver = func(*frame.Frame) { panic("capture called publication observer") }
	if !bytes.Equal(runtimeCheckpointBytes(t, s), want) {
		t.Fatal("excluded metadata entered payload")
	}
	if s.postLoop != nil || s.rngInitialized {
		t.Fatal("capture initialized lazy session state")
	}
}

func TestCheckpointRuntimeRefusals(t *testing.T) {
	for _, tc := range []struct {
		name     string
		s        *Session
		keys     *content.CheckpointKeys
		boundary CheckpointBoundary
	}{
		{"nil session", nil, &content.CheckpointKeys{}, CheckpointEntry},
		{"nil clock", &Session{}, &content.CheckpointKeys{}, CheckpointEntry},
		{"nil keys", &Session{Clock: &clock.State{}}, nil, CheckpointEntry},
		{"boundary", &Session{Clock: &clock.State{}}, &content.CheckpointKeys{}, 0},
		{"pending", &Session{Clock: &clock.State{}, pendingBattle: true}, &content.CheckpointKeys{}, CheckpointEntry},
		{"issuing", &Session{Clock: &clock.State{}, seatCommands: seatCommandState{issuing: true}}, &content.CheckpointKeys{}, CheckpointEntry},
		{"weapon", &Session{Clock: &clock.State{}, Meteor: MeteorState{Weapon: &content.WeaponDef{}}}, &content.CheckpointKeys{}, CheckpointEntry},
		{"NaN", &Session{Clock: &clock.State{}, Wind: &world.Wind{Scalar: math.Float32frombits(0x7fc00001)}}, &content.CheckpointKeys{}, CheckpointEntry},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := checkpoint.NewEncoder(io.Discard)
			if err := tc.s.writeCheckpointRuntime(e, tc.keys, tc.boundary); err == nil || e.Err() == nil {
				t.Fatal("invalid runtime accepted")
			}
		})
	}
	s := &Session{Clock: &clock.State{}}
	if err := s.writeCheckpointRuntime(nil, &content.CheckpointKeys{}, CheckpointEntry); err == nil {
		t.Fatal("nil encoder accepted")
	}
	boom := errors.New("writer failed")
	e := checkpoint.NewEncoder(runtimeCheckpointFailWriter{boom})
	if err := s.writeCheckpointRuntime(e, &content.CheckpointKeys{}, CheckpointEntry); !errors.Is(err, boom) {
		t.Fatalf("writer error=%v", err)
	}
}

type runtimeCheckpointFailWriter struct{ err error }

func (w runtimeCheckpointFailWriter) Write([]byte) (int, error) { return 0, w.err }

func TestCheckpointRuntimeMeteorFrozenIdentity(t *testing.T) {
	fs := vfs.New()
	if err := fs.MountDirectory(t.TempDir(), 10); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fs.Close() })
	sources, err := content.CaptureSimulationSources(fs, "")
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := content.FreezeSimulationInputs(sources, content.SimulationInputRequest{Catalog: &content.Catalog{Weapons: map[string]*content.WeaponDef{"rock": {ID: 7}}}})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := inputs.CheckpointKeys()
	if err != nil {
		t.Fatal(err)
	}
	weapon := inputs.Catalog().Weapons["rock"]
	var out bytes.Buffer
	e := checkpoint.NewEncoder(&out)
	writeCheckpointMeteor(e, keys, MeteorState{Weapon: weapon, WeaponName: "unrelated"})
	// Catalog family 1, raw weapon ordinal 7, exact frozen logical key.
	want := runtimeCheckpointHex(t, strings.Repeat("00", 47), "01 01 07000000 0b000000 776561706f6e2f726f636b 09000000 756e72656c61746564")
	if e.Err() != nil || !bytes.Equal(out.Bytes(), want) {
		t.Fatalf("weapon vector=%x err=%v want=%x", out.Bytes(), e.Err(), want)
	}
	copied := *weapon
	e = checkpoint.NewEncoder(io.Discard)
	writeCheckpointMeteor(e, keys, MeteorState{Weapon: &copied, WeaponName: "rock"})
	if e.Err() == nil {
		t.Fatal("equal-value foreign definition accepted")
	}
}

func runtimeCheckpointSummaryFixture() *Session {
	s := &Session{Clock: &clock.State{GlobalTick: 9}, State: 7, rngInitialized: true, Latch: EndLatch{Bits: 8, Countdown: -2, Pending: 2}, resultPending: true, result: Result{Ended: true, Draw: true}, Wind: &world.Wind{Strength: -3, Heading: 4, Scalar: math.Float32frombits(5), NextChange: 6}, Meteor: MeteorState{Active: true, NextStrike: 10, StrikeEnds: 11, NextHit: 12}, shakeActive: true, shakeRemaining: -4}
	s.rngSim.Uint32n(2)
	s.rngCrt.Rand()
	s.rngSim.State = 13
	s.rngCrt.State = 14
	s.pendingCommanderDeaths[0] = true
	s.pendingCommanderDeaths[9] = true
	return s
}

func TestCheckpointRuntimeSummaryVectorAndPurity(t *testing.T) {
	s := runtimeCheckpointSummaryFixture()
	// Independent selected vector: prefix 17; tick/state/init 9/7/1;
	// sim 13/1, CRT 14/1; latch 8/-2/2; results 1/1/1; deaths
	// [1,0,0,0,0,0,0,0,0,1]; wind 1/-3/4/bits5/6;
	// meteor 1/10/11/12; shake 1/-4. Weighted total is 1740.
	var got checkpoint.Summary
	got.Word(17)
	before := runtimeCheckpointSummaryFixture()
	if err := s.appendCheckpointRuntimeSummary(&got); err != nil {
		t.Fatal(err)
	}
	if n, sum := got.Result(); n != 35 || sum != 1740 || !reflect.DeepEqual(s, before) {
		t.Fatalf("summary=%d,%d want 35,1740, or mutated state", n, sum)
	}
	want := got
	if n := testing.AllocsPerRun(100, func() {
		var out checkpoint.Summary
		if err := s.appendCheckpointRuntimeSummary(&out); err != nil {
			panic(err)
		}
	}); n != 0 {
		t.Fatalf("summary allocations=%v", n)
	}
	s.resultPendingDraw = true
	s.result.Reason = "blind spot"
	s.Meteor.Radius = 123
	s.Wind.DirX = 99
	s.Community.AIBuilderPlacementLimit = 99
	s.Meteor.Weapon = &content.WeaponDef{}
	var blind checkpoint.Summary
	blind.Word(17)
	if err := s.appendCheckpointRuntimeSummary(&blind); err != nil || blind != got {
		t.Fatal("summary read unselected fields/content")
	}
	s.Wind.Scalar = math.Float32frombits(0x7fa00001)
	if err := s.appendCheckpointRuntimeSummary(&got); err == nil || got != want {
		t.Fatal("NaN did not fail atomically")
	}
	if err := s.appendCheckpointRuntimeSummary(nil); err == nil {
		t.Fatal("nil summary accepted")
	}
	if err := (*Session)(nil).appendCheckpointRuntimeSummary(&got); err == nil || got != want {
		t.Fatal("nil session mutated summary")
	}
	if err := (&Session{}).appendCheckpointRuntimeSummary(&got); err == nil || got != want {
		t.Fatal("nil clock mutated summary")
	}
}
