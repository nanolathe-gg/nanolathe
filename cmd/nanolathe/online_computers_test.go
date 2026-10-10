package main

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// roomComputers decodes the base configuration the host last sent and reads
// its computers back as every seat does (session.OnlineComputersOf).
func roomComputers(t *testing.T, fake *fakeOnlineLobby) []session.OnlineComputer {
	t.Helper()
	base, err := session.DecodeMatchConfig(fake.config)
	if err != nil {
		t.Fatal(err)
	}
	return session.OnlineComputersOf(base.Request())
}

// The host adds a computer from the row after the last player, edits it with
// its own row's controls and removes it through the name's computer cycle;
// every change is a new base configuration that clears readiness (§6.6,
// §16.6).
func TestOnlineLobbyHostAddsEditsAndRemovesComputers(t *testing.T) {
	useOnlineSessionSeams(t, 4)
	g := onlineTestShell(t)
	g.setup.Difficulty = 1
	fake := newFakeOnlineLobby(0, 0, 1)
	openTestLobby(t, g, fake, onlineTestCatalog())
	r := &g.online.room
	p := r.panel
	if lobbyText(g, "Player2") != onlineAddComputerCaption || !p.ActiveOf("Player2") || p.ActiveOf("Side2") || p.ActiveOf("Player3") || p.ActiveOf(lobbyLevelHead) {
		t.Fatalf("the host's Add computer row: %q", lobbyText(g, "Player2"))
	}
	fake.state.Seats[1].Ready = true
	version := fake.state.ConfigVersion
	g.activateGadget("Player2")
	g.pollOnline()
	computers := roomComputers(t, fake)
	want := session.OnlineComputer{Side: 0, Color: 2, Kind: ai.ControllerModern, Difficulty: 1}
	if len(computers) != 1 || computers[0] != want || len(r.settings.computers) != 1 || r.settings.computers[0] != want {
		t.Fatalf("added %+v, settings %+v", computers, r.settings.computers)
	}
	if fake.state.ConfigVersion == version || fake.state.Seats[1].Ready {
		t.Fatal("adding a computer did not replace the base or clear readiness")
	}
	if lobbyText(g, "Player2") != "Modern AI" || lobbyText(g, lobbyLevel+"2") != "Medium" || p.StatusAt(p.Index("Color2")) != 2 || !p.ActiveOf(lobbyLevelHead) || p.ActiveOf("Metal2") {
		t.Fatalf("computer row: %q %q colour %d", lobbyText(g, "Player2"), lobbyText(g, lobbyLevel+"2"), p.StatusAt(p.Index("Color2")))
	}
	if lobbyText(g, "Player3") != onlineAddComputerCaption {
		t.Fatal("no Add computer row after the computer")
	}
	// Difficulty, side and team cycle on the computer's row.
	for _, name := range []string{"Energy2", "Energy2", "Side2", "Allies2"} {
		g.activateGadget(name)
	}
	g.pollOnline()
	if got := roomComputers(t, fake)[0]; got.Difficulty != 0 || got.Side != 1 || got.Team != 1 {
		t.Fatalf("edited computer %+v", got)
	}
	if lobbyText(g, lobbyLevel+"2") != "Easy" || p.StageAt(p.Index("Side2")) != 1 || p.StatusAt(p.Index("Allies2")) != 1 {
		t.Fatal("the computer's row does not show its edits")
	}
	// Colours step past every player's, humans' and computers'.
	g.activateGadget("Player3")
	g.pollOnline()
	if got := roomComputers(t, fake); len(got) != 2 || got[1].Color != 3 || lobbyText(g, "Player2") != "Modern AI 1" || lobbyText(g, "Player3") != "Modern AI 2" {
		t.Fatalf("second computer %+v", got)
	}
	g.activateGadget("Color2")
	if got := roomComputers(t, fake)[0].Color; got != 4 {
		t.Fatalf("colour stepped to %d, want 4 past the second computer's 3", got)
	}
	g.activateOnlineColorBack(2)
	g.activateOnlineColorBack(2)
	if got := roomComputers(t, fake)[0].Color; got != 9 {
		t.Fatalf("colour stepped back to %d, want 9 past the humans' 1 and 0", got)
	}
	// A colour a placeholder seat would hold is stored as chosen once no
	// present player holds it.
	fake.state.Seats[0].Color, fake.state.Seats[1].Color = 5, 6
	g.pollOnline()
	g.activateGadget("Color2")
	if got := roomComputers(t, fake)[0].Color; got != 0 || p.StatusAt(p.Index("Color2")) != 0 {
		t.Fatalf("a computer on colour 0 was stored as %d", got)
	}
	// Modern to Classic, then out of the room.
	g.activateGadget("Player2")
	if got := roomComputers(t, fake); got[0].Kind != ai.ControllerClassic || lobbyText(g, "Player2") != "Classic AI 1" {
		t.Fatalf("kind %+v", got)
	}
	g.activateGadget("Player2")
	if got := roomComputers(t, fake); len(got) != 1 || got[0].Color != 3 || lobbyText(g, "Player2") != "Modern AI" {
		t.Fatalf("after removal %+v, row %q", got, lobbyText(g, "Player2"))
	}
	// A ready host changes nothing.
	r.prepared = &onlinePrepared{key: r.key()}
	g.activateGadget(lobbyReady)
	version = fake.state.ConfigVersion
	for _, name := range []string{"Player2", "Energy2", "Player3"} {
		g.activateGadget(name)
	}
	if fake.state.ConfigVersion != version || !greyed(p, "Player3") {
		t.Fatal("a ready host changed its computers")
	}
}

