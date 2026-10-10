//go:build retail

package aikit_test

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	_ "github.com/nanolathe-gg/nanolathe/mods/aikit"
)

// These tests run the real Modern AI computer player (this package's
// util+tac controller, thinking on its worker goroutine) in an online
// battle. internal/session's own online tests cannot link this package, so
// their Modern rows play a stand-in step that runs the retail planner; here
// every Modern row is the controller a released client runs
// (DESIGN_MULTIPLAYER §5.1, §6.6; DESIGN_GAMEPLAY_RULES "The Modern AI
// controller").

// onlineModernMap offers ten start positions, so every composition below
// fits. The Modern AI develops on it as it does in single-player: a few units
// in the first two minutes, a small army and its first losses within six.
const onlineModernMap = "the pass"

// onlineModernSetup is two humans on teams 1 and 2, then the host's
// computers in stored order: a Modern computer on each human's team (medium
// on team 1, hard on team 2, so two personas with different reaction windows
// and think cadences), extra further Modern computers alternating between the
// two teams, and, when classic is set, a Classic computer on team 3.
func onlineModernSetup(cat *content.Catalog, extra int, classic bool) session.OnlineMatchSetup {
	setup := session.OnlineMatchSetup{
		MapName: onlineModernMap, SimSeed: 7, CRTSeed: 11, SideCount: len(session.OnlineSides(cat)),
		Seats: []session.OnlineSeat{{Team: 1, Side: 0, Color: 0}, {Team: 2, Side: 1, Color: 1}},
	}
	for i := range 2 + extra {
		setup.Computers = append(setup.Computers, session.OnlineComputer{
			Team: uint8(1 + i%2), Side: uint8((i + 1) % 2), Color: uint8(2 + i),
			Kind: ai.ControllerModern, Difficulty: uint8(1 + i%2),
		})
	}
	if classic {
		setup.Computers = append(setup.Computers, session.OnlineComputer{
			Team: 3, Side: 0, Color: uint8(4 + extra), Kind: ai.ControllerClassic, Difficulty: 1,
		})
	}
	return setup
}

// onlineModernRoom resolves and freezes setup the way the host seat does at
// Ready (DESIGN_MULTIPLAYER §16.6), with the room values the session's own
// online tests use.
func onlineModernRoom(t *testing.T, setup session.OnlineMatchSetup) (*content.SimulationInputs, session.EffectiveMatchConfig) {
	t.Helper()
	cat, fs := retailcat.Shared(t)
	room := session.MatchRoomInputs{
		ContentProfile: "retail",
		PlayerView:     session.MatchView{MinimumScale: 1024, MaximumScale: 2048},
		SpectatorView:  session.MatchView{MinimumScale: 256, MaximumScale: 2048, FullMap: true},
		ReplayView:     session.MatchView{MinimumScale: 256, MaximumScale: 2048, FullMap: true},
		Policies:       session.MatchPolicies{Revision: 1, Scheduling: 1, Pacing: 1, Drop: 1, Audience: 1, RejoinGraceMilliseconds: 90000},
	}
	schema, err := session.OnlineMapSchema(fs, cat, setup.MapName, setup.Rows())
	if err != nil {
		t.Fatal(err)
	}
	room.MapSchema = schema
	r, err := session.NewOnlineMatchRequest(setup, session.SkirmishEntryOptions{}, room)
	if err != nil {
		t.Fatal(err)
	}
	config, err := session.ResolveMatchConfig(r)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := session.FreezeMatchInputs(fs, cat, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	return inputs, config
}

// onlineModernJoiner is the joining seat's own composition inputs: the
// configuration decoded from the bytes the host sent, and content frozen
// from it independently.
func onlineModernJoiner(t *testing.T, config session.EffectiveMatchConfig) (*content.SimulationInputs, session.EffectiveMatchConfig) {
	t.Helper()
	payload, err := session.EncodeMatchConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := session.DecodeMatchConfig(payload)
	if err != nil {
		t.Fatal(err)
	}
	cat, fs := retailcat.Shared(t)
	inputs, err := session.FreezeMatchInputs(fs, cat, decoded, nil)
	if err != nil {
		t.Fatal(err)
	}
	return inputs, decoded
}

// onlineModernReplica composes one client's battle presenting seat and
// prepares it for granted ticks. Its controllers are closed when the test
// ends, so no worker outlives the battle.
func onlineModernReplica(t *testing.T, inputs *content.SimulationInputs, config session.EffectiveMatchConfig, seat uint8) *session.Session {
	t.Helper()
	s, err := session.NewPlaytestSkirmish(inputs, config, seat, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeModernHosts(s) })
	if err := s.PrepareGrantedBattle(); err != nil {
		t.Fatal(err)
	}
	return s
}

