// Package save — save contents (boxes) per [08 "Save-file organization"].
//
// This file implements the established retail account helpers C14–C16 of PLAN_14:
//
//	C14 non-transactional load policy; .SAV normalization strips after last
//	dot; writes truncate-open directly, post-open errors ignored returning 1
//	[08 "File naming and write policy"] [GAP T9].
//	C15 28-byte game-time box round-trip (PLAN_03 C14) [08 "Scheduler and random
//	state in saves"] [01 §7.3].
//	C16 Alliances box exactly 11 bytes with forced self-alliance 1 [08 "Player
//	records"] [GAP T9].
package save

import (
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/internal/clock"
)

// ---------------------------------------------------------------------------
// File naming and write policy (C14) [08 "File naming and write policy"].
// ---------------------------------------------------------------------------

// NormalizeSavePath reproduces retail .SAV normalization: strip the last dot
// and everything after it from the assembled path (not path-component aware),
// then append .SAV [08 "File naming and write policy"]. Wrapper around
// bank.go's NormalizeSAV for the C14 contract; the logic lives in bank.go.
func NormalizeSavePath(p string) string { return NormalizeSAV(p) }

// WriteBankFile implements the retail write policy: open directly for
// truncate-write (an existing file truncates at successful open); after open,
// every write and close result is ignored and the caller sees success
// [08 "File naming and write policy"]. It normalizes the path first and
// returns nil on post-open success even if close fails; only open failure
// returns an error. The caller ignores the result per retail (C14 returns 1).
func WriteBankFile(path string, data []byte) error {
	// C14: .SAV normalization strips after last dot [08 "File naming and write policy"].
	norm := NormalizeSAV(path)
	// C14: truncate-open directly; post-open errors ignored returning success.
	return WriteFile(norm, data)
}

// OpenBankFile opens a save after .SAV normalization. It is the load-side
// counterpart to WriteBankFile's normalization (C14).
func OpenBankFile(path string) (*Bank, error) {
	return Open(NormalizeSAV(path))
}

// ---------------------------------------------------------------------------
// Summary account [08 "Save-file organization"] [08 "Summary"].
// ---------------------------------------------------------------------------

// Summary holds the established Summary writer fields in the order the writer
// emits them [08 "Summary"]. Fields beyond this list remain TODO(T25) unknown
// and are not decoded by this account helper.
type Summary struct {
	// Dynamic build keys (integer 0) [08 "Summary"].
	BuildDateKey string // e.g. "BUILD DATE:Aug 23 2026" — prefix BUILD DATE:
	BuildTimeKey string // e.g. "BUILD TIME:12:00:00" — prefix BUILD TIME:

	MaxUnits   int32  // low 16 bits else 0 on load missing [08 "Summary"]
	Campaign   string // [08 "Summary"]
	Mission    string // [08 "Summary"]
	MapName    string // Map [08 "Summary"]
	Difficulty int32  // default 0 [08 "Summary"]
	Side       int32  // default 0 [08 "Summary"]
	Players    int32
	Gametype   int32 // 1 campaign, 2 multiplayer [08 "Summary"] [GAP T9]
	Thumbs     int32 // copied during preflight; malformed absence can copy from null [08 "Summary"]

	// Multiplayer-only fields: written only when Gametype==2; defaults 1 on
	// missing/mistyped per [08 "Summary"].
	CommanderDeath  int32
	Location        int32
	Mapping         int32 // Summary integer named Mapping distinct from Mapping account [08 "Summary"]
	LineOfSight     int32
	LineOfSightType int32

	BetweenMissions int32  // =1 on saves written outside a live battle [08 "Summary"]
	Description     string // when caller supplies non-null [08 "Summary"]
	GameID          string // "Game ID" [08 "Summary"]
	GameTime        int32  // globalTick presentation metadata [08 "Summary"]; authoritative tick is Players/GameTime 28-byte box.

	RadarImage []byte // Radar Image binary box, live-battle saves only [08 "Summary"]

	IsMultiplayer bool // derived: Gametype==2
	IsBattle      bool // true if live-battle save (RadarImage present or BetweenMissions==0)
}

