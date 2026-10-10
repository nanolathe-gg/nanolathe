package session

import (
	"bytes"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Online computer seats (DESIGN_MULTIPLAYER §6.6, §16.6): the room host adds
// computer rows, each hosted by seat 0, that every seat composes after its
// present humans. These are Nanolathe lobby and protocol values, not retail
// data.

// A human-only online battle on the authored install rehearses exactly as
// it did before online computer seats were admitted: the value is the one
// recorded then, so released clients and this build agree at Ready.
func TestOnlineHumanOnlyFixtureRehearsalIsLocked(t *testing.T) {
	config := rehearsalFixtureConfig(t, nil)
	got := rehearsalDigestOf(t, rehearsalFixtureInputs(t, config), config, 0)
	if want := "f9367cbfad4bd3621389861584a8dcdac1ba9da398d60b6c2ba6c002d9b52039"; hex.EncodeToString(got[:]) != want {
		t.Fatalf("rehearsal digest %x, want %s", got, want)
	}
}

// onlineComputerTestSetup is a two-human lobby joined by a Classic computer
// on team 2 and a Modern one with no team.
func onlineComputerTestSetup(survivalBattle bool) OnlineMatchSetup {
	setup := onlineTestSetup(survivalBattle, 1, 2)
	setup.Computers = []OnlineComputer{
		{Team: 2, Side: 1, Color: 5, Kind: ai.ControllerClassic, Difficulty: 2},
		{Team: 0, Side: 0, Color: 7, Kind: ai.ControllerModern, Difficulty: 0},
	}
	return setup
}

// The host's computers follow the humans in their stored order, each hosted
// by seat 0 with its own controller, difficulty, side, colour and team; the
// request resolves and survives its encoding, and OnlineComputersOf reads the
// rows back as the host stored them, so every seat composes the same rows.
func TestOnlineMatchRequestSeatsHostComputers(t *testing.T) {
	setup := onlineComputerTestSetup(false)
	if setup.Rows() != 4 {
		t.Fatalf("rows %d", setup.Rows())
	}
	r := onlineTestRequest(t, setup)
	c := resolveMatch(t, r)
	payload, err := EncodeMatchConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecodeMatchConfig(payload)
	if err != nil || back.Digest() != c.Digest() {
		t.Fatalf("round trip: %v", err)
	}
	seats := back.Request().Seats
	if len(seats) != 4 {
		t.Fatalf("%d rows", len(seats))
	}
	for i, want := range []struct {
		role       MatchRole
		host       uint8
		kind       ai.Controller
		difficulty uint8
		side       uint8
		color      uint8
		group      uint8
		shared     bool
	}{
		{MatchRoleHuman, MatchHostNone, 0, 0, 0, 0, 0, false},
		{MatchRoleHuman, MatchHostNone, 0, 0, 1, 1, 1, true},
		{MatchRoleComputer, 0, ai.ControllerClassic, 2, 1, 5, 1, true},
		{MatchRoleComputer, 0, ai.ControllerModern, 0, 0, 7, SkirmishDefaultAllyGroup, false},
	} {
		s := seats[i]
		if s.Role != want.role || s.HostSeat != want.host || s.ComputerKind != want.kind || s.Difficulty != want.difficulty ||
			s.Side != want.side || s.Color != want.color || s.AllyGroup != want.group || s.SharedVictory != want.shared {
			t.Fatalf("row %d %+v", i, s)
		}
		if computer := want.role == MatchRoleComputer; computer != (s.Participant == MatchParticipantID{}) || computer && (s.Nickname != "" || len(s.AIParams) != 0) {
			t.Fatalf("row %d identity %+v", i, s)
		}
	}
	if got := OnlineComputersOf(back.Request()); !reflect.DeepEqual(got, setup.Computers) {
		t.Fatalf("computers read back %+v, want %+v", got, setup.Computers)
	}
	// A human-only setup builds the rows it always has.
	plain := onlineTestSetup(false, 1, 2)
	if got := onlineTestRequest(t, plain); len(got.Seats) != 2 || len(OnlineComputersOf(got)) != 0 {
		t.Fatalf("human-only rows %+v", got.Seats)
	}
}

// Online Survival seats one computer survivor beside two humans: it joins
// the survivor team, keeps its controller and is hosted by seat 0, and the
// attacker still comes last with the first colour no survivor holds.
func TestOnlineMatchRequestSurvivalComputer(t *testing.T) {
	setup := onlineTestSetup(true, 0, 0)
	setup.Computers = []OnlineComputer{{Side: 1, Color: 4, Kind: ai.ControllerModern, Difficulty: 1}}
	if setup.Rows() != 4 {
		t.Fatalf("rows %d", setup.Rows())
	}
	r := onlineTestRequest(t, setup)
	resolveMatch(t, r)
	if len(r.Seats) != 4 || r.Seats[2].Role != MatchRoleComputer || r.Seats[2].HostSeat != 0 || r.Seats[2].ComputerKind != ai.ControllerModern ||
		r.Seats[2].Difficulty != 1 || r.Seats[2].Color != 4 || r.Seats[3].Role != MatchRoleSurvivalAttacker {
		t.Fatalf("rows %+v", r.Seats)
	}
	if r.Seats[2].AllyGroup != r.Seats[0].AllyGroup || r.Seats[1].AllyGroup != r.Seats[0].AllyGroup {
		t.Fatal("the computer survivor is not on the survivor team")
	}
	if got := OnlineComputersOf(r); len(got) != 1 || got[0].Team != OnlineTeamNone || got[0].Color != 4 {
		t.Fatalf("Survival computers read back %+v", got)
	}
	if r.Seats[3].Color != 2 {
		t.Fatalf("attacker colour %d, want 2, the first no survivor holds", r.Seats[3].Color)
	}
	// The attacker's colour skips every survivor's, a computer's included:
	// with red free of humans, the computer keeps it and the attacker does
	// not take it.
	setup.Seats[1].Color = 2
	setup.Computers[0].Color = survivalAttackerColor
	r = onlineTestRequest(t, setup)
	if c, a := r.Seats[2].Color, r.Seats[3].Color; c != survivalAttackerColor || a != 3 {
		t.Fatalf("computer colour %d and attacker colour %d, want %d and 3", c, a, survivalAttackerColor)
	}
}

// Computer colours resolve at composition: a stored colour no human holds
// is kept; a computer whose colour a human took, in stored order, takes the
// lowest colour no row holds, never one a later computer keeps.
func TestOnlineComputerColorsResolve(t *testing.T) {
	setup := onlineTestSetup(false, 0, 0)
	setup.Seats[1].Color = 2
	setup.Computers = []OnlineComputer{{Color: 2}, {Color: 1}, {Color: 0}, {Color: 9}}
	r := onlineTestRequest(t, setup)
	var got []uint8
	for _, s := range r.Seats {
		got = append(got, s.Color)
	}
	if want := []uint8{0, 2, 3, 1, 4, 9}; !bytes.Equal(got, want) {
		t.Fatalf("colours %v, want %v", got, want)
	}
}

func TestOnlineMatchRequestComputerRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*OnlineMatchSetup)
		path string
	}{
		{"eleven skirmish rows", func(s *OnlineMatchSetup) {
			for range 9 {
				s.Computers = append(s.Computers, OnlineComputer{})
			}
		}, "setup.computers"},
		{"four survivors", func(s *OnlineMatchSetup) {
			s.Survival = true
			s.Computers = append(s.Computers, OnlineComputer{}, OnlineComputer{})
		}, "setup.computers"},
		{"a colour above 9", func(s *OnlineMatchSetup) { s.Computers = []OnlineComputer{{Color: 10}} }, "setup.computers[0].color"},
		{"a side beyond the catalog", func(s *OnlineMatchSetup) { s.Computers = []OnlineComputer{{Side: 2}} }, "setup.computers[0].side"},
		{"a team above 5", func(s *OnlineMatchSetup) { s.Computers = []OnlineComputer{{Team: 6}} }, "setup.computers[0].team"},
		{"an unknown controller", func(s *OnlineMatchSetup) { s.Computers = []OnlineComputer{{Kind: 2}} }, "setup.computers[0].kind"},
		{"a difficulty above hard", func(s *OnlineMatchSetup) { s.Computers = []OnlineComputer{{Difficulty: 3}} }, "setup.computers[0].difficulty"},
		{"one team holding every row", func(s *OnlineMatchSetup) {
			s.Seats[0].Team, s.Seats[1].Team = 1, 1
			s.Computers = []OnlineComputer{{Team: 1}}
		}, "hostile alliance"},
	} {
		setup := onlineTestSetup(false, 0, 0)
		tc.edit(&setup)
		_, err := NewOnlineMatchRequest(setup, SkirmishEntryOptions{}, matchTestRoom())
		if err == nil || !strings.Contains(err.Error(), tc.path) {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
	// Survival still needs two humans: a computer survivor cannot stand in.
	setup := onlineTestSetup(true, 0)
	setup.Computers = []OnlineComputer{{}}
	if _, err := NewOnlineMatchRequest(setup, SkirmishEntryOptions{}, matchTestRoom()); err == nil {
		t.Fatal("Survival with one human and a computer survivor was admitted")
	}
}

// onlineCountdownFixture is an online result fixture: two humans and a
// computer hosted by seat 0, each with one live unit, every row its own
// side, under rules.
func onlineCountdownFixture(t *testing.T, rules RuleSet) (*Session, [3]pool.Handle) {
	t.Helper()
	w, def := eliminationFixtureWorld(t)
	s := &Session{
		Units: w, Econ: &economy.Service{}, Mission: &mission.Mission{Type: mission.TypeSkirmish}, State: StateBattle,
		Latch: NewEndLatch(), onlineResults: newOnlineResultState([10]bool{true, true}), Rules: rules,
	}
	s.SeedSessionRNG(17, 31)
	r := &MatchConfigRequest{SessionKind: MatchOnlineSkirmish, Seats: []MatchSeat{
		{Role: MatchRoleHuman, AllyGroup: 0, HostSeat: MatchHostNone},
		{Role: MatchRoleHuman, AllyGroup: 1, HostSeat: MatchHostNone},
		{Role: MatchRoleComputer, AllyGroup: 2, HostSeat: 0},
	}}
	for i, seat := range r.Seats {
		control := uint8(1)
		if seat.Role == MatchRoleComputer {
			control = 2
		}
		s.Econ.Players[i] = economy.Player{Exists: true, ControllerState: control, EndGameCountdown: -1}
		s.Econ.Players[i].Allies[i] = true
	}
	s.Skirmish.NumPlayers = 3
	s.setOnlineVisionTeams(r)
	var handles [3]pool.Handle
	for i := range r.Seats {
		handles[i] = onlineResultCreate(t, s, def, uint8(i))
	}
	return s, handles
}

// settledOnTick reports whether player's settlement ran on tick: an
// overfull stock is clamped to capacity only by a settlement pass
// [05 "Stocks, counters, and waste"].
func settledOnTick(s *Session, player int, tick uint32) bool {
	const overfull = 1e9
	s.Econ.Players[player].Stock[economy.Energy] = overfull
	s.tickPlayers(tick)
	return s.Econ.Players[player].Stock[economy.Energy] != overfull
}

// Nanolathe Modern policy (2026-10-09): a hosted computer keeps playing when
// its host human is defeated. Its host's countdown and ending latch gate only
// the host, so the computer settles through the host's whole countdown and
// after it, and the battle goes on for the other human. Strict 3.1 keeps
// retail's machine-wide countdown: the host's countdown gates its hosted
// computer's settlement from the due that arms it [05 R-ECO-01 §1]
// [08 R-SKIR-01 §3]. Online admission is Modern-only, so the Strict answer is
// exercised here at the seam and its gate.
func TestHostedComputerOutlivesItsDefeatedHost(t *testing.T) {
	if !(StrictSeats{}).ComputersStopWithHost() || (ModernSeats{}).ComputersStopWithHost() ||
		CommunityRuleSet().Seats.ComputersStopWithHost() || !StrictRuleSet().Seats.ComputersStopWithHost() ||
		ModernRuleSet().Seats.ComputersStopWithHost() {
		t.Fatal("the reserved sets answer the countdown question wrongly")
	}
	for _, tc := range []struct {
		name  string
		rules RuleSet
		gated bool
	}{
		{"Modern", ModernRuleSet(), false},
		{"Community", CommunityRuleSet(), false},
		{"Strict", StrictRuleSet(), true},
	} {
		s, h := onlineCountdownFixture(t, tc.rules)
		if s.onlineResults.hosted != [10]int8{-1, -1, 0, -1, -1, -1, -1, -1, -1, -1} {
			t.Fatalf("%s: hosted rows %v", tc.name, s.onlineResults.hosted)
		}
		if !settledOnTick(s, 2, 0) {
			t.Fatalf("%s: the computer did not settle while its host played", tc.name)
		}
		onlineResultKill(t, s, h[0])
		ended := false
		for tick := uint32(30); tick <= 30*12; tick += 30 {
			settled := settledOnTick(s, 2, tick)
			host := &s.Econ.Players[0]
			if host.EndGameCountdown < 0 && !host.GameEnded {
				t.Fatalf("%s: the host's defeat did not arm at tick %d", tc.name, tick)
			}
			computer := &s.Econ.Players[2]
			if tc.gated {
				if settled || computer.EndGameCountdown != host.EndGameCountdown || computer.GameEnded != host.GameEnded {
					t.Fatalf("%s: tick %d: computer settled %v under its host's countdown %d/%v (its own %d/%v)", tc.name, tick, settled, host.EndGameCountdown, host.GameEnded, computer.EndGameCountdown, computer.GameEnded)
				}
			} else if !settled || computer.EndGameCountdown != -1 || computer.GameEnded {
				t.Fatalf("%s: tick %d: the computer stopped with its host (settled %v, countdown %d, ended %v)", tc.name, tick, settled, computer.EndGameCountdown, computer.GameEnded)
			}
			ended = ended || host.GameEnded
			if s.onlineResults.seats[1].latch.IsEnding() || s.OnlineBattleEnded() {
				t.Fatalf("%s: the other human's battle ended at tick %d", tc.name, tick)
			}
		}
		if !ended {
			t.Fatalf("%s: the host's countdown never ended", tc.name)
		}
		if r := s.ResultForSeat(2); r.Kind != "" {
			t.Fatalf("%s: the computer has a result row %+v", tc.name, r)
		}
		// The computer is an opponent in the victory sweep: the surviving
		// human wins only once it is gone.
		onlineResultKill(t, s, h[2])
		s.tickPlayers(30 * 13)
		if r := s.ResultForSeat(1); r.Kind != "victory" {
			t.Fatalf("%s: the last human's result %+v once the computer is gone", tc.name, r)
		}
	}
}

// onlineComputerInstall is the authored restriction install with its map
// replaced by one offering four start positions, so two humans and two
// computers fit it.
func onlineComputerInstall(t *testing.T) (vfs.FSOps, *content.Catalog) {
	t.Helper()
	fs, _ := restrictionMatchInstall(t)
	mounted, ok := fs.(*vfs.FS)
	if !ok {
		t.Fatalf("install is %T", fs)
	}
	ota := `[GlobalHeader]{MinWindSpeed=100;MaxWindSpeed=200;Gravity=112;
[Schema 0]{Type=Network 1;SurfaceMetal=1;
[specials]{[special0]{specialwhat=StartPos1;XPos=128;ZPos=128;}
[special1]{specialwhat=StartPos2;XPos=384;ZPos=320;}
[special2]{specialwhat=StartPos3;XPos=128;ZPos=384;}
[special3]{specialwhat=StartPos4;XPos=384;ZPos=128;}}}}`
	var archive bytes.Buffer
	if err := vfs.WriteArchive(&archive, []vfs.ArchiveFile{{Path: "maps/portable.ota", Data: []byte(ota)}}, vfs.ArchiveWriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := mounted.MountArchiveReader("four.hpi", bytes.NewReader(archive.Bytes()), int64(archive.Len()), 20, vfs.ArchiveOptions{}); err != nil {
		t.Fatal(err)
	}
	cat, err := content.Compile(mounted)
	if err != nil {
		t.Fatal(err)
	}
	if capacity, err := OnlineMapCapacity(cat, restrictionMatchMap); err != nil || capacity != 4 {
		t.Fatalf("overlay map capacity %d, %v", capacity, err)
	}
	return mounted, cat
}

// onlineComputerFixtureMatch resolves and freezes a lobby setup on the
// four-position install.
func onlineComputerFixtureMatch(t *testing.T, setup OnlineMatchSetup) (*content.SimulationInputs, EffectiveMatchConfig) {
	t.Helper()
	fs, cat := onlineComputerInstall(t)
	setup.MapName = restrictionMatchMap
	setup.SideCount = len(OnlineSides(cat))
	room := matchTestRoom()
	schema, err := OnlineMapSchema(fs, cat, setup.MapName, setup.Rows())
	if err != nil {
		t.Fatal(err)
	}
	room.MapSchema = schema
	_, options := restrictionMatchSetup()
	r, err := NewOnlineMatchRequest(setup, options, room)
	if err != nil {
		t.Fatal(err)
	}
	config := resolveMatch(t, r)
	inputs, err := FreezeMatchInputs(fs, cat, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	return inputs, config
}

// Two humans and two computers compose and play alike whichever human seat
// a composition presents; each computer borrows seat 0's perspective, joins
// its team's vision team, and has no result row; the rehearsal admits them
// and agrees across seats.
func TestOnlineComputerSeatsComposeAlike(t *testing.T) {
	setup := onlineComputerTestSetup(false)
	setup.Computers[0].Difficulty = 1
	setup.Computers[1].Difficulty = 1
	inputs, config := onlineComputerFixtureMatch(t, setup)
	var sessions [2]*Session
	for seat := range sessions {
		s, err := NewPlaytestSkirmish(inputs, config, uint8(seat), nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.closeAIControllers)
		if err := s.PrepareGrantedBattle(); err != nil {
			t.Fatal(err)
		}
		sessions[seat] = s
	}
	a := sessions[0]
	if _, err := NewPlaytestSkirmish(inputs, config, 2, nil); err == nil {
		t.Fatal("a computer row was admitted as the local seat")
	}
	for player, want := range []visibility.PlayerID{0, 1, 0, 0, 4} {
		if got := a.Vis.PerspectiveOf(visibility.PlayerID(player)); got != want {
			t.Fatalf("player %d reads perspective %d, want %d", player, got, want)
		}
	}
	if a.onlineResults.seats[2].present || a.onlineResults.seats[3].present || a.AI[2] == nil || a.AI[3] == nil ||
		a.AI[2].Controller != ai.ControllerClassic || a.AI[3].Controller != ai.ControllerModern {
		t.Fatal("the computer rows are not computer players without results")
	}
	for tick := uint32(1); tick <= 300; tick++ {
		for _, s := range sessions {
			if err := s.StepGranted(tick); err != nil {
				t.Fatal(err)
			}
		}
		if a.UnitStateChecksum() != sessions[1].UnitStateChecksum() || a.SimRNG().Draws() != sessions[1].SimRNG().Draws() || a.CrtRNG().Draws() != sessions[1].CrtRNG().Draws() {
			t.Fatalf("the compositions diverged at tick %d", tick)
		}
	}
	// The Classic computer is human 1's teammate: one vision team.
	if !bytes.Equal(a.Vis.ByteGrid(1), a.Vis.ByteGrid(2)) || bytes.Equal(a.Vis.ByteGrid(0), a.Vis.ByteGrid(1)) {
		t.Fatal("the computer's team does not share sight, or the teams do")
	}
	want := rehearsalDigestOf(t, inputs, config, 0)
	if got := rehearsalDigestOf(t, inputs, config, 1); got != want {
		t.Fatalf("seat 1's rehearsal digest %x, want %x", got, want)
	}
}

// Composition refuses a computer another seat hosts and more rows than the
// map's start positions.
func TestOnlineComputerSeatsRefused(t *testing.T) {
	setup := onlineComputerTestSetup(false)
	inputs, config := onlineComputerFixtureMatch(t, setup)
	r := config.Request()
	r.Seats[3].HostSeat = 1
	foreign := resolveMatch(t, r)
	if _, err := NewPlaytestSkirmish(inputs, foreign, 0, nil); err == nil {
		t.Fatal("a computer hosted by seat 1 was admitted")
	}
	r = config.Request()
	r.Seats[2], r.Seats[1] = r.Seats[1], r.Seats[2]
	r.Seats[1].Participant, r.Seats[2].Participant = MatchParticipantID{}, matchTestID(2)
	interleaved := resolveMatch(t, r)
	if _, err := NewPlaytestSkirmish(inputs, interleaved, 0, nil); err == nil {
		t.Fatal("a computer row between the humans was admitted")
	}
	setup.Computers = append(setup.Computers, OnlineComputer{Color: 8})
	r, err := NewOnlineMatchRequest(setup, SkirmishEntryOptions{}, matchTestRoom())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Seats) != 5 {
		t.Fatalf("%d rows", len(r.Seats))
	}
	r.MapSchema, r.MapName = config.Request().MapSchema, config.Request().MapName
	r.UnitLimit, r.Community, r.ContentProfile = config.Request().UnitLimit, config.Request().Community, config.Request().ContentProfile
	over := resolveMatch(t, r)
	if _, err := NewPlaytestSkirmish(inputs, over, 0, nil); err == nil || !strings.Contains(err.Error(), "start positions") {
		t.Fatalf("five rows on a four-position map: %v", err)
	}
}

// Nanolathe Modern seat policy (2026-10-09): a computer keeps normal sight
// after its host's defeat. While seat 0, defeated, still hosts live
// computers, its pass stays an ordinary viewer's, so its computers read an
// unseen opponent as unseen; binding Strict 3.1, whose computers stop with
// their host, projects the defeated viewer's pass back, which marks every
// unit friendly [03 R-VIS-01 §4] pass 1.
func TestDefeatedHostKeepsItsComputersSight(t *testing.T) {
	setup := onlineComputerTestSetup(false)
	setup.Computers[0].Difficulty = 1
	setup.Computers[1].Difficulty = 1
	inputs, config := onlineComputerFixtureMatch(t, setup)
	s, err := NewPlaytestSkirmish(inputs, config, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.closeAIControllers)
	if err := s.PrepareGrantedBattle(); err != nil {
		t.Fatal(err)
	}
	first := func(player int) *units.Unit {
		var found *units.Unit
		s.Units.ForEachPlayerSliceLive(player, func(u *units.Unit) {
			if found == nil {
				found = u
			}
		})
		return found
	}
	host, opponent := first(0), first(1)
	if host == nil || opponent == nil {
		t.Fatal("a human has no commander")
	}
	if err := s.EnqueueSeatCommand(CommandStamp{Seat: 0, Tick: 1, Position: 1}, SeatCommand{Kind: SeatSelfDestruct,
		SelfDestruct: SelfDestructPayload{Actors: []pool.UnitRef{{Handle: host.Handle, Serial: host.AllocationSerial}}}}); err != nil {
		t.Fatal(err)
	}
	tick := uint32(0)
	step := func(n uint32) {
		t.Helper()
		for end := tick + n; tick < end; {
			tick++
			if err := s.StepGranted(tick); err != nil {
				t.Fatal(err)
			}
		}
	}
	for s.Units.LiveCountForPlayer(0) != 0 {
		if step(1); tick > 900 {
			t.Fatal("the host's commander did not self-destruct")
		}
	}
	step(31) // the host's next settlement due runs its pass
	if !s.ownerEliminated(0) || s.Units.LiveCountForPlayer(2) == 0 || s.Units.LiveCountForPlayer(3) == 0 || s.OnlineBattleEnded() {
		t.Fatal("the fixture is not a defeated host with live computers in a running battle")
	}
	for _, c := range []uint8{2, 3} {
		if got := s.sensorStatus(c, opponent); got&visibility.FriendlyMask != 0 {
			t.Fatalf("Modern: computer %d reads the unseen opponent's commander as %#x", c, got)
		}
	}
	s.BindRules(StrictRuleSet())
	step(30)
	for _, c := range []uint8{2, 3} {
		if got := s.sensorStatus(c, opponent); got&visibility.FriendlyMask != visibility.FriendlyMask {
			t.Fatalf("Strict: computer %d reads the opponent's commander as %#x, not a defeated viewer's friendly mark", c, got)
		}
	}
}

// An online Modern computer's own sight predicate reads the sensor status of
// the perspective it borrows, so under Permanent LOS it keeps the sonar
// underwater exemption [03 §3.2] C8 step 3 that a single-player computer
// reads from the unit's own status word, written by the human's pass it
// borrows there.
func TestModernComputerSightReadsItsHostsSonar(t *testing.T) {
	w, def := eliminationFixtureWorld(t)
	terrain := &world.Terrain{CellW: 64, CellH: 64, SeaLevel: 50}
	s := &Session{Units: w, Econ: &economy.Service{}, World: terrain, Vis: visibility.New(terrain, visibility.ModeHistoryEnabled)}
	s.Econ.Players[2] = economy.Player{Exists: true, ControllerState: 2}
	h := onlineResultCreate(t, s, def, 1)
	target := s.Units.Unit(h)
	target.X, target.Y, target.Z = 200<<16, 10<<16, 200<<16 // below sea level
	words := s.Vis.WordMask()
	for i := range words {
		words[i] |= 1 << 2 // the computer has explored the whole map
	}
	// Single-player: the unit's own status word decides, as before.
	target.Flags &^= visibility.SonarBit
	if s.computerPlayerSeesOwn(2, target) {
		t.Fatal("single-player: an underwater unit without sonar contact is visible")
	}
	target.Flags |= visibility.SonarBit
	if !s.computerPlayerSeesOwn(2, target) {
		t.Fatal("single-player: the unit's sonar contact is not read")
	}
	// Online the unit's own word carries no contact; the computer reads its
	// host's bank, which seat 0's sonar wrote.
	target.Flags &^= visibility.SonarBit
	s.Vis.EnableOwnerPerspectives()
	s.Vis.SetPerspectiveHost(2, 0)
	if s.computerPlayerSeesOwn(2, target) {
		t.Fatal("online: visible before any sonar contact")
	}
	status, emitter := target.Flags, uint32(0)
	s.Vis.SensorTickForPerspective(0, false, 30, 3, []visibility.SensorUnit{
		{ID: uint16(h), AllocationSerial: target.AllocationSerial, Owner: 1, Status: &status, X: target.X, Y: target.Y, Z: target.Z, Alive: true},
		{ID: 60, AllocationSerial: 9000, Owner: 0, Status: &emitter, X: 220 << 16, Y: 10 << 16, Z: 200 << 16, Alive: true, Active: true, SonarDistance: 200},
	})
	if target.Flags&visibility.SonarBit != 0 || !s.computerPlayerSeesOwn(2, target) {
		t.Fatal("online: the computer does not read its host's sonar contact")
	}
}