func closeModernHosts(s *session.Session) {
	for _, m := range s.AI {
		if m == nil {
			continue
		}
		if h, ok := m.Ext.(*aikit.Host); ok && h != nil {
			h.Close()
		}
	}
}

// modernHosts returns the real Modern controller of every computer row
// marked Modern, failing when a marked row runs anything else.
func modernHosts(t *testing.T, s *session.Session, setup session.OnlineMatchSetup) map[uint8]*aikit.Host {
	t.Helper()
	out := map[uint8]*aikit.Host{}
	for k, c := range setup.Computers {
		row := uint8(len(setup.Seats) + k)
		m := s.AI[row]
		if m == nil || m.Controller != c.Kind {
			t.Fatalf("row %d: manager %v, want controller %v", row, m, c.Kind)
		}
		if c.Kind != ai.ControllerModern {
			continue
		}
		h, ok := m.Ext.(*aikit.Host)
		if !ok || h == nil {
			t.Fatalf("row %d is marked Modern but runs %T, not the util+tac controller", row, m.Ext)
		}
		out[row] = h
	}
	return out
}

// The human seats' script is this test's protocol, not retail data: each
// commander moves toward the map's centre at tick 1, then builds the first
// structures on its own build menu, one every onlineBuildEvery ticks from
// onlineFirstBuild, each at the nearest legal site around where it stands.
// Every command is computed from one replica's state after the previous
// tick through seat-independent reads (unit positions and the authoritative
// placement test with no viewer), so every replica receives the same stream
// whichever replica computed it, as the relay would deliver it.
const (
	onlineFirstBuild   = 60
	onlineBuildEvery   = 390
	onlineBuilds       = 4
	onlineMoveDistance = 128 // world units toward the centre on each axis
	onlineSiteRings    = 12
)

type onlineStamped struct {
	stamp   session.CommandStamp
	command session.SeatCommand
}

type onlineScript struct {
	seats    []uint8
	leads    []pool.UnitRef
	position uint64
}

func newOnlineScript(s *session.Session, humans int) *onlineScript {
	sc := &onlineScript{}
	for seat := range humans {
		sc.seats = append(sc.seats, uint8(seat))
		var ref pool.UnitRef
		s.Units.ForEachPlayerSliceLive(seat, func(u *units.Unit) {
			if ref.Handle == 0 {
				ref = pool.UnitRef{Handle: u.Handle, Serial: u.AllocationSerial}
			}
		})
		sc.leads = append(sc.leads, ref)
	}
	return sc
}

func (sc *onlineScript) lead(s *session.Session, i int) *units.Unit {
	ref := sc.leads[i]
	u := s.Units.Unit(ref.Handle)
	if ref.Handle == 0 || u == nil || !u.Alive || u.Dying || u.AllocationSerial != ref.Serial || u.Def == nil {
		return nil
	}
	return u
}

// commands is the stream's human commands for tick, read from s.
func (sc *onlineScript) commands(s *session.Session, tick uint32) []onlineStamped {
	var out []onlineStamped
	for i, seat := range sc.seats {
		u := sc.lead(s, i)
		if u == nil {
			continue
		}
		var c session.SeatCommand
		switch {
		case tick == 1:
			x, z := towardCentre(u.X, s.World.CellW), towardCentre(u.Z, s.World.CellH)
			c = session.SeatCommand{Kind: session.SeatOrder, Order: session.OrderPayload{Actors: []pool.UnitRef{sc.leads[i]}, Code: 2,
				Position: session.CommandPosition{X: x, Y: s.World.HeightAt(x, z), Z: z}}}
		case tick >= onlineFirstBuild && (tick-onlineFirstBuild)%onlineBuildEvery == 0 && (tick-onlineFirstBuild)/onlineBuildEvery < onlineBuilds:
			def := buildChoice(s, u, int((tick-onlineFirstBuild)/onlineBuildEvery))
			if def == nil {
				continue
			}
			site, ok := buildSite(s, u, def)
			if !ok {
				continue
			}
			c = session.SeatCommand{Kind: session.SeatMobileBuild, MobileBuild: session.MobileBuildPayload{Builder: sc.leads[i], Product: def.CanonicalKey, Position: site}}
		default:
			continue
		}
		sc.position++
		out = append(out, onlineStamped{stamp: session.CommandStamp{Seat: seat, Tick: tick, Position: sc.position}, command: c})
	}
	return out
}