// builderAccount returns the existing account for name or creates it.
// It prevents duplicate account headers when multiple helpers write to the
// same logical account (e.g., Players) via separate calls. Retail merges
// duplicate accounts [08 "Location and representation"], but our Builder would
// otherwise emit two headers with the same name; merging here keeps the bank
// readable via Bank.Account (which returns the first) and preserves the
// intended single-account layout.
func builderAccount(b *Builder, name string) *Account {
	for _, ac := range b.Accounts {
		if ac.Name == name {
			return ac
		}
	}
	return b.Add(name)
}

// WriteSummary writes the Summary account fields in the exact order the
// retail writer emits them [08 "Summary"].
//
// Order: BUILD DATE/BUI keys (int 0), maxunits, Campaign, Mission, Map,
// Difficulty, Side, Players, Gametype, Thumbs, then multiplayer-only
// CommanderDeath, Location, Mapping, LineOfSight, LineOfSightType, then
// BetweenMissions (=1 on non-battle saves), Description (when non-null),
// Game ID, Game Time, and the live-battle-only Radar Image box [08 "Summary"].
func WriteSummary(b *Builder, s Summary) {
	if b == nil {
		return
	}
	ac := builderAccount(b, SummaryAccount)
	// Dynamic build keys [08 "Summary"] — emit with integer 0.
	bd := s.BuildDateKey
	if bd == "" {
		bd = "BUILD DATE:"
	} else if !strings.HasPrefix(bd, "BUILD DATE:") {
		bd = "BUILD DATE:" + bd
	}
	bt := s.BuildTimeKey
	if bt == "" {
		bt = "BUILD TIME:"
	} else if !strings.HasPrefix(bt, "BUILD TIME:") {
		bt = "BUILD TIME:" + bt
	}
	ac.SetInt(bd, 0)
	ac.SetInt(bt, 0)
	ac.SetInt("maxunits", s.MaxUnits)
	ac.SetString("Campaign", s.Campaign)
	ac.SetString("Mission", s.Mission)
	ac.SetString("Map", s.MapName)
	ac.SetInt("Difficulty", s.Difficulty)
	ac.SetInt("Side", s.Side)
	ac.SetInt("Players", s.Players)
	ac.SetInt("Gametype", s.Gametype)
	ac.SetInt("Thumbs", s.Thumbs)
	// Multiplayer-only [08 "Summary"].
	if s.IsMultiplayer || s.Gametype == 2 {
		ac.SetInt("CommanderDeath", s.CommanderDeath)
		ac.SetInt("Location", s.Location)
		ac.SetInt("Mapping", s.Mapping)
		ac.SetInt("LineOfSight", s.LineOfSight)
		ac.SetInt("LineOfSightType", s.LineOfSightType)
	}
	// BetweenMissions =1 on saves written outside a live battle [08 "Summary"].
	if s.BetweenMissions != 0 || !s.IsBattle {
		// Emit only when needed; retail emits 1 on non-battle saves.
		// For battle saves we emit only if caller explicitly set 1.
		if s.BetweenMissions != 0 {
			ac.SetInt("BetweenMissions", s.BetweenMissions)
		} else if !s.IsBattle {
			ac.SetInt("BetweenMissions", 1)
		}
	}
	if s.Description != "" {
		ac.SetString("Description", s.Description)
	}
	if s.GameID != "" {
		ac.SetString("Game ID", s.GameID)
	}
	ac.SetInt("Game Time", s.GameTime)
	if len(s.RadarImage) > 0 {
		ac.AppendBox(RadarImageBoxName, 0, s.RadarImage)
	}
	// TODO(T25): retail save bulk-box byte layouts beyond established lengths
	// remain unknown and are not decoded here [GAP T25].
}

const (
	SummaryAccount    = "Summary"
	CameraAccount     = "Camera"
	PlayersAccount    = "Players"
	GameTimeBoxName   = "GameTime"
	AlliancesBoxName  = "Alliances"
	RadarImageBoxName = "Radar Image"
)

