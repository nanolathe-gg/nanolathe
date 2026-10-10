package session

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
)

// MatchRoomInputs are the agreed values a match configuration holds beyond
// today's skirmish setup and battle-entry options (docs/DESIGN_MULTIPLAYER.md
// §8.1 item 5, §8.6): the map schema the map-entry code selected, the mod and
// content profile, the seats' public identities, and the room's permissions,
// restrictions, views and policies. The room supplies every one of them; the
// adapter fills none. UnitRestrictions are field 12's records, which
// MatchUnitRestrictions maps from the host's restriction set against its
// unrestricted compiled catalog.
type MatchRoomInputs struct {
	// MapSchema is the schema the existing map-entry code selected for the
	// battle's map and seat count, resolved once by the room.
	MapSchema uint32
	Mod       MatchMod
	// ContentProfile names the mounted content profile, and
	// ContentDirectories is its directory table (profiles.Profile's Name and
	// Directories). The limits come from the entry options.
	ContentProfile     string
	ContentDirectories map[string]string
	// Participants are the public identities of the human and watcher rows,
	// by slot. Rows of other roles ignore theirs.
	Participants     [SkirmishMaxPlayers]MatchParticipantID
	UnitRestrictions []MatchUnitRestriction
	CheatsAllowed    bool
	WatchingAllowed  bool
	PlayerView       MatchView
	SpectatorView    MatchView
	ReplayView       MatchView
	Policies         MatchPolicies
}

// NewMatchConfigRequest is the local request adapter: it resolves a skirmish
// or Survival setup and its battle-entry options exactly as single-player
// battle entry would — the Community table resolved through the named rule
// set, its unit-limit override applied, then SkirmishConfig.Normalize — and
// writes the result as an explicit request. Only this adapter uses the
// setup's defaulting; ResolveMatchConfig and DecodeMatchConfig never do.
//
// It describes a setup with one local human, who added every computer row
// (§6.6): a lobby seating several humans builds its request itself. Neither
// argument is changed. Every entry option is accounted for (§8.6): the
// builder options become the human row's six values, the Community sources
// the resolved table, the content limits and mutators their fields, and the
// AI overrides each computer row's merged parameters. The restrictions are
// field 12, whose records the room supplies, mapped against its unrestricted
// catalog by MatchUnitRestrictions, which this adapter does not hold; a
// nonzero set in the options must be the set those records describe, so it is
// never silently dropped. AutomatedPlayers is refused, since a lockstep battle
// never sets it; Progress is a local load observer; SimArt belongs to frozen
// content identity, not configuration.
func NewMatchConfigRequest(cfg SkirmishConfig, options SkirmishEntryOptions, room MatchRoomInputs) (MatchConfigRequest, error) {
	return newMatchConfigRequest(cfg, options, room, nil)
}

