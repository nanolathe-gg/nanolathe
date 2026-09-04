package save

import (
	"bytes"
	"testing"

	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/economy"
)

// TestSummaryRoundTrip locks the Summary writer table order and field defaults
// [08 "Summary"].
func TestSummaryRoundTrip(t *testing.T) {
	b := NewBuilder()
	s := Summary{
		BuildDateKey:    "BUILD DATE:Aug 23 2026",
		BuildTimeKey:    "BUILD TIME:12:00:00",
		MaxUnits:        500,
		Campaign:        "TestCampaign",
		Mission:         "MISSION0",
		MapName:         "Coast to Coast",
		Difficulty:      1,
		Side:            0,
		Players:         2,
		Gametype:        2,
		Thumbs:          "UUW______________________",
		CommanderDeath:  1,
		Location:        2,
		Mapping:         1,
		LineOfSight:     0,
		LineOfSightType: 1,
		BetweenMissions: 0,
		Description:     "hello",
		GameID:          "GameID123",
		GameTime:        4242,
		RadarImage:      []byte{1, 2, 3, 4},
		IsMultiplayer:   true,
		IsBattle:        true,
	}
	WriteSummary(b, s)
	// Also write other established boxes so opaque test doesn't interfere.
	WriteCamera(b, Camera{XPosition: 123, ZPosition: 456})
	payload := b.Bytes()
	bank, err := OpenBytes(payload)
	if err != nil {
		t.Fatalf("OpenBytes summary: %v", err)
	}
	got, ok := ReadSummary(bank)
	if !ok {
		t.Fatalf("ReadSummary missing")
	}
	if got.MaxUnits != s.MaxUnits || got.Campaign != s.Campaign || got.Mission != s.Mission || got.MapName != s.MapName {
		t.Fatalf("summary mismatch got %+v want %+v", got, s)
	}
	if got.Difficulty != s.Difficulty || got.Side != s.Side || got.Players != s.Players || got.Gametype != s.Gametype {
		t.Fatalf("summary int mismatch got %+v want %+v", got, s)
	}
	if got.Description != s.Description || got.GameID != s.GameID || got.GameTime != s.GameTime {
		t.Fatalf("summary string/int mismatch got %+v want %+v", got, s)
	}
	if !bytes.Equal(got.RadarImage, s.RadarImage) {
		t.Fatalf("RadarImage mismatch %v vs %v", got.RadarImage, s.RadarImage)
	}
	// Default handling: missing Summary should default? But we have it.
	// Check multiplayer defaults: CommanderDeath etc were written; verify round-trip.
	if got.CommanderDeath != s.CommanderDeath || got.Location != s.Location {
		t.Fatalf("multiplayer field mismatch")
	}
}

// TestCameraRoundTrip locks Camera X Position / Z Position [08 "Account inventory"].
func TestCameraRoundTrip(t *testing.T) {
	b := NewBuilder()
	c := Camera{XPosition: 320, ZPosition: -100}
	WriteCamera(b, c)
	payload := b.Bytes()
	bank, err := OpenBytes(payload)
	if err != nil {
		t.Fatalf("OpenBytes camera: %v", err)
	}
	got, ok := ReadCamera(bank)
	if !ok {
		t.Fatalf("ReadCamera missing")
	}
	if got.XPosition != c.XPosition || got.ZPosition != c.ZPosition {
		t.Fatalf("camera mismatch got %+v want %+v", got, c)
	}
}

