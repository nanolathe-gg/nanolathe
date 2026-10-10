//go:build retail

package main

import (
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/relay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
)

func onlineTestSelection(t *testing.T) (content.Mutators, content.Restrictions) {
	t.Helper()
	mutators, err := content.ParseMutators(map[string]string{"buildSpeed": "2", "sight": "1.5"})
	if err != nil {
		t.Fatal(err)
	}
	restrictions, err := content.ParseRestrictions(map[string]int{"armpw": 5, "corak": 0})
	if err != nil {
		t.Fatal(err)
	}
	return mutators, restrictions
}

// The host's configuration, encoded, decoded and composed by the joiner on
// the same content, reports the host's identity and initial checksum; a
// different restriction set is a different configuration (§16.6).
func TestOnlineConfigurationAdoptionRetail(t *testing.T) {
	cat, fs := retailcat.Shared(t)
	cs := &contentSet{fs: fs, unmappedMount: fs, profile: "retail", limits: content.RetailLimits()}
	mutators, restrictions := onlineTestSelection(t)
	schema, err := session.OnlineMapSchema(cs.fs, cat, "ashap plateau", 2)
	if err != nil {
		t.Fatal(err)
	}
	records, err := onlineMatchRestrictions(restrictions, cat)
	if err != nil || len(records) == 0 {
		t.Fatalf("restriction records %v: %v", records, err)
	}
	spec := onlineMatchSpec{mapName: "ashap plateau", simSeed: 29, crtSeed: 31, mutators: mutators, restrictions: records, restrictionSet: restrictions}
	hostConfig, err := onlineMatchConfig(spec, cs, schema)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := session.EncodeMatchConfig(hostConfig)
	if err != nil {
		t.Fatal(err)
	}
	joinConfig, err := session.DecodeMatchConfig(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if joinConfig.Digest() != hostConfig.Digest() || joinConfig.Request().Mutators != mutators {
		t.Fatal("the decoded configuration is not the host's")
	}
	if adopted, err := session.RestrictionsFromMatch(cat, joinConfig.Request().UnitRestrictions); err != nil || !adopted.Equal(restrictions) {
		t.Fatalf("adopted restrictions %v: %v", adopted, err)
	}
	host, hostInputs, hostID, err := composeOnlineMatch(cs, cat, hostConfig, 0)
	if err != nil {
		t.Fatal(err)
	}
	joiner, joinInputs, joinID, err := composeOnlineMatch(cs, cat, joinConfig, 1)
	if err != nil {
		t.Fatal(err)
	}
	if hostID != joinID || host.UnitStateChecksum() != joiner.UnitStateChecksum() {
		t.Fatal("the joiner's identity or initial checksum differs from the host's")
	}
	// The rehearsal reuses what each seat composed from.
	if hostInputs == nil || hostInputs.Digest() != joinInputs.Digest() {
		t.Fatal("the seats froze different inputs for the rehearsal")
	}
	hostRehearsal, err := rehearsalDigest(hostInputs, hostConfig)
	if err != nil {
		t.Fatal(err)
	}
	if joinRehearsal, err := rehearsalDigest(joinInputs, joinConfig); err != nil || joinRehearsal != hostRehearsal {
		t.Fatalf("the seats' rehearsals disagree: %v", err)
	}
	if host.LocalOwner != 0 || joiner.LocalOwner != 1 || host.IsPendingBattle() || joiner.IsPendingBattle() {
		t.Fatal("seats or prepared entry wrong")
	}
	other, err := content.ParseRestrictions(map[string]int{"armpw": 6})
	if err != nil {
		t.Fatal(err)
	}
	spec.restrictionSet = other
	if spec.restrictions, err = onlineMatchRestrictions(other, cat); err != nil {
		t.Fatal(err)
	}
	differ, err := onlineMatchConfig(spec, cs, schema)
	if err != nil {
		t.Fatal(err)
	}
	if differ.Digest() == hostConfig.Digest() {
		t.Fatal("different restrictions share a configuration identity")
	}
}

// onlineRetailShell is a menu shell on the retail install, without saved
// settings, with an online server field pointing at address.
func onlineRetailShell(t *testing.T, cs *contentSet, opts Options) *gameShell {
	t.Helper()
	shell, err := newGameShell(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	shell.cam = &camera.Camera{ViewW: retailScreenW, ViewH: retailScreenH, MapW: retailScreenW, MapH: retailScreenH}
	return shell
}

func awaitOnlineShells(t *testing.T, what string, done func() bool, shells ...*gameShell) {
	t.Helper()
	end := time.Now().Add(60 * time.Second)
	for !done() {
		for _, g := range shells {
			if g.online != nil && g.online.phase == onlineIdle && g.online.status != "" && g.online.job == nil {
				t.Fatalf("waiting for %s: %q", what, g.online.status)
			}
		}
		if time.Now().After(end) {
			for _, g := range shells {
				if g.online != nil {
					t.Logf("phase %d status %q", g.online.phase, g.online.status)
				}
			}
			t.Fatalf("never reached %s", what)
		}
		for _, g := range shells {
			g.pollOnline()
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// onlineRetailRoom starts a match through a real local WebSocket relay: the
// host creates the room with mutators and restrictions, the guests join with
// their own different selection, each player picks its team, prepare (when
// given) changes the room through the lobbies, every seat adopts the host's
// latest settings and readies with its own rehearsal, and the host starts. It
// returns the shells with their battles entered. survival switches the room
// to Survival first.
func onlineRetailRoom(t *testing.T, players int, survival bool, teams []uint8, prepare ...func(shells []*gameShell)) []*gameShell {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	server, err := relay.ListenHostedWebSocket("127.0.0.1:0", relay.HostedConfig{InsecureLoopback: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	address := "ws://" + server.Addr() + "/relay"
	opts := Options{Root: testsupport.RetailRoot(t), Seed: -1}
	cs, err := openContent(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	opts.Root, opts.Roots = cs.root, cs.roots
	shells := make([]*gameShell, players)
	for i := range shells {
		shells[i] = onlineRetailShell(t, cs, opts)
	}
	host := shells[0]
	mapName := ""
	for _, m := range host.maps {
		if strings.EqualFold(m, "ashap plateau") {
			mapName = m
		}
	}
	if mapName == "" {
		t.Fatal("Ashap Plateau is not in the map census")
	}
	mutators, restrictions := onlineTestSelection(t)
	host.setup.MapName = mapName
	host.opts.Mutators, host.opts.Restrictions = mutators, restrictions
	if err := host.openOnlineScreen(); err != nil {
		t.Fatal(err)
	}
	host.onlineServer = address
	host.activateGadget("CREATE")
	awaitOnlineShells(t, "the host's lobby", func() bool { return host.online.phase == onlineInLobby }, host)
	if survival {
		host.activateGadget(lobbyGameType)
		if !host.online.room.settings.survival {
			t.Fatalf("the room did not switch to Survival: %q", host.online.room.notice)
		}
	}
	code := host.online.room.lobby.Code()
	for i, guest := range shells[1:] {
		// A guest's own selection never reaches the room's battle.
		guest.setup.MapName = guest.maps[0]
		guest.opts.Mutators = content.Mutators{Health: content.Factor{Num: 3, Den: 1}}
		if err := guest.openOnlineScreen(); err != nil {
			t.Fatal(err)
		}
		guest.onlineServer = address
		guest.activateGadget("JOIN")
		guest.online.panel.SetText("ADDRESS", strings.ToLower(code[:3]+" "+code[3:]))
		guest.activateGadget("OK")
		joined := i + 2
		awaitOnlineShells(t, "a guest's lobby", func() bool {
			return guest.online != nil && guest.online.phase == onlineInLobby && len(host.online.room.presentSeats()) == joined
		}, shells...)
	}
	for i, g := range shells {
		r := &g.online.room
		awaitOnlineShells(t, "the room's configuration", func() bool { return r.base.Digest() == host.online.room.base.Digest() }, shells...)
		if r.baseReq.Mutators != mutators || len(r.baseReq.UnitRestrictions) == 0 || r.settings.survival != survival {
			t.Fatalf("player %d did not adopt the room's configuration", i+1)
		}
		for range teams[i] {
			before := r.state.Seats[r.lobby.Seat()].Team
			g.activateGadget("Allies" + strconv.Itoa(i))
			awaitOnlineShells(t, "a team change", func() bool { return r.state.Seats[r.lobby.Seat()].Team != before }, shells...)
		}
	}
	for i, g := range shells {
		r := &g.online.room
		awaitOnlineShells(t, "the teams", func() bool { return r.state.Seats[i].Team == teams[i] }, shells...)
	}
	// Each arrival took its own colour; the last player steps theirs on.
	last := shells[len(shells)-1]
	lastSeat := last.online.room.lobby.Seat()
	before := last.online.room.state.Seats[lastSeat].Color
	last.activateGadget("Color" + strconv.Itoa(int(lastSeat)))
	awaitOnlineShells(t, "a colour change", func() bool { return host.online.room.state.Seats[lastSeat].Color != before }, shells...)
	held := map[uint8]bool{}
	for _, i := range host.online.room.presentSeats() {
		c := host.online.room.state.Seats[i].Color
		if held[c] {
			t.Fatalf("two players hold colour %d", c)
		}
		held[c] = true
	}
	for _, f := range prepare {
		f(shells)
	}
	for _, g := range shells {
		r := &g.online.room
		awaitOnlineShells(t, "the room's latest settings", func() bool { return r.base.Digest() == host.online.room.base.Digest() }, shells...)
	}
	readying := time.Now()
	for _, g := range shells {
		g.activateGadget(lobbyReady)
	}
	awaitOnlineShells(t, "every seat ready", func() bool { return host.onlineCanStart() }, shells...)
	// Each seat composes, prepares and rehearses on its own goroutine;
	// §16.7 bounds the rehearsal at about a second.
	t.Logf("%d seats composed, rehearsed and readied in %v", players, time.Since(readying).Round(time.Millisecond))
	for i, g := range shells {
		if g.online.room.prepared.identity != host.online.room.prepared.identity || g.online.room.prepared.rehearsal != host.online.room.prepared.rehearsal {
			t.Fatalf("player %d's digests differ from the host's", i+1)
		}
	}
	host.activateGadget("Start")
	awaitOnlineShells(t, "every battle", func() bool {
		for _, g := range shells {
			if g.battle == nil {
				return false
			}
		}
		return true
	}, shells...)
	for i, g := range shells {
		t.Cleanup(func() { g.teardownBattle(nil) })
		if g.online != nil || g.battle.sess.LocalOwner != uint8(i) {
			t.Fatalf("player %d entered at slot %d", i+1, g.battle.sess.LocalOwner)
		}
	}
	return shells
}

// pumpOnlineRetail runs every battle to ticks, the relay comparing their
// checksums; cl, when given, presents the first.
func pumpOnlineRetail(t *testing.T, shells []*gameShell, ticks uint32, cl *client.Client) {
	t.Helper()
	end := time.Now().Add(60 * time.Second)
	for {
		done := true
		for i, g := range shells {
			var presenter *client.Client
			if i == 0 {
				presenter = cl
			}
			g.battle.pumpLocalMultiplayer(presenter)
			if mp := g.battle.multiplayer; mp == nil || mp.failure != nil {
				t.Fatalf("multiplayer stopped: %v", mp.failure)
			}
			done = done && g.battle.sess.Clock.GlobalTick >= ticks
		}
		if done {
			return
		}
		if time.Now().After(end) {
			t.Fatalf("ticks %d of %d", shells[0].battle.sess.Clock.GlobalTick, ticks)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func onlineRetailMatch(t *testing.T, players int, survival bool, teams []uint8, ticks uint32) {
	t.Helper()
	pumpOnlineRetail(t, onlineRetailRoom(t, players, survival, teams), ticks, nil)
}

// TestOnlineOverlayCapture renders a two-player online battle with the
// network overlay. A review diagnostic, run only when
// NANOLATHE_ONLINE_CAPTURE names an output directory.
func TestOnlineOverlayCapture(t *testing.T) {
	out := os.Getenv("NANOLATHE_ONLINE_CAPTURE")
	if out == "" {
		t.Skip("NANOLATHE_ONLINE_CAPTURE is unset")
	}
	shells := onlineRetailRoom(t, 2, false, []uint8{0, 0})
	host := shells[0]
	cl, err := client.New(client.Options{Buffer: host.battle.sess.Snapshot, Width: retailScreenW, Height: retailScreenH})
	if err != nil {
		t.Fatal(err)
	}
	installBattleClient(cl, host.battle)
	cl.PrepareBattlePresentation()
	host.netVisible = true
	// An order from each seat, so the overlay has latencies to show.
	pumpOnlineRetail(t, shells, 60, cl)
	for _, g := range shells {
		for _, u := range g.battle.sess.Units.IterSliced() {
			if u.Alive && u.Owner == g.battle.sess.LocalOwner {
				_, _ = g.battle.multiplayer.driver.Submit(session.HumanCommand{Kind: session.HumanStop, Stop: session.HumanStopCommand{Handles: []pool.Handle{u.Handle}}})
				break
			}
		}
	}
	pumpOnlineRetail(t, shells, 150, cl)
	f, err := os.Create(filepath.Join(out, os.Getenv("NANOLATHE_ONLINE_CAPTURE_PREFIX")+"overlay.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, cl.ComposeFrame()); err != nil {
		t.Fatal(err)
	}
}

// One headless two-client match through a real local relay, started from the
// lobby with a mutator set and unit restrictions (§16.6.3, §16.7).
func TestOnlineLobbyTwoClientsRetail(t *testing.T) {
	onlineRetailMatch(t, 2, false, []uint8{0, 0}, 95)
}

// Three players on teams of two and one, and two Survival survivors, through
// a real local relay to a few hundred ticks (§16.6).
func TestOnlineLobbyTeamsAndSurvivalRetail(t *testing.T) {
	t.Run("2v1", func(t *testing.T) { onlineRetailMatch(t, 3, false, []uint8{1, 1, 2}, 300) })
	t.Run("survival", func(t *testing.T) { onlineRetailMatch(t, 2, true, []uint8{0, 0}, 300) })
}

// Final configurations from lobby seats: 2, 4 and 10 skirmish players with
// teams, and 3 Survival survivors, compose on the retail content and agree
// on every slot (§16.6).
func TestOnlineRoomCompositionRetail(t *testing.T) {
	cat, fs := retailcat.Shared(t)
	cs := &contentSet{fs: fs, unmappedMount: fs, profile: "retail", limits: content.RetailLimits()}
	mutators, restrictions := onlineTestSelection(t)
	records, err := onlineMatchRestrictions(restrictions, cat)
	if err != nil {
		t.Fatal(err)
	}
	frozen := onlineCreationFrozen(cs, [2]uint32{29, 31}, mutators, restrictions, records)
	tenMap := ""
	for _, name := range []string{"ashap plateau", "the pass", "seven islands", "comet catcher"} {
		if n, err := onlineMapCapacity(cat, name); err == nil && n >= 10 {
			tenMap = name
			break
		}
	}
	for _, c := range []struct {
		name     string
		mapName  string
		survival bool
		teams    []uint8
	}{
		{"2", "ashap plateau", false, []uint8{0, 0}},
		{"4", "ashap plateau", false, []uint8{1, 1, 2, 2}},
		{"10", tenMap, false, []uint8{1, 2, 3, 4, 5, 1, 2, 3, 4, 5}},
		{"survival-3", "ashap plateau", true, []uint8{0, 0, 0}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.mapName == "" {
				t.Skip("no retail map seats ten players")
			}
			settings := onlineSettings{survival: c.survival, mapName: c.mapName, location: 1, commanderDeath: 1}
			seats := make([]session.OnlineSeat, len(c.teams))
			for i, team := range c.teams {
				seats[i] = session.OnlineSeat{Team: team, Side: uint8(i % 2), Color: uint8(i * 3 % relay.HostedColors)}
			}
			config, err := onlineConfig(cs, cat, settings, seats, frozen)
			if err != nil {
				t.Fatal(err)
			}
			for i, seat := range seats {
				if got := config.Request().Seats[i].Color; got != seat.Color {
					t.Fatalf("player %d's colour %d, chose %d", i+1, got, seat.Color)
				}
			}
			var identity [32]byte
			for slot := range seats {
				sess, _, id, err := composeOnlineMatch(cs, cat, config, uint8(slot))
				if err != nil {
					t.Fatal(err)
				}
				if slot == 0 {
					identity = onlineIdentityDigest(id)
				} else if onlineIdentityDigest(id) != identity || sess.LocalOwner != uint8(slot) {
					t.Fatalf("slot %d differs", slot)
				}
			}
		})
	}
}

// TestOnlineScreenCapture renders the online screen, lobbies and the network
// overlay to PNGs for review. A review diagnostic: it runs only when
// NANOLATHE_ONLINE_CAPTURE names an output directory.
func TestOnlineScreenCapture(t *testing.T) {
	out := os.Getenv("NANOLATHE_ONLINE_CAPTURE")
	if out == "" {
		t.Skip("NANOLATHE_ONLINE_CAPTURE is unset")
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	opts := Options{Root: testsupport.RetailRoot(t), Seed: -1}
	cs, err := openContent(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	opts.Root, opts.Roots = cs.root, cs.roots
	shell := onlineRetailShell(t, cs, opts)
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: retailScreenW, Height: retailScreenH})
	if err != nil {
		t.Fatal(err)
	}
	saved := clPtr
	clPtr = cl
	defer func() { clPtr = saved }()
	if shell.assets != nil && shell.assets.pal != nil {
		cl.SetPalette(shell.assets.pal)
	}
	if shell.font != nil {
		cl.SetFNT(shell.font)
	}
	cl.SetUIStage(gameShellUIStage{shell: shell})
	prefix := os.Getenv("NANOLATHE_ONLINE_CAPTURE_PREFIX")
	capture := func(name string) {
		img := cl.ComposeFrame()
		f, err := os.Create(filepath.Join(out, prefix+name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
	}
	shell.openMenu(modeMenuMain)
	shell.opts.Mutators, _ = onlineTestSelection(t)
	if err := shell.openOnlineScreen(); err != nil {
		t.Fatal(err)
	}
	capture("chooser")
	shell.activateGadget("JOIN")
	capture("code")
	shell.online.panel.SetText("ADDRESS", "k7m-2qx")
	shell.syncOnlineCodeField()
	shell.online.status = onlineRefusalText(errors.New("nanolathe: hosted relay rejected: logical path room, providers searched [hosted transport], expected an existing invitation"))
	shell.refreshOnlinePanel()
	capture("code-error")
	shell.activateGadget("PREV")
	shell.activateGadget("SERVER")
	capture("server")
	shell.activateGadget("PREV")
	shell.online.status = "Opening a room on relay.nanolathe.gg..."
	shell.online.phase = onlineConnecting
	shell.refreshOnlinePanel()
	capture("chooser-connecting")
	shell.online.phase, shell.online.status = onlineIdle, ""
	// A four-player skirmish lobby: the host, two teams, both sides.
	fake := newFakeOnlineLobby(1, 0, 1, 2, 3)
	fake.code = "K7M2QX"
	copy(fake.state.Seats[:], []relay.HostedSeatState{{Present: true, Team: 1, Ready: true}, {Present: true, Team: 1, Side: 1, Color: 3}, {Present: true, Team: 2, Side: 1, Ready: true, Color: 1}, {Present: true, Team: 2, Color: 5}})
	cat, err := cs.compileCatalog(nil)
	if err != nil {
		t.Fatal(err)
	}
	base, err := onlineConfig(cs, cat, onlineSettingsFromSetup(shell.setup), onlinePlaceholderSeats(nil), onlineCreationFrozen(cs, [2]uint32{1, 2}, shell.opts.Mutators, content.Restrictions{}, nil))
	if err != nil {
		t.Fatal(err)
	}
	shell.openOnlineLobby(fake, cat, base, "wss://relay.nanolathe.gg/relay")
	shell.pollOnline()
	capture("lobby-teams")
	r := &shell.online.room
	r.prepared, r.ready = &onlinePrepared{key: r.key()}, true
	fake.state.Seats[1].Ready, fake.state.Seats[3].Ready = true, true
	shell.pollOnline()
	capture("lobby-ready")
	fake.state.Mismatch = true
	shell.pollOnline()
	capture("lobby-mismatch")
	// The host's Survival lobby.
	shell.leaveOnlineLobby("")
	hostLobby := newFakeOnlineLobby(0, 0, 1, 2)
	hostLobby.code = "K7M2QX"
	shell.openOnlineLobby(hostLobby, cat, base, "wss://relay.nanolathe.gg/relay")
	shell.online.room.settings.survival, shell.online.room.settings.pace = true, 1
	shell.pollOnline()
	shell.refreshOnlineLobby()
	capture("lobby-survival")
	shell.leaveOnlineLobby("")
	// The host's computers: a Modern AI on team 1 and a Classic AI on Hard
	// on team 2 beside two players, with the Add computer row after them.
	computerLobby := newFakeOnlineLobby(0, 0, 1)
	computerLobby.code = "K7M2QX"
	computerLobby.state.Seats[0].Team, computerLobby.state.Seats[1].Team, computerLobby.state.Seats[1].Side = 1, 2, 1
	shell.openOnlineLobby(computerLobby, cat, base, "wss://relay.nanolathe.gg/relay")
	shell.pollOnline()
	shell.online.room.capacity = 4
	for _, name := range []string{"Player2", "Allies2", "Player3", "Player3", "Allies3", "Allies3", "Energy3", "Side3"} {
		shell.activateGadget(name)
	}
	if r := &shell.online.room; len(r.settings.computers) != 2 || r.notice != "" {
		t.Fatalf("capture computers %+v: %q", r.settings.computers, r.notice)
	}
	computerLobby.state.Seats[1].Ready = true
	shell.pollOnline()
	shell.refreshOnlineLobby()
	capture("lobby-computers-host")
	// The hover help of the Classic AI's name and of its difficulty.
	cl.Input().Mouse.SetPosition(100, 148)
	capture("lobby-computers-host-help-name")
	cl.Input().Mouse.SetPosition(358, 148)
	capture("lobby-computers-host-help-difficulty")
	cl.Input().Mouse.SetPosition(0, 0)
	// The same room as the guest sees it.
	computerBase := shell.online.room.base
	shell.leaveOnlineLobby("")
	guestLobby := newFakeOnlineLobby(1, 0, 1)
	guestLobby.code = "K7M2QX"
	guestLobby.state.Seats = computerLobby.state.Seats
	guestLobby.state.Seats[0].Ready = true
	shell.openOnlineLobby(guestLobby, cat, computerBase, "wss://relay.nanolathe.gg/relay")
	shell.pollOnline()
	capture("lobby-computers-guest")
	shell.leaveOnlineLobby("")
	// A full room: two players and eight computers on a ten-player map.
	fullLobby := newFakeOnlineLobby(0, 0, 1)
	fullLobby.code = "K7M2QX"
	shell.openOnlineLobby(fullLobby, cat, base, "wss://relay.nanolathe.gg/relay")
	shell.pollOnline()
	shell.online.room.capacity = 10
	for row := 2; row < 10; row++ {
		shell.activateGadget("Player" + strconv.Itoa(row))
		if row%2 == 1 {
			shell.activateGadget("Player" + strconv.Itoa(row))
		}
	}
	if r := &shell.online.room; len(r.settings.computers) != 8 || r.notice != "" {
		t.Fatalf("full room computers %d: %q", len(r.settings.computers), r.notice)
	}
	capture("lobby-computers-full")
	shell.leaveOnlineLobby("")
	// Survival: one computer survivor beside two players.
	survivalLobby := newFakeOnlineLobby(0, 0, 1)
	survivalLobby.code = "K7M2QX"
	shell.openOnlineLobby(survivalLobby, cat, base, "wss://relay.nanolathe.gg/relay")
	shell.pollOnline()
	shell.activateGadget(lobbyGameType)
	shell.activateGadget("Player2")
	if r := &shell.online.room; !r.settings.survival || len(r.settings.computers) != 1 || r.notice != "" {
		t.Fatalf("Survival computers %+v: %q", r.settings.computers, r.notice)
	}
	capture("lobby-computers-survival")
	shell.leaveOnlineLobby("")
}
