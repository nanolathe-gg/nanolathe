// Package save — save contents (boxes) per [08 "Save-file organization"].
//
// This file implements C14–C16, C18 of PLAN_14:
//
//	C14 non-transactional load policy; .SAV normalization strips after last
//	dot; writes truncate-open directly, post-open errors ignored returning 1
//	[08 "File naming and write policy"] [GAP T9].
//	C15 28-byte game-time box round-trip (PLAN_03 C14) [08 "Scheduler and random
//	state in saves"] [01 §7.3].
//	C16 Alliances box exactly 11 bytes with forced self-alliance 1 [08 "Player
//	records"] [GAP T9].
//	C18 Native Nanolathe account with versioned StateV1 box: canonically encodes
//	every mutable authoritative service (slot-indexed records, queue payloads),
//	both RNG states+draw counts, catalog/manifest hashes to reject wrong content;
//	reconstruction ONLY through published package APIs; retail save without
//	StateV1 ⇒ explicit unsupported diagnostic for full continuation, metadata
//	boxes still readable [GAP T25] [08 "Save-file organization"].
//
// I13 exception: the 28-byte game-time box and StateV1 codec cross a boundary
// as bytes, so byte layout is the contract [docs/INVARIANTS.md I13].
package save

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
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
// emits them [08 "Summary"]. Fields beyond this list are TODO(T25) opaque and
// remain as raw unknown accounts/boxes that round-trip byte-for-byte (C18).
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
	// remain opaque. Unknown Summary fields beyond the list above stay as raw
	// boxes/ints and round-trip via opaque pass-through [GAP T25].
}

