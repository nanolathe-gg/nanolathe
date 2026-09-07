// Save contents (boxes) [08 "Save-file organization"].

package save

import (
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/economy"
)

// ---------------------------------------------------------------------------
// File naming and write policy (C14) [08 "File naming and write policy"].
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Summary account [08 "Save-file organization"] [08 "Summary"].
// ---------------------------------------------------------------------------

// Summary holds the established Summary writer fields in the order the writer
// emits them [08 "Summary"] [08 "Account inventory"].
//
// There are no fields beyond this list. The comment here previously said any
// further ones remained an unknown T25 residual; that was stale — doc 08's account
// inventory already gave the writer's items exhaustively, and WU-19-158
// re-censused the writer itself and found exactly these and no others
// (Established, bounded-negative).
type Summary struct {
	// Dynamic build keys (integer 0) [08 "Summary"].
	BuildDateKey string // e.g. "BUILD DATE:Aug 23 2026" — prefix BUILD DATE:
	BuildTimeKey string // e.g. "BUILD TIME:12:00:00" — prefix BUILD TIME:

	// MaxUnits is the configured unit-limit word the writer records — the
	// process-configured `[Preferences] UnitLimit` copy, never the battle's own
	// session limit [08 R-SESS-01 §9]. A read takes the item's low 16 bits.
	//
	// HasMaxUnits is the item's presence witness, and it is not redundant with a
	// nonzero MaxUnits: the restore step reads the word "only when present" and
	// applies no clamp, so a present zero and an absent item are two different
	// instructions [08 R-SESS-01 §9]. Collapsing them — which this type did
	// until the reader gained this field — makes a save that stores zero
	// indistinguishable from one that omits the item. ReadSummary sets it for a
	// present item whatever its value; WriteSummary emits the item
	// unconditionally, as retail's writer does, so a bank this package writes
	// always reads back present.
	MaxUnits    int32
	HasMaxUnits bool
	Campaign    string // [08 "Summary"]
	Mission     string // [08 "Summary"]
	MapName     string // Map [08 "Summary"]
	Difficulty  int32  // default 0 [08 "Summary"]
	Side        int32  // default 0 [08 "Summary"]
	Players     int32
	Gametype    int32 // 1 campaign, 2 multiplayer [08 "Summary"] [GAP T9]
	// Retail stores Thumbs as a bounded 25-byte string. Reset/application
	// semantics belong to the progression pass [08 R-SAVE-02 §3].
	Thumbs string

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

func boundedSummaryString(s string) string {
	if len(s) > 25 {
		return s[:25]
	}
	return s
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
	// The item is emitted unconditionally: retail's writer always emits
	// `maxunits`, and every producer in this build supplies the configured
	// unit-limit word [08 "Summary"] [08 R-SESS-01 §9]. HasMaxUnits is therefore
	// a read-side witness only — omitting the item here would write a file
	// retail never writes.
	ac.SetInt("maxunits", s.MaxUnits)
	ac.SetString("Campaign", s.Campaign)
	ac.SetString("Mission", s.Mission)
	ac.SetString("Map", s.MapName)
	ac.SetInt("Difficulty", s.Difficulty)
	ac.SetInt("Side", s.Side)
	ac.SetInt("Players", s.Players)
	ac.SetInt("Gametype", s.Gametype)
	ac.SetString("Thumbs", boundedSummaryString(s.Thumbs))
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
	// The bulk boxes are byte-for-byte pass-through here on purpose, not
	// because their layouts are unknown. The T25 marker that stood on this
	// line — "byte layouts beyond established lengths remain unknown" — was
	// stale: `[08 R-SAVE-02 §6]`–`§12` name every one of them, and
	// PLAN_14's own explicit-unknowns entry says the marker "should be retired
	// as they are implemented". `Radar Image` is a `u32` width, a `u32` height
	// and then `height` rows of `width` palette bytes [08 R-SAVE-02 §3]; the
	// caller supplies that header with the payload, and a short box yields no
	// image rather than an error. `Metal`, `PlayerFeatures` and `Mapping` are
	// exact-size plot maps owned by internal/world and internal/session
	// [08 R-SAVE-02 §12]; the `Units` family is [08 R-SAVE-02 §6]–§11. The
	// only spans still opaque are those [08 R-SAVE-02 §13] does not name.
}

// The account and box names this package writes and reads by name
// [08 "Account inventory"].
const (
	SummaryAccount    = "Summary"
	CameraAccount     = "Camera"
	PlayersAccount    = "Players"
	GameTimeBoxName   = "GameTime"
	AlliancesBoxName  = "Alliances"
	RadarImageBoxName = "Radar Image"
)

// ReadSummary reads the Summary account with preflight defaults [08 "Summary"].
// Missing/mistyped handling: maxunits low 16 bits else 0 with HasMaxUnits
// recording whether the item was there at all — the restore reads that word
// only when present [08 R-SESS-01 §9]; Difficulty/Side/Players default 0; the
// five multiplayer rule fields default 1 [08 "Summary"].
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
		s.HasMaxUnits = true
	} else {
		s.MaxUnits = 0
		s.HasMaxUnits = false
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
	if v, ok := ac.Str("Thumbs"); ok {
		s.Thumbs = boundedSummaryString(v)
	} else {
		s.Thumbs = ""
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

// Camera holds the established integer world-coordinate words [08 R-SAVE-02 §12].
type Camera struct {
	XPosition int32
	ZPosition int32
}

// WriteCamera writes the Camera account [08 "Account inventory"].
func WriteCamera(b *Builder, c Camera) {
	if b == nil {
		return
	}
	ac := builderAccount(b, CameraAccount)
	ac.SetInt("X Position", c.XPosition)
	ac.SetInt("Z Position", c.ZPosition)
}

// ReadCamera reads the Camera account [08 "Account inventory"].
func ReadCamera(bank *Bank) (Camera, bool) {
	ac, ok := bank.Account(CameraAccount)
	if !ok {
		return Camera{}, false
	}
	var c Camera
	if v, ok := ac.Int("X Position"); ok {
		c.XPosition = v
	}
	if v, ok := ac.Int("Z Position"); ok {
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
// Alliances box — one per Player%i account, exactly 11 bytes, with forced
// self-alliance 1 [08 "Player records"] [GAP T9] C16.
//
// The box is NOT an account-level item under `Players`. The writer census of
// WU-19-158 walks the ten player records and, for each active one, selects
// `Player%i` and emits the nineteen scalar items "followed by the 11-byte
// `Alliances` box, and nothing else"; the reader is symmetric [08 "Player
// records"]. Row *i* is therefore slot *i*'s own alliance row, and the
// runtime-row ownership question that stood open in the doc tail is answered
// by the account the box sits in.
//
// This corrects the earlier model, which put ONE box under the `Players`
// account. That model was not "right for slot 0": a retail bank has no
// `Players/Alliances` box at all, so the reader found nothing for any slot,
// and the writer emitted a box in an account whose retail reader never looks
// for one. It was inert in both directions rather than partially correct.
// ---------------------------------------------------------------------------

// WriteAlliances appends slot's 11-byte Alliances box to its `Player%i`
// account [08 "Player records"] [GAP T9] C16. The self byte is forced to 1 on
// read, not necessarily on write; it is forced here so the written row and the
// row a load produces are the same bytes.
func WriteAlliances(b *Builder, slot int, alliances [11]byte) {
	if b == nil || slot < 0 || slot >= 10 {
		return
	}
	// C16: forced self-alliance 1 [08 "Player records"] [GAP T9].
	alliances[slot] = 1
	ac := builderAccount(b, playerAccountName(slot))
	ac.AppendBox(AlliancesBoxName, 0, alliances[:])
}

// ReadAlliances reads slot's Alliances box from its `Player%i` account,
// requires exactly 11 bytes, and forces the self-alliance byte to 1
// [08 "Player records"] [GAP T9] C16. A box of any other size does not load —
// it is not an error, the runtime row simply keeps the values battle entry
// gave it. It returns the 11-byte payload and whether the box loaded.
func ReadAlliances(bank *Bank, slot int) ([11]byte, bool) {
	var out [11]byte
	if slot < 0 || slot >= 10 {
		return out, false
	}
	ac, ok := bank.Account(playerAccountName(slot))
	if !ok {
		return out, false
	}
	data, ok := ac.BoxData(AlliancesBoxName, 0)
	if !ok || len(data) != 11 { // exactly 11 bytes [08 "Player records"] [GAP T9] C16
		return out, false
	}
	copy(out[:], data)
	out[slot] = 1 // forced self-alliance [08 "Player records"] [GAP T9] C16
	return out, true
}

// ---------------------------------------------------------------------------
// Player%i field tables [08 "Player records"].
// ---------------------------------------------------------------------------

// PlayerSlot holds the Player%i scalar fields with wire types and defaults as
// transcribed verbatim with citations [08 "Player records"].
//
// The list is complete: doc 08 states the per-slot item list is "the full
// Player records table plus Logo and Side", and WU-19-158 censused the writer
// and the reader and found exactly these nineteen items and the 11-byte
// Alliances box, in this order, and nothing else (Established,
// bounded-negative). The T25 "unknown fields stay opaque" clause this comment
// used to carry named fields the account does not have.
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

	// Wire type double, runtime f32 narrowed. These are the two storage-bonus
	// OPERANDS the bonus setter writes (max(200, v), energy then metal), not
	// the derived capacities: capacity is zeroed and rebuilt from the unit sum
	// plus this bonus at every settlement pass, so it is never persisted
	// [08 "Player records"] [05 R-ECO-01 §4]. This used to carry Capacity,
	// which left a restored player with the flag set and a zero bonus — and
	// a capacity of nothing but its buildings' storage.
	PlayerEnergyStorage float32
	PlayerMetalStorage  float32

	// Wire type integer, runtime bit 0 of halfword — the storage-bonus enable
	// flag the setter sets [08 "Player records"]:
	AddPlayerStorage uint16 // 0/1

	// Wire type integer, runtime low signed 16 bits [08 "Player records"]:
	Kills  int16
	Losses int16
	// Commander-kill and commander-loss counters are NOT persisted: the
	// Player%i writer and reader carry no key for them, and the only place the
	// image spells "Commanders Killed"/"Commanders Lost" is the statistics
	// score board handed to the lobby and online callbacks, which no save
	// touches [08 "Player records"] [08 R-CAMP-01 §10]. Traced by WU-19-158;
	// the marker here previously said the question was open.

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
	// Logo and Side close the scalar list: the writer follows them only with
	// the 11-byte Alliances box [08 "Player records"] [08 R-SAVE-02 §12]. The
	// T25 marker that stood here reserved space for "network identity,
	// connection/alive state, sharing options" — none of which the Player%i
	// account carries in either direction (WU-19-158 census).

	// Alliances is this slot's own eleven-byte alliance row, emitted as the
	// account's last item and indexed by player slot [08 "Player records"]
	// [05 R-SHARE-01 §1]. HasAlliances distinguishes an absent or mis-sized
	// box — which retail does not load, leaving the runtime row as battle
	// entry built it — from a loaded all-zero row.
	Alliances    [11]byte
	HasAlliances bool
}

// PlayerSlotFromEconomy projects the established scalar/statistics fields into
// the typed bank record. It intentionally omits live buckets and the derived
// capacities — the account carries the bonus operands, and capacity is rebuilt
// at the slot's first settlement [05 R-ECO-01 §4]
// [05 "Saving economy, construction, and features"].
func PlayerSlotFromEconomy(index int, p economy.Player) PlayerSlot {
	return PlayerSlot{
		Index:  index,
		Energy: p.Stock[economy.Energy], Metal: p.Stock[economy.Metal],
		TotalEnergyProduced: p.TotalProduced[economy.Energy], TotalMetalProduced: p.TotalProduced[economy.Metal],
		TotalEnergyConsumed: p.TotalConsumed[economy.Energy], TotalMetalConsumed: p.TotalConsumed[economy.Metal],
		EnergyWasted: p.Waste[economy.Energy], MetalWasted: p.Waste[economy.Metal],
		PlayerEnergyStorage: p.StorageBonus[economy.Energy], PlayerMetalStorage: p.StorageBonus[economy.Metal],
		AddPlayerStorage: boolWord(p.StorageBonusEnabled),
		Kills:            p.Kills, Losses: p.Losses,
		UpdateTime: int32(p.UpdateTime), WinLoseTime: int32(p.WinLoseTime), DisplayTimer: int32(p.DisplayTimer),
		Controller: p.ControllerState, Logo: p.Logo, Side: p.Side,
		// Row i of the bank is slot i's own alliance row, taken from the
		// runtime table that owns it — row A, the alliance predicate's row
		// [08 R-SAVE-02 §15] [08 "Player records"] [05 R-SHARE-01 §1].
		Alliances: p.AllianceRow(index), HasAlliances: true,
	}
}

// ApplyToEconomy restores the scalar/statistics fields that PlayerSlot owns,
// then the account's own alliance row when the box loaded. Runtime bucket
// carry stays with its existing account reader, preserving the retail
// partial-load boundaries [08 "Player records"].
//
// The six cumulative doubles it restores are the running totals, not rates.
// The per-pass production and consumption pair — the four floats the HUD
// resource bar samples and the settled pair the planner scores with — has no
// item in this account and is therefore not restorable: the writer/reader
// census is closed at the nineteen scalars above plus `Alliances` (Established,
// bounded-negative; the WU-19-158 addendum under [08 "Player records"]).
// Retail is in the same position, and by a wider margin — its world rebuild
// zeroes every slot's "stocks, incomes, expenditures" before the restoration
// dispatcher runs [08 R-ENTRY-01 §3 step 24] — and refills the pair at the
// slot's next settlement pass [05 R-ECO-01 §6]. See the restore site in
// internal/session/retail_restore_core.go.
func (p PlayerSlot) ApplyToEconomy(dst *economy.Player) {
	if dst == nil {
		return
	}
	// The row belongs to this account's slot and the self column is forced to
	// 1 [08 "Player records"]. A missing or mis-sized box leaves the runtime
	// row untouched, which is retail's "other bytes keep their initialized
	// values".
	if p.HasAlliances {
		dst.SetAllianceRow(p.Index, p.Alliances)
	}
	dst.Stock[economy.Energy], dst.Stock[economy.Metal] = p.Energy, p.Metal
	dst.TotalProduced[economy.Energy], dst.TotalProduced[economy.Metal] = p.TotalEnergyProduced, p.TotalMetalProduced
	dst.TotalConsumed[economy.Energy], dst.TotalConsumed[economy.Metal] = p.TotalEnergyConsumed, p.TotalMetalConsumed
	dst.Waste[economy.Energy], dst.Waste[economy.Metal] = p.EnergyWasted, p.MetalWasted
	// The two storage keys are the bonus operands, restored into the operand
	// fields; capacity is left for the first settlement pass to rebuild, as
	// retail's world-rebuild reset zeroes it before the reader runs and the
	// reader never writes it [08 "Player records"] [05 R-ECO-01 §4]
	// [08 R-ENTRY-01 §3 step 24].
	dst.StorageBonus[economy.Energy], dst.StorageBonus[economy.Metal] = p.PlayerEnergyStorage, p.PlayerMetalStorage
	dst.StorageBonusEnabled = p.AddPlayerStorage&1 != 0
	dst.Kills, dst.Losses = p.Kills, p.Losses
	dst.UpdateTime, dst.WinLoseTime, dst.DisplayTimer = uint32(p.UpdateTime), uint32(p.WinLoseTime), uint32(p.DisplayTimer)
	dst.ControllerState, dst.Logo, dst.Side = p.Controller, p.Logo, p.Side
}

func boolWord(v bool) uint16 {
	if v {
		return 1
	}
	return 0
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
	// The account closes with the 11-byte Alliances box, after Side and
	// nothing after it [08 "Player records"].
	if p.HasAlliances {
		WriteAlliances(b, p.Index, p.Alliances)
	}
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
	p.Alliances, p.HasAlliances = ReadAlliances(bank, index)
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