func towardCentre(at numeric.Fixed, cells int32) numeric.Fixed {
	size := numeric.Fixed(int64(cells) * 16 << 16)
	offset := numeric.Fixed(onlineMoveDistance << 16)
	if at > size/2 {
		offset = -offset
	}
	return min(max(at+offset, 32<<16), size-32<<16)
}

// buildChoice is the k-th structure on the builder's menu as the bound
// construction rules present it, wrapping around a short menu.
func buildChoice(s *session.Session, u *units.Unit, k int) *content.UnitDef {
	var structures []*content.UnitDef
	for _, key := range construction.BuildProducts(s.Rules.Construction, s.Catalog.BuildMenus[content.CanonicalKey(u.Def.UnitName)]) {
		if def, ok := s.Catalog.Unit(key); ok && def != nil && def.BMCode == 0 {
			structures = append(structures, def)
		}
	}
	if len(structures) == 0 {
		return nil
	}
	return structures[k%len(structures)]
}

// buildSite is the nearest legal site for def around u, searching square
// rings of cells beyond the builder's own footprint, each in row order.
func buildSite(s *session.Session, u *units.Unit, def *content.UnitDef) (session.CommandPoint, bool) {
	g, err := s.Build.StructureGeometry(def, units.FacingSouth)
	if err != nil {
		return session.CommandPoint{}, false
	}
	fx, fz := g.FootprintX, g.FootprintZ
	cx, cz := int32(u.X>>20)-fx/2, int32(u.Z>>20)-fz/2
	first := (max(fx, fz)+max(u.Def.FootprintX, u.Def.FootprintZ))/2 + 1
	for ring := first; ring < first+onlineSiteRings; ring++ {
		for dz := -ring; dz <= ring; dz++ {
			for dx := -ring; dx <= ring; dx++ {
				if max(dx, -dx, dz, -dz) != ring {
					continue
				}
				x, z := cx+dx, cz+dz
				result, err := s.PreviewPlacement(x, z, def, fx, fz, 0)
				if err != nil {
					continue
				}
				return session.CommandPoint{X: numeric.Fixed(int64(fx+2*x) << 19), Y: numeric.Fixed(int64(result.SiteHeight) << 16), Z: numeric.Fixed(int64(fz+2*z) << 19)}, true
			}
		}
	}
	return session.CommandPoint{}, false
}

// onlineRunDigest folds what a replica's granted tick produced into a
// running digest: each receipt's stream position and outcome, then every 30
// ticks the tick, the play-test unit checksum and both random streams' state
// and draw count, all little-endian. A run's digest therefore covers every
// 30-tick checksum the relay compares and the streams the computers' engine
// upkeep draws from.
type onlineRunDigest struct {
	h hash.Hash
}

func newOnlineRunDigest() *onlineRunDigest {
	d := &onlineRunDigest{h: sha256.New()}
	d.h.Write([]byte("nanolathe/test/online-modern-ai/v1"))
	return d
}

func (d *onlineRunDigest) receipts(rs []session.CommandReceipt) {
	var b [9]byte
	for _, r := range rs {
		binary.LittleEndian.PutUint64(b[:8], r.Stamp.Position)
		b[8] = byte(r.Outcome)
		d.h.Write(b[:])
	}
}

