package relay

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/netproto"
)

func listenHostedTest(t *testing.T, config HostedConfig, deadlines hostedTimeouts) *HostedServer {
	t.Helper()
	s, err := listenHosted("127.0.0.1:0", config, deadlines)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func dialHostedTest(t *testing.T, s *HostedServer, room string, seat uint8) (*LocalClient, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, code, err := DialHosted(ctx, s.Addr(), room, localTestHello(seat), HostedDialOptions{InsecureLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	// These tests manage read deadlines themselves; one test covers the idle bound.
	c.idle = 0
	_ = c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	return c, code
}

func hostedTestPair(t *testing.T, s *HostedServer) ([2]*LocalClient, string) {
	t.Helper()
	c0, code := dialHostedTest(t, s, "", 0)
	c1, joined := dialHostedTest(t, s, code, 1)
	if joined != code {
		t.Fatalf("join changed room code: %q / %q", code, joined)
	}
	return [2]*LocalClient{c0, c1}, code
}

func awaitHostedCapacity(t *testing.T, s *HostedServer, rooms, connections int) {
	t.Helper()
	end := time.NewTimer(2 * time.Second)
	defer end.Stop()
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for {
		s.mu.Lock()
		r, c := len(s.rooms), len(s.conns)
		s.mu.Unlock()
		if r == rooms && c == connections {
			return
		}
		select {
		case <-end.C:
			t.Fatalf("capacity was not released: rooms/connections = %d/%d, want %d/%d", r, c, rooms, connections)
		case <-poll.C:
		}
	}
}

func hostedReadError(t *testing.T, c *LocalClient) error {
	t.Helper()
	for i := 0; i <= hostedMaxAhead; i++ {
		if _, err := c.ReadGrant(); err != nil {
			return err
		}
	}
	t.Fatal("received more than the outstanding grant bound while awaiting failure")
	return nil
}

func TestHostedExplicitTLSAndLoopback(t *testing.T) {
	for _, config := range []HostedConfig{{}, {TLSConfig: &tls.Config{}}, {InsecureLoopback: true, TLSConfig: &tls.Config{}}, {InsecureLoopback: true, MaxRooms: -1}, {InsecureLoopback: true, MaxRooms: 257}, {InsecureLoopback: true, MaxRooms: 16, MaxConnections: 31}, {InsecureLoopback: true, MaxConnections: 1025}} {
		if s, err := ListenHosted("127.0.0.1:0", config); err == nil {
			_ = s.Close()
			t.Fatalf("accepted invalid server configuration: %+v", config)
		}
	}
	for _, address := range []string{"localhost:39731", "0.0.0.0:39731", ":39731", "[::]:39731", "192.0.2.1:39731"} {
		if s, err := ListenHosted(address, HostedConfig{InsecureLoopback: true}); err == nil {
			_ = s.Close()
			t.Fatalf("accepted plaintext listener %q", address)
		}
		if c, _, err := DialHosted(context.Background(), address, "", localTestHello(0), HostedDialOptions{InsecureLoopback: true}); err == nil {
			_ = c.Close()
			t.Fatalf("accepted plaintext dial %q", address)
		}
	}
	for _, options := range []HostedDialOptions{{TLSConfig: &tls.Config{InsecureSkipVerify: true}}, {TLSConfig: &tls.Config{}, InsecureLoopback: true}} {
		if c, _, err := DialHosted(context.Background(), "127.0.0.1:1", "", localTestHello(0), options); err == nil || !strings.Contains(err.Error(), "TLS") {
			if c != nil {
				_ = c.Close()
			}
			t.Fatalf("invalid dial configuration: %v", err)
		}
	}
}

func hostedTestTLS(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "hosted relay test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}, &tls.Config{RootCAs: roots}
}

func TestHostedTLSTrustAndCompletion(t *testing.T) {
	serverTLS, clientTLS := hostedTestTLS(t)
	s := listenHostedTest(t, HostedConfig{TLSConfig: serverTLS}, hostedDefaultTimeouts)
	for _, config := range []*tls.Config{{RootCAs: x509.NewCertPool()}, {RootCAs: clientTLS.RootCAs, ServerName: "wrong.invalid"}} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		c, _, err := DialHosted(ctx, s.Addr(), "", localTestHello(0), HostedDialOptions{TLSConfig: config})
		cancel()
		if err == nil {
			_ = c.Close()
			t.Fatal("accepted an untrusted certificate or wrong hostname")
		}
	}
	var clients [2]*LocalClient
	room := ""
	for seat := range clients {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		var err error
		clients[seat], room, err = DialHosted(ctx, s.Addr(), room, localTestHello(uint8(seat)), HostedDialOptions{TLSConfig: clientTLS})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = clients[seat].Close() })
		_ = clients[seat].conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	}
	g := readLocalPair(t, clients)
	ackLocalPair(t, clients, g.Tick, [32]byte{}, true)
	for _, c := range clients {
		if err := hostedReadError(t, c); err != io.EOF {
			t.Fatalf("TLS completion did not flush: %v", err)
		}
	}
	awaitHostedCapacity(t, s, 0, 0)
}

