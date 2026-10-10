package headless

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/content/profiles"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// replayView is the view record a single-player battle's configuration
// carries when its host names none: the whole camera range. Single-player
// play never reads it; a replay header needs a valid value.
var replayView = session.MatchView{MinimumScale: 64, MaximumScale: 2048, FullMap: true}

// replayPolicies are configuration field 15's revision-1 values, the only
// ones admission supports, with the online lobby's rejoin grace. A
// single-player battle reads none of them.
var replayPolicies = session.MatchPolicies{Revision: session.MatchPolicyRevision, Scheduling: session.MatchPolicyScheduling,
	Pacing: session.MatchPolicyPacing, Drop: session.MatchPolicyDrop, Audience: session.MatchPolicyAudience, RejoinGraceMilliseconds: 90000}

// MatchConfigForFreshBattle is the battle configuration (DESIGN_MULTIPLAYER
// §8.6) of the skirmish or Survival battle ComposeFreshBattle composes from
// request: the setup normalizeFreshBattleRequest writes and the entry options
// ComposeFreshBattle passes, through the session's local request adapter. A
// replay header records it, and playback composes the same battle from it
// through admission (session.NewAdmittedSkirmish). A campaign mission and an
// automated-players arena have no such configuration and are refused.
//
// The room supplies the mod and the content profile's name and directories.
// The rest of the room is filled here where it is zero: the map schema is
// always the one the map-entry code selects for the battle's seat count from
// the request's content; field 12 is the request's restrictions mapped
// against the unrestricted catalog; each human or watcher row without a
// participant identity takes its slot number plus one; the profile name defaults to the
// base game's; views take the whole camera range and the policies their
// revision-1 values. None of those room values changes the battle a
// single-player entry composes.
//
// The request's Catalog is the unrestricted catalog to read the map header
// and the restriction records from; without one, the request's content is
// compiled here under its ContentLimits, so a host that already compiled one
// passes it. With a Catalog and no ContentLimits, the catalog's own limits
// are the configuration's.
func MatchConfigForFreshBattle(request FreshBattleRequest, room session.MatchRoomInputs) (session.EffectiveMatchConfig, error) {
	kind, identity, cfg, err := normalizeFreshBattleRequest(request)
	if err != nil {
		return session.EffectiveMatchConfig{}, err
	}
	switch kind {
	case ScenarioDirectOTA, ScenarioSkirmish, ScenarioSurvival:
	default:
		return session.EffectiveMatchConfig{}, diagnostic("replay configuration failed: a campaign mission has no battle configuration", identity, nil, "a skirmish or Survival map")
	}
	if request.FS == nil {
		return session.EffectiveMatchConfig{}, diagnostic("replay configuration failed: content mount is unavailable", identity, nil, "a mounted skirmish map")
	}
	cfg.Gameplay = request.Gameplay
	catalog, limits := request.Catalog, request.ContentLimits
	if catalog == nil {
		if catalog, err = content.CompileWithOptions(request.FS, content.Options{Limits: limits}); err != nil {
			return session.EffectiveMatchConfig{}, diagnostic("replay configuration failed: catalog compile failed: "+err.Error(), identity, providersFromOps(request.FS), "a complete compiled catalog")
		}
	} else if limits == (content.Limits{}) {
		limits = catalog.Limits
	}
	if !request.Restrictions.IsZero() && len(room.UnitRestrictions) == 0 {
		if room.UnitRestrictions, err = session.MatchUnitRestrictions(catalog, request.Restrictions); err != nil {
			return session.EffectiveMatchConfig{}, err
		}
	}
	if room.ContentProfile == "" {
		room.ContentProfile = profiles.RetailName
	}
	for _, view := range []*session.MatchView{&room.PlayerView, &room.SpectatorView, &room.ReplayView} {
		if *view == (session.MatchView{}) {
			*view = replayView
		}
	}
	if room.Policies == (session.MatchPolicies{}) {
		room.Policies = replayPolicies
	}
	options := session.SkirmishEntryOptions{
		BuilderOptions:   request.BuilderOptions,
		CommunitySources: request.CommunitySources,
		ContentLimits:    limits,
		Mutators:         request.Mutators,
		Restrictions:     request.Restrictions,
		AIOverrides:      request.AIOverrides,
		AutomatedPlayers: request.AutomatedPlayers,
	}
	r, err := session.NewMatchConfigRequest(cfg, options, room)
	if err != nil {
		return session.EffectiveMatchConfig{}, err
	}
	for i := range r.Seats {
		if seat := &r.Seats[i]; (seat.Role == session.MatchRoleHuman || seat.Role == session.MatchRoleWatcher) && seat.Participant == (session.MatchParticipantID{}) {
			seat.Participant[0] = byte(i + 1)
		}
	}
	// The schema the map-entry code selects for the configuration's seat
	// count, as its index in the compiled map header: what admission
	// requires (session.ValidateMatchInputs).
	m, err := mission.LoadWithType(request.FS, mission.TypeSkirmish, r.MapName, 0, len(r.Seats), nil)
	if err != nil {
		return session.EffectiveMatchConfig{}, diagnostic("replay configuration failed: "+err.Error(), identity, providersFromOps(request.FS), fmt.Sprintf("a map schema for %d seats", len(r.Seats)))
	}
	header := catalog.Maps[content.CanonicalKey(m.TerrainKey)]
	if header == nil {
		return session.EffectiveMatchConfig{}, diagnostic("replay configuration failed: no compiled map header", m.TerrainKey, providersFromOps(request.FS), "the selected map's header in the catalog")
	}
	r.MapSchema = uint32(len(header.Schemas))
	for i, schema := range header.Schemas {
		if schema.Name == m.Schema.Name {
			r.MapSchema = uint32(i)
			break
		}
	}
	if int(r.MapSchema) == len(header.Schemas) {
		return session.EffectiveMatchConfig{}, diagnostic("replay configuration failed: selected schema is not in the map header", m.TerrainKey, providersFromOps(request.FS), fmt.Sprintf("schema %q in the compiled map header", m.Schema.Name))
	}
	return session.ResolveMatchConfig(r)
}