// A guest sees the host's computers and cannot change them.
func TestOnlineLobbyGuestSeesComputers(t *testing.T) {
	useOnlineSessionSeams(t, 4)
	g := onlineTestShell(t)
	fake := newFakeOnlineLobby(1, 0, 1)
	openTestLobby(t, g, fake, onlineTestCatalog())
	r := &g.online.room
	settings := r.settings
	settings.computers = []session.OnlineComputer{{Team: 2, Side: 1, Color: 7, Kind: ai.ControllerClassic, Difficulty: 2}}
	base, err := onlineBaseConfig(nil, onlineTestCatalog(), settings, onlineFrozenOf(r.baseReq))
	if err != nil {
		t.Fatal(err)
	}
	if fake.config, err = session.EncodeMatchConfig(base); err != nil {
		t.Fatal(err)
	}
	fake.state.ConfigVersion++
	g.pollOnline()
	p := r.panel
	if lobbyText(g, "Player2") != "Classic AI" || lobbyText(g, lobbyLevel+"2") != "Hard" || p.StatusAt(p.Index("Color2")) != 7 || p.StageAt(p.Index("Side2")) != 1 || p.ActiveOf("Player3") {
		t.Fatalf("the guest's computer row: %q %q", lobbyText(g, "Player2"), lobbyText(g, lobbyLevel+"2"))
	}
	if !strings.Contains(p.HelpOf("Player2"), "the host added") {
		t.Fatalf("guest help %q", p.HelpOf("Player2"))
	}
	version := fake.state.ConfigVersion
	for _, name := range []string{"Player2", "Side2", "Color2", "Allies2", "Energy2"} {
		g.activateGadget(name)
	}
	if fake.state.ConfigVersion != version || len(r.settings.computers) != 1 {
		t.Fatal("a guest changed a computer")
	}
	// The guest's own colour steps past the computer's.
	fake.state.Seats[1].Color = 6
	g.pollOnline()
	g.activateGadget("Color1")
	if got := fake.colors[len(fake.colors)-1]; got != 8 {
		t.Fatalf("the guest stepped to %d, want 8 past the computer's 7", got)
	}
}

// Computers count as players against the map, ten players and Survival's
// three survivors, and join the one-team test; the Add computer row leaves
// room for the two humans every battle needs.
func TestOnlineLobbyComputerLimits(t *testing.T) {
	useOnlineSessionSeams(t, 4)
	g := onlineTestShell(t)
	fake := newFakeOnlineLobby(0, 0, 1)
	openTestLobby(t, g, fake, onlineTestCatalog())
	r := &g.online.room
	p := r.panel
	g.activateGadget("Player2")
	g.activateGadget("Player3")
	if len(r.settings.computers) != 2 || p.ActiveOf("Player4") {
		t.Fatal("the full four-player map offers another computer")
	}
	if block := g.onlineLobbyBlock(); block != "" {
		t.Fatalf("two players and two computers on a four-player map: %q", block)
	}
	fake.state.Seats[2].Present = true
	fake.state.Seats[2].Color = 9
	g.pollOnline()
	if !greyed(p, lobbyReady) || lobbyText(g, lobbyStatus) != "This map supports 4 players, computers included." || lobbyText(g, "Player2") != "Player 3" || lobbyText(g, "Player3") != "Modern AI 1" {
		t.Fatalf("over capacity: %q", lobbyText(g, lobbyStatus))
	}
	fake.state.Seats[2].Present = false
	g.pollOnline()
	// Everyone on one team, computers included, cannot start.
	fake.state.Seats[0].Team, fake.state.Seats[1].Team = 1, 1
	g.pollOnline()
	g.activateGadget("Allies2")
	g.activateGadget("Allies3")
	if !strings.Contains(lobbyText(g, lobbyStatus), "Everyone is on one team") {
		t.Fatalf("one team with computers: %q", lobbyText(g, lobbyStatus))
	}
	g.activateGadget("Allies3")
	if strings.Contains(lobbyText(g, lobbyStatus), "Everyone is on one team") || p.StatusAt(p.Index("Allies2")) != 0 || p.StatusAt(p.Index("Allies3")) != 3 {
		t.Fatalf("a computer on team 2: %q", lobbyText(g, lobbyStatus))
	}
	// Survival seats one computer beside its two players.
	g.activateGadget(lobbyGameType)
	if r.settings.survival || !strings.Contains(r.notice, "Survival allows one computer player") {
		t.Fatalf("Survival with two computers: %v %q", r.settings.survival, r.notice)
	}
	g.activateGadget("Player3")
	g.activateGadget("Player3")
	g.activateGadget(lobbyGameType)
	if !r.settings.survival || len(r.settings.computers) != 1 || r.settings.computers[0].Team != 0 || p.ActiveOf("Player3") || p.ActiveOf("Allies2") {
		t.Fatalf("Survival with one computer: %v %+v", r.settings.survival, r.settings.computers)
	}
	if block := g.onlineLobbyBlock(); block != "" {
		t.Fatalf("two survivors and a computer: %q", block)
	}
	fake.state.Seats[2].Present = true
	g.pollOnline()
	if lobbyText(g, lobbyStatus) != "Survival allows up to 3 players, computers included." {
		t.Fatalf("three survivors and a computer: %q", lobbyText(g, lobbyStatus))
	}
	if onlineCanAddComputer(true, 0, 1, 0) != true || onlineCanAddComputer(true, 0, 3, 0) || onlineCanAddComputer(false, 4, 1, 2) || !onlineCanAddComputer(false, 0, 1, 7) || onlineCanAddComputer(false, 0, 1, 8) {
		t.Fatal("the add rule does not keep room for two humans")
	}
}