// ReadSummary reads the Summary account with preflight defaults [08 "Summary"].
// Missing/mistyped handling: maxunits low 16 bits else 0; Difficulty/Side/Players
// default 0; the five multiplayer rule fields default 1 [08 "Summary"].
func ReadSummary(bank *Bank) (Summary, bool) {
	var s Summary
	ac, ok := bank.Account(SummaryAccount)
	if !ok {
		return s, false
	}
	// Find build keys by prefix [08 "Summary"].
	for _, it := range ac.Ints {
		if strings.HasPrefix(it.Name, "BUILD DATE:") {
			s.BuildDateKey = it.Name
		} else if strings.HasPrefix(it.Name, "BUILD TIME:") {
			s.BuildTimeKey = it.Name
		}
	}
	if s.BuildDateKey == "" {
		s.BuildDateKey = "BUILD DATE:"
	}
	if s.BuildTimeKey == "" {
		s.BuildTimeKey = "BUILD TIME:"
	}
	if v, ok := ac.Int("maxunits"); ok {
		s.MaxUnits = int32(uint16(v)) // low 16 bits [08 "Summary"]
	} else {
		s.MaxUnits = 0
	}
	if v, ok := ac.Str("Campaign"); ok {
		s.Campaign = v
	}
	if v, ok := ac.Str("Mission"); ok {
		s.Mission = v
	}
	if v, ok := ac.Str("Map"); ok {
		s.MapName = v
	}
	if v, ok := ac.Int("Difficulty"); ok {
		s.Difficulty = v
	} else {
		s.Difficulty = 0
	}
	if v, ok := ac.Int("Side"); ok {
		s.Side = v
	} else {
		s.Side = 0
	}
	if v, ok := ac.Int("Players"); ok {
		s.Players = v
	} else {
		s.Players = 0
	}
	if v, ok := ac.Int("Gametype"); ok {
		s.Gametype = v
	} else {
		s.Gametype = 0
	}
	if v, ok := ac.Int("Thumbs"); ok {
		s.Thumbs = v
	} else {
		s.Thumbs = 0
	}
	s.IsMultiplayer = s.Gametype == 2
	// Multiplayer-only defaults 1 [08 "Summary"].
	if v, ok := ac.Int("CommanderDeath"); ok {
		s.CommanderDeath = v
	} else {
		s.CommanderDeath = 1
	}
	if v, ok := ac.Int("Location"); ok {
		s.Location = v
	} else {
		s.Location = 1
	}
	if v, ok := ac.Int("Mapping"); ok {
		s.Mapping = v
	} else {
		s.Mapping = 1
	}
	if v, ok := ac.Int("LineOfSight"); ok {
		s.LineOfSight = v
	} else {
		s.LineOfSight = 1
	}
	if v, ok := ac.Int("LineOfSightType"); ok {
		s.LineOfSightType = v
	} else {
		s.LineOfSightType = 1
	}
	if v, ok := ac.Int("BetweenMissions"); ok {
		s.BetweenMissions = v
	}
	if v, ok := ac.Str("Description"); ok {
		s.Description = v
	}
	if v, ok := ac.Str("Game ID"); ok {
		s.GameID = v
	}
	if v, ok := ac.Int("Game Time"); ok {
		s.GameTime = v
	}
	if data, ok := ac.BoxData(RadarImageBoxName, 0); ok {
		s.RadarImage = append([]byte(nil), data...)
		s.IsBattle = true
	} else {
		s.IsBattle = s.BetweenMissions == 0
	}
	return s, true
}

// ---------------------------------------------------------------------------
// Camera account [08 "Save-file organization"] Account inventory.
// ---------------------------------------------------------------------------

// Camera holds the established Camera fields [08 "Account inventory"].
type Camera struct {
	XPosition float64 // "X Position" double [08 "Account inventory"]
	ZPosition float64 // "Z Position" double
}

// WriteCamera writes the Camera account [08 "Account inventory"].
func WriteCamera(b *Builder, c Camera) {
	if b == nil {
		return
	}
	ac := builderAccount(b, CameraAccount)
	ac.SetDouble("X Position", c.XPosition)
	ac.SetDouble("Z Position", c.ZPosition)
}

