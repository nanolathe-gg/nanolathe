package session

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/units"
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

func TestProjectRetailSessionAssemblesRuntimeUnit(t *testing.T) {
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
		Summary:           save.Summary{Gametype: 1},
		Mapping:           []byte{0xaa},
		StableIDs:         map[pool.Handle]uint16{h: 0x73},
		UnitWriterScratch: map[pool.Handle]units.RetailUnitWriterScratch{h: {}},
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

func TestProjectRetailSessionWritesNullForDeadMainOrderTarget(t *testing.T) {
	def := &content.UnitDef{UnitName: "runtime", MaxDamage: 100, Script: &cob.Program{Code: []uint32{0x10065000}, Scripts: map[string]int{}}}
	w := units.NewSliced(3, nil)
	owner, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	target, err := w.Create(def, 1, 0, 0, 0)
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	orders.BindQueue(w.Unit(owner), orders.NewQueueWith([]*orders.Node{{ID: orders.Lookup("Move_Ground"), Owner: owner, Target: target}}, nil))
	// The dead target stays in the queue link long enough for the writer to
	// project the established null wire field, but is no longer a live slot.
	w.Unit(target).Alive = false
	econ := &economy.Service{}
	econ.UnitBuckets(owner)
	p, err := ProjectRetailSession(&Session{Clock: &clock.State{}, Units: w, Econ: econ}, RetailSaveInputs{
		Summary:           save.Summary{Gametype: 1},
		Mapping:           []byte{0},
		StableIDs:         map[pool.Handle]uint16{owner: 9},
		UnitWriterScratch: map[pool.Handle]units.RetailUnitWriterScratch{owner: {}},
	})
	if err != nil {
		t.Fatalf("projection with dead main target: %v", err)
	}
	if len(p.Units.Orders) != 1 {
		t.Fatalf("projected order count=%d, want one", len(p.Units.Orders))
	}
	if got := binary.LittleEndian.Uint16(p.Units.Orders[0].Main[2:]); got != 0 {
		t.Fatalf("dead main target stable ID=%d, want wire null", got)
	}
}

func TestProjectRetailSessionRejectsLiveMainTargetWithoutStableID(t *testing.T) {
	def := &content.UnitDef{UnitName: "runtime", MaxDamage: 100, Script: &cob.Program{Code: []uint32{0x10065000}, Scripts: map[string]int{}}}
	w := units.NewSliced(3, nil)
	owner, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	target, err := w.Create(def, 1, 0, 0, 0)
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	orders.BindQueue(w.Unit(owner), orders.NewQueueWith([]*orders.Node{{ID: orders.Lookup("Move_Ground"), Owner: owner, Target: target}}, nil))
	econ := &economy.Service{}
	econ.UnitBuckets(owner)
	_, err = ProjectRetailSession(&Session{Clock: &clock.State{}, Units: w, Econ: econ}, RetailSaveInputs{
		Summary:           save.Summary{Gametype: 1},
		Mapping:           []byte{0},
		StableIDs:         map[pool.Handle]uint16{owner: 9},
		UnitWriterScratch: map[pool.Handle]units.RetailUnitWriterScratch{owner: {}},
	})
	if err == nil || !strings.Contains(err.Error(), "unresolved node target") {
		t.Fatalf("live target without a stable ID error=%v, want unresolved node target", err)
	}
}

// TestProjectRetailSessionNumbersUnitBoxesAscendingWithPoolIndex pins the
// direction of the outer walk. Retail's single Units writer starts at the pool
// base and advances one record stride per iteration, so numbered box `i` rises
// with pool position [08 R-SAVE-02 §6]. The direction is load-bearing: the
// reader restores numbered boxes 0..count-1 in index order and the base
// record's AI group word appends the unit to the owner's group vector, so a
// reversed walk builds every group vector backwards.
func TestProjectRetailSessionNumbersUnitBoxesAscendingWithPoolIndex(t *testing.T) {
	def := &content.UnitDef{UnitName: "grouped", MaxDamage: 100, Script: &cob.Program{Code: []uint32{0x10065000}, Scripts: map[string]int{}}}
	w := units.NewSliced(8, nil)
	econ := &economy.Service{}
	const aiGroup = 3
	stableIDs := map[pool.Handle]uint16{}
	scratch := map[pool.Handle]units.RetailUnitWriterScratch{}
	var handles []pool.Handle
	for i := 0; i < 3; i++ {
		h, err := w.Create(def, 0, 0, 0, 0)
		if err != nil {
			t.Fatalf("create unit %d: %v", i, err)
		}
		w.Unit(h).RestoredAIGroup = aiGroup
		econ.UnitBuckets(h)
		handles = append(handles, h)
		stableIDs[h] = uint16(0x40 + i)
		scratch[h] = units.RetailUnitWriterScratch{}
	}
	if !(handles[0] < handles[1] && handles[1] < handles[2]) {
		t.Fatalf("fixture handles are not ascending: %v", handles)
	}
	p, err := ProjectRetailSession(&Session{Clock: &clock.State{}, Units: w, Econ: econ}, RetailSaveInputs{
		Summary:           save.Summary{Gametype: 1},
		Mapping:           []byte{1},
		StableIDs:         stableIDs,
		UnitWriterScratch: scratch,
	})
	if err != nil {
		t.Fatalf("projection: %v", err)
	}
	if len(p.Units.Records) != len(handles) {
		t.Fatalf("records = %d, want %d", len(p.Units.Records), len(handles))
	}
	// Numbered box i belongs to the i-th pool slot, and Script%i is numbered by
	// the same running index.
	var restoreOrder []pool.Handle
	for i, rec := range p.Units.Records {
		if rec.Number != i {
			t.Fatalf("record %d carries numbered box %d", i, rec.Number)
		}
		if want := stableIDs[handles[i]]; rec.StableID != want {
			t.Fatalf("numbered box %d holds stable ID %#x, want %#x (ascending pool order)", i, rec.StableID, want)
		}
		if p.Units.Scripts[i].Index != i {
			t.Fatalf("Script box %d carries index %d", i, p.Units.Scripts[i].Index)
		}
		restoreOrder = append(restoreOrder, handles[i])
	}
	// These units have no forward references, so recursive group restoration
	// completes in numbered-box order [08 R-SAVE-02 §6].
	mgr := &ai.Manager{Player: 0}
	mgr.RestoreGroupsFromUnits(w.IterSliced())
	got := mgr.GroupMembers(aiGroup)
	if len(got) != len(restoreOrder) {
		t.Fatalf("restored group vector = %v, want %v", got, restoreOrder)
	}
	for i := range got {
		if got[i] != restoreOrder[i] {
			t.Fatalf("restored group vector = %v, want the numbered-box order %v", got, restoreOrder)
		}
	}
}

// TestProjectRetailSessionPieceBearingScriptProjects is the WU-19-161 defect:
// every real COB names pieces, so a projection that refused a piece-bearing
// program refused every real unit and the in-battle Save produced an error box
// instead of a bank. The piece tail is written from the bound VM
// [08 R-SAVE-02 §9].
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
		Summary:           save.Summary{Gametype: 1},
		Mapping:           []byte{1},
		StableIDs:         map[pool.Handle]uint16{h: 9},
		UnitWriterScratch: map[pool.Handle]units.RetailUnitWriterScratch{h: {}},
	})
	if err != nil {
		t.Fatalf("piece-bearing projection: %v", err)
	}
	want := save.ScriptSnapshotSize + 2*cob.ScriptPieceSize
	if len(p.Units.Scripts) != 1 || len(p.Units.Scripts[0].Data) != want {
		t.Fatalf("script projection = %d bytes, want %d", len(p.Units.Scripts[0].Data), want)
	}
}

