package session

import (
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/units"
)

func TestProjectRetailSessionMapsClockAndPlayers(t *testing.T) {
	s := &Session{Clock: &clock.State{GlobalTick: 77}, LocalOwner: 0, Econ: &economy.Service{}}
	s.Econ.Players[0].Exists = true
	s.Econ.Players[0].ControllerState = 1
	s.Econ.Players[0].Stock[economy.Energy] = 19.5
	s.Econ.Players[0].Stock[economy.Metal] = 8.25
	in := RetailSaveInputs{
		Summary: save.Summary{Campaign: "c", Mission: "m", MapName: "map", Gametype: 1, Players: 1, IsBattle: true},
		Camera:  save.Camera{XPosition: 4, ZPosition: 5},
		Mapping: []byte{2},
	}
	p, err := ProjectRetailSession(s, in)
	if err != nil {
		t.Fatalf("ProjectRetailSession: %v", err)
	}
	if p.Summary.GameTime != 77 || p.HumanPlayer != 0 || p.Scheduler[16] != 77 {
		t.Fatalf("session metadata not projected: summary=%+v human=%d scheduler=%v", p.Summary, p.HumanPlayer, p.Scheduler)
	}
	if len(p.Players) != 1 || p.Players[0].Energy != 19.5 || p.Players[0].Metal != 8.25 {
		t.Fatalf("player projection = %+v", p.Players)
	}
	if len(p.Mapping) != 1 {
		t.Fatal("explicit mapping state was not copied")
	}
}

func TestProjectRetailSessionContinuationDoesNotNeedClock(t *testing.T) {
	s := &Session{}
	p, err := ProjectRetailSession(s, RetailSaveInputs{Summary: save.Summary{Gametype: 1, BetweenMissions: 1}})
	if err != nil {
		t.Fatalf("continuation projection: %v", err)
	}
	data, err := p.Bytes()
	if err != nil {
		t.Fatalf("continuation Bytes: %v", err)
	}
	bank, err := save.OpenBytes(data)
	if err != nil {
		t.Fatalf("continuation OpenBytes: %v", err)
	}
	if len(bank.Accounts()) != 1 || bank.Accounts()[0].Name != save.SummaryAccount {
		t.Fatalf("continuation accounts = %v, want Summary only", bank.Accounts())
	}
}

func TestProjectRetailSessionAssemblesRuntimeUnitInReversePoolOrder(t *testing.T) {
	def := &content.UnitDef{UnitName: "runtime", MaxDamage: 100, Script: &cob.Program{Code: []uint32{0x10065000}, Scripts: map[string]int{}}}
	w := units.NewSliced(2, nil)
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create runtime unit: %v", err)
	}
	econ := &economy.Service{}
	econ.UnitBuckets(h)
	s := &Session{Clock: &clock.State{GlobalTick: 9}, Units: w, Econ: econ}
	p, err := ProjectRetailSession(s, RetailSaveInputs{
		Summary:             save.Summary{Gametype: 1},
		Mapping:             []byte{0xaa},
		StableIDs:           map[pool.Handle]uint16{h: 0x73},
		UnitWriterScratch:   map[pool.Handle]units.RetailUnitWriterScratch{h: {}},
		ScriptWriterScratch: map[pool.Handle]cob.RetailScriptWriterScratch{h: {}},
	})
	if err != nil {
		t.Fatalf("runtime projection: %v", err)
	}
	if len(p.Units.Records) != 1 || p.Units.Records[0].StableID != 0x73 {
		t.Fatalf("unit records = %#v, want explicit stable ID 0x73", p.Units.Records)
	}
	if len(p.Units.Scripts) != 1 || len(p.Units.Scripts[0].Data) != save.ScriptSnapshotSize {
		t.Fatalf("script projection = %#v, want one piece-less snapshot", p.Units.Scripts)
	}
	if len(p.Units.Other) != 1 || p.Units.Other[0].Name != "u0073acc" || len(p.Units.Other[0].Data) != 48 {
		t.Fatalf("unit auxiliary projection = %#v, want one 48-byte account", p.Units.Other)
	}
}

// TestProjectRetailSessionPieceBearingScriptProjects is the WU-19-161 defect:
// every real COB names pieces, so a projection that refused a piece-bearing
// program refused every real unit and the in-battle Save produced an error box
// instead of a bank. The piece tail is now written from the VM plus the
// caller's residue scratch [08 R-SAVE-02 §9].
func TestProjectRetailSessionPieceBearingScriptProjects(t *testing.T) {
	def := &content.UnitDef{UnitName: "pieceful", MaxDamage: 100, Script: &cob.Program{Code: []uint32{0x10065000}, Pieces: []string{"base", "turret"}, Scripts: map[string]int{}}}
	w := units.NewSliced(1, nil)
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create piece-bearing unit: %v", err)
	}
	econ := &economy.Service{}
	econ.UnitBuckets(h)
	p, err := ProjectRetailSession(&Session{Clock: &clock.State{}, Units: w, Econ: econ}, RetailSaveInputs{
		Summary:             save.Summary{Gametype: 1},
		Mapping:             []byte{1},
		StableIDs:           map[pool.Handle]uint16{h: 9},
		UnitWriterScratch:   map[pool.Handle]units.RetailUnitWriterScratch{h: {}},
		ScriptWriterScratch: map[pool.Handle]cob.RetailScriptWriterScratch{h: {PieceDword24: 1, PieceDword25: 1}},
	})
	if err != nil {
		t.Fatalf("piece-bearing projection: %v", err)
	}
	want := save.ScriptSnapshotSize + 2*cob.ScriptPieceSize
	if len(p.Units.Scripts) != 1 || len(p.Units.Scripts[0].Data) != want {
		t.Fatalf("script projection = %d bytes, want %d", len(p.Units.Scripts[0].Data), want)
	}
}

// TestProjectRetailSessionMissingScriptScratchFails keeps the seam explicit:
// the residue pair is a caller policy with a visible consequence, so an
// omitted entry fails rather than silently taking the zero pair.
func TestProjectRetailSessionMissingScriptScratchFails(t *testing.T) {
	def := &content.UnitDef{UnitName: "pieceful", MaxDamage: 100, Script: &cob.Program{Code: []uint32{0x10065000}, Pieces: []string{"base"}, Scripts: map[string]int{}}}
	w := units.NewSliced(1, nil)
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create piece-bearing unit: %v", err)
	}
	econ := &economy.Service{}
	econ.UnitBuckets(h)
	_, err = ProjectRetailSession(&Session{Clock: &clock.State{}, Units: w, Econ: econ}, RetailSaveInputs{
		Summary:           save.Summary{Gametype: 1},
		Mapping:           []byte{1},
		StableIDs:         map[pool.Handle]uint16{h: 9},
		UnitWriterScratch: map[pool.Handle]units.RetailUnitWriterScratch{h: {}},
	})
	if err == nil {
		t.Fatal("projection accepted a unit with no script writer scratch")
	}
	if got := err.Error(); !containsAny(got, "script piece scratch") {
		t.Fatalf("error = %q, want the explicit script scratch blocker", got)
	}
}

func containsAny(s string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(s, value) {
			return true
		}
	}
	return false
}