func (d *onlineRunDigest) sample(s *session.Session, tick uint32) {
	var b [8]byte
	binary.LittleEndian.PutUint32(b[:4], tick)
	d.h.Write(b[:4])
	sum := s.UnitStateChecksum()
	d.h.Write(sum[:])
	sim, crt := s.SimRNG(), s.CrtRNG()
	binary.LittleEndian.PutUint32(b[:4], sim.State)
	d.h.Write(b[:4])
	binary.LittleEndian.PutUint64(b[:], sim.Draws())
	d.h.Write(b[:])
	binary.LittleEndian.PutUint32(b[:4], crt.State)
	d.h.Write(b[:4])
	binary.LittleEndian.PutUint64(b[:], crt.Draws())
	d.h.Write(b[:])
}

func (d *onlineRunDigest) sum() string { return fmt.Sprintf("%x", d.h.Sum(nil)) }

// onlineModernOutcome is what one replica's run ended with: its digest, the
// tick it stopped at, the final partial fingerprint, and per Modern row the
// command outcomes its controller applied and its private generator's
// position.
type onlineModernOutcome struct {
	digest      string
	tick        uint32
	fingerprint string
	stats       map[uint8]aikit.ApplyStats
	generators  map[uint8]uint64
}

// finishOnlineRun closes a replica's run after its last tick.
func finishOnlineRun(t *testing.T, s *session.Session, setup session.OnlineMatchSetup, d *onlineRunDigest, tick uint32) onlineModernOutcome {
	t.Helper()
	d.sample(s, tick)
	fp, err := s.PartialStateFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	d.h.Write([]byte(fp))
	out := onlineModernOutcome{tick: tick, fingerprint: fp, stats: map[uint8]aikit.ApplyStats{}, generators: map[uint8]uint64{}}
	for row, h := range modernHosts(t, s, setup) {
		out.stats[row] = h.Stats()
		gen, ok := h.Generator()
		if !ok {
			t.Fatalf("row %d's controller never began", row)
		}
		out.generators[row] = gen
	}
	// Rows are folded in ascending order, never in map order.
	for row := uint8(0); row < 10; row++ {
		if st, ok := out.stats[row]; ok {
			fmt.Fprintf(d.h, "row %d %+v generator %d\n", row, st, out.generators[row])
		}
	}
	out.digest = d.sum()
	return out
}

