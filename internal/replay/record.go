package replay

import (
	"fmt"
	"sync"

	"github.com/nanolathe-gg/nanolathe/internal/lockstep"
	"github.com/nanolathe-gg/nanolathe/internal/netproto"
	"github.com/nanolathe-gg/nanolathe/internal/relay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/version"
)

// recordError is the one diagnostic shape of a recording refused at setup.
func recordError(path, expected string) error {
	return fmt.Errorf("nanolathe: replay recording refused: logical path %s, providers searched [replay format v%d], expected %s", path, FormatVersion, expected)
}

// SinglePlayerHeader is the header of a single-player battle's recording:
// s composed for config — the configuration MatchConfigForFreshBattle
// describes it with — and not yet ticked. The identity is what a
// single-player host can establish without freezing its content a second
// time: the protocol, this build's advisory digest (zero when unstamped),
// the frozen inputs' digest when the session reports one, the rules, the
// mod and the configuration; the map identity stays zero, covered by the
// content digest. The host sets Started.
func SinglePlayerHeader(s *session.Session, config session.EffectiveMatchConfig) (Header, error) {
	if s == nil || s.Clock == nil || s.Clock.GlobalTick != 0 || s.OnlineCommandContext() {
		return Header{}, recordError("session", "a single-player battle composed and not yet ticked")
	}
	r := config.Request()
	kind, ok := kindFor(r.SessionKind, false)
	if !ok {
		return Header{}, recordError("config", "a resolved skirmish or Survival configuration")
	}
	if int(s.LocalOwner) >= len(r.Seats) || r.Seats[s.LocalOwner].Role != session.MatchRoleHuman {
		return Header{}, recordError("session.localOwner", "the configuration's human seat")
	}
	h, err := headerFor(s, config, kind)
	if err != nil {
		return Header{}, err
	}
	// The seat identity of a join without frozen inputs: protocol, rules,
	// mod and configuration exactly as online play reports them. Its only
	// refusals are the content and map fields it cannot establish, filled
	// (content) or left zero (map) below.
	h.Identity, _ = session.MatchJoin{Config: config, Mod: r.Mod}.Identity()
	h.Identity.Build = advisoryBuild()
	if digest, ok := s.SimulationContentDigest(); ok {
		h.Identity.Content = digest
	}
	return h, nil
}

// OnlineHeader is the header of one seat's recording of an online battle: s
// composed and prepared for config, not yet ticked, and identity the seat's
// own (session.MatchJoin.Identity, as the battle was composed with). The
// host sets Started.
func OnlineHeader(s *session.Session, config session.EffectiveMatchConfig, identity netproto.Identity) (Header, error) {
	if s == nil || s.Clock == nil || s.Clock.GlobalTick != 0 || !s.OnlineCommandContext() {
		return Header{}, recordError("session", "an online battle composed and not yet ticked")
	}
	kind, ok := kindFor(config.Request().SessionKind, true)
	if !ok {
		return Header{}, recordError("config", "a resolved online skirmish or Survival configuration")
	}
	if identity.Configuration != config.Digest() {
		return Header{}, recordError("identity.configuration", "the identity the battle's own configuration digests to")
	}
	h, err := headerFor(s, config, kind)
	if err != nil {
		return Header{}, err
	}
	h.Identity = identity
	return h, nil
}

// headerFor fills what both kinds of header share.
func headerFor(s *session.Session, config session.EffectiveMatchConfig, kind Kind) (Header, error) {
	encoded, err := session.EncodeMatchConfig(config)
	if err != nil {
		return Header{}, err
	}
	r := config.Request()
	sides := session.OnlineSides(s.Catalog)
	h := Header{Kind: kind, Config: encoded, InitialChecksum: s.UnitStateChecksum(), LocalSeat: s.LocalOwner, MapName: r.MapName}
	for _, seat := range r.Seats {
		p := Seat{Name: seat.Nickname, Color: seat.Color, Role: seat.Role}
		if int(seat.Side) < len(sides) {
			p.Side = sides[seat.Side]
		}
		h.Seats = append(h.Seats, p)
	}
	return h, nil
}

// advisoryBuild is this build's common manifest digest, or zero for an
// unstamped development build, as a seat reports it online (§8.2).
func advisoryBuild() [32]byte {
	m, err := version.CurrentBuildManifest()
	if err != nil || !m.Stamped() {
		return [32]byte{}
	}
	d, err := m.Digest()
	if err != nil {
		return [32]byte{}
	}
	return d
}

// SessionRecorder records a single-player battle: the session reports every
// local command phase 1 applies, every pump that ran a tick and the
// cadence's unit checksums, on the simulation thread, and the recorder
// copies each into the Writer. A command the replay form cannot express, or
// the session's own failure report, stops the recording (Writer.Stop). The
// host keeps the Writer to flush and close it.
type SessionRecorder struct {
	w *Writer
}