// newMatchConfigRequest is the adapter for one local human (lobby nil) or
// for an online lobby's rows (NewOnlineMatchRequest), which seats several
// humans, each taking its own row's participant, and the computers the room
// host added, each with its own difficulty from lobby, indexed by row.
func newMatchConfigRequest(cfg SkirmishConfig, options SkirmishEntryOptions, room MatchRoomInputs, lobby *[SkirmishMaxPlayers]uint8) (MatchConfigRequest, error) {
	if options.AutomatedPlayers {
		return MatchConfigRequest{}, matchFieldError("options.automatedPlayers", "false: an online battle seats its computers through the configuration")
	}
	if !options.Restrictions.IsZero() {
		set, err := matchRestrictionSet(room.UnitRestrictions)
		if err != nil {
			return MatchConfigRequest{}, err
		}
		if !set.Equal(options.Restrictions) {
			return MatchConfigRequest{}, matchFieldError("options.restrictions", fmt.Sprintf("the set the room's field-12 records describe (MatchUnitRestrictions), %q; got %q", set.String(), options.Restrictions.String()))
		}
	}
	// The zero mode word is Modern by the vocabulary's own contract; any
	// other word must name a registered set and is never normalized to
	// Modern here.
	name := string(cfg.Gameplay)
	if name == "" {
		name = string(gameplay.Modern)
	}
	set, ok := LookupRuleSet(name)
	if !ok {
		return MatchConfigRequest{}, matchFieldError("gameplay", fmt.Sprintf("a rule set this build registered, one of %s; got %q", strings.Join(RuleSetNames(), ", "), name))
	}
	base, _ := matchRuleBaseFor(set.Base)
	features, err := resolveCommunity(set, options.CommunitySources)
	if err != nil {
		return MatchConfigRequest{}, err
	}
	// The same order as skirmish battle entry: the table's unit limit, then
	// the setup's normalization.
	if features.UnitLimit != 0 {
		cfg.UnitLimit = features.UnitLimit
	}
	if err := cfg.Normalize(); err != nil {
		return MatchConfigRequest{}, err
	}
	r := MatchConfigRequest{
		SessionKind:      MatchOnlineSkirmish,
		RuleName:         name,
		RuleBase:         base,
		MapName:          cfg.MapName,
		MapSchema:        room.MapSchema,
		SimulationSeed:   cfg.RNGSimSeed,
		CRTSeed:          cfg.RNGCrtSeed,
		Mod:              room.Mod,
		Community:        features,
		Mutators:         options.Mutators,
		CheatsAllowed:    room.CheatsAllowed,
		WatchingAllowed:  room.WatchingAllowed,
		PlayerView:       room.PlayerView,
		SpectatorView:    room.SpectatorView,
		ReplayView:       room.ReplayView,
		Policies:         room.Policies,
		UnitRestrictions: append([]MatchUnitRestriction(nil), room.UnitRestrictions...),
	}
	// The rule words as composition reads them: Location zero randomizes and
	// anything else is identity; commander death through its closed
	// vocabulary; the three visibility words by their low bit
	// (visibilityModeForSession).
	if cfg.Location != 0 {
		r.Location = 1
	}
	r.CommanderDeath = uint8(CommanderDeathMode(cfg.CommanderDeath))
	r.Mapping = uint8(cfg.Mapping & 1)
	r.LineOfSight = uint8(cfg.LineOfSight & 1)
	r.LOSType = uint8(cfg.LOSType & 1)
	if cfg.UnitLimit < matchMinUnitLimit || cfg.UnitLimit > matchMaxUnitLimit {
		return MatchConfigRequest{}, matchFieldError("unitLimit", fmt.Sprintf("%d..%d", matchMinUnitLimit, matchMaxUnitLimit))
	}
	r.UnitLimit = uint16(cfg.UnitLimit)
	if cfg.Survival.Enabled {
		r.SessionKind = MatchOnlineSurvival
		r.SurvivalPace = cfg.Survival.Pace
		r.SurvivalNoAir = cfg.Survival.NoAir
		r.SurvivalNoNaval = cfg.Survival.NoNaval
	}
	if r.Seats, err = matchSeatsFromSetup(cfg, options, room, lobby); err != nil {
		return MatchConfigRequest{}, err
	}
	if r.ContentProfile, err = matchContentProfileFromEntry(room, options.ContentLimits); err != nil {
		return MatchConfigRequest{}, err
	}
	return r, nil
}

// matchSeatsFromSetup writes the normalized setup's rows. Outside a lobby
// the setup has one local human, the host of every computer row, and the
// setup's one difficulty word is every computer's. A lobby's setup has one or
// more humans and the room host's computers: the host is the first human,
// relay seat 0, and each computer's difficulty is its own row's in lobby
// (§6.6, §16.6).
func matchSeatsFromSetup(cfg SkirmishConfig, options SkirmishEntryOptions, room MatchRoomInputs, lobby *[SkirmishMaxPlayers]uint8) ([]MatchSeat, error) {
	n := cfg.NumPlayers
	attacker := cfg.survivalAttacker()
	local := -1
	for i := 0; i < n; i++ {
		if i != attacker && cfg.Players[i].IsHuman() {
			if local >= 0 && lobby == nil {
				return nil, matchFieldError(fmt.Sprintf("players[%d]", i), "one local human: a lobby seating several humans builds its configuration with NewOnlineMatchRequest")
			}
			if local < 0 {
				local = i
			}
		}
	}
	if local < 0 {
		return nil, matchFieldError("players", "one local human row")
	}
	// The setup's one difficulty word is each added computer's own value
	// online (§6.6, §15 Q28).
	if cfg.Difficulty < 0 || cfg.Difficulty > 2 {
		return nil, matchFieldError("difficulty", "0 easy, 1 medium or 2 hard")
	}
	difficulty := uint8(cfg.Difficulty)
	seats := make([]MatchSeat, n)
	for i := 0; i < n; i++ {
		p := cfg.Players[i]
		path := fmt.Sprintf("players[%d]", i)
		s := MatchSeat{HostSeat: MatchHostNone, BuilderOptions: orders.DefaultBuilderOptions()}
		if p.Side < 0 || p.Side > math.MaxUint8 {
			return nil, matchFieldError(path+".side", "a side ordinal 0..255")
		}
		if p.Color < 0 || p.Color > 9 {
			return nil, matchFieldError(path+".color", "0..9")
		}
		if p.AllyGroup < 0 || p.AllyGroup > SkirmishDefaultAllyGroup {
			return nil, matchFieldError(path+".allyGroup", "0..5")
		}
		if p.Metal < 0 || p.Metal > math.MaxInt32 || p.Energy < 0 || p.Energy > math.MaxInt32 {
			return nil, matchFieldError(path+".metal, energy", "finalized resources 0..2147483647")
		}
		s.Side, s.Color, s.AllyGroup = uint8(p.Side), uint8(p.Color), uint8(p.AllyGroup)
		s.Metal, s.Energy = int32(p.Metal), int32(p.Energy)
		nickname, err := matchNickname(path, p.Nickname)
		if err != nil {
			return nil, err
		}
		s.Nickname = nickname
		switch {
		case i == attacker:
			// The attacker has no economy: its resources are read by nothing.
			s.Role = MatchRoleSurvivalAttacker
			s.Metal, s.Energy = 0, 0
		case p.IsHuman():
			s.Role = MatchRoleHuman
			s.Participant = room.Participants[i]
			s.BuilderOptions = RuleSetForMode(cfg.Gameplay).Orders.DefaultBuilderOptions()
			if options.BuilderOptions != nil {
				s.BuilderOptions = *options.BuilderOptions
			}
		case p.IsObserver():
			s.Role = MatchRoleWatcher
			s.Participant = room.Participants[i]
		default:
			s.Role = MatchRoleComputer
			s.HostSeat = uint8(local)
			s.ComputerKind = p.AI
			s.Difficulty = difficulty
			if lobby != nil {
				if s.Difficulty = lobby[i]; s.Difficulty > 2 {
					return nil, matchFieldError(path+".difficulty", "0 easy, 1 medium or 2 hard")
				}
			}
			text, err := options.AIOverrides.For(uint8(i), matchAIDifficulty(s.Difficulty))
			if err != nil {
				return nil, matchFieldError(path+".aiParams", "canonical key=value parameters: "+err.Error())
			}
			params, err := ParseAIParams(text)
			if err != nil {
				return nil, matchFieldError(path+".aiParams", "canonical key=value parameters: "+err.Error())
			}
			s.AIParams = params
		}
		seats[i] = s
	}
	for i := range seats {
		seats[i].SharedVictory = matchTeamOfTwo(seats, i)
	}
	return seats, nil
}

