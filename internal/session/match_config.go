package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/brains/utiltac"
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/survival"
)

// The effective match configuration of an online battle
// (docs/DESIGN_MULTIPLAYER.md §8.6, §8.8). It is a Nanolathe protocol value,
// not a retail record: every bound below is an admission limit, and every
// field is explicit. A host resolves its preferences once — the local adapter
// in match_config_local.go may use SkirmishConfig's defaulting for that —
// and ResolveMatchConfig then validates, freezes and encodes that same value.
// Neither resolution nor decoding fills a default: an unused row is not
// encoded, and a field a role does not read must hold its canonical zero.
//
// Resolution and decoding establish schema validity: closed values,
// canonical form and a rule set this build registered. Admission against
// frozen content — the map and its schema, side ordinals, definition
// restrictions, the content profile and the supported consumer policies —
// is a later, separate check (§8.8 ValidateMatchInputs).

// MatchSessionKind is field 1: which online session the configuration
// composes. Campaign and single-player recordings have their own admission
// and are not configurations of this kind.
type MatchSessionKind uint8

// The two session kinds of configuration version 1 (§8.6 field 1).
const (
	MatchOnlineSkirmish MatchSessionKind = 1
	MatchOnlineSurvival MatchSessionKind = 2
)

// MatchRuleBase is the reserved base of field 2, the wire spelling of the
// three reserved rule sets. The numbers are protocol constants, not an
// ordering of gameplay.Mode.
type MatchRuleBase uint8

// The reserved bases of configuration version 1 (§8.6 field 2).
const (
	MatchBaseStrict31    MatchRuleBase = 1
	MatchBaseModern      MatchRuleBase = 2
	MatchBaseCommunity39 MatchRuleBase = 3
)

// MatchRole is a seat row's first byte (§8.6, player row 1).
type MatchRole uint8

// The roles of configuration version 1. The Survival scenario attacker is
// not an added computer: it runs no planner and is not counted by the online
// computer-seat cap (§6.6, DESIGN_SURVIVAL §4.1).
const (
	MatchRoleHuman            MatchRole = 1
	MatchRoleComputer         MatchRole = 2
	MatchRoleWatcher          MatchRole = 3
	MatchRoleSurvivalAttacker MatchRole = 4
)

// MatchParticipantID is a human or watcher seat's public opaque identity, an
// `id` on the wire (§7.4.1). It is not a credential: no socket, token or
// machine name enters the configuration.
type MatchParticipantID [16]byte

// MatchHostNone is the HostSeat of every row that is not an added computer:
// a required value no reader consults.
const MatchHostNone uint8 = 255

// MatchSeat is one positional seat row (§8.6 "Each player row is
// positional"). Field order is wire order.
type MatchSeat struct {
	Role      MatchRole
	Side      uint8
	Color     uint8 // 0..9
	AllyGroup uint8 // 0..4 a team, 5 none
	// Nickname is valid UTF-8 without NUL, at most 16 bytes. A name a local
	// byte truncation left invalid is corrected before it is resolved, never
	// hashed differently by platform.
	Nickname string
	// Metal and Energy are the finalized starting resources, nonnegative. An
	// explicit zero stays zero.
	Metal, Energy int32

	// Participant is nonzero and unique for a human or a watcher and zero
	// for every other role.
	Participant MatchParticipantID
	// HostSeat names the initially human row that added this computer, the
	// seat whose perspective it borrows (§6.6, Q4). Every other role holds
	// MatchHostNone.
	HostSeat uint8
	// ComputerKind is an added computer's controller, Classic or Modern; zero
	// for every other role.
	ComputerKind ai.Controller
	// Difficulty is an added computer's own difficulty, 0 easy, 1 medium,
	// 2 hard: the only difficulty an online battle has (§6.6, §15 Q28). Zero
	// for every other role.
	Difficulty uint8

	// SharedVictory is the starting shared-victory bit: set exactly when the
	// row's team holds at least two seats [05 R-SHARE-01 §1].
	SharedVictory bool
	// BuilderOptions are the row's six Guard/Patrol values (§7.4.2 kind 32).
	// A human seat carries its own preference; every other role carries the
	// patch defaults, which are all battle entry gives it.
	BuilderOptions orders.BuilderOptions

	// AIParams are an added computer's effective Modern AI parameters, the
	// All, difficulty and seat layers already merged, in ascending key order.
	// Every other role carries none.
	AIParams []AIParam

	// Start is the row's chosen start point, required extension 17: unset
	// leaves it to Location; set, X and Z are world pixels below
	// StartPointLimit. Only a human or computer row of a skirmish chooses
	// one, under a rule set that honours it (docs/DESIGN_SESSIONS_AI_SAVE.md
	// "Modern chosen start positions").
	Start StartPoint
}

// MatchMod is field 8: the selected mod's public identity. Base content is
// the zero value; a selected mod names all three.
type MatchMod struct {
	ID      string
	Version string
	Archive [32]byte
}

// MatchDirectory is one row of a content profile's directory table: the
// retail directory a loader asks for and the directory the content set
// ships.
type MatchDirectory struct {
	From string
	To   string
}

// MatchContentProfile is field 9: the content profile's name, directory
// table and effective limits. The limits are metadata for admission against
// the local profile, never a remote instruction to allocate.
type MatchContentProfile struct {
	Name        string
	Directories []MatchDirectory
	Units       uint32
	Weapons     uint32
	TNTBytes    uint64
	LOSBytes    uint64
}

// MatchUnitRestriction is one record of field 12. Omitting a definition
// leaves it unrestricted; a Limit of zero forbids it.
type MatchUnitRestriction struct {
	DefinitionID uint16
	Unit         string
	Limit        uint8
}

// MatchContentKind is a field-16 entry's kind (§8.6, §16.8).
type MatchContentKind uint8

