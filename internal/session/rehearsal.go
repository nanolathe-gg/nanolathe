package session

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The pre-start rehearsal of DESIGN_MULTIPLAYER §16.7. Before a seat readies,
// it composes a private battle from the match's frozen inputs and
// configuration, runs a fixed script of ordinary seat commands for every
// human seat and reports the digest of what happened. The relay starts the
// match only when every seat reports the same digest, so clients whose
// simulations disagree on this content are refused before play rather than
// at the first 30-tick unit checksum. It is a behavioral check of the build and the
// content together: it needs no stamp and vouches only for what it ran.
//
// Everything here is Nanolathe protocol, not retail behavior: the script,
// its timing and geometry constants and the digest encoding. Changing any of
// them changes every digest; such a change is a protocol change and bumps
// rehearsalDomain with it.

// rehearsalDomain separates the rehearsal digest from every other digest.
const rehearsalDomain = "nanolathe/rehearsal/v2"

// The script's timing and geometry. Ticks are granted ticks at 30 Hz.
const (
	// rehearsalTicks is a skirmish rehearsal's length, and a Survival
	// rehearsal's least: 30 seconds of battle.
	rehearsalTicks = 900
	// rehearsalMoveTick is when each seat's lead unit is ordered to move.
	rehearsalMoveTick = 1
	// rehearsalBuildTick is when each lead unit is ordered to build.
	rehearsalBuildTick = 90
	// rehearsalAttackTick is when each lead unit is ordered to fire at its
	// own build site.
	rehearsalAttackTick = 480
	// rehearsalMoveDistance is the move's offset on each axis toward the
	// map's centre, in world units.
	rehearsalMoveDistance = 128
	// rehearsalMoveMargin keeps a move target this many world units inside
	// the map's edges.
	rehearsalMoveMargin = 32
	// rehearsalSiteRings is how many square rings of footprint cells the
	// build-site search visits beyond the first ring whose sites clear the
	// builder's own footprint.
	rehearsalSiteRings = 10
	// rehearsalWaveTicks bounds a Survival rehearsal: it stops once the
	// first wave has spawned, and at the latest this many ticks after the
	// director's planned arrival (its first-wave delay plus its warning).
	rehearsalWaveTicks = 300
)

// RehearsalDigest runs the room's pre-start rehearsal: a short deterministic
// battle composed from the same frozen inputs and configuration as the match,
// driven by a fixed script of commands for every human seat, and returns the
// digest of its final state (DESIGN_MULTIPLAYER §16.7). Seats whose
// simulations agree report the same digest; the relay starts the match only
// then. A skirmish rehearsal runs rehearsalTicks ticks; a Survival rehearsal
// runs at least as long and on until the director's first wave has spawned,
// so the wave director is part of what it checks.
//
// The configuration's computer rows simply play, as they will in the match:
// the script drives only the human seats, and every computer runs its own
// controller from the rehearsal's own state.
//
// The rehearsal composes its own session through the match's constructor,
// NewPlaytestSkirmish, and never touches another: the caller's match
// session, composed from the same inputs, is unaffected. Sessions only read
// their inputs — the catalog, models and animation table are immutable and
// the sealed view locks its own lookups — so one value backs the match and
// its rehearsal, and the rehearsal may run on the caller's goroutine of
// choice beside an idle or running match. It starts no goroutine of its own;
// a Modern computer's controller thinks on its own worker as in any battle,
// joined at the tick its plan is due, and the rehearsal closes it before
// returning.
//
// The digest is independent of the local seat, of local preferences and of
// host timing: the rehearsal always composes as seat 0, a seat's commands are
// computed from the rehearsal's own state alone, and nothing reads a clock.
func RehearsalDigest(inputs *content.SimulationInputs, config EffectiveMatchConfig) ([32]byte, error) {
	return rehearse(inputs, config, 0, nil)
}

// rehearse runs the rehearsal composed for localSeat. Production always
// passes seat 0; the tests pass the other seat to show that the digest does
// not depend on it. observe, when non-nil, sees the session after every
// granted tick and must not change it.
func rehearse(inputs *content.SimulationInputs, config EffectiveMatchConfig, localSeat uint8, observe func(*Session)) ([32]byte, error) {
	s, err := NewPlaytestSkirmish(inputs, config, localSeat, nil)
	if err != nil {
		return [32]byte{}, err
	}
	defer s.closeAIControllers()
	if err := s.PrepareGrantedBattle(); err != nil {
		return [32]byte{}, err
	}
	r := rehearsal{s: s, h: sha256.New()}
	r.h.Write([]byte(rehearsalDomain))
	r.findLeads(config)
	tick := uint32(0)
	// A battle that ends early, such as by a commander's death, stops the
	// script at that tick on every replica.
	for !r.done(tick) && s.State == StateBattle && !s.OnlineBattleEnded() {
		tick++
		if err := r.issue(tick); err != nil {
			return [32]byte{}, err
		}
		if err := s.StepGranted(tick); err != nil {
			return [32]byte{}, err
		}
		r.recordTick()
		if observe != nil {
			observe(s)
		}
	}
	return r.finish(tick)
}

