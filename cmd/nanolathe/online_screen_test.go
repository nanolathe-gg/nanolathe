package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/lockstep"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/relay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// useOnlineTestTemplate replaces the popup windows with authored windows of
// the same shapes — the message window's frame and OK, and the address
// window's prompt, field, OK and Cancel — so the entry builds without retail
// assets.
func useOnlineTestTemplate(t *testing.T) {
	t.Helper()
	saved := onlinePopupTemplate
	onlinePopupTemplate = func(_ *gameShell, logical string) (*gui.Window, error) {
		switch logical {
		case onlineChooserGUI:
			return &gui.Window{Name: "MSGBOX", Rect: gui.Rect{X: 116, Y: 82, W: 372, H: 272}, Header: gui.Header{Panel: "BackTile", CrDefault: "OK", EscDefault: "OK"}, Gadgets: []gui.Gadget{
				{Kind: gui.KindPanel, Name: "HEADER", Active: 1, Rect: gui.Rect{X: 116, Y: 82, W: 372, H: 272}},
				{Kind: gui.KindButton, Name: "OK", Text: "OK", Active: 1, QuickKey: 13, ColorF: 15, Attribs: 2, Rect: gui.Rect{X: 264, Y: 208, W: 80, H: 42}},
			}}, nil
		case onlineAddressGUI:
			return &gui.Window{Name: "TCP", Rect: gui.Rect{X: 70, Y: 97, W: 501, H: 154}, Header: gui.Header{Panel: "BackTile", CrDefault: "HOST", EscDefault: "PREV", DefaultFocus: "ADDRESS"}, Gadgets: []gui.Gadget{
				{Kind: gui.KindPanel, Name: "HEADER", Rect: gui.Rect{X: 70, Y: 97, W: 501, H: 154}},
				{Kind: gui.KindButton, Name: "PREV", Text: "Cancel", Active: 1, ColorF: 15, Attribs: 2, Rect: gui.Rect{X: 358, Y: 123, W: 120, H: 20}},
				{Kind: gui.KindLabel, Name: "TEXT", Text: "Enter TCP address (leave blank to search)", Active: 1, ColorF: 15, Attribs: 0x11, Rect: gui.Rect{X: 51, Y: 39, W: 276, H: 17}},
				{Kind: gui.KindButton, Name: "OK", Text: "OK", Active: 1, ColorF: 15, Attribs: 2, Rect: gui.Rect{X: 358, Y: 94, W: 120, H: 20}},
				{Kind: gui.KindTextBox, Name: "ADDRESS", Text: "Test String", Active: 1, ColorF: 15, MaxChars: 63, Rect: gui.Rect{X: 53, Y: 61, W: 252, H: 26}},
			}}, nil
		}
		return nil, errors.New("no test template " + logical)
	}
	t.Cleanup(func() { onlinePopupTemplate = saved })
}

// onlineTestSkirmishWindow is an authored window with SKIRMISH.GUI's controls
// in their authored places, which the lobby builds on.
func onlineTestSkirmishWindow() *gui.Window {
	button := func(name, text string, stages uint8, x, y, w int32) gui.Gadget {
		return gui.Gadget{Kind: gui.KindButton, Name: name, Text: text, Stages: stages, Active: 1, Rect: gui.Rect{X: x, Y: y, W: w, H: 20}}
	}
	label := func(name, text string, x, y int32) gui.Gadget {
		return gui.Gadget{Kind: gui.KindLabel, Name: name, Text: text, Active: 1, Attribs: 0x11, ColorF: 15, Rect: gui.Rect{X: x, Y: y, W: 117, H: 13}}
	}
	return &gui.Window{Name: "Skirmish.gui", Rect: gui.Rect{W: 640, H: 480}, Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel, Name: "Skirmish.gui", Active: 1, Rect: gui.Rect{W: 640, H: 480}},
		button("Start", "Start", 0, 502, 430, 96),
		button("PrevMenu", "Previous Menu", 0, 476, 379, 120),
		label("TEXT", "Location", 476, 132),
		label("TEXT", "Commander", 476, 76),
		button("SelectMap", "Select Map", 0, 263, 379, 120),
		button("StartLocation", "Fixed|Random", 2, 476, 153, 120),
		button("CommanderDeath", "Game ends|Continues", 2, 476, 96, 120),
		button("Mapping", "Unmapped|Mapped", 2, 476, 210, 120),
		label("MapName", "", 47, 382),
		button("LineOfSight", "Permanent|True|Circular", 3, 476, 267, 120),
		{Kind: gui.KindLabel, Name: "HELPTEXT", Active: 1, Attribs: 0x11, Rect: gui.Rect{X: 42, Y: 330, W: 447, H: 20}},
		button("Difficulty", "Easy|Medium|Hard", 3, 476, 324, 120),
		label("TEXT", "Difficulty", 476, 303),
	}}
}

