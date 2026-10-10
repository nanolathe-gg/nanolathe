package relay

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/netproto"
)

// HostedConfig selects the bounded room service of DESIGN_MULTIPLAYER §16.5.1.
// TLS is required unless InsecureLoopback explicitly selects numeric loopback
// or ListenHostedWebSocket explicitly selects a hosting TLS terminator.
type HostedConfig struct {
	TLSConfig        *tls.Config
	InsecureLoopback bool
	MaxRooms         int // Zero selects 16; otherwise 1..256.
	// MaxConnections bounds established and pending connections together.
	// Zero selects 512; otherwise 2..1024, and at least two per room.
	MaxConnections int
}

// HostedDialOptions permits custom trusted roots, never skipped verification.
type HostedDialOptions struct {
	TLSConfig        *tls.Config
	InsecureLoopback bool
}

const (
	hostedHelloMessage   = localDoneMessage + 1
	hostedWelcomeMessage = hostedHelloMessage + 1
	// Lobby messages before Start (DESIGN_MULTIPLAYER §16.6.1).
	hostedDescribeMessage    = hostedWelcomeMessage + 1
	hostedDescriptionMessage = hostedDescribeMessage + 1
	hostedLobbyMessage       = hostedDescriptionMessage + 1
	hostedReadyMessage       = hostedLobbyMessage + 1
	hostedStartMessage       = hostedReadyMessage + 1
	hostedStartedMessage     = hostedStartMessage + 1
	hostedTeamMessage        = hostedStartedMessage + 1
	hostedSideMessage        = hostedTeamMessage + 1
	// hostedConfigurationMessage carries a replacement base configuration
	// from the host and the latest one to every seat.
	hostedConfigurationMessage = hostedSideMessage + 1
	hostedColorMessage         = hostedConfigurationMessage + 1
	// hostedProgressMessage is the relay's match progress report, sent only
	// to seats that spoke version 6 or later (§16.5.2).
	hostedProgressMessage = hostedColorMessage + 1
	// A version-7 seat has finished its local opening after Started. Grants
	// wait for every seat's marker (DESIGN_MULTIPLAYER §16.5.2, §16.6.1).
	hostedOpeningReadyMessage = hostedProgressMessage + 1
	// hostedVersion is the protocol this build's clients speak; the relay
	// still serves seats that speak hostedMinVersion, each in its own
	// version, in the same rooms (§12.2).
	hostedVersion         = 7
	hostedMinVersion      = 5
	hostedProgressVersion = 6
	hostedOpeningVersion  = 7
	hostedMaxTeam         = 5
	hostedCodeLength      = 6
	// hostedAnySeat is a joiner's hello seat: the relay assigns the lowest
	// free one.
	hostedAnySeat = 255
	// A creator's hello flag: start as soon as the second seat joins, for the
	// command-line play test and its probes, which have no lobby.
	hostedAutoStart      = 1
	hostedMaxConfigBytes = 64 << 10
	// Lobby state bit: both seats are ready and their rehearsals disagree.
	hostedLobbyMismatch     = 16
	hostedMaxHandshakeFrame = localMaxHelloBytes + hostedMaxConfigBytes + 64
	// Room codes avoid every pair a player can misread or mistype: no 0, 1,
	// O, I, L, D or Q, and no S, Z, B or G beside 5, 2, 8 and 6. Without
	// vowels a code cannot spell a word (§16.5.1).
	hostedCodeAlphabet    = "CFHJKMNPRTVWX23456789"
	hostedMaxAhead        = 30
	hostedMaxQueuedFrames = 64
	// Per-room byte bounds keep one misbehaving room's worst case near
	// 2.3 MiB: pending commands, one shared queue of grant bodies, each
	// writer's in-flight copy and each reader's frame. 128 rooms then stay
	// under the image's 384 MiB soft memory limit (§16.5.1). A 1,000-unit
	// order with a destination per unit encodes to about 21 KB.
	hostedMaxCommandBytes = 256 << 10
	hostedMaxClientFrame  = hostedMaxCommandBytes + 32
	hostedMaxPendingBytes = 256 << 10
	hostedMaxQueuedBytes  = (1 << 20) + 4096
)

