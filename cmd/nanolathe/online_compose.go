package main

// Online match composition shared by the command-line play test and the MULTI
// menu (DESIGN_MULTIPLAYER §16.4.2, §16.6). A configuration is frozen once,
// by the host; every seat composes from it, never from its own preferences.

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/netproto"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/version"
)

// onlineMatchSpec is everything the host freezes into a configuration beyond
// the fixed two-human Modern shape of §16.4: the map, the seed pair, the
// content transformations and the mounted mod.
type onlineMatchSpec struct {
	mapName          string
	simSeed, crtSeed uint32
	mutators         content.Mutators
	restrictions     []session.MatchUnitRestriction
	mod              session.MatchMod
	// restrictionSet is the set the restriction records describe; the request
	// adapter cross-checks the two.
	restrictionSet content.Restrictions
	// community is the mounted content's own Community table (its config's
	// rules); the player's personal overrides never enter a configuration.
	community []community.Overrides
}

// onlineMatchConfig resolves the fixed two-human Modern configuration of
// §16.4 for spec: two hostile human seats, cheats and watching off, the full
// camera range, revision-1 policies and the default builder options for
// both seats.
func onlineMatchConfig(spec onlineMatchSpec, cs *contentSet, schema uint32) (session.EffectiveMatchConfig, error) {
	setup := session.DirectSkirmishConfig(spec.mapName)
	setup.Gameplay = gameplay.Modern
	setup.RNGSimSeed, setup.RNGCrtSeed = spec.simSeed, spec.crtSeed
	view := session.MatchView{MinimumScale: 64, MaximumScale: 2048, FullMap: true}
	room := session.MatchRoomInputs{MapSchema: schema, Mod: spec.mod, ContentProfile: cs.profile,
		PlayerView: view, SpectatorView: view, ReplayView: view, UnitRestrictions: spec.restrictions,
		Policies: session.MatchPolicies{Revision: 1, Scheduling: 1, Pacing: 1, Drop: 1, Audience: 1, RejoinGraceMilliseconds: 90000}}
	room.Participants[0][0] = 1
	options := session.SkirmishEntryOptions{ContentLimits: cs.limits, Mutators: spec.mutators, Restrictions: spec.restrictionSet}
	if len(spec.community) != 0 {
		options.CommunitySources = session.CommunitySources{Content: spec.community}
	}
	request, err := session.NewMatchConfigRequest(setup, options, room)
	if err != nil {
		return session.EffectiveMatchConfig{}, err
	}
	second := &request.Seats[1]
	second.Role, second.HostSeat = session.MatchRoleHuman, session.MatchHostNone
	second.ComputerKind, second.Difficulty, second.AIParams = 0, 0, nil
	second.Participant[0] = 2
	second.BuilderOptions = request.Seats[0].BuilderOptions
	return session.ResolveMatchConfig(request)
}

// matchModOf is a mounted mod's public identity (configuration field 8): its
// id, version and the archive digest its install receipt records. A
// directory install has no archive digest. Base content is the zero value.
func matchModOf(mod *modlibrary.Mod) session.MatchMod {
	if mod == nil {
		return session.MatchMod{}
	}
	out := session.MatchMod{ID: mod.ID, Version: mod.Version}
	if sum, err := hex.DecodeString(mod.Receipt.SHA256); err == nil && len(sum) == len(out.Archive) {
		copy(out.Archive[:], sum)
	}
	return out
}

// onlineContentRefusal reports content an online seat cannot describe to its
// peer: a command line that stacked its own roots, or a config file that is
// not an installed mod's own. Base content and an installed mod are allowed.
func onlineContentRefusal(cs *contentSet) error {
	if cs == nil || cs.fs == nil {
		return unavailableBattleContentError()
	}
	if cs.manualRoots || cs.configPath != "" {
		return localMultiplayerError("mounted content", "the base game or an installed mod, without extra --root or --mod-config content")
	}
	return nil
}

// onlineMatchRestrictions maps the host's unit restrictions to configuration
// field 12 against cat, the unrestricted compiled catalog whose record
// indices the records name (DESIGN_MODS_MUTATORS §15.3, DESIGN_MULTIPLAYER
// §16.6). Nothing is seeded: an empty set gives no records. A set the catalog
// cannot take is refused, wrapping *content.RestrictionsError.
func onlineMatchRestrictions(r content.Restrictions, cat *content.Catalog) ([]session.MatchUnitRestriction, error) {
	return session.MatchUnitRestrictions(cat, r)
}

// drawOnlineSeeds is the host's seed pair for a new room, drawn when the
// configuration is frozen. This is §16.6's temporary departure from §8.3:
// the relay draws the pair once the two-stage start exists. The configuration
// digest covers both seeds, so both seats agree on them.
func drawOnlineSeeds() (sim, crt uint32, err error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, 0, fmt.Errorf("nanolathe: online seeds: %w", err)
	}
	return binary.LittleEndian.Uint32(b[:4]), binary.LittleEndian.Uint32(b[4:]), nil
}

// composeOnlineMatch freezes the inputs config names from cs, reports this
// seat's identity and composes the battle prepared for its first grant. It
// returns the frozen inputs too, which the pre-start rehearsal reuses. The
// build is advisory: the rehearsal, not a stamp, shows whether two seats
// simulate alike (DESIGN_MULTIPLAYER §16.7).
func composeOnlineMatch(cs *contentSet, cat *content.Catalog, config session.EffectiveMatchConfig, seat uint8) (*session.Session, *content.SimulationInputs, netproto.Identity, error) {
	var identity netproto.Identity
	if err := onlineContentRefusal(cs); err != nil {
		return nil, nil, identity, err
	}
	// An unreadable stamp still names the running toolchain, which is all
	// an advisory build identity needs.
	build, _ := version.CurrentBuildManifest()
	var err error
	if cat == nil {
		if cat, err = cs.compileCatalog(nil); err != nil {
			return nil, nil, identity, err
		}
	}
	inputs, err := session.FreezeMatchInputs(cs.fs, cat, config, nil)
	if err != nil {
		return nil, nil, identity, err
	}
	identity, err = (session.MatchJoin{Build: session.MatchBuild{Running: build}, Inputs: inputs, Config: config, Mod: matchModOf(cs.mod)}).Identity()
	if err != nil {
		return nil, nil, identity, err
	}
	sess, err := session.NewPlaytestSkirmish(inputs, config, seat, nil)
	if err != nil {
		return nil, nil, identity, err
	}
	// Entry dispatch only installs the battle state; it runs no tick, so
	// presentation composed after it sees the same world it would have seen
	// before. The driver requires it, and the hello's checksum follows it.
	if err := sess.PrepareGrantedBattle(); err != nil {
		return nil, nil, identity, err
	}
	return sess, inputs, identity, nil
}

// onlineModName names a configuration's mod for the player.
func onlineModName(m session.MatchMod) string {
	if m.ID == "" {
		return "Total Annihilation"
	}
	return strings.TrimSpace(m.ID + " " + m.Version)
}
