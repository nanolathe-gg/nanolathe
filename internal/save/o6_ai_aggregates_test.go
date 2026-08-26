package save

import (
	"math"
	"testing"
)

func TestStateV1AIAggregatesRoundTripPreservesFloat32Bits(t *testing.T) {
	state := &StateV1{
		Version: StateV1VersionConst,
		Economy: EconomySnapshot{Players: [10]EconomyPlayerRecord{{
			AIProductionMetal:   math.Float32frombits(0x80000000), // negative zero
			AIProductionEnergy:  math.Float32frombits(0x7fc01234), // payload-bearing NaN
			AIConsumptionMetal:  math.Float32frombits(0x00000001), // smallest subnormal
			AIConsumptionEnergy: math.Float32frombits(0xff800000), // negative infinity
		}}},
	}
	decoded, err := UnmarshalStateV1(MarshalStateV1(state), "", "")
	if err != nil {
		t.Fatalf("UnmarshalStateV1: %v", err)
	}
	got := decoded.Economy.Players[0]
	want := state.Economy.Players[0]
	for name, gotBits := range map[string]uint32{
		"production metal":   math.Float32bits(got.AIProductionMetal),
		"production energy":  math.Float32bits(got.AIProductionEnergy),
		"consumption metal":  math.Float32bits(got.AIConsumptionMetal),
		"consumption energy": math.Float32bits(got.AIConsumptionEnergy),
	} {
		var wantBits uint32
		switch name {
		case "production metal":
			wantBits = math.Float32bits(want.AIProductionMetal)
		case "production energy":
			wantBits = math.Float32bits(want.AIProductionEnergy)
		case "consumption metal":
			wantBits = math.Float32bits(want.AIConsumptionMetal)
		case "consumption energy":
			wantBits = math.Float32bits(want.AIConsumptionEnergy)
		}
		if gotBits != wantBits {
			t.Errorf("%s bits = %#08x, want %#08x", name, gotBits, wantBits)
		}
	}
}

func TestStateV1Version7AIAggregatesDefaultZero(t *testing.T) {
	// Version 7 is the immediately previous native payload. It has no AI
	// aggregate fields; decoding must consume its old layout and leave the new
	// fields at their Go zero values rather than shifting subsequent boxes.
	state := &StateV1{
		Version: StateV1Version7,
		Economy: EconomySnapshot{Players: [10]EconomyPlayerRecord{{
			StorageBonusEnabled: true,
			StorageBonusMetal:   17,
			StorageBonusEnergy:  19,
		}}},
	}
	decoded, err := UnmarshalStateV1(MarshalStateV1(state), "", "")
	if err != nil {
		t.Fatalf("UnmarshalStateV1(v7): %v", err)
	}
	got := decoded.Economy.Players[0]
	if got.StorageBonusMetal != 17 || got.StorageBonusEnergy != 19 || !got.StorageBonusEnabled {
		t.Fatalf("v7 storage fields changed: %+v", got)
	}
	if got.AIProductionMetal != 0 || got.AIProductionEnergy != 0 || got.AIConsumptionMetal != 0 || got.AIConsumptionEnergy != 0 {
		t.Fatalf("v7 AI aggregates should default zero: %+v", got)
	}
}

func TestStateV1Version8AddsFourFloat32FieldsPerPlayer(t *testing.T) {
	v7 := MarshalStateV1(&StateV1{Version: StateV1Version7})
	v8 := MarshalStateV1(&StateV1{Version: StateV1Version8})
	if got, want := len(v8)-len(v7), 10*4*4; got != want {
		t.Fatalf("StateV1 v8 payload delta = %d bytes, want %d", got, want)
	}
}
