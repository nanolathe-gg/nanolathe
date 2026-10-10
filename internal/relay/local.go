// Package relay hosts the development-only loopback lockstep stream. It owns
// transport order and pacing, never gameplay (DESIGN_MULTIPLAYER §16.4.2).
package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/netproto"
)

// LocalHello identifies a seat before the tick-zero barrier.
type LocalHello struct {
	Seat            uint8
	Identity        netproto.Identity
	InitialChecksum [32]byte
}

// LocalCommand is a relay-stamped opaque command in a sealed grant.
type LocalCommand struct {
	Seat               uint8
	Sequence, Position uint64
	Payload            []byte
}

// LocalGrant seals one tick and the complete command prefix through Position.
type LocalGrant struct {
	Tick     uint32
	Position uint64
	Commands []LocalCommand
}

// Round upward to a whole nanosecond: never seal faster than 30 Hz.
const localTickInterval = (time.Second + 29) / 30

// LocalRelay serves exactly two numeric loopback connections. Closing it aborts
// the play test and unblocks all socket operations; it awards no result.
type LocalRelay struct {
	commandDelay time.Duration

	listener    net.Listener
	address     string
	done        chan struct{}
	once        sync.Once
	mu          sync.Mutex
	connections [2]net.Conn
	events      chan localEvent
}

type localPeer struct {
	conn       net.Conn
	hello      LocalHello
	registered bool
}

type localEvent struct {
	peer *localPeer
	body []byte
	err  error
}

func localAddress(address string, listen bool) error {
	a, err := netip.ParseAddrPort(address)
	if err != nil || !a.Addr().IsLoopback() || a.Addr().Zone() != "" || (!listen && a.Port() == 0) {
		return localError("address", "a numeric loopback IP and port")
	}
	return nil
}

func ListenLocal(address string) (*LocalRelay, error) {
	return ListenLocalWithCommandDelay(address, 0)
}

// ListenLocalWithCommandDelay adds a fixed order delay for responsiveness
// experiments. It leaves grants, acknowledgments and simulation speed alone;
// this is not a network emulator (DESIGN_MULTIPLAYER §16.4.3).
func ListenLocalWithCommandDelay(address string, delay time.Duration) (*LocalRelay, error) {
	if delay < 0 || delay > time.Second {
		return nil, localError("command delay", "0..1000 milliseconds of additional order delay")
	}
	if err := localAddress(address, true); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, localIOError("listen", err)
	}
	r := &LocalRelay{commandDelay: delay, listener: listener, address: listener.Addr().String(), done: make(chan struct{}), events: make(chan localEvent)}
	go r.run()
	go r.accept()
	return r, nil
}

func (r *LocalRelay) Addr() string {
	if r == nil {
		return ""
	}
	return r.address
}

func (r *LocalRelay) Close() error {
	if r == nil {
		return nil
	}
	r.once.Do(func() {
		close(r.done)
		_ = r.listener.Close()
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, conn := range r.connections {
			if conn != nil {
				_ = conn.Close()
			}
		}
	})
	return nil
}

func (r *LocalRelay) send(e localEvent) bool {
	select {
	case r.events <- e:
		return true
	case <-r.done:
		return false
	}
}

func (r *LocalRelay) accept() {
	for i := 0; i < 2; i++ {
		conn, err := r.listener.Accept()
		if err != nil {
			r.send(localEvent{err: localIOError("accept", err)})
			return
		}
		r.mu.Lock()
		select {
		case <-r.done:
			_ = conn.Close()
			r.mu.Unlock()
			return
		default:
			r.connections[i] = conn
		}
		r.mu.Unlock()
		go r.readPeer(&localPeer{conn: conn})
	}
	// No reconnect or third participant in this bounded prototype.
	_ = r.listener.Close()
}

func (r *LocalRelay) readPeer(peer *localPeer) {
	limit := localMaxHelloBytes
	for {
		body, err := readLocalFrame(peer.conn, limit)
		if !r.send(localEvent{peer: peer, body: body, err: err}) || err != nil {
			return
		}
		limit = localMaxClientFrame
	}
}

func (r *LocalRelay) broadcast(body []byte) error {
	r.mu.Lock()
	connections := r.connections
	r.mu.Unlock()
	for _, conn := range connections {
		if conn != nil {
			if err := writeLocalFrame(conn, body); err != nil {
				return err
			}
		}
	}
	return nil
}