const (
	// MatchContentBaseArchive is a retail-format archive in the base
	// install's folder, named by its file name.
	MatchContentBaseArchive MatchContentKind = 1
	// MatchContentAddon is an official add-on, named by its catalogue id
	// (DESIGN_MODS_MUTATORS §5.7).
	MatchContentAddon MatchContentKind = 2
	// MatchContentMapPackage is a downloaded map package, named by its
	// catalogue id (DESIGN_MODS_MUTATORS §5.6).
	MatchContentMapPackage MatchContentKind = 3
)

// MatchContentEntry is one entry of field 16, the room content: what one
// seat must mount for the battle. A base archive carries its file name, no
// version, and the file's size and SHA-256; an add-on or map package its
// catalogue id and version and its archive's size and SHA-256.
type MatchContentEntry struct {
	Kind    MatchContentKind
	Name    string
	Version string
	Size    uint64
	Digest  [32]byte
}

// MatchView is one view record of field 14, in the camera's 1/1024 zoom
// units. The native no-zoom-out preset is a minimum of 1024 without the full
// map.
type MatchView struct {
	MinimumScale uint16
	MaximumScale uint16
	FullMap      bool
}

// MatchPolicies is field 15. Revision 1 admits only policy 1 for each of
// scheduling (casual earliest-unsealed tick), pacing (normal speed, no
// pause), drop (§11.1 drop policy 1) and audience (§11.4); the three
// durations are the room's own values, never a default of this package.
type MatchPolicies struct {
	Revision                       uint16
	Scheduling                     uint8
	Pacing                         uint8
	Drop                           uint8
	Audience                       uint8
	RejoinGraceMilliseconds        uint32
	SpectatorDelayMilliseconds     uint32
	ReplayReleaseDelayMilliseconds uint32
}

// The one supported value of each policy field in configuration version 1.
const (
	MatchPolicyRevision   uint16 = 1
	MatchPolicyScheduling uint8  = 1
	MatchPolicyPacing     uint8  = 1
	MatchPolicyDrop       uint8  = 1
	MatchPolicyAudience   uint8  = 1
)

// MatchConfigRequest is the public, positional form of §8.6: one field or
// record per table row, in wire order, with no defaults. Seats holds exactly
// NumPlayers rows in slot order.
type MatchConfigRequest struct {
	SessionKind MatchSessionKind // 1

	RuleName string // 2
	RuleBase MatchRuleBase

	MapName   string // 3
	MapSchema uint32

	Seats []MatchSeat // 4

	Location       uint8 // 5
	CommanderDeath uint8
	Mapping        uint8
	LineOfSight    uint8
	LOSType        uint8

	UnitLimit      uint16 // 6
	SimulationSeed uint32
	CRTSeed        uint32

	SurvivalPace    survival.Pace // 7
	SurvivalNoAir   bool
	SurvivalNoNaval bool

	Mod MatchMod // 8

	ContentProfile MatchContentProfile // 9

	Community community.Features // 10

	// Mutators is field 11, except Builder speed, which has its own step
	// list and travels as required extension 19, and the Smart makers
	// switch, extension 21 (match_config_setup_extensions.go).
	Mutators content.Mutators // 11

	UnitRestrictions []MatchUnitRestriction // 12

	CheatsAllowed   bool // 13
	WatchingAllowed bool

	PlayerView    MatchView // 14
	SpectatorView MatchView
	ReplayView    MatchView

	Policies MatchPolicies // 15

	// RoomContent is field 16 in canonical order (§16.8). Empty means the
	// battle composes from the seat's own mounted content, as a
	// single-player recording or a command-line play test does.
	RoomContent []MatchContentEntry // 16

	// Extensions holds opaque records in the version-2 tail after fields 1–16
	// (docs/DESIGN_MULTIPLAYER.md §8.6), in ascending ID order. Resolution
	// projects recognized records into Start/AlliedSight and removes them
	// from this slice; encoding reconstructs them from those typed fields.
	Extensions []MatchConfigExtension

	// AlliedSight is the typed value of required extension 18.
	AlliedSight MatchAlliedSight

	// GeneratedMap is the typed value of required extension 20: the recipe
	// of the generated map the battle is played on, as the host wrote it
	// (docs/DESIGN_MAP_GENERATOR.md §10); empty for any other map. It
	// changes nothing a tick computes, since the map's own bytes are the
	// content identity's, but a build that cannot make the map again from it
	// refuses the configuration rather than play another map of that name.
	GeneratedMap []byte
}

// MatchAlliedSight is required extension 18: whether a skirmish's teams share sight and
// radar (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern allied sight",
// DESIGN_MULTIPLAYER §6.7 "Allied sight"). Its zero value is version 1's
// rule, which differs by how the battle is played, so every configuration
// version 1 describes keeps its meaning: an online skirmish's teams share
// and a single-player battle's do not. The adapters write zero for that rule
// and an explicit value only for the other choice.
type MatchAlliedSight uint8

const (
	MatchAlliedSightVersion1 MatchAlliedSight = 0
	MatchAlliedSightSeparate MatchAlliedSight = 1
	MatchAlliedSightShared   MatchAlliedSight = 2
)

// Shared reports whether teams share sight in a battle played online or
// single-player.
func (a MatchAlliedSight) Shared(online bool) bool {
	switch a {
	case MatchAlliedSightSeparate:
		return false
	case MatchAlliedSightShared:
		return true
	}
	return online
}

// EffectiveMatchConfig is a resolved, validated and frozen configuration.
// Its storage is private: Request returns a deep copy, and the encoding it
// was frozen with is what Digest hashes and EncodeMatchConfig returns. The
// zero value is no configuration.
type EffectiveMatchConfig struct {
	request  MatchConfigRequest
	encoding []byte
	// version is 1 only for a historical replay configuration. Zero and 2
	// use the current encoding and identity domain.
	version uint16
}