func TestHostedAdmissionPreservesCreatorAndRoomIsolation(t *testing.T) {
	s := listenHostedTest(t, HostedConfig{InsecureLoopback: true, MaxRooms: 2}, hostedDefaultTimeouts)
	c0, code := dialHostedTest(t, s, "", 0)
	if !validHostedCode(code) {
		t.Fatalf("invalid invitation: %q", code)
	}
	// A joiner's requested seat is ignored: the relay assigns it.
	for _, change := range []func(*LocalHello){
		func(h *LocalHello) { h.Identity.Content[0]++ },
		func(h *LocalHello) { h.Identity.Map[0]++ },
		func(h *LocalHello) { h.Identity.Rules.Name = "strict" },
		func(h *LocalHello) { h.Identity.Mod.Version = "other" },
		func(h *LocalHello) { h.Identity.Configuration[0]++ },
		func(h *LocalHello) { h.InitialChecksum[0]++ },
	} {
		h := localTestHello(1)
		change(&h)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		c, _, err := DialHosted(ctx, s.Addr(), code, h, HostedDialOptions{InsecureLoopback: true})
		cancel()
		if err == nil || !strings.Contains(err.Error(), "hello") {
			if c != nil {
				_ = c.Close()
			}
			t.Fatalf("joining mismatch did not refuse admission: %v", err)
		}
	}
	other, otherCode := hostedTestPair(t, s)
	if otherCode == code {
		t.Fatal("two rooms share an invitation")
	}
	for _, request := range []struct {
		code string
		seat uint8
	}{{"", 0}, {otherCode, 1}, {"AAAAAAAAAA", 1}} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		c, _, err := DialHosted(ctx, s.Addr(), request.code, localTestHello(request.seat), HostedDialOptions{InsecureLoopback: true})
		cancel()
		if err == nil {
			_ = c.Close()
			t.Fatal("accepted over-capacity, full or unknown room")
		}
	}
	if _, err := c0.Submit([]byte("creator identity survived")); err != nil {
		t.Fatal(err)
	}
	// Fence the pre-barrier command without relying on network scheduling.
	rawLocalSubmit(t, c0, 3, nil)
	if _, err := c0.ReadGrant(); err == nil || !strings.Contains(err.Error(), "sequence 2") {
		t.Fatalf("pre-barrier fence: %v", err)
	}
	c1, _ := dialHostedTest(t, s, code, 1)
	g := readLocalPair(t, [2]*LocalClient{c0, c1})
	if g.Tick != 1 || len(g.Commands) != 1 || g.Commands[0].Seat != 0 || string(g.Commands[0].Payload) != "creator identity survived" {
		t.Fatalf("creator did not retain its room: %+v", g)
	}
	if g := readLocalPair(t, other); len(g.Commands) != 0 || g.Position != 0 {
		t.Fatalf("another room received its commands: %+v", g)
	}
	_ = c0.Close()
	if err := hostedReadError(t, c1); err == io.EOF || err == nil {
		t.Fatalf("a disconnect awarded normal completion: %v", err)
	}
	_ = c1.Close()
	awaitHostedCapacity(t, s, 1, 2)
	_, newCode := dialHostedTest(t, s, "", 0)
	if newCode == code {
		t.Fatal("reused the closed invitation")
	}
}