func localFailureBody(kind uint8, err error) []byte {
	var w netproto.Writer
	w.U8(kind)
	message := err.Error()
	if len(message) > localMaxErrorBytes {
		message = message[:localMaxErrorBytes]
	}
	w.Text(message)
	return w.Bytes()
}

func (r *LocalRelay) run() {
	defer r.Close()
	var peers [2]*localPeer
	var sequences [2]uint64
	var acked, ended [2]bool
	var checks [2][32]byte
	var pending localCommandQueue
	var tick uint32
	var position uint64
	var lastSeal time.Time
	var timer *time.Timer
	var ready <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	arm := func(delay time.Duration) {
		if timer == nil {
			timer = time.NewTimer(delay)
		} else {
			timer.Reset(delay)
		}
		ready = timer.C
	}
	fail := func(err error) { _ = r.broadcast(localFailureBody(localFailedMessage, err)) }
	for {
		select {
		case <-r.done:
			return
		case <-ready:
			ready = nil
			if tick == ^uint32(0) {
				fail(localError("grant tick", "a tick before uint32 exhaustion"))
				return
			}
			tick++
			lastSeal = time.Now()
			acked = [2]bool{}
			commands := pending.release(lastSeal)
			body := encodeLocalGrant(LocalGrant{Tick: tick, Position: pending.sealed, Commands: commands})
			if err := r.broadcast(body); err != nil {
				fail(err)
				return
			}
		case e := <-r.events:
			if e.err != nil {
				fail(e.err)
				return
			}
			if !e.peer.registered {
				h, err := decodeLocalHello(e.body)
				if err == nil && h.Seat > 1 {
					err = localError("hello seat", "seat 0 or 1")
				}
				if err != nil {
					fail(err)
					return
				}
				if peers[h.Seat] != nil {
					fail(localError("hello seat", "two distinct seats"))
					return
				}
				other := peers[1-h.Seat]
				if other != nil {
					if field := localIdentityDifference(other.hello, h); field != "" {
						fail(localError("hello "+field, "identical values from both seats"))
						return
					}
				}
				e.peer.hello, e.peer.registered = h, true
				peers[h.Seat] = e.peer
				if other != nil {
					arm(0)
				}
				continue
			}
			seat := e.peer.hello.Seat
			d := netproto.NewReader(e.body, localError)
			switch d.U8() {
			case localSubmitMessage:
				sequence := d.U64()
				n := d.Count(netproto.MaxCommandBytes, 1)
				payload := d.Raw(n)
				if err := d.End(); err != nil {
					fail(err)
					return
				}
				if sequence <= sequences[seat] {
					continue
				}
				if sequence != sequences[seat]+1 {
					err := localError("command sequence", fmt.Sprintf("sequence %d", sequences[seat]+1))
					if err := writeLocalFrame(e.peer.conn, localFailureBody(localRefusedMessage, err)); err != nil {
						fail(err)
						return
					}
					continue
				}
				if len(pending.entries) == localMaxCommands || n > localMaxPendingBytes-pending.bytes || position == ^uint64(0) {
					fail(localError("pending commands", "at most 64 pending commands and 8 MiB, including delayed orders"))
					return
				}
				sequences[seat] = sequence
				position++
				pending.entries = append(pending.entries, localPendingCommand{
					command: LocalCommand{Seat: seat, Sequence: sequence, Position: position, Payload: payload},
					readyAt: time.Now().Add(r.commandDelay),
				})
				pending.bytes += n
			case localAckMessage:
				ackTick, flags := d.U32(), d.U8()
				if flags > ackFlagsMask {
					d.Abort(localError("acknowledgment flags", "the battle-ended and seat-final bits"))
				}
				end := flags&ackBattleEnded != 0
				var check [32]byte
				if ackTick%30 == 0 {
					check = d.Digest()
				}
				if err := d.End(); err != nil {
					fail(err)
					return
				}
				if tick == 0 || ackTick != tick || acked[seat] {
					fail(localError("acknowledgment tick", "one acknowledgment of the preceding grant"))
					return
				}
				acked[seat], ended[seat], checks[seat] = true, end, check
				if acked[0] && acked[1] {
					if tick%30 == 0 && checks[0] != checks[1] {
						fail(localError(fmt.Sprintf("tick %d checksum", tick), "identical unit checksums"))
						return
					}
					if ended[0] && ended[1] {
						_ = r.broadcast([]byte{localDoneMessage})
						return
					}
					arm(time.Until(lastSeal.Add(localTickInterval)))
				}
			default:
				fail(localError("client message", "submit or acknowledgment after hello"))
				return
			}
		}
	}
}

