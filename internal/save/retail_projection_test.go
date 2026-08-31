package save

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestRetailProjectionLiveAccountOrderAndDecode(t *testing.T) {
	base := make([]byte, UnitBoxSize)
	binary.LittleEndian.PutUint16(base[0x21:], 7)
	p := RetailProjection{
		Summary:        Summary{Campaign: "campaign", Mission: "mission", MapName: "map", Gametype: 1, Players: 1, IsBattle: true},
		Camera:         Camera{XPosition: 11, ZPosition: 22},
		Scheduler:      [28]byte{3, 4, 5},
		Units:          UnitImage{Records: []UnitRecord{{StableID: 7, Data: base}}, Scripts: []ScriptRecord{{Index: 0, Data: make([]byte, ScriptSnapshotSize)}}, Other: []RawBox{{Name: "u0007acc", Data: make([]byte, 48)}}},
		Features:       FeatureImage{},
		Metal:          []byte{1, 2},
		PlayerFeatures: []byte{3},
		Mapping:        []byte{4},
		Players:        []PlayerSlot{{Index: 0, Energy: 12.5, Metal: 7.25, Controller: 1}},
	}
	data, err := p.RetailBytes()
	if err != nil {
		t.Fatalf("RetailBytes: %v", err)
	}
	bank, err := OpenBytes(data, RetailTag)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	wantAccounts := []string{"Summary", "Camera", "Players", "Player0", "Features", "Metal", "PlayerFeatures", "Mapping", "Units", "Meteor"}
	accounts := bank.Accounts()
	if len(accounts) != len(wantAccounts) {
		t.Fatalf("account count %d, want %d", len(accounts), len(wantAccounts))
	}
	for i, want := range wantAccounts {
		if accounts[i].Name != want {
			t.Fatalf("account %d = %q, want %q", i, accounts[i].Name, want)
		}
	}
	image, err := DecodeBattleImage(bank)
	if err != nil {
		t.Fatalf("DecodeBattleImage: %v", err)
	}
	if image.Camera != p.Camera || !bytes.Equal(image.Metal, p.Metal) || !bytes.Equal(image.PlayerFeatures, p.PlayerFeatures) || !bytes.Equal(image.Mapping, p.Mapping) {
		t.Fatalf("established fields did not round-trip: camera=%+v metal=%v playerFeatures=%v mapping=%v", image.Camera, image.Metal, image.PlayerFeatures, image.Mapping)
	}
	player, ok := ReadPlayerSlot(bank, 0)
	if !ok || player.Energy != 12.5 || player.Metal != 7.25 || player.Controller != 1 {
		t.Fatalf("Player0 = %+v, present=%v", player, ok)
	}
}

func TestRetailProjectionContinuationWritesSummaryOnly(t *testing.T) {
	p := RetailProjection{Summary: Summary{Campaign: "campaign", Mission: "mission", Gametype: 1, BetweenMissions: 1, IsBattle: false}, Metal: []byte{9}}
	data, err := p.RetailBytes()
	if err != nil {
		t.Fatalf("RetailBytes: %v", err)
	}
	bank, err := OpenBytes(data, RetailTag)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	if got := len(bank.Accounts()); got != 1 {
		t.Fatalf("continuation accounts = %d, want Summary only", got)
	}
	if _, ok := bank.Account(SummaryAccount); !ok {
		t.Fatal("continuation lost Summary")
	}
	if _, ok := bank.Account("Metal"); ok {
		t.Fatal("continuation emitted battle terrain")
	}
}

func TestRetailProjectionFromBattleImageDeepCopies(t *testing.T) {
	image := &BattleImage{Summary: Summary{RadarImage: []byte{1}}, Metal: []byte{2}}
	p, err := RetailProjectionFromBattleImage(image)
	if err != nil {
		t.Fatalf("RetailProjectionFromBattleImage: %v", err)
	}
	image.Summary.RadarImage[0], image.Metal[0] = 8, 9
	if p.Summary.RadarImage[0] != 1 || p.Metal[0] != 2 {
		t.Fatal("projection aliases battle image bytes")
	}
}

