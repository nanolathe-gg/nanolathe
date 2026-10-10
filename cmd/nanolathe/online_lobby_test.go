package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/netproto"
	"github.com/nanolathe-gg/nanolathe/internal/relay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// openTestLobby opens fake's room on g with a two-seat skirmish base on Test
// Map and polls once.
func openTestLobby(t *testing.T, g *gameShell, fake *fakeOnlineLobby, cat *content.Catalog) {
	t.Helper()
	if err := g.openOnlineScreen(); err != nil {
		t.Fatal(err)
	}
	g.openOnlineLobby(fake, cat, onlineTestBase(t, "Test Map", session.MatchMod{}, content.Mutators{}), "wss://relay.example.test/relay")
	g.pollOnline()
	if !g.onlineLobbyActive() || g.frontend.Mode != modeMenuSkirmish {
		t.Fatal("the lobby did not open as the setup window")
	}
}

func lobbyText(g *gameShell, name string) string { return g.online.room.panel.TextOf(name) }

func TestOnlineLobbyRowsTeamsAndSides(t *testing.T) {
	useOnlineSessionSeams(t, 4)
	g := onlineTestShell(t)
	fake := newFakeOnlineLobby(2, 0, 2, 5)
	openTestLobby(t, g, fake, onlineTestCatalog())
	p := g.online.room.panel
	for row, want := range []string{"Player 1 (Host)", "Player 2 (You)", "Player 3"} {
		if got := p.TextOf("Player" + string(rune('0'+row))); got != want {
			t.Fatalf("row %d: %q, want %q", row, got, want)
		}
	}
	if p.ActiveOf("Player3") || p.ActiveOf("Color3") || p.ActiveOf("Energy0") || !p.ActiveOf("Color0") {
		t.Fatal("rows past the present players, or the energy column, are shown, or the colour column is not")
	}
	if p.StatusAt(p.Index("Allies1")) != 10 {
		t.Fatal("a player without a team shows an allegiance symbol")
	}
	// Only the local row's team and side cycle, and each change reaches the
	// relay, which clears every seat's ready.
	g.activateGadget("Allies0")
	g.activateGadget("Side2")
	if len(fake.teams) != 0 || len(fake.sides) != 0 {
		t.Fatal("another player's row changed")
	}
	g.activateGadget("Allies1")
	g.pollOnline()
	if len(fake.teams) != 1 || fake.teams[0] != 1 || p.StatusAt(p.Index("Allies1")) != 1 {
		t.Fatalf("team cycle: %v, icon %d", fake.teams, p.StatusAt(p.Index("Allies1")))
	}
	fake.state.Seats[5].Team = 1
	g.pollOnline()
	if p.StatusAt(p.Index("Allies1")) != 0 || p.StatusAt(p.Index("Allies2")) != 0 {
		t.Fatal("teammates do not share the joined symbol")
	}
	g.activateGadget("Side1")
	g.pollOnline()
	if len(fake.sides) != 1 || fake.sides[0] != 1 || p.StageAt(p.Index("Side1")) != 1 {
		t.Fatalf("side cycle: %v, stage %d", fake.sides, p.StageAt(p.Index("Side1")))
	}
	// A guest cannot change the host's settings.
	for _, name := range []string{lobbyGameType, "CommanderDeath", "SelectMap", "Start"} {
		if !greyed(p, name) {
			t.Fatalf("a guest is offered %s", name)
		}
	}
	// Survival has no teams.
	g.online.room.settings.survival = true
	g.refreshOnlineLobby()
	if p.ActiveOf("Allies0") || !p.ActiveOf(survivalPaceButton) || p.ActiveOf("StartLocation") || lobbyText(g, lobbyLocLabel) != "Wave Pace" {
		t.Fatal("the Survival lobby shows teams or lacks its options")
	}
}

