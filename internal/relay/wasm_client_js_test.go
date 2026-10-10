package relay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// TestWasmSeats is the browser half of TestWasmClientPlaysHostedMatch: two
// seats in this js/wasm process reach the native relay the harness started,
// through the browser's WebSocket (DESIGN_MULTIPLAYER §16.5.1). One creates
// the room and starts it; the other describes and joins it; both play to an
// agreed terminal tick.
func TestWasmSeats(t *testing.T) {
	address := os.Getenv("NANOLATHE_WASM_RELAY_URL")
	if address == "" {
		t.Skip("run by TestWasmClientPlaysHostedMatch")
	}
	options := HostedDialOptions{InsecureLoopback: true}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// A page cannot open a TCP socket, so host:port is refused by name.
	if c, _, err := DialHosted(ctx, "127.0.0.1:39032", "", localTestHello(0), options); err == nil || !strings.Contains(err.Error(), "only to wss:// relays") {
		if c != nil {
			_ = c.Close()
		}
		t.Fatalf("host:port in the browser: %v", err)
	}
	// The plaintext rules match the native client's.
	if _, err := dialHostedTransport(ctx, address, HostedDialOptions{}); err == nil {
		t.Fatal("ws:// without the loopback switch")
	}
	// An upgrade without the relay subprotocol is not a relay.
	if conn, err := dialHostedTransport(ctx, os.Getenv("NANOLATHE_WASM_BARE_URL"), options); err == nil {
		_ = conn.Close()
		t.Fatal("accepted an upgrade without the relay subprotocol")
	} else {
		t.Logf("upgrade without the subprotocol: %v", err)
	}
	// A read deadline wakes a blocked read on an open socket.
	conn, err := dialHostedTransport(ctx, address, options)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if _, err := conn.Read(make([]byte, 1)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("read past its deadline: %v", err)
	}
	_ = conn.Close()
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("read after close")
	}

	config := bytes.Repeat([]byte("browser room "), 512)
	host, err := OpenHostedLobby(ctx, address, "", localTestHello(0), config, 2, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close() })
	description, err := DescribeHostedRoom(ctx, address, host.Code(), options)
	if err != nil || description.Size != 2 || !bytes.Equal(description.Config, config) {
		t.Fatalf("describe: size %d, %d bytes, %v", description.Size, len(description.Config), err)
	}
	joiner, err := OpenHostedLobby(ctx, address, host.Code(), localTestHello(1), nil, 0, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = joiner.Close() })
	if err := joiner.SetColor(4); err != nil {
		t.Fatal(err)
	}
	awaitLobby(t, host, "the joiner's colour", func(s HostedLobbyState, err error) bool {
		return err == nil && s.Seats[1].Present && s.Seats[1].Color == 4
	})
	if !bytes.Equal(joiner.Configuration(), config) {
		t.Fatal("the joiner did not receive the room's configuration")
	}
	readyAll(t, host, host, joiner)
	if err := host.Start(); err != nil {
		t.Fatal(err)
	}
	var seats [2]*LocalClient
	for i, l := range []*HostedLobby{host, joiner} {
		awaitLobby(t, l, "Started", func(s HostedLobbyState, err error) bool { return err == nil && s.Started })
		if seats[i] = l.Battle(); seats[i] == nil {
			t.Fatal("started lobby did not hand over its connection")
		}
		if err := seats[i].OpeningReady(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = seats[i].Close() })
	}
	if _, err := seats[1].Submit([]byte("browser order")); err != nil {
		t.Fatal(err)
	}
	ordered, measured := false, [2]bool{}
	for tick := uint32(1); tick <= wasmTerminalTick; tick++ {
		for _, c := range seats {
			g, err := c.ReadGrant()
			if err != nil || g.Tick != tick {
				t.Fatalf("grant %d: %+v %v", tick, g, err)
			}
			for _, command := range g.Commands {
				ordered = ordered || (command.Seat == 1 && string(command.Payload) == "browser order")
			}
		}
		for i, c := range seats {
			if err := c.Acknowledge(tick, [32]byte{}, tick == wasmTerminalTick, false); err != nil {
				t.Fatal(err)
			}
			// The relay's report carries the round trip of its pings, which
			// only the browser answers (§16.5.2).
			if p, ok := c.Progress(); ok && len(p.Seats) == 2 {
				measured[i] = measured[i] || p.Seats[i].RTT > 0
			}
		}
	}
	if !ordered {
		t.Fatal("the browser seat's order never reached a grant")
	}
	for i, c := range seats {
		p, ok := c.Progress()
		if !ok || p.Agreed == 0 || len(p.Seats) != 2 || !p.Seats[0].Playing || !p.Seats[1].Playing || !measured[i] {
			t.Fatalf("seat %d progress: %+v %v, round trip measured %v", i, p, ok, measured[i])
		}
		if tr := c.Traffic(); tr.MessagesIn < wasmTerminalTick || tr.MessagesOut < wasmTerminalTick || tr.BytesIn < 2*tr.MessagesIn || tr.BytesOut < 2*tr.MessagesOut {
			t.Fatalf("seat %d traffic: %+v", i, tr)
		}
	}
	// Surplus grants sealed before the terminal acknowledgement drain first.
	for _, c := range seats {
		for {
			g, err := c.ReadGrant()
			if err == io.EOF {
				break
			}
			if err != nil || g.Tick <= wasmTerminalTick {
				t.Fatalf("after the terminal tick: %+v %v", g, err)
			}
		}
	}
}
