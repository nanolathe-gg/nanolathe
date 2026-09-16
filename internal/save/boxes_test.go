package save

import (
	"bytes"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
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
	WriteGameTime(b, clk.SaveBox())
	// Also test that larger box trailing bytes are ignored.
	payload := b.Bytes()
	bank, err := OpenBytes(payload)
	if err != nil {
		t.Fatalf("OpenBytes gametime: %v", err)
	}
	box, ok := ReadGameTime(bank)
	if !ok {
		t.Fatalf("ReadGameTime missing")
	}
	var restored clock.State
	restored.LoadBox(box)
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
	saved := clk.SaveBox()
	extended := append(append([]byte(nil), saved[:]...), []byte{9, 9, 9, 9}...)
	ac3.AppendBox(GameTimeBoxName, 0, extended)
	payload3 := b3.Bytes()
	bank3, err := OpenBytes(payload3)
	if err != nil {
		t.Fatalf("OpenBytes extended: %v", err)
	}
	box3, ok := ReadGameTime(bank3)
	if !ok {
		t.Fatalf("extended GameTime should succeed")
	}
	var restored3 clock.State
	restored3.LoadBox(box3)
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
	writePlayersMeta(b, PlayersMeta{HumanPlayer: 0}, clk)
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
	// Gating: without a 28-byte GameTime box the slot walk does not run
	// [08 "Player records"]. The shipped decoder reports the short box rather
	// than staging a partially loaded battle.
	b3 := NewBuilder()
	// Write Player0 without GameTime
	WritePlayerSlot(b3, p2)
	payload3 := b3.Bytes()
	bank3, err := OpenBytes(payload3)
	if err != nil {
		t.Fatalf("OpenBytes no gametime: %v", err)
	}
	var ungated BattleImage
	if err := decodePlayers(bank3, &ungated); err == nil || len(ungated.Players) != 0 {
		t.Fatalf("without GameTime, no Player%%i account may load: err=%v slots=%d", err, len(ungated.Players))
	}
	// With GameTime, should return slots.
	var gated BattleImage
	if err := decodePlayers(bank, &gated); err != nil || len(gated.Players) != 2 {
		t.Fatalf("with GameTime got %d slots, err=%v, want 2", len(gated.Players), err)
	}
	// Missing fields default 0 [08 "Player records"].
	b4 := NewBuilder()
	clk2 := &clock.State{Requested: 10, Active: 10}
	writePlayersMeta(b4, PlayersMeta{HumanPlayer: 1}, clk2)
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
	writePlayersMeta(b, PlayersMeta{HumanPlayer: 0}, clk)
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
	writePlayersMeta(b, PlayersMeta{HumanPlayer: 0}, clk)
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

// TestSummaryMaxUnitsPresenceIsDistinctFromZero locks the reader's presence
// witness. The restore step stores `Summary.maxunits` into the configured
// unit-limit word "only when present" and applies no clamp [08 R-SESS-01 §9],
// so a Summary that collapses "absent" into "present with value zero" cannot
// express the contract at all — which is what this type did before HasMaxUnits.
func TestSummaryMaxUnitsPresenceIsDistinctFromZero(t *testing.T) {
	read := func(t *testing.T, b *Builder) Summary {
		t.Helper()
		bank, err := OpenBytes(b.Bytes())
		if err != nil {
			t.Fatalf("OpenBytes: %v", err)
		}
		got, ok := ReadSummary(bank)
		if !ok {
			t.Fatal("ReadSummary reported no Summary account")
		}
		return got
	}

	// Present with value zero: the writer emits the item unconditionally, as
	// retail's does, so the value survives as a stored zero.
	present := NewBuilder()
	WriteSummary(present, Summary{Gametype: 2, IsBattle: true})
	if got := read(t, present); !got.HasMaxUnits || got.MaxUnits != 0 {
		t.Fatalf("written zero read back as MaxUnits=%d HasMaxUnits=%v, want a present zero", got.MaxUnits, got.HasMaxUnits)
	}

	// Absent: a Summary account with no `maxunits` item at all.
	absent := NewBuilder()
	ac := absent.Add(SummaryAccount)
	ac.SetInt("Gametype", 2)
	ac.SetInt("Game Time", 7)
	if got := read(t, absent); got.HasMaxUnits || got.MaxUnits != 0 {
		t.Fatalf("absent item read back as MaxUnits=%d HasMaxUnits=%v, want absent", got.MaxUnits, got.HasMaxUnits)
	}

	// A stored value whose low sixteen bits are zero is still present: the
	// truncation is a read-side rule, not an absence [08 "Summary"].
	truncating := NewBuilder()
	WriteSummary(truncating, Summary{MaxUnits: 0x10000, Gametype: 2, IsBattle: true})
	if got := read(t, truncating); !got.HasMaxUnits || got.MaxUnits != 0 {
		t.Fatalf("0x10000 read back as MaxUnits=%d HasMaxUnits=%v, want a present low-16-bit zero", got.MaxUnits, got.HasMaxUnits)
	}

	// A nondefault configured word round-trips whole.
	carried := NewBuilder()
	WriteSummary(carried, Summary{MaxUnits: 137, Gametype: 2, IsBattle: true})
	if got := read(t, carried); !got.HasMaxUnits || got.MaxUnits != 137 {
		t.Fatalf("137 read back as MaxUnits=%d HasMaxUnits=%v", got.MaxUnits, got.HasMaxUnits)
	}
}

// The two storage items are the storage-bonus operands, not the derived
// capacities: the writer takes them from the bonus fields and the reader puts
// them back there, leaving capacity for the first settlement pass to rebuild
// [08 "Player records"] [05 R-ECO-01 §4].
func TestPlayerSlotStorageItemsAreTheBonusOperands(t *testing.T) {
	var p economy.Player
	p.StorageBonusEnabled = true
	p.StorageBonus[economy.Energy], p.StorageBonus[economy.Metal] = 1000, 200
	p.Capacity[economy.Energy], p.Capacity[economy.Metal] = 4321, 8765
	slot := PlayerSlotFromEconomy(3, p)
	if slot.PlayerEnergyStorage != 1000 || slot.PlayerMetalStorage != 200 || slot.AddPlayerStorage != 1 {
		t.Fatalf("writer took %v/%v flag %d, want the bonus operands 1000/200 flag 1", slot.PlayerEnergyStorage, slot.PlayerMetalStorage, slot.AddPlayerStorage)
	}
	var dst economy.Player
	slot.ApplyToEconomy(&dst)
	if dst.StorageBonus[economy.Energy] != 1000 || dst.StorageBonus[economy.Metal] != 200 || !dst.StorageBonusEnabled {
		t.Fatalf("reader restored bonus %v flag %v, want 1000/200 enabled", dst.StorageBonus, dst.StorageBonusEnabled)
	}
	if dst.Capacity[economy.Energy] != 0 || dst.Capacity[economy.Metal] != 0 {
		t.Fatalf("reader wrote capacity %v; capacity is rebuilt at the first settlement, never restored", dst.Capacity)
	}
}

// TestPlayersMetaHumanPlayerLoadDefault locks the item's load default: a
// Players account that carries no "Human Player" integer reads back 10, no
// human [08 R-SAVE-02 §12]. Ten is outside the 0..9 slot range the restore
// accepts, so the zero value this replaces silently named slot 0 as the local
// and viewing player on a save that lost the item.
func TestPlayersMetaHumanPlayerLoadDefault(t *testing.T) {
	clk := &clock.State{Requested: 10, Active: 10, GlobalTick: 100}
	// WriteGameTime creates the Players account with the GameTime box alone, so
	// the account is present and the item is not.
	b := NewBuilder()
	WriteGameTime(b, clk.SaveBox())
	bank, err := OpenBytes(b.Bytes())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	meta, present := ReadPlayersMeta(bank)
	if !present {
		t.Fatal("the Players account is present; ReadPlayersMeta reported it missing")
	}
	if meta.HumanPlayer != 10 {
		t.Fatalf("Human Player absent reads %d, want the load default 10 [08 R-SAVE-02 §12]", meta.HumanPlayer)
	}

	// A bank with no Players account at all answers the same default, so no
	// caller can read a fabricated slot 0 out of an absent account either.
	empty, err := OpenBytes(NewBuilder().Bytes())
	if err != nil {
		t.Fatalf("OpenBytes empty: %v", err)
	}
	if meta, present := ReadPlayersMeta(empty); present || meta.HumanPlayer != 10 {
		t.Fatalf("absent Players account reads (%d,%v), want (10,false) [08 R-SAVE-02 §12]", meta.HumanPlayer, present)
	}

	// A written item still wins, including slot 0.
	b2 := NewBuilder()
	writePlayersMeta(b2, PlayersMeta{HumanPlayer: 0}, clk)
	bank2, err := OpenBytes(b2.Bytes())
	if err != nil {
		t.Fatalf("OpenBytes written: %v", err)
	}
	if meta, present := ReadPlayersMeta(bank2); !present || meta.HumanPlayer != 0 {
		t.Fatalf("written Human Player 0 reads (%d,%v), want (0,true)", meta.HumanPlayer, present)
	}
}

// writePlayersMeta is the Players-account meta these tests build: the
// "Human Player" item followed by the 28-byte GameTime box, which is what
// RetailProjection.Build emits [08 "Account inventory"]
// [08 "Scheduler and random state in saves"].
func writePlayersMeta(b *Builder, meta PlayersMeta, clk *clock.State) {
	if b == nil {
		return
	}
	builderAccount(b, PlayersAccount).SetInt("Human Player", meta.HumanPlayer)
	if clk != nil {
		WriteGameTime(b, clk.SaveBox())
	}
}