// The colours the lobby shows are the ones composition resolves, for every
// arrangement of humans and stored computer colours.
func TestOnlineComputerColorsMatchComposition(t *testing.T) {
	frozen := onlineCreationFrozen(&contentSet{profile: "retail", limits: content.RetailLimits()}, [2]uint32{1, 2}, content.Mutators{}, content.Restrictions{}, nil)
	for _, c := range []struct {
		seats     []uint8
		computers []uint8
	}{
		{[]uint8{0, 1}, []uint8{2, 3}},
		{[]uint8{0, 1}, []uint8{1, 0}},
		{[]uint8{5, 2}, []uint8{2, 2, 0}},
		{[]uint8{0, 1, 2}, []uint8{9, 4, 4, 3, 1}},
	} {
		setup := session.OnlineMatchSetup{MapName: "Test Map", SideCount: 2}
		for _, color := range c.seats {
			setup.Seats = append(setup.Seats, session.OnlineSeat{Color: color})
		}
		for _, color := range c.computers {
			setup.Computers = append(setup.Computers, session.OnlineComputer{Color: color, Kind: ai.ControllerModern})
		}
		r, err := session.NewOnlineMatchRequest(setup, frozen.options, frozen.room)
		if err != nil {
			t.Fatal(err)
		}
		shown := onlineComputerColors(setup.Seats, setup.Computers)
		for k := range shown {
			if got := r.Seats[len(c.seats)+k].Color; got != shown[k] {
				t.Fatalf("seats %v computers %v: computer %d composes on %d, the lobby shows %d", c.seats, c.computers, k, got, shown[k])
			}
		}
		if onlineHumanCount(r) != len(c.seats) {
			t.Fatalf("human count %d of %d", onlineHumanCount(r), len(c.seats))
		}
	}
}

// Every seat composes the base's computers after its present humans, and the
// base stores a computer's colour as the host chose it.
func TestOnlineFinalConfigurationCarriesComputers(t *testing.T) {
	setups := useOnlineSessionSeams(t, 0)
	base := onlineTestBase(t, "Test Map", session.MatchMod{}, content.Mutators{})
	settings := onlineSettingsOf(base.Request())
	settings.computers = []session.OnlineComputer{{Team: 1, Color: 0, Kind: ai.ControllerClassic, Difficulty: 2}, {Team: 2, Side: 1, Color: 1, Kind: ai.ControllerModern}}
	withComputers, err := onlineBaseConfig(nil, onlineTestCatalog(), settings, onlineFrozenOf(base.Request()))
	if err != nil {
		t.Fatal(err)
	}
	read := onlineSettingsOf(withComputers.Request())
	if len(read.computers) != 2 || read.computers[0] != settings.computers[0] || read.computers[1] != settings.computers[1] {
		t.Fatalf("the base stores %+v, the host chose %+v", read.computers, settings.computers)
	}
	final, err := onlineConfig(nil, onlineTestCatalog(), read, []session.OnlineSeat{{Color: 1}, {Team: 1, Side: 1, Color: 4}}, onlineFrozenOf(withComputers.Request()))
	if err != nil {
		t.Fatal(err)
	}
	last := (*setups)[len(*setups)-1]
	if len(last.Computers) != 2 || last.Computers[1] != settings.computers[1] {
		t.Fatalf("final setup computers %+v", last.Computers)
	}
	rows := final.Request().Seats
	if len(rows) != 4 || rows[2].Role != session.MatchRoleComputer || rows[2].Color != 0 || rows[3].Color != 2 || rows[3].ComputerKind != ai.ControllerModern || onlineHumanCount(final.Request()) != 2 {
		t.Fatalf("final rows %+v", rows)
	}
}
