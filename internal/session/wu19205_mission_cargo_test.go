package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The `i name` verb has exactly one owner: the InitialMission interpreter,
// which boards the ACTING unit into the NAMED carrier [04 §3.6]. Battle entry
// used to re-read the same verbs afterwards with those two roles reversed,
// which left the interpreter's link standing and added the opposite one — a
// two-way cycle that breaks attachment movement, unloading, and the restore
// walk's `cyclic unit reference` guard (WU-19-205, review finding R09). Retail
// has no second pass to reverse: "no delayed queue, cargo loop, or separate
// attachment pass exists beyond the immediate attach verb" [08 R-TRIG-01 §9].

// wu19205CargoMission is the stock shape of `maps/ac13.ota`: units that name a
// transport by Ident and board it, the transport itself naming nobody.
func wu19205CargoMission() *mission.Mission {
	m := strictSyntheticMission()
	// The interpreter runs for mission type 1 and BetweenMissions restores
	// only [04 §3.6].
	m.Type = mission.TypeCampaign
	const w = 1 << 16
	m.Units = []mission.UnitPlacement{
		{UnitName: "armcom", Ident: "TRANSPORT5", X: 48 * w, Z: 48 * w, Player: 1},
		{UnitName: "corcom", Ident: "CARGO1", InitialMission: "i TRANSPORT5", X: 64 * w, Z: 48 * w, Player: 1},
		{UnitName: "corcom", Ident: "CARGO2", InitialMission: "i TRANSPORT5", X: 80 * w, Z: 48 * w, Player: 1},
	}
	return m
}

func wu19205ByIdent(s *Session, ident string) *units.Unit {
	for _, u := range s.Units.IterSliced() {
		if u != nil && u.Alive && u.PlacementIdent == ident {
			return u
		}
	}
	return nil
}

func TestBattleEntryBoardsCargoIntoTheNamedCarrierOnce(t *testing.T) {
	s := wu19205SpawnerSession(t)
	m := wu19205CargoMission()
	if err := battleEntryPlacement(s, m); err != nil {
		t.Fatalf("battleEntryPlacement: %v", err)
	}
	carrier := wu19205ByIdent(s, "TRANSPORT5")
	first := wu19205ByIdent(s, "CARGO1")
	second := wu19205ByIdent(s, "CARGO2")
	if carrier == nil || first == nil || second == nil {
		t.Fatalf("mission units missing: carrier=%v cargo1=%v cargo2=%v", carrier, first, second)
	}
	// The named carrier is nobody's cargo. Before the fix it was carried by its
	// own first passenger.
	if carrier.Attachment.Carrier != 0 {
		t.Fatalf("carrier is carried by %d; `i name` boards the acting unit into the NAMED unit [04 §3.6]", carrier.Attachment.Carrier)
	}
	for _, cargo := range []*units.Unit{first, second} {
		if cargo.Attachment.Carrier != carrier.Handle {
			t.Fatalf("cargo %s has carrier %d, want the named transport %d [04 §3.6]", cargo.PlacementIdent, cargo.Attachment.Carrier, carrier.Handle)
		}
		if len(cargo.Attachment.Cargo) != 0 {
			t.Fatalf("cargo %s carries %v; the acting unit boards, it does not load [04 §3.6]", cargo.PlacementIdent, cargo.Attachment.Cargo)
		}
	}
	// Cargo is linked at the HEAD, so the later verb sits in front of the
	// earlier one [04 R-COB-03 §5]. Two passengers, listed once each: a second
	// interpretation of the same verbs would have appended duplicates.
	want := []pool.Handle{second.Handle, first.Handle}
	if len(carrier.Attachment.Cargo) != len(want) {
		t.Fatalf("carrier holds %v, want exactly %v [04 R-COB-03 §5]", carrier.Attachment.Cargo, want)
	}
	for i := range want {
		if carrier.Attachment.Cargo[i] != want[i] {
			t.Fatalf("carrier holds %v, want head-linked %v [04 R-COB-03 §5]", carrier.Attachment.Cargo, want)
		}
	}
	// The graph the save image has to round-trip: every carrier chain
	// terminates. The restore walk rejects anything else with
	// `session: retail restore: cyclic unit reference` [08 R-SAVE-02 §6].
	for _, u := range s.Units.IterSliced() {
		if u == nil || !u.Alive {
			continue
		}
		seen := map[pool.Handle]bool{u.Handle: true}
		for at := u.Attachment.Carrier; at != 0; {
			if seen[at] {
				t.Fatalf("unit %d sits on a carrier cycle; the restore walk rejects it [08 R-SAVE-02 §6]", u.Handle)
			}
			seen[at] = true
			next := s.Units.Unit(at)
			if next == nil {
				break
			}
			at = next.Attachment.Carrier
		}
	}
}
