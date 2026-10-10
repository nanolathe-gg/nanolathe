//go:build retail

package session

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/survival"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
)

// onlineScriptedRun drives a composed online battle presenting seat for ticks
// granted ticks with the rehearsal's script for its human seats, and returns
// a digest of the unit checksum taken every 30 ticks and at the end, with the
// final checksum itself. A battle that ends early stops the run on every
// replica at the same tick.
func onlineScriptedRun(t *testing.T, s *Session, config EffectiveMatchConfig, ticks uint32, every func(tick uint32)) ([32]byte, [32]byte) {
	t.Helper()
	r := rehearsal{s: s, h: sha256.New()}
	r.findLeads(config)
	h := sha256.New()
	var b [4]byte
	tick := uint32(0)
	for tick < ticks && s.State == StateBattle && !s.OnlineBattleEnded() {
		tick++
		if err := r.issue(tick); err != nil {
			t.Fatal(err)
		}
		if err := s.StepGranted(tick); err != nil {
			t.Fatal(err)
		}
		s.DrainCommandReceipts()
		if tick%30 == 0 {
			sum := s.UnitStateChecksum()
			binary.LittleEndian.PutUint32(b[:], tick)
			h.Write(b[:])
			h.Write(sum[:])
			if every != nil {
				every(tick)
			}
		}
	}
	final := s.UnitStateChecksum()
	binary.LittleEndian.PutUint32(b[:], tick)
	h.Write(b[:])
	h.Write(final[:])
	var out [32]byte
	h.Sum(out[:0])
	return out, final
}

// A human-only online battle is bit-identical to the one released clients
// compute: the rehearsal digest and an 1,800-tick scripted run of two- and
// three-seat skirmish and Survival rooms on the reference install are the
// values recorded before online computer seats were admitted. A change to
// them is a protocol change for every human-only room, which released clients
// would refuse at Ready.
func TestOnlineHumanOnlyRoomsAreLocked(t *testing.T) {
	for _, tc := range []struct {
		name      string
		survival  bool
		teams     []uint8
		rehearsal string
		run       string
		final     string
	}{
		{"two seats", false, []uint8{0, 0},
			"92404bbc9317764e35a72fd05223528ce13471cff98a96e1b8c70b120c50149d",
			"ddf7f0fdf27408d251f098e5366b8dd0f2d1b011792f7a600c3cdf3523d098d7",
			"636e71d3c35447e094f074f55e2acf88036ada86059ee1340025e5a40ca192cc"},
		{"three seats", false, []uint8{0, 0, 0},
			"2439c2183e531e74272df2909f424f4bf5b53aad15731a399ed10a7b5c92fba6",
			"7e4cb57f427941d237df914b9b672be6cc0445ca60cc87c99f432c08ca6c74e3",
			"25ef2f3e16580b8f6dbbfb0a89a8fe2c912d58952a630f1b74a38a418a863a89"},
		{"two survivors", true, []uint8{0, 0},
			"93d6ccc47d81e94b943f4b77d86a71fc71388c9e0c4175011662e24c1c81675f",
			"9e6bd2b49eca961147fd013d1fd661d5cbc3081941a60249e55fdd54c21a1be5",
			"55c8d0cee569aaab56ad70902b0ff7d3d71481a79bfaa58011d78ef49fe5c18c"},
		{"three survivors", true, []uint8{0, 0, 0},
			"192f1dda8ffdea7ff9b9749c22b5c5a576d60bfff4f649cda94ac949c1a79dab",
			"dd8184a0385b3974f0de7bc2118ae9dd5a5adca1debcec58fa296e8ed7cf3009",
			"2f14c30e90aadc9058f479217d154b5b2619943ba0a11403330ac8532faa67f3"},
	} {
		pace := survival.Pace(0)
		if tc.survival {
			pace = survival.PaceRelentless
		}
		inputs, config := onlineRetailMatch(t, onlineRetailSetup(tc.survival, pace, tc.teams...))
		digest, err := RehearsalDigest(inputs, config)
		if err != nil {
			t.Fatal(err)
		}
		s := onlineRetailSession(t, inputs, config, 0)
		run, final := onlineScriptedRun(t, s, config, 1800, nil)
		if s.Clock.GlobalTick != 1800 {
			t.Fatalf("%s: the run stopped at tick %d", tc.name, s.Clock.GlobalTick)
		}
		if got := hex.EncodeToString(digest[:]); got != tc.rehearsal {
			t.Errorf("%s: rehearsal digest %s, want %s", tc.name, got, tc.rehearsal)
		}
		if got := hex.EncodeToString(run[:]); got != tc.run {
			t.Errorf("%s: 1,800-tick checksum digest %s, want %s", tc.name, got, tc.run)
		}
		if got := hex.EncodeToString(final[:]); got != tc.final {
			t.Errorf("%s: final unit checksum %s, want %s", tc.name, got, tc.final)
		}
	}
}