// done reports whether the rehearsal has run its course after tick ticks:
// rehearsalTicks for a skirmish; for Survival also until the first wave has
// spawned, bounded by rehearsalWaveTicks past its planned arrival.
func (r *rehearsal) done(tick uint32) bool {
	if tick < rehearsalTicks {
		return false
	}
	st := r.s.Survival
	if st == nil {
		return true
	}
	return st.firstWaveSpawned() || tick >= st.tuning.FirstWaveDelay+st.tuning.WarningTime+rehearsalWaveTicks
}

// firstWaveSpawned reports whether the director has created its first wave:
// the spawn cursor has passed every group of wave 1, or a later phase or
// wave has begun (DESIGN_SURVIVAL §6.1, §6.5).
func (st *survivalState) firstWaveSpawned() bool {
	switch {
	case st.wave > 1:
		return true
	case st.wave == 1 && st.phase == survivalActive:
		return st.nextG >= len(st.plan.Groups)
	case st.wave == 1 && st.phase == survivalDowntime:
		return true
	}
	return false
}

// rehearsal is one running rehearsal: its session, the running digest, the
// last stream position it assigned, and for each human seat, in slot order,
// its slot, lead unit and build site.
type rehearsal struct {
	s        *Session
	h        hash.Hash
	position uint64
	seats    []uint8
	leads    []pool.UnitRef
	sites    []CommandPoint
	sited    []bool
}

// findLeads lists the configuration's human rows and picks each one's lead
// unit: its first live unit in slice order at battle entry, which is the
// commander an ordinary start creates first. A seat with no unit has a null
// lead, and its commands are skipped.
func (r *rehearsal) findLeads(config EffectiveMatchConfig) {
	for i := range config.request.Seats {
		if config.request.Seats[i].Role == MatchRoleHuman {
			r.seats = append(r.seats, uint8(i))
		}
	}
	r.leads = make([]pool.UnitRef, len(r.seats))
	r.sites = make([]CommandPoint, len(r.seats))
	r.sited = make([]bool, len(r.seats))
	for i, seat := range r.seats {
		found := false
		r.s.Units.ForEachPlayerSliceLive(int(seat), func(u *units.Unit) {
			if !found {
				r.leads[i] = pool.UnitRef{Handle: u.Handle, Serial: u.AllocationSerial}
				found = true
			}
		})
	}
}

// lead returns the lead unit of the i-th human seat while that allocation is
// alive.
func (r *rehearsal) lead(i int) *units.Unit {
	ref := r.leads[i]
	if ref.Handle == 0 {
		return nil
	}
	u := r.s.Units.Unit(ref.Handle)
	if u == nil || !u.Alive || u.AllocationSerial != ref.Serial || u.Def == nil {
		return nil
	}
	return u
}

// issue enqueues the script's commands for tick (commands).
func (r *rehearsal) issue(tick uint32) error {
	for _, c := range r.commands(tick) {
		if err := r.s.EnqueueSeatCommand(c.stamp, c.command); err != nil {
			return err
		}
	}
	return nil
}

// rehearsalCommand is one stamped command of the script.
type rehearsalCommand struct {
	stamp   CommandStamp
	command SeatCommand
}

// commands is the script's commands for tick, human seats in slot order,
// stamped as one stream would order them. Each command is computed from the
// rehearsal's state after the previous tick, which every replica holds
// identically. The script is the same for every seat: move toward the map's
// centre, build the first structure on the lead's own build list at the
// nearest known legal site, and fire at that site with an ordinary attack
// order. A command the session refuses or cannot carry out is refused
// identically on every replica.
func (r *rehearsal) commands(tick uint32) []rehearsalCommand {
	var out []rehearsalCommand
	for i, seat := range r.seats {
		u := r.lead(i)
		if u == nil {
			continue
		}
		ref := r.leads[i]
		var c SeatCommand
		switch tick {
		case rehearsalMoveTick:
			x, z := r.moveTarget(u)
			c = SeatCommand{Kind: SeatOrder, Order: OrderPayload{Actors: []pool.UnitRef{ref}, Code: 2,
				Position: CommandPosition{X: x, Y: r.s.World.HeightAt(x, z), Z: z}}}
		case rehearsalBuildTick:
			def := r.product(u)
			if def == nil {
				continue
			}
			site, ok := r.site(i, u, def)
			if !ok {
				continue
			}
			r.sites[i], r.sited[i] = site, true
			c = SeatCommand{Kind: SeatMobileBuild, MobileBuild: MobileBuildPayload{Builder: ref, Product: def.CanonicalKey, Position: site}}
		case rehearsalAttackTick:
			if !r.sited[i] {
				continue
			}
			site := r.sites[i]
			c = SeatCommand{Kind: SeatOrder, Order: OrderPayload{Actors: []pool.UnitRef{ref}, Code: 3,
				Position: CommandPosition{X: site.X, Y: site.Y, Z: site.Z}}}
		default:
			continue
		}
		r.position++
		out = append(out, rehearsalCommand{stamp: CommandStamp{Seat: seat, Tick: tick, Position: r.position}, command: c})
	}
	return out
}