// The colour column is the setup screen's: a left click steps the local
// player's colour forward and a right click back, past colours other present
// players hold [08 R-SKIR-01 §1], and only while not ready.
func TestOnlineLobbyColours(t *testing.T) {
	useOnlineSessionSeams(t, 4)
	g := onlineTestShell(t)
	fake := newFakeOnlineLobby(2, 0, 2, 5)
	openTestLobby(t, g, fake, onlineTestCatalog())
	r := &g.online.room
	p := r.panel
	colour := func(row string) int { return p.StatusAt(p.Index("Color" + row)) }
	if colour("0") != 0 || colour("1") != 1 || colour("2") != 2 {
		t.Fatalf("arrival colours %d %d %d", colour("0"), colour("1"), colour("2"))
	}
	g.activateGadget("Color0")
	g.activateOnlineColorBack(2)
	if len(fake.colors) != 0 {
		t.Fatal("another player's colour changed")
	}
	steps := []struct {
		back bool
		want uint8
	}{{false, 3}, {true, 1}, {true, 9}, {false, 1}}
	for _, step := range steps {
		if step.back {
			g.activateOnlineColorBack(1)
		} else {
			g.activateGadget("Color1")
		}
		g.pollOnline()
		if got := fake.colors[len(fake.colors)-1]; got != step.want || colour("1") != int(step.want) {
			t.Fatalf("back %v: sent %d, shown %d, want %d", step.back, got, colour("1"), step.want)
		}
	}
	// A ready seat changes nothing, with either button.
	r.prepared = &onlinePrepared{key: r.key()}
	g.activateGadget(lobbyReady)
	sent := len(fake.colors)
	g.activateGadget("Color1")
	g.activateOnlineColorBack(1)
	if len(fake.colors) != sent {
		t.Fatal("a ready seat changed its colour")
	}
	// Another player's colour change clears readiness, and the lobby says so.
	fake.state.Seats[0].Color = 4
	fake.clearReady()
	g.pollOnline()
	if r.ready || !strings.Contains(lobbyText(g, lobbyStatus), "changed team, side or colour") {
		t.Fatalf("colour change: ready %v, status %q", r.ready, lobbyText(g, lobbyStatus))
	}
	if next, ok := nextOnlineColor(fake.state.Seats[2].Color, -1, func(c uint8) bool { return onlineColorHeld(fake.state, 2, c) }); !ok || next != 0 {
		t.Fatalf("a freed colour is not offered: %d %v", next, ok)
	}
}

func TestOnlineLobbyReadyClearingAndStartGating(t *testing.T) {
	useOnlineSessionSeams(t, 4)
	g := onlineTestShell(t)
	fake := newFakeOnlineLobby(0, 0, 1, 2)
	openTestLobby(t, g, fake, onlineTestCatalog())
	r := &g.online.room
	p := r.panel
	if greyed(p, lobbyReady) || !greyed(p, "Start") || greyed(p, lobbyGameType) {
		t.Fatal("the host's new lobby offers the wrong controls")
	}
	// A battle prepared for this room is reported without composing again.
	identity, rehearsal := [32]byte{1}, [32]byte{2}
	r.prepared = &onlinePrepared{key: r.key(), identity: identity, rehearsal: rehearsal}
	g.activateGadget(lobbyReady)
	if len(fake.readies) != 1 || !fake.readies[0] || fake.identities[0] != identity || fake.digests[0] != rehearsal || !r.ready || lobbyText(g, lobbyReady) != "Not ready" {
		t.Fatalf("ready: %v %x %x", fake.readies, fake.identities, fake.digests)
	}
	// A ready seat changes nothing.
	g.activateGadget("Allies0")
	if len(fake.teams) != 0 {
		t.Fatal("a ready seat changed its team")
	}
	fake.state.Seats[1].Ready, fake.state.Seats[2].Ready = true, true
	g.pollOnline()
	if greyed(p, "Start") || !strings.Contains(lobbyText(g, lobbyStatus), "Choose Start") {
		t.Fatalf("all ready: start greyed %v, status %q", greyed(p, "Start"), lobbyText(g, lobbyStatus))
	}
	fake.state.Mismatch = true
	g.pollOnline()
	if !greyed(p, "Start") || !strings.Contains(lobbyText(g, lobbyStatus), "simulate differently") {
		t.Fatalf("mismatch: %q", lobbyText(g, lobbyStatus))
	}
	g.activateGadget("Start")
	if fake.starts != 0 {
		t.Fatal("a mismatched room started")
	}
	// A join clears every seat's ready, and the lobby says why.
	fake.state.Seats[3].Present = true
	fake.clearReady()
	g.pollOnline()
	if r.ready || !strings.Contains(lobbyText(g, lobbyStatus), "joined or left") || lobbyText(g, lobbyReady) != "Ready" {
		t.Fatalf("join: ready %v, status %q", r.ready, lobbyText(g, lobbyStatus))
	}
	// Everyone on one team cannot start or ready.
	for _, i := range []int{0, 1, 2, 3} {
		fake.state.Seats[i].Team = 2
	}
	g.pollOnline()
	if !greyed(p, lobbyReady) || !strings.Contains(lobbyText(g, lobbyStatus), "Everyone is on one team") {
		t.Fatalf("one team: %q", lobbyText(g, lobbyStatus))
	}
	fake.state.Seats[3].Team = 0
	fake.state.Seats[4].Present = true
	g.pollOnline()
	if !greyed(p, lobbyReady) || !strings.Contains(lobbyText(g, lobbyStatus), "This map supports 4 players.") {
		t.Fatalf("capacity: %q", lobbyText(g, lobbyStatus))
	}
	r.settings.survival = true
	g.refreshOnlineLobby()
	if !strings.Contains(lobbyText(g, lobbyStatus), "Survival allows up to 3 players.") {
		t.Fatalf("Survival size: %q", lobbyText(g, lobbyStatus))
	}
	// Hovering a control shows its help in the status line instead.
	g.updateOnlineLobbyHelp(-1, -1)
	if !strings.Contains(lobbyText(g, lobbyStatus), "Survival allows") {
		t.Fatal("the status line was lost without a hovered control")
	}
	// Leave returns to the online screen.
	g.activateGadget("PrevMenu")
	if fake.closes != 1 || g.online == nil || g.activePanel() != g.online.panel || g.online.status != "You left the game." {
		t.Fatal("Leave did not return to the online screen")
	}
}