// LocalClient supports one ReadGrant reader, concurrent serialized Submit and
// Acknowledge and OpeningReady writers, and Close from any goroutine
// (DESIGN_MULTIPLAYER §16.4.2, §16.5.2).
type LocalClient struct {
	conn          net.Conn
	idle          time.Duration // zero for the loopback prototype
	maxCommand    int           // the hosted relay's smaller per-command bound, or zero
	writeMu       sync.Mutex
	version       uint16      // zero for the loopback prototype
	started       atomic.Bool // Started consumed by the lobby or grant reader
	autoOpening   bool        // direct streams have no presentation opening
	openingReady  bool        // writeMu: marker already sent
	sequence      uint64
	readTick      uint32
	readPosition  uint64
	readSequences [HostedMaxSeats]uint64
	traffic       trafficCounter
	progressMu    sync.Mutex
	progress      *HostedMatchProgress // the latest report; never modified
}

func DialLocal(ctx context.Context, address string, hello LocalHello) (*LocalClient, error) {
	if err := localAddress(address, false); err != nil {
		return nil, err
	}
	body, err := encodeLocalHello(hello)
	if err != nil {
		return nil, err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, localIOError("dial", err)
	}
	c := &LocalClient{conn: conn}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	err = c.writeFrame(body)
	stopped := stop()
	if err != nil || !stopped || ctx.Err() != nil {
		_ = conn.Close()
		if ctx.Err() != nil {
			return nil, localIOError("dial hello", ctx.Err())
		}
		return nil, err
	}
	return c, nil
}

