package relay

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func listenWebSocketTest(t *testing.T, config HostedConfig, proxy bool, timeouts hostedTimeouts) *HostedServer {
	t.Helper()
	s, err := listenHostedWebSocket("127.0.0.1:0", config, proxy, timeouts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func dialWebSocketTest(t *testing.T, address, room string, seat uint8, options HostedDialOptions) (*LocalClient, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, code, err := DialHostedWebSocket(ctx, address, room, localTestHello(seat), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	// These tests manage read deadlines themselves; one test covers the idle bound.
	c.idle = 0
	_ = c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	return c, code
}

func TestWebSocketRoomsAndExplicitCompletion(t *testing.T) {
	for _, transport := range []string{"ws", "wss", "proxy"} {
		t.Run(transport, func(t *testing.T) {
			config := HostedConfig{InsecureLoopback: true}
			options := HostedDialOptions{InsecureLoopback: true}
			scheme := "ws"
			if transport == "wss" {
				serverTLS, clientTLS := hostedTestTLS(t)
				config = HostedConfig{TLSConfig: serverTLS}
				options = HostedDialOptions{TLSConfig: clientTLS}
				scheme = "wss"
			} else if transport == "proxy" {
				config = HostedConfig{}
			}
			s := listenWebSocketTest(t, config, transport == "proxy", hostedDefaultTimeouts)
			address := scheme + "://" + s.Addr() + "/relay"
			c0, code := dialWebSocketTest(t, address, "", 0, options)
			// A large command exercises extended lengths and masking over more
			// than one scratch chunk, before the second seat starts grants.
			payload := bytes.Repeat([]byte{0, 1, 2, 255, 17}, 14000)
			if _, err := c0.Submit(payload); err != nil {
				t.Fatal(err)
			}
			rawLocalSubmit(t, c0, 3, nil)
			if _, err := c0.ReadGrant(); err == nil || !strings.Contains(err.Error(), "sequence 2") {
				t.Fatalf("command fence: %v", err)
			}
			c1, joined := dialWebSocketTest(t, address, code, 1, options)
			if joined != code {
				t.Fatal("join changed invitation")
			}
			clients := [2]*LocalClient{c0, c1}
			grant := readLocalPair(t, clients)
			if len(grant.Commands) != 1 || !bytes.Equal(grant.Commands[0].Payload, payload) {
				t.Fatal("WebSocket changed opaque command bytes")
			}
			ackLocalPair(t, clients, grant.Tick, [32]byte{}, true)
			for _, c := range clients {
				if err := hostedReadError(t, c); err != io.EOF {
					t.Fatalf("explicit completion: %v", err)
				}
			}
			awaitHostedCapacity(t, s, 0, 0)
		})
	}
}

func TestWebSocketTLSAndURLRefusals(t *testing.T) {
	for _, entry := range []struct {
		config HostedConfig
		proxy  bool
	}{
		{HostedConfig{}, false}, {HostedConfig{TLSConfig: &tls.Config{}}, false},
		{HostedConfig{InsecureLoopback: true}, true}, {HostedConfig{TLSConfig: &tls.Config{}}, true},
		{HostedConfig{InsecureLoopback: true, TLSConfig: &tls.Config{}}, false},
		{HostedConfig{MaxRooms: 257}, true},
	} {
		if s, err := ListenHostedWebSocket("127.0.0.1:0", entry.config, entry.proxy); err == nil {
			_ = s.Close()
			t.Fatalf("accepted invalid server configuration: %+v", entry)
		}
	}
	if s, err := ListenHostedWebSocket("0.0.0.0:0", HostedConfig{InsecureLoopback: true}, false); err == nil {
		_ = s.Close()
		t.Fatal("accepted public loopback mode")
	}
	public, err := ListenHostedWebSocket(":0", HostedConfig{}, true)
	if err != nil {
		t.Fatal(err)
	}
	_ = public.Close()
	serverTLS, clientTLS := hostedTestTLS(t)
	s := listenWebSocketTest(t, HostedConfig{TLSConfig: serverTLS}, false, hostedDefaultTimeouts)
	address := "wss://" + s.Addr() + "/relay"
	for _, options := range []HostedDialOptions{
		{TLSConfig: &tls.Config{RootCAs: x509.NewCertPool()}},
		{TLSConfig: &tls.Config{RootCAs: clientTLS.RootCAs, ServerName: "wrong.invalid"}},
		{TLSConfig: &tls.Config{InsecureSkipVerify: true}},
		{InsecureLoopback: true},
	} {
		if c, _, err := DialHostedWebSocket(context.Background(), address, "", localTestHello(0), options); err == nil {
			_ = c.Close()
			t.Fatalf("accepted invalid TLS options: %+v", options)
		}
	}
	for _, address := range []string{
		"ws://localhost:1/relay", "ws://192.0.2.1:1/relay", "ws://0.0.0.0:1/relay",
		"http://127.0.0.1:1/relay", "ws://user:password@127.0.0.1:1/relay",
		"ws://127.0.0.1:1/relay?", "ws://127.0.0.1:1/relay?q=x", "ws://127.0.0.1:1/relay#",
		"ws://127.0.0.1:1/relay#part", "ws://127.0.0.1:1/", "ws://127.0.0.1:1/%72elay",
	} {
		if c, _, err := DialHostedWebSocket(context.Background(), address, "", localTestHello(0), HostedDialOptions{InsecureLoopback: true}); err == nil || strings.Contains(err.Error(), "connection refused") {
			if c != nil {
				_ = c.Close()
			}
			t.Fatalf("invalid URL was dialed: %s: %v", address, err)
		}
	}
	for _, options := range []HostedDialOptions{{}, {InsecureLoopback: true, TLSConfig: clientTLS}} {
		if c, _, err := DialHostedWebSocket(context.Background(), "ws://127.0.0.1:1/relay", "", localTestHello(0), options); err == nil || strings.Contains(err.Error(), "connection refused") {
			if c != nil {
				_ = c.Close()
			}
			t.Fatalf("invalid plaintext options were dialed: %v", err)
		}
	}
}

const websocketTestRequest = "GET /relay HTTP/1.1\r\nHost: localhost\r\nUpgrade: websocket\r\nConnection: keep-alive, Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Protocol: nanolathe-relay-v1\r\n\r\n"

func rawWebSocketTest(t *testing.T, address string) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	return c
}

func TestWebSocketHandshakeHealthAndPipelinedHello(t *testing.T) {
	s := listenWebSocketTest(t, HostedConfig{InsecureLoopback: true}, false, hostedDefaultTimeouts)
	for _, request := range []string{
		strings.Replace(websocketTestRequest, "GET ", "POST ", 1),
		strings.Replace(websocketTestRequest, "/relay ", "/relay?x=1 ", 1),
		strings.Replace(websocketTestRequest, "/relay ", "/other ", 1),
		strings.Replace(websocketTestRequest, "HTTP/1.1", "HTTP/1.0", 1),
		strings.Replace(websocketTestRequest, "Upgrade: websocket", "Upgrade: other", 1),
		strings.Replace(websocketTestRequest, "keep-alive, Upgrade", "keep-alive", 1),
		strings.Replace(websocketTestRequest, "Version: 13", "Version: 12", 1),
		strings.Replace(websocketTestRequest, "dGhlIHNhbXBsZSBub25jZQ==", "YQ==", 1),
		strings.Replace(websocketTestRequest, "nanolathe-relay-v1", "NANOLATHE-RELAY-V1", 1),
		strings.Replace(websocketTestRequest, "\r\n\r\n", "\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n", 1),
		strings.Replace(websocketTestRequest, "\r\n\r\n", "\r\nContent-Length: 1\r\n\r\n", 1),
		strings.Replace(websocketTestRequest, "\r\n\r\n", "\r\nTransfer-Encoding: chunked\r\n\r\n", 1),
	} {
		c := rawWebSocketTest(t, s.Addr())
		if _, err := io.WriteString(c, request); err != nil {
			t.Fatal(err)
		}
		response, err := http.ReadResponse(bufio.NewReader(c), nil)
		if err != nil || response.StatusCode != http.StatusBadRequest {
			t.Fatalf("accepted malformed upgrade %q: %v / %v", request, response, err)
		}
		_ = response.Body.Close()
		_ = c.Close()
	}
	c := rawWebSocketTest(t, s.Addr())
	if _, err := io.WriteString(c, "GET /healthz HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != 200 || string(body) != "ok\n" {
		t.Fatalf("health: %s / %v", body, err)
	}
	_ = c.Close()
	c = rawWebSocketTest(t, s.Addr())
	var envelope bytes.Buffer
	hello, err := encodeHostedHello("", hostedAutoStart, 2, localTestHello(0), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeLocalFrame(&envelope, hello); err != nil {
		t.Fatal(err)
	}
	request := append([]byte(websocketTestRequest), websocketTestFrame(0x82, envelope.Bytes(), true)...)
	if err := websocketWriteAll(c, request); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(c)
	response, err = http.ReadResponse(r, nil)
	if err != nil || response.StatusCode != 101 || response.Header.Get("Sec-WebSocket-Accept") != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("upgrade: %v / %v", response, err)
	}
	stream := newWebSocketConn(c, r, true, localMaxGrantFrame+5, time.Second, 0)
	defer stream.Close()
	welcome, err := readLocalFrame(stream, localMaxHelloBytes)
	if err != nil || len(welcome) == 0 || welcome[0] != hostedWelcomeMessage {
		t.Fatalf("pipelined hello: %x / %v", welcome, err)
	}
	_ = stream.Close()
	awaitHostedCapacity(t, s, 0, 0)
}

// A browser's upgrade always offers permessage-deflate; the relay accepts it
// and declines the extension by naming none in its response (RFC 6455 §9.1).
// A close frame from the browser is answered with a close frame and leaves
// the lobby like any departure: the seat is freed and the room goes on.
func TestWebSocketBrowserUpgradeAndClose(t *testing.T) {
	s := listenWebSocketTest(t, HostedConfig{InsecureLoopback: true}, false, hostedDefaultTimeouts)
	options := HostedDialOptions{InsecureLoopback: true}
	host := openSizedLobby(t, "ws://"+s.Addr()+"/relay", 3, []byte{1}, options)
	c := rawWebSocketTest(t, s.Addr())
	hello := localTestHello(1)
	hello.Seat = hostedAnySeat
	body, err := encodeHostedHello(host.Code(), 0, 0, hello, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	var envelope bytes.Buffer
	if err := writeLocalFrame(&envelope, body); err != nil {
		t.Fatal(err)
	}
	request := strings.Replace(websocketTestRequest, "\r\n\r\n", "\r\nOrigin: https://example.invalid\r\nSec-WebSocket-Extensions: permessage-deflate; client_max_window_bits\r\n\r\n", 1)
	if err := websocketWriteAll(c, append([]byte(request), websocketTestFrame(0x82, envelope.Bytes(), true)...)); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(c)
	response, err := http.ReadResponse(r, nil)
	if err != nil || response.StatusCode != http.StatusSwitchingProtocols || len(response.Header.Values("Sec-WebSocket-Extensions")) != 0 {
		t.Fatalf("browser upgrade: %v / %v", response, err)
	}
	awaitLobby(t, host, "the browser seat", present(true))
	closing := []byte{3, 233} // 1001, going away
	if err := websocketWriteAll(c, websocketTestFrame(0x88, closing, true)); err != nil {
		t.Fatal(err)
	}
	// The relay's data frames (welcome, configuration, lobby states) come
	// first, then its close frame echoing the status.
	for {
		var header [2]byte
		if _, err := io.ReadFull(r, header[:]); err != nil {
			t.Fatalf("no close frame before the connection ended: %v", err)
		}
		size := int(header[1] & 127)
		if size == 126 {
			var extended [2]byte
			if _, err := io.ReadFull(r, extended[:]); err != nil {
				t.Fatal(err)
			}
			size = int(extended[0])<<8 | int(extended[1])
		}
		payload := make([]byte, size)
		if _, err := io.ReadFull(r, payload); err != nil {
			t.Fatal(err)
		}
		if header[0] == 0x88 {
			if !bytes.Equal(payload, closing) {
				t.Fatalf("close reply %x", payload)
			}
			break
		}
		if header[0] != 0x82 && header[0] != 0x89 {
			t.Fatalf("frame %x before the close reply", header[0])
		}
	}
	awaitLobby(t, host, "the browser seat freed", present(false))
	if _, err := host.State(); err != nil {
		t.Fatalf("the browser leaving ended the room: %v", err)
	}
}

func TestWebSocketConnectionBoundExpiryAndClose(t *testing.T) {
	const capacity = 8
	s := listenWebSocketTest(t, HostedConfig{InsecureLoopback: true, MaxRooms: 4, MaxConnections: capacity}, false, hostedDefaultTimeouts)
	creator, _ := dialWebSocketTest(t, "ws://"+s.Addr()+"/relay", "", 0, HostedDialOptions{InsecureLoopback: true})
	pending := make([]net.Conn, 0, capacity-1)
	for range capacity - 1 {
		pending = append(pending, rawWebSocketTest(t, s.Addr()))
	}
	awaitHostedCapacity(t, s, 1, capacity)
	extra := rawWebSocketTest(t, s.Addr())
	var one [1]byte
	if _, err := extra.Read(one[:]); err == nil || websocketTestTimeout(err) {
		t.Fatal("accepted a socket above the combined limit")
	}
	_ = extra.Close()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := hostedReadError(t, creator); err == nil || err == io.EOF {
		t.Fatalf("server close awarded completion: %v", err)
	}
	awaitHostedCapacity(t, s, 0, 0)
	for _, c := range pending {
		if _, err := c.Read(one[:]); err == nil {
			t.Fatal("Close left pending HTTP connection open")
		}
	}
	short := hostedDefaultTimeouts
	short.handshake = 60 * time.Millisecond
	s = listenWebSocketTest(t, HostedConfig{InsecureLoopback: true}, false, short)
	pendingHTTP := rawWebSocketTest(t, s.Addr())
	_, _ = io.WriteString(pendingHTTP, "GET /relay HTTP/1.1\r\nHost: ")
	if _, err := pendingHTTP.Read(one[:]); err == nil || websocketTestTimeout(err) {
		t.Fatal("incomplete HTTP headers did not expire")
	}
	awaitHostedCapacity(t, s, 0, 0)
	c := rawWebSocketTest(t, s.Addr())
	stream, err := dialWebSocketUpgrade(c, s.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := stream.Read(one[:]); err == nil || websocketTestTimeout(err) {
		t.Fatal("upgraded socket without a hello did not expire")
	}
	awaitHostedCapacity(t, s, 0, 0)
	// A complete oversized header fails before accepting a room.
	c = rawWebSocketTest(t, s.Addr())
	_, _ = io.WriteString(c, "GET /relay HTTP/1.1\r\nX-Large: "+strings.Repeat("a", websocketHeaderLimit)+"\r\n\r\n")
	if _, err := c.Read(one[:]); err == nil {
		t.Fatal("oversized headers accepted")
	}
	awaitHostedCapacity(t, s, 0, 0)
}

func TestWebSocketCloseDoesNotCompleteGame(t *testing.T) {
	s := listenWebSocketTest(t, HostedConfig{InsecureLoopback: true}, false, hostedDefaultTimeouts)
	address := "ws://" + s.Addr() + "/relay"
	c0, code := dialWebSocketTest(t, address, "", 0, HostedDialOptions{InsecureLoopback: true})
	c1, _ := dialWebSocketTest(t, address, code, 1, HostedDialOptions{InsecureLoopback: true})
	readLocalPair(t, [2]*LocalClient{c0, c1})
	stream := c0.conn.(hostedClientConn).Conn.(*websocketConn)
	if err := stream.writeControl(8, []byte{3, 232}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []*LocalClient{c0, c1} {
		if err := hostedReadError(t, c); err == nil || errors.Is(err, io.EOF) {
			t.Fatalf("WebSocket close awarded completion: %v", err)
		}
	}
	awaitHostedCapacity(t, s, 0, 0)
}

func TestWebSocketClientRefusesBadUpgradeAndHonorsCancellation(t *testing.T) {
	for _, bad := range []string{"accept", "protocol", "extension", "duplicate-accept", "oversized", "cancel"} {
		t.Run(bad, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(time.Second))
				r := bufio.NewReader(conn)
				req, err := http.ReadRequest(r)
				if err != nil {
					return
				}
				extra := ""
				accept, protocol := websocketAccept(req.Header.Get("Sec-WebSocket-Key")), websocketProtocol
				switch bad {
				case "accept":
					accept = "wrong"
				case "protocol":
					protocol = "other"
				case "extension":
					extra = "Sec-WebSocket-Extensions: permessage-deflate\r\n"
				case "duplicate-accept":
					extra = "Sec-WebSocket-Accept: duplicate\r\n"
				case "oversized":
					extra = "X-Large: " + strings.Repeat("a", websocketHeaderLimit) + "\r\n"
				case "cancel":
					_, _ = io.Copy(io.Discard, r)
					return
				}
				_, _ = fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\nSec-WebSocket-Protocol: %s\r\n%s\r\n", accept, protocol, extra)
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
			defer cancel()
			c, _, err := DialHostedWebSocket(ctx, "ws://"+listener.Addr().String()+"/relay", "", localTestHello(0), HostedDialOptions{InsecureLoopback: true})
			if err == nil {
				_ = c.Close()
				t.Fatal("accepted invalid upgrade")
			}
			if bad != "oversized" && bad != "cancel" {
				var handshake *WebSocketHandshakeError
				if !errors.As(err, &handshake) || handshake.StatusCode != http.StatusSwitchingProtocols {
					t.Fatalf("invalid 101 upgrade lost its HTTP status: %v", err)
				}
				field := map[string]string{
					"accept": "Sec-WebSocket-Accept", "duplicate-accept": "Sec-WebSocket-Accept",
					"protocol": "Sec-WebSocket-Protocol", "extension": "Sec-WebSocket-Extensions",
				}[bad]
				if !strings.Contains(err.Error(), field) {
					t.Fatalf("invalid upgrade lost the failing field %s: %v", field, err)
				}
			}
			<-finished
		})
	}
}

func TestWebSocketClientReportsHTTPFailureWithoutReadingBody(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	_ = client.SetDeadline(time.Now().Add(time.Second))
	_ = server.SetDeadline(time.Now().Add(time.Second))
	finished := make(chan error, 1)
	go func() {
		request, err := http.ReadRequest(bufio.NewReader(server))
		if err == nil {
			_ = request.Body.Close()
			// No body follows: refusal must use only the bounded header block.
			_, err = fmt.Fprintf(server, "HTTP/1.1 503 Service Unavailable\r\nContent-Length: 1000000000\r\nServer: %s\r\nCf-Ray: authored\ttrace\r\nCf-Error-Type: 503\r\nCf-Error-Origin: proxy\r\nX-Do-Orig-Status: 503\r\nSet-Cookie: private-cookie\r\nX-Private: private-header\r\n\r\n", strings.Repeat("a", 256))
		}
		finished <- err
	}()
	_, err := dialWebSocketUpgrade(client, "relay.example.invalid")
	var handshake *WebSocketHandshakeError
	if !errors.As(err, &handshake) || handshake.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("HTTP refusal lost its status: %v", err)
	}
	for _, want := range []string{"HTTP 503 Service Unavailable", "HTTP upgrade refused", `Cf-Ray="authored\ttrace"`, `Cf-Error-Type="503"`, `Cf-Error-Origin="proxy"`, `X-Do-Orig-Status="503"`, `Server="` + strings.Repeat("a", 128) + `..."`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("HTTP refusal %v, want %q", err, want)
		}
	}
	for _, forbidden := range []string{"private-cookie", "private-header", "\t", "\n", strings.Repeat("a", 129)} {
		if strings.Contains(err.Error(), forbidden) {
			t.Errorf("HTTP refusal leaked %q: %v", forbidden, err)
		}
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func websocketTestTimeout(err error) bool {
	var timeout net.Error
	return errors.As(err, &timeout) && timeout.Timeout()
}
