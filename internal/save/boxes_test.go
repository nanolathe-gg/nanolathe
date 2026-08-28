package save

import (
	"bytes"
	"testing"

	"github.com/nanolathe/nanolathe/internal/clock"
)

// TestSummaryRoundTrip locks the Summary writer table order and field defaults
// [08 "Summary"].
func TestSummaryRoundTrip(t *testing.T) {
	b := NewBuilder("")
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
		Thumbs:          7,
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
	WriteCamera(b, Camera{XPosition: 123.5, ZPosition: 456.5})
	payload := b.Bytes()
	bank, err := OpenBytes(payload, RetailTag)
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
	b := NewBuilder("")
	c := Camera{XPosition: 320.5, ZPosition: -100.25}
	WriteCamera(b, c)
	payload := b.Bytes()
	bank, err := OpenBytes(payload, RetailTag)
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

// TestGameTimeBoxRoundTrip locks the 28-byte game-time box [08 "Scheduler and random state in saves"] [01 §7.3] C15.
func TestGameTimeBoxRoundTrip(t *testing.T) {
	clk := &clock.State{Requested: 7, Active: 4, GlobalTick: 4242}
	clk.AdvanceSP(9)
	b := NewBuilder("")
	WriteGameTime(b, clk)
	// Also test that larger box trailing bytes are ignored.
	payload := b.Bytes()
	bank, err := OpenBytes(payload, RetailTag)
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
	b2 := NewBuilder("")
	ac := b2.Add(PlayersAccount)
	ac.AppendBox(GameTimeBoxName, 0, []byte{1, 2, 3}) // 3 bytes, not 28
	payload2 := b2.Bytes()
	bank2, err := OpenBytes(payload2, RetailTag)
	if err != nil {
		t.Fatalf("OpenBytes short gametime: %v", err)
	}
	if _, ok := ReadGameTime(bank2); ok {
		t.Fatalf("short GameTime should fail")
	}
	// Larger box: append extra trailing bytes, should still succeed and ignore remainder.
	b3 := NewBuilder("")
	ac3 := b3.Add(PlayersAccount)
	box := clk.SaveBox()
	extended := append(append([]byte(nil), box[:]...), []byte{9, 9, 9, 9}...)
	ac3.AppendBox(GameTimeBoxName, 0, extended)
	payload3 := b3.Bytes()
	bank3, err := OpenBytes(payload3, RetailTag)
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

// TestAlliancesBoxRoundTrip locks C16: exactly 11 bytes with forced self-alliance 1 [08 "Player records"] [GAP T9].
func TestAlliancesBoxRoundTrip(t *testing.T) {
	b := NewBuilder("")
	var alliances [11]byte
	for i := range alliances {
		alliances[i] = 0
	}
	alliances[3] = 1
	alliances[7] = 1
	// self slot 2 should be forced to 1 even if we write 0 there.
	alliances[2] = 0
	WriteAlliances(b, 2, alliances)
	payload := b.Bytes()
	bank, err := OpenBytes(payload, RetailTag)
	if err != nil {
		t.Fatalf("OpenBytes alliances: %v", err)
	}
	// Correct size should succeed and force self.
	got, ok := ReadAlliances(bank, 2)
	if !ok {
		t.Fatalf("ReadAlliances missing")
	}
	if got[2] != 1 {
		t.Fatalf("self-alliance not forced: got %d want 1", got[2])
	}
	if got[3] != 1 || got[7] != 1 {
		t.Fatalf("alliances other bytes lost %v", got)
	}
	if len(got) != 11 {
		t.Fatalf("alliances len %d want 11", len(got))
	}
	// Wrong size should be rejected (not 11 bytes) [08 "Player records"] C16.
	b2 := NewBuilder("")
	ac2 := b2.Add(PlayersAccount)
	ac2.AppendBox(AlliancesBoxName, 0, []byte{1, 2, 3}) // 3 bytes, not 11
	payload2 := b2.Bytes()
	bank2, err := OpenBytes(payload2, RetailTag)
	if err != nil {
		t.Fatalf("OpenBytes alliances wrong size: %v", err)
	}
	if _, ok := ReadAlliances(bank2, 0); ok {
		t.Fatalf("Alliances with wrong size should be rejected")
	}
	// Verify that reading with different selfSlot still forces that slot.
	got3, ok := ReadAlliances(bank, 5)
	if !ok {
		t.Fatalf("ReadAlliances second read missing")
	}
	if got3[5] != 1 {
		t.Fatalf("self-alliance 5 not forced %v", got3)
	}
}

// TestPlayerSlotRoundTrip locks Player%i field tables [08 "Player records"].
func TestPlayerSlotRoundTrip(t *testing.T) {
	// Need GameTime present for gating [08 "Player records"].
	clk := &clock.State{Requested: 10, Active: 10, GlobalTick: 100}
	clk.AdvanceSP(1)
	b := NewBuilder("")
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
	bank, err := OpenBytes(payload, RetailTag)
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
	b3 := NewBuilder("")
	// Write Player0 without GameTime
	WritePlayerSlot(b3, p2)
	payload3 := b3.Bytes()
	bank3, err := OpenBytes(payload3, RetailTag)
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
	b4 := NewBuilder("")
	clk2 := &clock.State{Requested: 10, Active: 10}
	WritePlayersMeta(b4, PlayersMeta{HumanPlayer: 1}, clk2)
	// Add Player1 with only Controller, others missing.
	ac := b4.Add(playerAccountName(1))
	ac.SetInt("Controller", 2)
	payload4 := b4.Bytes()
	bank4, err := OpenBytes(payload4, RetailTag)
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