// These host deadlines are policy, not simulation time. Keeping them together
// also lets socket tests exercise expiry without waiting minutes (§16.5.1–2).
type hostedTimeouts struct {
	handshake, waiting, opening, write, progress time.Duration
	// ping is the WebSocket keepalive interval; report the interval between
	// a running match's progress reports.
	ping, report time.Duration
}

// A lobby and, after Started, an unfinished opening each have a 30-minute
// bound (DESIGN_MULTIPLAYER §16.5.1–2, §16.6.1).
var hostedDefaultTimeouts = hostedTimeouts{
	handshake: 10 * time.Second, waiting: 30 * time.Minute, opening: 30 * time.Minute,
	write: 5 * time.Second, progress: 10 * time.Second,
	ping: websocketPingInterval, report: time.Second,
}

// hostedClientIdle bounds a running client's wait for a relay message. It
// exceeds the relay's ten-second progress abort, and replaces TCP keepalive's
// minutes of silence on a half-open route. Pings do not count: a browser
// answers them without telling the page.
const hostedClientIdle = 25 * time.Second

// HostedServer holds independent rooms of 2 to 10 seats. It never loads game
// assets or interprets a gameplay command (DESIGN_MULTIPLAYER §12, §16.5).
type HostedServer struct {
	listener  net.Listener
	websocket bool
	config    HostedConfig
	timeouts  hostedTimeouts
	done      chan struct{}
	once      sync.Once
	wg        sync.WaitGroup
	mu        sync.Mutex
	rooms     map[string]*hostedRoom
	conns     map[net.Conn]struct{}
	nextRoom  uint64      // protected by mu
	stats     serverStats // protected by mu
}

func hostedError(path, expected string) error {
	return fmt.Errorf("nanolathe: hosted relay rejected: logical path %s, providers searched [hosted transport], expected %s", path, expected)
}

func ListenHosted(address string, config HostedConfig) (*HostedServer, error) {
	return listenHosted(address, config, hostedDefaultTimeouts)
}

func listenHosted(address string, config HostedConfig, timeouts hostedTimeouts) (*HostedServer, error) {
	return listenHostedTransport(address, config, timeouts, false, false)
}

func listenHostedTransport(address string, config HostedConfig, timeouts hostedTimeouts, websocket, behindTLSProxy bool) (*HostedServer, error) {
	if config.MaxRooms == 0 {
		config.MaxRooms = 16
	}
	if config.MaxRooms < 1 || config.MaxRooms > 256 {
		return nil, hostedError("room capacity", "1..256 rooms")
	}
	if config.MaxConnections == 0 {
		config.MaxConnections = 512
	}
	if config.MaxConnections < 2*config.MaxRooms || config.MaxConnections > 1024 {
		return nil, hostedError("connection capacity", "2..1024 connections and at least two per room")
	}
	if behindTLSProxy {
		if !websocket || config.TLSConfig != nil || config.InsecureLoopback {
			return nil, hostedError("TLS proxy", "WebSocket HTTP with neither native TLS nor loopback mode")
		}
	} else if config.InsecureLoopback {
		if config.TLSConfig != nil {
			return nil, hostedError("TLS", "TLS or explicit loopback plaintext, not both")
		}
		if err := localAddress(address, true); err != nil {
			return nil, err
		}
	} else {
		if config.TLSConfig == nil || (len(config.TLSConfig.Certificates) == 0 && config.TLSConfig.GetCertificate == nil && config.TLSConfig.GetConfigForClient == nil) {
			return nil, hostedError("TLS", "a server certificate")
		}
		config.TLSConfig = config.TLSConfig.Clone()
		config.TLSConfig.MinVersion = max(config.TLSConfig.MinVersion, tls.VersionTLS12)
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, localIOError("hosted listen", err)
	}
	s := &HostedServer{listener: listener, websocket: websocket, config: config, timeouts: timeouts, done: make(chan struct{}), rooms: make(map[string]*hostedRoom), conns: make(map[net.Conn]struct{}), stats: serverStats{started: time.Now()}}
	s.wg.Add(1)
	go s.accept()
	return s, nil
}

func (s *HostedServer) Addr() string {
	if s == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// Close releases listeners, pending handshakes, rooms and all peer goroutines.
func (s *HostedServer) Close() error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		close(s.done)
		_ = s.listener.Close()
		s.mu.Lock()
		for conn := range s.conns {
			_ = conn.Close()
		}
		s.mu.Unlock()
	})
	s.wg.Wait()
	return nil
}