// moveTarget is the lead's position moved rehearsalMoveDistance toward the
// map's centre on each axis, kept inside the map.
func (r *rehearsal) moveTarget(u *units.Unit) (numeric.Fixed, numeric.Fixed) {
	step := func(at numeric.Fixed, cells int32) numeric.Fixed {
		size := numeric.Fixed(int64(cells) * 16 << 16)
		offset := numeric.Fixed(rehearsalMoveDistance << 16)
		if at > size/2 {
			offset = -offset
		}
		lo, hi := numeric.Fixed(rehearsalMoveMargin<<16), size-numeric.Fixed(rehearsalMoveMargin<<16)
		return min(max(at+offset, lo), hi)
	}
	return step(u.X, r.s.World.CellW), step(u.Z, r.s.World.CellH)
}

// product is the first structure on the lead's own build list, as the bound
// construction rules present it, or nil when it has none.
func (r *rehearsal) product(u *units.Unit) *content.UnitDef {
	for _, key := range r.s.buildProducts(u.Def.UnitName) {
		if def, ok := r.s.Catalog.Unit(key); ok && def != nil && def.BMCode == 0 {
			return def
		}
	}
	return nil
}

// site finds the nearest legal, known build site for def around u, the lead
// of the i-th human seat, searching
// square rings of cells nearest first and each ring in row order. The first
// ring is the nearest whose sites clear the builder's own footprint, so the
// builder never stands on its site. It tests placement through the issuing
// seat's own knowledge and passes no builder, so the answer never depends on
// the local seat, and it requires the exact known-site admission phase 1
// applies to the command it becomes.
func (r *rehearsal) site(i int, u *units.Unit, def *content.UnitDef) (CommandPoint, bool) {
	s, seat := r.s, r.seats[i]
	geometry, err := s.Build.StructureGeometry(def, units.FacingSouth)
	if err != nil {
		return CommandPoint{}, false
	}
	fx, fz := geometry.FootprintX, geometry.FootprintZ
	cx, cz := int32(u.X>>20)-fx/2, int32(u.Z>>20)-fz/2
	viewer := &sessionPlacementViewer{vis: s.Vis, local: seat, player: seat}
	first := (max(fx, fz)+max(u.Def.FootprintX, u.Def.FootprintZ))/2 + 1
	for ring := first; ring < first+rehearsalSiteRings; ring++ {
		for dz := -ring; dz <= ring; dz++ {
			for dx := -ring; dx <= ring; dx++ {
				if max(abs32(dx), abs32(dz)) != ring {
					continue
				}
				x, z := cx+dx, cz+dz
				result, err := s.previewPlacement(x, z, def, fx, fz, 0, viewer, units.FacingSouth)
				if err != nil {
					continue
				}
				point := CommandPoint{X: numeric.Fixed(int64(fx+2*x) << 19), Y: numeric.Fixed(int64(result.SiteHeight) << 16), Z: numeric.Fixed(int64(fz+2*z) << 19)}
				if s.onlineBuildSiteKnown(seat, MobileBuildPayload{Builder: r.leads[i], Product: def.CanonicalKey, Position: point}) {
					return point, true
				}
			}
		}
	}
	return CommandPoint{}, false
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// recordTick folds one granted tick into the digest: each receipt phase 1
// produced, as its stream position u64 and outcome u8; the play-test unit
// checksum (DESIGN_MULTIPLAYER §16.4); then the simulation stream's state u32
// and draw count u64 and the CRT stream's state u32 and draw count u64, all
// little-endian.
func (r *rehearsal) recordTick() {
	var b [8]byte
	for _, receipt := range r.s.DrainCommandReceipts() {
		binary.LittleEndian.PutUint64(b[:], receipt.Stamp.Position)
		r.h.Write(b[:8])
		r.h.Write([]byte{byte(receipt.Outcome)})
	}
	sum := r.s.UnitStateChecksum()
	r.h.Write(sum[:])
	sim, crt := r.s.SimRNG(), r.s.CrtRNG()
	binary.LittleEndian.PutUint32(b[:4], sim.State)
	r.h.Write(b[:4])
	binary.LittleEndian.PutUint64(b[:], sim.Draws())
	r.h.Write(b[:])
	binary.LittleEndian.PutUint32(b[:4], crt.State)
	r.h.Write(b[:4])
	binary.LittleEndian.PutUint64(b[:], crt.Draws())
	r.h.Write(b[:])
}

// finish closes the digest with the number of ticks run, u32 little-endian,
// and the text of the final state's partial fingerprint, which adds what the
// unit checksum omits: the units' scripts, pieces, orders and weapon slots,
// the economy, pending paths and routes, and the features.
func (r *rehearsal) finish(ticks uint32) ([32]byte, error) {
	fingerprint, err := r.s.PartialStateFingerprint()
	if err != nil {
		return [32]byte{}, fmt.Errorf("nanolathe: rehearsal: %w", err)
	}
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], ticks)
	r.h.Write(b[:])
	r.h.Write([]byte(fingerprint))
	var out [32]byte
	r.h.Sum(out[:0])
	return out, nil
}