const (
	SummaryAccount    = "Summary"
	CameraAccount     = "Camera"
	PlayersAccount    = "Players"
	NanolatheAccount  = "Nanolathe"
	StateV1BoxName    = "StateV1"
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
	// opaque via the pass-through path [GAP T25].
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

// ---------------------------------------------------------------------------
// Opaque retail pass-through (C18) — unknown accounts/boxes round-trip byte-for-byte.
// ---------------------------------------------------------------------------

// CopyOpaque copies every account/box from src into dst that is not part of the
// established Nanolathe-authored set, preserving bytes verbatim for unknown
// retail spans [GAP T25]. Known accounts are skipped to avoid duplicating
// fields already written by the established helpers. The data bytes are copied
// byte-for-byte (I13 exception does not apply here — these are opaque blobs).
func CopyOpaque(src *Bank, dst *Builder) {
	if src == nil || dst == nil {
		return
	}
	knownAccounts := map[string]bool{
		SummaryAccount:   true,
		CameraAccount:    true,
		PlayersAccount:   true,
		NanolatheAccount: true,
	}
	// Player%i accounts are known-establish; skip them as well.
	for i := 0; i < 10; i++ {
		knownAccounts[playerAccountName(i)] = true
	}
	for _, ac := range src.Accounts() {
		if knownAccounts[ac.Name] {
			// Still copy unknown boxes inside known accounts? For Players we
			// already handle GameTime/Alliances; other boxes there are T25 opaque
			// but spec says unknown retail boxes inside known accounts must also
			// round-trip. To keep the contract "unknown accounts/boxes byte-for-byte"
			// we copy boxes whose names are not established.
			//
			// Established boxes: GameTime, Alliances, Radar Image, StateV1.
			// Everything else is opaque [GAP T25].
			knownBoxes := map[string]bool{
				GameTimeBoxName:   true,
				AlliancesBoxName:  true,
				RadarImageBoxName: true,
				StateV1BoxName:    true,
			}
			// For Summary, Radar Image is the only established box; others are opaque.
			// For simplicity, copy any box not in knownBoxes byte-for-byte.
			for _, box := range ac.Boxes {
				if knownBoxes[box.Name] {
					continue
				}
				if ac.Name == NanolatheAccount && box.Name == StateV1BoxName {
					continue
				}
				nac := builderAccount(dst, ac.Name)
				// Preserve number for numbered boxes (box.Name=="" case) [08 "Location and representation"].
				nac.AppendBox(box.Name, box.Number, box.Data)
			}
			// Also preserve unknown integer/double/string items inside known accounts?
			// Those are T25 opaque scalar items; we copy them by re-adding with same name/value.
			// However to avoid duplicating established scalars that were already written,
			// we would need to know which scalars are established; for now we copy
			// all scalars from unknown accounts only. Known-account scalars are
			// handled explicitly by Write* helpers, so we skip copying to avoid
			// overwriting canonical values. The opaque scalar path for known accounts
			// is TODO(T25): no established scalar beyond the tables has an open
			// writer, so copying them would be speculative.
			continue
		}
		// Unknown account — copy all items and boxes verbatim [GAP T25] C18 opaque.
		nac := builderAccount(dst, ac.Name)
		for _, it := range ac.Ints {
			nac.SetInt(it.Name, it.Value)
		}
		for _, it := range ac.Doubles {
			nac.SetDouble(it.Name, it.Value)
		}
		for _, it := range ac.Strings {
			nac.SetString(it.Name, it.Value)
		}
		for _, box := range ac.Boxes {
			nac.AppendBox(box.Name, box.Number, box.Data)
		}
	}
	// TODO(T25): retail bulk-box byte layouts beyond established lengths remain
	// TODO(T25) opaque. Unknown retail bulk boxes beyond sampled maps (unit 184-byte
	// records, order 58-byte, feature names 128-byte etc) are preserved as opaque
	// bytes and not decoded [GAP T25].
}

// BuilderFromBank creates a new Builder seeded with the retail tag and copies
// all accounts/boxes from src via CopyOpaque and by re-serializing known
// accounts through the established helpers if the caller has already extracted
// them. This helper is used by the opaque pass-through test to verify
// byte-equality of unknown payloads after a decode→encode cycle.
func BuilderFromBank(src *Bank) *Builder {
	b := NewBuilder(RetailTag)
	if src == nil {
		return b
	}
	// Copy unknown accounts/boxes byte-for-byte [GAP T25].
	CopyOpaque(src, b)
	return b
}

// ---------------------------------------------------------------------------
// StateV1 — versioned Nanolathe continuation box (C18) [GAP T25].
// ---------------------------------------------------------------------------

var (
	// ErrNoStateV1 is returned when a retail save without StateV1 is asked
	// for full battle continuation. Metadata boxes remain readable; the caller
	// should surface an explicit unsupported diagnostic rather than fabricating
	// missing state [PLAN_14 C18].
	ErrNoStateV1 = errors.New("save: retail save without StateV1 — full continuation unsupported; metadata only [GAP T25] [PLAN_14 C18]")

	// ErrStateV1Version is returned for an unknown StateV1 version.
	ErrStateV1Version = errors.New("save: unsupported StateV1 version")

	// ErrCatalogMismatch and ErrManifestMismatch are returned when the saved
	// catalog/manifest hashes do not match the current content, to reject wrong
	// content [PLAN_14 C18].
	ErrCatalogMismatch  = errors.New("save: catalog hash mismatch — refusing wrong content [PLAN_14 C18]")
	ErrManifestMismatch = errors.New("save: manifest hash mismatch — refusing wrong content [PLAN_14 C18]")
)

// StateV1Version is the version of the Nanolathe StateV1 box [PLAN_14 C18].
// Increment when the codec changes; decoder rejects unknown versions.
// Version 2 adds full continuation per P0-I11 [08 "Save"] [01 §6] [05][04].
// Version 3 adds COB piece transforms, anims, and flags per P1-I01 [04 §4.2][04 §4.6][04 §4.3].
// Version 4 adds ground steering state per [04 §8.1] C20 C21 [ON-12].
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Version 6 adds construction builder-product links [05 "Factory production lifecycle"] C18 [RS-10].
// Version 7 adds the per-unit control-group value [07 §9].
// Version 8 adds the four settled AI economy aggregates consumed by the
// strategic score [R-P0-05]. Older states decode these fields as zero.
// Version 9 adds all nine manager tactical vectors in recovered retail slot
// order [R-P0-04]. Older v8 states decode the legacy six vectors and default
// the three newly recovered vectors to empty.
// The next version adds script-owned INBUILDSTANCE and any continuation
// fields whose prior version has already been allocated.

const StateV1VersionConst uint32 = 9
const StateV1Version1 uint32 = 1
const StateV1Version2 uint32 = 2
const StateV1Version3 uint32 = 3
const StateV1Version4 uint32 = 4
const StateV1Version5 uint32 = 5
const StateV1Version6 uint32 = 6
const StateV1Version7 uint32 = 7
const StateV1Version8 uint32 = 8
const StateV1Version9 uint32 = 9

// UnitRecord is one slot-indexed unit record, canonically ordered by slot
// ascending for determinism (I1) [01 §6.1] [PLAN_14 C18]. Reconstruction uses
// only published package APIs (e.g., units.World.CreateWithForcedSlot via catalog lookup) —
// no direct memory image [P0-I11]. Slots are forced identity, not lowest-free [01 §6.1].
type UnitRecord struct {
	Slot          int32
	DefName       string
	Owner         uint8
	X, Y, Z       int32 // 16.16 fixed raw (numeric.Fixed bits) — int32 suffices for test fixtures
	Health        int32
	Remaining     float32 // construction remaining 1→0 [05 "Construction target state"] [I2]
	Flags         uint32
	Group         uint8 // one stored control-group value 0..9 [07 §9]
	InBuildStance bool  // COB INBUILDSTANCE port 5, separate from Flags [04 §4.4][R-P0-10]
	// Extended per-unit mutable state for full continuation [P0-I11][01 §6][04][05][06].
	MaxHealth         int32  // [04 §2.3]
	Dying             bool   // death mark before Cleanup [04 §2.4]
	DeathCause        uint8  // [04 §2.4]
	Pending           uint32 // capability word [04 §3.3]
	Kills             int32  // +0xB8 [P0-15]
	ParalyzeExpire    uint32 // [06 §10]
	Stunned           bool   // [06 §10]
	SpotMetal         float32
	PlacementIdx      int32
	PlacementIdent    string
	PlacementUnitName string
	MoveMode          uint8  // [04 §8.1] 0 none,1 parked,2 active
	MoveHeading       uint16 // 0..65535 [04 §5.1]
	MoveSpeed         int32  // 16.16 fixed raw
	// System-level steer state [04 §8.1][RX-07 G8]: the movement integrator's
	// per-unit scratch block; without it restored runs diverge on the first
	// direct-goal or turn-in tick.
	MovePendingHeading uint16
	MoveDirty          bool
	MoveHeightWord     int16
	MoveSeaLevel       uint8
	MoveDefFlags       uint32
	MoveMaxVelocity    int32
	MoveTurnRate       int32
	Carrier            int32 // pool.Handle 0 null [04 §4.4]
	Cargo              []int32
	Slots              [3]SlotRecord // three weapon slots [06 §1.2]
	HasCOB             bool
	COB                COBRecord // per-unit COB VM [04 §4.2][GAP T15]
}

// SlotRecord is one weapon slot per unit [06 §1.2] C1 [P0-I11].
type SlotRecord struct {
	WeaponName   string // canonical weapon key, empty if none
	Reload       int32
	Flags        uint8
	DesiredYaw   uint16
	DesiredPitch uint16
	Ammo         int32
	MuzzlePiece  int32
	AimIssue     bool
	AimReady     bool
	TargetKind   uint8 // 0 none,1 unit,2 ground [06 §1.2]
	TargetUnit   int32
	TargetX      int32
	TargetZ      int32
}

// COBRecord captures per-unit COB VM threads/stacks/statics [04 §4.2][GAP T15][P0-I11][P1-I01].
// Version 3 adds piece transforms, anims, and flags [04 §4.6][04 §4.3] P1-I01.
type COBRecord struct {
	Statics []int32
	Threads [8]ThreadRecord
	Pieces  []PieceStateSave // per-piece transforms [03 §2.4] P1-I01
	Flags   []uint8          // per-piece flags [04 §4.3] P1-I01
	Anims   []PieceAnimSave  // per-piece anim lanes [04 §4.6] P1-I01
}

// ThreadRecord captures one COB thread [04 §4.2] 8*164 identity (I13) [P0-I11].
type ThreadRecord struct {
	Status     int32
	PC         int32
	SP         int32
	Sleep      int32
	WaitPiece  int32
	WaitAxis   int32
	WaitThread int32
	SignalMask int32
	Stack      []int32 // 0..10 depth [04 §4.2]
}

// PieceStateSave captures one piece transform [03 §2.4] C21 P1-I01.
type PieceStateSave struct {
	RotX, RotY, RotZ       uint16
	TransX, TransY, TransZ int32 // Fixed raw 16.16
}

// PieceAnimSave captures one piece's per-axis anim lanes [04 §4.6] P1-I01.
type PieceAnimSave struct {
	Axes [3]AxisAnimSave
}

// AxisAnimSave captures one axis lane [04 §4.6] P1-I01.
type AxisAnimSave struct {
	MoveTarget int32
	MoveSpeed  int32
	MoveBusy   bool
	TurnTarget uint16
	TurnSpeed  int32
	TurnBusy   bool
	SpinSpeed  int32
	SpinTarget int32
	SpinAccel  int32
	SpinActive bool
}

// QueueRecord is a per-unit queue payload stub; real engine would include the
// full 86-byte order nodes [01 §6.1] [05 "Factory queue"]; this fixture captures
// the canonical slot-indexed queue payload principle [PLAN_14 C18].
// Retained for v1 compatibility; v2 uses Orders for full nodes [P0-I11].
type QueueRecord struct {
	UnitSlot int32
	Kind     string // e.g., "Build", "Move"
	Count    int32
	Payload  []byte // opaque queue bytes, slot-indexed
}

// OrderRecord is a full 86-byte order node identity [04 §3.2][P0-I11][P0-I03] plus
// queue segment, canonically ordered for determinism (I1).
type OrderRecord struct {
	UnitSlot     int32
	Segment      uint8  // 0 primary, 1 secondary [04 §3.2]
	Index        int32  // position within segment
	Descriptor   string // canonical descriptor name [04 §3.2]
	Phase        uint8
	DynamicGate  uint32
	Deadline     int32
	Owner        int32
	Target       int32
	GoalX        int32
	GoalY        int32
	GoalZ        int32
	GuardX       int16
	GuardY       int16
	CachedX      int16
	CachedY      int16
	Param1       uint32
	Param2       uint32
	Param3       uint32
	StaticGate   uint32
	CreationTick uint32
	Satisfied    uint32
	Flags        uint32
	MoveState    uint8
	PathStatus   uint32
	BuildDefKey  string
}

// MovementRouteRecord captures a published route per [04 §7.3] C14–C16 [P0-I11].
type MovementRouteRecord struct {
	Unit   int32
	Count  uint8
	Active bool
	Dirty  bool
	Points [20]PointRecord
}

// PointRecord holds route lattice point [04 §7.3].
type PointRecord struct {
	X int32
	Z int32
}

// SchedulerPendingRecord captures a pending path request [04 §7.3] C11 C12 [P0-I11].
type SchedulerPendingRecord struct {
	Unit       int32
	Player     uint8
	StartX     int32
	StartZ     int32
	GoalX      int32
	GoalZ      int32
	GoalRadius int32 // for point goal; 0 for exact [04 §7.2]
	GoalKind   uint8 // 0 point,1 annulus,2 rect,3 saved (only 0 used for now) [04 §7.2]
}

// MovementSteerRecord captures per-unit ground steering state [04 §8.1] C20 C21 [ON-12].
type MovementSteerRecord struct {
	Handle         int32
	X, Z           int32
	Heading        uint16
	PendingHeading uint16
	Dirty          bool
	Speed          int32
	MaxVelocity    int32
	TurnRate       int32
	HeightWord     int16
	SeaLevel       uint8
	DefFlags       uint32
}

// EconomySnapshot captures per-player ledger plus unit buckets [05][P0-I11].
type EconomySnapshot struct {
	Players     [10]EconomyPlayerRecord
	UnitBuckets []EconomyUnitBucketRecord // sparse, by handle
}

// EconomyPlayerRecord mirrors economy.Player fields that affect settlement [05][P0-I11].
type EconomyPlayerRecord struct {
	Exists              bool
	ControllerState     uint8
	IsObserver          bool
	StockMetal          float32
	StockEnergy         float32
	CapacityMetal       float32
	CapacityEnergy      float32
	MirrorMetal         BucketRecord
	MirrorEnergy        BucketRecord
	UpdateTime          uint32
	WinLoseTime         uint32
	DisplayTimer        uint32
	WasteMetal          float64
	WasteEnergy         float64
	TotalProducedMetal  float64
	TotalProducedEnergy float64
	TotalConsumedMetal  float64
	TotalConsumedEnergy float64
	PassProducedMetal   float32
	PassProducedEnergy  float32
	PassConsumedMetal   float32
	PassConsumedEnergy  float32
	ArchivedMetal       BucketRecord
	ArchivedEnergy      BucketRecord
	StatusHalfwordAt144 int16
	StatusWordAt140     int32
	GameEnded           bool
	EndGameCountdown    int32
	Helper1Deadline     uint32
	Helper2Deadline     uint32
	Helper1Calls        int32
	Helper2Calls        int32
	WeaponRefreshCalls  int32
	ReferencePlayer     int32
	SensorShareCalls    int32
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// Retail identity +0x149 bit0 enable, +0x37/+0x38 ints max(value,200) via FILD/FSTP at +0xDC/+0xE0.
	StorageBonusEnabled bool    // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	StorageBonusMetal   float32 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	StorageBonusEnergy  float32 // +0x37/+0xDC max(startEnergy,200)
	// Settled per-player aggregates consumed by the strategic AI score. These
	// are distinct from PassProduced/PassConsumed and include unit buckets plus
	// the player mirror [R-P0-05]. The wire form is raw float32 bits so NaN and
	// signed-zero payloads survive a round trip exactly.
	AIProductionMetal   float32
	AIProductionEnergy  float32
	AIConsumptionMetal  float32
	AIConsumptionEnergy float32
}

// BucketRecord mirrors economy.Bucket [05].
type BucketRecord struct {
	Production float32
	Requested  float32
	Accepted   float32
	Carry      float32
}

// EconomyUnitBucketRecord mirrors per-unit economy buckets [05][P0-I11].
type EconomyUnitBucketRecord struct {
	Handle   int32
	Buckets  [2]BucketRecord
	Archived [2]BucketRecord
}

// FeaturesSnapshot captures feature pool [05][06 §13.1][P0-I11].
type FeaturesSnapshot struct {
	Cursor       int32
	LastReproIdx int32
	Instances    []FeatureInstanceRecord
}

// FeatureInstanceRecord mirrors features.Instance [05][P0-I11].
type FeatureInstanceRecord struct {
	DefName         string
	CX              int32
	CZ              int32
	Health          int32
	MaxHealth       int32
	ReclaimProgress int32
	IsBurning       bool
	BurnCountdown   int32
	BurnTicks       int32
	BurnDuration    int32
	IsSinking       bool
	Y               int32
	Vy              int32
	Settled         bool
	Status          uint8
	X               int32
	Z               int32
	FootX           int32
	FootZ           int32
}

// ProjectileRecord mirrors combat.Projectile [06 §5.1][P0-I11].
type ProjectileRecord struct {
	Handle                             int32
	WeaponID                           int32
	PosX, PosY, PosZ                   int32
	StartPosX, StartPosY, StartPosZ    int32
	TargetPosX, TargetPosY, TargetPosZ int32
	TargetUnit                         int32
	TargetProjectile                   int32
	VelocityX, VelocityY, VelocityZ    int32
	Speed                              int32
	Yaw                                uint16
	Pitch                              uint16
	Shooter                            int32
	ShooterSide                        uint8
	MuzzlePiece                        int16
	CreationTick                       uint32
	BurstDeadline                      uint32
	BurstRemaining                     int32
	ExpiryTick                         uint32
	SmokeDeadline                      uint32
	BeamLatch                          bool
	TwoPhase                           bool
	Dead                               bool
	PropellerYaw                       uint16
	MeteorPitch                        uint16
	CacheCellX                         int32
	CacheCellZ                         int32
	Scratch5E                          int16
	State69                            uint8
	OldMarker                          int16
}

// AIManagerRecord mirrors ai.Manager [08][P0-I11][PLAN_11].
type AIManagerRecord struct {
	Player       uint8
	Deadlines    [12]uint32 // TaskKindCount is 12 [P0-02]
	Strategic    StrategicSnapshot
	Groups       AIGroupsSnapshot
	SurfaceMetal int32
	OriginX      int32
	OriginZ      int32
}

// StrategicSnapshot mirrors ai.Strategic [08][P0-I11].
type StrategicSnapshot struct {
	CenterX                int32
	CenterZ                int32
	Radius                 int32
	LastRefreshTick        uint32
	LastClassRecomputeTick uint32
	Counts                 []StrategicCountRecord
	ClassVectors           []StrategicClassVectorRecord
	InitVectors            []StrategicInitVectorRecord
	SingleVectors          []StrategicSingleVectorRecord
}

type StrategicCountRecord struct {
	Key   string
	Value int32
}
type StrategicClassVectorRecord struct {
	Key string
	C0  int8
	C1  int8
	C2  int8
}
type StrategicInitVectorRecord struct {
	Key   string
	Value int8
}
type StrategicSingleVectorRecord struct {
	Key   string
	Value int8
}

// AIGroupsSnapshot mirrors Manager groups
type AIGroupsSnapshot struct {
	// Fields follow the recovered manager record order (+0x20 through
	// +0x120), not presentation or task-dispatch order [R-P0-04].
	Resource     []int32
	WaveA        []int32
	RegroupA     []int32
	Construction []int32
	Null         []int32
	WaveB        []int32
	RegroupB     []int32
	Explore      []int32
	Rally        []int32
}

// VisibilitySnapshot mirrors visibility grids [03 §3][P0-I11].
type VisibilitySnapshot struct {
	W         int32
	H         int32
	WordMask  []uint16
	ByteGrids [10][]uint8
	Local     uint8
	Mode      uint32
	Status    []VisibilityStatusRecord // per-unit sensor status
	Decloak   []VisibilityDecloakRecord
}

// VisibilityStatusRecord mirrors session visStatus map [03 §3.4][P0-I11].
type VisibilityStatusRecord struct {
	Handle int32
	Status uint32
}
type VisibilityDecloakRecord struct {
	Handle   int32
	Deadline uint32
}

// LatchSnapshot mirrors session.EndLatch [P1-01][P0-I11].
type LatchSnapshot struct {
	Countdown int16
	Bits      uint16
	Pending   uint8
}

// WindSnapshot mirrors world.Wind [01 §7.3][P0-I11].
type WindSnapshot struct {
	Min        int32
	Max        int32
	Strength   int32
	Heading    uint16
	Scalar     float32
	DirX       int32
	DirZ       int32
	NextChange uint32
	LastChange uint32
	Changed    bool
	Pending    bool
}

// ConstructionSnapshot mirrors construction.Service builder-product links [05 "Factory production lifecycle"] C18 [RS-10].
type ConstructionSnapshot struct {
	BuilderLinks []BuilderLinkRecord // product -> builder, sorted by Product ascending (I1) [RS-10]
}

// BuilderLinkRecord is one builder-product link, product handle owns builder handle [05 C18][RS-10].
type BuilderLinkRecord struct {
	Builder int32 // builder handle (pool.Handle)
	Product int32 // product handle (pool.Handle)
}

// StateV1 is the versioned native continuation box that canonically encodes
// every mutable authoritative service, slot-indexed, plus both RNG states+draw counts and hash guards [PLAN_14 C18] [GAP T25] [P0-I11].
// Version 2 adds full continuation via forced slot identity and full per-system snapshots [P0-I11].
type StateV1 struct {
	Version      uint32 // supported versions 1..9; current writer emits 9 [PLAN_14 C18][P0-I11]
	CatalogHash  string // [02 §5] C12 catalog hash
	ManifestHash string // vfs.ManifestHash

	SimState uint32
	SimDraws uint64
	CrtState uint32
	CrtDraws uint64

	Clock clock.State // 28-byte scheduler block [08 "Scheduler and random state in saves"] [01 §7.3]

	Units  []UnitRecord  // sorted by Slot ascending (I1) [01 §6.1]
	Queues []QueueRecord // sorted by UnitSlot ascending (I1) — v1 compat retained [P0-I11]

	// P0-I11 full continuation fields (v2 only)
	Orders           []OrderRecord            // complete order nodes both segments [04 §3.2][P0-I11][P0-I03]
	MovementRoutes   []MovementRouteRecord    // active routes [04 §7.3][P0-I11]
	SchedulerPending []SchedulerPendingRecord // pending path requests [04 §7.3][P0-I11]
	MovementSteers   []MovementSteerRecord    // per-unit steering [04 §8.1] C20 C21 [ON-12]
	Economy          EconomySnapshot          // buckets and deadlines [05][P0-I11]
	Features         FeaturesSnapshot         // free lists, burning, sinking [05][P0-I11]
	Projectiles      []ProjectileRecord       // projectile pool for native continuation [06 §5.1][P0-I11]
	AI               []AIManagerRecord        // managers task deadlines strategic groups [08][P0-I11]
	Visibility       VisibilitySnapshot       // mapping/sensor state [03 §3][P0-I11]
	Latch            LatchSnapshot            // triggers/end latch/campaign progress [P1-01][P0-I11]
	Wind             WindSnapshot             // wind/meteor state [01 §7.3][P0-I11]
	Construction     ConstructionSnapshot     // builder-product links [05 C18][RS-10]
}

// MarshalStateV1 encodes s into the canonical StateV1 byte layout (I13 exception:
// bytes cross the save boundary, so layout is the contract) [PLAN_14 C18][P0-I11].
func MarshalStateV1(s *StateV1) []byte {
	if s == nil {
		return nil
	}
	// Ensure canonical ordering (I1) [01 §6.1] [PLAN_14 C18][P0-I11].
	units := append([]UnitRecord(nil), s.Units...)
	sort.Slice(units, func(i, j int) bool { return units[i].Slot < units[j].Slot })
	queues := append([]QueueRecord(nil), s.Queues...)
	sort.Slice(queues, func(i, j int) bool {
		if queues[i].UnitSlot != queues[j].UnitSlot {
			return queues[i].UnitSlot < queues[j].UnitSlot
		}
		return queues[i].Kind < queues[j].Kind
	})
	orders := append([]OrderRecord(nil), s.Orders...)
	sort.Slice(orders, func(i, j int) bool {
		if orders[i].UnitSlot != orders[j].UnitSlot {
			return orders[i].UnitSlot < orders[j].UnitSlot
		}
		if orders[i].Segment != orders[j].Segment {
			return orders[i].Segment < orders[j].Segment
		}
		return orders[i].Index < orders[j].Index
	})
	routes := append([]MovementRouteRecord(nil), s.MovementRoutes...)
	sort.Slice(routes, func(i, j int) bool { return routes[i].Unit < routes[j].Unit })
	pending := append([]SchedulerPendingRecord(nil), s.SchedulerPending...)
	sort.Slice(pending, func(i, j int) bool { return pending[i].Unit < pending[j].Unit })
	steers := append([]MovementSteerRecord(nil), s.MovementSteers...)
	sort.Slice(steers, func(i, j int) bool { return steers[i].Handle < steers[j].Handle })
	proj := append([]ProjectileRecord(nil), s.Projectiles...)
	sort.Slice(proj, func(i, j int) bool { return proj[i].Handle < proj[j].Handle })
	ai := append([]AIManagerRecord(nil), s.AI...)
	sort.Slice(ai, func(i, j int) bool { return ai[i].Player < ai[j].Player })
	links := append([]BuilderLinkRecord(nil), s.Construction.BuilderLinks...)
	sort.Slice(links, func(i, j int) bool {
		if links[i].Product != links[j].Product {
			return links[i].Product < links[j].Product
		}
		return links[i].Builder < links[j].Builder
	})
	var buf bytes.Buffer
	ver := s.Version
	if ver == 0 {
		ver = StateV1VersionConst
	}
	binaryWriteUint32(&buf, ver)
	writeString(&buf, s.CatalogHash)
	writeString(&buf, s.ManifestHash)
	binaryWriteUint32(&buf, s.SimState)
	binaryWriteUint64(&buf, s.SimDraws)
	binaryWriteUint32(&buf, s.CrtState)
	binaryWriteUint64(&buf, s.CrtDraws)
	clockBox := s.Clock.SaveBox()
	buf.Write(clockBox[:])
	// Units [01 §6.1] slot-indexed. v1: 6 fields, v2: extended [P0-I11].
	binaryWriteUint32(&buf, uint32(len(units)))
	for _, u := range units {
		binaryWriteInt32(&buf, u.Slot)
		writeString(&buf, u.DefName)
		buf.WriteByte(u.Owner)
		binaryWriteInt32(&buf, u.X)
		binaryWriteInt32(&buf, u.Y)
		binaryWriteInt32(&buf, u.Z)
		binaryWriteInt32(&buf, u.Health)
		binaryWriteUint32(&buf, math.Float32bits(u.Remaining))
		binaryWriteUint32(&buf, u.Flags)
		if ver >= 7 {
			buf.WriteByte(u.Group)
		}
		if ver >= 9 {
			buf.WriteByte(boolToByte(u.InBuildStance))
		}
		if ver >= 2 {
			binaryWriteInt32(&buf, u.MaxHealth)
			buf.WriteByte(boolToByte(u.Dying))
			buf.WriteByte(u.DeathCause)
			binaryWriteUint32(&buf, u.Pending)
			binaryWriteInt32(&buf, u.Kills)
			binaryWriteUint32(&buf, u.ParalyzeExpire)
			buf.WriteByte(boolToByte(u.Stunned))
			binaryWriteUint32(&buf, math.Float32bits(u.SpotMetal))
			binaryWriteInt32(&buf, u.PlacementIdx)
			writeString(&buf, u.PlacementIdent)
			writeString(&buf, u.PlacementUnitName)
			buf.WriteByte(u.MoveMode)
			binaryWriteUint16(&buf, u.MoveHeading)
			binaryWriteInt32(&buf, u.MoveSpeed)
			binaryWriteUint16(&buf, u.MovePendingHeading)
			buf.WriteByte(boolToByte(u.MoveDirty))
			binaryWriteInt32(&buf, int32(u.MoveHeightWord))
			buf.WriteByte(u.MoveSeaLevel)
			binaryWriteUint32(&buf, u.MoveDefFlags)
			binaryWriteInt32(&buf, u.MoveMaxVelocity)
			binaryWriteInt32(&buf, u.MoveTurnRate)
			binaryWriteInt32(&buf, u.Carrier)
			binaryWriteUint32(&buf, uint32(len(u.Cargo)))
			for _, h := range u.Cargo {
				binaryWriteInt32(&buf, h)
			}
			for k := 0; k < 3; k++ {
				sr := u.Slots[k]
				writeString(&buf, sr.WeaponName)
				binaryWriteInt32(&buf, sr.Reload)
				buf.WriteByte(sr.Flags)
				binaryWriteUint16(&buf, sr.DesiredYaw)
				binaryWriteUint16(&buf, sr.DesiredPitch)
				binaryWriteInt32(&buf, sr.Ammo)
				binaryWriteInt32(&buf, sr.MuzzlePiece)
				buf.WriteByte(boolToByte(sr.AimIssue))
				buf.WriteByte(boolToByte(sr.AimReady))
				buf.WriteByte(sr.TargetKind)
				binaryWriteInt32(&buf, sr.TargetUnit)
				binaryWriteInt32(&buf, sr.TargetX)
				binaryWriteInt32(&buf, sr.TargetZ)
			}
			buf.WriteByte(boolToByte(u.HasCOB))
			if u.HasCOB {
				binaryWriteUint32(&buf, uint32(len(u.COB.Statics)))
				for _, v := range u.COB.Statics {
					binaryWriteInt32(&buf, v)
				}
				for t := 0; t < 8; t++ {
					tr := u.COB.Threads[t]
					binaryWriteInt32(&buf, tr.Status)
					binaryWriteInt32(&buf, tr.PC)
					binaryWriteInt32(&buf, tr.SP)
					binaryWriteInt32(&buf, tr.Sleep)
					binaryWriteInt32(&buf, tr.WaitPiece)
					binaryWriteInt32(&buf, tr.WaitAxis)
					binaryWriteInt32(&buf, tr.WaitThread)
					binaryWriteInt32(&buf, tr.SignalMask)
					binaryWriteUint32(&buf, uint32(len(tr.Stack)))
					for _, v := range tr.Stack {
						binaryWriteInt32(&buf, v)
					}
				}
				if ver >= 3 {
					// Pieces [03 §2.4] P1-I01
					binaryWriteUint32(&buf, uint32(len(u.COB.Pieces)))
					for _, p := range u.COB.Pieces {
						binaryWriteUint16(&buf, p.RotX)
						binaryWriteUint16(&buf, p.RotY)
						binaryWriteUint16(&buf, p.RotZ)
						binaryWriteInt32(&buf, p.TransX)
						binaryWriteInt32(&buf, p.TransY)
						binaryWriteInt32(&buf, p.TransZ)
					}
					binaryWriteUint32(&buf, uint32(len(u.COB.Flags)))
					for _, f := range u.COB.Flags {
						buf.WriteByte(f)
					}
					binaryWriteUint32(&buf, uint32(len(u.COB.Anims)))
					for _, a := range u.COB.Anims {
						for ax := 0; ax < 3; ax++ {
							aa := a.Axes[ax]
							binaryWriteInt32(&buf, aa.MoveTarget)
							binaryWriteInt32(&buf, aa.MoveSpeed)
							buf.WriteByte(boolToByte(aa.MoveBusy))
							binaryWriteUint16(&buf, aa.TurnTarget)
							binaryWriteInt32(&buf, aa.TurnSpeed)
							buf.WriteByte(boolToByte(aa.TurnBusy))
							binaryWriteInt32(&buf, aa.SpinSpeed)
							binaryWriteInt32(&buf, aa.SpinTarget)
							binaryWriteInt32(&buf, aa.SpinAccel)
							buf.WriteByte(boolToByte(aa.SpinActive))
						}
					}
				}
			}
		}
	}
	binaryWriteUint32(&buf, uint32(len(queues)))
	for _, q := range queues {
		binaryWriteInt32(&buf, q.UnitSlot)
		writeString(&buf, q.Kind)
		binaryWriteInt32(&buf, q.Count)
		binaryWriteUint32(&buf, uint32(len(q.Payload)))
		buf.Write(q.Payload)
	}
	if ver >= 2 {
		// Orders
		binaryWriteUint32(&buf, uint32(len(orders)))
		for _, o := range orders {
			binaryWriteInt32(&buf, o.UnitSlot)
			buf.WriteByte(o.Segment)
			binaryWriteInt32(&buf, o.Index)
			writeString(&buf, o.Descriptor)
			buf.WriteByte(o.Phase)
			binaryWriteUint32(&buf, o.DynamicGate)
			binaryWriteInt32(&buf, o.Deadline)
			binaryWriteInt32(&buf, o.Owner)
			binaryWriteInt32(&buf, o.Target)
			binaryWriteInt32(&buf, o.GoalX)
			binaryWriteInt32(&buf, o.GoalY)
			binaryWriteInt32(&buf, o.GoalZ)
			binaryWriteInt16(&buf, o.GuardX)
			binaryWriteInt16(&buf, o.GuardY)
			binaryWriteInt16(&buf, o.CachedX)
			binaryWriteInt16(&buf, o.CachedY)
			binaryWriteUint32(&buf, o.Param1)
			binaryWriteUint32(&buf, o.Param2)
			binaryWriteUint32(&buf, o.Param3)
			binaryWriteUint32(&buf, o.StaticGate)
			binaryWriteUint32(&buf, o.CreationTick)
			binaryWriteUint32(&buf, o.Satisfied)
			binaryWriteUint32(&buf, o.Flags)
			buf.WriteByte(o.MoveState)
			binaryWriteUint32(&buf, o.PathStatus)
			writeString(&buf, o.BuildDefKey)
		}
		// MovementRoutes
		binaryWriteUint32(&buf, uint32(len(routes)))
		for _, r := range routes {
			binaryWriteInt32(&buf, r.Unit)
			buf.WriteByte(r.Count)
			buf.WriteByte(boolToByte(r.Active))
			buf.WriteByte(boolToByte(r.Dirty))
			for i := 0; i < 20; i++ {
				binaryWriteInt32(&buf, r.Points[i].X)
				binaryWriteInt32(&buf, r.Points[i].Z)
			}
		}
		// SchedulerPending
		binaryWriteUint32(&buf, uint32(len(pending)))
		for _, p := range pending {
			binaryWriteInt32(&buf, p.Unit)
			buf.WriteByte(p.Player)
			binaryWriteInt32(&buf, p.StartX)
			binaryWriteInt32(&buf, p.StartZ)
			binaryWriteInt32(&buf, p.GoalX)
			binaryWriteInt32(&buf, p.GoalZ)
			binaryWriteInt32(&buf, p.GoalRadius)
			buf.WriteByte(p.GoalKind)
		}
		if ver >= 4 {
			// MovementSteers [04 §8.1] C20 C21 [ON-12]
			binaryWriteUint32(&buf, uint32(len(steers)))
			for _, st := range steers {
				binaryWriteInt32(&buf, st.Handle)
				binaryWriteInt32(&buf, st.X)
				binaryWriteInt32(&buf, st.Z)
				binaryWriteUint16(&buf, st.Heading)
				binaryWriteUint16(&buf, st.PendingHeading)
				buf.WriteByte(boolToByte(st.Dirty))
				binaryWriteInt32(&buf, st.Speed)
				binaryWriteInt32(&buf, st.MaxVelocity)
				binaryWriteInt32(&buf, st.TurnRate)
				binaryWriteInt16(&buf, st.HeightWord)
				buf.WriteByte(st.SeaLevel)
				binaryWriteUint32(&buf, st.DefFlags)
			}
		}
		// Economy
		for i := 0; i < 10; i++ {
			pl := s.Economy.Players[i]
			buf.WriteByte(boolToByte(pl.Exists))
			buf.WriteByte(pl.ControllerState)
			buf.WriteByte(boolToByte(pl.IsObserver))
			binaryWriteUint32(&buf, math.Float32bits(pl.StockMetal))
			binaryWriteUint32(&buf, math.Float32bits(pl.StockEnergy))
			binaryWriteUint32(&buf, math.Float32bits(pl.CapacityMetal))
			binaryWriteUint32(&buf, math.Float32bits(pl.CapacityEnergy))
			binaryWriteUint32(&buf, math.Float32bits(pl.MirrorMetal.Production))
			binaryWriteUint32(&buf, math.Float32bits(pl.MirrorMetal.Requested))
			binaryWriteUint32(&buf, math.Float32bits(pl.MirrorMetal.Accepted))
			binaryWriteUint32(&buf, math.Float32bits(pl.MirrorMetal.Carry))
			binaryWriteUint32(&buf, math.Float32bits(pl.MirrorEnergy.Production))
			binaryWriteUint32(&buf, math.Float32bits(pl.MirrorEnergy.Requested))
			binaryWriteUint32(&buf, math.Float32bits(pl.MirrorEnergy.Accepted))
			binaryWriteUint32(&buf, math.Float32bits(pl.MirrorEnergy.Carry))
			binaryWriteUint32(&buf, pl.UpdateTime)
			binaryWriteUint32(&buf, pl.WinLoseTime)
			binaryWriteUint32(&buf, pl.DisplayTimer)
			binaryWriteUint64(&buf, math.Float64bits(pl.WasteMetal))
			binaryWriteUint64(&buf, math.Float64bits(pl.WasteEnergy))
			binaryWriteUint64(&buf, math.Float64bits(pl.TotalProducedMetal))
			binaryWriteUint64(&buf, math.Float64bits(pl.TotalProducedEnergy))
			binaryWriteUint64(&buf, math.Float64bits(pl.TotalConsumedMetal))
			binaryWriteUint64(&buf, math.Float64bits(pl.TotalConsumedEnergy))
			binaryWriteUint32(&buf, math.Float32bits(pl.PassProducedMetal))
			binaryWriteUint32(&buf, math.Float32bits(pl.PassProducedEnergy))
			binaryWriteUint32(&buf, math.Float32bits(pl.PassConsumedMetal))
			binaryWriteUint32(&buf, math.Float32bits(pl.PassConsumedEnergy))
			binaryWriteUint32(&buf, math.Float32bits(pl.ArchivedMetal.Production))
			binaryWriteUint32(&buf, math.Float32bits(pl.ArchivedMetal.Requested))
			binaryWriteUint32(&buf, math.Float32bits(pl.ArchivedMetal.Accepted))
			binaryWriteUint32(&buf, math.Float32bits(pl.ArchivedMetal.Carry))
			binaryWriteUint32(&buf, math.Float32bits(pl.ArchivedEnergy.Production))
			binaryWriteUint32(&buf, math.Float32bits(pl.ArchivedEnergy.Requested))
			binaryWriteUint32(&buf, math.Float32bits(pl.ArchivedEnergy.Accepted))
			binaryWriteUint32(&buf, math.Float32bits(pl.ArchivedEnergy.Carry))
			binaryWriteInt16(&buf, pl.StatusHalfwordAt144)
			binaryWriteInt32(&buf, pl.StatusWordAt140)
			buf.WriteByte(boolToByte(pl.GameEnded))
			binaryWriteInt32(&buf, pl.EndGameCountdown)
			binaryWriteUint32(&buf, pl.Helper1Deadline)
			binaryWriteUint32(&buf, pl.Helper2Deadline)
			if ver >= 4 {
				binaryWriteInt32(&buf, pl.Helper1Calls)
				binaryWriteInt32(&buf, pl.Helper2Calls)
				binaryWriteInt32(&buf, pl.WeaponRefreshCalls)
			}
			binaryWriteInt32(&buf, pl.ReferencePlayer)
			binaryWriteInt32(&buf, pl.SensorShareCalls)
			if ver >= 5 {
				buf.WriteByte(boolToByte(pl.StorageBonusEnabled))
				binaryWriteUint32(&buf, math.Float32bits(pl.StorageBonusMetal))
				binaryWriteUint32(&buf, math.Float32bits(pl.StorageBonusEnergy))
			}
			if ver >= 8 {
				binaryWriteUint32(&buf, math.Float32bits(pl.AIProductionMetal))
				binaryWriteUint32(&buf, math.Float32bits(pl.AIProductionEnergy))
				binaryWriteUint32(&buf, math.Float32bits(pl.AIConsumptionMetal))
				binaryWriteUint32(&buf, math.Float32bits(pl.AIConsumptionEnergy))
			}
		}
		binaryWriteUint32(&buf, uint32(len(s.Economy.UnitBuckets)))
		for _, ub := range s.Economy.UnitBuckets {
			binaryWriteInt32(&buf, ub.Handle)
			binaryWriteUint32(&buf, math.Float32bits(ub.Buckets[0].Production))
			binaryWriteUint32(&buf, math.Float32bits(ub.Buckets[0].Requested))
			binaryWriteUint32(&buf, math.Float32bits(ub.Buckets[0].Accepted))
			binaryWriteUint32(&buf, math.Float32bits(ub.Buckets[0].Carry))
			binaryWriteUint32(&buf, math.Float32bits(ub.Buckets[1].Production))
			binaryWriteUint32(&buf, math.Float32bits(ub.Buckets[1].Requested))
			binaryWriteUint32(&buf, math.Float32bits(ub.Buckets[1].Accepted))
			binaryWriteUint32(&buf, math.Float32bits(ub.Buckets[1].Carry))
			binaryWriteUint32(&buf, math.Float32bits(ub.Archived[0].Production))
			binaryWriteUint32(&buf, math.Float32bits(ub.Archived[0].Requested))
			binaryWriteUint32(&buf, math.Float32bits(ub.Archived[0].Accepted))
			binaryWriteUint32(&buf, math.Float32bits(ub.Archived[0].Carry))
			binaryWriteUint32(&buf, math.Float32bits(ub.Archived[1].Production))
			binaryWriteUint32(&buf, math.Float32bits(ub.Archived[1].Requested))
			binaryWriteUint32(&buf, math.Float32bits(ub.Archived[1].Accepted))
			binaryWriteUint32(&buf, math.Float32bits(ub.Archived[1].Carry))
		}
		// Features
		binaryWriteInt32(&buf, s.Features.Cursor)
		binaryWriteInt32(&buf, s.Features.LastReproIdx)
		binaryWriteUint32(&buf, uint32(len(s.Features.Instances)))
		for _, f := range s.Features.Instances {
			writeString(&buf, f.DefName)
			binaryWriteInt32(&buf, f.CX)
			binaryWriteInt32(&buf, f.CZ)
			binaryWriteInt32(&buf, f.Health)
			binaryWriteInt32(&buf, f.MaxHealth)
			binaryWriteInt32(&buf, f.ReclaimProgress)
			buf.WriteByte(boolToByte(f.IsBurning))
			binaryWriteInt32(&buf, f.BurnCountdown)
			binaryWriteInt32(&buf, f.BurnTicks)
			binaryWriteInt32(&buf, f.BurnDuration)
			buf.WriteByte(boolToByte(f.IsSinking))
			binaryWriteInt32(&buf, f.Y)
			binaryWriteInt32(&buf, f.Vy)
			buf.WriteByte(boolToByte(f.Settled))
			buf.WriteByte(f.Status)
			binaryWriteInt32(&buf, f.X)
			binaryWriteInt32(&buf, f.Z)
			binaryWriteInt32(&buf, f.FootX)
			binaryWriteInt32(&buf, f.FootZ)
		}
		// Projectiles
		binaryWriteUint32(&buf, uint32(len(proj)))
		for _, p := range proj {
			binaryWriteInt32(&buf, p.Handle)
			binaryWriteInt32(&buf, p.WeaponID)
			binaryWriteInt32(&buf, p.PosX)
			binaryWriteInt32(&buf, p.PosY)
			binaryWriteInt32(&buf, p.PosZ)
			binaryWriteInt32(&buf, p.StartPosX)
			binaryWriteInt32(&buf, p.StartPosY)
			binaryWriteInt32(&buf, p.StartPosZ)
			binaryWriteInt32(&buf, p.TargetPosX)
			binaryWriteInt32(&buf, p.TargetPosY)
			binaryWriteInt32(&buf, p.TargetPosZ)
			binaryWriteInt32(&buf, p.TargetUnit)
			binaryWriteInt32(&buf, p.TargetProjectile)
			binaryWriteInt32(&buf, p.VelocityX)
			binaryWriteInt32(&buf, p.VelocityY)
			binaryWriteInt32(&buf, p.VelocityZ)
			binaryWriteInt32(&buf, p.Speed)
			binaryWriteUint16(&buf, p.Yaw)
			binaryWriteUint16(&buf, p.Pitch)
			binaryWriteInt32(&buf, p.Shooter)
			buf.WriteByte(p.ShooterSide)
			binaryWriteInt16(&buf, p.MuzzlePiece)
			binaryWriteUint32(&buf, p.CreationTick)
			binaryWriteUint32(&buf, p.BurstDeadline)
			binaryWriteInt32(&buf, p.BurstRemaining)
			binaryWriteUint32(&buf, p.ExpiryTick)
			binaryWriteUint32(&buf, p.SmokeDeadline)
			buf.WriteByte(boolToByte(p.BeamLatch))
			buf.WriteByte(boolToByte(p.TwoPhase))
			buf.WriteByte(boolToByte(p.Dead))
			binaryWriteUint16(&buf, p.PropellerYaw)
			binaryWriteUint16(&buf, p.MeteorPitch)
			binaryWriteInt32(&buf, p.CacheCellX)
			binaryWriteInt32(&buf, p.CacheCellZ)
			binaryWriteInt16(&buf, p.Scratch5E)
			buf.WriteByte(p.State69)
			binaryWriteInt16(&buf, p.OldMarker)
		}
		// AI
		binaryWriteUint32(&buf, uint32(len(ai)))
		for _, m := range ai {
			buf.WriteByte(m.Player)
			for k := 0; k < 12; k++ {
				var v uint32
				if k < len(m.Deadlines) {
					v = m.Deadlines[k]
				}
				binaryWriteUint32(&buf, v)
			}
			binaryWriteInt32(&buf, m.Strategic.CenterX)
			binaryWriteInt32(&buf, m.Strategic.CenterZ)
			binaryWriteInt32(&buf, m.Strategic.Radius)
			binaryWriteUint32(&buf, m.Strategic.LastRefreshTick)
			binaryWriteUint32(&buf, m.Strategic.LastClassRecomputeTick)
			binaryWriteUint32(&buf, uint32(len(m.Strategic.Counts)))
			for _, c := range m.Strategic.Counts {
				writeString(&buf, c.Key)
				binaryWriteInt32(&buf, c.Value)
			}
			binaryWriteUint32(&buf, uint32(len(m.Strategic.ClassVectors)))
			for _, c := range m.Strategic.ClassVectors {
				writeString(&buf, c.Key)
				buf.WriteByte(byte(c.C0))
				buf.WriteByte(byte(c.C1))
				buf.WriteByte(byte(c.C2))
			}
			binaryWriteUint32(&buf, uint32(len(m.Strategic.InitVectors)))
			for _, c := range m.Strategic.InitVectors {
				writeString(&buf, c.Key)
				buf.WriteByte(byte(c.Value))
			}
			binaryWriteUint32(&buf, uint32(len(m.Strategic.SingleVectors)))
			for _, c := range m.Strategic.SingleVectors {
				writeString(&buf, c.Key)
				buf.WriteByte(byte(c.Value))
			}
			writeGroupVector := func(v []int32) {
				binaryWriteUint32(&buf, uint32(len(v)))
				for _, h := range v {
					binaryWriteInt32(&buf, h)
				}
			}
			if ver >= StateV1Version9 {
				// [R-P0-04] exact manager record order: resource, wave A,
				// regroup A, construction, null, wave B, regroup B, explore, rally.
				writeGroupVector(m.Groups.Resource)
				writeGroupVector(m.Groups.WaveA)
				writeGroupVector(m.Groups.RegroupA)
				writeGroupVector(m.Groups.Construction)
				writeGroupVector(m.Groups.Null)
				writeGroupVector(m.Groups.WaveB)
				writeGroupVector(m.Groups.RegroupB)
				writeGroupVector(m.Groups.Explore)
				writeGroupVector(m.Groups.Rally)
			} else {
				// Preserve the pre-v9 wire order for explicitly requested old
				// versions. v8 has no resource/construction/null fields.
				writeGroupVector(m.Groups.WaveA)
				writeGroupVector(m.Groups.WaveB)
				writeGroupVector(m.Groups.Explore)
				writeGroupVector(m.Groups.Rally)
				writeGroupVector(m.Groups.RegroupA)
				writeGroupVector(m.Groups.RegroupB)
			}
			binaryWriteInt32(&buf, m.SurfaceMetal)
			binaryWriteInt32(&buf, m.OriginX)
			binaryWriteInt32(&buf, m.OriginZ)
		}
		// Visibility
		binaryWriteInt32(&buf, s.Visibility.W)
		binaryWriteInt32(&buf, s.Visibility.H)
		binaryWriteUint32(&buf, uint32(len(s.Visibility.WordMask)))
		for _, v := range s.Visibility.WordMask {
			binaryWriteUint16(&buf, v)
		}
		for p := 0; p < 10; p++ {
			binaryWriteUint32(&buf, uint32(len(s.Visibility.ByteGrids[p])))
			buf.Write(s.Visibility.ByteGrids[p])
		}
		buf.WriteByte(s.Visibility.Local)
		binaryWriteUint32(&buf, s.Visibility.Mode)
		binaryWriteUint32(&buf, uint32(len(s.Visibility.Status)))
		for _, vs := range s.Visibility.Status {
			binaryWriteInt32(&buf, vs.Handle)
			binaryWriteUint32(&buf, vs.Status)
		}
		binaryWriteUint32(&buf, uint32(len(s.Visibility.Decloak)))
		for _, vd := range s.Visibility.Decloak {
			binaryWriteInt32(&buf, vd.Handle)
			binaryWriteUint32(&buf, vd.Deadline)
		}
		// Latch
		binaryWriteInt16(&buf, s.Latch.Countdown)
		binaryWriteUint16(&buf, s.Latch.Bits)
		buf.WriteByte(s.Latch.Pending)
		// Wind
		binaryWriteInt32(&buf, s.Wind.Min)
		binaryWriteInt32(&buf, s.Wind.Max)
		binaryWriteInt32(&buf, s.Wind.Strength)
		binaryWriteUint16(&buf, s.Wind.Heading)
		binaryWriteUint32(&buf, math.Float32bits(s.Wind.Scalar))
		binaryWriteInt32(&buf, s.Wind.DirX)
		binaryWriteInt32(&buf, s.Wind.DirZ)
		binaryWriteUint32(&buf, s.Wind.NextChange)
		binaryWriteUint32(&buf, s.Wind.LastChange)
		buf.WriteByte(boolToByte(s.Wind.Changed))
		buf.WriteByte(boolToByte(s.Wind.Pending))
		if ver >= 6 {
			// Construction builder-product links [05 C18][RS-10] — sorted by Product ascending (I1)
			binaryWriteUint32(&buf, uint32(len(links)))
			for _, l := range links {
				binaryWriteInt32(&buf, l.Builder)
				binaryWriteInt32(&buf, l.Product)
			}
		}
	}
	return buf.Bytes()
}

// UnmarshalStateV1 decodes a StateV1 payload, validates catalog/manifest hashes
// against expected values (empty expected means no check), and checks version
// [PLAN_14 C18][P0-I11]. It accepts both version 1 and 2 for backwards compatibility.
// TODO(question): COB malformed-save fatal-versus-skip policy [P1-I09][GAP T13]
// Retail may abort through its allocator where Nanolathe returns an error for a
// truncated COB blob; the bulk retail loader's partial-load policy would skip
// with diagnostic per [08]. For the versioned StateV1 (Nanolathe-native) we
// currently treat a truncated COB as fatal (return error) with TODO(question)
// to revisit if retail evidence shows skip is observable.
func UnmarshalStateV1(data []byte, expectedCatalogHash, expectedManifestHash string) (*StateV1, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("save: StateV1 too short")
	}
	r := bytes.NewReader(data)
	var ver uint32
	if err := binary.Read(r, binary.LittleEndian, &ver); err != nil {
		return nil, err
	}
	if ver != StateV1VersionConst && ver != StateV1Version8 && ver != StateV1Version7 && ver != StateV1Version6 && ver != StateV1Version5 && ver != StateV1Version4 && ver != StateV1Version3 && ver != StateV1Version2 && ver != StateV1Version1 {
		return nil, ErrStateV1Version
	}
	catHash, err := readString(r)
	if err != nil {
		return nil, err
	}
	manHash, err := readString(r)
	if err != nil {
		return nil, err
	}
	if expectedCatalogHash != "" && catHash != expectedCatalogHash {
		return nil, ErrCatalogMismatch
	}
	if expectedManifestHash != "" && manHash != expectedManifestHash {
		return nil, ErrManifestMismatch
	}
	var simState uint32
	var simDraws uint64
	var crtState uint32
	var crtDraws uint64
	if err := binary.Read(r, binary.LittleEndian, &simState); err != nil {
		return nil, err
	}
	if err := binary.Read(r, binary.LittleEndian, &simDraws); err != nil {
		return nil, err
	}
	if err := binary.Read(r, binary.LittleEndian, &crtState); err != nil {
		return nil, err
	}
	if err := binary.Read(r, binary.LittleEndian, &crtDraws); err != nil {
		return nil, err
	}
	var clockBox [28]byte
	if _, err := io.ReadFull(r, clockBox[:]); err != nil {
		return nil, fmt.Errorf("save: StateV1 clock box short")
	}
	var clk clock.State
	clk.LoadBox(clockBox)
	numUnits, err := readStateCount(r, "units", 8)
	if err != nil {
		return nil, err
	}
	units := make([]UnitRecord, 0, numUnits)
	for i := uint32(0); i < numUnits; i++ {
		var slot int32
		if err := binary.Read(r, binary.LittleEndian, &slot); err != nil {
			return nil, err
		}
		defName, err := readString(r)
		if err != nil {
			return nil, err
		}
		ownerByte, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		var x, y, z, health int32
		var remBits uint32
		var flags uint32
		if err := binary.Read(r, binary.LittleEndian, &x); err != nil {
			return nil, err
		}
		if err := binary.Read(r, binary.LittleEndian, &y); err != nil {
			return nil, err
		}
		if err := binary.Read(r, binary.LittleEndian, &z); err != nil {
			return nil, err
		}
		if err := binary.Read(r, binary.LittleEndian, &health); err != nil {
			return nil, err
		}
		if err := binary.Read(r, binary.LittleEndian, &remBits); err != nil {
			return nil, err
		}
		if err := binary.Read(r, binary.LittleEndian, &flags); err != nil {
			return nil, err
		}
		var group byte
		if ver >= 7 {
			group, err = r.ReadByte()
			if err != nil {
				return nil, err
			}
		}
		var inBuildStance bool
		if ver >= 9 {
			stance, readErr := r.ReadByte()
			if readErr != nil {
				return nil, readErr
			}
			inBuildStance = stance != 0
		}
		rec := UnitRecord{
			Slot:          slot,
			DefName:       defName,
			Owner:         ownerByte,
			X:             x,
			Y:             y,
			Z:             z,
			Health:        health,
			Remaining:     math.Float32frombits(remBits),
			Flags:         flags,
			Group:         group,
			InBuildStance: inBuildStance,
		}
		if ver >= 2 {
			var maxHealth int32
			if err := binary.Read(r, binary.LittleEndian, &maxHealth); err != nil {
				return nil, err
			}
			dyingByte, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			deathCause, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			var pending uint32
			if err := binary.Read(r, binary.LittleEndian, &pending); err != nil {
				return nil, err
			}
			var kills int32
			if err := binary.Read(r, binary.LittleEndian, &kills); err != nil {
				return nil, err
			}
			var paralyze uint32
			if err := binary.Read(r, binary.LittleEndian, &paralyze); err != nil {
				return nil, err
			}
			stunnedByte, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			var spotBits uint32
			if err := binary.Read(r, binary.LittleEndian, &spotBits); err != nil {
				return nil, err
			}
			var placementIdx int32
			if err := binary.Read(r, binary.LittleEndian, &placementIdx); err != nil {
				return nil, err
			}
			placementIdent, err := readString(r)
			if err != nil {
				return nil, err
			}
			placementUnitName, err := readString(r)
			if err != nil {
				return nil, err
			}
			moveMode, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			moveHeading, err := binaryReadUint16(r)
			if err != nil {
				return nil, err
			}
			var moveSpeed int32
			if err := binary.Read(r, binary.LittleEndian, &moveSpeed); err != nil {
				return nil, err
			}
			movePendingHeading, err := binaryReadUint16(r)
			if err != nil {
				return nil, err
			}
			moveDirtyByte, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			moveHeightWord, err := binaryReadInt32(r)
			if err != nil {
				return nil, err
			}
			moveSeaLevel, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			moveDefFlags, err := binaryReadUint32(r)
			if err != nil {
				return nil, err
			}
			moveMaxVelocity, err := binaryReadInt32(r)
			if err != nil {
				return nil, err
			}
			moveTurnRate, err := binaryReadInt32(r)
			if err != nil {
				return nil, err
			}
			var carrier int32
			if err := binary.Read(r, binary.LittleEndian, &carrier); err != nil {
				return nil, err
			}
			cargoLen, err := readStateCount(r, "unit cargo", 4)
			if err != nil {
				return nil, err
			}
			cargo := make([]int32, cargoLen)
			for ci := uint32(0); ci < cargoLen; ci++ {
				if err := binary.Read(r, binary.LittleEndian, &cargo[ci]); err != nil {
					return nil, err
				}
			}
			var slots [3]SlotRecord
			for k := 0; k < 3; k++ {
				wname, err := readString(r)
				if err != nil {
					return nil, err
				}
				var reload int32
				if err := binary.Read(r, binary.LittleEndian, &reload); err != nil {
					return nil, err
				}
				flagByte, err := r.ReadByte()
				if err != nil {
					return nil, err
				}
				dyaw, err := binaryReadUint16(r)
				if err != nil {
					return nil, err
				}
				dpitch, err := binaryReadUint16(r)
				if err != nil {
					return nil, err
				}
				var ammo int32
				if err := binary.Read(r, binary.LittleEndian, &ammo); err != nil {
					return nil, err
				}
				var muzzle int32
				if err := binary.Read(r, binary.LittleEndian, &muzzle); err != nil {
					return nil, err
				}
				aimIssueByte, err := r.ReadByte()
				if err != nil {
					return nil, err
				}
				aimReadyByte, err := r.ReadByte()
				if err != nil {
					return nil, err
				}
				tkind, err := r.ReadByte()
				if err != nil {
					return nil, err
				}
				var tunit int32
				if err := binary.Read(r, binary.LittleEndian, &tunit); err != nil {
					return nil, err
				}
				var tx int32
				if err := binary.Read(r, binary.LittleEndian, &tx); err != nil {
					return nil, err
				}
				var tz int32
				if err := binary.Read(r, binary.LittleEndian, &tz); err != nil {
					return nil, err
				}
				slots[k] = SlotRecord{
					WeaponName:   wname,
					Reload:       reload,
					Flags:        flagByte,
					DesiredYaw:   dyaw,
					DesiredPitch: dpitch,
					Ammo:         ammo,
					MuzzlePiece:  muzzle,
					AimIssue:     byteToBool(aimIssueByte),
					AimReady:     byteToBool(aimReadyByte),
					TargetKind:   tkind,
					TargetUnit:   tunit,
					TargetX:      tx,
					TargetZ:      tz,
				}
			}
			hasCOBByte, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			hasCOB := byteToBool(hasCOBByte)
			var cobRec COBRecord
			if hasCOB {
				nStatics, err := readStateCount(r, "COB statics", 4)
				if err != nil {
					return nil, err
				}
				statics := make([]int32, nStatics)
				for si := uint32(0); si < nStatics; si++ {
					if err := binary.Read(r, binary.LittleEndian, &statics[si]); err != nil {
						return nil, err
					}
				}
				cobRec.Statics = statics
				for t := 0; t < 8; t++ {
					var st, pc, sp, slp, wpiece, waxis, wthread, smask int32
					if err := binary.Read(r, binary.LittleEndian, &st); err != nil {
						return nil, err
					}
					if err := binary.Read(r, binary.LittleEndian, &pc); err != nil {
						return nil, err
					}
					if err := binary.Read(r, binary.LittleEndian, &sp); err != nil {
						return nil, err
					}
					if err := binary.Read(r, binary.LittleEndian, &slp); err != nil {
						return nil, err
					}
					if err := binary.Read(r, binary.LittleEndian, &wpiece); err != nil {
						return nil, err
					}
					if err := binary.Read(r, binary.LittleEndian, &waxis); err != nil {
						return nil, err
					}
					if err := binary.Read(r, binary.LittleEndian, &wthread); err != nil {
						return nil, err
					}
					if err := binary.Read(r, binary.LittleEndian, &smask); err != nil {
						return nil, err
					}
					stackLen, err := readStateCount(r, "COB stack", 4)
					if err != nil {
						return nil, err
					}
					stack := make([]int32, stackLen)
					for si := uint32(0); si < stackLen; si++ {
						if err := binary.Read(r, binary.LittleEndian, &stack[si]); err != nil {
							return nil, err
						}
					}
					cobRec.Threads[t] = ThreadRecord{
						Status:     st,
						PC:         pc,
						SP:         sp,
						Sleep:      slp,
						WaitPiece:  wpiece,
						WaitAxis:   waxis,
						WaitThread: wthread,
						SignalMask: smask,
						Stack:      stack,
					}
				}
				if ver >= 3 {
					nPieces, err := readStateCount(r, "COB pieces", 14)
					if err != nil {
						return nil, err
					}
					if nPieces > 0 {
						pieces := make([]PieceStateSave, nPieces)
						for pi := uint32(0); pi < nPieces; pi++ {
							var rx, ry, rz uint16
							if err := binary.Read(r, binary.LittleEndian, &rx); err != nil {
								return nil, err
							}
							if err := binary.Read(r, binary.LittleEndian, &ry); err != nil {
								return nil, err
							}
							if err := binary.Read(r, binary.LittleEndian, &rz); err != nil {
								return nil, err
							}
							var tx, ty, tz int32
							if err := binary.Read(r, binary.LittleEndian, &tx); err != nil {
								return nil, err
							}
							if err := binary.Read(r, binary.LittleEndian, &ty); err != nil {
								return nil, err
							}
							if err := binary.Read(r, binary.LittleEndian, &tz); err != nil {
								return nil, err
							}
							pieces[pi] = PieceStateSave{RotX: rx, RotY: ry, RotZ: rz, TransX: tx, TransY: ty, TransZ: tz}
						}
						cobRec.Pieces = pieces
					}
					nFlags, err := readStateCount(r, "COB flags", 1)
					if err != nil {
						return nil, err
					}
					if nFlags > 0 {
						flags := make([]uint8, nFlags)
						if _, err := io.ReadFull(r, flags); err != nil {
							return nil, err
						}
						cobRec.Flags = flags
					}
					nAnims, err := readStateCount(r, "COB animations", 120)
					if err != nil {
						return nil, err
					}
					if nAnims > 0 {
						anims := make([]PieceAnimSave, nAnims)
						for pi := uint32(0); pi < nAnims; pi++ {
							for ax := 0; ax < 3; ax++ {
								var mt, ms int32
								if err := binary.Read(r, binary.LittleEndian, &mt); err != nil {
									return nil, err
								}
								if err := binary.Read(r, binary.LittleEndian, &ms); err != nil {
									return nil, err
								}
								mb, err := r.ReadByte()
								if err != nil {
									return nil, err
								}
								var tt uint16
								if err := binary.Read(r, binary.LittleEndian, &tt); err != nil {
									return nil, err
								}
								var ts int32
								if err := binary.Read(r, binary.LittleEndian, &ts); err != nil {
									return nil, err
								}
								tb, err := r.ReadByte()
								if err != nil {
									return nil, err
								}
								var ss, st, sa int32
								if err := binary.Read(r, binary.LittleEndian, &ss); err != nil {
									return nil, err
								}
								if err := binary.Read(r, binary.LittleEndian, &st); err != nil {
									return nil, err
								}
								if err := binary.Read(r, binary.LittleEndian, &sa); err != nil {
									return nil, err
								}
								sab, err := r.ReadByte()
								if err != nil {
									return nil, err
								}
								anims[pi].Axes[ax] = AxisAnimSave{MoveTarget: mt, MoveSpeed: ms, MoveBusy: byteToBool(mb), TurnTarget: tt, TurnSpeed: ts, TurnBusy: byteToBool(tb), SpinSpeed: ss, SpinTarget: st, SpinAccel: sa, SpinActive: byteToBool(sab)}
							}
						}
						cobRec.Anims = anims
					}
				}
			}
			rec.MaxHealth = maxHealth
			rec.Dying = byteToBool(dyingByte)
			rec.DeathCause = deathCause
			rec.Pending = pending
			rec.Kills = kills
			rec.ParalyzeExpire = paralyze
			rec.Stunned = byteToBool(stunnedByte)
			rec.SpotMetal = math.Float32frombits(spotBits)
			rec.PlacementIdx = placementIdx
			rec.PlacementIdent = placementIdent
			rec.PlacementUnitName = placementUnitName
			rec.MoveMode = moveMode
			rec.MoveHeading = moveHeading
			rec.MoveSpeed = moveSpeed
			rec.MovePendingHeading = movePendingHeading
			rec.MoveDirty = byteToBool(moveDirtyByte)
			rec.MoveHeightWord = int16(moveHeightWord)
			rec.MoveSeaLevel = moveSeaLevel
			rec.MoveDefFlags = moveDefFlags
			rec.MoveMaxVelocity = moveMaxVelocity
			rec.MoveTurnRate = moveTurnRate
			rec.Carrier = carrier
			rec.Cargo = cargo
			rec.Slots = slots
			rec.HasCOB = hasCOB
			rec.COB = cobRec
		}
		units = append(units, rec)
	}
	numQueues, err := readStateCount(r, "queues", 16)
	if err != nil {
		return nil, err
	}
	queues := make([]QueueRecord, 0, numQueues)
	for i := uint32(0); i < numQueues; i++ {
		var slot int32
		if err := binary.Read(r, binary.LittleEndian, &slot); err != nil {
			return nil, err
		}
		kind, err := readString(r)
		if err != nil {
			return nil, err
		}
		var count int32
		if err := binary.Read(r, binary.LittleEndian, &count); err != nil {
			return nil, err
		}
		payloadLen, err := readStateCount(r, "queue payload", 1)
		if err != nil {
			return nil, err
		}
		payload := make([]byte, payloadLen)
		if payloadLen > 0 {
			if _, err := io.ReadFull(r, payload); err != nil {
				return nil, fmt.Errorf("save: StateV1 queue payload short")
			}
		}
		queues = append(queues, QueueRecord{
			UnitSlot: slot,
			Kind:     kind,
			Count:    count,
			Payload:  payload,
		})
	}
	// Ensure deterministic iteration order on decode as well (I1) [01 §6.1].
	sort.Slice(units, func(i, j int) bool { return units[i].Slot < units[j].Slot })
	sort.Slice(queues, func(i, j int) bool {
		if queues[i].UnitSlot != queues[j].UnitSlot {
			return queues[i].UnitSlot < queues[j].UnitSlot
		}
		return queues[i].Kind < queues[j].Kind
	})
	st := &StateV1{
		Version:      ver,
		CatalogHash:  catHash,
		ManifestHash: manHash,
		SimState:     simState,
		SimDraws:     simDraws,
		CrtState:     crtState,
		CrtDraws:     crtDraws,
		Clock:        clk,
		Units:        units,
		Queues:       queues,
	}
	if ver >= 2 {
		// Orders
		nOrders, err := readStateCount(r, "orders", 8)
		if err != nil {
			return nil, err
		}
		orders := make([]OrderRecord, 0, nOrders)
		for i := uint32(0); i < nOrders; i++ {
			var rec OrderRecord
			var unitSlot int32
			if err := binary.Read(r, binary.LittleEndian, &unitSlot); err != nil {
				return nil, err
			}
			seg, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			var idx int32
			if err := binary.Read(r, binary.LittleEndian, &idx); err != nil {
				return nil, err
			}
			desc, err := readString(r)
			if err != nil {
				return nil, err
			}
			phase, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			var dg uint32
			if err := binary.Read(r, binary.LittleEndian, &dg); err != nil {
				return nil, err
			}
			var dl int32
			if err := binary.Read(r, binary.LittleEndian, &dl); err != nil {
				return nil, err
			}
			var owner, target, gx, gy, gz int32
			if err := binary.Read(r, binary.LittleEndian, &owner); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &target); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &gx); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &gy); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &gz); err != nil {
				return nil, err
			}
			gx16, err := binaryReadInt16(r)
			if err != nil {
				return nil, err
			}
			gy16, err := binaryReadInt16(r)
			if err != nil {
				return nil, err
			}
			cx16, err := binaryReadInt16(r)
			if err != nil {
				return nil, err
			}
			cy16, err := binaryReadInt16(r)
			if err != nil {
				return nil, err
			}
			var p1, p2, p3, sg, ck, sat, fl uint32
			if err := binary.Read(r, binary.LittleEndian, &p1); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &p2); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &p3); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &sg); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &ck); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &sat); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &fl); err != nil {
				return nil, err
			}
			moveState, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			var ps uint32
			if err := binary.Read(r, binary.LittleEndian, &ps); err != nil {
				return nil, err
			}
			bkey, err := readString(r)
			if err != nil {
				return nil, err
			}
			rec = OrderRecord{
				UnitSlot:     unitSlot,
				Segment:      seg,
				Index:        idx,
				Descriptor:   desc,
				Phase:        phase,
				DynamicGate:  dg,
				Deadline:     dl,
				Owner:        owner,
				Target:       target,
				GoalX:        gx,
				GoalY:        gy,
				GoalZ:        gz,
				GuardX:       gx16,
				GuardY:       gy16,
				CachedX:      cx16,
				CachedY:      cy16,
				Param1:       p1,
				Param2:       p2,
				Param3:       p3,
				StaticGate:   sg,
				CreationTick: ck,
				Satisfied:    sat,
				Flags:        fl,
				MoveState:    moveState,
				PathStatus:   ps,
				BuildDefKey:  bkey,
			}
			orders = append(orders, rec)
		}
		sort.Slice(orders, func(i, j int) bool {
			if orders[i].UnitSlot != orders[j].UnitSlot {
				return orders[i].UnitSlot < orders[j].UnitSlot
			}
			if orders[i].Segment != orders[j].Segment {
				return orders[i].Segment < orders[j].Segment
			}
			return orders[i].Index < orders[j].Index
		})
		st.Orders = orders
		// MovementRoutes
		nRoutes, err := readStateCount(r, "movement routes", 8)
		if err != nil {
			return nil, err
		}
		routes := make([]MovementRouteRecord, 0, nRoutes)
		for i := uint32(0); i < nRoutes; i++ {
			var unit int32
			if err := binary.Read(r, binary.LittleEndian, &unit); err != nil {
				return nil, err
			}
			cnt, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			activeB, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			dirtyB, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			var pts [20]PointRecord
			for p := 0; p < 20; p++ {
				var x, z int32
				if err := binary.Read(r, binary.LittleEndian, &x); err != nil {
					return nil, err
				}
				if err := binary.Read(r, binary.LittleEndian, &z); err != nil {
					return nil, err
				}
				pts[p] = PointRecord{X: x, Z: z}
			}
			routes = append(routes, MovementRouteRecord{
				Unit:   unit,
				Count:  cnt,
				Active: byteToBool(activeB),
				Dirty:  byteToBool(dirtyB),
				Points: pts,
			})
		}
		sort.Slice(routes, func(i, j int) bool { return routes[i].Unit < routes[j].Unit })
		st.MovementRoutes = routes
		// SchedulerPending
		nPend, err := readStateCount(r, "pending paths", 8)
		if err != nil {
			return nil, err
		}
		pend := make([]SchedulerPendingRecord, 0, nPend)
		for i := uint32(0); i < nPend; i++ {
			var unit int32
			if err := binary.Read(r, binary.LittleEndian, &unit); err != nil {
				return nil, err
			}
			player, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			var sx, sz, gx, gz, rad int32
			if err := binary.Read(r, binary.LittleEndian, &sx); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &sz); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &gx); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &gz); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &rad); err != nil {
				return nil, err
			}
			kind, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			pend = append(pend, SchedulerPendingRecord{
				Unit: unit, Player: player, StartX: sx, StartZ: sz, GoalX: gx, GoalZ: gz, GoalRadius: rad, GoalKind: kind,
			})
		}
		sort.Slice(pend, func(i, j int) bool { return pend[i].Unit < pend[j].Unit })
		st.SchedulerPending = pend
		if ver >= 4 {
			nSteer, err := readStateCount(r, "movement steers", 8)
			if err != nil {
				return nil, err
			}
			steers := make([]MovementSteerRecord, 0, nSteer)
			for i := uint32(0); i < nSteer; i++ {
				var rec MovementSteerRecord
				if err := binary.Read(r, binary.LittleEndian, &rec.Handle); err != nil {
					return nil, err
				}
				if err := binary.Read(r, binary.LittleEndian, &rec.X); err != nil {
					return nil, err
				}
				if err := binary.Read(r, binary.LittleEndian, &rec.Z); err != nil {
					return nil, err
				}
				if err := binary.Read(r, binary.LittleEndian, &rec.Heading); err != nil {
					return nil, err
				}
				if err := binary.Read(r, binary.LittleEndian, &rec.PendingHeading); err != nil {
					return nil, err
				}
				dirtyB, err := r.ReadByte()
				if err != nil {
					return nil, err
				}
				rec.Dirty = byteToBool(dirtyB)
				if err := binary.Read(r, binary.LittleEndian, &rec.Speed); err != nil {
					return nil, err
				}
				if err := binary.Read(r, binary.LittleEndian, &rec.MaxVelocity); err != nil {
					return nil, err
				}
				if err := binary.Read(r, binary.LittleEndian, &rec.TurnRate); err != nil {
					return nil, err
				}
				if err := binary.Read(r, binary.LittleEndian, &rec.HeightWord); err != nil {
					return nil, err
				}
				seaB, err := r.ReadByte()
				if err != nil {
					return nil, err
				}
				rec.SeaLevel = seaB
				if err := binary.Read(r, binary.LittleEndian, &rec.DefFlags); err != nil {
					return nil, err
				}
				steers = append(steers, rec)
			}
			sort.Slice(steers, func(i, j int) bool { return steers[i].Handle < steers[j].Handle })
			st.MovementSteers = steers
		}
		// Economy
		var econ EconomySnapshot
		for i := 0; i < 10; i++ {
			var pl EconomyPlayerRecord
			existsB, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			ctrlB, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			obsB, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			var sm, se, cm, ce uint32
			if err := binary.Read(r, binary.LittleEndian, &sm); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &se); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &cm); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &ce); err != nil {
				return nil, err
			}
			var mmProd, mmReq, mmAcc, mmCarry, meProd, meReq, meAcc, meCarry uint32
			if err := binary.Read(r, binary.LittleEndian, &mmProd); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &mmReq); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &mmAcc); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &mmCarry); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &meProd); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &meReq); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &meAcc); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &meCarry); err != nil {
				return nil, err
			}
			var ut, wlt, dt uint32
			if err := binary.Read(r, binary.LittleEndian, &ut); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &wlt); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &dt); err != nil {
				return nil, err
			}
			var wm, we, tpm, tpe, tcm, tce uint64
			if err := binary.Read(r, binary.LittleEndian, &wm); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &we); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &tpm); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &tpe); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &tcm); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &tce); err != nil {
				return nil, err
			}
			var ppm, ppe, pcm, pce uint32
			if err := binary.Read(r, binary.LittleEndian, &ppm); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &ppe); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &pcm); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &pce); err != nil {
				return nil, err
			}
			var amProd, amReq, amAcc, amCarry, aeProd, aeReq, aeAcc, aeCarry uint32
			if err := binary.Read(r, binary.LittleEndian, &amProd); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &amReq); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &amAcc); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &amCarry); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &aeProd); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &aeReq); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &aeAcc); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &aeCarry); err != nil {
				return nil, err
			}
			sh, err := binaryReadInt16(r)
			if err != nil {
				return nil, err
			}
			var sw int32
			if err := binary.Read(r, binary.LittleEndian, &sw); err != nil {
				return nil, err
			}
			geB, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			var egc int32
			if err := binary.Read(r, binary.LittleEndian, &egc); err != nil {
				return nil, err
			}
			var h1, h2 uint32
			if err := binary.Read(r, binary.LittleEndian, &h1); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &h2); err != nil {
				return nil, err
			}
			var h1c, h2c, wrc int32
			if ver >= 4 {
				if err := binary.Read(r, binary.LittleEndian, &h1c); err != nil {
					return nil, err
				}
				if err := binary.Read(r, binary.LittleEndian, &h2c); err != nil {
					return nil, err
				}
				if err := binary.Read(r, binary.LittleEndian, &wrc); err != nil {
					return nil, err
				}
			}
			var rp, ssc int32
			if err := binary.Read(r, binary.LittleEndian, &rp); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &ssc); err != nil {
				return nil, err
			}
			var sbe bool
			var sbm, sbe32 uint32
			if ver >= 5 {
				b, err := r.ReadByte()
				if err != nil {
					return nil, err
				}
				sbe = byteToBool(b)
				if err := binary.Read(r, binary.LittleEndian, &sbm); err != nil {
					return nil, err
				}
				if err := binary.Read(r, binary.LittleEndian, &sbe32); err != nil {
					return nil, err
				}
			}
			var aipm, aipe, aicm, aice uint32
			if ver >= 8 {
				if err := binary.Read(r, binary.LittleEndian, &aipm); err != nil {
					return nil, err
				}
				if err := binary.Read(r, binary.LittleEndian, &aipe); err != nil {
					return nil, err
				}
				if err := binary.Read(r, binary.LittleEndian, &aicm); err != nil {
					return nil, err
				}
				if err := binary.Read(r, binary.LittleEndian, &aice); err != nil {
					return nil, err
				}
			}
			pl = EconomyPlayerRecord{
				Exists: byteToBool(existsB), ControllerState: ctrlB, IsObserver: byteToBool(obsB),
				StockMetal: math.Float32frombits(sm), StockEnergy: math.Float32frombits(se),
				CapacityMetal: math.Float32frombits(cm), CapacityEnergy: math.Float32frombits(ce),
				MirrorMetal:  BucketRecord{Production: math.Float32frombits(mmProd), Requested: math.Float32frombits(mmReq), Accepted: math.Float32frombits(mmAcc), Carry: math.Float32frombits(mmCarry)},
				MirrorEnergy: BucketRecord{Production: math.Float32frombits(meProd), Requested: math.Float32frombits(meReq), Accepted: math.Float32frombits(meAcc), Carry: math.Float32frombits(meCarry)},
				UpdateTime:   ut, WinLoseTime: wlt, DisplayTimer: dt,
				WasteMetal: math.Float64frombits(wm), WasteEnergy: math.Float64frombits(we),
				TotalProducedMetal: math.Float64frombits(tpm), TotalProducedEnergy: math.Float64frombits(tpe),
				TotalConsumedMetal: math.Float64frombits(tcm), TotalConsumedEnergy: math.Float64frombits(tce),
				PassProducedMetal: math.Float32frombits(ppm), PassProducedEnergy: math.Float32frombits(ppe),
				PassConsumedMetal: math.Float32frombits(pcm), PassConsumedEnergy: math.Float32frombits(pce),
				ArchivedMetal:       BucketRecord{Production: math.Float32frombits(amProd), Requested: math.Float32frombits(amReq), Accepted: math.Float32frombits(amAcc), Carry: math.Float32frombits(amCarry)},
				ArchivedEnergy:      BucketRecord{Production: math.Float32frombits(aeProd), Requested: math.Float32frombits(aeReq), Accepted: math.Float32frombits(aeAcc), Carry: math.Float32frombits(aeCarry)},
				StatusHalfwordAt144: sh, StatusWordAt140: sw, GameEnded: byteToBool(geB), EndGameCountdown: egc,
				Helper1Deadline: h1, Helper2Deadline: h2, Helper1Calls: h1c, Helper2Calls: h2c, WeaponRefreshCalls: wrc, ReferencePlayer: rp, SensorShareCalls: ssc,
				StorageBonusEnabled: sbe, StorageBonusMetal: math.Float32frombits(sbm), StorageBonusEnergy: math.Float32frombits(sbe32),
				AIProductionMetal: math.Float32frombits(aipm), AIProductionEnergy: math.Float32frombits(aipe),
				AIConsumptionMetal: math.Float32frombits(aicm), AIConsumptionEnergy: math.Float32frombits(aice),
			}
			econ.Players[i] = pl
		}
		nUB, err := readStateCount(r, "economy unit buckets", 8)
		if err != nil {
			return nil, err
		}
		ubs := make([]EconomyUnitBucketRecord, 0, nUB)
		for i := uint32(0); i < nUB; i++ {
			var h int32
			if err := binary.Read(r, binary.LittleEndian, &h); err != nil {
				return nil, err
			}
			var b0Prod, b0Req, b0Acc, b0Carry, b1Prod, b1Req, b1Acc, b1Carry uint32
			if err := binary.Read(r, binary.LittleEndian, &b0Prod); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &b0Req); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &b0Acc); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &b0Carry); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &b1Prod); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &b1Req); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &b1Acc); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &b1Carry); err != nil {
				return nil, err
			}
			var a0Prod, a0Req, a0Acc, a0Carry, a1Prod, a1Req, a1Acc, a1Carry uint32
			if err := binary.Read(r, binary.LittleEndian, &a0Prod); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &a0Req); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &a0Acc); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &a0Carry); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &a1Prod); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &a1Req); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &a1Acc); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &a1Carry); err != nil {
				return nil, err
			}
			ubs = append(ubs, EconomyUnitBucketRecord{
				Handle: h,
				Buckets: [2]BucketRecord{
					{Production: math.Float32frombits(b0Prod), Requested: math.Float32frombits(b0Req), Accepted: math.Float32frombits(b0Acc), Carry: math.Float32frombits(b0Carry)},
					{Production: math.Float32frombits(b1Prod), Requested: math.Float32frombits(b1Req), Accepted: math.Float32frombits(b1Acc), Carry: math.Float32frombits(b1Carry)},
				},
				Archived: [2]BucketRecord{
					{Production: math.Float32frombits(a0Prod), Requested: math.Float32frombits(a0Req), Accepted: math.Float32frombits(a0Acc), Carry: math.Float32frombits(a0Carry)},
					{Production: math.Float32frombits(a1Prod), Requested: math.Float32frombits(a1Req), Accepted: math.Float32frombits(a1Acc), Carry: math.Float32frombits(a1Carry)},
				},
			})
		}
		econ.UnitBuckets = ubs
		st.Economy = econ
		// Features
		var feat FeaturesSnapshot
		var cur, lri int32
		if err := binary.Read(r, binary.LittleEndian, &cur); err != nil {
			return nil, err
		}
		if err := binary.Read(r, binary.LittleEndian, &lri); err != nil {
			return nil, err
		}
		nInst, err := readStateCount(r, "features", 8)
		if err != nil {
			return nil, err
		}
		insts := make([]FeatureInstanceRecord, 0, nInst)
		for i := uint32(0); i < nInst; i++ {
			defName, err := readString(r)
			if err != nil {
				return nil, err
			}
			var cx, cz, hl, mhl, rp int32
			if err := binary.Read(r, binary.LittleEndian, &cx); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &cz); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &hl); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &mhl); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &rp); err != nil {
				return nil, err
			}
			isBurnB, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			var bc, bt, bd int32
			if err := binary.Read(r, binary.LittleEndian, &bc); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &bt); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &bd); err != nil {
				return nil, err
			}
			isSinkB, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			var y, vy int32
			if err := binary.Read(r, binary.LittleEndian, &y); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &vy); err != nil {
				return nil, err
			}
			settledB, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			status, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			var fx, fz, footx, footz int32
			if err := binary.Read(r, binary.LittleEndian, &fx); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &fz); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &footx); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &footz); err != nil {
				return nil, err
			}
			insts = append(insts, FeatureInstanceRecord{
				DefName: defName, CX: cx, CZ: cz, Health: hl, MaxHealth: mhl, ReclaimProgress: rp,
				IsBurning: byteToBool(isBurnB), BurnCountdown: bc, BurnTicks: bt, BurnDuration: bd,
				IsSinking: byteToBool(isSinkB), Y: y, Vy: vy, Settled: byteToBool(settledB), Status: status,
				X: fx, Z: fz, FootX: footx, FootZ: footz,
			})
		}
		feat.Cursor = cur
		feat.LastReproIdx = lri
		feat.Instances = insts
		st.Features = feat
		// Projectiles
		nProj, err := readStateCount(r, "projectiles", 8)
		if err != nil {
			return nil, err
		}
		projs := make([]ProjectileRecord, 0, nProj)
		for i := uint32(0); i < nProj; i++ {
			var rec ProjectileRecord
			if err := binary.Read(r, binary.LittleEndian, &rec.Handle); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &rec.WeaponID); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &rec.PosX); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &rec.PosY); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &rec.PosZ); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &rec.StartPosX); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &rec.StartPosY); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &rec.StartPosZ); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &rec.TargetPosX); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &rec.TargetPosY); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &rec.TargetPosZ); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &rec.TargetUnit); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &rec.TargetProjectile); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &rec.VelocityX); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &rec.VelocityY); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &rec.VelocityZ); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &rec.Speed); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &rec.Yaw); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &rec.Pitch); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &rec.Shooter); err != nil {
				return nil, err
			}
			sb, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			rec.ShooterSide = sb
			mp, err := binaryReadInt16(r)
			if err != nil {
				return nil, err
			}
			rec.MuzzlePiece = mp
			if err := binary.Read(r, binary.LittleEndian, &rec.CreationTick); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &rec.BurstDeadline); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &rec.BurstRemaining); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &rec.ExpiryTick); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &rec.SmokeDeadline); err != nil {
				return nil, err
			}
			bl, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			tp, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			dd, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			rec.BeamLatch = byteToBool(bl)
			rec.TwoPhase = byteToBool(tp)
			rec.Dead = byteToBool(dd)
			if err := binary.Read(r, binary.LittleEndian, &rec.PropellerYaw); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &rec.MeteorPitch); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &rec.CacheCellX); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &rec.CacheCellZ); err != nil {
				return nil, err
			}
			scratch, err := binaryReadInt16(r)
			if err != nil {
				return nil, err
			}
			rec.Scratch5E = scratch
			s69, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			rec.State69 = s69
			om, err := binaryReadInt16(r)
			if err != nil {
				return nil, err
			}
			rec.OldMarker = om
			projs = append(projs, rec)
		}
		sort.Slice(projs, func(i, j int) bool { return projs[i].Handle < projs[j].Handle })
		st.Projectiles = projs
		// AI
		nAI, err := readStateCount(r, "AI managers", 8)
		if err != nil {
			return nil, err
		}
		ais := make([]AIManagerRecord, 0, nAI)
		for i := uint32(0); i < nAI; i++ {
			var playerB byte
			playerB, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			var dl [12]uint32
			for k := 0; k < 12; k++ {
				if err := binary.Read(r, binary.LittleEndian, &dl[k]); err != nil {
					return nil, err
				}
			}
			var cx, cz, rad int32
			if err := binary.Read(r, binary.LittleEndian, &cx); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &cz); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &rad); err != nil {
				return nil, err
			}
			var lrt, lcrt uint32
			if err := binary.Read(r, binary.LittleEndian, &lrt); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &lcrt); err != nil {
				return nil, err
			}
			nCounts, err := readStateCount(r, "AI strategic counts", 8)
			if err != nil {
				return nil, err
			}
			counts := make([]StrategicCountRecord, 0, nCounts)
			for c := uint32(0); c < nCounts; c++ {
				k, err := readString(r)
				if err != nil {
					return nil, err
				}
				var v int32
				if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
					return nil, err
				}
				counts = append(counts, StrategicCountRecord{Key: k, Value: v})
			}
			nCV, err := readStateCount(r, "AI class vectors", 8)
			if err != nil {
				return nil, err
			}
			cvs := make([]StrategicClassVectorRecord, 0, nCV)
			for c := uint32(0); c < nCV; c++ {
				k, err := readString(r)
				if err != nil {
					return nil, err
				}
				c0, err := r.ReadByte()
				if err != nil {
					return nil, err
				}
				c1, err := r.ReadByte()
				if err != nil {
					return nil, err
				}
				c2, err := r.ReadByte()
				if err != nil {
					return nil, err
				}
				cvs = append(cvs, StrategicClassVectorRecord{Key: k, C0: int8(c0), C1: int8(c1), C2: int8(c2)})
			}
			nIV, err := readStateCount(r, "AI init vectors", 8)
			if err != nil {
				return nil, err
			}
			ivs := make([]StrategicInitVectorRecord, 0, nIV)
			for c := uint32(0); c < nIV; c++ {
				k, err := readString(r)
				if err != nil {
					return nil, err
				}
				vb, err := r.ReadByte()
				if err != nil {
					return nil, err
				}
				ivs = append(ivs, StrategicInitVectorRecord{Key: k, Value: int8(vb)})
			}
			nSV, err := readStateCount(r, "AI single vectors", 8)
			if err != nil {
				return nil, err
			}
			svs := make([]StrategicSingleVectorRecord, 0, nSV)
			for c := uint32(0); c < nSV; c++ {
				k, err := readString(r)
				if err != nil {
					return nil, err
				}
				vb, err := r.ReadByte()
				if err != nil {
					return nil, err
				}
				svs = append(svs, StrategicSingleVectorRecord{Key: k, Value: int8(vb)})
			}
			readGroupVector := func(label string) ([]int32, error) {
				n, err := readStateCount(r, label, 4)
				if err != nil {
					return nil, err
				}
				v := make([]int32, n)
				for idx := uint32(0); idx < n; idx++ {
					if err := binary.Read(r, binary.LittleEndian, &v[idx]); err != nil {
						return nil, err
					}
				}
				return v, nil
			}
			var resource, construction, nullGroup []int32
			var waveA, waveB, exp, rally, regA, regB []int32
			if ver >= StateV1Version9 {
				if resource, err = readGroupVector("AI resource"); err != nil {
					return nil, err
				}
				if waveA, err = readGroupVector("AI wave A"); err != nil {
					return nil, err
				}
				if regA, err = readGroupVector("AI regroup A"); err != nil {
					return nil, err
				}
				if construction, err = readGroupVector("AI construction"); err != nil {
					return nil, err
				}
				if nullGroup, err = readGroupVector("AI null"); err != nil {
					return nil, err
				}
				if waveB, err = readGroupVector("AI wave B"); err != nil {
					return nil, err
				}
				if regB, err = readGroupVector("AI regroup B"); err != nil {
					return nil, err
				}
				if exp, err = readGroupVector("AI explore"); err != nil {
					return nil, err
				}
				if rally, err = readGroupVector("AI rally"); err != nil {
					return nil, err
				}
			} else {
				if waveA, err = readGroupVector("AI wave A"); err != nil {
					return nil, err
				}
				if waveB, err = readGroupVector("AI wave B"); err != nil {
					return nil, err
				}
				if exp, err = readGroupVector("AI explore"); err != nil {
					return nil, err
				}
				if rally, err = readGroupVector("AI rally"); err != nil {
					return nil, err
				}
				if regA, err = readGroupVector("AI regroup A"); err != nil {
					return nil, err
				}
				if regB, err = readGroupVector("AI regroup B"); err != nil {
					return nil, err
				}
			}
			var surf, ox, oz int32
			if err := binary.Read(r, binary.LittleEndian, &surf); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &ox); err != nil {
				return nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &oz); err != nil {
				return nil, err
			}
			ais = append(ais, AIManagerRecord{
				Player: playerB, Deadlines: dl,
				Strategic:    StrategicSnapshot{CenterX: cx, CenterZ: cz, Radius: rad, LastRefreshTick: lrt, LastClassRecomputeTick: lcrt, Counts: counts, ClassVectors: cvs, InitVectors: ivs, SingleVectors: svs},
				Groups:       AIGroupsSnapshot{Resource: resource, WaveA: waveA, RegroupA: regA, Construction: construction, Null: nullGroup, WaveB: waveB, RegroupB: regB, Explore: exp, Rally: rally},
				SurfaceMetal: surf, OriginX: ox, OriginZ: oz,
			})
		}
		sort.Slice(ais, func(i, j int) bool { return ais[i].Player < ais[j].Player })
		if err := validateAIGroups(ais); err != nil {
			return nil, err
		}
		st.AI = ais
		// Visibility
		var vis VisibilitySnapshot
		var wv, hv int32
		if err := binary.Read(r, binary.LittleEndian, &wv); err != nil {
			return nil, err
		}
		if err := binary.Read(r, binary.LittleEndian, &hv); err != nil {
			return nil, err
		}
		nWord, err := readStateCount(r, "visibility word mask", 2)
		if err != nil {
			return nil, err
		}
		wordMask := make([]uint16, nWord)
		for i := uint32(0); i < nWord; i++ {
			if err := binary.Read(r, binary.LittleEndian, &wordMask[i]); err != nil {
				return nil, err
			}
		}
		var byteGrids [10][]uint8
		for p := 0; p < 10; p++ {
			nByte, err := readStateCount(r, "visibility byte grid", 1)
			if err != nil {
				return nil, err
			}
			b := make([]uint8, nByte)
			if nByte > 0 {
				if _, err := io.ReadFull(r, b); err != nil {
					return nil, err
				}
			}
			byteGrids[p] = b
		}
		localB, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		var mode uint32
		if err := binary.Read(r, binary.LittleEndian, &mode); err != nil {
			return nil, err
		}
		nStatus, err := readStateCount(r, "visibility status", 8)
		if err != nil {
			return nil, err
		}
		status := make([]VisibilityStatusRecord, 0, nStatus)
		for i := uint32(0); i < nStatus; i++ {
			var h int32
			if err := binary.Read(r, binary.LittleEndian, &h); err != nil {
				return nil, err
			}
			var s uint32
			if err := binary.Read(r, binary.LittleEndian, &s); err != nil {
				return nil, err
			}
			status = append(status, VisibilityStatusRecord{Handle: h, Status: s})
		}
		nDecloak, err := readStateCount(r, "visibility decloak", 8)
		if err != nil {
			return nil, err
		}
		decloak := make([]VisibilityDecloakRecord, 0, nDecloak)
		for i := uint32(0); i < nDecloak; i++ {
			var h int32
			if err := binary.Read(r, binary.LittleEndian, &h); err != nil {
				return nil, err
			}
			var d uint32
			if err := binary.Read(r, binary.LittleEndian, &d); err != nil {
				return nil, err
			}
			decloak = append(decloak, VisibilityDecloakRecord{Handle: h, Deadline: d})
		}
		vis.W = wv
		vis.H = hv
		vis.WordMask = wordMask
		vis.ByteGrids = byteGrids
		vis.Local = localB
		vis.Mode = mode
		vis.Status = status
		vis.Decloak = decloak
		st.Visibility = vis
		// Latch
		var latch LatchSnapshot
		cd, err := binaryReadInt16(r)
		if err != nil {
			return nil, err
		}
		bits, err := binaryReadUint16(r)
		if err != nil {
			return nil, err
		}
		pendB, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		latch.Countdown = cd
		latch.Bits = bits
		latch.Pending = pendB
		st.Latch = latch
		// Wind
		var wind WindSnapshot
		if err := binary.Read(r, binary.LittleEndian, &wind.Min); err != nil {
			return nil, err
		}
		if err := binary.Read(r, binary.LittleEndian, &wind.Max); err != nil {
			return nil, err
		}
		if err := binary.Read(r, binary.LittleEndian, &wind.Strength); err != nil {
			return nil, err
		}
		if err := binary.Read(r, binary.LittleEndian, &wind.Heading); err != nil {
			return nil, err
		}
		var scalarBits uint32
		if err := binary.Read(r, binary.LittleEndian, &scalarBits); err != nil {
			return nil, err
		}
		wind.Scalar = math.Float32frombits(scalarBits)
		if err := binary.Read(r, binary.LittleEndian, &wind.DirX); err != nil {
			return nil, err
		}
		if err := binary.Read(r, binary.LittleEndian, &wind.DirZ); err != nil {
			return nil, err
		}
		if err := binary.Read(r, binary.LittleEndian, &wind.NextChange); err != nil {
			return nil, err
		}
		if err := binary.Read(r, binary.LittleEndian, &wind.LastChange); err != nil {
			return nil, err
		}
		changedB, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		pendingB, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		wind.Changed = byteToBool(changedB)
		wind.Pending = byteToBool(pendingB)
		st.Wind = wind
		if ver >= 6 {
			nLinks, err := readStateCount(r, "construction links", 8)
			if err != nil {
				return nil, err
			}
			links := make([]BuilderLinkRecord, 0, nLinks)
			for i := uint32(0); i < nLinks; i++ {
				var b, p int32
				if err := binary.Read(r, binary.LittleEndian, &b); err != nil {
					return nil, err
				}
				if err := binary.Read(r, binary.LittleEndian, &p); err != nil {
					return nil, err
				}
				links = append(links, BuilderLinkRecord{Builder: b, Product: p})
			}
			sort.Slice(links, func(i, j int) bool {
				if links[i].Product != links[j].Product {
					return links[i].Product < links[j].Product
				}
				return links[i].Builder < links[j].Builder
			})
			st.Construction.BuilderLinks = links
		}
	}
	return st, nil
}