// Protocol limits of configuration version 2 (§8.6). They are admission
// bounds, not retail claims.
const (
	matchConfigMaxBytes      = 2 << 20
	matchMinSeats            = 2
	matchNicknameMaxBytes    = 16
	matchMapNameMaxBytes     = 1024
	matchModTextMaxBytes     = 255
	matchProfileNameMaxBytes = 255
	matchDirFromMaxBytes     = 255
	matchDirToMaxBytes       = 1024
	matchMaxDirectories      = 64
	matchCommunityMaxBytes   = 16 << 10
	matchMaxRestrictions     = 65535
	matchRestrictionMaxLimit = 100
	matchMaxAIParams         = 128
	matchAIParamMaxBytes     = 32
	matchAIParamsRowMaxBytes = 8192
	matchMinUnitLimit        = 20
	matchMaxUnitLimit        = 3276
	matchViewMinScale        = 64
	matchViewMaxScale        = 2048
	matchKeyMaxBytes         = 255
	matchMaxRoomContent      = 128
	matchContentNameMaxBytes = 255
	matchContentVersionBytes = 64
)

// matchMutatorSteps is field 11's step table: index i is the factor the
// index names. It is a wire table written out here rather than derived from
// content.MutatorSteps, so a reordering there cannot renumber the protocol.
// Index 3 is the identity, which the zero Factor and 1/1 both encode.
var matchMutatorSteps = [...]content.Factor{{Num: 1, Den: 4}, {Num: 1, Den: 2}, {Num: 3, Den: 4}, {Num: 1, Den: 1}, {Num: 3, Den: 2}, {Num: 2, Den: 1}, {Num: 3, Den: 1}, {Num: 4, Den: 1}}

const matchMutatorIdentity = 3

// matchMutatorFields lists field 11's eleven factors in wire order:
// BuildSpeed, BuildCost, Health, Damage, AreaOfEffect, Sight, Radar, Income,
// Salvage, FireRate, UnitSpeed.
func matchMutatorFields(m *content.Mutators) [11]*content.Factor {
	return [11]*content.Factor{&m.BuildSpeed, &m.BuildCost, &m.Health, &m.Damage, &m.AreaOfEffect, &m.Sight, &m.Radar, &m.Income, &m.Salvage, &m.FireRate, &m.UnitSpeed}
}

// matchMutatorIndex is a factor's step index; ok is false for a factor off
// the step list.
func matchMutatorIndex(f content.Factor) (uint8, bool) {
	if f.IsIdentity() {
		return matchMutatorIdentity, true
	}
	for i, step := range matchMutatorSteps {
		if f == step {
			return uint8(i), true
		}
	}
	return 0, false
}

// matchMutatorFactor is the canonical factor of a step index: the zero value
// for the identity, as content.Mutators.SetFactor stores it.
func matchMutatorFactor(index uint8) (content.Factor, bool) {
	if int(index) >= len(matchMutatorSteps) {
		return content.Factor{}, false
	}
	if index == matchMutatorIdentity {
		return content.Factor{}, true
	}
	return matchMutatorSteps[index], true
}

// matchRuleBaseFor is the wire spelling of a registered set's reserved base.
func matchRuleBaseFor(mode gameplay.Mode) (MatchRuleBase, bool) {
	switch mode {
	case gameplay.Strict31:
		return MatchBaseStrict31, true
	case gameplay.Modern:
		return MatchBaseModern, true
	case gameplay.Community39:
		return MatchBaseCommunity39, true
	}
	return 0, false
}

// matchComputersPerHost is how many computer seats one human may add under
// the configuration's rule set, as its seat seam answers (§6.6, Q23): one
// under Strict 3.1, up to the available seats under Modern and Community
// 3.9, and whatever a registered set composes.
func matchComputersPerHost(set RuleSet) int {
	return set.Seats.ComputerSeatsPerHuman()
}

// ResolveMatchConfig validates a request and freezes it. The result owns a
// deep copy; the request's slices may be reused afterwards. A request whose
// mutator factors spell the identity as 1/1 resolves to the same value as one
// that leaves them zero; every other field is taken as written.
func ResolveMatchConfig(r MatchConfigRequest) (EffectiveMatchConfig, error) {
	return resolveMatchConfig(r, 2)
}

func resolveMatchConfig(r MatchConfigRequest, version uint16) (EffectiveMatchConfig, error) {
	c := r.clone()
	if err := c.consumeSetupExtensions(); err != nil {
		return EffectiveMatchConfig{}, err
	}
	if version == 1 && (c.hasChosenStarts() || c.AlliedSight != MatchAlliedSightVersion1 || !c.Mutators.BuilderSpeed.IsIdentity() || !c.Mutators.SmartMakers.IsIdentity() || len(c.GeneratedMap) != 0) {
		return EffectiveMatchConfig{}, matchFieldError("configuration.version", "version 2 for chosen starts, allied sight, Builder speed, Smart makers or a generated map")
	}
	if _, err := c.validate(); err != nil {
		return EffectiveMatchConfig{}, err
	}
	// The identity factor's two spellings are one value (§8.6 field 11), and
	// so are an empty collection and none: the encoding cannot tell them
	// apart, so neither can the frozen value.
	for _, f := range matchMutatorFields(&c.Mutators) {
		if f.IsIdentity() {
			*f = content.Factor{}
		}
	}
	if c.Mutators.BuilderSpeed.IsIdentity() {
		c.Mutators.BuilderSpeed = content.Factor{}
	}
	if c.Mutators.SmartMakers.IsIdentity() {
		c.Mutators.SmartMakers = content.Factor{}
	}
	for i := range c.Seats {
		if len(c.Seats[i].AIParams) == 0 {
			c.Seats[i].AIParams = nil
		}
	}
	if len(c.ContentProfile.Directories) == 0 {
		c.ContentProfile.Directories = nil
	}
	if len(c.UnitRestrictions) == 0 {
		c.UnitRestrictions = nil
	}
	if len(c.RoomContent) == 0 {
		c.RoomContent = nil
	}
	encoding, err := encodeMatchRequest(&c, version)
	if err != nil {
		return EffectiveMatchConfig{}, err
	}
	if len(encoding) > matchConfigMaxBytes {
		return EffectiveMatchConfig{}, matchFieldError("configuration", fmt.Sprintf("an encoding of at most %d bytes, got %d", matchConfigMaxBytes, len(encoding)))
	}
	return EffectiveMatchConfig{request: c, encoding: encoding, version: version}, nil
}