func TestHostedContinuousPacingWindowAndTerminalSurplus(t *testing.T) {
	s := listenHostedTest(t, HostedConfig{InsecureLoopback: true}, hostedDefaultTimeouts)
	clients, _ := hostedTestPair(t, s)
	first := readLocalPair(t, clients)
	start := time.Now()
	for tick := uint32(2); tick <= hostedMaxAhead; tick++ {
		if g := readLocalPair(t, clients); g.Tick != tick {
			t.Fatalf("continuous grants skipped tick %d: %+v", tick, g)
		}
	}
	if elapsed := time.Since(start); elapsed < (hostedMaxAhead-2)*localTickInterval {
		t.Fatalf("grants exceeded normal speed: %v", elapsed)
	}
	// There are no ACKs: even the next complete interval cannot issue tick 31.
	if err := clients[0].conn.SetReadDeadline(time.Now().Add(2 * localTickInterval)); err != nil {
		t.Fatal(err)
	}
	if _, err := clients[0].ReadGrant(); err == nil {
		t.Fatal("granted more than 30 ticks ahead")
	} else {
		var netErr net.Error
		if !errors.As(err, &netErr) || !netErr.Timeout() {
			t.Fatalf("window ended the connection: %v", err)
		}
	}
	_ = clients[0].conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	ackLocalPair(t, clients, first.Tick, [32]byte{}, false)
	if g, err := clients[0].ReadGrant(); err != nil || g.Tick != 31 {
		t.Fatalf("execution progress did not release the window: %+v %v", g, err)
	}
	// Both replicas discover shared termination at tick two. Tick 31 was
	// already sent; its surplus must precede explicit completion, never another
	// required simulation step or ACK.
	ackLocalPair(t, clients, 2, [32]byte{}, true)
	if g, err := clients[1].ReadGrant(); err != nil || g.Tick != 31 {
		t.Fatalf("buffered surplus grant was lost: %+v %v", g, err)
	}
	for _, c := range clients {
		if err := hostedReadError(t, c); err != io.EOF {
			t.Fatalf("terminal surplus handling: %v", err)
		}
	}
	awaitHostedCapacity(t, s, 0, 0)
}

func TestHostedDelayedChecksumFailureAndTerminalDisagreement(t *testing.T) {
	t.Run("delayed periodic checksum", func(t *testing.T) {
		s := listenHostedTest(t, HostedConfig{InsecureLoopback: true}, hostedDefaultTimeouts)
		clients, _ := hostedTestPair(t, s)
		for tick := uint32(1); tick <= 30; tick++ {
			readLocalPair(t, clients)
			if err := clients[0].Acknowledge(tick, [32]byte{1}, false, false); err != nil {
				t.Fatal(err)
			}
		}
		// The slower peer reports tick 30 after the first peer's later grants.
		for tick := uint32(1); tick <= 30; tick++ {
			if err := clients[1].Acknowledge(tick, [32]byte{2}, false, false); err != nil {
				t.Fatal(err)
			}
		}
		for _, c := range clients {
			if err := hostedReadError(t, c); err == nil || !strings.Contains(err.Error(), "tick 30 checksum") {
				t.Fatalf("delayed checksum mismatch: %v", err)
			}
		}
	})
	for _, terminalFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "nonterminal first", true: "terminal first"}[terminalFirst], func(t *testing.T) {
			s := listenHostedTest(t, HostedConfig{InsecureLoopback: true}, hostedDefaultTimeouts)
			clients, _ := hostedTestPair(t, s)
			g := readLocalPair(t, clients)
			if err := clients[0].Acknowledge(g.Tick, [32]byte{}, terminalFirst, false); err != nil {
				t.Fatal(err)
			}
			if err := clients[1].Acknowledge(g.Tick, [32]byte{}, !terminalFirst, false); err != nil {
				t.Fatal(err)
			}
			for _, c := range clients {
				if err := hostedReadError(t, c); err == nil || !strings.Contains(err.Error(), "terminal agreement") {
					t.Fatalf("terminal disagreement: %v", err)
				}
			}
		})
	}
}