func (s *HostedServer) accept() {
	defer s.wg.Done()
	var backoff time.Duration
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			// Descriptor or buffer exhaustion passes; only Close ends the
			// service. Returning would leave a live process accepting nothing.
			if errors.Is(err, net.ErrClosed) {
				return
			}
			backoff = min(max(2*backoff, 5*time.Millisecond), time.Second)
			select {
			case <-s.done:
				return
			case <-time.After(backoff):
			}
			continue
		}
		backoff = 0
		s.mu.Lock()
		select {
		case <-s.done:
			_ = conn.Close()
			s.mu.Unlock()
			return
		default:
		}
		if len(s.conns) == s.config.MaxConnections {
			_ = conn.Close()
			s.mu.Unlock()
			continue
		}
		s.conns[conn] = struct{}{}
		s.wg.Add(1)
		s.mu.Unlock()
		go s.serve(conn)
	}
}

func (s *HostedServer) serve(raw net.Conn) {
	defer s.wg.Done()
	defer func() {
		_ = raw.Close()
		s.mu.Lock()
		delete(s.conns, raw)
		s.mu.Unlock()
	}()
	conn := raw
	handshakeDeadline := time.Now().Add(s.timeouts.handshake)
	_ = conn.SetDeadline(handshakeDeadline)
	if s.config.TLSConfig != nil {
		secure := tls.Server(raw, s.config.TLSConfig)
		if err := secure.Handshake(); err != nil {
			return
		}
		conn = secure
	}
	if s.websocket {
		stream, err := acceptHostedWebSocket(conn, s.timeouts, handshakeDeadline, s.statusPage)
		if err != nil {
			return
		}
		defer stream.Close()
		conn = stream
	}
	body, err := readLocalFrame(conn, hostedMaxHandshakeFrame)
	if err != nil {
		return
	}
	_ = conn.SetWriteDeadline(minDeadline(handshakeDeadline, time.Now().Add(s.timeouts.write)))
	if body[0] == hostedDescribeMessage {
		_ = writeLocalFrame(conn, s.describe(body))
		return
	}
	hello, err := decodeHostedHello(body)
	if err != nil {
		_ = writeLocalFrame(conn, localFailureBody(localRefusedMessage, err))
		return
	}
	peer := newHostedPeer(conn, hello.hello, s.timeouts.write)
	peer.version, peer.helloDeadline = hello.version, handshakeDeadline
	room, creator, err := s.admit(hello.code, hello.flags, hello.size, hello.config, peer)
	if err != nil {
		_ = writeLocalFrame(conn, localFailureBody(localRefusedMessage, err))
		return
	}
	_ = conn.SetDeadline(time.Time{})
	go peer.write()
	if creator {
		go room.run()
	} else if !room.send(hostedEvent{peer: peer, join: true}) {
		_ = peer.conn.Close()
		peer.stop()
		<-peer.written
		return
	}
	for {
		body, err := readLocalFrame(conn, hostedMaxClientFrame)
		if !room.send(hostedEvent{peer: peer, body: body, err: err}) || err != nil {
			break
		}
	}
	// The room's writer must flush explicit completion before this owner closes
	// the connection. The reader exiting is not permission to discard that frame.
	<-peer.written
}

// describe answers a joiner's request for a room's base configuration and
// size before it composes anything (DESIGN_MULTIPLAYER §16.6.1). The
// connection then closes.
func (s *HostedServer) describe(body []byte) []byte {
	r := netproto.NewReader(body, hostedError)
	r.U8()
	// A description is the same in every served version.
	if v := r.U16(); v < hostedMinVersion || v > hostedVersion {
		r.Abort(hostedVersionError(v))
	}
	code := r.Text(hostedCodeLength)
	if err := r.End(); err != nil {
		return localFailureBody(localRefusedMessage, err)
	}
	s.mu.Lock()
	room := s.rooms[code]
	var refusal error
	var config []byte
	size := 0
	switch {
	case room == nil:
		refusal = hostedError("room", "an existing invitation")
	case room.started:
		refusal = hostedError("room", "a room still in its lobby")
	case room.free() < 0:
		refusal = hostedError("room", "an unoccupied seat")
	default:
		config, size = room.config, room.size
	}
	s.mu.Unlock()
	if refusal != nil {
		return localFailureBody(localRefusedMessage, refusal)
	}
	var w netproto.Writer
	w.U8(hostedDescriptionMessage)
	w.U8(uint8(size))
	w.U32(uint32(len(config)))
	w.Raw(config)
	return w.Bytes()
}