func (c *LocalClient) Close() error {
	if c == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *LocalClient) Submit(payload []byte) (uint64, error) {
	if c == nil {
		return 0, localError("client", "a connected client")
	}
	if len(payload) > netproto.MaxCommandBytes {
		return 0, localError("command payload", "at most 4 MiB")
	}
	if c.maxCommand > 0 && len(payload) > c.maxCommand {
		return 0, hostedError("command payload", fmt.Sprintf("at most %d KiB through a hosted relay", c.maxCommand>>10))
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.sequence == ^uint64(0) {
		return 0, localError("client sequence", "a sequence before uint64 exhaustion")
	}
	sequence := c.sequence + 1
	var w netproto.Writer
	w.U8(localSubmitMessage)
	w.U64(sequence)
	w.U32(uint32(len(payload)))
	w.Raw(payload)
	if err := c.writeFrame(w.Bytes()); err != nil {
		return 0, err
	}
	c.sequence = sequence
	return sequence, nil
}

// ReadGrant waits for a sealed tick. Both terminal ACKs end it with io.EOF.
// A sequence refusal is reported without closing the connection; other relay
// failures abort the room. The prototype host may abort on either error.
// A hosted relay's progress reports are consumed here and kept for Progress.
func (c *LocalClient) ReadGrant() (LocalGrant, error) {
	if c == nil {
		return LocalGrant{}, localError("client", "a connected client")
	}
	var body []byte
	for {
		if c.idle > 0 && c.started.Load() {
			// After Started, the relay sends a grant, report or failure
			// well within this bound; silence means the route is gone.
			if err := c.conn.SetReadDeadline(time.Now().Add(c.idle)); err != nil {
				return LocalGrant{}, err
			}
		}
		var err error
		if body, err = c.readFrame(localMaxGrantFrame); err != nil {
			if c.idle > 0 && c.started.Load() && errors.Is(err, os.ErrDeadlineExceeded) {
				return LocalGrant{}, fmt.Errorf("%w: %w", hostedError("relay connection", fmt.Sprintf("relay traffic within %s", c.idle)), err)
			}
			return LocalGrant{}, err
		}
		if body[0] == hostedProgressMessage {
			// Host diagnostics, not a grant (§16.5.2).
			if err := c.recordProgress(body); err != nil {
				return LocalGrant{}, err
			}
			continue
		}
		// A hosted room without a lobby still reports its state and Started
		// before the first grant (§16.6.1).
		if c.readTick == 0 && body[0] == hostedStartedMessage {
			r := netproto.NewReader(body, hostedError)
			r.U8()
			if r.U8() >= HostedMaxSeats {
				r.Abort(hostedError("Started slot", "a slot below 10"))
			}
			if err := r.End(); err != nil {
				return LocalGrant{}, err
			}
			c.started.Store(true)
			if c.autoOpening {
				if err := c.OpeningReady(); err != nil {
					return LocalGrant{}, err
				}
			}
			continue
		}
		if c.readTick == 0 && (body[0] == hostedLobbyMessage || body[0] == hostedConfigurationMessage) {
			continue
		}
		break
	}
	r := netproto.NewReader(body, localError)
	switch r.U8() {
	case localGrantMessage:
		g, err := decodeLocalGrant(r)
		if err != nil {
			return LocalGrant{}, err
		}
		position, sequences := c.readPosition, c.readSequences
		if c.readTick == ^uint32(0) || g.Tick != c.readTick+1 {
			return LocalGrant{}, localError("grant tick", "exactly the next tick")
		}
		for _, command := range g.Commands {
			if position == ^uint64(0) || command.Position != position+1 || sequences[command.Seat] == ^uint64(0) || command.Sequence != sequences[command.Seat]+1 {
				return LocalGrant{}, localError("grant command order", "consecutive stream positions and per-seat sequences")
			}
			position, sequences[command.Seat] = command.Position, command.Sequence
		}
		if g.Position != position {
			return LocalGrant{}, localError("sealed position", "the last accepted command position")
		}
		c.readTick, c.readPosition, c.readSequences = g.Tick, position, sequences
		return g, nil
	case localRefusedMessage, localFailedMessage:
		message := r.Text(localMaxErrorBytes)
		if err := r.End(); err != nil {
			return LocalGrant{}, err
		}
		return LocalGrant{}, errors.New(message)
	case localDoneMessage:
		if err := r.End(); err != nil {
			return LocalGrant{}, err
		}
		return LocalGrant{}, io.EOF
	default:
		return LocalGrant{}, localError("server message", "grant, refusal, failure or stream completion")
	}
}

// OpeningReady reports that this seat finished its local presentation opening.
// Hosted version-7 clients may call it after Started; it sends one marker,
// serialized with Submit and Acknowledge. Earlier protocols and the loopback
// prototype need no marker (DESIGN_MULTIPLAYER §16.5.2, §16.6.1).
func (c *LocalClient) OpeningReady() error {
	if c == nil {
		return localError("client", "a connected client")
	}
	if c.version < hostedOpeningVersion {
		return nil
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.openingReady {
		return nil
	}
	if !c.started.Load() {
		return hostedError("opening readiness", "OpeningReady after Started")
	}
	if err := c.writeFrame([]byte{hostedOpeningReadyMessage}); err != nil {
		return err
	}
	c.openingReady = true
	return nil
}

// writeMessage sends one envelope, serialized with Submit and Acknowledge.
func (c *LocalClient) writeMessage(body []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.writeFrame(body)
}

// readFrame and writeFrame move one envelope and count it for Traffic.
func (c *LocalClient) readFrame(limit int) ([]byte, error) {
	return c.traffic.read(c.conn, limit)
}

func (c *LocalClient) writeFrame(body []byte) error {
	return c.traffic.write(c.conn, body)
}

// Acknowledgement flags: the shared battle has ended, and this seat's own
// result is final, which lets a defeated seat leave a hosted match
// (DESIGN_MULTIPLAYER §16.6.1). The loopback relay reads only the first.
const (
	ackBattleEnded = 1 << iota
	ackSeatFinal
	ackFlagsMask = ackBattleEnded | ackSeatFinal
)

// Acknowledge reports tick executed, with the unit checksum at every 30th
// tick, whether the battle has ended and whether this seat's result is final.
func (c *LocalClient) Acknowledge(tick uint32, checksum [32]byte, ended, final bool) error {
	if c == nil {
		return localError("client", "a connected client")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	var w netproto.Writer
	w.U8(localAckMessage)
	w.U32(tick)
	var flags uint8
	if ended {
		flags |= ackBattleEnded
	}
	if final {
		flags |= ackSeatFinal
	}
	w.U8(flags)
	if tick%30 == 0 {
		w.Digest(checksum)
	}
	return c.writeFrame(w.Bytes())
}