func TestHostedHandshakeBoundsExpiryProgressAndClose(t *testing.T) {
	timeouts := hostedDefaultTimeouts
	timeouts.handshake, timeouts.waiting, timeouts.progress = 100*time.Millisecond, 100*time.Millisecond, 100*time.Millisecond
	s := listenHostedTest(t, HostedConfig{InsecureLoopback: true, MaxRooms: 1}, timeouts)
	conn, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("silent handshake did not expire")
	}
	awaitHostedCapacity(t, s, 0, 0)
	for _, malformed := range [][]byte{{0}, {0x81, 0}, {0xff, 0xff, 0xff, 0xff, 0x1f}, {0x81, 0x80, 1}} {
		conn, err := net.Dial("tcp", s.Addr())
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		_, _ = conn.Write(malformed)
		if _, err := conn.Read(make([]byte, 1)); err == nil {
			t.Fatal("malformed envelope accepted")
		}
		_ = conn.Close()
	}
	awaitHostedCapacity(t, s, 0, 0)
	c0, _ := dialHostedTest(t, s, "", 0)
	if err := hostedReadError(t, c0); err == nil || !strings.Contains(err.Error(), "room wait") {
		t.Fatalf("waiting room did not expire: %v", err)
	}
	awaitHostedCapacity(t, s, 0, 0)
	clients, _ := hostedTestPair(t, s)
	readLocalPair(t, clients) // both direct readers consume Started
	if err := hostedReadError(t, clients[0]); err == nil || !strings.Contains(err.Error(), "execution progress") {
		t.Fatalf("stalled room did not expire: %v", err)
	}
	awaitHostedCapacity(t, s, 0, 0)
	c0, _ = dialHostedTest(t, s, "", 0)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := hostedReadError(t, c0); err == nil || err == io.EOF {
		t.Fatalf("server close looked like normal completion: %v", err)
	}
	awaitHostedCapacity(t, s, 0, 0)
}

func TestHostedMalformedMessagesAndPendingBounds(t *testing.T) {
	var gapAck netproto.Writer
	gapAck.U8(localAckMessage)
	gapAck.U32(2)
	gapAck.Bool(false)
	for name, body := range map[string][]byte{
		"ACK ahead":         gapAck.Bytes(),
		"invalid bool":      {localAckMessage, 1, 2},
		"unknown message":   {255},
		"trailing bytes":    {localSubmitMessage, 1, 0, 1},
		"oversized command": {localSubmitMessage, 1, 0x81, 0x80, 0x80, 2},
		"oversized frame":   append([]byte{localSubmitMessage}, make([]byte, hostedMaxClientFrame)...),
	} {
		t.Run(name, func(t *testing.T) {
			s := listenHostedTest(t, HostedConfig{InsecureLoopback: true}, hostedDefaultTimeouts)
			c, _ := dialHostedTest(t, s, "", 0)
			if err := writeLocalFrame(c.conn, body); err != nil {
				// The relay refuses an oversized frame from its length
				// prefix and may close before the rest is written.
				if name != "oversized frame" {
					t.Fatal(err)
				}
				awaitHostedCapacity(t, s, 0, 0)
				return
			}
			if err := hostedReadError(t, c); err == nil || err == io.EOF {
				t.Fatalf("malformed message accepted: %v", err)
			}
			awaitHostedCapacity(t, s, 0, 0)
		})
	}
	for _, byteLimit := range []bool{false, true} {
		s := listenHostedTest(t, HostedConfig{InsecureLoopback: true}, hostedDefaultTimeouts)
		c, code := dialHostedTest(t, s, "", 0)
		rawLocalSubmit(t, c, 1, []byte("first"))
		rawLocalSubmit(t, c, 1, []byte("duplicate"))
		rawLocalSubmit(t, c, 3, nil)
		if _, err := c.ReadGrant(); err == nil || !strings.Contains(err.Error(), "sequence 2") {
			t.Fatalf("sequence gap not recoverable: %v", err)
		}
		if byteLimit {
			for seq := uint64(2); seq <= 3; seq++ {
				rawLocalSubmit(t, c, seq, make([]byte, hostedMaxPendingBytes/2+1))
			}
		} else {
			for seq := uint64(2); seq <= 65; seq++ {
				rawLocalSubmit(t, c, seq, nil)
			}
		}
		if err := hostedReadError(t, c); err == nil || !strings.Contains(err.Error(), "pending commands") {
			t.Fatalf("room %s exceeded pending bound: %v", code, err)
		}
		awaitHostedCapacity(t, s, 0, 0)
	}
}

