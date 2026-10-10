package relay

import (
	"context"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/netproto"
)

func openingLobbyPair(t *testing.T, timeouts hostedTimeouts, websocket bool, versions [2]uint16) (*HostedServer, [2]*HostedLobby) {
	t.Helper()
	var s *HostedServer
	var address string
	if websocket {
		s = listenWebSocketTest(t, HostedConfig{InsecureLoopback: true}, false, timeouts)
		address = "ws://" + s.Addr() + "/relay"
	} else {
		s = listenHostedTest(t, HostedConfig{InsecureLoopback: true}, timeouts)
		address = s.Addr()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var lobbies [2]*HostedLobby
	code := ""
	for seat := range lobbies {
		var config []byte
		size := 0
		if seat == 0 {
			config, size = []byte("opening"), 2
		}
		l, err := openHostedLobby(ctx, versions[seat], address, code, localTestHello(uint8(seat)), config, size, HostedDialOptions{InsecureLoopback: true})
		if err != nil {
			t.Fatal(err)
		}
		lobbies[seat], code = l, l.Code()
		t.Cleanup(func() { _ = l.Close() })
	}
	readyAll(t, lobbies[0], lobbies[0], lobbies[1])
	return s, lobbies
}

func startOpeningPair(t *testing.T, lobbies [2]*HostedLobby) [2]*LocalClient {
	t.Helper()
	if err := lobbies[0].Start(); err != nil {
		t.Fatal(err)
	}
	var clients [2]*LocalClient
	for i, l := range lobbies {
		awaitLobby(t, l, "Started", func(s HostedLobbyState, err error) bool { return err == nil && s.Started })
		clients[i] = l.Battle()
		c := clients[i]
		t.Cleanup(func() { _ = c.Close() })
	}
	return clients
}

type openingGrantResult struct {
	grant LocalGrant
	err   error
}

func readOpeningGrant(c *LocalClient) <-chan openingGrantResult {
	result := make(chan openingGrantResult, 1)
	go func() {
		g, err := c.ReadGrant()
		result <- openingGrantResult{g, err}
	}()
	return result
}

func awaitOpeningGrant(t *testing.T, result <-chan openingGrantResult) LocalGrant {
	t.Helper()
	select {
	case r := <-result:
		if r.err != nil {
			t.Fatal(r.err)
		}
		return r.grant
	case <-time.After(3 * time.Second):
		t.Fatal("opening did not release a grant")
		return LocalGrant{}
	}
}

// Started publishes the opening, then every version-7 seat must explicitly
// finish it. The execution-progress deadline starts only at release, while
// regular tick-zero progress traffic keeps both readers responsive
// (DESIGN_MULTIPLAYER §16.5.2, §16.6.1).
func TestHostedOpeningBarrierRetainsCommandsAndProgress(t *testing.T) {
	for _, websocket := range []bool{false, true} {
		t.Run(fmt.Sprint(websocket), func(t *testing.T) {
			timeouts := hostedDefaultTimeouts
			timeouts.progress, timeouts.report, timeouts.opening = 100*time.Millisecond, 20*time.Millisecond, 2*time.Second
			s, lobbies := openingLobbyPair(t, timeouts, websocket, [2]uint16{7, 7})
			host := lobbies[0].client
			before := host.Traffic().MessagesOut
			if err := host.OpeningReady(); err == nil || !strings.Contains(err.Error(), "after Started") {
				t.Fatalf("early API marker: %v", err)
			}
			if host.Traffic().MessagesOut != before {
				t.Fatal("early API marker wrote to the connection")
			}
			// These two commands precede Start on the host's ordered stream.
			for _, order := range []string{"first", "second"} {
				if _, err := host.Submit([]byte(order)); err != nil {
					t.Fatal(err)
				}
			}
			clients := startOpeningPair(t, lobbies)
			for _, c := range clients {
				c.idle = 200 * time.Millisecond
			}
			reads := [2]<-chan openingGrantResult{readOpeningGrant(clients[0]), readOpeningGrant(clients[1])}
			before = host.Traffic().MessagesOut
			// Concurrent, duplicate API calls send exactly one serialized marker.
			var writers sync.WaitGroup
			errors := make(chan error, 16)
			for range 16 {
				writers.Add(1)
				go func() {
					defer writers.Done()
					errors <- host.OpeningReady()
				}()
			}
			writers.Wait()
			close(errors)
			for err := range errors {
				if err != nil {
					t.Fatal(err)
				}
			}
			if host.Traffic().MessagesOut != before+1 {
				t.Fatal("OpeningReady sent more than one marker")
			}
			if _, err := clients[1].Submit([]byte("third")); err != nil {
				t.Fatal(err)
			}
			// Waiting longer than execution's abbreviated deadline must neither
			// seal a tick nor fail the room while one opening remains unfinished.
			select {
			case r := <-reads[0]:
				t.Fatalf("only one seat ready: %+v %v", r.grant, r.err)
			case r := <-reads[1]:
				t.Fatalf("unfinished seat got a grant: %+v %v", r.grant, r.err)
			case <-time.After(250 * time.Millisecond):
			}
			for _, c := range clients {
				p, ok := c.Progress()
				if !ok || p.Sealed != 0 || p.Agreed != 0 || len(p.Seats) != 2 || p.Seats[0].Acked != 0 || p.Seats[1].Acked != 0 {
					t.Fatalf("opening progress: %+v %v", p, ok)
				}
			}
			if status := s.Status(); len(status.Rooms) != 1 || status.Rooms[0].Ticks != 0 {
				t.Fatalf("opening advanced the room: %+v", status.Rooms)
			}
			if err := clients[1].OpeningReady(); err != nil {
				t.Fatal(err)
			}
			first := awaitOpeningGrant(t, reads[0])
			if other := awaitOpeningGrant(t, reads[1]); !reflect.DeepEqual(first, other) {
				t.Fatalf("released grants differ: %+v / %+v", first, other)
			}
			want := LocalGrant{Tick: 1, Position: 3, Commands: []LocalCommand{
				{Seat: 0, Sequence: 1, Position: 1, Payload: []byte("first")},
				{Seat: 0, Sequence: 2, Position: 2, Payload: []byte("second")},
				{Seat: 1, Sequence: 1, Position: 3, Payload: []byte("third")},
			}}
			if !reflect.DeepEqual(first, want) {
				t.Fatalf("opening command order: %+v, want %+v", first, want)
			}
			// Duplicate markers on the wire remain harmless after release too.
			if err := clients[0].writeMessage([]byte{hostedOpeningReadyMessage}); err != nil {
				t.Fatal(err)
			}
			ackLocalPair(t, clients, 1, [32]byte{}, true)
			for _, c := range clients {
				if err := hostedReadError(t, c); err != io.EOF {
					t.Fatalf("completion after opening: %v", err)
				}
			}
			awaitHostedCapacity(t, s, 0, 0)
		})
	}
}

func TestHostedOpeningRefusesEarlyMalformedAndDisconnectedSeats(t *testing.T) {
	for _, failure := range []string{"early", "malformed", "disconnect", "expiry", "acknowledgment"} {
		t.Run(failure, func(t *testing.T) {
			timeouts := hostedDefaultTimeouts
			timeouts.opening, timeouts.report = 120*time.Millisecond, 20*time.Millisecond
			s, lobbies := openingLobbyPair(t, timeouts, false, [2]uint16{7, 7})
			if failure == "early" {
				if err := lobbies[1].send([]byte{hostedOpeningReadyMessage}); err != nil {
					t.Fatal(err)
				}
				_, err := awaitLobby(t, lobbies[0], "an early marker refusal", func(_ HostedLobbyState, err error) bool { return err != nil })
				if !strings.Contains(err.Error(), "OpeningReady after Started") {
					t.Fatalf("early marker: %v", err)
				}
				awaitHostedCapacity(t, s, 0, 0)
				return
			}
			clients := startOpeningPair(t, lobbies)
			if err := clients[0].OpeningReady(); err != nil {
				t.Fatal(err)
			}
			want := ""
			switch failure {
			case "malformed":
				want = "no bytes after the last field"
				if err := clients[1].writeMessage([]byte{hostedOpeningReadyMessage, 0}); err != nil {
					t.Fatal(err)
				}
			case "disconnect":
				_ = clients[1].Close()
			case "expiry":
				want = "every seat to finish its opening within 30 minutes"
			case "acknowledgment":
				want = "after every seat finishes its opening"
				if err := clients[1].Acknowledge(1, [32]byte{}, false, false); err != nil {
					t.Fatal(err)
				}
			}
			if g, err := clients[0].ReadGrant(); err == nil || err == io.EOF || (want != "" && !strings.Contains(err.Error(), want)) {
				t.Fatalf("%s during opening: %+v %v", failure, g, err)
			}
			awaitHostedCapacity(t, s, 0, 0)
		})
	}
}

// Earlier protocols have no opening marker and are implicitly ready, including
// in a room with a version-7 seat (DESIGN_MULTIPLAYER §12.2).
func TestHostedOpeningLegacySeatsRemainImplicitlyReady(t *testing.T) {
	for _, versions := range [][2]uint16{{5, 5}, {6, 6}, {5, 6}, {5, 7}, {6, 7}} {
		t.Run(fmt.Sprint(versions), func(t *testing.T) {
			_, lobbies := openingLobbyPair(t, hostedDefaultTimeouts, false, versions)
			clients := startOpeningPair(t, lobbies)
			for i, c := range clients {
				if versions[i] == 7 {
					if err := c.OpeningReady(); err != nil {
						t.Fatal(err)
					}
				} else {
					before := c.Traffic().MessagesOut
					if err := c.OpeningReady(); err != nil || c.Traffic().MessagesOut != before {
						t.Fatalf("legacy marker: %v", err)
					}
				}
			}
			if g := readLocalPair(t, clients); g.Tick != 1 {
				t.Fatalf("legacy first grant: %+v", g)
			}
		})
	}
}

// Auto-start and direct dial are still held until their readers consume
// Started. Starting one direct reader cannot ready the other seat.
func TestHostedDirectOpeningReadyFollowsStarted(t *testing.T) {
	timeouts := hostedDefaultTimeouts
	timeouts.progress, timeouts.report = 60*time.Millisecond, 20*time.Millisecond
	s := listenHostedTest(t, HostedConfig{InsecureLoopback: true}, timeouts)
	clients, _ := hostedTestPair(t, s)
	first := readOpeningGrant(clients[0])
	select {
	case r := <-first:
		t.Fatalf("one direct reader released opening: %+v %v", r.grant, r.err)
	case <-time.After(150 * time.Millisecond):
	}
	if out := clients[1].Traffic().MessagesOut; out != 2 {
		t.Fatalf("unread Started sent opening readiness: %d messages", out)
	}
	second := readOpeningGrant(clients[1])
	if g := awaitOpeningGrant(t, first); g.Tick != 1 {
		t.Fatalf("first direct grant: %+v", g)
	}
	if g := awaitOpeningGrant(t, second); g.Tick != 1 {
		t.Fatalf("second direct grant: %+v", g)
	}
	for _, c := range clients {
		if out := c.Traffic().MessagesOut; out != 3 {
			t.Fatalf("direct opening marker: %d messages", out)
		}
	}
}

// After Started, silence must wake the client even before its first grant.
func TestHostedOpeningClientIdleBound(t *testing.T) {
	conn, server := net.Pipe()
	defer conn.Close()
	defer server.Close()
	_ = server.SetWriteDeadline(time.Now().Add(time.Second))
	c := newHostedClient(conn)
	c.version, c.idle = hostedVersion, 40*time.Millisecond
	written := make(chan error, 1)
	go func() { written <- writeLocalFrame(server, []byte{hostedStartedMessage, 0}) }()
	if g, err := c.ReadGrant(); err == nil || !strings.Contains(err.Error(), "relay traffic within 40ms") {
		t.Fatalf("silent opening: %+v %v", g, err)
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
}

// All writer APIs share the same lock. A readiness fan-out emits one marker
// while concurrent Submit and Acknowledge envelopes remain complete.
func TestHostedOpeningReadySerializesWriters(t *testing.T) {
	conn, server := net.Pipe()
	defer conn.Close()
	defer server.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_ = server.SetDeadline(time.Now().Add(3 * time.Second))
	c := newHostedClient(conn)
	c.version = hostedVersion
	c.started.Store(true)
	read := make(chan error, 1)
	go func() {
		markers, sequence := 0, uint64(0)
		for range 17 {
			body, err := readLocalFrame(server, hostedMaxClientFrame)
			if err != nil {
				read <- err
				return
			}
			r := netproto.NewReader(body, hostedError)
			switch r.U8() {
			case hostedOpeningReadyMessage:
				markers++
			case localSubmitMessage:
				sequence++
				if r.U64() != sequence || string(r.Raw(r.Count(8, 1))) != "order" {
					r.Abort(fmt.Errorf("interleaved submission"))
				}
			case localAckMessage:
				if r.U32() != 1 || r.U8() != 0 {
					r.Abort(fmt.Errorf("interleaved acknowledgment"))
				}
			default:
				r.Abort(fmt.Errorf("unexpected writer message"))
			}
			if err := r.End(); err != nil {
				read <- err
				return
			}
		}
		if markers != 1 || sequence != 8 {
			read <- fmt.Errorf("markers/submissions: %d/%d", markers, sequence)
			return
		}
		read <- nil
	}()
	var writers sync.WaitGroup
	errors := make(chan error, 24)
	for range 8 {
		for _, write := range []func() error{c.OpeningReady, func() error { _, err := c.Submit([]byte("order")); return err }, func() error { return c.Acknowledge(1, [32]byte{}, false, false) }} {
			writers.Add(1)
			go func() { defer writers.Done(); errors <- write() }()
		}
	}
	writers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := <-read; err != nil {
		t.Fatal(err)
	}
	local := &LocalClient{}
	if err := local.OpeningReady(); err != nil {
		t.Fatalf("loopback readiness: %v", err)
	}
}
