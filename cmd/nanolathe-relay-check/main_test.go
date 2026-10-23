package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/netproto"
	"github.com/nanolathe-gg/nanolathe/internal/relay"
)

func TestRelayCheckCreatesDescribesJoinsAndLeavesOnlyItsRoom(t *testing.T) {
	for _, transport := range []string{"tcp", "websocket"} {
		t.Run(transport, func(t *testing.T) {
			finished := make(chan relay.FinishedRoom, 2)
			server, address := listenTestRelay(t, transport, func(room relay.FinishedRoom) { finished <- room })
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			options := relay.HostedDialOptions{InsecureLoopback: true}
			hello := relay.LocalHello{Identity: netproto.Identity{
				Protocol: netproto.CommandSchemaVersion,
				Rules:    netproto.RuleIdentity{Name: "unrelated-room"},
			}}
			other, err := relay.OpenHostedLobby(ctx, address, "", hello, []byte("another lobby"), 2, options)
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close()
			var out bytes.Buffer
			if err := run(ctx, []string{"--address", address, "--insecure-loopback"}, &out); err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf("Relay compatible: command schema %d at %s (create, describe, join; both seats present)\n", netproto.CommandSchemaVersion, address)
			if out.String() != want {
				t.Fatalf("output %q, want %q", out.String(), want)
			}
			select {
			case room := <-finished:
				if room.Ticks != 0 || room.Orders != 0 || server.Status().Totals.Matches != 0 {
					t.Fatalf("compatibility check started a battle: %+v", room)
				}
			case <-ctx.Done():
				t.Fatal("compatibility check left its room open")
			}
			if _, err := relay.DescribeHostedRoom(ctx, address, other.Code(), options); err != nil {
				t.Fatalf("unrelated room was affected: %v", err)
			}
		})
	}
}

func TestRelayCheckReportsRefusal(t *testing.T) {
	server, address := listenTestRelay(t, "websocket", nil)
	server.Drain()
	var out bytes.Buffer
	err := run(context.Background(), []string{"--address", address, "--insecure-loopback"}, &out)
	for _, want := range []string{"logical path create", address, fmt.Sprintf("command schema %d", netproto.CommandSchemaVersion), "restarting"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %v, want %q", err, want)
		}
	}
	if out.Len() != 0 {
		t.Fatalf("reported success after refusal: %q", out.String())
	}
}

func TestRelayCheckRecoversFromGatewayFailuresAtEveryStage(t *testing.T) {
	for _, entry := range []struct {
		stage  string
		at     int
		status int
	}{
		{"create", 1, http.StatusBadGateway},
		{"describe", 2, http.StatusServiceUnavailable},
		{"join", 3, http.StatusGatewayTimeout},
	} {
		t.Run(entry.stage, func(t *testing.T) {
			finished := make(chan relay.FinishedRoom, 3)
			server, address := listenTestRelay(t, "websocket", func(room relay.FinishedRoom) { finished <- room })
			proxy, requests := relayCheckProxy(t, address, func(request int) int {
				if request == entry.at || request == 2*entry.at {
					return entry.status
				}
				return 0
			})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var out bytes.Buffer
			if err := checkRelay(ctx, proxy, relay.HostedDialOptions{InsecureLoopback: true}, &out, time.Millisecond); err != nil {
				t.Fatal(err)
			}
			if got, want := int(requests.Load()), 2*entry.at+3; got != want {
				t.Fatalf("HTTP requests %d, want %d including a complete final attempt", got, want)
			}
			for _, want := range []string{"attempt 1/3", "attempt 2/3", "logical path " + entry.stage, fmt.Sprintf("HTTP %d", entry.status), `Cf-Ray="authored-proxy-request"`, "retrying in 1ms", "retrying in 2ms"} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("retry output %q, want %q", out.String(), want)
				}
			}
			rooms := 3
			if entry.at == 1 {
				rooms = 1 // Failed upgrades cannot create a room.
			}
			for range rooms {
				select {
				case room := <-finished:
					if room.Ticks != 0 || room.Orders != 0 || server.Status().Totals.Matches != 0 {
						t.Fatalf("retry started a battle: %+v", room)
					}
				case <-ctx.Done():
					t.Fatal("a check attempt left its room open")
				}
			}
		})
	}
}