// ReadCamera reads the Camera account [08 "Account inventory"].
func ReadCamera(bank *Bank) (Camera, bool) {
	ac, ok := bank.Account(CameraAccount)
	if !ok {
		return Camera{}, false
	}
	var c Camera
	if v, ok := ac.Double("X Position"); ok {
		c.XPosition = v
	}
	if v, ok := ac.Double("Z Position"); ok {
		c.ZPosition = v
	}
	return c, true
}

// ---------------------------------------------------------------------------
// Players/GameTime 28-byte box [08 "Scheduler and random state in saves"]
// [01 §7.3] [PLAN_03 C14] (I13 exception).
// ---------------------------------------------------------------------------

// WriteGameTime writes the 28-byte scheduler block into Players/GameTime
// [08 "Scheduler and random state in saves"]. The writer copies those 28 bytes
// verbatim; the loader requires at least 28 bytes and ignores trailing bytes.
func WriteGameTime(b *Builder, clk *clock.State) {
	if b == nil || clk == nil {
		return
	}
	ac := builderAccount(b, PlayersAccount)
	box := clk.SaveBox() // 28 bytes [08 "Scheduler and random state in saves"] [01 §7.3]
	ac.AppendBox(GameTimeBoxName, 0, box[:])
}

// ReadGameTime reads the 28-byte scheduler block from Players/GameTime.
// It requires at least 28 bytes; larger boxes have trailing bytes ignored;
// a short or absent read fails without partial application [08 "Scheduler and
// random state in saves"].
func ReadGameTime(bank *Bank) (*clock.State, bool) {
	ac, ok := bank.Account(PlayersAccount)
	if !ok {
		return nil, false
	}
	data, ok := ac.BoxData(GameTimeBoxName, 0)
	if !ok || len(data) < 28 {
		return nil, false
	}
	// Larger boxes: ignore trailing bytes [08 "Scheduler and random state in saves"].
	var box [28]byte
	copy(box[:], data[:28])
	var clk clock.State
	clk.LoadBox(box)
	return &clk, true
}

// ---------------------------------------------------------------------------
// Alliances box — exactly 11 bytes with forced self-alliance 1
// [08 "Player records"] [GAP T9] C16.
// ---------------------------------------------------------------------------

// WriteAlliances writes the Alliances box as exactly 11 bytes into
// Players/Alliances [08 "Player records"] [GAP T9] C16. The self byte is
// forced to 1 on read, not necessarily on write, but we force it here for
// canonical output.
func WriteAlliances(b *Builder, selfSlot int, alliances [11]byte) {
	if b == nil {
		return
	}
	// C16: forced self-alliance 1 [08 "Player records"] [GAP T9].
	if selfSlot >= 0 && selfSlot < 11 {
		alliances[selfSlot] = 1
	}
	ac := builderAccount(b, PlayersAccount)
	ac.AppendBox(AlliancesBoxName, 0, alliances[:])
}

// ReadAlliances reads the Alliances box, validates exactly 11 bytes, and
// forces the self-alliance byte to 1 [08 "Player records"] [GAP T9] C16.
// It returns the 11-byte payload and whether the box was present with exact size.
func ReadAlliances(bank *Bank, selfSlot int) ([11]byte, bool) {
	var out [11]byte
	ac, ok := bank.Account(PlayersAccount)
	if !ok {
		return out, false
	}
	data, ok := ac.BoxData(AlliancesBoxName, 0)
	if !ok || len(data) != 11 { // exactly 11 bytes [08 "Player records"] [GAP T9] C16
		return out, false
	}
	copy(out[:], data)
	if selfSlot >= 0 && selfSlot < 11 {
		out[selfSlot] = 1 // forced self-alliance [08 "Player records"] [GAP T9] C16
	}
	return out, true
}

// ---------------------------------------------------------------------------
// Player%i field tables [08 "Player records"].
// ---------------------------------------------------------------------------

