package session

import (
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
)

// errStartPositionMissing is the skirmish stamp helper's fatal diagnostic,
// reproduced verbatim: `Error: Could not find start position number %i on the
// map!`. The number retail formats is the STORED number — zero-based, one less
// than the authored `StartPos<n>` label — so slot 0 taking `StartPos1` reports
// number 0 [08 R-ENTRY-01 §5] step 4.
//
// On the kind-2 (skirmish) path a `StartPos` miss ends the process: retail
// raises this through the modal-fatal helper (message box, then exit code 1),
// the same channel the spawner's `Player number %d invalid for unit %s` uses
// [08 "Unit creation and InitialMission timing"][08 R-TRIG-01 §9]. This build
// carries retail's verbatim fatal text as a battle-entry error, exactly like
// the six mission-file diagnostics of internal/mission; the shell shows it in
// the retail message window and returns to the screen the start was launched
// from, which is this build's shape for a fatal that retail answers with a
// modal and an exit.
func errStartPositionMissing(stored int) error {
	//lint:ignore ST1005 retail text: reproduced verbatim [08 R-ENTRY-01 §5].
	return fmt.Errorf("Error: Could not find start position number %d on the map!", stored)
}

// C8 defaults per [02 §3], [GAP T14] and [08 "Skirmish configuration"].
const (
	SkirmishMinPlayers            = 2  // [GAP T14] validated 2..10
	SkirmishMaxPlayers            = 10 // [GAP T14]
	SkirmishDefaultPlayers        = 4  // [02 §3] missing NumSkirmishPlayers installs default 4
	SkirmishDefaultMetal          = 1000
	SkirmishDefaultEnergy         = 1000
	SkirmishDefaultAllyGroup      = 5  // [GAP T14]
	SkirmishDefaultController     = 0  // [GAP T14] human
	SkirmishDefaultDifficulty     = 1  // Medium [02 §3] [08 "Skirmish configuration"]
	SkirmishDefaultLocation       = 1  // pre-determined start positions [08 "Skirmish configuration"]
	SkirmishDefaultCommanderDeath = 1  // commander death ends the game [08 "Skirmish configuration"]
	SkirmishDefaultMapping        = 1  // terrain is blacked out until explored [08 "Skirmish configuration"]
	SkirmishDefaultLineOfSight    = 1  // LOS enabled [08 "Skirmish configuration"]
	SkirmishDefaultLOSType        = 1  // terrain elevations affect LOS [08 "Skirmish configuration"]
	SkirmishNickCap               = 17 // [02 §3] 17-byte buffers
	// neutralSideIndex is the side ordinal that names no playable side. Every
	// per-slot participation gate in the retail image tests it: start-position
	// eligibility [08 R-ENTRY-01 §5], the two live-player counters
	// [08 R-SESS-01 §1], and the post-battle score-board row condition
	// [08 R-CAMP-01 §7].
	neutralSideIndex    = 10
	skirmishNickPayload = 16 // usable chars (17 includes NUL) [02 §3]
)

// The per-player unit limit a skirmish carries into battle entry. Retail reads
// it once at start-up from the profile file's `[Preferences]` `UnitLimit` — a
// profile value, not a registry value — with a missing-value default of 250,
// clamps it into 20..500, and keeps it as a sixteen-bit configured limit
// [02 "Unit limit"][08 R-SKIR-01 §6]. Skirmish battle entry copies that
// configured limit over the session's unit-limit word, so a skirmish never
// sees a map's `maxunits`; only a campaign keeps the OTA value
// [08 R-SKIR-01 §6][05 R-SHARE-01 §7].
//
// The 20..500 clamp is the profile read's alone and lives with it, in
// internal/settings (`MinUnitLimit`, `MaxUnitLimit`); this package sees the
// word only after that read and copies it verbatim.
const SkirmishDefaultUnitLimit = 250 // missing `UnitLimit` [08 R-SKIR-01 §6]

// unitLimitOrDefault is the copy every stage after the start-up read makes of
// the configured unit limit: verbatim, no clamp. Retail clamps the word once,
// when the profile file is read; skirmish battle entry then copies the
// configured word into the session limit as it stands, and the save restore
// that overwrites the configured word from `Summary.maxunits` applies no clamp
// either, so a restored out-of-range value sizes the next battle's pool at
// exactly that value [08 R-SESS-01 §9][08 R-SKIR-01 §6]. Only zero is
// rewritten, and only because it is this build's missing-value sentinel for a
// setup record composed without a profile (a fixture, the displayless
// runner): the legal range starts at 20, so no stored choice collides with
// it, and a retail-written save never carries 0 [08 R-SESS-01 §9].
//
// Correction: the two normalization entry points and the session copy used to
// re-apply the start-up clamp here, so a restored value outside 20..500 was
// pulled to the bound on its way into the second battle.
func unitLimitOrDefault(v int) int {
	if v == 0 {
		return SkirmishDefaultUnitLimit
	}
	return v
}

// CommanderDeathRule is the closed retail rule vocabulary.  The setup field
// remains an int for save/menu source assignment; every gameplay consumer
// passes it through CommanderDeathMode, so values outside this vocabulary can
// never select a fourth behavior [08 R-SKIR-01 §3].
type CommanderDeathRule int

const (
	CommanderDeathContinues  CommanderDeathRule = iota // 0: keep the owner's units
	CommanderDeathEnds                                 // 1: owner elimination
	CommanderDeathDeathmatch                           // 2: local commander respawn
)

// CommanderDeathMode accepts only the three values present in retail.  An
// invalid setup word is not a new mode; callers fail closed to the established
// game-ends path [08 R-SKIR-01 §3].
func CommanderDeathMode(v int) CommanderDeathRule {
	switch CommanderDeathRule(v) {
	case CommanderDeathContinues, CommanderDeathEnds, CommanderDeathDeathmatch:
		return CommanderDeathRule(v)
	default:
		return CommanderDeathEnds
	}
}

// Shell controller states distinguished at lobby boundary before conversion [08 "Skirmish configuration"].
const (
	ShellControllerOpen     = 0 // open/inactive slot [08 "Skirmish configuration"]
	ShellControllerHuman    = 1 // Player/human [08 "Skirmish configuration"]
	ShellControllerComputer = 2 // Computer [08 "Skirmish configuration"]
	ShellControllerObserver = 3 // observer/spectator [GAP T14]
)

// Session controller values stored in SkirmishConfig.Players[].Controller after conversion.
const (
	SkirmishControllerHuman    = 0 // human local [GAP T14]
	SkirmishControllerComputer = 1 // computer AI [GAP T14]
	SkirmishControllerObserver = 3 // observer [GAP T14] (distinct from controller value 2 used as computer)
)

// IsHuman, IsObserver and IsComputer are the exact controller predicates.
//
// Composition used to ask "is the controller nonzero?" to mean "is this a
// computer player". Observer is a distinct nonzero controller, so an observer
// slot was given an AI manager, units, economy actions and a share of result
// ownership. Computer stays "neither human nor observer" so controller value
// controller value 2 keeps being treated as a computer [GAP T14].
func (p SkirmishPlayer) IsHuman() bool { return p.Controller == SkirmishControllerHuman }