// NewSessionRecorder attaches a recorder writing to w to s, a single-player
// battle not yet ticked whose local seat is the header's. It returns the
// session's refusal if the battle cannot be recorded.
func NewSessionRecorder(w *Writer, s *session.Session) (*SessionRecorder, error) {
	if w == nil || s == nil {
		return nil, recordError("recorder", "a writer and a session")
	}
	kind, seat := w.recording()
	if kind.Online() || s.OnlineCommandContext() {
		return nil, recordError("recorder", "a single-player replay of a single-player battle; an online battle records its relay stream (NewOnlineRecorder)")
	}
	if s.LocalOwner != seat {
		return nil, recordError("session.localOwner", fmt.Sprintf("the header's local seat %d", seat))
	}
	rec := &SessionRecorder{w: w}
	if err := s.SetReplayRecorder(rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// Command implements session.ReplayRecorder.
func (r *SessionRecorder) Command(stamp session.CommandStamp, c session.SeatCommand) {
	payload, err := session.EncodeSeatCommand(session.SinglePlayerReplay, c)
	if err != nil {
		r.w.Stop(err)
		return
	}
	_ = r.w.Command(stamp.Tick, stamp.Seat, stamp.Position, payload)
}

// PumpEnded implements session.ReplayRecorder.
func (r *SessionRecorder) PumpEnded(lastTick uint32, ticks int) { _ = r.w.Pump(lastTick, ticks) }

// Checksum implements session.ReplayRecorder.
func (r *SessionRecorder) Checksum(tick uint32, sum [32]byte) { _ = r.w.Checksum(tick, sum) }

// Failed implements session.ReplayRecorder.
func (r *SessionRecorder) Failed(err error) { r.w.Stop(err) }

// onlineGrantBacklog bounds the grants read but not yet acknowledged: the
// paced driver buffers 32, and the relay may seal a few more after the
// battle ends that no one executes.
const onlineGrantBacklog = 256

// OnlineRecorder is a lockstep.Client that records the relay stream one seat
// executes: each grant's commands with their seats and stream positions as
// the tick it seals, a one-tick pump per tick, and the checksum this seat
// acknowledges every 30th tick. It records a grant when the driver
// acknowledges it, so a grant read but never executed is never recorded. A
// gap in the acknowledged ticks stops the recording; the stream itself is
// passed through unchanged.
type OnlineRecorder struct {
	client lockstep.Client
	w      *Writer
	mu     sync.Mutex
	grants []relay.LocalGrant
}

var _ lockstep.Client = (*OnlineRecorder)(nil)

// NewOnlineRecorder wraps an online battle's client, recording to w, whose
// header is an online kind. The host installs it where it builds the
// driver.
func NewOnlineRecorder(w *Writer, c lockstep.Client) (*OnlineRecorder, error) {
	if w == nil || c == nil {
		return nil, recordError("recorder", "a writer and a client")
	}
	if kind, _ := w.recording(); !kind.Online() {
		return nil, recordError("recorder", "an online replay; a single-player battle records its session (NewSessionRecorder)")
	}
	return &OnlineRecorder{client: c, w: w}, nil
}

// Submit passes a command payload through.
func (o *OnlineRecorder) Submit(payload []byte) (uint64, error) { return o.client.Submit(payload) }

// ReadGrant passes the next grant through and keeps it until its tick is
// acknowledged.
func (o *OnlineRecorder) ReadGrant() (relay.LocalGrant, error) {
	g, err := o.client.ReadGrant()
	if err != nil {
		return g, err
	}
	o.mu.Lock()
	if len(o.grants) == onlineGrantBacklog {
		// Only grants nobody executes pile up; if one of them is executed
		// after all, Acknowledge finds the gap and stops the recording.
		o.grants = append(o.grants[:0], o.grants[1:]...)
	}
	o.grants = append(o.grants, g)
	o.mu.Unlock()
	return g, nil
}

// Acknowledge records the acknowledged tick's grant and checksum, then
// passes the acknowledgement through.
func (o *OnlineRecorder) Acknowledge(tick uint32, checksum [32]byte, ended, final bool) error {
	o.record(tick, checksum)
	return o.client.Acknowledge(tick, checksum, ended, final)
}

func (o *OnlineRecorder) record(tick uint32, checksum [32]byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	i := 0
	for i < len(o.grants) && o.grants[i].Tick != tick {
		i++
	}
	if i != 0 || i == len(o.grants) {
		o.w.Stop(recordError(fmt.Sprintf("acknowledged tick %d", tick), "the oldest grant read and not yet acknowledged"))
		if i < len(o.grants) {
			o.grants = o.grants[i+1:]
		}
		return
	}
	g := o.grants[0]
	o.grants = o.grants[1:]
	for _, c := range g.Commands {
		if o.w.Command(g.Tick, c.Seat, c.Position, c.Payload) != nil {
			return
		}
	}
	if o.w.Pump(g.Tick, 1) != nil {
		return
	}
	if g.Tick%session.ReplayChecksumInterval == 0 {
		_ = o.w.Checksum(g.Tick, checksum)
	}
}

// Close closes the wrapped client. The host closes the Writer itself.
func (o *OnlineRecorder) Close() error { return o.client.Close() }