// PlayerSlot holds the established Player%i scalar fields with wire types
// and defaults as transcribed verbatim with citations; unknown fields stay
// TODO(T25) opaque [08 "Player records"].
type PlayerSlot struct {
	Index int // 0..9 — selects account Player%i [08 "Player records"]

	// Wire type double, runtime f32 narrowed [08 "Player records"]:
	Energy float32
	Metal  float32

	// Wire type double, runtime f64 [08 "Player records"]:
	TotalEnergyProduced float64
	TotalMetalProduced  float64
	TotalEnergyConsumed float64
	TotalMetalConsumed  float64
	EnergyWasted        float64
	MetalWasted         float64

	// Wire type double, runtime f32 narrowed [08 "Player records"]:
	PlayerEnergyStorage float32
	PlayerMetalStorage  float32

	// Wire type integer, runtime bit 0 of halfword [08 "Player records"]:
	AddPlayerStorage uint16 // 0/1

	// Wire type integer, runtime low signed 16 bits [08 "Player records"]:
	Kills  int16
	Losses int16

	// Wire type integer, runtime i32 [08 "Player records"]:
	// UpdateTime is the player's economy settlement deadline — absolute tick
	// compared against global tick and advanced by 30 when due [05 "Authoritative
	// settlement order"] [08 "Player records"].
	UpdateTime   int32
	WinLoseTime  int32
	DisplayTimer int32

	// Wire type integer, runtime u8 [08 "Player records"]:
	Controller uint8
	// Wire type integer, runtime low byte [08 "Player records"]:
	Logo uint8
	Side uint8
	// TODO(T25): fields beyond the established table (network identity,
	// connection/alive state, sharing options, etc) remain unknown and are kept
	// unsupported until their retail consumers are established [GAP T25].
}

// playerAccountName returns Player%i account name [08 "Player records"].
func playerAccountName(i int) string { return fmt.Sprintf("Player%d", i) }

// WritePlayerSlot writes one Player%i account with the established field table
// [08 "Player records"]. Fields are written in table order for determinism (I1).
func WritePlayerSlot(b *Builder, p PlayerSlot) {
	if b == nil || p.Index < 0 || p.Index >= 10 {
		return
	}
	ac := builderAccount(b, playerAccountName(p.Index))
	// Double wire fields [08 "Player records"].
	ac.SetDouble("Energy", float64(p.Energy))
	ac.SetDouble("Metal", float64(p.Metal))
	ac.SetDouble("TotalEnergyProduced", p.TotalEnergyProduced)
	ac.SetDouble("TotalMetalProduced", p.TotalMetalProduced)
	ac.SetDouble("TotalEnergyConsumed", p.TotalEnergyConsumed)
	ac.SetDouble("TotalMetalConsumed", p.TotalMetalConsumed)
	ac.SetDouble("EnergyWasted", p.EnergyWasted)
	ac.SetDouble("MetalWasted", p.MetalWasted)
	ac.SetDouble("PlayerEnergyStorage", float64(p.PlayerEnergyStorage))
	ac.SetDouble("PlayerMetalStorage", float64(p.PlayerMetalStorage))
	// Integer wire fields [08 "Player records"].
	ac.SetInt("AddPlayerStorage", int32(p.AddPlayerStorage&1)) // bit 0 of halfword [08 "Player records"]
	ac.SetInt("Kills", int32(p.Kills))                         // low signed 16 [08 "Player records"]
	ac.SetInt("Losses", int32(p.Losses))
	ac.SetInt("UpdateTime", p.UpdateTime) // i32 [08 "Player records"]
	ac.SetInt("WinLoseTime", p.WinLoseTime)
	ac.SetInt("DisplayTimer", p.DisplayTimer)
	ac.SetInt("Controller", int32(p.Controller)) // u8 [08 "Player records"]
	ac.SetInt("Logo", int32(p.Logo))             // low byte [08 "Player records"]
	ac.SetInt("Side", int32(p.Side))             // low byte [08 "Player records"]
}