func (p SkirmishPlayer) IsObserver() bool { return p.Controller == SkirmishControllerObserver }

func (p SkirmishPlayer) IsComputer() bool { return !p.IsHuman() && !p.IsObserver() }

// SkirmishPlayer is per-slot skirmish state per [GAP T14].
// Absent per-slot values install defaults: controller 0, ally group 5,
// metal and energy 1000, color the slot index, side slot&1, nicknames
// truncated to 17-byte buffer.
type SkirmishPlayer struct {
	Nickname   string
	Controller int // 0 human, non-zero computer [GAP T14]; economy mapping uses 1/2
	Side       int // side ordinal; absent defaults to slot &1 [02 §3]
	Color      int // slot index [GAP T14]
	AllyGroup  int // 5 [GAP T14]
	Metal      int // Player%dMetal default 1000 [GAP T14]
	Energy     int // Player%dEnergy default 1000 [GAP T14]
}

// SkirmishConfig is the skirmish setup discriminant per [08 "Skirmish configuration"].
// MapName is the --map selection (basename without extension).
// NumPlayers is NumSkirmishPlayers validated 2..10 [GAP T14] but retail
// validation is a compiled no-op (both branches store raw) [P0-05].
// Players holds per-slot controller/side/color/ally/metal/energy/nickname state.
// Location selects start-position assignment: 0 = randomized via CRT shuffle
// [P0-04], !=0 = identity mapping [P0-04].
type SkirmishConfig struct {
	MapName        string
	NumPlayers     int
	Players        [10]SkirmishPlayer
	Difficulty     int // 0 easy, 1 medium, 2 hard [08 "Skirmish configuration"]
	Location       int // 0 randomized (CRT Fisher-Yates), !=0 identity [P0-04]
	CommanderDeath int // CommanderDeathRule values 0/1/2 [08 R-SKIR-01 §3]
	Mapping        int // 0 all terrain visible, 1 blacked out until explored [08 "Skirmish configuration"]
	LineOfSight    int // 0 disables LOS, 1 enables it [08 "Skirmish configuration"]
	LOSType        int // 0 elevations ignored, 1 elevations affect LOS [08 "Skirmish configuration"]
	// UnitLimit is the configured per-player unit limit skirmish battle entry
	// copies over the session's unit-limit word [08 R-SKIR-01 §6]. It sizes
	// the unit pool — `limit × 10 + 1` records, exactly `limit` per slot
	// [05 R-SHARE-01 §7] — and is read again by the AI's half-capacity term
	// [08 R-AI-01 §13]. Zero means the value was absent; ApplyDefaults and
	// Normalize install the default and otherwise carry the value verbatim —
	// the 20..500 clamp is applied once, when the profile file is read, and a
	// restored save may legitimately carry a value outside it into the next
	// battle [08 R-SESS-01 §9]. No skirmish gadget edits it — it comes from
	// the profile file, not the lobby screen [08 R-SKIR-01 §6].
	UnitLimit int

	// Explicit battle RNG seeds [R-CORE-02] DET-01. Retail derives the sim
	// seed from the QPC sum XOR a fixed constant (forced odd) and the CRT seed
	// from the time-of-day helper, both AT BATTLE ENTRY, wiping every
	// pre-battle draw. Nanolathe takes explicit seeds instead; the bootstrap
	// seeds both streams fresh with these before any battle setup draw.
	RNGSimSeed uint32
	RNGCrtSeed uint32

	// rulesDefaultsApplied distinguishes a zero-value config (missing registry
	// values) from an explicit Easy/randomized/off choice. It is deliberately
	// private: callers use ApplyDefaults once, then may cycle the public values.
	rulesDefaultsApplied bool
}

// ApplyDefaults fills absent per-slot values with retail defaults per [GAP T14] [02 §3].
// It is idempotent and mutates the receiver. Missing NumPlayers installs default 4
// per [02 §3]. Truncation is by bytes to match buffer semantics.
func (c *SkirmishConfig) ApplyDefaults() {
	if c.NumPlayers == 0 {
		c.NumPlayers = SkirmishDefaultPlayers
	}
	if !c.rulesDefaultsApplied {
		// The six scalar preferences are all allowed to be zero after the menu
		// changes them, so apply the retail missing-value defaults only once.
		c.Difficulty = SkirmishDefaultDifficulty
		c.Location = SkirmishDefaultLocation
		// Preserve the explicit Deathmatch value when a caller supplies it
		// before applying the remaining missing registry defaults [08
		// R-SKIR-01 §3].  Zero remains the missing-value sentinel here;
		// callers selecting value 0 should set it after ApplyDefaults.
		if c.CommanderDeath != int(CommanderDeathDeathmatch) {
			c.CommanderDeath = SkirmishDefaultCommanderDeath
		}
		c.Mapping = SkirmishDefaultMapping
		c.LineOfSight = SkirmishDefaultLineOfSight
		c.LOSType = SkirmishDefaultLOSType
		// The ally group belongs with the scalars above, not with the per-slot
		// rewrites below: its range is `0..4` with `5` the unassigned sentinel
		// [08 R-SKIR-01 §2][08 R-SKIR-01 §1] "The setup record", so `0` is a
		// real group a player can pick — the lobby's Allies gadget cycles
		// 0..5 — and only an ABSENT `Player%dAllyGroup` takes the miss default
		// 5 [08 R-SKIR-01 §1] "Registry mirror". Every row is defaulted here,
		// not just the first NumPlayers, so raising the row count later cannot
		// reintroduce a bare zero for a row the settings block never described.
		//
		// **Correction.** This rewrote every stored `0` to the sentinel on every
		// call. internal/settings emits all ten rows with no `omitempty`, so a
		// stored `allyGroup: 0` is a choice and not an absent value; the second
		// ApplyDefaults inside skirmishConfigForStart then silently promoted the
		// player's group 0 to "allied with nobody".
		for i := range c.Players {
			if c.Players[i].AllyGroup == 0 {
				c.Players[i].AllyGroup = SkirmishDefaultAllyGroup
			}
		}
		c.rulesDefaultsApplied = true
	}
	// The unit limit sits outside the rules-defaults guard on purpose: unlike
	// the six scalars above, zero is not a choice a player can make — the
	// legal range is 20..500 — so it is the missing-value sentinel on every
	// call. The value itself is carried verbatim: the 20..500 clamp is the
	// profile read's, not battle entry's [08 R-SKIR-01 §6][08 R-SESS-01 §9].
	c.UnitLimit = unitLimitOrDefault(c.UnitLimit)
	c.MapName = strings.TrimSpace(c.MapName)
	n := c.NumPlayers
	if n < 0 {
		n = 0
	}
	if n > 10 {
		n = 10
	}
	for i := 0; i < n; i++ {
		p := &c.Players[i]
		if p.Metal == 0 {
			p.Metal = SkirmishDefaultMetal
		}
		if p.Energy == 0 {
			p.Energy = SkirmishDefaultEnergy
		}
		// Colour = slot index [GAP T14]; missing (0 for non-zero slot) installs slot.
		if i != 0 && p.Color == 0 {
			p.Color = i
		}
		// Side = slot &1 [02 §3]; missing for odd slots installs 1.
		if p.Side == 0 && (i&1) == 1 {
			p.Side = 1
		}
		if len(p.Nickname) > skirmishNickPayload {
			// 17-byte buffer includes NUL, so truncate to 16 bytes [02 §3].
			p.Nickname = p.Nickname[:skirmishNickPayload]
		}
	}
	// Truncate nicknames for inactive slots as well, in case they carry data.
	for i := 0; i < 10; i++ {
		if len(c.Players[i].Nickname) > skirmishNickPayload {
			c.Players[i].Nickname = c.Players[i].Nickname[:skirmishNickPayload]
		}
	}
}

