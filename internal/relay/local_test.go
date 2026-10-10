package relay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/netproto"
)

func localTestHello(seat uint8) LocalHello {
	return LocalHello{
		Seat: seat,
		Identity: netproto.Identity{
			Protocol: netproto.CommandSchemaVersion,
			Build:    [32]byte{1}, Content: [32]byte{2}, Map: [32]byte{3},
			Rules:         netproto.RuleIdentity{Name: "modern", Base: 2, Community: [32]byte{4}},
			Configuration: [32]byte{5},
		},
		InitialChecksum: [32]byte{6},
	}
}

func listenLocalTest(t *testing.T) *LocalRelay {
	t.Helper()
	r, err := ListenLocal("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func dialLocalTest(t *testing.T, address string, h LocalHello) *LocalClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := DialLocal(ctx, address, h)
	if err != nil {
		t.Fatal(err)
	}
	// A failed assertion must not strand the suite in a socket call.
	if err := c.conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func localTestPair(t *testing.T) (*LocalRelay, [2]*LocalClient) {
	t.Helper()
	r := listenLocalTest(t)
	return r, [2]*LocalClient{dialLocalTest(t, r.Addr(), localTestHello(0)), dialLocalTest(t, r.Addr(), localTestHello(1))}
}

func readLocalPair(t *testing.T, clients [2]*LocalClient) LocalGrant {
	t.Helper()
	// Hosted direct streams must both consume Started before either receives
	// its first grant; each real seat has its own reader.
	type result struct {
		grant LocalGrant
		err   error
	}
	var results [2]chan result
	for i, c := range clients {
		results[i] = make(chan result, 1)
		go func() {
			g, err := c.ReadGrant()
			results[i] <- result{g, err}
		}()
	}
	a, b := <-results[0], <-results[1]
	if a.err != nil || b.err != nil {
		t.Fatalf("grant reads: %v / %v", a.err, b.err)
	}
	if !reflect.DeepEqual(a.grant, b.grant) {
		t.Fatalf("replicas got different sealed grants: %+v / %+v", a.grant, b.grant)
	}
	return a.grant
}

func ackLocalPair(t *testing.T, clients [2]*LocalClient, tick uint32, check [32]byte, end bool) {
	t.Helper()
	for _, c := range clients {
		if err := c.Acknowledge(tick, check, end, false); err != nil {
			t.Fatal(err)
		}
	}
}

func rawLocalSubmit(t *testing.T, c *LocalClient, sequence uint64, payload []byte) {
	t.Helper()
	var w netproto.Writer
	w.U8(localSubmitMessage)
	w.U64(sequence)
	w.U32(uint32(len(payload)))
	w.Raw(payload)
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := writeLocalFrame(c.conn, w.Bytes()); err != nil {
		t.Fatal(err)
	}
}

func TestLocalOnlyNumericLoopback(t *testing.T) {
	for _, address := range []string{"localhost:1234", ":1234", "0.0.0.0:1234", "192.0.2.1:1234", "[::]:1234", "[::1%lo0]:1234", "127.0.0.1"} {
		if r, err := ListenLocal(address); err == nil {
			_ = r.Close()
			t.Fatalf("accepted listener %q", address)
		}
		if c, err := DialLocal(context.Background(), address, localTestHello(0)); err == nil {
			_ = c.Close()
			t.Fatalf("accepted dial %q", address)
		}
	}
	if err := localAddress("[::1]:1234", false); err != nil {
		t.Fatal(err)
	}
	if _, err := DialLocal(context.Background(), "127.0.0.1:0", localTestHello(0)); err == nil {
		t.Fatal("accepted zero destination port")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := listenLocalTest(t)
	if _, err := DialLocal(ctx, r.Addr(), localTestHello(0)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled dial: %v", err)
	}
}

func TestLocalHelloBarrierAndOpaqueSeatCommands(t *testing.T) {
	r := listenLocalTest(t)
	c1 := dialLocalTest(t, r.Addr(), localTestHello(1)) // Opposite connection/seat order.
	if seq, err := c1.Submit([]byte{0, 255, 0, 1}); err != nil || seq != 1 {
		t.Fatalf("submit: %d %v", seq, err)
	}
	// A recoverable refusal fences the earlier submit before seat two arrives.
	rawLocalSubmit(t, c1, 3, nil)
	if _, err := c1.ReadGrant(); err == nil || !strings.Contains(err.Error(), "sequence 2") {
		t.Fatalf("submit fence: %v", err)
	}
	read := make(chan LocalGrant, 1)
	readErr := make(chan error, 1)
	go func() {
		g, err := c1.ReadGrant()
		if err != nil {
			readErr <- err
		} else {
			read <- g
		}
	}()
	select {
	case <-read:
		t.Fatal("granted before the other hello")
	case err := <-readErr:
		t.Fatal(err)
	case <-time.After(20 * time.Millisecond):
	}
	c0 := dialLocalTest(t, r.Addr(), localTestHello(0))
	g0, err := c0.ReadGrant()
	if err != nil {
		t.Fatal(err)
	}
	var g1 LocalGrant
	select {
	case g1 = <-read:
	case err := <-readErr:
		t.Fatal(err)
	case <-time.After(3 * time.Second):
		t.Fatal("ready barrier did not release")
	}
	if !reflect.DeepEqual(g0, g1) || g0.Tick != 1 || g0.Position != 1 || len(g0.Commands) != 1 {
		t.Fatalf("first grants: %+v / %+v", g0, g1)
	}
	if c := g0.Commands[0]; c.Seat != 1 || c.Sequence != 1 || c.Position != 1 || !bytes.Equal(c.Payload, []byte{0, 255, 0, 1}) {
		t.Fatalf("opaque command: %+v", c)
	}
	clients := [2]*LocalClient{c0, c1}
	// With the first grant sealed, these commands can only enter tick two.
	for i := 0; i < 3; i++ {
		if _, err := c0.Submit([]byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
		if _, err := c1.Submit([]byte{byte(i + 10)}); err != nil {
			t.Fatal(err)
		}
	}
	ackLocalPair(t, clients, 1, [32]byte{}, false)
	g := readLocalPair(t, clients)
	if g.Tick != 2 || g.Position != 7 || len(g.Commands) != 6 {
		t.Fatalf("second grant: %+v", g)
	}
	counts := [2]uint64{0, 1}
	for i, command := range g.Commands {
		counts[command.Seat]++
		if command.Position != uint64(i+2) || command.Sequence != counts[command.Seat] {
			t.Fatalf("command order: %+v", g.Commands)
		}
	}
	ackLocalPair(t, clients, 2, [32]byte{}, true)
	for _, c := range clients {
		if _, err := c.ReadGrant(); !errors.Is(err, io.EOF) {
			t.Fatalf("both-ended completion: %v", err)
		}
	}
}

func TestLocalHelloComparesEveryIdentity(t *testing.T) {
	cases := []struct {
		name   string
		change func(*LocalHello)
	}{
		{"content", func(h *LocalHello) { h.Identity.Content[0]++ }},
		{"map", func(h *LocalHello) { h.Identity.Map[0]++ }},
		{"rules", func(h *LocalHello) { h.Identity.Rules.Name = "strict" }},
		{"rule-base", func(h *LocalHello) { h.Identity.Rules.Base = 1 }},
		{"community", func(h *LocalHello) { h.Identity.Rules.Community[0]++ }},
		{"mod-id", func(h *LocalHello) { h.Identity.Mod.ID = "other" }},
		{"mod-version", func(h *LocalHello) { h.Identity.Mod.Version = "1" }},
		{"mod-archive", func(h *LocalHello) { h.Identity.Mod.Archive[0]++ }},
		{"configuration", func(h *LocalHello) { h.Identity.Configuration[0]++ }},
		{"initial checksum", func(h *LocalHello) { h.InitialChecksum[0]++ }},
		{"duplicate-seat", func(h *LocalHello) { h.Seat = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := listenLocalTest(t)
			c0 := dialLocalTest(t, r.Addr(), localTestHello(0))
			h := localTestHello(1)
			tc.change(&h)
			c1 := dialLocalTest(t, r.Addr(), h)
			for _, c := range []*LocalClient{c0, c1} {
				if _, err := c.ReadGrant(); err == nil || !strings.Contains(err.Error(), "hello") {
					t.Fatalf("mismatch did not refuse entry: %v", err)
				}
			}
		})
	}
	for _, h := range []LocalHello{{Seat: HostedMaxSeats, Identity: localTestHello(0).Identity}, {Seat: 0, Identity: netproto.Identity{Protocol: 2}}} {
		if _, err := encodeLocalHello(h); err == nil {
			t.Fatal("accepted invalid hello")
		}
	}
}

func TestLocalDuplicateIgnoredAndGapRecoverable(t *testing.T) {
	r := listenLocalTest(t)
	c0 := dialLocalTest(t, r.Addr(), localTestHello(0))
	rawLocalSubmit(t, c0, 1, []byte("accepted"))
	rawLocalSubmit(t, c0, 1, []byte("different duplicate"))
	rawLocalSubmit(t, c0, 3, []byte("gap"))
	if _, err := c0.ReadGrant(); err == nil || !strings.Contains(err.Error(), "expected sequence 2") {
		t.Fatalf("gap refusal: %v", err)
	}
	rawLocalSubmit(t, c0, 2, []byte("missing"))
	rawLocalSubmit(t, c0, 4, nil)
	if _, err := c0.ReadGrant(); err == nil || !strings.Contains(err.Error(), "sequence 3") {
		t.Fatalf("missing-sequence fence: %v", err)
	}
	c1 := dialLocalTest(t, r.Addr(), localTestHello(1))
	g := readLocalPair(t, [2]*LocalClient{c0, c1})
	if g.Position != 2 || len(g.Commands) != 2 || string(g.Commands[0].Payload) != "accepted" || string(g.Commands[1].Payload) != "missing" {
		t.Fatalf("sequence policy changed stream: %+v", g)
	}
}

func TestLocalAckBarrierAndNormalPacing(t *testing.T) {
	_, clients := localTestPair(t)
	g := readLocalPair(t, clients)
	if err := clients[0].Acknowledge(g.Tick, [32]byte{1}, false, false); err != nil {
		t.Fatal(err)
	}
	next := make(chan error, 1)
	go func() { _, err := clients[0].ReadGrant(); next <- err }()
	select {
	case err := <-next:
		t.Fatalf("advanced without second ack: %v", err)
	case <-time.After(2 * localTickInterval):
	}
	if err := clients[1].Acknowledge(g.Tick, [32]byte{2}, false, false); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-next:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("both acknowledgments did not release next grant")
	}
	if _, err := clients[1].ReadGrant(); err != nil {
		t.Fatal(err)
	}
	// Different non-periodic checksums above are intentionally not transmitted.
	// With immediate ACKs, several grants still take their normal-speed span.
	start := time.Now()
	for tick := uint32(2); tick < 6; tick++ {
		ackLocalPair(t, clients, tick, [32]byte{}, false)
		g = readLocalPair(t, clients)
		if g.Tick != tick+1 {
			t.Fatal("skipped a tick")
		}
	}
	// Delivery of tick two can lag its seal. Three further intervals are a
	// conservative lower bound for four newly sealed ticks, with no upper bound.
	if elapsed := time.Since(start); elapsed < 3*localTickInterval {
		t.Fatalf("four ticks were sealed too quickly: %v", elapsed)
	}
}

func TestLocalPeriodicChecksumAndBothEnded(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		_, clients := localTestPair(t)
		for tick := uint32(1); tick <= 30; tick++ {
			g := readLocalPair(t, clients)
			if g.Tick != tick {
				t.Fatalf("tick = %d, want %d", g.Tick, tick)
			}
			check := [32]byte{9}
			if err := clients[0].Acknowledge(tick, check, tick == 30, false); err != nil {
				t.Fatal(err)
			}
			if mismatch && tick == 30 {
				check[0]++
			}
			if err := clients[1].Acknowledge(tick, check, tick == 30, false); err != nil {
				t.Fatal(err)
			}
		}
		for _, c := range clients {
			_, err := c.ReadGrant()
			if mismatch {
				if err == nil || !strings.Contains(err.Error(), "tick 30 checksum") {
					t.Fatalf("periodic mismatch: %v", err)
				}
			} else if !errors.Is(err, io.EOF) {
				t.Fatalf("terminal stream: %v", err)
			}
		}
	}
}

func TestLocalOneEndedSeatKeepsRunning(t *testing.T) {
	_, clients := localTestPair(t)
	g := readLocalPair(t, clients)
	if err := clients[0].Acknowledge(g.Tick, [32]byte{}, true, false); err != nil {
		t.Fatal(err)
	}
	if err := clients[1].Acknowledge(g.Tick, [32]byte{}, false, false); err != nil {
		t.Fatal(err)
	}
	g = readLocalPair(t, clients)
	if g.Tick != 2 {
		t.Fatal("one terminal seat stopped the other")
	}
	ackLocalPair(t, clients, g.Tick, [32]byte{}, true)
	for _, c := range clients {
		if _, err := c.ReadGrant(); !errors.Is(err, io.EOF) {
			t.Fatalf("both-ended completion: %v", err)
		}
	}
}

func TestLocalRejectsWrongAndDuplicateAcks(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		_, clients := localTestPair(t)
		g := readLocalPair(t, clients)
		tick := g.Tick + 1
		if duplicate {
			tick = g.Tick
			if err := clients[0].Acknowledge(tick, [32]byte{}, false, false); err != nil {
				t.Fatal(err)
			}
		}
		if err := clients[0].Acknowledge(tick, [32]byte{}, false, false); err != nil {
			t.Fatal(err)
		}
		for _, c := range clients {
			if _, err := c.ReadGrant(); err == nil || !strings.Contains(err.Error(), "acknowledgment tick") {
				t.Fatalf("invalid ACK accepted: %v", err)
			}
		}
	}
}

func TestLocalPendingLimitsAbort(t *testing.T) {
	for _, bytesLimit := range []bool{false, true} {
		r := listenLocalTest(t)
		c := dialLocalTest(t, r.Addr(), localTestHello(0))
		if bytesLimit {
			payload := make([]byte, netproto.MaxCommandBytes)
			for i := 0; i < 2; i++ {
				if _, err := c.Submit(payload); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := c.Submit([]byte{1}); err != nil {
				t.Fatal(err)
			}
		} else {
			for i := 0; i <= localMaxCommands; i++ {
				if _, err := c.Submit(nil); err != nil {
					t.Fatal(err)
				}
			}
		}
		if _, err := c.ReadGrant(); err == nil || !strings.Contains(err.Error(), "pending commands") {
			t.Fatalf("pending limit: %v", err)
		}
	}
	r := listenLocalTest(t)
	c := dialLocalTest(t, r.Addr(), localTestHello(0))
	if _, err := c.Submit(make([]byte, netproto.MaxCommandBytes+1)); err == nil || c.sequence != 0 {
		t.Fatal("oversized local command advanced sequence")
	}
}

func TestLocalGrantAtPendingByteLimit(t *testing.T) {
	r := listenLocalTest(t)
	c0 := dialLocalTest(t, r.Addr(), localTestHello(0))
	payload := bytes.Repeat([]byte{0xa5}, netproto.MaxCommandBytes)
	for i := 0; i < 2; i++ {
		if _, err := c0.Submit(payload); err != nil {
			t.Fatal(err)
		}
	}
	rawLocalSubmit(t, c0, 4, nil)
	if _, err := c0.ReadGrant(); err == nil || !strings.Contains(err.Error(), "sequence 3") {
		t.Fatalf("payload acceptance fence: %v", err)
	}
	c1 := dialLocalTest(t, r.Addr(), localTestHello(1))
	clients := [2]*LocalClient{c0, c1}
	g := readLocalPair(t, clients)
	if g.Position != 2 || len(g.Commands) != 2 {
		t.Fatalf("maximum payload grant shape: %+v", g)
	}
	for _, command := range g.Commands {
		if !bytes.Equal(command.Payload, payload) {
			t.Fatal("maximum payload grant truncated a command")
		}
	}
	ackLocalPair(t, clients, g.Tick, [32]byte{}, true)
	for _, c := range clients {
		if _, err := c.ReadGrant(); !errors.Is(err, io.EOF) {
			t.Fatal(err)
		}
	}
}

func TestLocalConcurrentWriters(t *testing.T) {
	_, clients := localTestPair(t)
	g := readLocalPair(t, clients)
	var wg sync.WaitGroup
	errs := make(chan error, 17)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _, err := clients[0].Submit([]byte{byte(i)}); errs <- err }(i)
	}
	wg.Add(1)
	go func() { defer wg.Done(); errs <- clients[0].Acknowledge(g.Tick, [32]byte{}, false, false) }()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	// ACK and submit writers may interleave. Fence the accepted commands
	// before the other ACK rather than relying on TCP reader scheduling.
	rawLocalSubmit(t, clients[0], 18, nil)
	if _, err := clients[0].ReadGrant(); err == nil || !strings.Contains(err.Error(), "sequence 17") {
		t.Fatalf("concurrent writer fence: %v", err)
	}
	if err := clients[1].Acknowledge(g.Tick, [32]byte{}, false, false); err != nil {
		t.Fatal(err)
	}
	g = readLocalPair(t, clients)
	if len(g.Commands) != 16 {
		t.Fatalf("concurrent submits lost commands: %d", len(g.Commands))
	}
	for i, command := range g.Commands {
		if command.Sequence != uint64(i+1) || command.Position != uint64(i+1) || command.Seat != 0 {
			t.Fatalf("concurrent writer order: %+v", g.Commands)
		}
	}
}

