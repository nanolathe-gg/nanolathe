package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func awaitLobby(t *testing.T, l *HostedLobby, what string, ok func(HostedLobbyState, error) bool) (HostedLobbyState, error) {
	t.Helper()
	end := time.Now().Add(3 * time.Second)
	for {
		state, err := l.State()
		if ok(state, err) {
			return state, err
		}
		if time.Now().After(end) {
			t.Fatalf("lobby never reached %s: %+v %v", what, state, err)
		}
		time.Sleep(time.Millisecond)
	}
}

func openLobbyTest(t *testing.T, address, room string, seat uint8, config []byte, options HostedDialOptions) *HostedLobby {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	size := 0
	if room == "" {
		size = 2
	}
	l, err := OpenHostedLobby(ctx, address, room, localTestHello(seat), config, size, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

// Existing transport tests have no local presentation opening to play.
func openingBattleTest(t *testing.T, l *HostedLobby) *LocalClient {
	t.Helper()
	c := l.Battle()
	if c != nil {
		if err := c.OpeningReady(); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func describeTest(address, room string, options HostedDialOptions) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	d, err := DescribeHostedRoom(ctx, address, room, options)
	return d.Config, err
}

func present(both bool) func(HostedLobbyState, error) bool {
	return func(s HostedLobbyState, err error) bool {
		return err == nil && s.Seats[0].Present && s.Seats[1].Present == both
	}
}

// The lobby of DESIGN_MULTIPLAYER §16.6.1 over both hosted transports: the
// joiner learns the configuration first, readiness gates the host's start,
// and the first grant reaches the battle client after Started.
func TestHostedLobbyDescribeReadyAndStart(t *testing.T) {
	for _, websocket := range []bool{false, true} {
		var s *HostedServer
		address := ""
		if websocket {
			s = listenWebSocketTest(t, HostedConfig{InsecureLoopback: true}, false, hostedDefaultTimeouts)
			address = "ws://" + s.Addr() + "/relay"
		} else {
			s = listenHostedTest(t, HostedConfig{InsecureLoopback: true}, hostedDefaultTimeouts)
			address = s.Addr()
		}
		options := HostedDialOptions{InsecureLoopback: true}
		host := openLobbyTest(t, address, "", 0, []byte("configuration"), options)
		awaitLobby(t, host, "the host's seat", present(false))
		if len(host.Code()) != hostedCodeLength || host.Seat() != 0 {
			t.Fatalf("room %q seat %d", host.Code(), host.Seat())
		}
		if config, err := describeTest(address, host.Code(), options); err != nil || string(config) != "configuration" {
			t.Fatalf("describe: %q %v", config, err)
		}
		joiner := openLobbyTest(t, address, host.Code(), 1, nil, options)
		awaitLobby(t, host, "both seats", present(true))
		awaitLobby(t, joiner, "both seats", present(true))
		if _, err := describeTest(address, host.Code(), options); err == nil || !strings.Contains(err.Error(), "unoccupied seat") {
			t.Fatalf("describe of a full room: %v", err)
		}
		if err := joiner.Start(); err == nil {
			t.Fatal("the joining seat started the match")
		}
		// A start before both seats are ready only repeats the state.
		rehearsal := [32]byte{7}
		if err := host.SetReady(true, [32]byte{}, rehearsal); err != nil {
			t.Fatal(err)
		}
		if err := host.Start(); err != nil {
			t.Fatal(err)
		}
		awaitLobby(t, joiner, "the host ready", func(s HostedLobbyState, err error) bool { return err == nil && s.Seats[0].Ready })
		if err := joiner.SetReady(true, [32]byte{}, rehearsal); err != nil {
			t.Fatal(err)
		}
		state, _ := awaitLobby(t, host, "both ready", func(s HostedLobbyState, err error) bool { return err == nil && s.Seats[0].Ready && s.Seats[1].Ready })
		if state.Started || openingBattleTest(t, host) != nil {
			t.Fatal("an early start began the match")
		}
		if err := host.Start(); err != nil {
			t.Fatal(err)
		}
		for _, l := range []*HostedLobby{host, joiner} {
			awaitLobby(t, l, "Started", func(s HostedLobbyState, err error) bool { return err == nil && s.Started })
		}
		battles := [2]*LocalClient{openingBattleTest(t, host), openingBattleTest(t, joiner)}
		if battles[0] == nil || battles[1] == nil {
			t.Fatal("started lobby did not hand over its connection")
		}
		_ = host.Close() // the battle owns the connection now
		for _, c := range battles {
			_ = c.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		}
		if g := readLocalPair(t, battles); g.Tick != 1 {
			t.Fatalf("first grant after Started: %+v", g)
		}
		for _, c := range battles {
			_ = c.Close()
		}
		awaitHostedCapacity(t, s, 0, 0)
	}
}

func TestHostedLobbySeatsLeaveAndExpire(t *testing.T) {
	s := listenHostedTest(t, HostedConfig{InsecureLoopback: true}, hostedDefaultTimeouts)
	options := HostedDialOptions{InsecureLoopback: true}
	host := openLobbyTest(t, s.Addr(), "", 0, []byte{1}, options)
	first := openLobbyTest(t, s.Addr(), host.Code(), 1, nil, options)
	if err := first.SetReady(true, [32]byte{}, [32]byte{1}); err != nil {
		t.Fatal(err)
	}
	awaitLobby(t, host, "the first joiner ready", func(s HostedLobbyState, err error) bool { return err == nil && s.Seats[1].Ready })
	// A joiner leaving frees seat 2 and its readiness for another player.
	_ = first.Close()
	awaitLobby(t, host, "seat 2 free", func(s HostedLobbyState, err error) bool {
		return err == nil && !s.Seats[1].Present && !s.Seats[1].Ready
	})
	second := openLobbyTest(t, s.Addr(), host.Code(), 1, nil, options)
	awaitLobby(t, host, "the second joiner", present(true))
	// The host leaving closes the room for the joiner.
	_ = host.Close()
	if _, err := awaitLobby(t, second, "the room closed", func(_ HostedLobbyState, err error) bool { return err != nil }); !strings.Contains(err.Error(), "room host") {
		t.Fatalf("joiner after the host left: %v", err)
	}
	awaitHostedCapacity(t, s, 0, 0)

	timeouts := hostedDefaultTimeouts
	timeouts.waiting = 200 * time.Millisecond
	s = listenHostedTest(t, HostedConfig{InsecureLoopback: true}, timeouts)
	waiting := openLobbyTest(t, s.Addr(), "", 0, []byte{1}, options)
	if _, err := awaitLobby(t, waiting, "expiry", func(_ HostedLobbyState, err error) bool { return err != nil }); !strings.Contains(err.Error(), "room wait") {
		t.Fatalf("lobby expiry: %v", err)
	}
	if _, err := describeTest(s.Addr(), "CFHJKM", options); err == nil || !strings.Contains(err.Error(), "existing invitation") {
		t.Fatalf("describe of an unknown room: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := OpenHostedLobby(ctx, s.Addr(), "", localTestHello(0), nil, 2, options); err == nil {
		t.Fatal("created a lobby without a configuration")
	}
	if _, err := OpenHostedLobby(ctx, s.Addr(), "CFHJKM", localTestHello(1), []byte{1}, 0, options); err == nil {
		t.Fatal("a joiner sent a configuration")
	}
	awaitHostedCapacity(t, s, 0, 0)
}

// A command-line client joins a lobby room, reports ready at once and plays
// when the lobby's host starts; its grant reader skips the lobby messages.
func TestHostedCommandLineClientJoinsLobby(t *testing.T) {
	s := listenHostedTest(t, HostedConfig{InsecureLoopback: true}, hostedDefaultTimeouts)
	options := HostedDialOptions{InsecureLoopback: true}
	host := openLobbyTest(t, s.Addr(), "", 0, []byte{1}, options)
	joiner, _ := dialHostedTest(t, s, host.Code(), 1)
	awaitLobby(t, host, "the command-line joiner ready", func(s HostedLobbyState, err error) bool { return err == nil && s.Seats[1].Present && s.Seats[1].Ready })
	// A command-line seat runs no rehearsal, so only a zero digest matches it.
	if err := host.SetReady(true, [32]byte{}, [32]byte{}); err != nil {
		t.Fatal(err)
	}
	awaitLobby(t, host, "both ready", func(s HostedLobbyState, err error) bool { return err == nil && s.Seats[0].Ready })
	if err := host.Start(); err != nil {
		t.Fatal(err)
	}
	awaitLobby(t, host, "Started", func(s HostedLobbyState, err error) bool { return err == nil && s.Started })
	battle := openingBattleTest(t, host)
	_ = battle.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if g := readLocalPair(t, [2]*LocalClient{battle, joiner}); g.Tick != 1 {
		t.Fatalf("first grant: %+v", g)
	}
	_ = battle.Close()
}

// Two seats whose rehearsals disagree cannot start (DESIGN_MULTIPLAYER §16.7),
// and the build identity is advisory: a joiner reporting another build is
// admitted, since only the rehearsal shows whether they simulate alike.
func TestHostedLobbyRehearsalGatesStart(t *testing.T) {
	s := listenHostedTest(t, HostedConfig{InsecureLoopback: true}, hostedDefaultTimeouts)
	options := HostedDialOptions{InsecureLoopback: true}
	host := openLobbyTest(t, s.Addr(), "", 0, []byte{1}, options)
	other := localTestHello(1)
	other.Identity.Build = [32]byte{9, 9, 9}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	joiner, err := OpenHostedLobby(ctx, s.Addr(), host.Code(), other, nil, 0, options)
	if err != nil {
		t.Fatalf("a different advisory build was refused: %v", err)
	}
	t.Cleanup(func() { _ = joiner.Close() })
	if err := host.SetReady(true, [32]byte{}, [32]byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := joiner.SetReady(true, [32]byte{}, [32]byte{2}); err != nil {
		t.Fatal(err)
	}
	for _, l := range []*HostedLobby{host, joiner} {
		awaitLobby(t, l, "the mismatch", func(s HostedLobbyState, err error) bool { return err == nil && s.Mismatch })
	}
	if err := host.Start(); err != nil {
		t.Fatal(err)
	}
	// Un-readying on the same connection orders after the Start, so once
	// the joiner sees it the refused Start has been handled.
	if err := host.SetReady(false, [32]byte{}, [32]byte{}); err != nil {
		t.Fatal(err)
	}
	if state, _ := awaitLobby(t, joiner, "the host un-ready", func(s HostedLobbyState, err error) bool { return err == nil && !s.Seats[0].Ready }); state.Started {
		t.Fatal("a start sent during a mismatch began the match")
	}
	// Both re-ready alike; Start then succeeds.
	for _, l := range []*HostedLobby{host, joiner} {
		if err := l.SetReady(true, [32]byte{}, [32]byte{1}); err != nil {
			t.Fatal(err)
		}
	}
	awaitLobby(t, host, "agreement", func(s HostedLobbyState, err error) bool {
		return err == nil && s.Seats[0].Ready && s.Seats[1].Ready && !s.Mismatch
	})
	if err := host.Start(); err != nil {
		t.Fatal(err)
	}
	awaitLobby(t, joiner, "Started", func(s HostedLobbyState, err error) bool { return err == nil && s.Started })
}

func openSizedLobby(t *testing.T, address string, size int, config []byte, options HostedDialOptions) *HostedLobby {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	l, err := OpenHostedLobby(ctx, address, "", localTestHello(0), config, size, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

// readyAll readies every lobby with the same digests and waits until the
// host sees them all.
func readyAll(t *testing.T, host *HostedLobby, lobbies ...*HostedLobby) {
	t.Helper()
	for _, l := range lobbies {
		if err := l.SetReady(true, [32]byte{1}, [32]byte{2}); err != nil {
			t.Fatal(err)
		}
	}
	awaitLobby(t, host, "everyone ready", func(s HostedLobbyState, err error) bool {
		for _, l := range lobbies {
			if !s.Seats[l.Seat()].Ready {
				return false
			}
		}
		return err == nil && !s.Mismatch
	})
}

// A three-seat room (DESIGN_MULTIPLAYER §16.6.1): joiners take the lowest
// free seats; teams, sides and the host's settings reach every seat and
// clear readiness; Start numbers present seats into slots; every grant
// reaches every slot and the checksums of all three are compared.
func TestHostedLobbyThreeSeatsTeamsSidesSettingsAndSlots(t *testing.T) {
	s := listenHostedTest(t, HostedConfig{InsecureLoopback: true}, hostedDefaultTimeouts)
	options := HostedDialOptions{InsecureLoopback: true}
	host := openSizedLobby(t, s.Addr(), 4, []byte("map one"), options)
	a := openLobbyTest(t, s.Addr(), host.Code(), 1, nil, options)
	b := openLobbyTest(t, s.Addr(), host.Code(), 1, nil, options)
	if a.Seat() != 1 || b.Seat() != 2 {
		t.Fatalf("joiners took seats %d and %d", a.Seat(), b.Seat())
	}
	awaitLobby(t, b, "three seats", func(s HostedLobbyState, err error) bool {
		return err == nil && s.Size == 4 && s.Seats[0].Present && s.Seats[1].Present && s.Seats[2].Present && !s.Seats[3].Present
	})
	if string(b.Configuration()) != "map one" {
		t.Fatalf("joiner configuration: %q", b.Configuration())
	}
	readyAll(t, host, host, a, b)
	// A team or side change, or the host's settings, clear every seat.
	if err := a.SetReady(false, [32]byte{}, [32]byte{}); err != nil {
		t.Fatal(err)
	}
	if err := a.SetTeam(2); err != nil {
		t.Fatal(err)
	}
	if err := a.SetSide(1); err != nil {
		t.Fatal(err)
	}
	awaitLobby(t, b, "a's team and side", func(s HostedLobbyState, err error) bool {
		return err == nil && s.Seats[1].Team == 2 && s.Seats[1].Side == 1 && !s.Seats[0].Ready && !s.Seats[2].Ready
	})
	if err := a.SetConfiguration([]byte("not the host")); err == nil {
		t.Fatal("a joiner changed the room's settings")
	}
	readyAll(t, host, host, a, b)
	if err := host.SetConfiguration([]byte("map two")); err != nil {
		t.Fatal(err)
	}
	awaitLobby(t, a, "the new settings", func(s HostedLobbyState, err error) bool {
		return err == nil && s.ConfigVersion == 1 && !s.Seats[0].Ready && !s.Seats[1].Ready && !s.Seats[2].Ready
	})
	if string(a.Configuration()) != "map two" {
		t.Fatalf("settings: %q", a.Configuration())
	}
	if d, err := describeTest(s.Addr(), host.Code(), options); err != nil || string(d) != "map two" {
		t.Fatalf("describe after settings: %q %v", d, err)
	}
	// Differing digests are a mismatch; equal ones start the match.
	for i, l := range []*HostedLobby{host, a, b} {
		if err := l.SetReady(true, [32]byte{1}, [32]byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	awaitLobby(t, host, "the mismatch", func(s HostedLobbyState, err error) bool { return err == nil && s.Mismatch })
	readyAll(t, host, host, a, b)
	if err := host.Start(); err != nil {
		t.Fatal(err)
	}
	var battles [3]*LocalClient
	for i, l := range []*HostedLobby{host, a, b} {
		st, _ := awaitLobby(t, l, "Started", func(s HostedLobbyState, err error) bool { return err == nil && s.Started })
		if int(st.Slot) != i {
			t.Fatalf("seat %d got slot %d", l.Seat(), st.Slot)
		}
		battles[i] = openingBattleTest(t, l)
		_ = battles[i].conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	}
	// Slot 2's order is stamped with its slot and reaches every slot.
	if _, err := battles[2].Submit([]byte("order")); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for tick := uint32(1); ; tick++ {
		for i, c := range battles {
			g, err := c.ReadGrant()
			if err != nil || g.Tick != tick {
				t.Fatalf("slot %d grant %d: %+v %v", i, tick, g, err)
			}
			for _, cmd := range g.Commands {
				if cmd.Seat == 2 && string(cmd.Payload) == "order" {
					seen++
				}
			}
			check := [32]byte{}
			if tick == 30 && i == 1 {
				check[0] = 9 // slot 1 diverges at the first compared checksum
			}
			if err := c.Acknowledge(tick, check, false, false); err != nil {
				t.Fatal(err)
			}
		}
		if tick == 30 {
			break
		}
	}
	if seen != len(battles) {
		t.Fatalf("slot 2's order reached %d of %d slots", seen, len(battles))
	}
	for i, c := range battles {
		if _, err := c.ReadGrant(); err == nil || !strings.Contains(err.Error(), "tick 30 checksum") {
			t.Fatalf("slot %d after a divergence: %v", i, err)
		}
	}
	awaitHostedCapacity(t, s, 0, 0)
}

// Player colours (DESIGN_MULTIPLAYER §16.6.1): each seat takes the lowest
// colour no present seat holds; a colour another seat holds, or one asked for
// while ready, changes nothing; a free colour is taken and clears every
// seat's ready; a leaver's colour is free again; and a colour past the ten
// is a protocol error.
func TestHostedLobbyColours(t *testing.T) {
	s := listenHostedTest(t, HostedConfig{InsecureLoopback: true}, hostedDefaultTimeouts)
	options := HostedDialOptions{InsecureLoopback: true}
	host := openSizedLobby(t, s.Addr(), 4, []byte{1}, options)
	a := openLobbyTest(t, s.Addr(), host.Code(), 1, nil, options)
	b := openLobbyTest(t, s.Addr(), host.Code(), 1, nil, options)
	colours := func(want ...uint8) func(HostedLobbyState, error) bool {
		return func(st HostedLobbyState, err error) bool {
			for i, c := range want {
				if !st.Seats[i].Present || st.Seats[i].Color != c {
					return false
				}
			}
			return err == nil
		}
	}
	awaitLobby(t, host, "colours 0, 1 and 2", colours(0, 1, 2))
	if err := a.SetColor(HostedColors); err == nil {
		t.Fatal("a colour past the ten was sent")
	}
	// A held colour, and any colour from a ready seat, change nothing. The
	// last message on a's connection, a ready with another rehearsal, makes
	// a mismatch that only its arrival can show; by then the colours are
	// handled, and had either applied, the others would not be ready.
	readyAll(t, host, host, b)
	steps := []func() error{
		func() error { return a.SetColor(0) },
		func() error { return a.SetReady(true, [32]byte{1}, [32]byte{2}) },
		func() error { return a.SetColor(3) },
		func() error { return a.SetReady(true, [32]byte{1}, [32]byte{9}) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	awaitLobby(t, host, "everyone ready in colours 0, 1 and 2, mismatched", func(st HostedLobbyState, err error) bool {
		return colours(0, 1, 2)(st, err) && st.Seats[0].Ready && st.Seats[1].Ready && st.Seats[2].Ready && st.Mismatch
	})
	// A free colour is taken and clears every seat's ready.
	if err := a.SetReady(false, [32]byte{}, [32]byte{}); err != nil {
		t.Fatal(err)
	}
	if err := a.SetColor(3); err != nil {
		t.Fatal(err)
	}
	awaitLobby(t, host, "a in colour 3, nobody ready", func(st HostedLobbyState, err error) bool {
		return colours(0, 3, 2)(st, err) && !st.Seats[0].Ready && !st.Seats[2].Ready
	})
	// b leaving frees colour 2 for the host; the next joiner takes the
	// lowest free colour, the host's old 0.
	_ = b.Close()
	awaitLobby(t, host, "b gone", func(st HostedLobbyState, err error) bool { return err == nil && !st.Seats[2].Present })
	if err := host.SetColor(2); err != nil {
		t.Fatal(err)
	}
	awaitLobby(t, a, "the host in colour 2", colours(2, 3))
	c := openLobbyTest(t, s.Addr(), host.Code(), 1, nil, options)
	awaitLobby(t, host, "the next joiner in colour 0", colours(2, 3, 0))
	// The relay refuses a colour past the ten that SetColor would not send.
	if err := c.send([]byte{hostedColorMessage, HostedColors}); err != nil {
		t.Fatal(err)
	}
	if _, err := awaitLobby(t, host, "the room failed", func(_ HostedLobbyState, err error) bool { return err != nil }); !strings.Contains(err.Error(), "colour") {
		t.Fatalf("out-of-range colour: %v", err)
	}
	awaitHostedCapacity(t, s, 0, 0)
}

// A defeated seat may leave and the others play on; a seat that is still
// playing may not (DESIGN_MULTIPLAYER §16.6).
func TestHostedDefeatedSeatMayLeave(t *testing.T) {
	s := listenHostedTest(t, HostedConfig{InsecureLoopback: true}, hostedDefaultTimeouts)
	options := HostedDialOptions{InsecureLoopback: true}
	host := openSizedLobby(t, s.Addr(), 3, []byte{1}, options)
	a := openLobbyTest(t, s.Addr(), host.Code(), 1, nil, options)
	b := openLobbyTest(t, s.Addr(), host.Code(), 1, nil, options)
	readyAll(t, host, host, a, b)
	if err := host.Start(); err != nil {
		t.Fatal(err)
	}
	var battles [3]*LocalClient
	for i, l := range []*HostedLobby{host, a, b} {
		awaitLobby(t, l, "Started", func(s HostedLobbyState, err error) bool { return err == nil && s.Started })
		battles[i] = openingBattleTest(t, l)
		_ = battles[i].conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	}
	tick := uint32(0)
	step := func(clients ...*LocalClient) {
		t.Helper()
		tick++
		for _, c := range clients {
			if g, err := c.ReadGrant(); err != nil || g.Tick != tick {
				t.Fatalf("grant %d: %+v %v", tick, g, err)
			}
		}
		for _, c := range clients {
			final := c == battles[2] && tick >= 5
			if err := c.Acknowledge(tick, [32]byte{}, false, final); err != nil {
				t.Fatal(err)
			}
		}
	}
	for range 5 {
		step(battles[0], battles[1], battles[2])
	}
	// Slot 2 reported its result final at tick 5; it leaves.
	_ = battles[2].Close()
	for range 40 {
		step(battles[0], battles[1])
	}
	st := s.Status()
	if len(st.Rooms) != 1 || !st.Rooms[0].Seats[2].Left || !st.Rooms[0].Seats[2].Defeated || st.Rooms[0].Phase != "playing" {
		t.Fatalf("status after a defeated seat left: %+v", st.Rooms)
	}
	// A seat still playing leaving ends the match for everyone.
	_ = battles[1].Close()
	for {
		if _, err := battles[0].ReadGrant(); err != nil {
			if !strings.Contains(err.Error(), "relay") && !strings.Contains(err.Error(), "EOF") {
				t.Logf("host saw: %v", err)
			}
			break
		}
	}
	awaitHostedCapacity(t, s, 0, 0)
	if st := s.Status(); len(st.Recent) != 1 || st.Recent[0].Outcome != finishDisconnected || st.Recent[0].Players != 3 {
		t.Fatalf("recent rooms: %+v", st.Recent)
	}
}

// The public status page shows rooms and seats by number, never a room code
// (an invitation), and is served beside /healthz on the relay's port.
func TestHostedStatusPage(t *testing.T) {
	s := listenWebSocketTest(t, HostedConfig{InsecureLoopback: true}, false, hostedDefaultTimeouts)
	address := "ws://" + s.Addr() + "/relay"
	options := HostedDialOptions{InsecureLoopback: true}
	host := openSizedLobby(t, address, 4, []byte{1}, options)
	joiner := openLobbyTest(t, address, host.Code(), 1, nil, options)
	if err := joiner.SetTeam(3); err != nil {
		t.Fatal(err)
	}
	awaitLobby(t, host, "the joiner's team", func(s HostedLobbyState, err error) bool { return err == nil && s.Seats[1].Team == 3 })
	for _, path := range []string{"/status", "/status.json"} {
		response, err := http.Get("http://" + s.Addr() + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK || bytes.Contains(body, []byte(host.Code())) {
			t.Fatalf("%s: %d, code shown: %v", path, response.StatusCode, bytes.Contains(body, []byte(host.Code())))
		}
		if path == "/status.json" {
			var st HostedStatus
			if err := json.Unmarshal(body, &st); err != nil {
				t.Fatal(err)
			}
			if st.Protocol != hostedVersion || st.Lobbies != 1 || st.Players != 2 || len(st.Rooms) != 1 || st.Rooms[0].Size != 4 || len(st.Rooms[0].Seats) != 2 || st.Rooms[0].Seats[1].Team != 3 {
				t.Fatalf("status: %+v", st)
			}
		} else if !bytes.Contains(body, []byte("Room 1")) {
			t.Fatalf("status page: %s", body)
		}
	}
}