// playerRecordByte narrows a setup row's side or colour ordinal to the low byte
// the player record carries [08 "Player records"]. Both are small ordinals; the
// guard exists so a hand-built configuration cannot write a negative value into
// an unsigned field.
func playerRecordByte(v int) uint8 {
	if v < 0 || v > 255 {
		return 0
	}
	return uint8(v)
}

// skirmishSlotName is the name the row-to-player conversion gives a registered
// skirmish slot: `Player` for a human, and for a computer the side literal
// `Arm` when its side is 0 and `Core` otherwise [08 R-SKIR-01 §2]. The literals
// are the conversion's, not the side-data table's names, and the setup row's
// nickname does not reach the record.
func skirmishSlotName(controller uint8, side int) string {
	if controller != 2 {
		return "Player"
	}
	if side == 0 {
		return "Arm"
	}
	return "Core"
}

// Validate checks NumPlayers 2..10 and non-empty map per [GAP T14] C8.
// Retail validation stores the supplied player count unchanged in this path;
// the player-count range branch is a compiled no-op [P0-05]. We preserve that as no-op for NumPlayers range
// (only map emptiness is an error for load). [P0-05]
func (c SkirmishConfig) Validate() error {
	if strings.TrimSpace(c.MapName) == "" {
		return fmt.Errorf("session: empty skirmish map [08 \"Skirmish configuration\"]")
	}
	// NumSkirmishPlayers range check is intentionally no-op [P0-05].
	return nil
}

// Normalize is the one canonical setup normalization path used by menu and direct
// battle entry [08 "Skirmish configuration"] [GAP T14].
// It trims map name, defaults missing NumPlayers to 4 only when zero, clamps 0..10,
// clears inactive rows 2..9 beyond NumPlayers, fills per-slot defaults for active
// rows, and validates at least one hostile alliance (all live ally groups equal
// is an error) [08 "Skirmish configuration"]. The production constructor applies
// the separate human/computer presence check after normalization.
// It is idempotent and must be called before session composition.
func (c *SkirmishConfig) Normalize() error {
	c.MapName = strings.TrimSpace(c.MapName)
	if c.MapName == "" {
		return fmt.Errorf("session: empty skirmish map [08 \"Skirmish configuration\"]")
	}
	if c.NumPlayers == 0 {
		c.NumPlayers = SkirmishDefaultPlayers
	}
	n := c.NumPlayers
	if n < 0 {
		n = 0
	}
	if n > 10 {
		n = 10
	}
	c.NumPlayers = n
	// The configured unit limit, verbatim; zero is the missing-value
	// sentinel, never a choice, and the 20..500 clamp is the profile read's
	// alone [08 R-SKIR-01 §6][08 R-SESS-01 §9].
	c.UnitLimit = unitLimitOrDefault(c.UnitLimit)
	// Clear inactive rows beyond NumPlayers to ensure inactive cannot affect result [GAP T14].
	for i := n; i < 10; i++ {
		c.Players[i] = SkirmishPlayer{}
	}
	if !c.rulesDefaultsApplied {
		c.Difficulty = SkirmishDefaultDifficulty
		c.Location = SkirmishDefaultLocation
		if c.CommanderDeath != int(CommanderDeathDeathmatch) {
			c.CommanderDeath = SkirmishDefaultCommanderDeath
		}
		c.Mapping = SkirmishDefaultMapping
		c.LineOfSight = SkirmishDefaultLineOfSight
		c.LOSType = SkirmishDefaultLOSType
		// Ally group 0 is a real group; only an absent value takes the
		// unassigned sentinel 5, and only on the first application. See the
		// same block in ApplyDefaults [08 R-SKIR-01 §1][08 R-SKIR-01 §2]. Rows
		// past NumPlayers were just cleared on purpose above, so this loop
		// stops at n rather than covering all ten.
		for i := 0; i < n; i++ {
			if c.Players[i].AllyGroup == 0 {
				c.Players[i].AllyGroup = SkirmishDefaultAllyGroup
			}
		}
		c.rulesDefaultsApplied = true
	}
	for i := 0; i < n; i++ {
		p := &c.Players[i]
		if p.Metal == 0 {
			p.Metal = SkirmishDefaultMetal
		}
		if p.Energy == 0 {
			p.Energy = SkirmishDefaultEnergy
		}
		if i != 0 && p.Color == 0 {
			p.Color = i
		}
		if p.Side == 0 && (i&1) == 1 {
			p.Side = 1
		}
		if len(p.Nickname) > skirmishNickPayload {
			p.Nickname = p.Nickname[:skirmishNickPayload]
		}
	}
	for i := 0; i < 10; i++ {
		if len(c.Players[i].Nickname) > skirmishNickPayload {
			c.Players[i].Nickname = c.Players[i].Nickname[:skirmishNickPayload]
		}
	}
	// Validate skirmish start per [08 "Skirmish configuration"]:
	// The lobby's human/computer presence check is applied by the production
	// constructor after normalization; this method also serves low-level config
	// editing where controller rows may still be incomplete.
	// Hostile alliance: at least one pair with different ally group, with sentinel
	// exception that all groups 5 (unassigned) is allowed per retail [08 "Skirmish configuration"].
	if n >= 2 {
		hostile := false
		base := c.Players[0].AllyGroup
		for i := 1; i < n; i++ {
			if c.Players[i].AllyGroup != base {
				hostile = true
				break
			}
		}
		allSentinel := true
		for i := 0; i < n; i++ {
			if c.Players[i].AllyGroup != SkirmishDefaultAllyGroup {
				allSentinel = false
				break
			}
		}
		if !hostile && !allSentinel {
			return fmt.Errorf("session: skirmish requires at least one hostile alliance (all ally groups %d) [08 \"Skirmish configuration\"]", base)
		}
	}
	return nil
}