func TestLocalCloseUnblocksReadsAndPeerLoss(t *testing.T) {
	for _, relayClose := range []bool{false, true} {
		r, clients := localTestPair(t)
		readLocalPair(t, clients)
		result := make(chan error, 1)
		go func() { _, err := clients[1].ReadGrant(); result <- err }()
		if relayClose {
			_ = r.Close()
		} else {
			_ = clients[0].Close()
		}
		select {
		case err := <-result:
			if err == nil || errors.Is(err, io.EOF) {
				t.Fatalf("close looked like a grant or normal battle completion: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("close did not unblock peer")
		}
	}
}

func TestLocalCloseUnblocksBlockedSubmit(t *testing.T) {
	// A numeric loopback peer that never reads fills TCP's bounded send window.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	c := dialLocalTest(t, listener.Addr().String(), localTestHello(0))
	peer := <-accepted
	defer peer.Close()
	if err := c.conn.(*net.TCPConn).SetWriteBuffer(1024); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		payload := make([]byte, netproto.MaxCommandBytes)
		for {
			if _, err := c.Submit(payload); err != nil {
				result <- err
				return
			}
		}
	}()
	select {
	case err := <-result:
		t.Fatalf("write ended before Close: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	_ = c.Close()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("closed write succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("close did not unblock submit")
	}
}

func TestLocalEnvelopeBoundsAndLiteralFraming(t *testing.T) {
	for _, prefix := range [][]byte{{0}, {0x81, 0}, {0xff, 0xff, 0xff, 0xff, 0x1f}, {0xff, 0xff, 0xff, 0xff, 0x80}, {0x81, 8}} {
		if _, err := readLocalFrame(bytes.NewReader(prefix), localMaxHelloBytes); err == nil {
			t.Fatalf("accepted invalid length %x", prefix)
		}
	}
	var stream bytes.Buffer
	body := encodeLocalGrant(LocalGrant{Tick: 2, Position: 1, Commands: []LocalCommand{{Seat: 1, Sequence: 1, Position: 1, Payload: []byte{0, 255}}}})
	if err := writeLocalFrame(&stream, body); err != nil {
		t.Fatal(err)
	}
	want := []byte{10, localGrantMessage, 2, 1, 1, 1, 1, 1, 2, 0, 255}
	if !bytes.Equal(stream.Bytes(), want) {
		t.Fatalf("literal grant frame = %x, want %x", stream.Bytes(), want)
	}
}

func TestLocalHelloIgnoresTheAdvisoryBuild(t *testing.T) {
	r := listenLocalTest(t)
	c0 := dialLocalTest(t, r.Addr(), localTestHello(0))
	h := localTestHello(1)
	h.Identity.Build[0]++
	c1 := dialLocalTest(t, r.Addr(), h)
	if g := readLocalPair(t, [2]*LocalClient{c0, c1}); g.Tick != 1 {
		t.Fatalf("a different advisory build blocked the first grant: %+v", g)
	}
}