// useOnlineTestRelay installs a fake relay for the test.
func useOnlineTestRelay(t *testing.T, r *fakeOnlineRelay) {
	t.Helper()
	saved, savedCopy := onlineRelayService, onlineClipboardWrite
	onlineRelayService = r
	onlineClipboardWrite = func(text string) bool { r.copied = append(r.copied, text); return true }
	t.Cleanup(func() { onlineRelayService, onlineClipboardWrite = saved, savedCopy })
}

func onlineTestShell(t *testing.T) *gameShell {
	t.Helper()
	useOnlineTestTemplate(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	main := &gui.Window{Rect: gui.Rect{W: 640, H: 480}, Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel, Active: 1},
		{Kind: gui.KindButton, Name: "MULTI", Active: 1, Rect: gui.Rect{X: 20, Y: 20, W: 80, H: 30}},
		{Kind: gui.KindButton, Name: "SINGLE", Active: 1, Rect: gui.Rect{X: 20, Y: 60, W: 80, H: 30}},
	}}
	g := &gameShell{frontend: ui.NewFrontend(modeMenuMain), cs: testContentSet(vfs.New()), maps: []string{"Test Map", "Second Map"}, skirmishSides: 2,
		assets: &menuAssets{panel: map[shellMode]*retailPanelAssets{modeMenuMain: {window: main}, modeMenuSkirmish: {window: onlineTestSkirmishWindow()}}}}
	g.setup.MapName = "Test Map"
	g.openMenu(modeMenuMain)
	return g
}

// useOnlineSessionSeams records every online setup the lobby composes, and
// stands in for the map schema, which needs map files these tests do not
// mount. capacity, when positive, stands in for the map's start positions.
func useOnlineSessionSeams(t *testing.T, capacity int) *[]session.OnlineMatchSetup {
	t.Helper()
	var setups []session.OnlineMatchSetup
	savedRequest, savedCapacity, savedSchema := newOnlineMatchRequest, onlineMapCapacity, onlineMapSchema
	newOnlineMatchRequest = func(setup session.OnlineMatchSetup, options session.SkirmishEntryOptions, room session.MatchRoomInputs) (session.MatchConfigRequest, error) {
		setups = append(setups, setup)
		return savedRequest(setup, options, room)
	}
	onlineMapSchema = func(vfs.FSOps, *content.Catalog, string, int) (uint32, error) { return 0, nil }
	if capacity > 0 {
		onlineMapCapacity = func(*content.Catalog, string) (int, error) { return capacity, nil }
	}
	t.Cleanup(func() {
		newOnlineMatchRequest, onlineMapCapacity, onlineMapSchema = savedRequest, savedCapacity, savedSchema
	})
	return &setups
}

// onlineTestCatalog is a catalog with the two retail sides' names, authored
// here, for the lobby's side count.
func onlineTestCatalog() *content.Catalog {
	return &content.Catalog{Sides: []*content.SideDef{{Name: "ARM"}, {Name: "CORE"}}}
}

