package visibility

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// A hosted computer borrows its host seat's perspective online, as retail's
// hosted computer reads its machine's sensor picture (DESIGN_MULTIPLAYER
// §6.2, §6.6) [03 R-VIS-01 §4]: its status reads are its host's bank, sonar
// exemption included, and its host's pass runs pass 4 for its units.
func TestHostedComputerReadsItsHostsSensorBank(t *testing.T) {
	s := newTestService(&world.Terrain{CellW: 64, CellH: 64}, ModeHistoryEnabled|ModeCurrentEnabled)
	s.EnableOwnerPerspectives()
	if !s.SetPerspectiveHost(2, 0) {
		t.Fatal("seat 0 cannot host player 2")
	}
	// One level only, a valid other player, never the host itself.
	for _, tc := range [][2]PlayerID{{2, 2}, {3, 2}, {0, 1}, {10, 0}, {3, 10}} {
		if s.SetPerspectiveHost(tc[0], tc[1]) {
			t.Fatalf("SetPerspectiveHost(%d, %d) was admitted", tc[0], tc[1])
		}
	}
	for player, want := range []PlayerID{0, 1, 0, 3, 4, 5, 6, 7, 8, 9} {
		if got := s.PerspectiveOf(PlayerID(player)); got != want {
			t.Fatalf("player %d reads perspective %d, want %d", player, got, want)
		}
	}

	flags := [3]uint32{0x1700, 0x1700, 0x1700}
	deadlines := [3]uint32{5, 6, 7}
	units := []SensorUnit{
		// The host's own unit, the opponent's unit and the hosted computer's
		// cloak-capable unit, which has the opponent's unit on its own
		// primary list and within its minimum cloak distance.
		{ID: 1, AllocationSerial: 101, Owner: 0, Status: &flags[0], X: 64 << 16, Z: 64 << 16, Alive: true,
			OwnerLocallySimulated: true, DecloakDeadline: &deadlines[0]},
		{ID: 2, AllocationSerial: 102, Owner: 1, Status: &flags[1], X: 96 << 16, Z: 64 << 16, Alive: true,
			OwnerLocallySimulated: true, PrimaryCandidateOf: 1 << 2, DecloakDeadline: &deadlines[1]},
		{ID: 3, AllocationSerial: 103, Owner: 2, Status: &flags[2], X: 112 << 16, Z: 64 << 16, Alive: true,
			CanCloak: true, MinCloakDistance: 100, OwnerLocallySimulated: true, DecloakDeadline: &deadlines[2]},
	}
	same := func(when string) {
		t.Helper()
		for _, u := range units {
			if got, want := s.StatusForPerspective(2, u.ID, u.AllocationSerial, u.Owner, 0x40000000), s.StatusForPerspective(0, u.ID, u.AllocationSerial, u.Owner, 0x40000000); got != want {
				t.Fatalf("%s: the computer reads unit %d as %#x, its host as %#x", when, u.ID, got, want)
			}
		}
	}
	same("before any pass")
	// The host's pass is the one that simulates the computer's units: pass 4
	// suppresses its cloak and writes the shared deadline [03 R-VIS-01 §4].
	s.SensorTickForPerspective(0, false, 30, 3, units)
	if deadlines != [3]uint32{5, 6, 30 + DecloakDeadlineAdd} {
		t.Fatalf("host pass deadlines %v", deadlines)
	}
	if s.StatusForPerspective(2, 3, 103, 2, 0)&DecloakBit == 0 {
		t.Fatal("the computer does not read its own unit's suppression")
	}
	same("after the host's pass")
	// The opponent's pass is another machine's: it never runs pass 4 for the
	// computer's units, and the computer never reads the opponent's bank.
	deadlines = [3]uint32{5, 6, 7}
	s.SensorTickForPerspective(1, false, 60, 3, units)
	if deadlines != [3]uint32{5, 6, 7} {
		t.Fatalf("an unrelated seat's pass simulated the computer's units: %v", deadlines)
	}
	same("after the opponent's pass")
	if flags != [3]uint32{0x1700, 0x1700, 0x1700} {
		t.Fatal("perspective passes changed the callers' status words")
	}
}

// Without the host relation the same pass leaves the computer's units to
// nobody, as the first online slice did: pass 4 is the host's own units only.
func TestUnhostedComputerIsNoSeatsSource(t *testing.T) {
	s := newTestService(&world.Terrain{CellW: 64, CellH: 64}, ModeHistoryEnabled|ModeCurrentEnabled)
	s.EnableOwnerPerspectives()
	flags := [2]uint32{0x1700, 0x1700}
	deadlines := [2]uint32{6, 7}
	units := []SensorUnit{
		{ID: 2, AllocationSerial: 102, Owner: 1, Status: &flags[0], X: 96 << 16, Z: 64 << 16, Alive: true,
			OwnerLocallySimulated: true, PrimaryCandidateOf: 1 << 2, DecloakDeadline: &deadlines[0]},
		{ID: 3, AllocationSerial: 103, Owner: 2, Status: &flags[1], X: 112 << 16, Z: 64 << 16, Alive: true,
			CanCloak: true, MinCloakDistance: 100, OwnerLocallySimulated: true, DecloakDeadline: &deadlines[1]},
	}
	s.SensorTickForPerspective(0, false, 30, 3, units)
	if deadlines != [2]uint32{6, 7} {
		t.Fatalf("seat 0 simulated an unhosted owner's units: %v", deadlines)
	}
}