// onlineComputerRetailPair composes setup twice the way two seats do at
// Ready: the host from its resolved configuration and the joiner from the
// configuration's bytes, each freezing its own inputs, and presents seat 0 on
// the first and seat 1 on the second.
func onlineComputerRetailPair(t *testing.T, setup OnlineMatchSetup) (a, b *Session, config EffectiveMatchConfig, inputs *content.SimulationInputs) {
	t.Helper()
	inputs, config = onlineRetailMatch(t, setup)
	payload, err := EncodeMatchConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeMatchConfig(payload)
	if err != nil {
		t.Fatal(err)
	}
	cat, fs := retailcat.Shared(t)
	joiner, err := FreezeMatchInputs(fs, cat, decoded, nil)
	if err != nil {
		t.Fatal(err)
	}
	return onlineRetailSession(t, inputs, config, 0), onlineRetailSession(t, joiner, decoded, 1), config, inputs
}

// onlineLockstep steps a and b through the same granted ticks with the same
// stamped commands — the rehearsal's script for the human seats, computed
// once from a — and compares their unit checksums and both streams every 30
// ticks and at the end, and every receipt. On each comparison it also checks
// that every hosted computer reads every live unit's sensor status exactly as
// seat 0 does (DESIGN_MULTIPLAYER §6.2).
func onlineLockstep(t *testing.T, a, b *Session, config EffectiveMatchConfig, ticks uint32, computers []uint8) {
	t.Helper()
	script := rehearsal{s: a}
	script.findLeads(config)
	compare := func(tick uint32) {
		if a.UnitStateChecksum() != b.UnitStateChecksum() || *a.SimRNG() != *b.SimRNG() || *a.CrtRNG() != *b.CrtRNG() {
			t.Fatalf("the seat-0 and seat-1 compositions diverged by tick %d", tick)
		}
		for _, u := range a.Units.IterSliced() {
			if u == nil || !u.Alive {
				continue
			}
			for _, c := range computers {
				if a.sensorStatus(c, u) != a.sensorStatus(0, u) {
					t.Fatalf("tick %d: computer %d reads unit %d's sensor status apart from its host", tick, c, u.Handle)
				}
			}
		}
	}
	for tick := uint32(1); tick <= ticks; tick++ {
		for _, c := range script.commands(tick) {
			for _, s := range []*Session{a, b} {
				if err := s.EnqueueSeatCommand(c.stamp, c.command); err != nil {
					t.Fatal(err)
				}
			}
		}
		for _, s := range []*Session{a, b} {
			if err := s.StepGranted(tick); err != nil {
				t.Fatal(err)
			}
		}
		ra, rb := a.DrainCommandReceipts(), b.DrainCommandReceipts()
		if len(ra) != len(rb) {
			t.Fatalf("tick %d: %d and %d receipts", tick, len(ra), len(rb))
		}
		for i := range ra {
			if ra[i].Stamp != rb[i].Stamp || ra[i].Outcome != rb[i].Outcome {
				t.Fatalf("tick %d: receipt %d differs", tick, i)
			}
		}
		if tick%30 == 0 {
			compare(tick)
		}
		if a.OnlineBattleEnded() != b.OnlineBattleEnded() {
			t.Fatalf("tick %d: one composition ended", tick)
		}
		if a.OnlineBattleEnded() {
			t.Logf("the battle ended at tick %d", tick)
			compare(tick)
			return
		}
	}
	compare(ticks)
}

// Two humans, a Classic computer and a Modern computer, each on its own
// team, play two minutes alike whichever human seat a composition presents
// and whichever seat froze its inputs; both computers are hosted by seat 0,
// borrow its perspective, have no result row and build beyond their
// commanders; the rehearsal agrees across seats (DESIGN_MULTIPLAYER §6.6,
// §16.7).
func TestOnlineComputerSeatsPlayAlikeRetail(t *testing.T) {
	setup := onlineRetailSetup(false, 0, 1, 2)
	setup.Computers = []OnlineComputer{
		{Team: 3, Side: 0, Color: 2, Kind: ai.ControllerClassic, Difficulty: 1},
		{Team: 4, Side: 1, Color: 3, Kind: ai.ControllerModern, Difficulty: 1},
	}
	a, b, config, inputs := onlineComputerRetailPair(t, setup)
	for _, s := range []*Session{a, b} {
		if s.AI[2] == nil || s.AI[2].Controller != ai.ControllerClassic || s.AI[3] == nil || s.AI[3].Controller != ai.ControllerModern {
			t.Fatal("the computer rows have no Classic and Modern managers")
		}
		if s.Vis.PerspectiveOf(2) != 0 || s.Vis.PerspectiveOf(3) != 0 || s.Vis.PerspectiveOf(1) != 1 {
			t.Fatal("the computers do not borrow seat 0's perspective")
		}
		if s.onlineResults.seats[2].present || s.onlineResults.seats[3].present {
			t.Fatal("a computer has a result row")
		}
	}
	begin := time.Now()
	onlineLockstep(t, a, b, config, 3600, []uint8{2, 3})
	t.Logf("3,600 lockstep ticks of two sessions: %v", time.Since(begin))
	for _, c := range []int{2, 3} {
		if a.Units.CreatedCountForPlayer(c) <= 1 {
			t.Fatalf("computer %d built nothing in two minutes", c)
		}
	}
	t.Logf("units created by players 0..3: %d %d %d %d; live %d %d %d %d",
		a.Units.CreatedCountForPlayer(0), a.Units.CreatedCountForPlayer(1), a.Units.CreatedCountForPlayer(2), a.Units.CreatedCountForPlayer(3),
		a.Units.LiveCountForPlayer(0), a.Units.LiveCountForPlayer(1), a.Units.LiveCountForPlayer(2), a.Units.LiveCountForPlayer(3))
	begin = time.Now()
	want, err := RehearsalDigest(inputs, config)
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(begin)
	if got := rehearsalDigestOf(t, inputs, config, 1); got != want {
		t.Fatalf("seat 1's rehearsal digest %x, want %x", got, want)
	}
	t.Logf("rehearsal with a Classic and a Modern computer on %q: %v", admittedSkirmishMap, elapsed)
}