// Request returns a deep copy of the resolved value. ResolveMatchConfig
// resolves that value under the current version; a legacy replay retains its
// original identity only in the EffectiveMatchConfig returned by decoding.
func (c EffectiveMatchConfig) Request() MatchConfigRequest { return c.request.clone() }

// clone deep-copies every slice the request holds.
func (r MatchConfigRequest) clone() MatchConfigRequest {
	out := r
	if r.Seats != nil {
		out.Seats = make([]MatchSeat, len(r.Seats))
		for i, seat := range r.Seats {
			if seat.AIParams != nil {
				seat.AIParams = append([]AIParam{}, seat.AIParams...)
			}
			out.Seats[i] = seat
		}
	}
	if r.ContentProfile.Directories != nil {
		out.ContentProfile.Directories = append([]MatchDirectory{}, r.ContentProfile.Directories...)
	}
	if r.UnitRestrictions != nil {
		out.UnitRestrictions = append([]MatchUnitRestriction{}, r.UnitRestrictions...)
	}
	if r.RoomContent != nil {
		out.RoomContent = append([]MatchContentEntry{}, r.RoomContent...)
	}
	out.Extensions = cloneMatchExtensions(r.Extensions)
	if r.GeneratedMap != nil {
		out.GeneratedMap = append([]byte(nil), r.GeneratedMap...)
	}
	return out
}

// matchFieldError is the one diagnostic shape of a rejected configuration.
func matchFieldError(path, expected string) error {
	return fmt.Errorf("nanolathe: match configuration rejected: logical path %s, providers searched [match configuration], expected %s", path, expected)
}

// validate checks every field of §8.6 and returns the rule set the
// configuration names.
func (r *MatchConfigRequest) validate() (RuleSet, error) {
	// 1. Session kind.
	if r.SessionKind != MatchOnlineSkirmish && r.SessionKind != MatchOnlineSurvival {
		return RuleSet{}, matchFieldError("sessionKind", "1 online skirmish or 2 online Survival")
	}
	// 2. Rule set: a registered name, never normalized to Modern, and the
	// base that set declares.
	if err := validateMatchKey("ruleName", r.RuleName); err != nil {
		return RuleSet{}, err
	}
	set, ok := LookupRuleSet(r.RuleName)
	if !ok {
		return RuleSet{}, matchFieldError("ruleName", fmt.Sprintf("a rule set this build registered, one of %s; got %q", strings.Join(RuleSetNames(), ", "), r.RuleName))
	}
	base, ok := matchRuleBaseFor(set.Base)
	if !ok || base != r.RuleBase {
		return RuleSet{}, matchFieldError("ruleBase", fmt.Sprintf("the base rule set %q declares (%d), got %d", r.RuleName, base, r.RuleBase))
	}
	// 3. Map.
	if err := validateMatchText("mapName", r.MapName, matchMapNameMaxBytes); err != nil {
		return RuleSet{}, err
	}
	if r.MapName == "" || strings.TrimSpace(r.MapName) != r.MapName {
		return RuleSet{}, matchFieldError("mapName", "a nonempty logical map name without surrounding space")
	}
	// 4. Seats.
	if err := r.validateSeats(set); err != nil {
		return RuleSet{}, err
	}
	// 5. Rule words. There is no battle-wide difficulty word (§15 Q28).
	if r.Location > 1 {
		return RuleSet{}, matchFieldError("location", "0 randomized or 1 identity")
	}
	if r.CommanderDeath > 2 {
		return RuleSet{}, matchFieldError("commanderDeath", "0, 1 or 2")
	}
	if r.Mapping > 1 || r.LineOfSight > 1 || r.LOSType > 1 {
		return RuleSet{}, matchFieldError("mapping, lineOfSight, losType", "0 or 1 each")
	}
	// 6. Unit limit and seeds. The Community override has already been
	// applied, so a nonzero table limit and the field must agree; seeds are
	// any explicit value, zero included.
	if r.UnitLimit < matchMinUnitLimit || r.UnitLimit > matchMaxUnitLimit {
		return RuleSet{}, matchFieldError("unitLimit", fmt.Sprintf("%d..%d", matchMinUnitLimit, matchMaxUnitLimit))
	}
	if r.Community.UnitLimit != 0 && r.Community.UnitLimit != int(r.UnitLimit) {
		return RuleSet{}, matchFieldError("unitLimit", fmt.Sprintf("the Community table's unit limit %d, already applied", r.Community.UnitLimit))
	}
	// 7. Survival.
	if r.SessionKind == MatchOnlineSkirmish {
		if r.SurvivalPace != 0 || r.SurvivalNoAir || r.SurvivalNoNaval {
			return RuleSet{}, matchFieldError("survival", "all zero for an online skirmish")
		}
	} else if r.SurvivalPace > survival.PaceRelentless {
		return RuleSet{}, matchFieldError("survival.pace", "0 normal, 1 relaxed or 2 relentless")
	}
	// 8. Mod.
	if err := r.validateMod(); err != nil {
		return RuleSet{}, err
	}
	// 9. Content profile.
	if err := r.ContentProfile.validate(); err != nil {
		return RuleSet{}, err
	}
	// 10. Community table.
	if _, err := matchCommunityJSON(set, r.Community); err != nil {
		return RuleSet{}, err
	}
	// 11. Mutators, Builder speed on its own list (extension 19) and the
	// Smart makers switch (extension 21).
	for i, f := range matchMutatorFields(&r.Mutators) {
		if _, ok := matchMutatorIndex(*f); !ok {
			return RuleSet{}, matchFieldError(fmt.Sprintf("mutators[%d]", i), "the identity or a factor on the mutator step list")
		}
	}
	if _, ok := content.BuilderSpeedStepIndex(r.Mutators.BuilderSpeed); !ok {
		return RuleSet{}, matchFieldError("mutators.builderSpeed", "the identity or a factor on the Builder speed step list")
	}
	if f := r.Mutators.SmartMakers; !f.IsIdentity() && f != content.SwitchOn() {
		return RuleSet{}, matchFieldError("mutators.smartMakers", "off or on")
	}
	// 12. Unit restrictions.
	if err := r.validateRestrictions(); err != nil {
		return RuleSet{}, err
	}
	// 13. Permissions: two booleans, nothing to check. Watcher admission is
	// checked with the seats.
	// 14. Views.
	for _, view := range []struct {
		path string
		view MatchView
	}{{"playerView", r.PlayerView}, {"spectatorView", r.SpectatorView}, {"replayView", r.ReplayView}} {
		v := view.view
		if v.MinimumScale < matchViewMinScale || v.MinimumScale > v.MaximumScale || v.MaximumScale > matchViewMaxScale {
			return RuleSet{}, matchFieldError(view.path, fmt.Sprintf("%d <= minimumScale <= maximumScale <= %d", matchViewMinScale, matchViewMaxScale))
		}
	}
	// 15. Policies.
	p := r.Policies
	if p.Revision != MatchPolicyRevision || p.Scheduling != MatchPolicyScheduling || p.Pacing != MatchPolicyPacing || p.Drop != MatchPolicyDrop || p.Audience != MatchPolicyAudience {
		return RuleSet{}, matchFieldError("policies", "revision 1 with scheduling, pacing, drop and audience policy 1")
	}
	// Chosen starts are checked with the seats. Allied sight: Survival's survivors
	// always share (DESIGN_SURVIVAL §4.3), so only a skirmish chooses.
	if r.AlliedSight > MatchAlliedSightShared {
		return RuleSet{}, matchFieldError("alliedSight", "0 version 1's rule, 1 separate or 2 shared")
	}
	if r.SessionKind != MatchOnlineSkirmish && r.AlliedSight != MatchAlliedSightVersion1 {
		return RuleSet{}, matchFieldError("alliedSight", "0 for Survival, whose survivors always share")
	}
	if r.AlliedSight != MatchAlliedSightVersion1 && !seatRulesOf(set).AlliedSightChoice() {
		return RuleSet{}, matchFieldError("alliedSight", fmt.Sprintf("0: rule set %q keeps version 1's sight", r.RuleName))
	}
	// 16. Room content.
	if err := r.validateRoomContent(); err != nil {
		return RuleSet{}, err
	}
	if len(r.GeneratedMap) > matchConfigMaxExtensionLen {
		return RuleSet{}, matchFieldError("generatedMap", fmt.Sprintf("at most %d bytes", matchConfigMaxExtensionLen))
	}
	if err := validateMatchExtensions(r.Extensions); err != nil {
		return RuleSet{}, err
	}
	return set, nil
}