func TestOnlineLobbyAdoptsTheHostsSettings(t *testing.T) {
	useOnlineSessionSeams(t, 4)
	g := onlineTestShell(t)
	fake := newFakeOnlineLobby(1, 0, 1)
	openTestLobby(t, g, fake, onlineTestCatalog())
	r := &g.online.room
	r.prepared, r.ready = &onlinePrepared{key: r.key()}, true
	fake.state.Seats[1].Ready = true
	g.pollOnline()
	other, err := session.EncodeMatchConfig(onlineTestBase(t, "Unknown Map", session.MatchMod{}, content.Mutators{}))
	if err != nil {
		t.Fatal(err)
	}
	fake.config = other
	fake.state.ConfigVersion++
	fake.clearReady()
	g.pollOnline()
	if r.ready || r.baseReq.MapName != "Unknown Map" || lobbyText(g, "MapName") != "Unknown Map" {
		t.Fatalf("adoption: ready %v map %q", r.ready, r.baseReq.MapName)
	}
	if !greyed(r.panel, lobbyReady) || lobbyText(g, lobbyStatus) != "The host chose Unknown Map, which you don't have." {
		t.Fatalf("missing map: %q", lobbyText(g, lobbyStatus))
	}
	if r.prepared.key == r.key() {
		t.Fatal("a new base configuration kept the prepared battle valid")
	}
	// A base configuration for another mod is refused: the mod is the room's.
	modded, err := session.EncodeMatchConfig(onlineTestBase(t, "Test Map", session.MatchMod{ID: "x", Version: "1", Archive: [32]byte{1}}, content.Mutators{}))
	if err != nil {
		t.Fatal(err)
	}
	fake.config = modded
	fake.state.ConfigVersion++
	g.pollOnline()
	if fake.closes != 1 || g.online == nil || !strings.Contains(g.online.status, "could not be read") {
		t.Fatal("a base configuration for another mod was adopted")
	}
}

func TestOnlineLobbyStartedChecksTheSlot(t *testing.T) {
	useOnlineSessionSeams(t, 4)
	g := onlineTestShell(t)
	stream := &fakeGrantStream{}
	fake := newFakeOnlineLobby(1, 0, 1)
	fake.battle = stream
	openTestLobby(t, g, fake, onlineTestCatalog())
	r := &g.online.room
	r.prepared, r.ready = &onlinePrepared{key: r.key(), slot: 1}, true
	fake.state.Started, fake.state.Slot = true, 0
	g.pollOnline()
	if stream.closes != 1 || g.online == nil || !strings.Contains(g.online.status, "without your prepared battle") {
		t.Fatal("a start at another slot entered the battle")
	}
	// The right slot enters; this prepared battle has no session, so entry
	// fails, the stream closes and the screen says so.
	fake = newFakeOnlineLobby(1, 0, 1)
	fake.battle = stream
	openTestLobby(t, g, fake, onlineTestCatalog())
	r = &g.online.room
	r.prepared, r.ready = &onlinePrepared{key: r.key(), slot: 1}, true
	fake.state.Started, fake.state.Slot = true, 1
	g.pollOnline()
	if stream.closes != 2 || g.online == nil || !strings.Contains(g.online.status, "could not start") {
		t.Fatalf("failed entry: %+v", g.online)
	}
}