func TestRetailIntegerCameraAndThumbString(t *testing.T) {
	b := NewBuilder()
	WriteCamera(b, Camera{XPosition: 320, ZPosition: -100})
	WriteSummary(b, Summary{Gametype: 1, Thumbs: "thumbs-identity", IsBattle: true})
	bank, err := OpenBytes(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	camera, ok := bank.Account(CameraAccount)
	if !ok {
		t.Fatal("missing camera account")
	}
	if _, ok := camera.Int("X Position"); !ok {
		t.Fatal("retail camera X Position was not encoded as integer")
	}
	summary, ok := ReadSummary(bank)
	if !ok || summary.Thumbs != "thumbs-identity" {
		t.Fatalf("Thumbs = %q, want bounded string", summary.Thumbs)
	}
}

// TestGameTimeBoxRoundTrip locks the 28-byte game-time box [08 "Scheduler and random state in saves"] [01 §7.3] C15.
func TestGameTimeBoxRoundTrip(t *testing.T) {
	clk := &clock.State{Requested: 7, Active: 4, GlobalTick: 4242}
	clk.AdvanceSP(9)
	b := NewBuilder()
	WriteGameTime(b, clk)
	// Also test that larger box trailing bytes are ignored.
	payload := b.Bytes()
	bank, err := OpenBytes(payload)
	if err != nil {
		t.Fatalf("OpenBytes gametime: %v", err)
	}
	restored, ok := ReadGameTime(bank)
	if !ok {
		t.Fatalf("ReadGameTime missing")
	}
	if restored.GlobalTick != clk.GlobalTick || restored.ScaledAnchor != clk.ScaledAnchor || restored.Delta != clk.Delta || restored.Carry != clk.Carry {
		t.Fatalf("gametime mismatch got %+v want %+v", restored, clk)
	}
	// Short box should fail without partial application.
	b2 := NewBuilder()
	ac := b2.Add(PlayersAccount)
	ac.AppendBox(GameTimeBoxName, 0, []byte{1, 2, 3}) // 3 bytes, not 28
	payload2 := b2.Bytes()
	bank2, err := OpenBytes(payload2)
	if err != nil {
		t.Fatalf("OpenBytes short gametime: %v", err)
	}
	if _, ok := ReadGameTime(bank2); ok {
		t.Fatalf("short GameTime should fail")
	}
	// Larger box: append extra trailing bytes, should still succeed and ignore remainder.
	b3 := NewBuilder()
	ac3 := b3.Add(PlayersAccount)
	box := clk.SaveBox()
	extended := append(append([]byte(nil), box[:]...), []byte{9, 9, 9, 9}...)
	ac3.AppendBox(GameTimeBoxName, 0, extended)
	payload3 := b3.Bytes()
	bank3, err := OpenBytes(payload3)
	if err != nil {
		t.Fatalf("OpenBytes extended: %v", err)
	}
	restored3, ok := ReadGameTime(bank3)
	if !ok {
		t.Fatalf("extended GameTime should succeed")
	}
	if restored3.GlobalTick != clk.GlobalTick {
		t.Fatalf("extended gametime mismatch")
	}
}

// TestAlliancesBoxRoundTrip locks C16: the box is per `Player%i`, exactly 11
// bytes, with the account's own self-alliance byte forced to 1, and row i
// belongs to slot i [08 "Player records"] [GAP T9].
func TestAlliancesBoxRoundTrip(t *testing.T) {
	b := NewBuilder()
	var two [11]byte
	two[3] = 1
	two[7] = 1
	two[2] = 0 // self slot 2 is forced to 1 even though we write 0 here
	WriteAlliances(b, 2, two)
	// A second slot's row must land in its own account and not disturb the
	// first: row i belongs to slot i [08 "Player records"].
	var five [11]byte
	five[9] = 1
	WriteAlliances(b, 5, five)
	payload := b.Bytes()
	bank, err := OpenBytes(payload)
	if err != nil {
		t.Fatalf("OpenBytes alliances: %v", err)
	}
	if ac, ok := bank.Account(PlayersAccount); ok {
		if _, present := ac.BoxData(AlliancesBoxName, 0); present {
			t.Fatal("Alliances must not be an account-level box under Players")
		}
	}
	got, ok := ReadAlliances(bank, 2)
	if !ok {
		t.Fatalf("ReadAlliances missing")
	}
	if got[2] != 1 {
		t.Fatalf("self-alliance not forced: got %d want 1", got[2])
	}
	if got[3] != 1 || got[7] != 1 || got[9] != 0 {
		t.Fatalf("slot 2 row wrong %v", got)
	}
	if len(got) != 11 {
		t.Fatalf("alliances len %d want 11", len(got))
	}
	got5, ok := ReadAlliances(bank, 5)
	if !ok {
		t.Fatalf("ReadAlliances slot 5 missing")
	}
	if got5[5] != 1 || got5[9] != 1 || got5[3] != 0 || got5[7] != 0 {
		t.Fatalf("slot 5 row wrong %v", got5)
	}
	// A slot with no account of its own has no row.
	if _, ok := ReadAlliances(bank, 7); ok {
		t.Fatal("slot 7 has no Player account and must report no row")
	}
	// Wrong size does not load (not 11 bytes) [08 "Player records"] C16.
	b2 := NewBuilder()
	b2.Add("Player0").AppendBox(AlliancesBoxName, 0, []byte{1, 2, 3}) // 3 bytes, not 11
	payload2 := b2.Bytes()
	bank2, err := OpenBytes(payload2)
	if err != nil {
		t.Fatalf("OpenBytes alliances wrong size: %v", err)
	}
	if _, ok := ReadAlliances(bank2, 0); ok {
		t.Fatalf("Alliances with wrong size should be rejected")
	}
}

// TestPlayerSlotRoundTrip locks Player%i field tables [08 "Player records"].
func TestPlayerSlotRoundTrip(t *testing.T) {
	// Need GameTime present for gating [08 "Player records"].
	clk := &clock.State{Requested: 10, Active: 10, GlobalTick: 100}
	clk.AdvanceSP(1)
	b := NewBuilder()
	// Players meta to ensure GameTime gate passes.
	WritePlayersMeta(b, PlayersMeta{HumanPlayer: 0}, clk)
	p := PlayerSlot{
		Index:               2,
		Energy:              123.5,
		Metal:               456.25,
		TotalEnergyProduced: 1000.5,
		TotalMetalProduced:  2000.75,
		TotalEnergyConsumed: 300.125,
		TotalMetalConsumed:  400.5,
		EnergyWasted:        5.5,
		MetalWasted:         6.5,
		PlayerEnergyStorage: 500,
		PlayerMetalStorage:  600,
		AddPlayerStorage:    1,
		Kills:               7,
		Losses:              3,
		UpdateTime:          12345,
		WinLoseTime:         12350,
		DisplayTimer:        12360,
		Controller:          2,
		Logo:                4,
		Side:                1,
	}
	WritePlayerSlot(b, p)
	// Also write a second slot to verify deterministic iteration.
	p2 := PlayerSlot{Index: 0, Energy: 10, Metal: 20, Controller: 1, UpdateTime: 9999}
	WritePlayerSlot(b, p2)
	payload := b.Bytes()
	bank, err := OpenBytes(payload)
	if err != nil {
		t.Fatalf("OpenBytes player: %v", err)
	}
	got, ok := ReadPlayerSlot(bank, 2)
	if !ok {
		t.Fatalf("ReadPlayerSlot 2 missing")
	}
	// Float narrowing check: Energy/Metal stored as double narrowed to f32.
	if got.Energy != p.Energy || got.Metal != p.Metal {
		t.Fatalf("player Energy/Metal mismatch got %v/%v want %v/%v", got.Energy, got.Metal, p.Energy, p.Metal)
	}
	if got.TotalEnergyProduced != p.TotalEnergyProduced || got.UpdateTime != p.UpdateTime || got.WinLoseTime != p.WinLoseTime || got.DisplayTimer != p.DisplayTimer {
		t.Fatalf("player totals/deadlines mismatch got %+v want %+v", got, p)
	}
	if got.Controller != p.Controller || got.Logo != p.Logo || got.Side != p.Side || got.Kills != p.Kills || got.Losses != p.Losses {
		t.Fatalf("player controller/side/kills mismatch got %+v want %+v", got, p)
	}
	// Gating: without GameTime, ReadAllPlayerSlots should return nil.
	b3 := NewBuilder()
	// Write Player0 without GameTime
	WritePlayerSlot(b3, p2)
	payload3 := b3.Bytes()
	bank3, err := OpenBytes(payload3)
	if err != nil {
		t.Fatalf("OpenBytes no gametime: %v", err)
	}
	if slots := ReadAllPlayerSlots(bank3); len(slots) != 0 {
		t.Fatalf("without GameTime, ReadAllPlayerSlots should process zero accounts, got %d", len(slots))
	}
	// With GameTime, should return slots.
	if slots := ReadAllPlayerSlots(bank); len(slots) != 2 {
		t.Fatalf("ReadAllPlayerSlots with GameTime got %d want 2", len(slots))
	}
	// Missing fields default 0 [08 "Player records"].
	b4 := NewBuilder()
	clk2 := &clock.State{Requested: 10, Active: 10}
	WritePlayersMeta(b4, PlayersMeta{HumanPlayer: 1}, clk2)
	// Add Player1 with only Controller, others missing.
	ac := b4.Add(playerAccountName(1))
	ac.SetInt("Controller", 2)
	payload4 := b4.Bytes()
	bank4, err := OpenBytes(payload4)
	if err != nil {
		t.Fatalf("OpenBytes missing fields: %v", err)
	}
	got4, ok := ReadPlayerSlot(bank4, 1)
	if !ok {
		t.Fatalf("ReadPlayerSlot 1 missing after partial")
	}
	if got4.Energy != 0 || got4.Metal != 0 || got4.UpdateTime != 0 {
		t.Fatalf("missing fields should default 0 got %+v", got4)
	}
	if got4.Controller != 2 {
		t.Fatalf("Controller should be 2 got %d", got4.Controller)
	}
}

// TestNormalizeSAV locks C14 .SAV normalization [08 "File naming and write policy"].
func TestNormalizeSAV(t *testing.T) {
	if got := NormalizeSAV("savegame/foo.bar.baz"); got != "savegame/foo.bar.SAV" {
		t.Fatalf("NormalizeSAV foo.bar.baz = %q want savegame/foo.bar.SAV", got)
	}
	if got := NormalizeSAV("savegame\\my.save.test"); got != "savegame\\my.save.SAV" {
		t.Fatalf("NormalizeSAV my.save.test = %q want savegame\\my.save.SAV", got)
	}
	// Dot in directory should be stripped too (not path-component aware) [08 "File naming and write policy"].
	if got := NormalizeSAV("my.dir/save"); got != "my.SAV" {
		t.Fatalf("dot in dir should be stripped, got %q want my.SAV", got)
	}
	if got := NormalizeSAV("nosuffix"); got != "nosuffix.SAV" {
		t.Fatalf("nosuffix = %q want nosuffix.SAV", got)
	}
}

// TestAlliancesArePerPlayerAccount locks the WU-19-158 census: each active
// `Player%i` account carries its own eleven-byte row as its last item, row i
// is slot i's row, and the `Players` account carries no row at all
// [08 "Player records"] [05 R-SHARE-01 §1].
func TestAlliancesArePerPlayerAccount(t *testing.T) {
	var zero, one economy.Player
	// Slots 0 and 1 are allies; slot 2 is hostile to both.
	zero.Allies[0], zero.Allies[1] = true, true
	one.Allies[0], one.Allies[1] = true, true
	var two economy.Player
	two.Allies[2] = true

	clk := &clock.State{Requested: 10, Active: 10, GlobalTick: 100}
	b := NewBuilder()
	WritePlayersMeta(b, PlayersMeta{HumanPlayer: 0}, clk)
	for i, p := range []economy.Player{zero, one, two} {
		WritePlayerSlot(b, PlayerSlotFromEconomy(i, p))
	}
	bank, err := OpenBytes(b.Bytes())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	if ac, ok := bank.Account(PlayersAccount); ok {
		if _, present := ac.BoxData(AlliancesBoxName, 0); present {
			t.Fatal("the Players account must carry no Alliances box")
		}
	}
	for i := 0; i < 3; i++ {
		ac, ok := bank.Account("Player" + string(rune('0'+i)))
		if !ok {
			t.Fatalf("Player%d account missing", i)
		}
		data, ok := ac.BoxData(AlliancesBoxName, 0)
		if !ok || len(data) != 11 {
			t.Fatalf("Player%d Alliances box ok=%v len=%d, want a present 11-byte box", i, ok, len(data))
		}
		if data[i] != 1 {
			t.Fatalf("Player%d self column = %d, want the forced 1", i, data[i])
		}
		if data[10] != 0 {
			t.Fatalf("Player%d column 10 = %d; the neutral row is never allied", i, data[10])
		}
	}
	// Restoring reproduces each row on its own slot, hostility included.
	var restored [3]economy.Player
	for i := 0; i < 3; i++ {
		slot, ok := ReadPlayerSlot(bank, i)
		if !ok || !slot.HasAlliances {
			t.Fatalf("Player%d slot ok=%v hasAlliances=%v", i, ok, slot.HasAlliances)
		}
		slot.ApplyToEconomy(&restored[i])
	}
	if !restored[0].Allies[1] || !restored[1].Allies[0] {
		t.Fatalf("ally pair lost: %v %v", restored[0].Allies, restored[1].Allies)
	}
	if restored[0].Allies[2] || restored[2].Allies[0] || restored[2].Allies[1] {
		t.Fatalf("hostility lost: %v %v", restored[0].Allies, restored[2].Allies)
	}
	for i := 0; i < 3; i++ {
		if !restored[i].Allies[i] {
			t.Fatalf("slot %d self-alliance not forced on restore", i)
		}
	}
}

// TestAllianceBoxAbsenceLeavesRuntimeRow locks the load rule: a box that is
// absent or not exactly 11 bytes does not load, and the runtime row keeps the
// values battle entry gave it [08 "Player records"].
func TestAllianceBoxAbsenceLeavesRuntimeRow(t *testing.T) {
	clk := &clock.State{Requested: 10, Active: 10, GlobalTick: 100}
	b := NewBuilder()
	WritePlayersMeta(b, PlayersMeta{HumanPlayer: 0}, clk)
	WritePlayerSlot(b, PlayerSlot{Index: 0, Controller: 1}) // HasAlliances false
	bank, err := OpenBytes(b.Bytes())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	slot, ok := ReadPlayerSlot(bank, 0)
	if !ok {
		t.Fatal("Player0 missing")
	}
	if slot.HasAlliances {
		t.Fatal("no box was written, so none may be reported")
	}
	live := economy.Player{}
	live.Allies[4] = true
	slot.ApplyToEconomy(&live)
	if !live.Allies[4] {
		t.Fatalf("an absent box must leave the runtime row alone: %v", live.Allies)
	}
}