// validateRoomContent checks field 16's schema: at most 128 entries of a
// known kind in ascending kind order and, within a kind, strictly ascending
// names compared without regard to case, so a name appears once per kind. A
// base archive is a plain archive file name with no version; an add-on or
// map package names a version. Whether a seat can mount each one is the
// seat's own question at join (§16.8).
func (r *MatchConfigRequest) validateRoomContent() error {
	if len(r.RoomContent) > matchMaxRoomContent {
		return matchFieldError("roomContent", fmt.Sprintf("at most %d entries", matchMaxRoomContent))
	}
	for i, e := range r.RoomContent {
		at := fmt.Sprintf("roomContent[%d]", i)
		if e.Kind < MatchContentBaseArchive || e.Kind > MatchContentMapPackage {
			return matchFieldError(at+".kind", "1 base archive, 2 official add-on or 3 map package")
		}
		if err := validateMatchText(at+".name", e.Name, matchContentNameMaxBytes); err != nil {
			return err
		}
		if err := validateMatchText(at+".version", e.Version, matchContentVersionBytes); err != nil {
			return err
		}
		if e.Name == "" || strings.TrimSpace(e.Name) != e.Name {
			return matchFieldError(at+".name", "a nonempty name without surrounding space")
		}
		if e.Kind == MatchContentBaseArchive {
			if strings.ContainsAny(e.Name, "/\\:") || e.Name == "." || e.Name == ".." || !matchArchiveName(e.Name) {
				return matchFieldError(at+".name", "an archive's file name: an HPI, UFO, CCX or patch-tier GP3, GP4, GPF or SWX")
			}
			if e.Version != "" {
				return matchFieldError(at+".version", "empty for a base archive")
			}
		} else if e.Version == "" {
			return matchFieldError(at+".version", "the catalogue version of an add-on or map package")
		}
		if i > 0 {
			prev := r.RoomContent[i-1]
			if prev.Kind > e.Kind || prev.Kind == e.Kind && strings.ToLower(prev.Name) >= strings.ToLower(e.Name) {
				return matchFieldError(at, "entries in ascending kind order and, within a kind, strictly ascending names compared without regard to case")
			}
		}
	}
	return nil
}

// matchArchiveName reports a file name the base install's mount discovers
// as an archive (vfs.IsArchiveName).
func matchArchiveName(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".hpi", ".ufo", ".ccx", ".gp3", ".gp4", ".gpf", ".swx":
		return true
	}
	return false
}