// validateAIGroups rejects malformed native group records before they can be
// applied to a live session. A retail manager group is a single-owner,
// single-membership vector: the direct writer removes a unit from its old
// record before appending it to the new record [R-P0-04]. A zero/null handle,
// duplicate handle, or duplicate manager player therefore cannot describe a
// recoverable StateV1. This is deliberately a structural check; Unit.Group
// ownership/coherence is checked by session.RestoreStateV1 once the forced
// unit slots are available.
func validateAIGroups(managers []AIManagerRecord) error {
	seenPlayers := make(map[uint8]struct{}, len(managers))
	seenHandles := make(map[int32]uint8)
	for _, m := range managers {
		if m.Player >= 10 {
			return fmt.Errorf("save: AI manager player %d out of range", m.Player)
		}
		if _, ok := seenPlayers[m.Player]; ok {
			return fmt.Errorf("save: duplicate AI manager player %d", m.Player)
		}
		seenPlayers[m.Player] = struct{}{}
		vectors := []struct {
			name string
			list []int32
		}{
			{"resource", m.Groups.Resource}, {"wave A", m.Groups.WaveA},
			{"regroup A", m.Groups.RegroupA}, {"construction", m.Groups.Construction},
			{"null", m.Groups.Null}, {"wave B", m.Groups.WaveB},
			{"regroup B", m.Groups.RegroupB}, {"explore", m.Groups.Explore},
			{"rally", m.Groups.Rally},
		}
		for _, v := range vectors {
			for _, h := range v.list {
				if h <= 0 {
					return fmt.Errorf("save: AI %s group contains null handle %d", v.name, h)
				}
				if prior, ok := seenHandles[h]; ok {
					return fmt.Errorf("save: duplicate AI group handle players=%d,%d handle=%d", prior, m.Player, h)
				}
				seenHandles[h] = m.Player
			}
		}
	}
	return nil
}