// DirectSkirmishConfig returns the canonical direct 1v1 config [08 "Skirmish configuration"] [GAP T14].
// It explicitly sets NumPlayers=2 and clears rows 2..9 inactive, then normalizes.
// It does not rely on ApplyDefaults default 4 remaining.
func DirectSkirmishConfig(mapName string) SkirmishConfig {
	cfg := SkirmishConfig{MapName: mapName}
	cfg.NumPlayers = 2
	// Clear rows 2..9 before defaults to ensure inactive cannot affect result.
	for i := 2; i < 10; i++ {
		cfg.Players[i] = SkirmishPlayer{}
	}
	cfg.Players[0].Controller = SkirmishControllerHuman
	cfg.Players[1].Controller = SkirmishControllerComputer
	// Distinct ally groups to ensure hostile alliance [08 "Skirmish configuration"].
	// Human gets 2 (matching retail ensureRetail's first human ally 2), computer gets 5 (default sentinel distinct) for byte-equivalence with menu path.
	cfg.Players[0].AllyGroup = 2
	cfg.Players[1].AllyGroup = 5
	cfg.Players[0].Side = 0
	cfg.Players[1].Side = 1
	cfg.Players[0].Color = 0
	cfg.Players[1].Color = 1
	cfg.Players[0].Metal = SkirmishDefaultMetal
	cfg.Players[0].Energy = SkirmishDefaultEnergy
	cfg.Players[1].Metal = SkirmishDefaultMetal
	cfg.Players[1].Energy = SkirmishDefaultEnergy
	_ = cfg.Normalize()
	return cfg
}

// LocalOwnerForConfig derives LocalOwner from the configured human row, not zero default [08 "Skirmish configuration"].
func LocalOwnerForConfig(cfg SkirmishConfig) int {
	n := cfg.NumPlayers
	if n < 0 {
		n = 0
	}
	if n > 10 {
		n = 10
	}
	for i := 0; i < n; i++ {
		if cfg.Players[i].Controller == SkirmishControllerHuman {
			return i
		}
	}
	return 0
}

// skirmishPlayersAllied is the battle-entry alliance predicate. The frontend
// has already compacted open rows out of cfg, so every index below NumPlayers
// is live at this boundary. Group 5 is unassigned, not a team: it allies no
// two distinct players even when both rows carry 5 [08 R-SKIR-01 §2].
func skirmishPlayersAllied(cfg SkirmishConfig, a, b int) bool {
	if a == b {
		return true
	}
	group := cfg.Players[a].AllyGroup
	return group != SkirmishDefaultAllyGroup && group == cfg.Players[b].AllyGroup
}

// NormalizedBytes returns a deterministic byte representation for equivalence checks.
// It encodes NumPlayers and all player rows in a stable order.
func (c SkirmishConfig) NormalizedBytes() []byte {
	// Simple deterministic encoding: map name + numPlayers + each player's fields.
	var b []byte
	b = append(b, []byte(c.MapName)...)
	b = append(b, byte(c.NumPlayers), byte(c.Difficulty), byte(c.Location), byte(c.CommanderDeath), byte(c.Mapping), byte(c.LineOfSight), byte(c.LOSType))
	for i := 0; i < 10; i++ {
		p := c.Players[i]
		b = append(b, byte(p.Controller), byte(p.Side), byte(p.Color), byte(p.AllyGroup))
		// Metal/Energy as 2-byte little endian (clamped to 0..65535)
		b = append(b, byte(p.Metal), byte(p.Metal>>8), byte(p.Energy), byte(p.Energy>>8))
		b = append(b, []byte(p.Nickname)...)
		b = append(b, 0)
	}
	// The unit limit is part of the setup two configs must agree on: it sizes
	// the pool [05 R-SHARE-01 §7]. Two bytes, little endian, appended after
	// the rows so the existing prefix keeps its meaning.
	b = append(b, byte(c.UnitLimit), byte(c.UnitLimit>>8))
	return b
}

// NewSkirmishWithFS is the strict production constructor. It never fabricates
// an empty catalog, nil terrain, or invented commander. Missing retail content
// aborts with a diagnostic.
// [02 §5][03 §2.2][P0-16]
func NewSkirmishWithFS(fs vfs.FSOps, cat *content.Catalog, cfg SkirmishConfig) (*Session, error) {
	return NewSkirmishWithProgress(fs, cat, cfg, nil)
}

// The session's own load families, reported after the catalog's. They are what
// battle entry does once the immutable catalog exists.
const (
	FamilyTerrain   = "terrain"
	FamilyUnitWorld = "unitworld"
	FamilyPlacement = "placement"
	FamilyScripts   = "scripts"
)