// onlineTestBase is a two-seat skirmish base configuration on mapName.
func onlineTestBase(t *testing.T, mapName string, mod session.MatchMod, mutators content.Mutators) session.EffectiveMatchConfig {
	t.Helper()
	cs := &contentSet{profile: "retail", limits: content.RetailLimits()}
	frozen := onlineCreationFrozen(cs, [2]uint32{1, 2}, mutators, content.Restrictions{}, nil)
	frozen.room.Mod = mod
	base, err := onlineConfig(cs, onlineTestCatalog(), onlineSettings{mapName: mapName, location: 1, commanderDeath: 1}, onlinePlaceholderSeats(nil), frozen)
	if err != nil {
		t.Fatal(err)
	}
	return base
}

type fakeOnlineRelay struct {
	mu        sync.Mutex
	describes []string
	opens     []relay.LocalHello
	sizes     []int
	configs   [][]byte
	describe  func(room string) (relay.HostedRoomDescription, error)
	open      func(room string, hello relay.LocalHello, config []byte) (onlineLobby, error)
	copied    []string
}

func (r *fakeOnlineRelay) Describe(_ context.Context, address, room string, _ relay.HostedDialOptions) (relay.HostedRoomDescription, error) {
	r.mu.Lock()
	r.describes = append(r.describes, address+" "+room)
	r.mu.Unlock()
	if r.describe == nil {
		return relay.HostedRoomDescription{}, errors.New("no describe")
	}
	return r.describe(room)
}

func (r *fakeOnlineRelay) Open(_ context.Context, _, room string, hello relay.LocalHello, config []byte, size int, _ relay.HostedDialOptions) (onlineLobby, error) {
	r.mu.Lock()
	r.opens = append(r.opens, hello)
	r.sizes = append(r.sizes, size)
	r.configs = append(r.configs, config)
	r.mu.Unlock()
	if r.open == nil {
		return nil, errors.New("no open")
	}
	return r.open(room, hello, config)
}

// fakeOnlineLobby behaves as the relay's lobby does for one seat: a team,
// side, settings, join or leave change clears every seat's ready.
type fakeOnlineLobby struct {
	code       string
	seat       uint8
	state      relay.HostedLobbyState
	config     []byte
	err        error
	readies    []bool
	identities [][32]byte
	digests    [][32]byte
	teams      []uint8
	sides      []uint8
	colors     []uint8
	starts     int
	closes     int
	battle     lockstep.Client
}

func newFakeOnlineLobby(seat uint8, present ...int) *fakeOnlineLobby {
	l := &fakeOnlineLobby{code: "CFH234", seat: seat, state: relay.HostedLobbyState{Size: relay.HostedMaxSeats}}
	// Each arrival takes the lowest colour no present seat holds, as the
	// relay assigns it.
	for _, i := range present {
		l.state.Seats[i].Present = true
		l.state.Seats[i].Color = l.freeColor(i)
	}
	return l
}

func (l *fakeOnlineLobby) freeColor(seat int) uint8 {
	for c := range uint8(relay.HostedColors) {
		if !onlineColorHeld(l.state, seat, c) {
			return c
		}
	}
	return 0
}

func (l *fakeOnlineLobby) clearReady() {
	for i := range l.state.Seats {
		l.state.Seats[i].Ready = false
	}
	l.state.Mismatch = false
}