// ReadPlayerSlot reads one Player%i account with the defaults from the table
// [08 "Player records"]: missing/wrong defaults 0.0 or 0 as indicated.
// Returns the slot and whether the account existed (at least one known field present
// or the account itself exists). Wire narrowing is applied per column.
func ReadPlayerSlot(bank *Bank, index int) (PlayerSlot, bool) {
	var p PlayerSlot
	p.Index = index
	ac, ok := bank.Account(playerAccountName(index))
	if !ok {
		return p, false
	}
	// Helper to read double wire with 0.0 default [08 "Player records"].
	getDouble := func(name string) float64 {
		if v, ok := ac.Double(name); ok {
			return v
		}
		return 0.0
	}
	getInt := func(name string) int32 {
		if v, ok := ac.Int(name); ok {
			return v
		}
		return 0
	}
	p.Energy = float32(getDouble("Energy")) // narrowed f32 [08 "Player records"]
	p.Metal = float32(getDouble("Metal"))
	p.TotalEnergyProduced = getDouble("TotalEnergyProduced")
	p.TotalMetalProduced = getDouble("TotalMetalProduced")
	p.TotalEnergyConsumed = getDouble("TotalEnergyConsumed")
	p.TotalMetalConsumed = getDouble("TotalMetalConsumed")
	p.EnergyWasted = getDouble("EnergyWasted")
	p.MetalWasted = getDouble("MetalWasted")
	p.PlayerEnergyStorage = float32(getDouble("PlayerEnergyStorage"))
	p.PlayerMetalStorage = float32(getDouble("PlayerMetalStorage"))
	p.AddPlayerStorage = uint16(getInt("AddPlayerStorage") & 1) // bit 0 of halfword [08 "Player records"]
	p.Kills = int16(getInt("Kills"))                            // low signed 16 [08 "Player records"]
	p.Losses = int16(getInt("Losses"))
	p.UpdateTime = getInt("UpdateTime") // i32 [08 "Player records"]
	p.WinLoseTime = getInt("WinLoseTime")
	p.DisplayTimer = getInt("DisplayTimer")
	p.Controller = uint8(getInt("Controller") & 0xFF) // u8 [08 "Player records"]
	p.Logo = uint8(getInt("Logo") & 0xFF)             // low byte [08 "Player records"]
	p.Side = uint8(getInt("Side") & 0xFF)
	return p, true
}

// ReadAllPlayerSlots is gated on a successful 28-byte Players/GameTime read:
// a short or absent read processes zero Player%i accounts (after the human-player
// byte has already been applied) [08 "Player records"]. It returns up to 10 slots
// that were present; the human-player byte is handled separately via ReadHumanPlayer.
func ReadAllPlayerSlots(bank *Bank) []PlayerSlot {
	if _, ok := ReadGameTime(bank); !ok { // gate [08 "Player records"]
		return nil
	}
	var out []PlayerSlot
	for i := 0; i < 10; i++ {
		if p, ok := ReadPlayerSlot(bank, i); ok {
			out = append(out, p)
		}
	}
	return out
}

// PlayersMeta holds the Players-account scalars outside Player%i [08 "Account inventory"].
type PlayersMeta struct {
	HumanPlayer int32 // "Human Player" [08 "Account inventory"]
}

// WritePlayersMeta writes Players-account meta: Human Player and the 28-byte
// GameTime box via WriteGameTime (C15) [08 "Account inventory"] [08 "Scheduler and random state in saves"].
func WritePlayersMeta(b *Builder, meta PlayersMeta, clk *clock.State) {
	if b == nil {
		return
	}
	ac := builderAccount(b, PlayersAccount)
	ac.SetInt("Human Player", meta.HumanPlayer)
	if clk != nil {
		// WriteGameTime appends the 28-byte box [08 "Scheduler and random state in saves"].
		// We already have the account; avoid duplicate Add by directly appending.
		box := clk.SaveBox()
		ac.AppendBox(GameTimeBoxName, 0, box[:])
	}
}

// ReadPlayersMeta reads Human Player (always) and GameTime gating is handled by
// callers that need Player%i; it returns the meta even if GameTime is short.
func ReadPlayersMeta(bank *Bank) (PlayersMeta, bool) {
	ac, ok := bank.Account(PlayersAccount)
	if !ok {
		return PlayersMeta{}, false
	}
	var m PlayersMeta
	if v, ok := ac.Int("Human Player"); ok {
		m.HumanPlayer = v
	}
	// Note: Human Player byte is applied even when GameTime is short/absent,
	// per the gating clause that says "(after the human-player byte has already
	// been applied)" [08 "Player records"].
	return m, true
}
