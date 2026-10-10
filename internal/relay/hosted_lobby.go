package relay

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"sync"

	"github.com/nanolathe-gg/nanolathe/internal/netproto"
)

// HostedMaxSeats is the largest hosted room (DESIGN_MULTIPLAYER §16.6).
const HostedMaxSeats = 10

// HostedColors is how many player colours a seat may choose: the colour
// indices 0..9, one per frame of the logo art (DESIGN_MULTIPLAYER §16.6).
const HostedColors = 10

// Every seat of a full room can hold its own colour.
const _ = uint(HostedColors - HostedMaxSeats)

// HostedSeatState is one seat as the lobby reports it. Team is 0 for none or
// 1..5 (Survival ignores it); Side indexes the catalog's sides; Color is the
// seat's player colour, below HostedColors and held by no other present seat.
type HostedSeatState struct {
	Present, Ready    bool
	Team, Side, Color uint8
}

// HostedLobbyState is one seat's latest view of its room before Start
// (DESIGN_MULTIPLAYER §16.6.1). Seat 0 is the creator; only the first Size
// seats exist. It stays comparable, so a caller can tell a change.
type HostedLobbyState struct {
	Size  int
	Seats [HostedMaxSeats]HostedSeatState
	// Mismatch reports every present seat ready with differing
	// configuration or rehearsal digests: the match cannot start (§16.7).
	Mismatch bool
	Started  bool
	// Slot is this seat's slot once Started: its rank among present seats.
	Slot uint8
	// ConfigVersion counts replacements of the room's base configuration,
	// which the host may make until Start; Configuration returns the latest.
	ConfigVersion uint32
}

// HostedRoomDescription is what Describe returns: the room's encoded base
// configuration and its size.
type HostedRoomDescription struct {
	Config []byte
	Size   int
}

// HostedLobby is one seat's connection to a hosted room from Create or Join
// until Start, when Battle hands the same connection to the grant stream. Its
// reader goroutine stops exactly after Started, so the first grant reaches
// the battle client. State never blocks, so the window host may poll it every
// frame.
type HostedLobby struct {
	code   string
	seat   uint8
	conn   net.Conn
	client *LocalClient
	done   chan struct{} // closed when the reader stops

	mu     sync.Mutex
	state  HostedLobbyState
	config []byte
	err    error
	handed bool
}