// validateSeats checks field 4 and every row.
func (r *MatchConfigRequest) validateSeats(set RuleSet) error {
	n := len(r.Seats)
	if n < matchMinSeats || n > SkirmishMaxPlayers {
		return matchFieldError("seats", fmt.Sprintf("%d..%d rows", matchMinSeats, SkirmishMaxPlayers))
	}
	humans := 0
	hosted := [SkirmishMaxPlayers]int{}
	for i := range r.Seats {
		seat := &r.Seats[i]
		path := fmt.Sprintf("seats[%d]", i)
		if err := validateMatchSeat(path, seat); err != nil {
			return err
		}
		switch seat.Role {
		case MatchRoleHuman:
			humans++
		case MatchRoleWatcher:
			if !r.WatchingAllowed {
				return matchFieldError(path+".role", "no watcher row unless watching is allowed")
			}
		case MatchRoleComputer:
			if int(seat.HostSeat) >= n || r.Seats[seat.HostSeat].Role != MatchRoleHuman {
				return matchFieldError(path+".hostSeat", "the row of the human that added this computer")
			}
			hosted[seat.HostSeat]++
			// Computers are excluded from Deathmatch in every online mode in
			// the first releases (§6.6, Q23).
			if r.CommanderDeath == uint8(CommanderDeathDeathmatch) {
				return matchFieldError(path+".role", "no computer seat under the Deathmatch commander-death rule")
			}
		case MatchRoleSurvivalAttacker:
			if r.SessionKind != MatchOnlineSurvival || i != n-1 {
				return matchFieldError(path+".role", "the Survival attacker only as an online Survival battle's last row")
			}
		}
		// Participant identities are unique among the seats that carry one.
		if seat.Participant != (MatchParticipantID{}) {
			for j := 0; j < i; j++ {
				if r.Seats[j].Participant == seat.Participant {
					return matchFieldError(path+".participant", fmt.Sprintf("an identity no other seat holds (seat %d holds it)", j))
				}
			}
		}
	}
	if humans == 0 {
		return matchFieldError("seats", "at least one human seat")
	}
	if err := r.validateSeatStarts(set); err != nil {
		return err
	}
	limit := matchComputersPerHost(set)
	for host, count := range hosted {
		if count > limit {
			return matchFieldError(fmt.Sprintf("seats[%d]", host), fmt.Sprintf("at most %d computer seat(s) added by one human under rule set %q", limit, r.RuleName))
		}
	}
	if r.SessionKind == MatchOnlineSurvival {
		// The attacker is the last row and allied with nobody; every other
		// row is one team (DESIGN_SURVIVAL §4.1, §4.3).
		attacker := r.Seats[n-1]
		if attacker.Role != MatchRoleSurvivalAttacker {
			return matchFieldError(fmt.Sprintf("seats[%d].role", n-1), "the Survival attacker as an online Survival battle's last row")
		}
		if attacker.AllyGroup != SkirmishDefaultAllyGroup {
			return matchFieldError(fmt.Sprintf("seats[%d].allyGroup", n-1), fmt.Sprintf("%d: the attacker is allied with nobody", SkirmishDefaultAllyGroup))
		}
		team := r.Seats[0].AllyGroup
		if team == SkirmishDefaultAllyGroup {
			return matchFieldError("seats[0].allyGroup", "a team 0..4 shared by every survivor")
		}
		for i := 1; i < n-1; i++ {
			if r.Seats[i].AllyGroup != team {
				return matchFieldError(fmt.Sprintf("seats[%d].allyGroup", i), fmt.Sprintf("the survivors' team %d", team))
			}
		}
	}
	// One team holding every seat prevents START [08 "Skirmish
	// configuration"]; every seat in the unassigned group 5 does not.
	allOne := true
	for i := 1; i < n; i++ {
		if r.Seats[i].AllyGroup != r.Seats[0].AllyGroup {
			allOne = false
			break
		}
	}
	if allOne && r.Seats[0].AllyGroup != SkirmishDefaultAllyGroup {
		return matchFieldError("seats", "at least one hostile alliance: one team holds every seat")
	}
	// The starting shared-victory bit is the team rule's, not an independent
	// choice: set for a team of two or more, clear otherwise
	// [05 R-SHARE-01 §1].
	for i := range r.Seats {
		if r.Seats[i].SharedVictory != matchTeamOfTwo(r.Seats, i) {
			return matchFieldError(fmt.Sprintf("seats[%d].sharedVictory", i), "set exactly when the row's team holds two or more seats")
		}
	}
	return nil
}

// validateSeatStarts checks required extension 17: a chosen point is set
// only on a skirmish's human or computer row, under a rule set whose
// SeatRules honours chosen starts, with coordinates below StartPointLimit.
// Survival places its commanders itself (DESIGN_SURVIVAL §4.2), so every
// Survival row holds none.
func (r *MatchConfigRequest) validateSeatStarts(set RuleSet) error {
	for i := range r.Seats {
		p := r.Seats[i].Start
		if !p.Set {
			if p.X != 0 || p.Z != 0 {
				return matchFieldError(fmt.Sprintf("seats[%d].start", i), "zero coordinates for an unset start")
			}
			continue
		}
		path := fmt.Sprintf("seats[%d].start", i)
		if !seatRulesOf(set).ChosenStarts() {
			return matchFieldError(path, fmt.Sprintf("unset: rule set %q assigns every start by Location", r.RuleName))
		}
		if role := r.Seats[i].Role; r.SessionKind != MatchOnlineSkirmish || (role != MatchRoleHuman && role != MatchRoleComputer) {
			return matchFieldError(path, "unset except on a skirmish's human or computer row")
		}
		if p.X < 0 || p.Z < 0 || p.X >= StartPointLimit || p.Z >= StartPointLimit {
			return matchFieldError(path, fmt.Sprintf("coordinates 0..%d", StartPointLimit-1))
		}
	}
	return nil
}

// matchTeamOfTwo reports whether seat i shares a team (an ally group other
// than 5) with another seat.
func matchTeamOfTwo(seats []MatchSeat, i int) bool {
	group := seats[i].AllyGroup
	if group == SkirmishDefaultAllyGroup {
		return false
	}
	for j := range seats {
		if j != i && seats[j].AllyGroup == group {
			return true
		}
	}
	return false
}

