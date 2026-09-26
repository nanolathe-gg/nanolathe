package session

import (
	"encoding/json"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
)

// generatorTestExt stands in for a controller that has (or has not yet)
// drawn from its generator.
type generatorTestExt struct {
	position uint64
	begun    bool
}

func (g *generatorTestExt) Generator() (uint64, bool) { return g.position, g.begun }

// A save records the battle seed and each begun controller's generator
// position, in player order; a controller that has not begun records none,
// and a restored manager whose controller has not begun yet carries its
// pending resume forward. The record survives the sidecar's JSON, and a load
// puts the seed on every manager and each position on its own player's.
func TestAIControllerRecordRoundTrips(t *testing.T) {
	src := &Session{}
	pending := uint64(77)
	src.AI[1] = &ai.Manager{Player: 1, BattleSeed: 9001, Ext: &generatorTestExt{position: 1 << 60, begun: true}}
	src.AI[3] = &ai.Manager{Player: 3, BattleSeed: 9001, Ext: &generatorTestExt{}}
	src.AI[5] = &ai.Manager{Player: 5, BattleSeed: 9001, ResumeGenerator: &pending}
	rec := RecordAIControllers(src)
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	var back AIControllers
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	want := AIControllers{Seed: 9001, Generators: []AIGenerator{{Player: 1, Position: 1 << 60}, {Player: 5, Position: 77}}}
	if back.Seed != want.Seed || len(back.Generators) != 2 || back.Generators[0] != want.Generators[0] || back.Generators[1] != want.Generators[1] {
		t.Fatalf("record %s, want %+v", data, want)
	}

	dst := &Session{}
	for _, p := range []uint8{1, 3, 5} {
		dst.AI[p] = &ai.Manager{Player: p, BattleSeed: 12}
	}
	if err := restoreAIControllers(dst, &back); err != nil {
		t.Fatal(err)
	}
	for _, p := range []uint8{1, 3, 5} {
		if dst.AI[p].BattleSeed != 9001 {
			t.Fatalf("player %d restored seed %d, want the saved battle's", p, dst.AI[p].BattleSeed)
		}
	}
	if r := dst.AI[1].ResumeGenerator; r == nil || *r != 1<<60 {
		t.Fatal("player 1 lost its generator position")
	}
	if dst.AI[3].ResumeGenerator != nil {
		t.Fatal("player 3 had not begun but was given a position")
	}
	if r := dst.AI[5].ResumeGenerator; r == nil || *r != 77 {
		t.Fatal("player 5's pending resume was not carried")
	}

	// No record (a retail save) leaves the load's entry seed.
	plain := &Session{}
	plain.AI[1] = &ai.Manager{Player: 1, BattleSeed: 12}
	if err := restoreAIControllers(plain, nil); err != nil {
		t.Fatal(err)
	}
	if plain.AI[1].BattleSeed != 12 || plain.AI[1].ResumeGenerator != nil {
		t.Fatal("a load without a record changed the entry seed")
	}
	if RecordAIControllers(&Session{}) != nil {
		t.Fatal("a battle without computer players recorded AI controllers")
	}
}