// WriteStateV1 writes the StateV1 box into the Nanolathe account [PLAN_14 C18].
// It canonically encodes every mutable authoritative service present in s,
// slot-indexed, plus both RNG states+draw counts and catalog/manifest hashes.
func WriteStateV1(b *Builder, s *StateV1) {
	if b == nil || s == nil {
		return
	}
	data := MarshalStateV1(s)
	ac := builderAccount(b, NanolatheAccount)
	ac.AppendBox(StateV1BoxName, 0, data)
}

// ReadStateV1 reads the StateV1 box from the Nanolathe account, validates
// version and hashes, and returns the decoded state [PLAN_14 C18].
// If the Nanolathe account or StateV1 box is missing, it returns ErrNoStateV1
// with an explicit diagnostic that metadata boxes remain readable but full
// continuation is unsupported [PLAN_14 C18] [GAP T25].
func ReadStateV1(bank *Bank, expectedCatalogHash, expectedManifestHash string) (*StateV1, error) {
	if bank == nil {
		return nil, ErrNoStateV1
	}
	ac, ok := bank.Account(NanolatheAccount)
	if !ok {
		return nil, ErrNoStateV1
	}
	data, ok := ac.BoxData(StateV1BoxName, 0)
	if !ok {
		return nil, ErrNoStateV1
	}
	return UnmarshalStateV1(data, expectedCatalogHash, expectedManifestHash)
}