// DescribeHostedRoom returns a room's base configuration and size, before the
// joiner composes anything. address is host:port for TLS or a
// wss://host/relay URL.
func DescribeHostedRoom(ctx context.Context, address, room string, options HostedDialOptions) (HostedRoomDescription, error) {
	if !validHostedCode(room) {
		return HostedRoomDescription{}, hostedError("room code", "a six-character invitation")
	}
	ctx, cancel := context.WithTimeout(ctx, hostedDefaultTimeouts.handshake)
	defer cancel()
	conn, err := dialHostedTransport(ctx, address, options)
	if err != nil {
		return HostedRoomDescription{}, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	var w netproto.Writer
	w.U8(hostedDescribeMessage)
	w.U16(hostedVersion)
	w.Text(room)
	if err := writeLocalFrame(conn, w.Bytes()); err != nil {
		return HostedRoomDescription{}, err
	}
	response, err := readLocalFrame(conn, hostedMaxConfigBytes+64)
	if err != nil {
		if expired := handshakeExpired(ctx); expired != nil {
			return HostedRoomDescription{}, localIOError("room description", expired)
		}
		return HostedRoomDescription{}, err
	}
	r := netproto.NewReader(response, hostedError)
	switch r.U8() {
	case hostedDescriptionMessage:
		size := int(r.U8())
		config := r.Raw(r.Count(hostedMaxConfigBytes, 1))
		if size < 2 || size > HostedMaxSeats {
			r.Abort(hostedError("room description", "2 to 10 seats"))
		}
		return HostedRoomDescription{Config: config, Size: size}, r.End()
	case localRefusedMessage, localFailedMessage:
		message := r.Text(localMaxErrorBytes)
		if err := r.End(); err != nil {
			return HostedRoomDescription{}, err
		}
		return HostedRoomDescription{}, errors.New(message)
	default:
		return HostedRoomDescription{}, hostedError("room description", "a description or refusal")
	}
}

// OpenHostedLobby creates a room of size seats (2..HostedMaxSeats) when room
// is empty, carrying the host's encoded base configuration (at most 64 KiB),
// or joins one, passing config nil and size 0, and takes its lowest free
// seat.
func OpenHostedLobby(ctx context.Context, address, room string, hello LocalHello, config []byte, size int, options HostedDialOptions) (*HostedLobby, error) {
	return openHostedLobby(ctx, hostedVersion, address, room, hello, config, size, options)
}

// openHostedLobby speaks a served version; tests use it to play a version-5
// seat, which the relay never sends a progress report.
func openHostedLobby(ctx context.Context, version uint16, address, room string, hello LocalHello, config []byte, size int, options HostedDialOptions) (*HostedLobby, error) {
	if room == "" && len(config) == 0 {
		return nil, hostedError("room configuration", "the creator's encoded configuration")
	}
	if room != "" {
		hello.Seat = hostedAnySeat
	}
	body, err := encodeHostedHelloVersion(version, room, 0, size, hello, config)
	if err != nil {
		return nil, err
	}
	c, code, seat, err := hostedHandshake(ctx, version, address, options, body, room, hello.Seat)
	if err != nil {
		return nil, err
	}
	l := &HostedLobby{code: code, seat: seat, conn: c.conn, client: c, done: make(chan struct{})}
	if room == "" {
		l.config = bytes.Clone(config)
	}
	go l.read()
	return l, nil
}

func (l *HostedLobby) read() {
	defer close(l.done)
	for {
		body, err := l.client.readFrame(hostedMaxConfigBytes + 64)
		if err != nil {
			l.fail(err)
			return
		}
		r := netproto.NewReader(body, hostedError)
		switch r.U8() {
		case hostedLobbyMessage:
			var st HostedLobbyState
			st.Size = int(r.U8())
			flags := r.U8()
			st.ConfigVersion = r.U32()
			if st.Size < 2 || st.Size > HostedMaxSeats {
				r.Abort(hostedError("lobby state", "2 to 10 seats"))
			}
			for i := range st.Size {
				bits := r.U8()
				st.Seats[i] = HostedSeatState{Present: bits&1 != 0, Ready: bits&2 != 0, Team: r.U8(), Side: r.U8(), Color: r.U8()}
				if st.Seats[i].Color >= HostedColors {
					r.Abort(hostedError("lobby state", "colours 0 to 9"))
				}
			}
			st.Mismatch = flags&hostedLobbyMismatch != 0
			if err := r.End(); err != nil {
				l.fail(err)
				return
			}
			l.mu.Lock()
			l.state = st
			l.mu.Unlock()
		case hostedConfigurationMessage:
			version := r.U32()
			config := r.Raw(r.Count(hostedMaxConfigBytes, 1))
			if err := r.End(); err != nil {
				l.fail(err)
				return
			}
			l.mu.Lock()
			l.config = bytes.Clone(config)
			l.state.ConfigVersion = version
			l.mu.Unlock()
		case hostedStartedMessage:
			slot := r.U8()
			if slot >= HostedMaxSeats {
				r.Abort(hostedError("Started slot", "a slot below 10"))
			}
			if err := r.End(); err != nil {
				l.fail(err)
				return
			}
			l.client.started.Store(true)
			l.mu.Lock()
			l.state.Started, l.state.Slot = true, slot
			l.mu.Unlock()
			return // the battle client reads across the opening barrier into grants
		case localRefusedMessage, localFailedMessage:
			message := r.Text(localMaxErrorBytes)
			if err := r.End(); err != nil {
				l.fail(err)
				return
			}
			l.fail(errors.New(message))
			return
		case localDoneMessage:
			l.fail(hostedError("room", "an open room"))
			return
		default:
			l.fail(hostedError("lobby message", "lobby state, configuration, Started or a refusal"))
			return
		}
	}
}

func (l *HostedLobby) fail(err error) {
	l.mu.Lock()
	if l.err == nil {
		l.err = err
	}
	l.mu.Unlock()
	_ = l.conn.Close()
}

func (l *HostedLobby) Code() string { return l.code }
func (l *HostedLobby) Seat() uint8  { return l.seat }

// State returns the latest lobby snapshot, or the error that ended the lobby.
func (l *HostedLobby) State() (HostedLobbyState, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.state, l.err
}

// Configuration returns the room's latest base configuration bytes.
func (l *HostedLobby) Configuration() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return bytes.Clone(l.config)
}