// free is the lowest unreserved seat, or -1; the caller holds server.mu.
func (r *hostedRoom) free() int {
	for i := range r.size {
		if !r.taken[i] {
			return i
		}
	}
	return -1
}

// hostedVersionError is the relay's refusal of a version it does not serve.
func hostedVersionError(sent uint16) error {
	return hostedError("handshake", fmt.Sprintf("hosted protocol version 5, 6 or %d; this client sent version %d", hostedVersion, sent))
}

// hostedRelayVersionError is a client's refusal of a relay that answered in
// another version than the one it spoke.
func hostedRelayVersionError(spoke, answered uint16) error {
	return hostedError("welcome", fmt.Sprintf("hosted protocol version %d; this relay answered version %d", spoke, answered))
}

func validHostedCode(code string) bool {
	if len(code) != hostedCodeLength {
		return false
	}
	for i := range code {
		found := false
		for j := range hostedCodeAlphabet {
			found = found || code[i] == hostedCodeAlphabet[j]
		}
		if !found {
			return false
		}
	}
	return true
}

// newHostedCode draws a room code. Rejecting the bytes above the alphabet's
// last whole multiple keeps every symbol equally likely.
func newHostedCode() (string, error) {
	var picked [hostedCodeLength]byte
	for i := 0; i < len(picked); {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", localIOError("room invitation", err)
		}
		for _, b := range random {
			if i < len(picked) && int(b) < 256/len(hostedCodeAlphabet)*len(hostedCodeAlphabet) {
				picked[i] = hostedCodeAlphabet[int(b)%len(hostedCodeAlphabet)]
				i++
			}
		}
	}
	return string(picked[:]), nil
}

func (s *HostedServer) admit(code string, flags uint8, size int, config []byte, peer *hostedPeer) (*hostedRoom, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.done:
		return nil, false, hostedError("admission", "an open server")
	default:
	}
	creator := code == ""
	var room *hostedRoom
	if creator {
		if peer.hello.Seat != 0 {
			return nil, false, hostedError("hello seat", "creator seat 0")
		}
		if len(s.rooms) >= s.config.MaxRooms {
			return nil, false, hostedError("room capacity", "space for another room")
		}
		for {
			var err error
			if code, err = newHostedCode(); err != nil {
				return nil, false, err
			}
			if s.rooms[code] == nil {
				break
			}
		}
		s.nextRoom++
		s.stats.rooms++
		room = &hostedRoom{server: s, id: s.nextRoom, code: code, size: size, creator: peer, config: config, autoStart: flags&hostedAutoStart != 0,
			created: time.Now(), events: make(chan hostedEvent), done: make(chan struct{})}
		room.taken[0] = true
		s.rooms[code] = room
		s.wg.Add(1)
	} else {
		room = s.rooms[code]
		if room == nil {
			return nil, false, hostedError("room", "an existing invitation")
		}
		select {
		case <-room.done:
			return nil, false, hostedError("room", "an open room")
		default:
		}
		if room.started {
			return nil, false, hostedError("room", "a room still in its lobby")
		}
		seat := room.free()
		if seat < 0 {
			return nil, false, hostedError("room", "an unoccupied seat")
		}
		// A command-line room has no Ready digests, so its joiner's
		// identity is compared here; a lobby compares at Ready (§16.6.1).
		if room.autoStart {
			if field := localIdentityDifference(room.creator.hello, peer.hello); field != "" {
				return nil, false, hostedError("hello "+field, "identical values from every seat")
			}
		}
		room.taken[seat] = true
		peer.seat = uint8(seat)
	}
	peer.room = room
	// A fresh peer's queue is empty, so welcome always precedes its grants.
	_ = peer.enqueue(encodeHostedWelcome(code, peer.seat, peer.version), false)
	return room, creator, nil
}