// IsNativeSave reports whether the bank contains a Nanolathe StateV1 box
// (i.e., is a native save capable of full continuation) [PLAN_14 C18].
func IsNativeSave(bank *Bank) bool {
	if bank == nil {
		return false
	}
	ac, ok := bank.Account(NanolatheAccount)
	if !ok {
		return false
	}
	_, ok = ac.BoxData(StateV1BoxName, 0)
	return ok
}

// ---------------------------------------------------------------------------
// Non-transactional apply helpers (C14) [GAP T9].
// ---------------------------------------------------------------------------

// ApplyResult is returned by non-transactional loaders to convey that
// restoration was attempted and which subsystems succeeded, without implying a
// shadow/rollback. The loader returns success unconditionally after attempting
// restoration (C14) [GAP T9].
type ApplyResult struct {
	Summary     *Summary
	Camera      *Camera
	PlayersMeta *PlayersMeta
	GameTime    *clock.State
	Alliances   *[11]byte
	PlayerSlots []PlayerSlot
	StateV1     *StateV1
	Warnings    []string
	// Native indicates whether full continuation is possible (StateV1 present).
	Native bool
}

// ApplyBankNonTransactional mutates no shadow world; it reads live bank data
// and returns an ApplyResult with whatever could be decoded. Missing accounts
// are treated as empty and each subsystem's defaults govern the result, and
// the function returns success unconditionally (C14) [GAP T9]. The caller
// should check result.StateV1 == nil to surface the explicit unsupported
// diagnostic for retail saves without StateV1 [PLAN_14 C18].
func ApplyBankNonTransactional(bank *Bank) ApplyResult {
	var res ApplyResult
	if bank == nil {
		return res
	}
	if s, ok := ReadSummary(bank); ok {
		res.Summary = &s
	}
	if c, ok := ReadCamera(bank); ok {
		res.Camera = &c
	}
	if m, ok := ReadPlayersMeta(bank); ok {
		res.PlayersMeta = &m
	}
	if gt, ok := ReadGameTime(bank); ok {
		res.GameTime = gt
	}
	if al, ok := ReadAlliances(bank, 0); ok {
		res.Alliances = &al
	}
	res.PlayerSlots = ReadAllPlayerSlots(bank)
	if st, err := ReadStateV1(bank, "", ""); err == nil {
		res.StateV1 = st
		res.Native = true
	} else if errors.Is(err, ErrNoStateV1) {
		res.Warnings = append(res.Warnings, ErrNoStateV1.Error())
		res.Native = false
	} else {
		// Hash mismatches etc are explicit diagnostics; still non-transactional
		// — we keep what succeeded and surface the warning.
		res.Warnings = append(res.Warnings, err.Error())
		res.Native = false
	}
	res.Warnings = append(res.Warnings, bank.Warnings()...)
	return res
}