func TestOnlineSeatsAndFinalSetup(t *testing.T) {
	setups := useOnlineSessionSeams(t, 0)
	state := relay.HostedLobbyState{Size: relay.HostedMaxSeats}
	for i, s := range map[int]relay.HostedSeatState{0: {Present: true, Team: 1, Color: 2}, 3: {Present: true, Team: 2, Side: 1}, 7: {Present: true, Team: 1, Side: 1, Color: 5}} {
		state.Seats[i] = s
	}
	seats, slot, ok := onlineSeatsOf(state, 3, false)
	want := []session.OnlineSeat{{Team: 1, Color: 2}, {Team: 2, Side: 1}, {Team: 1, Side: 1, Color: 5}}
	if !ok || slot != 1 || len(seats) != 3 || seats[0] != want[0] || seats[1] != want[1] || seats[2] != want[2] {
		t.Fatalf("seats %+v slot %d", seats, slot)
	}
	if seats, _, _ := onlineSeatsOf(state, 3, true); seats[0].Team != 0 || seats[2].Side != 1 || seats[2].Color != 5 {
		t.Fatal("Survival kept teams or lost sides or colours")
	}
	if _, _, ok := onlineSeatsOf(state, 4, false); ok {
		t.Fatal("an absent seat has a slot")
	}
	if onlineOneTeam(seats, nil) || !onlineOneTeam([]session.OnlineSeat{{Team: 2}, {Team: 2}}, nil) || onlineOneTeam([]session.OnlineSeat{{Team: 2}, {Team: 0}}, nil) || onlineOneTeam([]session.OnlineSeat{{}, {}}, nil) {
		t.Fatal("one-team test wrong")
	}
	// The final configuration is the base's frozen part, the settings and
	// the seats: two seats compose through the stand-in.
	base := onlineTestBase(t, "Test Map", session.MatchMod{}, content.Mutators{Health: content.Factor{Num: 2, Den: 1}})
	settings := onlineSettingsOf(base.Request())
	settings.commanderDeath, settings.mapping = 0, 1
	final, err := onlineConfig(nil, onlineTestCatalog(), settings, []session.OnlineSeat{{}, {Side: 1, Color: 1}}, onlineFrozenOf(base.Request()))
	if err != nil {
		t.Fatal(err)
	}
	last := (*setups)[len(*setups)-1]
	r := final.Request()
	if last.MapName != "Test Map" || last.SimSeed != 1 || last.CRTSeed != 2 || len(last.Seats) != 2 || last.Seats[1].Side != 1 || last.Seats[1].Color != 1 || last.Survival {
		t.Fatalf("setup %+v", last)
	}
	if r.Mutators != base.Request().Mutators || r.CommanderDeath != 0 || r.Mapping != 1 || r.Seats[1].Side != 1 || r.Community != base.Request().Community {
		t.Fatalf("final %+v", r)
	}
	// Survival's options reach the setup.
	settings.survival, settings.pace, settings.noAir = true, 2, true
	_, _ = onlineConfig(nil, onlineTestCatalog(), settings, []session.OnlineSeat{{}, {Color: 1}}, onlineFrozenOf(base.Request()))
	last = (*setups)[len(*setups)-1]
	if !last.Survival || !last.SurvivalOptions.Enabled || last.SurvivalOptions.Pace != 2 || !last.SurvivalOptions.NoAir {
		t.Fatalf("Survival setup %+v", last)
	}
}

func TestOnlineIdentityDigestIgnoresTheBuild(t *testing.T) {
	id := netproto.Identity{Protocol: 3, Content: [32]byte{1}, Map: [32]byte{2}, Configuration: [32]byte{3}, Rules: netproto.RuleIdentity{Name: "modern"}}
	built := id
	built.Build = [32]byte{9}
	if onlineIdentityDigest(id) != onlineIdentityDigest(built) {
		t.Fatal("the advisory build changed the identity digest")
	}
	other := id
	other.Configuration[0] ^= 1
	if onlineIdentityDigest(id) == onlineIdentityDigest(other) {
		t.Fatal("a different configuration kept the identity digest")
	}
}

// A hosted seat whose result is final sees it before the battle ends, while
// grants keep running; the loopback play test still waits (§16.6).
func TestOnlineDefeatedSeatSeesItsResult(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	d := &testLocalBattleDriver{}
	b.multiplayer = &battleMultiplayer{driver: d, completed: func() bool { return false }}
	staging := b.sess.Snapshot.BeginWrite()
	staging.Result = frame.ResultView{Ended: true, Kind: "defeat"}
	if err := b.sess.Snapshot.Publish(1); err != nil {
		t.Fatal(err)
	}
	if !b.isResultVisible() {
		t.Fatal("a hosted seat's final defeat waited for the shared end")
	}
	b.multiplayer.failure = errors.New("test disconnect")
	if b.isResultVisible() {
		t.Fatal("a failed transport showed a result")
	}
}