// Online Survival with two humans and a Modern computer survivor: the
// compositions agree for two minutes, through the first waves, the buddy is
// on the survivor team and borrows seat 0's perspective, and the rehearsal
// agrees across seats.
func TestOnlineSurvivalComputerBuddyPlaysAlikeRetail(t *testing.T) {
	setup := onlineRetailSetup(true, survival.PaceRelentless, 0, 0)
	setup.Computers = []OnlineComputer{{Side: 1, Color: 3, Kind: ai.ControllerModern, Difficulty: 1}}
	a, b, config, inputs := onlineComputerRetailPair(t, setup)
	if a.Survival == nil || a.Survival.attacker != 3 || len(a.Survival.team) != 3 || !a.Survival.onTeam(2) {
		t.Fatalf("survival state %+v", a.Survival)
	}
	if a.Vis.PerspectiveOf(2) != 0 || a.onlineResults.seats[2].present || a.onlineResults.seats[3].present {
		t.Fatal("the buddy is not a hosted computer without a result row")
	}
	onlineLockstep(t, a, b, config, 3600, []uint8{2})
	if !a.Survival.firstWaveSpawned() || a.Units.CreatedCountForPlayer(2) <= 1 {
		t.Fatalf("first wave spawned %v; the buddy created %d units", a.Survival.firstWaveSpawned(), a.Units.CreatedCountForPlayer(2))
	}
	t.Logf("wave %d; units created by players 0..3: %d %d %d %d; live %d %d %d %d", a.Survival.wave,
		a.Units.CreatedCountForPlayer(0), a.Units.CreatedCountForPlayer(1), a.Units.CreatedCountForPlayer(2), a.Units.CreatedCountForPlayer(3),
		a.Units.LiveCountForPlayer(0), a.Units.LiveCountForPlayer(1), a.Units.LiveCountForPlayer(2), a.Units.LiveCountForPlayer(3))
	begin := time.Now()
	want, err := RehearsalDigest(inputs, config)
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(begin)
	if got := rehearsalDigestOf(t, inputs, config, 1); got != want {
		t.Fatalf("seat 1's rehearsal digest %x, want %x", got, want)
	}
	t.Logf("Survival rehearsal with a Modern buddy on %q: %v", admittedSkirmishMap, elapsed)
}

// The rehearsal's cost with computers (§16.7 "Bound"): two humans alone, and
// two humans with two Modern computers, each measured once after a warm-up.
func TestOnlineRehearsalWithModernComputersRetail(t *testing.T) {
	plain := onlineRetailSetup(false, 0, 0, 0)
	modern := onlineRetailSetup(false, 0, 0, 0)
	modern.Computers = []OnlineComputer{
		{Side: 0, Color: 2, Kind: ai.ControllerModern, Difficulty: 1},
		{Side: 1, Color: 3, Kind: ai.ControllerModern, Difficulty: 1},
	}
	for _, tc := range []struct {
		name  string
		setup OnlineMatchSetup
	}{{"two humans", plain}, {"two humans and two Modern computers", modern}} {
		inputs, config := onlineRetailMatch(t, tc.setup)
		want := rehearsalDigestOf(t, inputs, config, 0)
		begin := time.Now()
		got, err := RehearsalDigest(inputs, config)
		elapsed := time.Since(begin)
		if err != nil || got != want {
			t.Fatalf("%s: digest %x, %v; want %x", tc.name, got, err, want)
		}
		t.Logf("%s on %q: rehearsal %v", tc.name, admittedSkirmishMap, elapsed)
	}
}