// TestRetailBattleSummaryRecordsTheConfiguredUnitLimit locks the producer of
// `Summary.maxunits`. The writer records the **configured** unit-limit word —
// the process-wide `[Preferences] UnitLimit` copy — and not the session word
// [08 R-SESS-01 §9]; the two differ exactly in campaign, where the session word
// is the mission OTA's `maxunits` [08 R-SKIR-01 §6]. The campaign fixture below
// therefore carries an OTA limit of 200 while the caller's configured word is
// 137, so a producer that read the session limit would be visible immediately.
//
// This item had no producer before: the summary left it zero, which the reader
// could not tell from an omitted item.
func TestRetailBattleSummaryRecordsTheConfiguredUnitLimit(t *testing.T) {
	const configured = 137

	campaign := &Session{
		Mission: &mission.Mission{
			Type: mission.TypeCampaign,
			OTA: &formats.OTA{Global: &formats.Section{
				Name:  "GlobalHeader",
				Items: []formats.Item{{Kind: formats.Assignment, Key: "maxunits", Value: "200"}},
			}},
			CampaignPath:        "camps/arm campaign.tdf",
			CampaignMissionName: "MISSION1",
		},
	}
	if got := sessionUnitLimit(campaign); got != 200 {
		t.Fatalf("campaign session limit = %d, want the fixture's OTA 200; the test cannot tell the two words apart otherwise", got)
	}

	skirmish := &Session{Gameplay: gameplay.Strict31}
	skirmish.Skirmish.MapName = "Coast to Coast"
	skirmish.Skirmish.NumPlayers = 2
	skirmish.Skirmish.UnitLimit = 300 // a session word unequal to the configured one

	for _, tc := range []struct {
		name string
		s    *Session
	}{{"campaign", campaign}, {"skirmish", skirmish}} {
		t.Run(tc.name, func(t *testing.T) {
			summary := RetailBattleSummary(tc.s, "limits", "0", configured)
			if !summary.HasMaxUnits || summary.MaxUnits != configured {
				t.Fatalf("summary MaxUnits=%d HasMaxUnits=%v, want the configured %d present", summary.MaxUnits, summary.HasMaxUnits, configured)
			}
			b := save.NewBuilder()
			save.WriteSummary(b, summary)
			bank, err := save.OpenBytes(b.Bytes())
			if err != nil {
				t.Fatalf("OpenBytes: %v", err)
			}
			ac, ok := bank.Account(save.SummaryAccount)
			if !ok {
				t.Fatal("no Summary account in the written bank")
			}
			value, ok := ac.Int("maxunits")
			if !ok {
				t.Fatal("the written bank carries no maxunits item")
			}
			if value != configured {
				t.Fatalf("bank maxunits = %d, want the configured word %d", value, configured)
			}
			back, ok := save.ReadSummary(bank)
			if !ok {
				t.Fatal("ReadSummary reported no Summary account")
			}
			if !back.HasMaxUnits || back.MaxUnits != configured {
				t.Fatalf("read back MaxUnits=%d HasMaxUnits=%v, want the configured %d present", back.MaxUnits, back.HasMaxUnits, configured)
			}
		})
	}
}