// ---------------------------------------------------------------------------
// RNG snapshot helpers (C15 / C18) [08 "Scheduler and random state in saves"]
// [01 §7.1] [01 §7.2].
// ---------------------------------------------------------------------------

// RNGSnapshot captures both RNG states+draw counts for C18.
type RNGSnapshot struct {
	SimState uint32
	SimDraws uint64
	CrtState uint32
	CrtDraws uint64
}

// CaptureRNG captures the two global RNG states and draw counts [01 §7.1]
// [01 §7.2] [PLAN_14 C18]. It reads rng.Global but does not seed it.
func CaptureRNG() RNGSnapshot {
	var snap RNGSnapshot
	if rng.Global.Sim != nil {
		snap.SimState = rng.Global.Sim.State
		snap.SimDraws = rng.Global.Sim.Draws()
	}
	if rng.Global.Crt != nil {
		snap.CrtState = rng.Global.Crt.State
		snap.CrtDraws = rng.Global.Crt.Draws()
	}
	return snap
}

// RestoreRNG restores both global RNG states and draw counts through published
// APIs [01 §7.1] [01 §7.2]. Draw counts are restored via published RestoreDraws [P0-I11].
func RestoreRNG(snap RNGSnapshot) {
	sim := rng.SimulationFromState(snap.SimState)
	sim.RestoreDraws(snap.SimDraws)
	crt := rng.CRTFromState(snap.CrtState)
	crt.RestoreDraws(snap.CrtDraws)
	rng.Global.Sim = &sim
	rng.Global.Crt = &crt
}

