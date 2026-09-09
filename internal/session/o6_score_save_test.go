package session

import (
	"math"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
)

func TestHashStateIncludesAIAggregatesAndPreservesOrdering(t *testing.T) {
	s := &Session{Econ: &economy.Service{}}
	s.Econ.Players[0].Exists = true
	s.Econ.Players[0].AIProduction[economy.Metal] = 12
	s.Econ.Players[0].AIProduction[economy.Energy] = 34
	s.Econ.Players[0].AIConsumption[economy.Metal] = 5
	s.Econ.Players[0].AIConsumption[economy.Energy] = 6
	s.Econ.Players[1].Exists = true
	s.Econ.Players[1].AIProduction[economy.Metal] = 56
	base := HashState(s)

	// A one-bit change must affect the authoritative hash even where the
	// displayed float value would be unchanged.
	s.Econ.Players[0].AIProduction[economy.Metal] = math.Float32frombits(math.Float32bits(12) + 1)
	if got := HashState(s); got == base {
		t.Fatal("AI aggregate bit change did not affect HashState")
	}

	// Player order is part of the canonical hash contract; swapping the same
	// values between players is not equivalent state.
	s.Econ.Players[0].AIProduction[economy.Metal] = 56
	s.Econ.Players[1].AIProduction[economy.Metal] = 12
	if got := HashState(s); got == base {
		t.Fatal("player-index change did not affect HashState")
	}

	// Stocks are authoritative float32 values too; neighboring values that
	// round to the same display cents must still hash differently.
	s.Econ.Players[0].AIProduction[economy.Metal] = 12
	s.Econ.Players[1].AIProduction[economy.Metal] = 56
	stockHash := HashState(s)
	s.Econ.Players[0].Stock[economy.Metal] = math.Float32frombits(1)
	if got := HashState(s); got == stockHash {
		t.Fatal("stock bit change did not affect HashState")
	}
}