// liveUnitMirrorWords is the census of the four base-record words whose
// runtime owner is a service rather than the Unit. The save projects them; the
// live records must come back from a save exactly as they went in, because one
// of them — the committed cell pair — is read by the death hook as the corpse
// anchor whenever the movement system has no committed footprint for the
// handle [05 R-FEAT-01 §13][04 R-ORD-01 §1].
func liveUnitMirrorWords(s *Session) map[pool.Handle][6]int32 {
	out := make(map[pool.Handle][6]int32)
	for slot := 1; slot < s.Units.TotalRecords(); slot++ {
		h := pool.Handle(slot)
		u := s.Units.Unit(h)
		if u == nil {
			continue
		}
		mover := int32(0)
		if u.HasMover {
			mover = 1
		}
		out[h] = [6]int32{mover, u.RestoredAIGroup, int32(u.CachedOccupancyX), int32(u.CachedOccupancyZ), int32(u.FootprintSizeX), int32(u.FootprintSizeZ)}
	}
	return out
}

// TestRetailSaveLeavesLiveUnitRecordsUntouched locks the save projection out of
// live state. The projection fills the service-owned base-record words on a
// DETACHED copy of each unit [08 R-SAVE-02 §6]; writing them onto the live
// record — which is what this build used to do — changed the corpse anchor the
// death hook reads for every unit whose committed footprint is missing
// [05 R-FEAT-01 §13], so taking a save moved a later wreck.
func TestRetailSaveLeavesLiveUnitRecordsUntouched(t *testing.T) {
	s := newLoopTestSession(t, 4)
	// The projection requires a per-unit account and a bound script for every
	// live unit; this fixture composes neither.
	for slot := 1; slot < s.Units.TotalRecords(); slot++ {
		h := pool.Handle(slot)
		u := s.Units.Unit(h)
		if u == nil {
			continue
		}
		s.Econ.UnitBuckets(h)
		if u.GetScript() == nil {
			u.SetScript(cob.NewVM(&cob.Program{}))
		}
	}
	before := liveUnitMirrorWords(s)
	if len(before) == 0 {
		t.Fatal("fixture has no live units")
	}
	in, err := s.RetailBattleSaveInputs(RetailBattleSummary(s, "mirrors", "0", SkirmishDefaultUnitLimit), save.Camera{})
	if err != nil {
		t.Fatalf("battle save inputs: %v", err)
	}
	p, err := ProjectRetailSession(s, in)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if _, err := p.Bytes(); err != nil {
		t.Fatalf("bank bytes: %v", err)
	}
	after := liveUnitMirrorWords(s)
	for h, want := range before {
		got, ok := after[h]
		if !ok {
			t.Fatalf("unit %04x disappeared across the save", h)
		}
		if got != want {
			t.Fatalf("unit %04x service-owned words changed across the save: before %v after %v — the save must not write live records [08 R-SAVE-02 §6]", h, want, got)
		}
	}

	// The projection must still CARRY those words, or the assertion above
	// would pass for a save that simply dropped them. At least one fixture
	// unit owns a mover, and the live record does not say so.
	var carried bool
	for h, m := range in.UnitMirrors {
		if !m.HasMover {
			continue
		}
		carried = true
		if u := s.Units.Unit(h); u != nil && u.HasMover {
			t.Fatalf("unit %04x has-mover was written onto the live record", h)
		}
	}
	if !carried {
		t.Fatal("no projected mirror carried a mover; the fixture cannot discriminate")
	}
	var moverBoxes int
	for _, rec := range p.Units.Records {
		if binary.LittleEndian.Uint32(rec.Data[0x27:]) != 0 {
			moverBoxes++
		}
	}
	if moverBoxes == 0 {
		t.Fatal("no saved base record carries the has-mover word; the projection lost the service-owned words [08 R-SAVE-02 §6]")
	}
}
