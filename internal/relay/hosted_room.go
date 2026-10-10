package relay

import (
	"bytes"
	"fmt"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/netproto"
)

type hostedEvent struct {
	peer *hostedPeer
	body []byte
	err  error
	join bool
}

// hostedRoom is one room: a lobby of up to HostedMaxSeats seats, then a
// match between the seats present at Start (DESIGN_MULTIPLAYER §16.6.1).
type hostedRoom struct {
	server    *HostedServer
	id        uint64 // sequential, shown on the status page instead of the code
	code      string
	size      int
	creator   *hostedPeer
	autoStart bool
	created   time.Time
	// Protected by server.mu: the latest base configuration (for Describe),
	// the seats admission has reserved, and whether Start closed the room.
	config  []byte
	taken   [HostedMaxSeats]bool
	started bool
	events  chan hostedEvent
	done    chan struct{}
	status  roomStatusBox
}

func (r *hostedRoom) send(e hostedEvent) bool {
	select {
	case r.events <- e:
		return true
	case <-r.done:
		return false
	case <-r.server.done:
		return false
	}
}

type hostedAck struct {
	tick  uint32
	ended bool
	check [32]byte
}

// hostedProgress follows every playing slot's acknowledgements. A ring keeps
// reports until every active slot has reached that tick; the grant window
// bounds the spread to 30 ticks (§16.5.2). A slot that leaves after its
// result became final is no longer waited for or compared.
type hostedProgress struct {
	n        int
	active   [HostedMaxSeats]bool
	final    [HostedMaxSeats]bool
	acked    [HostedMaxSeats]uint32
	last     [HostedMaxSeats]time.Time
	history  [HostedMaxSeats][hostedMaxAhead + 1]hostedAck
	terminal uint32
	compared uint32
}

func (p *hostedProgress) acknowledge(slot int, grant uint32, ack hostedAck, final bool, now time.Time) (bool, error) {
	if p.acked[slot] == ^uint32(0) || ack.tick != p.acked[slot]+1 || ack.tick > grant || (p.terminal != 0 && ack.tick > p.terminal) {
		return false, hostedError("acknowledgment tick", "exactly the next executed tick within the granted prefix")
	}
	if ack.ended {
		if p.terminal != 0 && p.terminal != ack.tick {
			return false, hostedError("terminal tick", "the same terminal tick from every seat")
		}
		p.terminal = ack.tick
	}
	p.acked[slot], p.last[slot] = ack.tick, now
	p.final[slot] = p.final[slot] || final
	p.history[slot][ack.tick%(hostedMaxAhead+1)] = ack
	return p.compare()
}

// compare checks every tick all active slots have executed: the same ended
// bit, and at every 30th tick the same unit checksum. It reports completion
// once the agreed terminal tick has been compared.
func (p *hostedProgress) compare() (bool, error) {
	for {
		next := p.compared + 1
		first := -1
		for s := range p.n {
			if !p.active[s] {
				continue
			}
			if p.acked[s] < next {
				return false, nil
			}
			if first < 0 {
				first = s
			}
		}
		if first < 0 {
			return false, nil
		}
		ref := p.history[first][next%(hostedMaxAhead+1)]
		for s := first + 1; s < p.n; s++ {
			if !p.active[s] {
				continue
			}
			h := p.history[s][next%(hostedMaxAhead+1)]
			if h.tick != next || h.ended != ref.ended {
				return false, hostedError("terminal agreement", "the same terminal tick and outcome bit from every seat")
			}
			if next%30 == 0 && h.check != ref.check {
				return false, hostedError(fmt.Sprintf("tick %d checksum", next), "identical unit checksums")
			}
		}
		p.compared = next
		if ref.ended {
			return true, nil
		}
	}
}