// A creator's hello carries its flags, room size and base configuration; a
// joiner's carries none of them and asks for any seat (§16.6.1).
func encodeHostedHello(code string, flags uint8, size int, hello LocalHello, config []byte) ([]byte, error) {
	return encodeHostedHelloVersion(hostedVersion, code, flags, size, hello, config)
}

// encodeHostedHelloVersion speaks a served version; tests use it to play a
// version-5 seat.
func encodeHostedHelloVersion(version uint16, code string, flags uint8, size int, hello LocalHello, config []byte) ([]byte, error) {
	if code != "" && !validHostedCode(code) {
		return nil, hostedError("room code", "a six-character invitation")
	}
	if code != "" && (flags != 0 || size != 0 || len(config) != 0) {
		return nil, hostedError("join", "no creator flags, size or configuration")
	}
	if code == "" && (size < 2 || size > HostedMaxSeats) {
		return nil, hostedError("room size", "2 to 10 seats")
	}
	if len(config) > hostedMaxConfigBytes {
		return nil, hostedError("room configuration", "at most 64 KiB")
	}
	body, err := encodeLocalHello(hello)
	if err != nil {
		return nil, err
	}
	if len(body) > localMaxHelloBytes {
		return nil, hostedError("hello", "at most 1024 bytes")
	}
	var w netproto.Writer
	w.U8(hostedHelloMessage)
	w.U16(version)
	w.Text(code)
	w.U8(flags)
	w.U8(uint8(size))
	w.U32(uint32(len(body)))
	w.Raw(body)
	w.U32(uint32(len(config)))
	w.Raw(config)
	return w.Bytes(), nil
}

// hostedHello is a decoded hosted hello.
type hostedHello struct {
	version uint16
	code    string
	flags   uint8
	size    int
	hello   LocalHello
	config  []byte
}

func decodeHostedHello(body []byte) (hostedHello, error) {
	r := netproto.NewReader(body, hostedError)
	if r.U8() != hostedHelloMessage {
		r.Abort(hostedError("handshake", "a hosted hello or room description request"))
	}
	version := r.U16()
	if version < hostedMinVersion || version > hostedVersion {
		r.Abort(hostedVersionError(version))
	}
	code := r.Text(hostedCodeLength)
	if code != "" && !validHostedCode(code) {
		r.Abort(hostedError("room code", "a six-character invitation"))
	}
	flags := r.U8()
	size := int(r.U8())
	n := r.Count(localMaxHelloBytes, 1)
	hello := r.Raw(n)
	config := r.Raw(r.Count(hostedMaxConfigBytes, 1))
	if flags&^hostedAutoStart != 0 || (code != "" && (flags != 0 || size != 0 || len(config) != 0)) || (code == "" && (size < 2 || size > HostedMaxSeats)) {
		r.Abort(hostedError("hello flags", "a creator's known flags and 2 to 10 seats, and none of them from a joiner"))
	}
	if err := r.End(); err != nil {
		return hostedHello{}, err
	}
	h, err := decodeLocalHello(hello)
	return hostedHello{version: version, code: code, flags: flags, size: size, hello: h, config: config}, err
}

// The welcome answers in the version the seat spoke.
func encodeHostedWelcome(code string, seat uint8, version uint16) []byte {
	var w netproto.Writer
	w.U8(hostedWelcomeMessage)
	w.U16(version)
	w.Text(code)
	w.U8(seat)
	return w.Bytes()
}

// DialHosted creates a room when room is empty, or joins its invitation, and
// returns the grant stream directly (§16.5.1). The room needs no lobby: a
// creator's room starts as soon as a second seat joins, and a joiner reports
// ready at once, so it also starts when a lobby host starts (§16.6.1).
func DialHosted(ctx context.Context, address, room string, hello LocalHello, options HostedDialOptions) (*LocalClient, string, error) {
	return dialHostedStream(ctx, address, room, hello, options)
}