func (l *fakeOnlineLobby) Code() string                           { return l.code }
func (l *fakeOnlineLobby) Seat() uint8                            { return l.seat }
func (l *fakeOnlineLobby) State() (relay.HostedLobbyState, error) { return l.state, l.err }
func (l *fakeOnlineLobby) Configuration() []byte                  { return l.config }
func (l *fakeOnlineLobby) SetConfiguration(config []byte) error {
	l.config = config
	l.state.ConfigVersion++
	l.clearReady()
	return nil
}
func (l *fakeOnlineLobby) SetTeam(team uint8) error {
	l.teams = append(l.teams, team)
	l.state.Seats[l.seat].Team = team
	l.clearReady()
	return nil
}
func (l *fakeOnlineLobby) SetSide(side uint8) error {
	l.sides = append(l.sides, side)
	l.state.Seats[l.seat].Side = side
	l.clearReady()
	return nil
}
func (l *fakeOnlineLobby) SetColor(color uint8) error {
	l.colors = append(l.colors, color)
	if color >= relay.HostedColors || l.state.Seats[l.seat].Ready || onlineColorHeld(l.state, int(l.seat), color) {
		return nil
	}
	l.state.Seats[l.seat].Color = color
	l.clearReady()
	return nil
}
func (l *fakeOnlineLobby) SetReady(ready bool, identity, rehearsal [32]byte) error {
	l.readies = append(l.readies, ready)
	l.identities = append(l.identities, identity)
	l.digests = append(l.digests, rehearsal)
	l.state.Seats[l.seat].Ready = ready
	return nil
}
func (l *fakeOnlineLobby) Start() error { l.starts++; return nil }
func (l *fakeOnlineLobby) Battle() lockstep.Client {
	if !l.state.Started {
		return nil
	}
	return l.battle
}
func (l *fakeOnlineLobby) Close() error { l.closes++; return nil }

type fakeGrantStream struct{ closes int }

func (c *fakeGrantStream) Submit([]byte) (uint64, error) { return 0, nil }
func (c *fakeGrantStream) ReadGrant() (relay.LocalGrant, error) {
	return relay.LocalGrant{}, errors.New("closed")
}
func (c *fakeGrantStream) Acknowledge(uint32, [32]byte, bool, bool) error { return nil }
func (c *fakeGrantStream) Close() error                                   { c.closes++; return nil }

func greyed(p *ui.Panel, name string) bool {
	i := p.Index(name)
	return i < 0 || p.Window.Gadgets[i].GrayedOut&1 != 0
}

// awaitOnline polls the shell until its running job finishes.
func awaitOnline(t *testing.T, g *gameShell) {
	t.Helper()
	end := time.Now().Add(10 * time.Second)
	for g.online != nil && (g.online.job != nil || g.online.room.readyJob != nil) {
		if time.Now().After(end) {
			t.Fatalf("online job never finished: %+v", g.online)
		}
		g.pollOnline()
		time.Sleep(time.Millisecond)
	}
}