// runOnlineLockstep steps every replica through the same granted ticks with
// the same stamped human commands, computed from replicas[0], and compares
// the replicas' unit checksums and both random streams every 30 ticks and
// their receipts every tick, failing at the first 30-tick sample where they
// part. A battle that ends early stops every replica at the same tick.
func runOnlineLockstep(t *testing.T, replicas []*session.Session, setup session.OnlineMatchSetup, ticks uint32) []onlineModernOutcome {
	t.Helper()
	script := newOnlineScript(replicas[0], len(setup.Seats))
	digests := make([]*onlineRunDigest, len(replicas))
	for i := range digests {
		digests[i] = newOnlineRunDigest()
	}
	compare := func(tick uint32) {
		a := replicas[0]
		for i, b := range replicas[1:] {
			if a.UnitStateChecksum() != b.UnitStateChecksum() {
				t.Fatalf("tick %d: replica %d's unit checksum differs from replica 0's", tick, i+1)
			}
			if *a.SimRNG() != *b.SimRNG() || *a.CrtRNG() != *b.CrtRNG() {
				t.Fatalf("tick %d: replica %d's random streams differ from replica 0's (sim %d/%d draws, CRT %d/%d)",
					tick, i+1, a.SimRNG().Draws(), b.SimRNG().Draws(), a.CrtRNG().Draws(), b.CrtRNG().Draws())
			}
		}
	}
	tick := uint32(0)
	for tick < ticks {
		if replicas[0].State != session.StateBattle || replicas[0].OnlineBattleEnded() {
			break
		}
		tick++
		cmds := script.commands(replicas[0], tick)
		for i, s := range replicas {
			for _, c := range cmds {
				if err := s.EnqueueSeatCommand(c.stamp, c.command); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.StepGranted(tick); err != nil {
				t.Fatal(err)
			}
			digests[i].receipts(s.DrainCommandReceipts())
			if tick%30 == 0 {
				digests[i].sample(s, tick)
			}
		}
		if tick%30 == 0 {
			compare(tick)
		}
		for i, s := range replicas[1:] {
			if s.OnlineBattleEnded() != replicas[0].OnlineBattleEnded() {
				t.Fatalf("tick %d: replica %d's battle end differs", tick, i+1)
			}
		}
	}
	compare(tick)
	out := make([]onlineModernOutcome, len(replicas))
	for i, s := range replicas {
		out[i] = finishOnlineRun(t, s, setup, digests[i], tick)
	}
	return out
}

// lockstepCensus summarises what the battle did, for the log and for the
// assertions that the computers built and fought.
type lockstepCensus struct {
	created, live [10]int
}

func censusOf(s *session.Session) lockstepCensus {
	var c lockstepCensus
	for p := range 10 {
		c.created[p] = int(s.Units.CreatedCountForPlayer(p))
		c.live[p] = s.Units.LiveCountForPlayer(p)
	}
	return c
}

// onlineModernSpan is six minutes of battle. The Modern AI opens slowly, as
// it does in single-player, and needs this long to build a small army and
// lose units in fights; on the way the Classic computer and seat 0, which
// hosts every computer, are destroyed, so seat 0's computers play on with its
// perspective under the Modern seat policy (DESIGN_MULTIPLAYER §6.6). Two
// clients cost about three seconds natively.
const onlineModernSpan = 10800

// Two humans and three computers hosted by seat 0 — a Modern computer on
// each human's team, medium on one and hard on the other, and a Classic
// computer on a third team — play six minutes alike on two independent
// clients: the host composes from its resolved configuration, the joiner
// from the configuration's bytes with content it froze itself, and each
// presents its own seat. Both receive the same human command stream through
// the granted path, and their unit checksums and both random streams agree
// at every 30-tick sample; at the end their receipts digests, partial
// fingerprints, each Modern controller's applied-command outcomes and the
// position of its private generator agree too. The Modern computers run the
// real util+tac controller, thinking on its worker (DESIGN_MULTIPLAYER §5.1,
// §6.6).
func TestOnlineModernComputersAgreeAcrossClientsRetail(t *testing.T) {
	cat, _ := retailcat.Shared(t)
	setup := onlineModernSetup(cat, 0, true)
	inputs, config := onlineModernRoom(t, setup)
	joinerInputs, joinerConfig := onlineModernJoiner(t, config)
	a := onlineModernReplica(t, inputs, config, 0)
	b := onlineModernReplica(t, joinerInputs, joinerConfig, 1)
	for _, s := range []*session.Session{a, b} {
		if hosts := modernHosts(t, s, setup); len(hosts) != 2 {
			t.Fatalf("%d Modern controllers, want 2", len(hosts))
		}
		for row := uint8(2); row < 5; row++ {
			if s.Vis.PerspectiveOf(visibility.PlayerID(row)) != 0 {
				t.Fatalf("computer %d does not borrow seat 0's perspective", row)
			}
		}
	}
	begin := time.Now()
	out := runOnlineLockstep(t, []*session.Session{a, b}, setup, onlineModernSpan)
	elapsed := time.Since(begin)
	if out[0].tick != onlineModernSpan {
		t.Fatalf("the battle ended at tick %d, before the span", out[0].tick)
	}
	if out[0].digest != out[1].digest || out[0].fingerprint != out[1].fingerprint {
		t.Fatalf("the clients agreed at every sample but ended apart: digests %s and %s", out[0].digest, out[1].digest)
	}
	for row, st := range out[0].stats {
		if out[1].stats[row] != st || out[1].generators[row] != out[0].generators[row] {
			t.Fatalf("row %d's controller applied %+v (generator %d) on one client and %+v (generator %d) on the other",
				row, st, out[0].generators[row], out[1].stats[row], out[1].generators[row])
		}
	}
	c := censusOf(a)
	t.Logf("%d lockstep ticks of two clients in %v; created %v, live %v; digest %s", out[0].tick, elapsed, c.created[:5], c.live[:5], out[0].digest)
	for _, row := range []uint8{2, 3} {
		t.Logf("Modern computer %d: %+v", row, out[0].stats[row])
		if c.created[row] < 8 || out[0].stats[row].Applied < 10 {
			t.Fatalf("Modern computer %d created %d units and applied %d commands in six minutes", row, c.created[row], out[0].stats[row].Applied)
		}
	}
	if lost := c.created[2] - c.live[2] + c.created[3] - c.live[3]; lost == 0 {
		t.Fatal("the Modern computers lost no unit: the span never reached a fight")
	}
}