// NewSkirmishWithProgress is NewSkirmishWithFS with a load observer. The
// observer sees the catalog's families first and then this constructor's own,
// so a caller painting the retail loading screen can drive it from one stream.
// A nil observer makes this exactly NewSkirmishWithFS.
func NewSkirmishWithProgress(fs vfs.FSOps, cat *content.Catalog, cfg SkirmishConfig, report content.Progress) (*Session, error) {
	if err := cfg.Normalize(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := validateSkirmishLobby(cfg); err != nil {
		return nil, err
	}
	// DET-01 [R-CORE-02]: battle bootstrap seeds both streams fresh BEFORE any
	// battle setup draw (skirmish slot shuffle, commander placement), wiping
	// every pre-battle draw from the streams' state. The session is the sole
	// RNG authority from here on; production composition supplies this explicit
	// pair, and no package-global stream or implicit seed source is consulted.
	if fs == nil {
		return nil, fmt.Errorf("session: nil filesystem for skirmish battle [02 §5]")
	}
	// 1. mount/receive VFS and compile one immutable catalog [02 §5]
	cat, err := strictCatalogWithProgress(fs, cat, report)
	if err != nil {
		return nil, err
	}
	// 2. select mission/schema [08 "Mission type dispatch"]
	m, err := mission.LoadWithType(fs, mission.TypeSkirmish, cfg.MapName, 0, cfg.NumPlayers, nil)
	if err != nil {
		return nil, fmt.Errorf("session: skirmish map %q: %w", cfg.MapName, err)
	}
	// 3. load terrain and apply selected schema including surface metal [03 §2.2][05]
	terrain, err := loadTerrainStrict(fs, cat, m)
	if err != nil {
		return nil, err
	}
	report.Report(FamilyTerrain, 100)
	// 4. create retail sliced unit pool [P0-16]. This constructor only ever
	// builds a skirmish session (session kind 2), whose comparator path
	// always orders by slot; the peer-identity sort key is consulted only
	// in session kind 3 (multiplayer, never built by this engine)
	// [08 R-SESS-01 §7]. Pass the skirmish kind explicitly rather than
	// mission.Type — a different, file-loading discriminant — and drop the
	// sort-key plumbing entirely: [08 R-SESS-01 §7 "Consequence for
	// single-player"] establishes that an engine which never builds a
	// kind-3 session needs none.
	//
	// The slice is `limit` records per slot, `limit × 10 + 1` in all
	// [05 R-SHARE-01 §7]. A skirmish's limit is the configured
	// `[Preferences] UnitLimit`, which battle entry copies over the session
	// word — a skirmish never uses the map's `maxunits` [08 R-SKIR-01 §6].
	// Normalize above applied the missing-value default and the 20..500 clamp.
	unitsWorld, err := newBattleSlicedWorldWithCOBSized(cat, fs, sessionKindSkirmish, [pool.PlayerCount]uint32{}, cfg.UnitLimit)
	if err != nil {
		return nil, err
	}
	report.Report(FamilyUnitWorld, 100)
	// Strict: ensure side commanders exist; do not invent armcom [P0-I01]
	nPlayersCheck := cfg.NumPlayers
	if nPlayersCheck < 0 {
		nPlayersCheck = 0
	}
	if nPlayersCheck > 10 {
		nPlayersCheck = 10
	}
	for i := 0; i < nPlayersCheck; i++ {
		sideIdx := cfg.Players[i].Side
		if _, err := skirmishCommander(cat, sideIdx, i); err != nil {
			return nil, err
		}
	}
	// Derive LocalOwner from configured human row, not zero default [08 "Skirmish configuration"].
	localOwner := LocalOwnerForConfig(cfg)
	enemyOwner := 0
	for i := 0; i < cfg.NumPlayers && i < 10; i++ {
		if i == localOwner {
			continue
		}
		if cfg.Players[i].IsComputer() && !skirmishPlayersAllied(cfg, i, localOwner) {
			enemyOwner = i
			break
		}
	}
	if enemyOwner == 0 && cfg.NumPlayers > 1 {
		for i := 0; i < cfg.NumPlayers && i < 10; i++ {
			if i != localOwner && cfg.Players[i].IsComputer() {
				enemyOwner = i
				break
			}
		}
	}
	s := &Session{
		Catalog:    cat,
		World:      terrain,
		Mission:    m,
		Skirmish:   cfg,
		Clock:      &clock.State{Requested: 10, Active: 10},
		Snapshot:   frame.NewBuffer(),
		Units:      unitsWorld,
		Econ:       &economy.Service{},
		Latch:      NewEndLatch(),
		LocalOwner: uint8(localOwner),
		EnemyOwner: uint8(enemyOwner),
	}
	// Seed both streams fresh at battle bootstrap, before any battle setup
	// draw [R-CORE-02] DET-01.
	s.SeedSessionRNG(cfg.RNGSimSeed, cfg.RNGCrtSeed)
	nPlayers := cfg.NumPlayers
	if nPlayers < 0 {
		nPlayers = 0
	}
	if nPlayers > 10 {
		nPlayers = 10
	}
	for i := 0; i < nPlayers && i < 10; i++ {
		p := &s.Econ.Players[i]
		p.Exists = true
		var ctrlState uint8
		switch cfg.Players[i].Controller {
		case SkirmishControllerHuman:
			ctrlState = 1 // human local [08][PLAN_14 C8]
		case SkirmishControllerObserver:
			ctrlState = 1
		default:
			ctrlState = 2 // computer [08]
		}
		p.IsObserver = cfg.Players[i].Controller == SkirmishControllerObserver
		p.ControllerState = ctrlState
		// Registration writes the score-panel rank byte beside the controller
		// byte [08 R-SKIR-01 §2].
		p.SeedScorePanelRank(i)
		// [08 R-SKIR-01 §2] "Row-to-player conversion": a live row "copies
		// colour and side into the player's lobby record" before registering
		// the slot, and the placement stamp helper "copies side and colour
		// again". The PLAYER RECORD, not the setup record, is what the runtime
		// reads a slot's side from — which is why the save's `Player%i` account
		// persists the side and logo bytes [08 "Player records"] while a load
		// restores only the five rule words and the map name into the setup
		// record [08 R-SKIR-01 §2] "Save persistence".
		//
		// This write was missing, so every player record read side 0 / colour
		// 0. Nothing noticed while the setup record was alive beside it, but a
		// restored battle has no setup rows: the commander-identity test of
		// [08 R-SKIR-01 §3] then compared every dead unit against side 0's
		// commander name, and a CORE player's commander death raised nothing —
		// no storage-bonus clear, no owner sweep, no elimination.
		p.Side = playerRecordByte(cfg.Players[i].Side)
		p.Logo = playerRecordByte(cfg.Players[i].Color)
		// Registration also "names the slot `Player` for a human or
		// `Arm`/`Core` for a computer by side (`side == 0` → `Arm`)", skirmish
		// only [08 R-SKIR-01 §2]. That name is the 30-byte copy each row of the
		// post-battle board carries [08 R-CAMP-01 §7], and it belongs to the
		// record: the setup row's nickname is lobby text and the conversion
		// does not copy it. Nothing wrote this field before, which is why the
		// result rows carried a nickname fallback the setup row stopped
		// answering after a load.
		p.Name = skirmishSlotName(ctrlState, int(p.Side))
		p.GameEnded = false
		p.EndGameCountdown = -1
	}
	// Apply the skirmish first alliance row. Group 5 is the unassigned
	// sentinel, so equality allies only non-5 groups; self is always allied
	// [08 R-SKIR-01 §2].
	for i := 0; i < nPlayers && i < 10; i++ {
		for j := 0; j < nPlayers && j < 10; j++ {
			s.Econ.Players[i].Allies[j] = skirmishPlayersAllied(cfg, i, j)
		}
	}
	// Validate at least one hostile alliance for skirmish start [08 "Skirmish configuration"].
	// Retail sentinel: all groups 5 is allowed (no error) [08 "Skirmish configuration"].
	hostile := false
	for i := 0; i < nPlayers && i < 10; i++ {
		for j := i + 1; j < nPlayers && j < 10; j++ {
			if !s.Econ.Players[i].Allies[j] {
				hostile = true
				break
			}
		}
		if hostile {
			break
		}
	}
	allSentinel := true
	for i := 0; i < nPlayers && i < 10; i++ {
		if cfg.Players[i].AllyGroup != SkirmishDefaultAllyGroup {
			allSentinel = false
			break
		}
	}
	if nPlayers >= 2 && !hostile && !allSentinel {
		return nil, fmt.Errorf("session: skirmish requires at least one hostile alliance [08 \"Skirmish configuration\"]")
	}
	s.Econ.SeedDeadlines(0)
	// Battle-entry wind: deadline zeroed, NO draws — the first wind chain runs
	// in sub-tick 1 [R-CORE-02] DET-03.
	s.InitBattleWindForSession()
	// Meteor scheduler state (initial next-strike = per-hit spacing) is
	// written at battle entry; the write consumes no draws [R-CORE-01 §4.4.1].
	s.initMeteor()
	// Audio presentation queue/cache/music owned by session so unit/weapon/feature/UI events can queue without client import cycle [03 §8.3][03 §8.4] I6.
	s.InitAudio(fs)
	// 5. create every required service non-nil and bind ports [08][04 §7.2]
	if err := createAndBindServices(s); err != nil {
		return nil, err
	}
	// Construct every manager in ascending slot order before commander and map
	// unit allocation. Each constructor consumes its exact eight strategic
	// draws from the shared stream [08 R-ENTRY-01 §3 step 24][08 R-AI-01 §9].
	hasManagerOwner := false
	for i := 0; i < nPlayers && i < 10; i++ {
		if !cfg.Players[i].IsObserver() {
			hasManagerOwner = true
			break
		}
	}
	var sharedProf *ai.Profile
	if hasManagerOwner {
		profileName := "default"
		if m != nil && m.OTA != nil && m.OTA.Global != nil {
			if mg := mission.DecodeMissionGlobals(m.OTA.Global); mg != nil && strings.TrimSpace(mg.AIProfile) != "" {
				profileName = mg.AIProfile
			}
		}
		prof, perr := loadSkirmishAIProfile(fs, profileName)
		if perr != nil {
			return nil, perr
		}
		sharedProf = prof
	}
	for i := range s.AI {
		s.AI[i] = nil
	}
	for i := 0; i < nPlayers && i < 10; i++ {
		if cfg.Players[i].IsObserver() {
			continue
		}
		if err := initializeBattleAI(s, uint8(i), sharedProf); err != nil {
			return nil, err
		}
	}
	// 7-9. battle entry: place features → units → resources (InitialMission inside) [08 "Placement and battle entry"] C9
	if err := skirmishBattleEntry(s, cfg, m); err != nil {
		return nil, err
	}
	report.Report(FamilyPlacement, 100)
	// Ensure COB VMs for all units (load via VFS, statics zero-init, piece count from program, Create run) [04 §4.1][P1-I01]
	if err := ensureCOBForAll(s, fs); err != nil {
		return nil, err
	}
	report.Report(FamilyScripts, 100)
	// 8. movement state and visibility state for new units
	ensureMovementForAll(s)
	publishVisibilityForAll(s)
	// 11. register every authoritative phase once [01 §4.4] I7
	s.RegisterAll()
	if err := finishBattleEntry(s, func() error {
		clearLiveResourceStocks(s)
		skirmishGrantResourcesDirect(s, cfg)
		s.InitShareThresholds()
		return nil
	}); err != nil {
		return nil, err
	}
	// Alliance-aware skirmish victory: rule 0 and rule 1 use the owner's live
	// unit count; rule 1 first sweeps the owner's remaining units after a
	// commander death.  Rule 2 suppresses elimination while respawn remains
	// applicable [08 R-SKIR-01 §3][08 R-TRIG-01 §6]. Countdown via EndLatch
	// [P1-01 §2.2] remains the result visibility boundary.
	// Victory evaluation runs inside authoritativeTick (loop.go) after ledger
	// cleanup [RX-08][ON-09]; victory evaluation is part of the direct session tick.
	// 12. transition through state machine [08 "Session states"] C3
	if err := s.SelectForGametype(GametypeMultiplayer); err != nil {
		return nil, err
	}
	if err := s.ValidateComposition(); err != nil {
		return nil, fmt.Errorf("session: composition invalid: %w", err)
	}
	return s, nil
}

// loadSkirmishAIProfile resolves the authored mission profile through the
// established ai/default.txt fallback. A computer player without a profile
// cannot run the rooted planner, so strict construction fails closed
// [08 "Established AI-facing data and rooted planner"].
func loadSkirmishAIProfile(fs vfs.FSOps, name string) (*ai.Profile, error) {
	prof, err := ai.LoadProfile(fs, name)
	if err != nil {
		return nil, fmt.Errorf("session: ai profile %q: %w", name, err)
	}
	if prof == nil {
		return nil, fmt.Errorf("session: ai profile %q: nil profile", name)
	}
	return prof, nil
}

func validateSkirmishLobby(cfg SkirmishConfig) error {
	n := cfg.NumPlayers
	if n < 0 {
		n = 0
	}
	if n > 10 {
		n = 10
	}
	human, computer := false, false
	for i := 0; i < n; i++ {
		switch cfg.Players[i].Controller {
		case SkirmishControllerHuman:
			human = true
		case SkirmishControllerComputer:
			computer = true
		}
	}
	if !human || !computer {
		return fmt.Errorf("session: There must be at least one player and one computer opponent [08 \"Skirmish configuration\"]")
	}
	return nil
}

// skirmishCommander resolves the configured side commander. Strict callers
// must use the authored side and unit definitions; there is no built-in
// commander substitute [08 "Skirmish configuration"].
func skirmishCommander(cat *content.Catalog, sideIdx, playerIdx int) (*content.UnitDef, error) {
	if cat == nil || sideIdx < 0 || sideIdx >= len(cat.Sides) {
		return nil, fmt.Errorf("session: side %d out of range for player %d [02 §6]", sideIdx, playerIdx)
	}
	sd := cat.Sides[sideIdx]
	if sd == nil || strings.TrimSpace(sd.Commander) == "" {
		return nil, fmt.Errorf("session: side %d missing commander [02 §6]", sideIdx)
	}
	def, ok := cat.Unit(sd.Commander)
	if !ok || def == nil {
		return nil, fmt.Errorf("session: commander %q for side %d not found [02]", sd.Commander, sideIdx)
	}
	return def, nil
}

// skirmishBattleEntry performs the single-player battle-entry order per [08
// "Placement and battle entry"] C9: place features → reconstruct units →
// grant starting resources DIRECTLY to live stock outside the ledger
// (economy.CreditSpawn) [05 "Authoritative settlement order"].
// No second draw path exists [C17].
func skirmishBattleEntry(s *Session, cfg SkirmishConfig, m *mission.Mission) error {
	if s == nil {
		return fmt.Errorf("session: nil session")
	}
	if m == nil {
		return fmt.Errorf("session: nil mission")
	}
	if err := skirmishPlaceFeatures(s, m); err != nil {
		return err
	}
	// The deposit pass runs after every feature stamp, mission-placed ones
	// included [05 R-FEAT-01 §7].
	s.World.SeedFeatureMetalDeposits()
	if err := skirmishReconstructUnits(s, cfg, m); err != nil {
		return err
	}
	// Reconstructed units may carry lazily-created order queues. Apply the
	// already-composed session binding before any subsequent battle-entry work
	// can dispatch or resolve those queues [04 §3.3][04 §3.5][06 §11.1].
	s.bindExistingOrderQueues()
	// Initialize COB before any scripted orders (mirrors mission path) [04 §4.1]
	if err := requireCOBForSession(s); err != nil {
		return err
	}
	// No cargo pass runs here. A skirmish never reaches the InitialMission
	// interpreter at all — it runs "for mission-type-1 games and BetweenMissions
	// restores only; no other start path reaches it" [04 §3.6] — so a skirmish
	// map's `i name` verbs attach nothing. This call site used to run battle
	// entry's own re-reading of those verbs, whose two units were the wrong way
	// round; it is deleted along with that helper (WU-19-205, review finding
	// R09).
	skirmishGrantResourcesDirect(s, cfg)
	// Zero the two sharing thresholds once, after units exist [P1-06] [P1-I04].
	//
	// Closed 2026-09-01 (WU-19-16). This carried an open question asking
	// whether retail writes the thresholds before or after the storage bonus is
	// applied, and answered it by writing bonus-inclusive values. The question
	// is moot: [05 R-SHARE-01 §3] establishes both threshold fields are "zeroed
	// at battle setup and never written again in the reachable image", so
	// nothing derived from capacity ever reaches them and the ordering cannot
	// matter. economy.InitShareThresholds already applies the zero writes; this
	// call site only needs the rebuilt capacity for the settlement that follows.
	s.InitShareThresholds()
	return nil
}

func skirmishPlaceFeatures(s *Session, m *mission.Mission) error {
	if s.World == nil || s.Features == nil {
		return nil
	}
	// Deterministic source order: terrain features already stamped via world.Load's
	// ExpandPlot + stampFeatureAnchors into Plot; we now stamp mission-authored
	// features in decode order (not map iteration) [08 "Placement and battle entry"] [I1][04 §6.2].
	// Each placement is tried in order; out-of-range or missing definition silently
	// fails per pool limits 0x100/0x800/WH*0xD [P1-10][P1-15] without aborting earlier placements.
	if m == nil || len(m.Features) == 0 {
		return nil
	}
	for _, fp := range m.Features {
		if !fp.IsPlaced() {
			continue
		}
		name := fp.Name
		if name == "" {
			continue
		}
		var def *content.FeatureDef
		if s.Catalog != nil && s.Catalog.Features != nil {
			def = s.Catalog.Features[content.CanonicalKey(name)]
		}
		if def == nil {
			continue
		}
		cx, cz := int(fp.X), int(fp.Z)
		// Bounds check before stamping; OOB silently skips like retail [P1-15] WH*0xD.
		if cx < 0 || cz < 0 || cx >= int(s.World.CellW) || cz >= int(s.World.CellH) {
			continue
		}
		// Stamp via service PlaceAt which handles footprint fringe and pool limits [06 §13.1].
		// No RNG draws here [08 "Placement and battle entry"].
		s.Features.PlaceAt(cx, cz, def)
	}
	return nil
}

func skirmishReconstructUnits(s *Session, cfg SkirmishConfig, m *mission.Mission) error {
	if s.Units == nil {
		return fmt.Errorf("session: missing Units for skirmish battle entry [01 §6.1]")
	}
	// Collect the StartPos specials in AUTHORED order. The lookup of
	// [08 R-ENTRY-01 §5] step 3 "scans the specials array in authored order for
	// the first record of type start position whose stored number equals p_i",
	// so a map that authored two records with the same number resolves to the
	// earlier one. This slice used to be sorted by (ID, X, Z) "for stable
	// tests", which silently re-ranked such a pair by coordinate; the decode
	// order is already deterministic (TDF enumeration order, [I1]) and is the
	// order retail scans, so the sort is removed rather than made stabler.
	// Special.ID is the STORED number — the authored suffix minus one, the same
	// number this lookup and the fatal diagnostic use [08 R-TRIG-01 §9]. It
	// used to hold the authored label instead, with a +1 applied at the call
	// site below, which decoded every alphabetic label as a number no slot ever
	// asks for and rejected `StartPos0` outright (WU-19-205, review finding
	// R10).
	var starts []mission.Special
	for _, sp := range m.Specials {
		if sp.Kind == 1 {
			starts = append(starts, sp)
		}
	}
	nPlayersLocal := cfg.NumPlayers
	if nPlayersLocal < 0 {
		nPlayersLocal = 0
	}
	if nPlayersLocal > 10 {
		nPlayersLocal = 10
	}
	// The eligible-slot gate is Established and has three clauses, not a
	// terminator byte: "record live, controller 1/2/3, side ≠ 10"
	// [08 R-ENTRY-01 §5] "Kind 2 (skirmish), no save file". Slot existence is
	// s.Econ.Players[i].Exists; the controller mapping is ControllerState 1/2/3.
	//
	// **Correction.** Both this list and the commander loop below carried a
	// open-question marker saying "the placement terminator is not represented in
	// Session". There is no placement terminator. The third clause is the
	// slot's SIDE index tested against the neutral value 10 — the same 10 that
	// [08 R-SESS-01 §1]'s two live-player counters and [08 R-CAMP-01 §7]'s
	// score-board row condition test, and the same sentinel the movement
	// sweep's own third clause carries [04 R-MOV-03 §10]. Reading `10` as a
	// newline terminator is a byte-value coincidence. The clause is inert in
	// this engine — a skirmish row's Side is a side ordinal and is never
	// seated at 10 — and is written out here so the gate is the traced one.
	var eligible []int
	for i := 0; i < nPlayersLocal && i < 10; i++ {
		if i >= len(s.Econ.Players) {
			continue
		}
		pl := &s.Econ.Players[i]
		if !pl.Exists {
			continue
		}
		cs := pl.ControllerState
		if cs != 1 && cs != 2 && cs != 3 {
			continue
		}
		if cfg.Players[i].Side == neutralSideIndex {
			continue
		}
		eligible = append(eligible, i)
	}
	n := len(eligible)
	// The shuffle is the only stream this function touches directly, and it is
	// the CRT one [P0-04] I4 DET-01: from session, not global. The simulation
	// stream is not read here at all — the stamp takes no simulation draw
	// [08 R-ENTRY-01 §5] — so nothing binds it; the allocator reaches its own.
	crt := s.CrtRNG()
	// Build the permutation of eligible slots.
	local28 := make([]int, n)
	copy(local28, eligible)
	if cfg.Location == 0 {
		// Location==0 → randomized via CRT Fisher-Yates [P0-04].
		if n < 3 {
			// Gate: (CRT_rand()*2)/0x8000 unbiased 0/1 ; if 0 skip shuffle [P0-04].
			if crt != nil {
				gate := (int(crt.Rand()) * 2) / 0x8000
				if gate != 0 {
					// proceed to shuffle
					for j := 1; j < n; j++ {
						bound := uint32(j + 1)
						r := crt.Uint32n(bound)
						local28[j], local28[r] = local28[r], local28[j]
					}
				}
			}
		} else {
			for j := 1; j < n; j++ {
				bound := uint32(j + 1)
				r := crt.Uint32n(bound)
				local28[j], local28[r] = local28[r], local28[j]
			}
		}
	} else {
		// Location !=0 → identity (no shuffle) [P0-04].
	}
	// Map logical slot → startPosIndex via permuted eligible list.
	// For each eligible logical in ascending order, map = local28[next].
	permMap := make(map[int]int) // logical slot -> permuted slot
	for idx, logical := range eligible {
		if idx < len(local28) {
			permMap[logical] = local28[idx]
		}
	}
	// Helper to find a StartPos special by its authored 1-based suffix, which
	// is what this build's decode stores in Special.ID [GAP T14]. The scan is
	// first-match in authored order and the comparison is exact
	// [08 R-ENTRY-01 §5] step 3.
	findSpecial := func(id int) *mission.Special {
		for i := range starts {
			if int(starts[i].ID) == id {
				return &starts[i]
			}
		}
		return nil
	}
	// Commander creation loop player 0..9 asc, same as retail sweep [P0-04] I1.
	for playerIdx := 0; playerIdx < 10; playerIdx++ {
		if playerIdx >= nPlayersLocal {
			continue
		}
		if playerIdx >= len(s.Econ.Players) || !s.Econ.Players[playerIdx].Exists {
			continue
		}
		ctrl := s.Econ.Players[playerIdx].ControllerState
		// The same three-clause gate as the eligible list above
		// [08 R-ENTRY-01 §5].
		if ctrl != 1 && ctrl != 2 && ctrl != 3 {
			continue
		}
		if cfg.Players[playerIdx].Side == neutralSideIndex {
			continue
		}
		// Side/commander lookup
		sideIdx := cfg.Players[playerIdx].Side
		def, commanderErr := skirmishCommander(s.Catalog, sideIdx, playerIdx)
		if commanderErr != nil {
			return commanderErr
		}
		// The stamp helper places the commander AT the slot's start position and
		// draws nothing [08 R-ENTRY-01 §5] "Kind 2 (skirmish), no save file":
		// steps 1–4 copy side and colour, set the storage bonus, resolve the
		// `StartPos`, and a miss is fatal. "No simulation draw is made by the
		// stamp or the grant themselves" — the only draws in this loop are the
		// allocator's two, taken inside s.Units.Create below [04 §2.3b].
		//
		// **Correction (WU-19-178).** This site drew two simulation values per
		// eligible slot — an X/Z "jitter" — and kept the jittered value as a
		// fallback when the map had no matching `StartPos`, with a comment
		// asserting the bounds were map-cell counts. Both halves were wrong.
		// The jitter belongs to the kind-3 (multiplayer) path alone, where its
		// bounds are the TNT extents × 16, i.e. WORLD units, not cells
		// [08 R-ENTRY-01 §5] "Kind 3", correction 2 of [08 R-ENTRY-01 §10]; the
		// "in cells" reading came from the superseded "Randomization for
		// skirmish starts" paragraph, which that correction retracts. This
		// engine never builds a kind-3 session, so the jitter has no reachable
		// caller and is deleted rather than carried with the wrong unit; the
		// draws it was taking on every skirmish were phantom traffic that
		// displaced every later draw in the battle.
		perm, has := permMap[playerIdx]
		if !has {
			// permMap is built from `eligible`, which applies the same
			// three-clause gate this loop does, so every slot reaching here has
			// an assigned position. A miss is an internal inconsistency, not a
			// retail path, and gets this build's diagnostic shape rather than
			// retail's verbatim one.
			return fmt.Errorf("nanolathe: skirmish placement has no assigned start position: slot %d, eligible slots %v, expected one assignment per eligible slot [08 R-ENTRY-01 §5]", playerIdx, eligible)
		}
		// `perm` and the decoded record both carry the stored, zero-based
		// number, so the scan compares them directly: slot i under identity
		// placement takes stored number i, whose authored label is StartPos<i+1>
		// — or StartPos0, which stores 0 as well [08 R-ENTRY-01 §5] step 3,
		// [08 R-TRIG-01 §9].
		sp := findSpecial(perm)
		if sp == nil {
			return errStartPositionMissing(perm)
		}
		x := numeric.Fixed(int32(sp.X) * 65536)
		z := numeric.Fixed(int32(sp.Z) * 65536)
		y := numeric.Fixed(0)
		if s.World != nil {
			y = s.World.HeightAt(x, z)
			if y == -1 {
				y = 0
			}
		}
		h, err := s.Units.Create(def, uint8(playerIdx), x, y, z)
		if err != nil {
			// The normal allocator may fail; battle entry keeps the sparse
			// creation entry null and continues [08 "Placement and battle entry"].
			continue
		}
		if u := s.Units.Unit(h); u != nil {
			// Mark as commander? No extra flags here but preserve placement linkage for debugging
			u.PlacementIdx = -1
			// Publish visibility synchronously before loader returns — no empty-coverage frame [03 §3.3] C10.
			publishOne(s, u)
			if s.Movement != nil && s.Movement.Routes != nil {
				s.Movement.EnsureUnit(u)
			}
		}
	}
	// Also reconstruct any mission-placed units that are not commanders, at authored positions,
	// via sparse two-pass semantics [P0-04] – reuse same sparse logic as mission path.
	// For skirmish, these are additional scenario units beyond commanders.
	for idx, up := range m.Units {
		if s.Catalog != nil {
			if def, found := s.Catalog.Unit(up.UnitName); found && def != nil {
				ownerIdx := int(up.Player)
				if ownerIdx < 0 {
					ownerIdx = 0
				}
				if ownerIdx >= 10 {
					ownerIdx = 9
				}
				// Use authored fixed positions [P0-04]
				h, err := s.Units.Create(def, uint8(ownerIdx), numeric.Fixed(int64(up.X)), numeric.Fixed(int64(up.Y)), numeric.Fixed(int64(up.Z)))
				if err != nil {
					continue
				}
				if u := s.Units.Unit(h); u != nil {
					// Scenario placement overwrites the initialized heading only after
					// both common-allocation RNG invocations [R-P28-ANG-01R §2].
					u.Move.Heading = up.Angle
					u.PlacementIdx = idx
					u.PlacementIdent = up.Ident
					u.PlacementUnitName = up.UnitName
					if up.HealthPercentage != 0 && up.HealthPercentage != 100 {
						u.Health = int32(int64(u.MaxHealth) * int64(up.HealthPercentage) / 100)
					}
					if up.IsImmune() {
						u.Flags |= units.ImmunityStatus
					}
					publishOne(s, u)
					if s.Movement != nil && s.Movement.Routes != nil {
						s.Movement.EnsureUnit(u)
					}
				}
			}
		}
	}
	return nil
}

func skirmishGrantResourcesDirect(s *Session, cfg SkirmishConfig) {
	if s == nil || s.Econ == nil {
		return
	}
	nP := cfg.NumPlayers
	if nP < 0 {
		nP = 0
	}
	if nP > 10 {
		nP = 10
	}
	for p := 0; p < nP && p < 10; p++ {
		if !s.Econ.Players[p].Exists {
			continue
		}
		// [05 "Storage capacity"] [05 R-ECO-01 §4] [OX P1] per-player storage bonus.
		// Retail enables the per-player storage bonus and applies a minimum
		// capacity of 0xC8 (200) to each resource, storing the integer bonus
		// as a float (truncation toward zero, I3).
		// Bonus must be installed BEFORE spawn credits and before first settlement's RebuildCapacity
		// so that CommitPostSettlement's bonus-inclusive capacity clamp preserves opening 1000/1000
		// past tick 30/60 [OX P1]. ARM and CORE both get correct values derived from their own startMetal/Energy.
		s.Econ.Players[p].InstallStorageBonus(cfg.Players[p].Metal, cfg.Players[p].Energy)
		// Ensure bonus is visible to capacity before first settlement.
		// RebuildCapacity will include it on next Settle; we also rebuild now so
		// the settlement that follows sees bonus-inclusive capacity. The
		// threshold-ordering question that stood here is closed: the two
		// sharing thresholds are zeroed at battle setup and never written again
		// [05 R-SHARE-01 §3], so the bonus install cannot make them stale.
		metal := float32(cfg.Players[p].Metal)
		energy := float32(cfg.Players[p].Energy)
		if metal != 0 {
			economy.CreditSpawn(&s.Econ.Players[p], economy.Metal, metal)
		}
		if energy != 0 {
			economy.CreditSpawn(&s.Econ.Players[p], economy.Energy, energy)
		}
	}
	// After all bonuses installed, rebuild capacity once so that InitShareThresholds that follows
	// sees bonus-inclusive capacity without waiting for the first 30-tick settlement.
	// This also ensures that a strict probe that steps 60 ticks without waiting for settlement
	// still has correct capacity for CommitPostSettlement clamp.
	if s.Units != nil {
		economy.RebuildCapacity(s.Econ, s.Units)
	}
}

// Ensure imports are used.
var (
	_ = clock.State{}
	_ = frame.Buffer{}
	_ = world.NewWind
	_ = numeric.Fixed(0)
	_ = content.CanonicalKey
)

var _ = strings.TrimSpace