func TestHostedConnectionCapacityAndCanceledHandshake(t *testing.T) {
	// The production cap is 512; a small one exercises the same accept path
	// without exhausting a developer machine's descriptor limit.
	const capacity = 8
	s := listenHostedTest(t, HostedConfig{InsecureLoopback: true, MaxRooms: 4, MaxConnections: capacity}, hostedDefaultTimeouts)
	var conns []net.Conn
	defer func() {
		for _, conn := range conns {
			_ = conn.Close()
		}
	}()
	for i := 0; i < capacity; i++ {
		conn, err := net.DialTimeout("tcp", s.Addr(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, conn)
	}
	awaitHostedCapacity(t, s, 0, capacity)
	extra, err := net.DialTimeout("tcp", s.Addr(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer extra.Close()
	_ = extra.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := extra.Read(make([]byte, 1)); err == nil {
		t.Fatal("admitted a connection over capacity")
	}
	for _, conn := range conns {
		_ = conn.Close()
	}
	awaitHostedCapacity(t, s, 0, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if c, _, err := DialHosted(ctx, s.Addr(), "", localTestHello(0), HostedDialOptions{InsecureLoopback: true}); !errors.Is(err, context.Canceled) {
		if c != nil {
			_ = c.Close()
		}
		t.Fatalf("canceled handshake: %v", err)
	}
}

func twoSlotProgress() hostedProgress {
	p := hostedProgress{n: 2}
	p.active[0], p.active[1] = true, true
	return p
}

func TestHostedProgressRetainsSameTickReports(t *testing.T) {
	progress := twoSlotProgress()
	for tick := uint32(1); tick <= 89; tick++ {
		if tick > hostedMaxAhead {
			slow := tick - hostedMaxAhead
			if _, err := progress.acknowledge(1, tick-1, hostedAck{tick: slow, check: [32]byte{byte(slow)}}, false, time.Now()); err != nil {
				t.Fatalf("same-tick report fell out of the bounded history: %v", err)
			}
		}
		if _, err := progress.acknowledge(0, tick, hostedAck{tick: tick, check: [32]byte{byte(tick)}}, false, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := progress.acknowledge(1, 89, hostedAck{tick: 60, check: [32]byte{1}}, false, time.Now()); err == nil || !strings.Contains(err.Error(), "tick 60 checksum") {
		t.Fatalf("wrapped checksum history compared different ticks: %v", err)
	}
	for _, sequence := range [][]uint32{{0}, {2}, {1, 1}, {1, 3}, {1, 2, 3}} {
		p := twoSlotProgress()
		var err error
		for _, tick := range sequence {
			_, err = p.acknowledge(0, 2, hostedAck{tick: tick}, false, time.Now())
			if err != nil {
				break
			}
		}
		if err == nil {
			t.Fatalf("accepted ACK sequence %v within two grants", sequence)
		}
	}
}

func TestHostedWriterBudgetIncludesInflightAndFlushes(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	p := newHostedPeer(a, LocalHello{}, time.Second)
	p.room = &hostedRoom{server: &HostedServer{done: make(chan struct{})}, events: make(chan hostedEvent, 1), done: make(chan struct{})}
	// Leave one byte of budget: a one-byte frame with its header needs two.
	payload := make([]byte, hostedMaxQueuedBytes-1-netproto.UvarintLen(hostedMaxQueuedBytes))
	if err := p.enqueue(payload, false); err != nil {
		t.Fatal(err)
	}
	go p.write()
	if err := p.enqueue([]byte{1}, false); err == nil {
		t.Fatal("in-flight frame escaped the byte budget")
	}
	_ = a.Close()
	select {
	case <-p.written:
	case <-time.After(time.Second):
		t.Fatal("blocked writer did not exit")
	}

	a, b = net.Pipe()
	defer b.Close()
	p = newHostedPeer(a, LocalHello{}, time.Second)
	if err := p.enqueue([]byte{localDoneMessage}, true); err != nil {
		t.Fatal(err)
	}
	go p.write()
	// The writer is blocked until the terminal frame is consumed; closing the
	// socket before then would turn normal completion into unexpected EOF.
	select {
	case <-p.written:
		t.Fatal("closed the socket before delivering completion")
	default:
	}
	body, err := readLocalFrame(b, localMaxGrantFrame)
	if err != nil || !bytes.Equal(body, []byte{localDoneMessage}) {
		t.Fatalf("completion flush: %v %v", body, err)
	}
	<-p.written

	a, b = net.Pipe()
	defer a.Close()
	defer b.Close()
	p = newHostedPeer(a, LocalHello{}, time.Second)
	for i := 0; i < hostedMaxQueuedFrames; i++ {
		if err := p.enqueue([]byte{1}, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.enqueue([]byte{1}, false); err == nil {
		t.Fatal("tiny frames escaped the queue count limit")
	}
}

func TestHostedSlowReaderDoesNotBlockOtherRoom(t *testing.T) {
	timeouts := hostedDefaultTimeouts
	timeouts.write = 150 * time.Millisecond
	s := listenHostedTest(t, HostedConfig{InsecureLoopback: true}, timeouts)
	c0, code := dialHostedTest(t, s, "", 0)
	// Keep the peer's OS window small so the maximum grant exercises our writer
	// deadline, independent of the platform's default socket buffer sizes.
	rawClient := c0.conn.(hostedClientConn).Conn.(*net.TCPConn)
	_ = rawClient.SetReadBuffer(1024)
	s.mu.Lock()
	for raw := range s.conns {
		if raw.RemoteAddr().String() == rawClient.LocalAddr().String() {
			_ = raw.(*net.TCPConn).SetWriteBuffer(1024)
		}
	}
	s.mu.Unlock()
	if _, err := c0.Submit(make([]byte, hostedMaxCommandBytes)); err != nil {
		t.Fatal(err)
	}
	rawLocalSubmit(t, c0, 3, nil)
	if _, err := c0.ReadGrant(); err == nil || !strings.Contains(err.Error(), "sequence 2") {
		t.Fatalf("pending grant fence: %v", err)
	}
	c1, _ := dialHostedTest(t, s, code, 1)
	// Consume only through Started, then stop reading the large grant. The
	// other direct reader has not started yet, so the barrier keeps this
	// fixture's receive window empty until both markers arrive.
	for {
		body, err := c0.readFrame(localMaxGrantFrame)
		if err != nil {
			t.Fatal(err)
		}
		if body[0] == hostedStartedMessage {
			c0.started.Store(true)
			break
		}
	}
	if err := c0.OpeningReady(); err != nil {
		t.Fatal(err)
	}
	other, _ := hostedTestPair(t, s)
	g := readLocalPair(t, other)
	ackLocalPair(t, other, g.Tick, [32]byte{}, true)
	for _, c := range other {
		if err := hostedReadError(t, c); err != io.EOF {
			t.Fatalf("a blocked writer stalled another room: %v", err)
		}
	}
	if err := hostedReadError(t, c1); err == nil || err == io.EOF {
		t.Fatalf("slow reader room did not fail: %v", err)
	}
	awaitHostedCapacity(t, s, 0, 0)
}

func TestHostedVersionAndHelloFraming(t *testing.T) {
	body, err := encodeHostedHello("", hostedAutoStart, 4, localTestHello(0), []byte("configuration"))
	if err != nil {
		t.Fatal(err)
	}
	if h, err := decodeHostedHello(body); err != nil || h.version != hostedVersion || h.code != "" || h.flags != hostedAutoStart || h.size != 4 || string(h.config) != "configuration" {
		t.Fatalf("creator hello round trip: %+v %v", h, err)
	}
	for _, mutate := range []func([]byte) []byte{
		func(b []byte) []byte { b[0] = localHelloMessage; return b },
		func(b []byte) []byte { b[1] = hostedVersion + 1; return b },
		func(b []byte) []byte { return append(b, 0) },
		func(b []byte) []byte { return b[:len(b)-1] },
	} {
		if _, err := decodeHostedHello(mutate(bytes.Clone(body))); err == nil {
			t.Fatal("accepted malformed versioned hello")
		}
	}
	// A version-1 client is told the served versions and its own; a
	// version-5 hello is still served, in its own version (§12.2).
	old := bytes.Clone(body)
	old[1] = 1
	if _, err := decodeHostedHello(old); err == nil || !strings.Contains(err.Error(), "version 5, 6 or 7; this client sent version 1") {
		t.Fatalf("version mismatch: %v", err)
	}
	old[1] = 5
	if h, err := decodeHostedHello(old); err != nil || h.version != 5 {
		t.Fatalf("version-5 hello: %+v %v", h, err)
	}
	for _, code := range []string{"SHORT", "AAAAAAA", "AAAAA0", "aaaaaa", "AAA AA"} {
		if _, err := encodeHostedHello(code, 0, 0, localTestHello(1), nil); err == nil {
			t.Fatalf("accepted invalid invitation %q", code)
		}
	}
	// A joiner carries no creator flags, size or configuration; a creator
	// names a size of 2 to 10 seats.
	if _, err := encodeHostedHello("CFHJKM", hostedAutoStart, 0, localTestHello(1), nil); err == nil {
		t.Fatal("joiner sent creator flags")
	}
	if _, err := encodeHostedHello("CFHJKM", 0, 2, localTestHello(1), nil); err == nil {
		t.Fatal("joiner sent a room size")
	}
	if _, err := encodeHostedHello("CFHJKM", 0, 0, localTestHello(1), []byte{1}); err == nil {
		t.Fatal("joiner sent a configuration")
	}
	for _, size := range []int{0, 1, 11} {
		if _, err := encodeHostedHello("", 0, size, localTestHello(0), []byte{1}); err == nil {
			t.Fatalf("created a room of %d seats", size)
		}
	}
	if code, ok := NormalizeRoomCode(" cfh-jk5 "); !ok || code != "CFHJK5" {
		t.Fatalf("typed code: %q %v", code, ok)
	}
}

// Codes leave out every character a player could take for another, and a
// typed lookalike letter reads as the digit it resembles (§16.5.1).
func TestHostedRoomCodesAreUnambiguous(t *testing.T) {
	for _, r := range "01OILDQSZBGAEUY" {
		if strings.ContainsRune(hostedCodeAlphabet, r) {
			t.Fatalf("alphabet holds %q", r)
		}
	}
	if code, ok := NormalizeRoomCode("s z-b g c f"); !ok || code != "5286CF" {
		t.Fatalf("lookalike letters: %q %v", code, ok)
	}
	for _, typed := range []string{"CFHJK0", "CFHJKO", "CFHJK1", "CFHJKI", "CFHJKL", "CFHJKD", "CFHJK", "CFHJKMN"} {
		if code, ok := NormalizeRoomCode(typed); ok {
			t.Fatalf("accepted %q as %q", typed, code)
		}
	}
	seen := map[byte]bool{}
	for range 48 {
		code, err := newHostedCode()
		if err != nil {
			t.Fatal(err)
		}
		if !validHostedCode(code) {
			t.Fatalf("generated %q", code)
		}
		for i := range code {
			seen[code[i]] = true
		}
	}
	// 288 draws reach all 21 symbols except with probability below 1e-17.
	if len(seen) != len(hostedCodeAlphabet) {
		t.Fatalf("generated %d of %d symbols", len(seen), len(hostedCodeAlphabet))
	}
}

func TestHostedCancellationClosesPendingHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	closed := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			closed <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		_, err = readLocalFrame(conn, localMaxHelloBytes+32)
		if err == nil {
			_, err = conn.Read(make([]byte, 1))
		}
		closed <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	c, _, err := DialHosted(ctx, listener.Addr().String(), "", localTestHello(0), HostedDialOptions{InsecureLoopback: true})
	if c != nil {
		_ = c.Close()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting handshake did not honor cancellation: %v", err)
	}
	if err := <-closed; err != io.EOF {
		t.Fatalf("canceled handshake retained its socket: %v", err)
	}
}

func TestHostedClientIdleBoundAfterStarted(t *testing.T) {
	// Before the battle a creator may wait for its peer; the relay's own
	// waiting deadline reports that, not the client's idle bound.
	timeouts := hostedDefaultTimeouts
	timeouts.waiting = 200 * time.Millisecond
	s := listenHostedTest(t, HostedConfig{InsecureLoopback: true}, timeouts)
	creator, _ := dialHostedTest(t, s, "", 0)
	creator.idle = 20 * time.Millisecond
	if _, err := creator.ReadGrant(); err == nil || !strings.Contains(err.Error(), "room wait") {
		t.Fatalf("waiting creator: %v", err)
	}
	// Once grants flow, silence ends the client promptly. Without ACKs the
	// relay stops at its 30-tick lead and then waits ten seconds to abort.
	s = listenHostedTest(t, HostedConfig{InsecureLoopback: true}, hostedDefaultTimeouts)
	clients, _ := hostedTestPair(t, s)
	clients[0].idle = 150 * time.Millisecond
	readLocalPair(t, clients)
	for tick := uint32(2); tick <= hostedMaxAhead; tick++ {
		if g, err := clients[0].ReadGrant(); err != nil || g.Tick != tick {
			t.Fatalf("grant %d: %+v %v", tick, g, err)
		}
	}
	start := time.Now()
	_, err := clients[0].ReadGrant()
	if err == nil || !strings.Contains(err.Error(), "relay traffic within 150ms") {
		t.Fatalf("silent route: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("idle bound took %v", elapsed)
	}
}

type flakyHostedListener struct {
	net.Listener
	failures int
}

func (l *flakyHostedListener) Accept() (net.Conn, error) {
	if l.failures > 0 {
		l.failures--
		return nil, &net.OpError{Op: "accept", Net: "tcp", Err: errors.New("too many open files")}
	}
	return l.Listener.Accept()
}

func TestHostedAcceptSurvivesTransientErrors(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// Descriptor exhaustion must not leave a live process accepting nothing.
	s := &HostedServer{listener: &flakyHostedListener{Listener: inner, failures: 3}, config: HostedConfig{InsecureLoopback: true, MaxRooms: 16, MaxConnections: 32}, timeouts: hostedDefaultTimeouts, done: make(chan struct{}), rooms: make(map[string]*hostedRoom), conns: make(map[net.Conn]struct{})}
	s.wg.Add(1)
	go s.accept()
	t.Cleanup(func() { _ = s.Close() })
	clients, _ := hostedTestPair(t, s)
	if g := readLocalPair(t, clients); g.Tick != 1 {
		t.Fatalf("first grant after accept errors: %+v", g)
	}
}