func TestRetailProjectionUnitsEmitPerUnitAndDeferHeader(t *testing.T) {
	base0 := make([]byte, UnitBoxSize)
	binary.LittleEndian.PutUint16(base0[0x21:], 7)
	binary.LittleEndian.PutUint32(base0[0x23:], 1)
	binary.LittleEndian.PutUint32(base0[0x27:], 1) // established has-mover word
	base1 := make([]byte, UnitBoxSize)
	binary.LittleEndian.PutUint16(base1[0x21:], 9)
	order := make([]byte, OrderBoxSize)
	binary.LittleEndian.PutUint16(order[0:], 7)
	units := UnitImage{
		Records: []UnitRecord{{StableID: 7, Data: base0}, {StableID: 9, Data: base1}},
		Orders:  []OrderRecord{{ParentStableID: 7, Sequence: 0, Main: order}},
		Scripts: []ScriptRecord{{Index: 0, Data: make([]byte, ScriptSnapshotSize)}, {Index: 1, Data: make([]byte, ScriptSnapshotSize)}},
		Other: []RawBox{
			{Name: "u0007mob", Data: make([]byte, 35)},
			{Name: "u0007acc", Data: make([]byte, 48)},
			{Name: "u0009acc", Data: make([]byte, 48)},
		},
	}
	p := RetailProjection{Summary: Summary{Gametype: 1, IsBattle: true}, Units: units}
	b, err := p.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ac := accountByName(b, UnitsAccount)
	if ac == nil {
		t.Fatal("Units account missing")
	}
	if len(ac.Ints) != 2 || ac.Ints[0].Name != "Version" || ac.Ints[1].Name != "Number of Units" {
		t.Fatalf("Units header items = %+v, want deferred Version then Number of Units", ac.Ints)
	}
	wantBoxes := []string{"Script0", "u0007m0000", "u0007mob", "u0007acc", "", "Script1", "u0009acc", ""}
	if len(ac.Boxes) != len(wantBoxes) {
		t.Fatalf("Units box count %d, want %d", len(ac.Boxes), len(wantBoxes))
	}
	for i, want := range wantBoxes {
		if ac.Boxes[i].Name != want {
			t.Fatalf("Units box %d = %q, want %q", i, ac.Boxes[i].Name, want)
		}
	}

	empty, err := (RetailProjection{Summary: Summary{Gametype: 1, IsBattle: true}}).Build()
	if err != nil {
		t.Fatalf("empty Build: %v", err)
	}
	emptyUnits := accountByName(empty, UnitsAccount)
	if emptyUnits == nil || len(emptyUnits.Ints) != 0 || len(emptyUnits.Boxes) != 0 {
		t.Fatalf("empty Units account = %+v, want account with no header or boxes", emptyUnits)
	}
}

func accountByName(b *Builder, name string) *Account {
	for _, account := range b.Accounts {
		if account != nil && account.Name == name {
			return account
		}
	}
	return nil
}

func validSingleUnitProjection() RetailProjection {
	base := make([]byte, UnitBoxSize)
	binary.LittleEndian.PutUint16(base[0x21:], 7)
	return RetailProjection{
		Units: UnitImage{
			Records: []UnitRecord{{StableID: 7, Data: base}},
			Scripts: []ScriptRecord{{Index: 0, Data: make([]byte, ScriptSnapshotSize)}},
			Other:   []RawBox{{Name: "u0007acc", Data: make([]byte, 48)}},
		},
	}
}

func TestRetailProjectionUnitsRejectsMissingScript(t *testing.T) {
	p := validSingleUnitProjection()
	p.Units.Scripts = nil
	if _, err := p.RetailBytes(); err == nil {
		t.Fatal("missing Script0 unexpectedly accepted")
	}
}

func TestRetailProjectionUnitsRejectsUnmatchedReferencesDeterministically(t *testing.T) {
	tests := []struct {
		name string
		edit func(*RetailProjection)
		want string
	}{
		{
			name: "order",
			edit: func(p *RetailProjection) {
				p.Units.Orders = []OrderRecord{{ParentStableID: 9, Sequence: 0}}
			},
			want: "order references unknown unit 0009",
		},
		{
			name: "accessory",
			edit: func(p *RetailProjection) {
				p.Units.Other = append(p.Units.Other, RawBox{Name: "u0009acc", Data: make([]byte, 48)})
			},
			want: "acc box references unknown unit 0009",
		},
		{
			name: "mover",
			edit: func(p *RetailProjection) {
				p.Units.Other = append(p.Units.Other, RawBox{Name: "u0009mob", Data: make([]byte, 35)})
			},
			want: "mob box references unknown unit 0009",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := validSingleUnitProjection()
			tc.edit(&p)
			_, err := p.RetailBytes()
			if err == nil || !bytes.Contains([]byte(err.Error()), []byte(tc.want)) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestRetailProjectionUnitsRejectsInconsistentOrderSideData(t *testing.T) {
	tests := []struct {
		name string
		edit func(*RetailProjection)
	}{
		{
			name: "secondary bit",
			edit: func(p *RetailProjection) {
				main := make([]byte, OrderBoxSize)
				binary.LittleEndian.PutUint16(main[0:], 7)
				p.Units.Records[0].Data[0x23] = 1
				p.Units.Orders = []OrderRecord{{ParentStableID: 7, Sequence: 0, Secondary: true, Main: main}}
			},
		},
		{
			name: "zero code subtype",
			edit: func(p *RetailProjection) {
				main := make([]byte, OrderBoxSize)
				binary.LittleEndian.PutUint16(main[0:], 7)
				p.Units.Records[0].Data[0x23] = 1
				p.Units.Orders = []OrderRecord{{ParentStableID: 7, Sequence: 0, Main: main, Subtype: []byte{1}}}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := validSingleUnitProjection()
			tc.edit(&p)
			if _, err := p.RetailBytes(); err == nil {
				t.Fatal("inconsistent order side data unexpectedly accepted")
			}
		})
	}
}

func TestRetailProjectionUnitsRejectsSideDataWithoutRecords(t *testing.T) {
	p := RetailProjection{Units: UnitImage{TypeNames: []StringItem{{Name: "UTYPENAME0", Value: "unit"}}}}
	if _, err := p.RetailBytes(); err == nil {
		t.Fatal("empty Units image with type names unexpectedly accepted")
	}
}
