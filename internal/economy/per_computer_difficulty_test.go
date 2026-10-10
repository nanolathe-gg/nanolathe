package economy

import (
	"bytes"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// perSeatService is a ledger with three computer players and a human: the
// battle's word is medium, player 1 carries easy and player 3 hard as their
// own words (docs/DESIGN_MULTIPLAYER.md §6.6), and player 4 keeps the
// battle's word.
func perSeatService() *Service {
	s := &Service{}
	s.SetEconomySelector(1)
	for _, p := range []int{1, 3, 4} {
		s.Players[p] = Player{Exists: true, ControllerState: 2}
	}
	s.Players[2] = Player{Exists: true, ControllerState: 1}
	s.SetPlayerSelector(1, 0)
	s.SetPlayerSelector(3, 2)
	return s
}

// Every credit the discount ladder gates on the destination's record selects
// on that record's own word when it has one — the production gather, a
// transfer's recipient credit, the feature-reclaim payout and the unit-reclaim
// refund [05 R-ECO-01 §3][05 R-ECO-01 §11][05 R-SHARE-01 §2] — and on the
// battle's word otherwise. A human is never discounted, whatever word it
// holds.
func TestEachComputerCreditSelectsOnItsOwnWord(t *testing.T) {
	want := map[int]float32{1: 500, 2: 1000, 3: 1000, 4: 700}
	for _, player := range []int{1, 2, 3, 4} {
		s := perSeatService()
		s.SetPlayerSelector(2, 0) // a human's word is never read
		var b Bucket
		addContribution(s, &s.Players[player], &b, 1000)
		if b.Production != want[player] {
			t.Fatalf("production: player %d credited %v of 1000, want %v", player, b.Production, want[player])
		}
		handle := pool.Handle(10 + player)
		s.CreditFeatureReclaim(handle, uint8(player), 1000, 1000)
		if got := s.UnitBuckets(handle); got[Metal].Production != want[player] || got[Energy].Production != want[player] {
			t.Fatalf("feature reclaim: player %d credited %v / %v of 1000, want %v", player, got[Metal].Production, got[Energy].Production, want[player])
		}
		killer := pool.Handle(20 + player)
		s.CreditUnitReclaimRefund(killer, uint8(player), 0, 1000)
		if got := s.UnitBuckets(killer)[Metal].Production; got != want[player] {
			t.Fatalf("unit reclaim: killer owner %d refunded %v of 1000, want %v", player, got, want[player])
		}
		src := (player + 1) % 5
		activePlayer(&s.Players[src])
		s.Players[src].Stock[Metal] = 5000
		before := s.Players[player].Mirror[Metal].Production
		s.Transfer(uint8(src), uint8(player), Metal, 1000)
		if got := s.Players[player].Mirror[Metal].Production - before; got != want[player] {
			t.Fatalf("transfer: recipient %d credited %v of 1000, want %v", player, got, want[player])
		}
	}
}

// The ProTA 4.8 income table selects on the same per-player word, and a
// full-income mark still overrides it.
func TestPerSeatWordReachesProTAAndFullIncome(t *testing.T) {
	s := perSeatService()
	s.Community = community.Features{AIDifficultyIncome: true}
	for player, want := range map[int]float32{1: 500, 3: 4000, 4: 1000} {
		var b Bucket
		addContribution(s, &s.Players[player], &b, 1000)
		if b.Production != want {
			t.Fatalf("ProTA: player %d credited %v of 1000, want %v", player, b.Production, want)
		}
	}
	s.Players[1].FullIncome = true
	var b Bucket
	addContribution(s, &s.Players[1], &b, 1000)
	if b.Production != 1000 {
		t.Fatalf("a full-income player with its own word was credited %v of 1000", b.Production)
	}
}

// Setting, reading and clearing a player's own word. A cleared record reads
// the battle's word again, and a slot outside the ten is ignored.
func TestPlayerSelectorLifecycle(t *testing.T) {
	s := perSeatService()
	if w, ok := s.PlayerSelector(1); !ok || w != 0 {
		t.Fatalf("player 1 own word = %d, %t", w, ok)
	}
	if _, ok := s.PlayerSelector(4); ok {
		t.Fatal("player 4 reports a word of its own")
	}
	s.ClearPlayerSelector(1)
	if _, ok := s.PlayerSelector(1); ok || s.selectorForOwner(1) != 1 {
		t.Fatalf("cleared player 1 selects on %d", s.selectorForOwner(1))
	}
	s.SetPlayerSelector(10, 0)
	s.ClearPlayerSelector(10)
	if _, ok := s.PlayerSelector(10); ok || s.selectorForOwner(10) != 1 {
		t.Fatal("a slot outside the ten took a word")
	}
	var nilService *Service
	nilService.SetPlayerSelector(1, 0)
	if _, ok := nilService.PlayerSelector(1); ok || nilService.selectorForOwner(1) != 2 {
		t.Fatal("a nil ledger answered a word")
	}
}

// A ledger in which no player holds its own word encodes exactly as it did
// before per-player words existed: the EconomySelector tag is the presence
// byte and the player rows carry no extra field. Any own word changes the
// payload, and clearing it restores the original bytes.
func TestCheckpointPlayerSelectorsAreAbsentUntilSet(t *testing.T) {
	for _, global := range []bool{false, true} {
		s := &Service{}
		if global {
			s.SetEconomySelector(1)
		}
		base := economyCheckpointBytes(t, s)
		s.SetPlayerSelector(3, 0)
		withEasy := economyCheckpointBytes(t, s)
		if bytes.Equal(base, withEasy) {
			t.Fatalf("global=%t: a player's own word left the payload unchanged", global)
		}
		s.SetPlayerSelector(3, 2)
		if bytes.Equal(withEasy, economyCheckpointBytes(t, s)) {
			t.Fatalf("global=%t: the player's word did not enter the payload", global)
		}
		s.SetPlayerSelector(3, 0)
		s.ClearPlayerSelector(3)
		s.SetPlayerSelector(4, 0)
		if bytes.Equal(withEasy, economyCheckpointBytes(t, s)) {
			t.Fatalf("global=%t: the player owning the word did not enter the payload", global)
		}
		s.ClearPlayerSelector(4)
		if !bytes.Equal(base, economyCheckpointBytes(t, s)) {
			t.Fatalf("global=%t: clearing every own word did not restore the original payload", global)
		}
	}
}