// ---------------------------------------------------------------------------
// Binary helpers (I13 exception: StateV1 bytes cross the save boundary).
// ---------------------------------------------------------------------------

func binaryWriteUint32(buf *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	buf.Write(b[:])
}

func binaryWriteUint64(buf *bytes.Buffer, v uint64) {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	buf.Write(b[:])
}

func binaryWriteInt32(buf *bytes.Buffer, v int32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], uint32(v))
	buf.Write(b[:])
}

func binaryWriteUint16(buf *bytes.Buffer, v uint16) {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], v)
	buf.Write(b[:])
}

func binaryWriteInt16(buf *bytes.Buffer, v int16) {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], uint16(v))
	buf.Write(b[:])
}

func binaryReadUint32(r *bytes.Reader) (uint32, error) {
	var v uint32
	err := binary.Read(r, binary.LittleEndian, &v)
	return v, err
}

func binaryReadInt32(r *bytes.Reader) (int32, error) {
	var v int32
	err := binary.Read(r, binary.LittleEndian, &v)
	return v, err
}

func binaryReadUint16(r *bytes.Reader) (uint16, error) {
	var v uint16
	err := binary.Read(r, binary.LittleEndian, &v)
	return v, err
}

func binaryReadInt16(r *bytes.Reader) (int16, error) {
	var v int16
	err := binary.Read(r, binary.LittleEndian, &v)
	return v, err
}

