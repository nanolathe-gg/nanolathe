package session

import (
	"math"
	"testing"

	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/save"
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

func TestAIAggregatesRoundTripResumeScore(t *testing.T) {
	const player = uint8(0)
	s := &Session{Econ: &economy.Service{}}
	p := &s.Econ.Players[player]
	p.Exists = true
	p.Stock[economy.Energy] = 100
	p.Stock[economy.Metal] = 80
	p.Capacity[economy.Energy] = 400
	p.Capacity[economy.Metal] = 200
	p.AIProduction[economy.Energy] = 180
	p.AIProduction[economy.Metal] = 7
	p.AIConsumption[economy.Energy] = 12
	p.AIConsumption[economy.Metal] = 2

	before := ai.ScoreInputsFromEconomy(s.Econ, player)
	st := s.CaptureStateV1()
	if st == nil || st.Version != save.StateV1VersionConst {
		t.Fatalf("CaptureStateV1 version = %#v, want %d", st, save.StateV1VersionConst)
	}
	payload := save.MarshalStateV1(st)
	decoded, err := save.UnmarshalStateV1(payload, "", "")
	if err != nil {
		t.Fatalf("UnmarshalStateV1: %v", err)
	}
	resumed := &Session{Econ: &economy.Service{}}
	if err := resumed.RestoreStateV1(decoded); err != nil {
		t.Fatalf("RestoreStateV1: %v", err)
	}
	after := ai.ScoreInputsFromEconomy(resumed.Econ, player)
	if before != after {
		t.Fatalf("score inputs changed across save/resume: before=%+v after=%+v", before, after)
	}
	if got, want := HashState(resumed), HashState(s); got != want {
		t.Fatalf("state hash changed across save/resume: got %s want %s", got, want)
	}
}