func TestRelayCheckBoundsRetriesAndKeepsPermanentRefusals(t *testing.T) {
	for _, status := range []int{http.StatusBadGateway, http.StatusForbidden, http.StatusNotFound, http.StatusUpgradeRequired, http.StatusNotImplemented, http.StatusSwitchingProtocols} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			_, address := listenTestRelay(t, "websocket", nil)
			proxy, requests := relayCheckProxy(t, address, func(int) int { return status })
			var out bytes.Buffer
			err := checkRelay(context.Background(), proxy, relay.HostedDialOptions{InsecureLoopback: true}, &out, time.Millisecond)
			var handshake *relay.WebSocketHandshakeError
			if !errors.As(err, &handshake) || handshake.StatusCode != status {
				t.Fatalf("HTTP failure was lost: %v", err)
			}
			want := int32(1)
			if status == http.StatusBadGateway {
				want = 3
			}
			if requests.Load() != want {
				t.Fatalf("made %d requests, want %d", requests.Load(), want)
			}
			if want == 1 && out.Len() != 0 {
				t.Fatalf("retried a permanent refusal: %q", out.String())
			}
		})
	}
}

func TestRelayCheckCancellationInterruptsRetryWait(t *testing.T) {
	_, address := listenTestRelay(t, "websocket", nil)
	proxy, requests := relayCheckProxy(t, address, func(int) int { return http.StatusServiceUnavailable })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := checkRelay(ctx, proxy, relay.HostedDialOptions{InsecureLoopback: true}, cancelOnRetry{cancel}, time.Hour)
	var handshake *relay.WebSocketHandshakeError
	if !errors.Is(err, context.Canceled) || !errors.As(err, &handshake) || handshake.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("retry wait lost cancellation or HTTP failure: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("retried after cancellation: %d requests", requests.Load())
	}
}

type cancelOnRetry struct{ cancel context.CancelFunc }

func (w cancelOnRetry) Write(p []byte) (int, error) {
	w.cancel()
	return len(p), nil
}

// Model a gateway before a real relay, so recovery still has to create,
// describe and join a lobby using the production client and wire protocol.
func relayCheckProxy(t *testing.T, address string, status func(request int) int) (string, *atomic.Int32) {
	t.Helper()
	upstream, err := url.Parse("http://" + strings.TrimPrefix(address, "ws://"))
	if err != nil {
		t.Fatal(err)
	}
	upstream.Path = ""
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	requests := new(atomic.Int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if code := status(int(requests.Add(1))); code != 0 {
			w.Header().Set("Cf-Ray", "authored-proxy-request")
			w.WriteHeader(code)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return "ws://" + strings.TrimPrefix(server.URL, "http://") + "/relay", requests
}

func TestRelayCheckBoundsLobbyWait(t *testing.T) {
	_, address := listenTestRelay(t, "websocket", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	hello := relay.LocalHello{Identity: netproto.Identity{
		Protocol: netproto.CommandSchemaVersion,
		Rules:    netproto.RuleIdentity{Name: "relay-check"},
	}}
	lobby, err := relay.OpenHostedLobby(ctx, address, "", hello, []byte("waiting"), 2, relay.HostedDialOptions{InsecureLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	defer lobby.Close()
	ctx, cancelWait := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancelWait()
	if err := awaitBothSeats(ctx, lobby); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting for an absent joiner: %v", err)
	}
}

func listenTestRelay(t *testing.T, transport string, finished func(relay.FinishedRoom)) (*relay.HostedServer, string) {
	t.Helper()
	config := relay.HostedConfig{InsecureLoopback: true, Finished: finished}
	var server *relay.HostedServer
	var err error
	if transport == "websocket" {
		server, err = relay.ListenHostedWebSocket("127.0.0.1:0", config, false)
	} else {
		server, err = relay.ListenHosted("127.0.0.1:0", config)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	address := server.Addr()
	if transport == "websocket" {
		address = "ws://" + address + "/relay"
	}
	return server, address
}