// Under Permanent LOS a query on behalf of a hosted computer reads its host's
// explored-history bit, retail's local player's [03 §3.2] C8 step 4, while
// current coverage stays the querying record's own grid.
func TestHostedComputerReadsItsHostsHistory(t *testing.T) {
	terrain := &world.Terrain{CellW: 64, CellH: 64}
	s := newTestService(terrain, ModeHistoryEnabled)
	s.EnableOwnerPerspectives()
	s.SetPerspectiveHost(2, 0)
	const x, z = 200 << 16, 200 << 16
	idx := int((200>>5)*s.W + (200 >> 5))
	s.wordMask[idx] |= cellBit(0)
	if !s.VisiblePoint(2, x, 0, z) || !s.VisiblePoint(0, x, 0, z) {
		t.Fatal("the host's explored cell is hidden from it or its computer")
	}
	if s.VisiblePoint(3, x, 0, z) || s.VisiblePoint(1, x, 0, z) {
		t.Fatal("another player reads the host's history")
	}
	s.wordMask[idx] = cellBit(2)
	if s.VisiblePoint(2, x, 0, z) {
		t.Fatal("the computer read its own history bit instead of its host's")
	}

	current := newTestService(terrain, ModeHistoryEnabled|ModeCurrentEnabled)
	current.EnableOwnerPerspectives()
	current.SetPerspectiveHost(2, 0)
	current.byteGrids[0][idx] = 1
	if current.VisiblePoint(2, x, 0, z) {
		t.Fatal("the computer read its host's current coverage")
	}
	current.byteGrids[2][idx] = 1
	if !current.VisiblePoint(2, x, 0, z) {
		t.Fatal("the computer lost its own current coverage")
	}
}

// Single-player battles never set a host, and the relation is inert without
// owner perspectives: every read keeps the local slot's history bit and the
// unit's own status word.
func TestPerspectiveHostIsInertOffline(t *testing.T) {
	s := newTestService(&world.Terrain{CellW: 64, CellH: 64}, ModeHistoryEnabled)
	s.SetPerspectiveHost(2, 0)
	s.SetLocal(1)
	if got := s.StatusForPerspective(2, 3, 103, 2, 0xa5a5); got != 0xa5a5 {
		t.Fatalf("offline status %#x", got)
	}
	if got := s.historyPlayer(2); got != 1 {
		t.Fatalf("offline history player %d, want the local slot", got)
	}
}

// Nanolathe Modern seat policy (DESIGN_MULTIPLAYER §6.6, 2026-10-09): while a
// defeated seat hosts a computer with a live unit, its pass is an ordinary
// viewer's, so the computer that reads it keeps normal sight; once the
// computer is gone, or with the policy off (retail's machine, whose computers
// stop with it), the defeated viewer marks every unit friendly
// [03 R-VIS-01 §4] pass 1.
func TestDefeatedHostKeepsSightWhileHosting(t *testing.T) {
	pass := func(keep bool, computerAlive bool) uint32 {
		s := newTestService(&world.Terrain{CellW: 64, CellH: 64}, ModeHistoryEnabled|ModeCurrentEnabled)
		s.EnableOwnerPerspectives()
		s.SetPerspectiveHost(2, 0)
		s.SetDefeatedHostKeepsSight(keep)
		flags := [2]uint32{0x1700, 0x1700}
		units := []SensorUnit{
			// An opponent's unit nobody sees, and the hosted computer's unit.
			{ID: 1, AllocationSerial: 101, Owner: 1, Status: &flags[0], X: 400 << 16, Z: 400 << 16, Alive: true},
			{ID: 2, AllocationSerial: 102, Owner: 2, Status: &flags[1], X: 64 << 16, Z: 64 << 16, Alive: computerAlive},
		}
		s.SensorTickForPerspective(0, true, 30, 3, units)
		return s.StatusForPerspective(2, 1, 101, 1, 0)
	}
	if got := pass(true, true); got&FriendlyMask != 0 {
		t.Fatalf("a defeated host with a live computer marked the unseen opponent %#x", got)
	}
	if got := pass(true, false); got&FriendlyMask != FriendlyMask {
		t.Fatalf("a defeated host whose computers are gone kept an ordinary pass: %#x", got)
	}
	if got := pass(false, true); got&FriendlyMask != FriendlyMask {
		t.Fatalf("retail's defeated machine kept an ordinary pass: %#x", got)
	}
	// An undefeated host is unaffected either way, and the policy never
	// turns an ordinary viewer into a defeated one.
	s := newTestService(&world.Terrain{CellW: 64, CellH: 64}, ModeHistoryEnabled|ModeCurrentEnabled)
	s.EnableOwnerPerspectives()
	s.SetPerspectiveHost(2, 0)
	s.SetDefeatedHostKeepsSight(true)
	flags := uint32(0x1700)
	s.SensorTickForPerspective(0, false, 30, 3, []SensorUnit{{ID: 1, AllocationSerial: 101, Owner: 1, Status: &flags, X: 400 << 16, Z: 400 << 16, Alive: true}})
	if got := s.StatusForPerspective(2, 1, 101, 1, 0); got&FriendlyMask != 0 {
		t.Fatalf("an ordinary pass marked an unseen opponent %#x", got)
	}
}