// slowest is the lowest acknowledged tick and the oldest acknowledgement
// time among active slots.
func (p *hostedProgress) slowest() (uint32, time.Time) {
	low, oldest, any := uint32(0), time.Time{}, false
	for s := range p.n {
		if !p.active[s] {
			continue
		}
		if !any || p.acked[s] < low {
			low = p.acked[s]
		}
		if !any || p.last[s].Before(oldest) {
			oldest = p.last[s]
		}
		any = true
	}
	return low, oldest
}

func (p *hostedProgress) playing() int {
	n := 0
	for s := range p.n {
		if p.active[s] {
			n++
		}
	}
	return n
}

// lobbySeat is one seat's lobby state.
type lobbySeat struct {
	peer                *hostedPeer
	ready               bool
	team, side, color   uint8
	identity, rehearsal [32]byte
}

func (r *hostedRoom) run() {
	defer r.server.wg.Done()
	defer func() {
		r.server.mu.Lock()
		delete(r.server.rooms, r.code)
		r.server.mu.Unlock()
	}()
	var seats [HostedMaxSeats]lobbySeat
	// The creator takes colour 0, the lowest free one.
	seats[0].peer = r.creator
	var players [HostedMaxSeats]*hostedPeer // by slot, after Start
	var sequences [HostedMaxSeats]uint64    // by slot after Start; seat 0 before it
	var pending localCommandQueue
	var position uint64
	var tick uint32
	var progress hostedProgress
	var sealed [hostedMaxAhead + 1]time.Time // when each recent tick was sealed
	var lastSeal time.Time
	seal := time.NewTimer(time.Hour)
	seal.Stop()
	defer seal.Stop()
	var ready <-chan time.Time
	reports := time.NewTimer(time.Hour)
	reports.Stop()
	defer reports.Stop()
	var reporting <-chan time.Time
	deadline := time.NewTimer(r.server.timeouts.waiting)
	defer deadline.Stop()
	var started bool
	var opening bool
	var openingReady [HostedMaxSeats]bool // by slot; legacy seats are implicit
	var configVersion uint32
	stats := roomStats{created: r.created}

	every := func(fn func(*hostedPeer)) {
		if started {
			for slot := range progress.n {
				if progress.active[slot] {
					fn(players[slot])
				}
			}
			return
		}
		for i := range r.size {
			if seats[i].peer != nil {
				fn(seats[i].peer)
			}
		}
	}
	publish := func() {
		var snap roomSnapshot
		snap.id, snap.size, snap.created, snap.started = r.id, r.size, r.created, started
		snap.ticks, snap.compared = tick, progress.compared
		snap.queued = pending.bytes
		stats.fill(&snap)
		if started {
			snap.startedAt = stats.startedAt
			for slot := range progress.n {
				p := players[slot]
				seat := seatSnapshot{seat: int(p.seat), slot: slot, present: true, ready: true, team: seats[p.seat].team, side: seats[p.seat].side,
					acked: progress.acked[slot], final: progress.final[slot], left: !progress.active[slot], rtt: p.rtt(), ackLag: stats.ackLag[slot]}
				if tick > progress.acked[slot] {
					seat.behind = tick - progress.acked[slot]
				}
				snap.queued += p.queuedBytes()
				snap.seats = append(snap.seats, seat)
			}
		} else {
			for i := range r.size {
				s := seats[i]
				if s.peer == nil {
					continue
				}
				snap.seats = append(snap.seats, seatSnapshot{seat: i, slot: -1, present: true, ready: s.ready, team: s.team, side: s.side, rtt: s.peer.rtt()})
				snap.queued += s.peer.queuedBytes()
			}
		}
		r.status.set(snap)
	}
	enqueue := func(p *hostedPeer, body []byte, last bool) error {
		stats.bytesOut += uint64(len(body))
		return p.enqueue(body, last)
	}
	finish := func(err error, reason string) {
		// Unblock producers first; then let each writer flush its last frame.
		// Closing the room must never truncate the explicit done message.
		close(r.done)
		body := []byte{localDoneMessage}
		if err != nil {
			body = localFailureBody(localFailedMessage, err)
		}
		var peers []*hostedPeer
		every(func(p *hostedPeer) { peers = append(peers, p) })
		for _, p := range peers {
			if err := p.enqueue(body, true); err != nil {
				_ = p.conn.Close()
				p.stop()
			}
		}
		for _, p := range peers {
			<-p.written
		}
		r.server.recordFinished(r, &stats, started, progress.n, tick, reason)
	}
	// held reports a colour some present seat other than skip holds.
	held := func(color uint8, skip int) bool {
		for i := range r.size {
			if i != skip && seats[i].peer != nil && seats[i].color == color {
				return true
			}
		}
		return false
	}
	present := func() int {
		n := 0
		for i := range r.size {
			if seats[i].peer != nil {
				n++
			}
		}
		return n
	}
	allReady := func() bool {
		any := false
		for i := range r.size {
			if seats[i].peer != nil {
				if !seats[i].ready {
					return false
				}
				any = true
			}
		}
		return any
	}
	// mismatch: every present seat is ready but their configuration or
	// rehearsal digests differ (§16.7).
	mismatch := func() bool {
		if present() < 2 || !allReady() {
			return false
		}
		var ref *lobbySeat
		for i := range r.size {
			s := &seats[i]
			if s.peer == nil {
				continue
			}
			if ref == nil {
				ref = s
			} else if s.identity != ref.identity || s.rehearsal != ref.rehearsal {
				return true
			}
		}
		return false
	}
	clearReady := func() {
		for i := range seats {
			seats[i].ready = false
		}
	}
	lobby := func() error {
		var w netproto.Writer
		w.U8(hostedLobbyMessage)
		w.U8(uint8(r.size))
		flags := uint8(0)
		if mismatch() {
			flags |= hostedLobbyMismatch
		}
		w.U8(flags)
		w.U32(configVersion)
		for i := range r.size {
			s := seats[i]
			bits := uint8(0)
			if s.peer != nil {
				bits |= 1
			}
			if s.ready {
				bits |= 2
			}
			w.U8(bits)
			w.U8(s.team)
			w.U8(s.side)
			w.U8(s.color)
		}
		body := w.Bytes()
		var err error
		every(func(p *hostedPeer) {
			if err == nil {
				err = enqueue(p, body, false)
			}
		})
		publish()
		return err
	}
	configuration := func() []byte {
		r.server.mu.Lock()
		config := r.config
		r.server.mu.Unlock()
		var w netproto.Writer
		w.U8(hostedConfigurationMessage)
		w.U32(configVersion)
		w.U32(uint32(len(config)))
		w.Raw(config)
		return w.Bytes()
	}
	// report sends every playing seat that spoke version 6 or later the
	// match's progress and restarts the interval (§16.5.2). Version-5 seats
	// receive exactly the stream they always did.
	report := func() {
		body := encodeHostedProgress(&progress, tick, func(slot int) time.Duration { return players[slot].rtt() })
		for slot := range progress.n {
			if p := players[slot]; progress.active[slot] && p.version >= hostedProgressVersion && p.report(body) {
				stats.bytesOut += uint64(len(body))
			}
		}
		reports.Reset(r.server.timeouts.report)
		reporting = reports.C
	}
	arm := func() {
		low, _ := progress.slowest()
		if started && !opening && progress.playing() > 0 && ready == nil && progress.terminal == 0 && tick-low < hostedMaxAhead {
			seal.Reset(max(0, time.Until(lastSeal.Add(localTickInterval))))
			ready = seal.C
		}
	}
	// Finish the presentation barrier with a fresh execution-progress budget.
	// No grant timer has run, so opening time creates no catch-up debt
	// (DESIGN_MULTIPLAYER §16.5.2).
	releaseOpening := func() {
		if !opening {
			return
		}
		for slot := range progress.n {
			if !openingReady[slot] {
				return
			}
		}
		opening = false
		now := time.Now()
		for slot := range progress.n {
			progress.last[slot] = now
		}
		deadline.Reset(r.server.timeouts.progress)
		arm()
	}
	// begin starts the match: present seats become slots in ascending seat
	// order, and each learns its slot before the first grant.
	begin := func() error {
		r.server.mu.Lock()
		r.started = true
		r.server.mu.Unlock()
		started, opening = true, true
		stats.startedAt = time.Now()
		r.server.recordStarted()
		for i := range r.size {
			if p := seats[i].peer; p != nil {
				p.slot = uint8(progress.n)
				players[progress.n] = p
				progress.active[progress.n] = true
				progress.last[progress.n] = stats.startedAt
				openingReady[progress.n] = p.version < hostedOpeningVersion
				progress.n++
			}
		}
		for slot := range progress.n {
			if err := enqueue(players[slot], []byte{hostedStartedMessage, uint8(slot)}, false); err != nil {
				return err
			}
		}
		deadline.Reset(r.server.timeouts.opening)
		reports.Reset(r.server.timeouts.report)
		reporting = reports.C
		releaseOpening()
		publish()
		return nil
	}
	if err := enqueue(r.creator, configuration(), false); err != nil {
		finish(err, finishProtocol)
		return
	}
	if err := lobby(); err != nil {
		finish(err, finishProtocol)
		return
	}
	for {
		select {
		case <-r.server.done:
			finish(hostedError("server", "an open server"), finishServer)
			return
		case <-deadline.C:
			if opening {
				finish(hostedError("opening readiness", "every seat to finish its opening within 30 minutes"), finishExpired)
			} else if started {
				finish(hostedError("execution progress", "execution progress from every seat within ten seconds"), finishStalled)
			} else {
				finish(hostedError("room wait", "a started match within 30 minutes"), finishExpired)
			}
			return
		case <-ready:
			ready = nil
			if tick == ^uint32(0) {
				finish(hostedError("grant tick", "a tick before uint32 exhaustion"), finishProtocol)
				return
			}
			tick++
			// Keep the 30 Hz phase when the timer wakes late, so wake-up delay
			// does not accumulate into sealing below 30 Hz. A longer gap, such
			// as a wait at the lead bound, restarts the phase without a burst.
			now := time.Now()
			if next := lastSeal.Add(localTickInterval); !lastSeal.IsZero() && now.Sub(next) < localTickInterval {
				lastSeal = next
			} else {
				lastSeal = now
			}
			sealed[tick%(hostedMaxAhead+1)] = now
			stats.tickAt(now)
			commands := pending.release(now)
			body := encodeLocalGrant(LocalGrant{Tick: tick, Position: pending.sealed, Commands: commands})
			var err error
			every(func(p *hostedPeer) {
				if err == nil {
					err = enqueue(p, body, false)
				}
			})
			if err != nil {
				finish(err, finishDisconnected)
				return
			}
			arm()
			if tick%30 == 0 {
				publish()
			}
		case <-reporting:
			report()
		case e := <-r.events:
			stats.bytesIn += uint64(len(e.body))
			if e.join {
				// A joiner takes the lowest colour no present seat holds; a
				// room has at least as many colours as seats.
				seat := e.peer.seat
				color := uint8(0)
				for held(color, int(seat)) {
					color++
				}
				seats[seat] = lobbySeat{peer: e.peer, color: color}
				clearReady()
				if r.autoStart && present() == r.size {
					for i := range r.size {
						seats[i].ready = true
					}
				}
				if err := enqueue(e.peer, configuration(), false); err != nil {
					finish(err, finishDisconnected)
					return
				}
				if err := lobby(); err != nil {
					finish(err, finishDisconnected)
					return
				}
				if r.autoStart && present() == r.size {
					if err := begin(); err != nil {
						finish(err, finishDisconnected)
						return
					}
				}
				continue
			}
			p := e.peer
			if (started && (players[p.slot] != p || !progress.active[p.slot])) || (!started && seats[p.seat].peer != p) {
				continue // a released or departed seat's late event
			}
			if e.err != nil {
				switch {
				case !started && p.seat != 0:
					// A joiner leaving the lobby frees its seat for another.
					_ = p.conn.Close()
					p.stop()
					seats[p.seat] = lobbySeat{}
					clearReady()
					r.server.mu.Lock()
					r.taken[p.seat] = false
					r.server.mu.Unlock()
					if err := lobby(); err != nil {
						finish(err, finishDisconnected)
						return
					}
				case !started:
					finish(hostedError("room host", "the host to stay until the match starts"), finishHostLeft)
					return
				case progress.final[p.slot]:
					// A defeated seat may leave; the match goes on without it.
					_ = p.conn.Close()
					p.stop()
					progress.active[p.slot] = false
					if progress.playing() == 0 {
						finish(nil, finishCompleted)
						return
					}
					complete, err := progress.compare()
					if err != nil {
						finish(err, finishDiverged)
						return
					}
					if complete {
						finish(nil, finishCompleted)
						return
					}
					report()
					arm()
					publish()
				default:
					finish(e.err, finishDisconnected)
					return
				}
				continue
			}
			d := netproto.NewReader(e.body, hostedError)
			switch kind := d.U8(); kind {
			case hostedOpeningReadyMessage:
				if err := d.End(); err != nil {
					finish(err, finishProtocol)
					return
				}
				if !started || p.version < hostedOpeningVersion {
					finish(hostedError("opening readiness", "a version-7 OpeningReady after Started"), finishProtocol)
					return
				}
				openingReady[p.slot] = true
				releaseOpening() // a duplicate marker changes nothing
			case hostedReadyMessage:
				flag := d.Bool()
				var identity, rehearsal [32]byte
				if flag {
					identity, rehearsal = d.Digest(), d.Digest()
				}
				if err := d.End(); err != nil {
					finish(err, finishProtocol)
					return
				}
				// After Start a late ready changes nothing (§16.6.1).
				if !started {
					s := &seats[p.seat]
					s.ready, s.identity, s.rehearsal = flag, identity, rehearsal
					if err := lobby(); err != nil {
						finish(err, finishDisconnected)
						return
					}
				}
			case hostedTeamMessage, hostedSideMessage:
				value := d.U8()
				if err := d.End(); err != nil {
					finish(err, finishProtocol)
					return
				}
				if kind == hostedTeamMessage && value > hostedMaxTeam {
					finish(hostedError("team", "no team or teams 1 to 5"), finishProtocol)
					return
				}
				if started || seats[p.seat].ready {
					continue
				}
				if kind == hostedTeamMessage {
					seats[p.seat].team = value
				} else {
					seats[p.seat].side = value
				}
				clearReady()
				if err := lobby(); err != nil {
					finish(err, finishDisconnected)
					return
				}
			case hostedColorMessage:
				value := d.U8()
				if err := d.End(); err != nil {
					finish(err, finishProtocol)
					return
				}
				if value >= HostedColors {
					finish(hostedError("colour", "colours 0 to 9"), finishProtocol)
					return
				}
				// A colour another present seat holds changes nothing, as a
				// change from a ready seat or after Start does (§16.6.1).
				if started || seats[p.seat].ready || held(value, int(p.seat)) {
					continue
				}
				seats[p.seat].color = value
				clearReady()
				if err := lobby(); err != nil {
					finish(err, finishDisconnected)
					return
				}
			case hostedConfigurationMessage:
				config := d.Raw(d.Count(hostedMaxConfigBytes, 1))
				if err := d.End(); err != nil {
					finish(err, finishProtocol)
					return
				}
				if started {
					continue
				}
				if p.seat != 0 || len(config) == 0 {
					finish(hostedError("room configuration", "a nonempty configuration from the room's host"), finishProtocol)
					return
				}
				r.server.mu.Lock()
				same := bytes.Equal(r.config, config)
				if !same {
					r.config = bytes.Clone(config)
				}
				r.server.mu.Unlock()
				if same {
					continue
				}
				configVersion++
				clearReady()
				body := configuration()
				var err error
				every(func(q *hostedPeer) {
					if err == nil {
						err = enqueue(q, body, false)
					}
				})
				if err == nil {
					err = lobby()
				}
				if err != nil {
					finish(err, finishDisconnected)
					return
				}
			case hostedStartMessage:
				if err := d.End(); err != nil {
					finish(err, finishProtocol)
					return
				}
				if started {
					continue
				}
				if p.seat != 0 {
					finish(hostedError("start", "the room's host"), finishProtocol)
					return
				}
				// A start that races a change, or meets seats whose digests
				// disagree, just repeats the state (§16.7).
				var err error
				if present() < 2 || !allReady() || mismatch() {
					err = lobby()
				} else {
					err = begin()
				}
				if err != nil {
					finish(err, finishDisconnected)
					return
				}
			case localSubmitMessage:
				sequence := d.U64()
				n := d.Count(netproto.MaxCommandBytes, 1)
				payload := d.Raw(n)
				if err := d.End(); err != nil {
					finish(err, finishProtocol)
					return
				}
				// Before Start only the host's seat, slot 0 in every match,
				// may queue commands for the first grant: the command-line
				// creator does while it waits in its battle.
				if !started && p.seat != 0 {
					finish(hostedError("command", "commands after the match starts"), finishProtocol)
					return
				}
				slot := 0
				if started {
					slot = int(p.slot)
				}
				if progress.terminal != 0 {
					// Input already in flight cannot add work after shared termination.
					continue
				}
				if sequence <= sequences[slot] {
					continue
				}
				if sequence != sequences[slot]+1 {
					err := hostedError("command sequence", fmt.Sprintf("sequence %d", sequences[slot]+1))
					if err := enqueue(p, localFailureBody(localRefusedMessage, err), false); err != nil {
						finish(err, finishDisconnected)
						return
					}
					continue
				}
				if len(pending.entries) == localMaxCommands || n > hostedMaxPendingBytes-pending.bytes || position == ^uint64(0) {
					finish(hostedError("pending commands", "at most 64 pending commands and 256 KiB"), finishFlooded)
					return
				}
				sequences[slot] = sequence
				position++
				pending.entries = append(pending.entries, localPendingCommand{command: LocalCommand{Seat: uint8(slot), Sequence: sequence, Position: position, Payload: payload}})
				pending.bytes += n
				stats.orderAt(time.Now())
			case localAckMessage:
				ack := hostedAck{tick: d.U32()}
				flags := d.U8()
				if flags > ackFlagsMask {
					d.Abort(hostedError("acknowledgment flags", "the battle-ended and seat-final bits"))
				}
				ack.ended = flags&ackBattleEnded != 0
				if ack.tick%30 == 0 {
					ack.check = d.Digest()
				}
				if err := d.End(); err != nil {
					finish(err, finishProtocol)
					return
				}
				if !started || opening {
					finish(hostedError("acknowledgment", "acknowledgments after every seat finishes its opening"), finishProtocol)
					return
				}
				now := time.Now()
				slot := int(p.slot)
				wasFinal := progress.final[slot]
				complete, err := progress.acknowledge(slot, tick, ack, flags&ackSeatFinal != 0, now)
				if err != nil {
					reason := finishProtocol
					if progress.acked[slot] == ack.tick {
						reason = finishDiverged
					}
					finish(err, reason)
					return
				}
				if at := sealed[ack.tick%(hostedMaxAhead+1)]; !at.IsZero() {
					stats.ackLag[slot] = now.Sub(at)
				}
				if complete {
					finish(nil, finishCompleted)
					return
				}
				if !wasFinal && progress.final[slot] {
					report()
				}
				_, oldest := progress.slowest()
				deadline.Reset(max(0, time.Until(oldest.Add(r.server.timeouts.progress))))
				if progress.terminal != 0 {
					seal.Stop()
					ready = nil
				}
				arm()
			default:
				finish(hostedError("client message", "ready, team, side, colour, configuration, start, opening-ready, submit or acknowledgment after hello"), finishProtocol)
				return
			}
		}
	}
}