func TestMainMenuMultiOpensTheOnlineChooser(t *testing.T) {
	g := onlineTestShell(t)
	main := g.activePanel()
	multi := main.Index("MULTI")
	// The shell has no skirmish map yet, and MULTI greys as Skirmish does.
	if main.Window.Gadgets[multi].GrayedOut&1 == 0 || main.Fires(multi) {
		t.Fatal("MULTI was offered without skirmish content")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "maps"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "maps", "Test Map.tnt"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := g.cs.unmappedMount.MountDirectory(root, 20); err != nil {
		t.Fatal(err)
	}
	g.assets.panel[modeMenuMap] = &retailPanelAssets{window: &gui.Window{Rect: gui.Rect{W: 640, H: 480}, Gadgets: []gui.Gadget{{Kind: gui.KindPanel, Active: 1}}}}
	g.openMenu(modeMenuMain)
	main = g.activePanel()
	if main.Window.Gadgets[multi].GrayedOut != 0 || !main.Fires(multi) {
		t.Fatal("MULTI is not enabled")
	}
	g.activateGadget("MULTI")
	if g.online == nil || g.online.view != onlineChooser || g.activePanel() != g.online.panel || g.frontend.Panels.Under() != main {
		t.Fatal("MULTI did not open the chooser over the main menu")
	}
	// One sentence, the two choices, and Server and Cancel off the main path.
	p := g.online.panel
	if p.TextOf("PROMPT") != onlineIntro || !strings.Contains(p.TextOf("SERVERNAME"), defaultOnlineServer) {
		t.Fatalf("prompt %q server %q", p.TextOf("PROMPT"), p.TextOf("SERVERNAME"))
	}
	for _, name := range []string{"CREATE", "JOIN", "SERVER", "PREVMENU"} {
		if p.Index(name) < 0 || greyed(p, name) {
			t.Fatalf("%s missing or unavailable", name)
		}
	}
	if r := p.Window.Rect; r.X+r.W/2 != retailScreenW/2 || r.Y+r.H/2 != retailScreenH/2 {
		t.Fatalf("the chooser is not centred: %+v", r)
	}
	// Join asks for the code in the address window, with the field focused.
	g.activateGadget("JOIN")
	p = g.online.panel
	if g.online.view != onlineCodeEntry || p.TextOf("TEXT") != onlineCodePrompt || p.TextOf("OK") != "Join" || p.TextOf("ADDRESS") != "" || !p.EditorCaptured() || p.EditorIndex() != p.Index("ADDRESS") {
		t.Fatalf("code popup: %q %q %q captured %v", p.TextOf("TEXT"), p.TextOf("OK"), p.TextOf("ADDRESS"), p.EditorCaptured())
	}
	if g.frontend.Panels.Under() != main {
		t.Fatal("the code popup did not replace the chooser")
	}
	// A typed or pasted code shows in capitals.
	p.SetText("ADDRESS", "k7m-2px")
	g.pollOnline()
	if p.TextOf("ADDRESS") != "K7M-2PX" {
		t.Fatalf("code field %q", p.TextOf("ADDRESS"))
	}
	g.activateGadget("PREV")
	if g.online.view != onlineChooser {
		t.Fatal("Cancel did not return to the chooser")
	}
	g.activateGadget("PREVMENU")
	if g.online != nil || g.activePanel() != main {
		t.Fatal("Cancel did not return to the main menu")
	}
}

func TestOnlineServerPopupRemembersTheServer(t *testing.T) {
	g := onlineTestShell(t)
	if err := g.openOnlineScreen(); err != nil {
		t.Fatal(err)
	}
	g.activateGadget("SERVER")
	p := g.online.panel
	if g.online.view != onlineServerEntry || p.TextOf("TEXT") != onlineHostPrompt || p.TextOf("ADDRESS") != defaultOnlineServer || !p.EditorCaptured() {
		t.Fatalf("server popup: %q %q", p.TextOf("TEXT"), p.TextOf("ADDRESS"))
	}
	// Escape empties the field, which saves nothing.
	p.SetText("ADDRESS", "")
	g.activateGadget("ADDRESS")
	if g.online.view != onlineServerEntry || g.onlineServer != "" {
		t.Fatal("an emptied server field was saved")
	}
	// A bad address keeps the popup open with the reason.
	p.SetText("ADDRESS", "ws://192.0.2.1:8080/relay")
	g.activateGadget("ADDRESS")
	if g.online.view != onlineServerEntry || !strings.Contains(p.TextOf("STATUS"), "not a server address") || g.onlineServer != "" {
		t.Fatalf("bad server: %q", p.TextOf("STATUS"))
	}
	p.SetText("ADDRESS", "relay.example.test")
	g.activateGadget("OK")
	if g.online.view != onlineChooser || g.onlineServer != "relay.example.test" || !strings.Contains(g.online.panel.TextOf("SERVERNAME"), "relay.example.test") {
		t.Fatalf("server %q, chooser %q", g.onlineServer, g.online.panel.TextOf("SERVERNAME"))
	}
}

func TestOnlineServerAddress(t *testing.T) {
	for typed, want := range map[string]string{
		"":                               "wss://relay.nanolathe.gg/relay",
		"  relay.example.test ":          "wss://relay.example.test/relay",
		"relay.example.test:39032":       "relay.example.test:39032",
		"wss://relay.example.test/relay": "wss://relay.example.test/relay",
		"[::1]":                          "wss://[::1]/relay",
	} {
		got, options, err := onlineServerAddress(typed)
		if err != nil || got != want || options.InsecureLoopback || options.TLSConfig != nil {
			t.Fatalf("%q: %q %+v %v, want %q", typed, got, options, err, want)
		}
	}
	if got, options, err := onlineServerAddress("ws://127.0.0.1:8080/relay"); err != nil || got != "ws://127.0.0.1:8080/relay" || !options.InsecureLoopback {
		t.Fatalf("loopback plaintext: %q %+v %v", got, options, err)
	}
	for _, typed := range []string{"ws://relay.example.test/relay", "ws://192.0.2.1:8080/relay", "https://relay.example.test/relay", "wss://relay.example.test/", "relay.example.test:0", "wss://user@relay.example.test/relay", "relay example"} {
		if _, _, err := onlineServerAddress(typed); err == nil {
			t.Fatalf("admitted %q", typed)
		} else if text := onlineRefusalText(err); !strings.Contains(text, "not a server address") {
			t.Fatalf("%q refusal: %q", typed, text)
		}
	}
}

func TestOnlineRefusalTextIsPlain(t *testing.T) {
	relayRefusal := func(path, expected string) error {
		return errors.New("nanolathe: hosted relay rejected: logical path " + path + ", providers searched [hosted transport], expected " + expected)
	}
	for _, c := range []struct {
		err  error
		want string
	}{
		{relayRefusal("room", "an existing invitation"), "No game has that code"},
		{relayRefusal("room", "an unoccupied seat"), "full or has already started"},
		{relayRefusal("room", "an open room"), "That game has closed"},
		{relayRefusal("room host", "the host to stay until the match starts"), "The host left"},
		{relayRefusal("room wait", "a started match within 30 minutes"), "30 minutes"},
		{relayRefusal("room capacity", "space for another room"), "server is full"},
		{relayRefusal("hello protocol", "identical values from every seat"), "(protocol)"},
		{relayRefusal("handshake", "hosted protocol version 4; this client sent version 3"), "different version of online play"},
		{localMultiplayerError("mounted content", "the base game or an installed mod"), "base game or an installed mod"},
		{&content.RestrictionsError{Issues: []content.RestrictionIssue{{Unit: "armcom", Reason: content.RestrictionRemovesCommander}}}, "armcom"},
		{errors.New("nanolathe: relay stream failed: logical path hosted dial, providers searched [relay transport], expected a complete transport message: dial tcp: lookup relay.example.test: no such host"), "Could not reach the server: dial tcp: lookup relay.example.test: no such host"},
	} {
		if got := onlineRefusalText(c.err); !strings.Contains(got, c.want) || strings.Contains(got, "logical path") {
			t.Fatalf("%v: %q, want %q in plain words", c.err, got, c.want)
		}
	}
}

func TestOnlineJoinChecksTheRoomBeforeJoining(t *testing.T) {
	fake := &fakeOnlineRelay{}
	useOnlineTestRelay(t, fake)
	useOnlineSessionSeams(t, 0)
	g := onlineTestShell(t)
	g.onlineServer = "relay.example.test"
	if err := g.openOnlineScreen(); err != nil {
		t.Fatal(err)
	}
	g.activateGadget("JOIN")
	p := g.online.panel
	// Escape clears the field and fires it empty, which joins nothing.
	g.activateGadget("ADDRESS")
	if g.online.view != onlineCodeEntry || g.online.status != "" || g.online.job != nil {
		t.Fatalf("an emptied field acted: %q", g.online.status)
	}
	// A malformed code never reaches the server, and the popup stays open.
	p.SetText("ADDRESS", "cfh")
	g.activateGadget("OK")
	if len(fake.describes) != 0 || g.online.view != onlineCodeEntry || !strings.Contains(p.TextOf("STATUS"), "six letters and digits") {
		t.Fatalf("short code: %v %q", fake.describes, p.TextOf("STATUS"))
	}
	p.SetText("ADDRESS", "cfh-j0k")
	g.activateGadget("OK")
	if len(fake.describes) != 0 || !strings.Contains(p.TextOf("STATUS"), `never use '0'`) {
		t.Fatalf("misread code: %v %q", fake.describes, p.TextOf("STATUS"))
	}
	room := onlineTestBase(t, "Test Map", session.MatchMod{ID: "absentmod", Version: "1.0", Archive: sha256.Sum256([]byte("absentmod"))}, content.Mutators{})
	encoded, err := session.EncodeMatchConfig(room)
	if err != nil {
		t.Fatal(err)
	}
	fake.describe = func(string) (relay.HostedRoomDescription, error) {
		return relay.HostedRoomDescription{Config: encoded, Size: relay.HostedMaxSeats}, nil
	}
	p.SetText("ADDRESS", "cfh-234")
	g.activateGadget("ADDRESS") // Enter in the field joins
	if g.online.phase != onlineDescribing || !greyed(p, "OK") {
		t.Fatal("the join did not wait for the room's description")
	}
	awaitOnline(t, g)
	if len(fake.describes) != 1 || fake.describes[0] != "wss://relay.example.test/relay CFH234" {
		t.Fatalf("describe calls %v", fake.describes)
	}
	// The room's mod is fixed: a missing one is named, nothing is joined,
	// and the popup stays open to try another code.
	if len(fake.opens) != 0 || pendingContentReload != nil || g.online.view != onlineCodeEntry || !strings.Contains(p.TextOf("STATUS"), "absentmod 1.0") || greyed(p, "OK") {
		t.Fatalf("missing mod: opens %d, reload %v, status %q", len(fake.opens), pendingContentReload, p.TextOf("STATUS"))
	}
	// A relay refusal is said in plain words, in the same popup.
	fake.describe = func(string) (relay.HostedRoomDescription, error) {
		return relay.HostedRoomDescription{}, errors.New("nanolathe: hosted relay rejected: logical path room, providers searched [hosted transport], expected an existing invitation")
	}
	g.activateGadget("OK")
	awaitOnline(t, g)
	if g.online.view != onlineCodeEntry || !strings.Contains(p.TextOf("STATUS"), "No game has that code") {
		t.Fatalf("unknown room: %q", p.TextOf("STATUS"))
	}
}

func TestOnlineCreateRefusalsAndBusyState(t *testing.T) {
	fake := &fakeOnlineRelay{}
	useOnlineTestRelay(t, fake)
	g := onlineTestShell(t)
	if err := g.openOnlineScreen(); err != nil {
		t.Fatal(err)
	}
	p := g.online.panel
	g.onlineServer = "ws://192.0.2.1:8080/relay"
	g.activateGadget("CREATE")
	if g.online.job != nil || !strings.Contains(p.TextOf("STATUS"), "not a server address") {
		t.Fatalf("bad server: %q", p.TextOf("STATUS"))
	}
	g.onlineServer = ""
	g.cs.manualRoots = true
	g.activateGadget("CREATE")
	if g.online.job != nil || !strings.Contains(g.online.status, "installed mod") {
		t.Fatalf("manual roots: %q", g.online.status)
	}
	g.cs.manualRoots = false
	g.cs.mod = &modlibrary.Mod{Metadata: modlibrary.Metadata{ID: "folder", Version: "1"}}
	g.activateGadget("CREATE")
	if g.online.job != nil || !strings.Contains(g.online.status, "installed from its archive") {
		t.Fatalf("folder mod: %q", g.online.status)
	}
	g.cs.mod = nil
	g.activateGadget("CREATE")
	if g.online.phase != onlineConnecting || !greyed(p, "CREATE") || !greyed(p, "JOIN") || !strings.Contains(p.TextOf("STATUS"), "Opening a room") {
		t.Fatal("create did not say it is connecting")
	}
	// Cancel stops the opening room and keeps the chooser.
	g.activateGadget("PREVMENU")
	if g.online == nil || g.online.job != nil || g.online.phase != onlineIdle || greyed(p, "CREATE") {
		t.Fatal("Cancel did not stop the opening room")
	}
	onlineWork.Wait()
	if len(fake.opens) != 0 {
		t.Fatal("the empty test content opened a room")
	}
}

// A room's mod that is installed but not mounted is mounted through the
// ordinary content reload, which carries the join and the player's own
// mutators; a different copy of the mod is refused by name.
func TestOnlineJoinRequestsTheRoomsMod(t *testing.T) {
	fake := &fakeOnlineRelay{}
	useOnlineTestRelay(t, fake)
	useOnlineSessionSeams(t, 0)
	g := onlineTestShell(t)
	t.Cleanup(func() { pendingContentReload = nil })
	var archive bytes.Buffer
	z := zip.NewWriter(&archive)
	w, err := z.Create(modlibrary.MetadataFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(`{"schema":1,"id":"testmod","name":"Test Mod","version":"1"}`)); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "testmod.zip")
	if err := os.WriteFile(path, archive.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	lib, err := openModLibrary()
	if err != nil {
		t.Fatal(err)
	}
	installed, err := lib.InstallArchive(path, modlibrary.InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := matchModOf(&installed)
	if want.Archive != sha256.Sum256(archive.Bytes()) {
		t.Fatal("the mod's identity is not its archive digest")
	}
	if err := g.openOnlineScreen(); err != nil {
		t.Fatal(err)
	}
	g.activateGadget("JOIN")
	g.opts.Mutators = content.Mutators{Damage: content.Factor{Num: 2, Den: 1}}
	encode := func(mod session.MatchMod) []byte {
		encoded, err := session.EncodeMatchConfig(onlineTestBase(t, "Test Map", mod, content.Mutators{Sight: content.Factor{Num: 2, Den: 1}}))
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	other := want
	other.Archive[0] ^= 1
	g.adoptOnlineRoom(onlineRoom{address: "wss://relay.example.test/relay", code: "CFH234", config: encode(other)}, false)
	if pendingContentReload != nil || !strings.Contains(g.online.status, "different copy") {
		t.Fatalf("another copy: reload %v, status %q", pendingContentReload, g.online.status)
	}
	room := onlineRoom{address: "wss://relay.example.test/relay", code: "CFH234", config: encode(want)}
	g.adoptOnlineRoom(room, false)
	r := pendingContentReload
	if r == nil || r.selector != "testmod@1" || r.mod.ID != "testmod" || r.online == nil || r.online.code != "CFH234" || r.mutators != g.opts.Mutators {
		t.Fatalf("reload request %+v", r)
	}
	if !strings.Contains(g.online.status, "testmod 1") || g.online.phase != onlineConnecting || len(fake.opens) != 0 {
		t.Fatalf("status %q phase %d", g.online.status, g.online.phase)
	}
	// After the reload the mod must be the room's; if it is not, the join
	// stops rather than switching again.
	pendingContentReload = nil
	g.adoptOnlineRoom(room, true)
	if pendingContentReload != nil || !strings.Contains(g.online.status, "could not be selected") {
		t.Fatalf("remounted mismatch: %q", g.online.status)
	}
}

// A browser hands a pasted code to the engine as a paste key's token
// (web/clipboard.js): Ctrl+V as pressed, and Insert for Command+V and its
// menus. Either fills the focused code field, shown as the relay spells codes;
// a paste with no text leaves the field alone [07 §2].
func TestOnlineCodeFieldTakesAPastedCode(t *testing.T) {
	g := onlineTestShell(t)
	g.activateGadget("MULTI")
	g.activateGadget("JOIN")
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	paste := func(token input.Token) string {
		cl.Input().EnqueueToken(token)
		g.menuInput(cl)
		g.pollOnline()
		return g.online.panel.TextOf("ADDRESS")
	}
	if got := paste(input.Token{Kind: input.TokenEdit, Key: input.KeyInsert, Clipboard: input.ClipboardText{Text: "k7m-2px", Available: true}}); got != "K7M-2PX" {
		t.Fatalf("Insert paste: %q", got)
	}
	if got := paste(input.Token{Kind: input.TokenEdit, Key: input.KeyV, Ctrl: true, Clipboard: input.ClipboardText{Text: "cfh 234", Available: true}}); got != "CFH 234" {
		t.Fatalf("Ctrl+V paste: %q", got)
	}
	if got := paste(input.Token{Kind: input.TokenEdit, Key: input.KeyInsert}); got != "CFH 234" {
		t.Fatalf("a paste with no text changed the field: %q", got)
	}
	if g.online.view != onlineCodeEntry || !g.online.panel.EditorCaptured() {
		t.Fatal("pasting left the code entry")
	}
}
