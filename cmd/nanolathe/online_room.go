package main

// An online room's configurations (DESIGN_MULTIPLAYER §16.6). The host keeps a
// base configuration in the relay: the map, mod, mutators, restrictions, seeds,
// game type, the room's options and the host's computer players (§6.6). It may
// replace the base until Start. Every seat composes the final configuration
// from the latest base plus the present seats, in ascending seat order as
// slots, with their teams and sides, then the base's computers in their stored
// order, when it presses Ready.

import (
	"crypto/sha256"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/netproto"
	"github.com/nanolathe-gg/nanolathe/internal/relay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/survival"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Seams over the session's online setup and map queries, so tests without
// content can stand in for the map's schema and capacity, or record setups.
var (
	newOnlineMatchRequest = session.NewOnlineMatchRequest
	onlineMapCapacity     = session.OnlineMapCapacity
	onlineMapSchema       = session.OnlineMapSchema
)

// onlineTeams is the team choice: none, then teams 1..5 (§16.6).
const onlineTeams = session.OnlineMaxTeam + 1

// onlineSettings are the host's room settings, which it may change until
// Start. The four standard skirmish options are configuration field 5 in the
// request's own words.
type onlineSettings struct {
	survival       bool
	mapName        string
	pace           survival.Pace
	noAir, noNaval bool
	location       uint8 // 0 random, 1 fixed start positions
	commanderDeath uint8 // 0 the game continues, 1 it ends
	mapping        uint8
	lineOfSight    uint8
	losType        uint8
	// computers are the host's computer players in their stored order
	// (session.OnlineComputersOf), every one hosted by seat 0. Every seat
	// composes them after its present humans. A change replaces the slice,
	// never its elements, so a running Ready job keeps the one it read.
	computers []session.OnlineComputer
}

// onlineSettingsFromSetup are a new room's settings: a skirmish on the host's
// current skirmish map with its skirmish options, normalized as the request
// adapter normalizes them (session.NewMatchConfigRequest).
func onlineSettingsFromSetup(setup session.SkirmishConfig) onlineSettings {
	s := onlineSettings{mapName: setup.MapName, mapping: uint8(setup.Mapping & 1), lineOfSight: uint8(setup.LineOfSight & 1), losType: uint8(setup.LOSType & 1)}
	if setup.Location != 0 {
		s.location = 1
	}
	if session.CommanderDeathMode(setup.CommanderDeath) != session.CommanderDeathContinues {
		s.commanderDeath = 1
	}
	return s
}

// onlineSettingsOf reads a base configuration's settings back.
func onlineSettingsOf(r session.MatchConfigRequest) onlineSettings {
	return onlineSettings{
		survival: r.SessionKind == session.MatchOnlineSurvival, mapName: r.MapName,
		pace: r.SurvivalPace, noAir: r.SurvivalNoAir, noNaval: r.SurvivalNoNaval,
		location: r.Location, commanderDeath: r.CommanderDeath, mapping: r.Mapping,
		lineOfSight: r.LineOfSight, losType: r.LOSType,
		computers: session.OnlineComputersOf(r),
	}
}

// onlinePlaceholderSeats seat a base configuration, which no battle composes:
// the smallest room, two players without teams on the first side, in the two
// lowest colours none of computers holds. A computer keeps its stored colour
// unless a human holds it (session.NewOnlineMatchRequest), so the placeholders
// stand aside and the base stores each computer's colour as the host chose it;
// the final configuration resolves it against the players actually present.
// The lobby never seats more than eight computers beside them
// (onlineMostComputers), so two colours are always free.
func onlinePlaceholderSeats(computers []session.OnlineComputer) []session.OnlineSeat {
	var held [relay.HostedColors]bool
	for _, c := range computers {
		if c.Color < relay.HostedColors {
			held[c.Color] = true
		}
	}
	seats := make([]session.OnlineSeat, 0, 2)
	for c := range uint8(relay.HostedColors) {
		if !held[c] && len(seats) < 2 {
			seats = append(seats, session.OnlineSeat{Color: c})
		}
	}
	return seats
}

// onlineBaseConfig is the host's base configuration for settings: the
// placeholder seats, the computers and the frozen part.
func onlineBaseConfig(cs *contentSet, cat *content.Catalog, settings onlineSettings, frozen onlineFrozen) (session.EffectiveMatchConfig, error) {
	return onlineConfig(cs, cat, settings, onlinePlaceholderSeats(settings.computers), frozen)
}

// onlineFrozen is what a room keeps from its creation and never changes: the
// seeds, the content's identity and transformations, and the request's fixed
// room inputs.
type onlineFrozen struct {
	seeds   [2]uint32
	options session.SkirmishEntryOptions
	room    session.MatchRoomInputs
	// community and unitLimit are the base configuration's resolved table
	// and limit; nil resolves the table from options at creation.
	community *community.Features
	unitLimit uint16
}

// onlineCreationFrozen freezes a new room on the host's content: its mod,
// mutators, restriction records and set, and its own Community table.
func onlineCreationFrozen(cs *contentSet, seeds [2]uint32, mutators content.Mutators, restrictions content.Restrictions, records []session.MatchUnitRestriction) onlineFrozen {
	view := session.MatchView{MinimumScale: 64, MaximumScale: 2048, FullMap: true}
	f := onlineFrozen{
		seeds:   seeds,
		options: session.SkirmishEntryOptions{ContentLimits: cs.limits, Mutators: mutators, Restrictions: restrictions},
		room: session.MatchRoomInputs{Mod: matchModOf(cs.mod), ContentProfile: cs.profile, UnitRestrictions: records,
			PlayerView: view, SpectatorView: view, ReplayView: view,
			Policies: session.MatchPolicies{Revision: 1, Scheduling: 1, Pacing: 1, Drop: 1, Audience: 1, RejoinGraceMilliseconds: 90000}},
	}
	if len(cs.gameplayFeatures) != 0 {
		f.options.CommunitySources = session.CommunitySources{Content: cs.gameplayFeatures}
	}
	return f
}

// onlineFrozenOf is a base configuration's frozen part, as every seat reads it.
func onlineFrozenOf(r session.MatchConfigRequest) onlineFrozen {
	p := r.ContentProfile
	dirs := map[string]string{}
	for _, d := range p.Directories {
		dirs[d.From] = d.To
	}
	features := r.Community
	return onlineFrozen{
		seeds: [2]uint32{r.SimulationSeed, r.CRTSeed},
		options: session.SkirmishEntryOptions{
			ContentLimits: content.Limits{Units: int(p.Units), Weapons: int(p.Weapons), TNTBytes: int64(p.TNTBytes), LOSBytes: int64(p.LOSBytes)},
			Mutators:      r.Mutators,
		},
		room: session.MatchRoomInputs{Mod: r.Mod, ContentProfile: p.Name, ContentDirectories: dirs,
			UnitRestrictions: append([]session.MatchUnitRestriction(nil), r.UnitRestrictions...),
			PlayerView:       r.PlayerView, SpectatorView: r.SpectatorView, ReplayView: r.ReplayView, Policies: r.Policies},
		community: &features,
		unitLimit: r.UnitLimit,
	}
}

// onlineConfig resolves a room configuration on cs and its catalog: the
// settings, with their computers, and seats through the session's online
// request, at the schema the map-entry code selects for its rows, with the
// frozen part carried unchanged.
func onlineConfig(cs *contentSet, cat *content.Catalog, settings onlineSettings, seats []session.OnlineSeat, frozen onlineFrozen) (session.EffectiveMatchConfig, error) {
	setup := session.OnlineMatchSetup{Survival: settings.survival, MapName: settings.mapName, Seats: seats, Computers: settings.computers,
		SimSeed: frozen.seeds[0], CRTSeed: frozen.seeds[1], SideCount: len(session.OnlineSides(cat))}
	if settings.survival {
		setup.SurvivalOptions = session.SurvivalOptions{Enabled: true, Pace: settings.pace, NoAir: settings.noAir, NoNaval: settings.noNaval}
	}
	var fs vfs.FSOps
	if cs != nil {
		fs = cs.fs
	}
	schema, err := onlineMapSchema(fs, cat, settings.mapName, setup.Rows())
	if err != nil {
		return session.EffectiveMatchConfig{}, err
	}
	room := frozen.room
	room.MapSchema = schema
	request, err := newOnlineMatchRequest(setup, frozen.options, room)
	if err != nil {
		return session.EffectiveMatchConfig{}, err
	}
	// The standard options the setup does not carry. Survival ignores start
	// positions, as its single-player screen does (DESIGN_SURVIVAL §9).
	if !settings.survival {
		request.Location = settings.location
	}
	request.CommanderDeath, request.Mapping, request.LineOfSight, request.LOSType = settings.commanderDeath, settings.mapping, settings.lineOfSight, settings.losType
	if frozen.community != nil {
		request.Community, request.UnitLimit = *frozen.community, frozen.unitLimit
	}
	return session.ResolveMatchConfig(request)
}

// onlineSeatsOf are a lobby's present seats in ascending seat order, which is
// slot order, with their teams, sides and colours, and the local seat's slot.
// Survival has no teams.
func onlineSeatsOf(state relay.HostedLobbyState, local uint8, survival bool) ([]session.OnlineSeat, uint8, bool) {
	var seats []session.OnlineSeat
	slot, found := uint8(0), false
	for i := range state.Seats {
		seat := state.Seats[i]
		if !seat.Present {
			continue
		}
		if uint8(i) == local {
			slot, found = uint8(len(seats)), true
		}
		team := seat.Team
		if survival {
			team = 0
		}
		seats = append(seats, session.OnlineSeat{Team: team, Side: seat.Side, Color: seat.Color})
	}
	return seats, slot, found
}

// onlineOneTeam reports a skirmish whose players, the host's computers
// included, all share one team, which cannot start [08 R-SKIR-01 §12].
func onlineOneTeam(seats []session.OnlineSeat, computers []session.OnlineComputer) bool {
	teams := make([]uint8, 0, len(seats)+len(computers))
	for _, s := range seats {
		teams = append(teams, s.Team)
	}
	for _, c := range computers {
		teams = append(teams, c.Team)
	}
	if len(teams) < 2 || teams[0] == session.OnlineTeamNone {
		return false
	}
	for _, team := range teams[1:] {
		if team != teams[0] {
			return false
		}
	}
	return true
}

// onlineMinHumans is how many human players any online battle needs
// (session.NewOnlineMatchRequest).
const onlineMinHumans = 2

// onlinePlayerLimit is the most players, humans and computers together, a
// room's battle seats: Survival's three survivors, or a skirmish's map
// capacity (session.OnlineMapCapacity) within ten. A capacity of 0 is a map
// the lobby could not read, held to ten.
func onlinePlayerLimit(survival bool, capacity int) int {
	if survival {
		return session.OnlineSurvivalMaxSurvivors
	}
	if capacity <= 0 || capacity > session.SkirmishMaxPlayers {
		return session.SkirmishMaxPlayers
	}
	return capacity
}

// onlineMostComputers is how many computers a base configuration holds at
// most: the battle's limit less the two humans every battle needs, so the base
// with its two placeholders always validates and every computer can play.
func onlineMostComputers(survival bool, capacity int) int {
	return onlinePlayerLimit(survival, capacity) - onlineMinHumans
}

// onlineCanAddComputer reports whether the host, with computers already and
// humans players present, may add one more: the room must still have space
// for the two humans every battle needs.
func onlineCanAddComputer(survival bool, capacity, humans, computers int) bool {
	return computers+max(humans, onlineMinHumans) < onlinePlayerLimit(survival, capacity)
}

// onlineComputerColors are the colours the computers take when every seat
// composes the final configuration with these present seats, as
// session.NewOnlineMatchRequest resolves them: a computer keeps its stored
// colour when no seat, and no earlier computer keeping its own, holds it;
// each other computer, in stored order, takes the lowest colour nobody holds.
// The lobby shows these colours, so the rows show the battle's.
func onlineComputerColors(seats []session.OnlineSeat, computers []session.OnlineComputer) []uint8 {
	var held [relay.HostedColors]bool
	for _, s := range seats {
		if s.Color < relay.HostedColors {
			held[s.Color] = true
		}
	}
	colors := make([]uint8, len(computers))
	kept := make([]bool, len(computers))
	for k, c := range computers {
		if c.Color < relay.HostedColors && !held[c.Color] {
			held[c.Color], colors[k], kept[k] = true, c.Color, true
		}
	}
	for k := range computers {
		if kept[k] {
			continue
		}
		for c := range uint8(relay.HostedColors) {
			if !held[c] {
				held[c], colors[k] = true, c
				break
			}
		}
	}
	return colors
}

// onlineHumanCount is how many human rows a composed configuration seats: the
// online players, without the host's computers or Survival's attacker.
func onlineHumanCount(r session.MatchConfigRequest) int {
	n := 0
	for i := range r.Seats {
		if r.Seats[i].Role == session.MatchRoleHuman {
			n++
		}
	}
	return n
}

// onlineIdentityDigest is a seat's configuration-identity digest for Ready:
// SHA-256 over its final MatchJoin identity, encoded in the hello's field
// order under its own domain, with the advisory build zeroed
// (DESIGN_MULTIPLAYER §16.6.1, §16.7). Every seat that composed the same
// configuration from the same content reports the same digest.
func onlineIdentityDigest(id netproto.Identity) [32]byte {
	var w netproto.Writer
	w.Text("nanolathe/online-identity/1")
	w.U16(id.Protocol)
	w.Digest([netproto.DigestBytes]byte{})
	w.Digest(id.Content)
	w.Digest(id.Map)
	w.Text(id.Rules.Name)
	w.U8(id.Rules.Base)
	w.Digest(id.Rules.Community)
	w.Text(id.Mod.ID)
	w.Text(id.Mod.Version)
	w.Digest(id.Mod.Archive)
	w.Digest(id.Configuration)
	return sha256.Sum256(w.Bytes())
}