func dialHostedStream(ctx context.Context, address, room string, hello LocalHello, options HostedDialOptions) (*LocalClient, string, error) {
	var flags uint8
	size := 0
	if room == "" {
		flags, size = hostedAutoStart, 2
	}
	body, err := encodeHostedHello(room, flags, size, hello, nil)
	if err != nil {
		return nil, "", err
	}
	c, code, _, err := hostedHandshake(ctx, hostedVersion, address, options, body, room, hello.Seat)
	if err != nil {
		return nil, "", err
	}
	// A direct stream has no presentation host. Its reader reports opening
	// readiness only after consuming Started, never during admission.
	c.autoOpening = true
	// A command-line seat runs no rehearsal; its zero digest matches only
	// another command-line seat, and an auto-start room ignores digests.
	if err := c.writeMessage(append([]byte{hostedReadyMessage, 1}, make([]byte, 64)...)); err != nil {
		_ = c.Close()
		return nil, "", err
	}
	return c, code, nil
}

// hostedHandshake connects as dialHostedTransport does, exchanges the hello
// within the handshake deadline and returns the client that owns the stream.
// The hello body speaks version.
func hostedHandshake(ctx context.Context, version uint16, address string, options HostedDialOptions, body []byte, room string, seat uint8) (*LocalClient, string, uint8, error) {
	ctx, cancel := context.WithTimeout(ctx, hostedDefaultTimeouts.handshake)
	defer cancel()
	conn, err := dialHostedTransport(ctx, address, options)
	if err != nil {
		return nil, "", 0, err
	}
	c := newHostedClient(conn)
	c.version = version
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	code, assigned, err := exchangeHostedHello(conn, &c.traffic, version, body, room, seat)
	stopped := stop()
	if expired := handshakeExpired(ctx); err != nil || !stopped || expired != nil {
		_ = conn.Close()
		if expired != nil {
			return nil, "", 0, localIOError("hosted handshake", expired)
		}
		return nil, "", 0, err
	}
	_ = conn.SetDeadline(time.Time{})
	return c, code, assigned, nil
}

// handshakeExpired reports the context's end, including a socket deadline
// that fired at the context's deadline just before its timer did.
func handshakeExpired(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return nil
}

// dialHostedTransport opens a verified TLS stream to host:port, or a WebSocket
// stream when address is a ws(s) URL, with the context's deadline applied.
// The browser build has only the WebSocket (websocket_browser_js.go).
func dialHostedTransport(ctx context.Context, address string, options HostedDialOptions) (net.Conn, error) {
	if strings.Contains(address, "://") {
		return dialWebSocketTransport(ctx, address, options)
	}
	return dialHostedSocket(ctx, address, options)
}

// exchangeHostedHello sends the hello and returns the welcome's code and
// assigned seat: seat 0 for a creator, any free seat for a joiner. The relay
// must answer in the version the hello spoke.
func exchangeHostedHello(conn net.Conn, traffic *trafficCounter, version uint16, body []byte, requested string, seat uint8) (string, uint8, error) {
	if err := traffic.write(conn, body); err != nil {
		return "", 0, err
	}
	response, err := traffic.read(conn, localMaxErrorBytes+32)
	if err != nil {
		return "", 0, err
	}
	r := netproto.NewReader(response, hostedError)
	kind := r.U8()
	if kind == localRefusedMessage || kind == localFailedMessage {
		message := r.Text(localMaxErrorBytes)
		if err := r.End(); err != nil {
			return "", 0, err
		}
		return "", 0, errors.New(message)
	}
	if kind != hostedWelcomeMessage {
		r.Abort(hostedError("welcome", "a hosted welcome"))
	}
	if v := r.U16(); v != version {
		r.Abort(hostedRelayVersionError(version, v))
	}
	code, assigned := r.Text(hostedCodeLength), r.U8()
	if !validHostedCode(code) || (requested != "" && code != requested) || (requested == "" && assigned != seat) || assigned >= HostedMaxSeats {
		r.Abort(hostedError("welcome", "the requested room and assigned seat"))
	}
	return code, assigned, r.End()
}

func newHostedClient(conn net.Conn) *LocalClient {
	return &LocalClient{conn: hostedClientConn{Conn: conn}, idle: hostedClientIdle, maxCommand: hostedMaxCommandBytes}
}

// LocalClient serializes all writes. Apply the hosted write bound without
// changing the loopback client's behavior; ReadGrant owns the idle bound.
type hostedClientConn struct{ net.Conn }

func (c hostedClientConn) Write(p []byte) (int, error) {
	if err := c.Conn.SetWriteDeadline(time.Now().Add(hostedDefaultTimeouts.write)); err != nil {
		return 0, err
	}
	return c.Conn.Write(p)
}
