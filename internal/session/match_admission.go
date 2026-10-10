package session

// Match admission (docs/DESIGN_MULTIPLAYER.md §8.6–§8.8, contracts M2-C9 and
// M2-C10). Resolution and decoding establish a configuration's schema
// validity; ValidateMatchInputs establishes its admission validity against
// the frozen content the battle will run on, and NewAdmittedSkirmish composes
// a battle from exactly that admitted pair. A configuration digest that
// matches a peer's is not admission. These are Nanolathe protocol values, not
// retail findings.

import (
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// The admission refusals, one per identity a configuration is compared on
// (§8.2, §8.8). Each refusal wraps one of them, so a caller reports map,
// rules, content and configuration mismatches separately with errors.Is; a
// configuration that fails several comparisons wraps each.
var (
	// ErrMatchMapMismatch: the configuration's map or schema is not the one
	// the frozen content was captured and selected for.
	ErrMatchMapMismatch = errors.New("nanolathe: match admission rejected the map")
	// ErrMatchRulesMismatch: the configuration's effective Community table is
	// not the one the frozen content was prepared under, or its rule set is
	// not registered in this build.
	ErrMatchRulesMismatch = errors.New("nanolathe: match admission rejected the rules")
	// ErrMatchContentMismatch: the frozen content does not hold what the
	// configuration names — a side, the applied mutators or unit
	// restrictions, the content limits — or there are no frozen inputs.
	ErrMatchContentMismatch = errors.New("nanolathe: match admission rejected the content")
	// ErrMatchConfigurationRejected: the configuration selects something this
	// build cannot run, or there is no configuration.
	ErrMatchConfigurationRejected = errors.New("nanolathe: match admission rejected the configuration")
	// ErrMatchNeedsMultiSeat: the configuration is admissible but describes a
	// battle only multi-seat composition can run, which M5 delivers
	// (DESIGN_MULTIPLAYER §8.6, §8.8, §16.2).
	ErrMatchNeedsMultiSeat = errors.New("nanolathe: admitted match needs multi-seat composition, which arrives with M5")
)

// matchAdmissionError is the one diagnostic shape of an admission refusal.
// The frozen content's identity names what the configuration was compared
// with.
func matchAdmissionError(kind error, inputs *content.SimulationInputs, path, expected string) error {
	searched := "none"
	if inputs != nil {
		d := inputs.Digest()
		searched = hex.EncodeToString(d[:8])
	}
	return fmt.Errorf("%w: logical path %s, providers searched [match configuration v1, simulation content %s], expected %s", kind, path, searched, expected)
}

// matchAdmission is what admission established: the admitted configuration
// and the map the map-entry code selected for it from the frozen content.
type matchAdmission struct {
	request *MatchConfigRequest
	mission *mission.Mission
}

// ValidateMatchInputs establishes a configuration's admission validity
// against the frozen content a battle would run on: the map and its schema,
// the side ordinals, the effective Community table, the applied mutators, the
// content limits the content attests, unit restrictions and the online
// policies this build supports. Every failed comparison is reported, each
// wrapping its category's error. It allocates no world and draws from neither
// stream; it reads the frozen content only through its sealed view.
func ValidateMatchInputs(c EffectiveMatchConfig, inputs *content.SimulationInputs) error {
	_, err := admitMatch(c, inputs)
	return err
}

// admitMatch is ValidateMatchInputs, keeping the selected map for
// composition.
func admitMatch(c EffectiveMatchConfig, inputs *content.SimulationInputs) (matchAdmission, error) {
	if c.encoding == nil {
		return matchAdmission{}, matchAdmissionError(ErrMatchConfigurationRejected, inputs, "configuration", "a resolved match configuration")
	}
	if inputs == nil || inputs.Catalog() == nil || inputs.Filesystem() == nil {
		return matchAdmission{}, matchAdmissionError(ErrMatchContentMismatch, inputs, "inputs", "the frozen simulation inputs of this battle")
	}
	// The resolved value is private and immutable; admission only reads it.
	r := &c.request
	var errs []error
	refuse := func(kind error, path, expected string) {
		errs = append(errs, matchAdmissionError(kind, inputs, path, expected))
	}

	// The online policies are the consumer policies this build supports
	// (§8.6 field 15). Schema validity admits the same values today; the two
	// are separate questions, and a policy a later schema admits is not
	// supported here until its consumer exists.
	if p := r.Policies; p.Revision != MatchPolicyRevision || p.Scheduling != MatchPolicyScheduling || p.Pacing != MatchPolicyPacing || p.Drop != MatchPolicyDrop || p.Audience != MatchPolicyAudience {
		refuse(ErrMatchConfigurationRejected, "policies", "revision 1 with scheduling, pacing, drop and audience policy 1, the policies this build supports")
	}

	// Map: the configuration names the map the capture was taken for, and
	// the schema the map-entry code selects for its seat count from the
	// frozen map file is the schema the freeze recorded and the one the
	// configuration names. Design reading: the name is compared exactly, as
	// the configuration's identity spells it, and MapSchema is the selected
	// schema's index in the frozen catalog's map header, the index the freeze
	// records (skirmishSimulationRequest).
	var selected *mission.Mission
	cat := inputs.Catalog()
	if captured := inputs.MapName(); captured != r.MapName {
		refuse(ErrMatchMapMismatch, "mapName", fmt.Sprintf("the map the frozen content was captured for, %q", captured))
	} else if m, err := mission.LoadWithType(inputs.Filesystem(), mission.TypeSkirmish, r.MapName, 0, len(r.Seats), nil); err != nil || m == nil {
		refuse(ErrMatchMapMismatch, "mapName", fmt.Sprintf("a map the frozen content holds with a schema for %d seats: %v", len(r.Seats), err))
	} else if header := cat.Maps[content.CanonicalKey(m.TerrainKey)]; header == nil {
		refuse(ErrMatchMapMismatch, "mapName", fmt.Sprintf("a map header for %q in the frozen catalog", m.TerrainKey))
	} else {
		ota, tnt := inputs.MapFiles()
		index := mapSchemaIndex(header, m.Schema.Name)
		switch {
		case header.LogicalOTA != ota || header.LogicalTNT != tnt:
			refuse(ErrMatchMapMismatch, "mapName", fmt.Sprintf("the map files the frozen content recorded, %q and %q", ota, tnt))
		case int(index) >= len(header.Schemas):
			refuse(ErrMatchMapMismatch, "mapSchema", fmt.Sprintf("a schema the map header lists for the selected %q", m.Schema.Name))
		case inputs.MapSchema() != index:
			refuse(ErrMatchMapMismatch, "mapSchema", fmt.Sprintf("the schema the frozen content selected, %d; the map-entry code selects %d for %d seats", inputs.MapSchema(), index, len(r.Seats)))
		case r.MapSchema != index:
			refuse(ErrMatchMapMismatch, "mapSchema", fmt.Sprintf("the schema the map-entry code selects for %d seats, %d", len(r.Seats), index))
		default:
			selected = m
		}
	}

	// The frozen metadata records the exact resolved set that prepared the
	// catalog. A matching Community table alone cannot establish that: two
	// sets can prepare different reload values under the same table
	// (DESIGN_MULTIPLAYER §16.3.34).
	set, known := LookupRuleSet(r.RuleName)
	name, base := inputs.PreparingRule()
	if !known || name != r.RuleName || base != string(set.Base) {
		refuse(ErrMatchRulesMismatch, "preparingRule", fmt.Sprintf("catalog preparation under rule %q with its registered base, frozen under %q (%q)", r.RuleName, name, base))
	}
	if got := communityDigest(r.Community); got != inputs.CommunityDigest() {
		want := inputs.CommunityDigest()
		refuse(ErrMatchRulesMismatch, "community", fmt.Sprintf("the Community table the frozen content was prepared under, digest %x, got %x", want[:], got[:]))
	}

	// Content: the mutators are the vector the frozen catalog clone was
	// already prepared with, so composition never applies one a second time
	// (§8.7).
	if frozen := inputs.Mutators(); r.Mutators != frozen {
		refuse(ErrMatchContentMismatch, "mutators", fmt.Sprintf("the mutators the frozen catalog was prepared with, %q, got %q", frozen.String(), r.Mutators.String()))
	}
	// Every seat's side is a side the admitted content defines (§8.6 player
	// row 1). Design reading: every role is checked, the Survival attacker
	// and watchers included, since each row's side reaches its player record.
	for i := range r.Seats {
		if int(r.Seats[i].Side) >= len(cat.Sides) {
			refuse(ErrMatchContentMismatch, fmt.Sprintf("seats[%d].side", i), fmt.Sprintf("a side below the admitted content's %d sides", len(cat.Sides)))
		}
	}
	// The unit restrictions are the set the frozen catalog clone was already
	// restricted with, before the mutators, so composition never applies one
	// a second time (§8.6 field 12, §16.6; DESIGN_MODS_MUTATORS §15.3, §15.5).
	// An empty list therefore admits only unrestricted content. The records'
	// definition IDs index the unrestricted catalog, which FreezeMatchInputs
	// verified them against; here every surviving record is checked against
	// the frozen table as well.
	if set, err := matchRestrictionSet(r.UnitRestrictions); err != nil {
		refuse(ErrMatchConfigurationRejected, "unitRestrictions", "records describing one restriction set: "+err.Error())
	} else if frozen := inputs.Restrictions(); !set.Equal(frozen) {
		refuse(ErrMatchContentMismatch, "unitRestrictions", fmt.Sprintf("the unit restrictions the frozen catalog was prepared with, %q, got %q", frozen.String(), set.String()))
	} else if !set.IsZero() {
		if path, expected, ok := matchFrozenRestrictionRecords(cat, r.UnitRestrictions, set); !ok {
			refuse(ErrMatchContentMismatch, path, expected)
		}
	}
	// Clone preserves the effective compile limits even when preparation
	// transforms definitions. Missing limits are an admission failure too.
	limits, p := cat.Limits, r.ContentProfile
	if int64(p.Units) != int64(limits.Units) || int64(p.Weapons) != int64(limits.Weapons) || p.TNTBytes != uint64(limits.TNTBytes) || p.LOSBytes != uint64(limits.LOSBytes) {
		refuse(ErrMatchContentMismatch, "contentProfile", fmt.Sprintf("the limits the frozen catalog was compiled under: units %d, weapons %d, TNT bytes %d, LOS bytes %d", limits.Units, limits.Weapons, limits.TNTBytes, limits.LOSBytes))
	}
	// TODO(question): the frozen inputs carry no record of the content
	// profile's name or directory table, nor of the mod's id, version and
	// archive digest (§8.6 fields 8 and 9): the mount applied them before the
	// capture began, and the capture records only the files it read. Neither
	// can be checked against the content here. The mod half is now compared
	// at the join (U6): CompareMatchIdentity takes the mod this seat's mount
	// applied, as its mod library names it, and refuses it unless it is the
	// configuration's and the other seat's. The profile's name and directory
	// table remain unattested; settle by having the mount's owner record the
	// mounted profile with the capture.

	if len(errs) != 0 {
		return matchAdmission{}, errors.Join(errs...)
	}
	return matchAdmission{request: r, mission: selected}, nil
}

// NewAdmittedSkirmish composes the battle an admitted configuration describes
// from the frozen inputs it was admitted against. It establishes admission
// validity first (ValidateMatchInputs) and returns that refusal before any
// pool, world, random stream or catalog work.
//
// Until M5 lands per-seat perspectives, it composes only a configuration with
// exactly one human seat, who added every computer, and no watcher, and
// refuses any other with ErrMatchNeedsMultiSeat rather than composing it
// through the single-human path (§8.6, §8.8, §16.2). Its computers may differ
// in difficulty: each seat's readers take its own word (§6.6). That shape
// composes through skirmish entry's own back half from the given inputs:
// their catalog, their sealed view and their map, with nothing captured,
// compiled, prepared or frozen again, and with the configuration's seeds,
// rows, options and Survival fields. When every computer has one difficulty
// the battle is the one the local adapter's setup and options
// (NewMatchConfigRequest) compose through NewSkirmishWithEntryOptions.
//
// Design reading: the permissions, views, online policies and participant
// identities are not composition inputs. The command boundary (U2) and the
// host (M6) enforce them from the configuration, so they neither change the
// battle composed here nor refuse it. The session privately retains the
// admitted inputs/configuration for checkpoint provenance; a host keeps its
// own resolved value for command and view policy enforcement.
func NewAdmittedSkirmish(inputs *content.SimulationInputs, c EffectiveMatchConfig, progress content.Progress) (*Session, error) {
	admitted, err := admitMatch(c, inputs)
	if err != nil {
		return nil, err
	}
	r := admitted.request
	if err := matchSingleSeat(r, inputs); err != nil {
		return nil, err
	}
	// The composition's own selection of the admitted rule set. Selection by
	// an unregistered name would fall back to Modern (SetGameplay), so a
	// registry that changed since resolution is refused instead.
	set, ok := LookupRuleSet(r.RuleName)
	if !ok {
		return nil, matchAdmissionError(ErrMatchRulesMismatch, inputs, "ruleName", fmt.Sprintf("a rule set this build registered, got %q", r.RuleName))
	}
	cfg, options := matchSkirmishSetup(r)
	options.Progress = progress
	// The session re-resolves its table from its sources whenever it binds
	// rules, so the sources must resolve to the admitted table under the
	// admitted set.
	if features, err := resolveCommunity(set, options.CommunitySources); err != nil {
		return nil, matchAdmissionError(ErrMatchRulesMismatch, inputs, "community", fmt.Sprintf("a table rule set %q resolves: %v", r.RuleName, err))
	} else if features != r.Community {
		return nil, matchAdmissionError(ErrMatchRulesMismatch, inputs, "community", fmt.Sprintf("a table rule set %q resolves to itself", r.RuleName))
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := validateSkirmishLobby(cfg); err != nil {
		return nil, err
	}
	// Audio is presentation and keeps the live mount: the admitted
	// constructor takes none, so the service starts silent and the host binds
	// its own mount when it adopts the battle, as it does for every battle
	// (attachBattleAudio). The simulation reads nothing through it, and its
	// presentation draws use a copy of the CRT stream.
	receipt, err := newSessionCheckpointAdmission(inputs, c, admitted.mission)
	if err != nil {
		return nil, err
	}
	return composeSkirmish(skirmishEntry{cfg: cfg, features: r.Community, mission: admitted.mission, inputs: inputs, checkpointAdmission: receipt}, options, nil)
}

// matchSingleSeat refuses a configuration the single-player composition
// cannot run: more than one human seat or a watcher row. With one human,
// every computer is that human's: resolution requires each computer's host
// seat to name a human row. Computers may differ in difficulty: each seat's
// readers take its own word (matchSkirmishSetup, §6.6).
func matchSingleSeat(r *MatchConfigRequest, inputs *content.SimulationInputs) error {
	humans := 0
	for i, seat := range r.Seats {
		path := fmt.Sprintf("seats[%d]", i)
		switch seat.Role {
		case MatchRoleHuman:
			if humans++; humans > 1 {
				return matchAdmissionError(ErrMatchNeedsMultiSeat, inputs, path+".role", "one human seat until M5 composes a battle for several seats (DESIGN_MULTIPLAYER §6, §16.2)")
			}
		case MatchRoleWatcher:
			return matchAdmissionError(ErrMatchNeedsMultiSeat, inputs, path+".role", "no watcher seat until M5 composes watcher perspectives (DESIGN_MULTIPLAYER §11.4, §16.2)")
		}
	}
	return nil
}

// matchSkirmishSetup writes an admitted configuration as the setup and
// battle-entry options skirmish entry composes: the inverse of the local
// adapter (NewMatchConfigRequest) for every configuration that adapter can
// describe. The setup is already normalized; it is never passed through
// Normalize, whose defaults would replace an explicit zero resource with 1000
// (§8.6 player row 1). The options' load observer is the caller's.
//
// It also serves the front half (FreezeMatchInputs), which reads only the
// map, the seat count, the rule set and the content fields, so it writes
// every role: a human row as a human, a watcher as an observer, a computer
// and the Survival attacker as computers.
func matchSkirmishSetup(r *MatchConfigRequest) (SkirmishConfig, SkirmishEntryOptions) {
	cfg := SkirmishConfig{
		Gameplay:       gameplay.Mode(r.RuleName),
		MapName:        r.MapName,
		NumPlayers:     len(r.Seats),
		Location:       int(r.Location),
		CommanderDeath: int(r.CommanderDeath),
		Mapping:        int(r.Mapping),
		LineOfSight:    int(r.LineOfSight),
		LOSType:        int(r.LOSType),
		UnitLimit:      int(r.UnitLimit),
		RNGSimSeed:     r.SimulationSeed,
		RNGCrtSeed:     r.CRTSeed,
		// Every rule word above is the configuration's explicit choice.
		rulesDefaultsApplied: true,
	}
	if r.SessionKind == MatchOnlineSurvival {
		cfg.Survival = SurvivalOptions{Enabled: true, Pace: r.SurvivalPace, NoAir: r.SurvivalNoAir, NoNaval: r.SurvivalNoNaval}
	}
	options := SkirmishEntryOptions{
		Mutators: r.Mutators,
		ContentLimits: content.Limits{
			Units:    int(r.ContentProfile.Units),
			Weapons:  int(r.ContentProfile.Weapons),
			TNTBytes: int64(r.ContentProfile.TNTBytes),
			LOSBytes: int64(r.ContentProfile.LOSBytes),
		},
	}
	// The agreed table is the battle's whole Community input: one final
	// layer naming it as a complete base, after the set's own declarations,
	// resolves to exactly that table under the admitted set. Strict 3.1
	// resolves the zero table from any sources, so it carries none.
	if RuleSetForMode(cfg.Gameplay).Base != gameplay.Strict31 {
		table := r.Community
		options.CommunitySources = CommunitySources{CommandLine: []community.Overrides{{Base: &table}}}
	}
	// Each computer seat's readers take its own difficulty (§6.6, §15 Q28).
	// When every computer has one value that value is the battle's one word,
	// which composes exactly the battle a single-player setup with that word
	// does. When they differ, the setup carries each seat's word
	// (SeatDifficulty) and no computer reads the battle's. Design reading:
	// with no added computer no reader consults the word — a human consults
	// none and the Survival attacker runs no planner and has no economy
	// (§6.6, DESIGN_SURVIVAL §4.1) — so the setup's missing-value default
	// stands in for it, and for those rows of a setup whose computers differ.
	cfg.Difficulty = SkirmishDefaultDifficulty
	first := -1
	for _, seat := range r.Seats {
		if seat.Role != MatchRoleComputer {
			continue
		}
		switch {
		case first < 0:
			first = int(seat.Difficulty)
		case int(seat.Difficulty) != first:
			cfg.SeatDifficulty = true
		}
	}
	if first >= 0 && !cfg.SeatDifficulty {
		cfg.Difficulty = first
	}
	for i := range r.Seats {
		seat := &r.Seats[i]
		p := SkirmishPlayer{
			Nickname:  seat.Nickname,
			Side:      int(seat.Side),
			Color:     int(seat.Color),
			AllyGroup: int(seat.AllyGroup),
			Metal:     int(seat.Metal),
			Energy:    int(seat.Energy),
		}
		switch seat.Role {
		case MatchRoleHuman:
			p.Controller = SkirmishControllerHuman
			// Battle entry gives the local human its preference and every
			// other seat the bound rules' defaults. Non-human wire rows keep
			// their canonical padding, which is not a preference override.
			human := seat.BuilderOptions
			options.BuilderOptions = &human
		case MatchRoleWatcher:
			p.Controller = SkirmishControllerObserver
		case MatchRoleComputer:
			p.Controller = SkirmishControllerComputer
			p.AI = seat.ComputerKind
			if cfg.SeatDifficulty {
				p.Difficulty = int(seat.Difficulty)
			}
			// Each computer's merged parameters as its own slot's layer; the
			// All and difficulty layers are already merged into them. The
			// managers of other seats, which no controller reads, get none.
			options.AIOverrides.Players[i] = joinAIParams(append([]AIParam(nil), seat.AIParams...))
		case MatchRoleSurvivalAttacker:
			// The attacker row as the Survival setup writes it: a computer
			// slot whose manager battle entry makes passive. Its resources
			// are the configuration's zero, read by nothing, since it has no
			// economy (DESIGN_SURVIVAL §4.1).
			p.Controller = SkirmishControllerComputer
			p.AI = ai.ControllerClassic
		}
		cfg.Players[i] = p
	}
	return cfg, options
}

// FreezeMatchInputs is battle entry's front half for an agreed configuration,
// the step that turns a decoded configuration (DecodeMatchConfig) into the
// frozen content a battle runs on and is admitted against
// (DESIGN_MULTIPLAYER §8.7, §8.8). It captures fs, compiles cat through the
// capture — or validates a supplied catalog against it — prepares it under
// the configuration's rule set, Community table and mutators, selects the
// configuration's map for its seat count and freezes the result. Content
// freezing reads no seat but their count, so it serves every configuration,
// multi-seat ones included; whether a battle can be composed from the result
// is NewAdmittedSkirmish's question, and whether the result admits the
// configuration is ValidateMatchInputs'. A supplied catalog must have been
// compiled under the configuration's content limits, which admission checks,
// and must be unrestricted: the catalog compile's own table, whose record
// indices field 12's definition IDs are.
//
// The unit restrictions of field 12 are read back against that unrestricted
// catalog (RestrictionsFromMatch) and handed to battle entry as its
// restriction set, so the clone is restricted exactly as single-player battle
// entry restricts it, before the Community preparation and the mutators
// (DESIGN_MODS_MUTATORS §15.3, §15.5). Without a supplied catalog and with
// restrictions, the catalog is compiled from a capture of its own first and
// then handed to battle entry, whose freeze checks it against the battle's
// capture like any supplied catalog. An empty field 12 leaves the front half
// exactly as before.
//
// Every refusal wraps ErrMatchConfigurationRejected (no configuration) or
// ErrMatchContentMismatch (this install could not capture, compile, select
// or freeze what the configuration names, its cause chained), in the
// project's diagnostic shape.
func FreezeMatchInputs(fs vfs.FSOps, cat *content.Catalog, c EffectiveMatchConfig, progress content.Progress) (*content.SimulationInputs, error) {
	if c.encoding == nil {
		return nil, matchAdmissionError(ErrMatchConfigurationRejected, nil, "configuration", "a resolved match configuration")
	}
	if fs == nil {
		return nil, matchAdmissionError(ErrMatchContentMismatch, nil, "filesystem", "the mounted content the configuration's battle is frozen from")
	}
	cfg, options := matchSkirmishSetup(&c.request)
	options.Progress = progress
	if records := c.request.UnitRestrictions; len(records) != 0 {
		base, err := matchUnrestrictedCatalog(fs, cat, c.request.MapName, options.ContentLimits, progress)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", matchAdmissionError(ErrMatchContentMismatch, nil, "catalog",
				"the unrestricted catalog this install compiles, which the unit restrictions' definition IDs index"), err)
		}
		restrictions, err := RestrictionsFromMatch(base, records)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", matchAdmissionError(ErrMatchContentMismatch, nil, "unitRestrictions",
				"unit restrictions this install's unrestricted catalog holds, record for record"), err)
		}
		cat, options.Restrictions = base, restrictions
	}
	entry, err := prepareSkirmishInputs(fs, cat, cfg, options, false)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", matchAdmissionError(ErrMatchContentMismatch, nil, "inputs",
			fmt.Sprintf("content this install can capture, compile, prepare and freeze for map %q, %d seats and rule set %q", c.request.MapName, len(c.request.Seats), c.request.RuleName)), err)
	}
	return entry.inputs, nil
}

// matchUnrestrictedCatalog is the catalog field 12's definition IDs index: a
// supplied catalog, validated as battle entry validates it, or one compiled
// through a capture of fs taken for the configuration's map.
func matchUnrestrictedCatalog(fs vfs.FSOps, cat *content.Catalog, mapName string, limits content.Limits, progress content.Progress) (*content.Catalog, error) {
	if cat != nil {
		return strictCatalogWithProgress(fs, cat, limits, progress)
	}
	sources, err := content.CaptureSimulationSources(fs, mapName)
	if err != nil {
		return nil, err
	}
	return strictCatalogWithProgress(sources.Filesystem(), nil, limits, progress)
}