// validateMatchSeat checks one row's own fields.
func validateMatchSeat(path string, seat *MatchSeat) error {
	switch seat.Role {
	case MatchRoleHuman, MatchRoleComputer, MatchRoleWatcher, MatchRoleSurvivalAttacker:
	default:
		return matchFieldError(path+".role", "1 human, 2 computer, 3 watcher or 4 Survival attacker")
	}
	if seat.Color > 9 {
		return matchFieldError(path+".color", "0..9")
	}
	if seat.AllyGroup > SkirmishDefaultAllyGroup {
		return matchFieldError(path+".allyGroup", "0..5")
	}
	if err := validateMatchText(path+".nickname", seat.Nickname, matchNicknameMaxBytes); err != nil {
		return err
	}
	if seat.Metal < 0 || seat.Energy < 0 {
		return matchFieldError(path+".metal, energy", "nonnegative finalized resources")
	}
	for i := range seat.BuilderOptions.Guard {
		if seat.BuilderOptions.Guard[i] > orders.GuardScatter || seat.BuilderOptions.Patrol[i] > orders.PatrolAssistOnly {
			return matchFieldError(path+".builderOptions", "six values 0..2")
		}
	}
	none := seat.Participant == (MatchParticipantID{})
	switch seat.Role {
	case MatchRoleHuman, MatchRoleWatcher:
		if none {
			return matchFieldError(path+".participant", "a nonzero public identity for a human or watcher seat")
		}
	default:
		if !none {
			return matchFieldError(path+".participant", "zero for a computer or the Survival attacker")
		}
	}
	if seat.Role == MatchRoleComputer {
		if seat.ComputerKind != ai.ControllerClassic && seat.ComputerKind != ai.ControllerModern {
			return matchFieldError(path+".computerKind", "0 Classic or 1 Modern")
		}
		if seat.Difficulty > 2 {
			return matchFieldError(path+".difficulty", "0 easy, 1 medium or 2 hard")
		}
		if err := validateMatchAIParams(path+".aiParams", seat.AIParams); err != nil {
			return err
		}
	} else {
		// A field no reader consults for this role holds its canonical zero.
		if seat.HostSeat != MatchHostNone || seat.ComputerKind != 0 || seat.Difficulty != 0 {
			return matchFieldError(path, "hostSeat 255, computerKind 0 and difficulty 0 for a seat that is not an added computer")
		}
		if len(seat.AIParams) != 0 {
			return matchFieldError(path+".aiParams", "none for a seat that is not an added computer")
		}
	}
	// Non-human rows carry canonical unused padding. Battle entry supplies
	// their bound orders rules' defaults (builder_options.go).
	if seat.Role != MatchRoleHuman && seat.BuilderOptions != orders.DefaultBuilderOptions() {
		return matchFieldError(path+".builderOptions", "canonical builder-option padding for a seat that is not human")
	}
	// The attacker has no economy, so its starting resources are read by
	// nothing (DESIGN_SURVIVAL §4.1).
	if seat.Role == MatchRoleSurvivalAttacker && (seat.Metal != 0 || seat.Energy != 0) {
		return matchFieldError(path+".metal, energy", "zero for the Survival attacker, which has no economy")
	}
	return nil
}

// validateMatchAIParams checks one computer's effective parameters: at most
// 128 pairs in strictly ascending key order, each key and value a canonical
// token of at most 32 bytes that the controller's own vocabulary accepts,
// and at most 8192 encoded bytes in all.
func validateMatchAIParams(path string, params []AIParam) error {
	if len(params) > matchMaxAIParams {
		return matchFieldError(path, fmt.Sprintf("at most %d pairs", matchMaxAIParams))
	}
	size := matchUvarintLen(uint64(len(params)))
	for i, p := range params {
		at := fmt.Sprintf("%s[%d]", path, i)
		if err := validateMatchText(at+".key", p.Key, matchAIParamMaxBytes); err != nil {
			return err
		}
		if err := validateMatchText(at+".value", p.Value, matchAIParamMaxBytes); err != nil {
			return err
		}
		if !canonicalAIToken(p.Key) || !canonicalAIToken(p.Value) {
			return matchFieldError(at, "a key and a value without spaces, commas or '='")
		}
		if i > 0 && params[i-1].Key >= p.Key {
			return matchFieldError(at+".key", "keys in strictly ascending order")
		}
		if err := utiltac.ValidateParam(p.Key, p.Value); err != nil {
			return matchFieldError(at, "a parameter the Modern AI's vocabulary accepts: "+err.Error())
		}
		size += matchUvarintLen(uint64(len(p.Key))) + len(p.Key) + matchUvarintLen(uint64(len(p.Value))) + len(p.Value)
	}
	if size > matchAIParamsRowMaxBytes {
		return matchFieldError(path, fmt.Sprintf("at most %d encoded bytes, got %d", matchAIParamsRowMaxBytes, size))
	}
	return nil
}

// validateMod checks field 8: base content names nothing, a mod names all
// three of its identities.
func (r *MatchConfigRequest) validateMod() error {
	m := r.Mod
	if err := validateMatchText("mod.id", m.ID, matchModTextMaxBytes); err != nil {
		return err
	}
	if err := validateMatchText("mod.version", m.Version, matchModTextMaxBytes); err != nil {
		return err
	}
	if m == (MatchMod{}) {
		return nil
	}
	if m.ID == "" || m.Version == "" || m.Archive == ([32]byte{}) {
		return matchFieldError("mod", "all of id, version and archive digest for a selected mod, or none for base content")
	}
	return nil
}