// matchAIDifficulty is the AI vocabulary's word for a difficulty value, the
// layer AIOverrides.For merges.
func matchAIDifficulty(d uint8) ai.Difficulty {
	switch d {
	case 0:
		return ai.DifficultyEasy
	case 2:
		return ai.DifficultyHard
	}
	return ai.DifficultyMedium
}

// matchNickname corrects the one invalid form a local name can take
// innocently: Normalize truncates a name to sixteen bytes, which may cut a
// multi-byte character, so an incomplete character at the end is dropped.
// Any other invalid UTF-8, or a NUL, is the caller's to fix before ready.
func matchNickname(path, name string) (string, error) {
	for trimmed := 0; !utf8.ValidString(name) && trimmed < utf8.UTFMax-1 && len(name) > 0; trimmed++ {
		if r, size := utf8.DecodeLastRuneInString(name); r != utf8.RuneError || size != 1 {
			break
		}
		name = name[:len(name)-1]
	}
	if err := validateMatchText(path+".nickname", name, matchNicknameMaxBytes); err != nil {
		return "", err
	}
	return name, nil
}

// matchContentProfileFromEntry writes field 9 from the room's profile and
// the entry's limits. A limit the entry leaves unset is the retail value
// the catalog compile would use for it (content.Limits.normalize); a
// directory row that maps a name to itself, which the layout drops, is
// dropped here too, and every target is spelled in lower case, since the
// lookups that read it are case-insensitive.
func matchContentProfileFromEntry(room MatchRoomInputs, limits content.Limits) (MatchContentProfile, error) {
	retail := content.RetailLimits()
	if limits.Units <= 0 {
		limits.Units = retail.Units
	}
	if limits.Weapons <= 0 {
		limits.Weapons = retail.Weapons
	}
	if limits.TNTBytes <= 0 {
		limits.TNTBytes = retail.TNTBytes
	}
	if limits.LOSBytes <= 0 {
		limits.LOSBytes = retail.LOSBytes
	}
	if limits.Units > content.MaxDefinitionDomain || limits.Weapons > math.MaxInt32 {
		return MatchContentProfile{}, matchFieldError("options.contentLimits", fmt.Sprintf("units 1..%d and weapons 1..%d", content.MaxDefinitionDomain, math.MaxInt32))
	}
	p := MatchContentProfile{
		Name:     room.ContentProfile,
		Units:    uint32(limits.Units),
		Weapons:  uint32(limits.Weapons),
		TNTBytes: uint64(limits.TNTBytes),
		LOSBytes: uint64(limits.LOSBytes),
	}
	for _, from := range slices.Sorted(maps.Keys(room.ContentDirectories)) {
		to := room.ContentDirectories[from]
		if from == "" || to == "" || strings.EqualFold(from, to) {
			continue
		}
		p.Directories = append(p.Directories, MatchDirectory{From: content.CanonicalKey(from), To: asciiLower(to)})
	}
	// Canonical keys may reorder rows whose authored spelling differed.
	slices.SortFunc(p.Directories, func(a, b MatchDirectory) int { return strings.Compare(a.From, b.From) })
	return p, nil
}

// asciiLower folds ASCII capitals only, leaving every other byte as it is.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}