func (l *HostedLobby) send(body []byte) error {
	if _, err := l.State(); err != nil {
		return err
	}
	return l.client.writeMessage(body)
}

// SetTeam chooses this seat's team, 0 for none or 1..5, while not ready.
func (l *HostedLobby) SetTeam(team uint8) error {
	if team > hostedMaxTeam {
		return hostedError("team", "no team or teams 1 to 5")
	}
	return l.send([]byte{hostedTeamMessage, team})
}

// SetSide chooses this seat's side, an index into the catalog's sides, while
// not ready.
func (l *HostedLobby) SetSide(side uint8) error {
	return l.send([]byte{hostedSideMessage, side})
}

// SetColor chooses this seat's player colour, 0..HostedColors-1, while not
// ready. The relay ignores a colour another present seat holds, and a change
// clears every seat's ready, as a team or side change does.
func (l *HostedLobby) SetColor(color uint8) error {
	if color >= HostedColors {
		return hostedError("colour", "colours 0 to 9")
	}
	return l.send([]byte{hostedColorMessage, color})
}

// SetConfiguration replaces the room's base configuration (host only, at
// most 64 KiB) until Start; every seat's ready is cleared.
func (l *HostedLobby) SetConfiguration(config []byte) error {
	if l.seat != 0 {
		return hostedError("room configuration", "the room's host")
	}
	if len(config) == 0 || len(config) > hostedMaxConfigBytes {
		return hostedError("room configuration", "1 byte to 64 KiB")
	}
	var w netproto.Writer
	w.U8(hostedConfigurationMessage)
	w.U32(uint32(len(config)))
	w.Raw(config)
	return l.send(w.Bytes())
}

// SetReady reports this seat ready with its configuration-identity digest
// (its final MatchJoin identity without the advisory build) and its
// rehearsal digest, or not ready. The relay starts the match only when every
// present seat is ready with equal digests (§16.7).
func (l *HostedLobby) SetReady(ready bool, identity, rehearsal [32]byte) error {
	if !ready {
		return l.send([]byte{hostedReadyMessage, 0})
	}
	body := append([]byte{hostedReadyMessage, 1}, identity[:]...)
	return l.send(append(body, rehearsal[:]...))
}

// Start asks the relay to begin the match; only seat 0 may, once at least
// two seats are present, all ready with equal digests. A refused start
// leaves the lobby open.
func (l *HostedLobby) Start() error {
	if l.seat != 0 {
		return hostedError("start", "the room's host")
	}
	return l.send([]byte{hostedStartMessage})
}

// Battle returns the grant-stream client once Started, or nil before then.
// The lobby no longer reads from the connection after it is returned, and
// Close leaves that connection to the battle. The host calls OpeningReady
// on the client when its local presentation opening finishes (§16.6.1).
func (l *HostedLobby) Battle() *LocalClient {
	l.mu.Lock()
	started := l.state.Started && l.err == nil
	l.mu.Unlock()
	if !started {
		return nil
	}
	<-l.done
	l.mu.Lock()
	l.handed = true
	l.mu.Unlock()
	return l.client
}

// Close leaves the lobby. After Battle has handed over the connection, the
// battle client owns it and Close does nothing.
func (l *HostedLobby) Close() error {
	l.mu.Lock()
	handed := l.handed
	if !handed && l.err == nil {
		l.err = hostedError("lobby", "an open lobby")
	}
	l.mu.Unlock()
	if handed {
		return nil
	}
	err := l.conn.Close()
	<-l.done
	return err
}

// RoomCodeAlphabet is every symbol a room code uses (§16.5.1).
const RoomCodeAlphabet = hostedCodeAlphabet

// NormalizeRoomCode accepts a typed invitation in any case, with spaces or
// dashes, and returns the canonical code. A letter that codes never use but
// that looks like one of their digits reads as that digit: S as 5, Z as 2,
// B as 8 and G as 6.
func NormalizeRoomCode(typed string) (string, bool) {
	var b strings.Builder
	for _, r := range strings.ToUpper(typed) {
		switch r {
		case ' ', '-':
			continue
		case 'S':
			r = '5'
		case 'Z':
			r = '2'
		case 'B':
			r = '8'
		case 'G':
			r = '6'
		}
		b.WriteRune(r)
	}
	code := b.String()
	return code, validHostedCode(code)
}