// validate checks field 9.
func (p *MatchContentProfile) validate() error {
	if err := validateMatchText("contentProfile.name", p.Name, matchProfileNameMaxBytes); err != nil {
		return err
	}
	if p.Name == "" {
		return matchFieldError("contentProfile.name", "the profile's name (retail for the base game)")
	}
	if len(p.Directories) > matchMaxDirectories {
		return matchFieldError("contentProfile.directories", fmt.Sprintf("at most %d rows", matchMaxDirectories))
	}
	for i, d := range p.Directories {
		at := fmt.Sprintf("contentProfile.directories[%d]", i)
		if err := validateMatchText(at+".from", d.From, matchDirFromMaxBytes); err != nil {
			return err
		}
		if err := validateMatchText(at+".to", d.To, matchDirToMaxBytes); err != nil {
			return err
		}
		// From is a canonical logical key, a single directory. Lookups are
		// case-insensitive, so To is canonical without ASCII capitals: two
		// spellings of one directory are one table.
		if d.From == "" || content.CanonicalKey(d.From) != d.From || strings.ContainsAny(d.From, "/\\") {
			return matchFieldError(at+".from", "a canonical single retail directory name")
		}
		if d.To == "" || strings.TrimSpace(d.To) != d.To || strings.ContainsAny(d.To, "/\\") || hasASCIIUpper(d.To) {
			return matchFieldError(at+".to", "a single shipped directory name in lower case without surrounding space")
		}
		if strings.EqualFold(d.From, d.To) {
			return matchFieldError(at, "no row mapping a directory to itself")
		}
		if i > 0 && p.Directories[i-1].From >= d.From {
			return matchFieldError(at+".from", "rows in strictly ascending retail-name order")
		}
	}
	if p.Units < 1 || p.Units > content.MaxDefinitionDomain {
		return matchFieldError("contentProfile.units", fmt.Sprintf("1..%d", content.MaxDefinitionDomain))
	}
	if p.Weapons < 1 || p.Weapons > math.MaxInt32 {
		return matchFieldError("contentProfile.weapons", fmt.Sprintf("1..%d", math.MaxInt32))
	}
	if p.TNTBytes < 1 || p.TNTBytes > math.MaxInt64 || p.LOSBytes < 1 || p.LOSBytes > math.MaxInt64 {
		return matchFieldError("contentProfile.tntBytes, losBytes", fmt.Sprintf("1..%d each", int64(math.MaxInt64)))
	}
	return nil
}

func hasASCIIUpper(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			return true
		}
	}
	return false
}

// validateRestrictions checks field 12's schema. Whether each key names the
// definition its identifier does is RestrictionsFromMatch's question against
// the unrestricted catalog, which FreezeMatchInputs asks, and admission
// compares the set with the frozen inputs' (§16.6, DESIGN_MODS_MUTATORS §15.3).
func (r *MatchConfigRequest) validateRestrictions() error {
	if len(r.UnitRestrictions) > matchMaxRestrictions {
		return matchFieldError("unitRestrictions", fmt.Sprintf("at most %d records", matchMaxRestrictions))
	}
	for i, u := range r.UnitRestrictions {
		at := fmt.Sprintf("unitRestrictions[%d]", i)
		if u.DefinitionID == 0 || (i > 0 && r.UnitRestrictions[i-1].DefinitionID >= u.DefinitionID) {
			return matchFieldError(at+".definitionID", "nonzero definition IDs in strictly ascending order")
		}
		if err := validateMatchKey(at+".unit", u.Unit); err != nil {
			return err
		}
		if u.Limit > matchRestrictionMaxLimit {
			return matchFieldError(at+".limit", fmt.Sprintf("0..%d", matchRestrictionMaxLimit))
		}
	}
	return nil
}

// matchCommunityJSON is field 10: the resolved table's canonical JSON, the
// bytes community.Features.Digest hashes. Strict 3.1 requires exactly the
// zero table; every other base requires a complete table within the feature
// bounds, whose repair multipliers are positive.
func matchCommunityJSON(set RuleSet, f community.Features) ([]byte, error) {
	if set.Base == gameplay.Strict31 {
		if f != (community.Features{}) {
			return nil, matchFieldError("community", "the zero feature table under Strict 3.1")
		}
	} else {
		resolved, err := community.Resolve(false, community.Overrides{Base: &f})
		if err != nil {
			return nil, matchFieldError("community", "a complete feature table within the feature bounds: "+err.Error())
		}
		if resolved != f {
			return nil, matchFieldError("community", "a complete feature table that resolves to itself")
		}
	}
	data, err := json.Marshal(f)
	if err != nil {
		return nil, matchFieldError("community", "a table with a JSON form: "+err.Error())
	}
	if len(data) > matchCommunityMaxBytes {
		return nil, matchFieldError("community", fmt.Sprintf("at most %d bytes of JSON", matchCommunityMaxBytes))
	}
	return data, nil
}

// decodeMatchCommunity reads field 10's JSON closed: unknown or repeated
// keys, another spelling of a key, trailing data and any non-canonical form
// are refused because only the canonical encoding re-encodes to the same
// bytes.
func decodeMatchCommunity(data []byte) (community.Features, error) {
	var f community.Features
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&f); err != nil {
		return community.Features{}, matchFieldError("community", "the canonical community.Features JSON: "+err.Error())
	}
	if decoder.More() || decoder.InputOffset() != int64(len(data)) {
		return community.Features{}, matchFieldError("community", "exactly one JSON object")
	}
	again, err := json.Marshal(f)
	if err != nil || !bytes.Equal(again, data) {
		return community.Features{}, matchFieldError("community", "the table's exact canonical JSON")
	}
	return f, nil
}

// validateMatchText checks a text(N) value: valid UTF-8 without NUL, at most
// max bytes.
func validateMatchText(path, s string, max int) error {
	if len(s) > max {
		return matchFieldError(path, fmt.Sprintf("at most %d bytes", max))
	}
	if !utf8.ValidString(s) || strings.IndexByte(s, 0) >= 0 {
		return matchFieldError(path, "valid UTF-8 without NUL")
	}
	return nil
}

// validateMatchKey checks a key value (§7.4.1): 1..255 bytes of the canonical
// content key content.CanonicalKey returns, with no NUL. Bytes above ASCII
// are kept as they stand; a key need not be UTF-8.
func validateMatchKey(path, s string) error {
	if len(s) < 1 || len(s) > matchKeyMaxBytes || strings.IndexByte(s, 0) >= 0 || content.CanonicalKey(s) != s {
		return matchFieldError(path, fmt.Sprintf("a canonical content key of 1..%d bytes without NUL", matchKeyMaxBytes))
	}
	return nil
}