func boolToByte(b bool) byte {
	if b {
		return 1
	}
	return 0
}
func byteToBool(b byte) bool { return b != 0 }

func writeString(buf *bytes.Buffer, s string) {
	binaryWriteUint32(buf, uint32(len(s)))
	buf.WriteString(s)
}

func readString(r *bytes.Reader) (string, error) {
	var l uint32
	if err := binary.Read(r, binary.LittleEndian, &l); err != nil {
		return "", err
	}
	if l > 1<<20 { // sanity bound (1MiB) — C13 divergence: we bound offsets [PLAN_14 C13]
		return "", fmt.Errorf("save: string length %d exceeds bound", l)
	}
	if l == 0 {
		return "", nil
	}
	b := make([]byte, l)
	if _, err := io.ReadFull(r, b); err != nil {
		return "", err
	}
	return string(b), nil
}

// readStateCount reads a variable-length StateV1 collection count and rejects
// counts that cannot fit in the remaining payload before allocating. StateV1
// is a native continuation box, but its bytes still cross an untrusted save
// boundary; a corrupt count must produce an error rather than an OOM or a
// partially decoded state [PLAN_14 C18][I11]. minBytes is only a lower bound
// used for the pre-allocation check; each record's full parser still performs
// exact EOF checks as it consumes variable-length fields.
const maxStateCollectionEntries = 1 << 20

func readStateCount(r *bytes.Reader, label string, minBytes int) (uint32, error) {
	if minBytes < 1 {
		minBytes = 1
	}
	var n uint32
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return 0, err
	}
	if n > maxStateCollectionEntries {
		return 0, fmt.Errorf("save: StateV1 %s count %d exceeds bound", label, n)
	}
	if uint64(n)*uint64(minBytes) > uint64(r.Len()) {
		return 0, fmt.Errorf("save: StateV1 %s count %d exceeds remaining payload", label, n)
	}
	return n, nil
}
