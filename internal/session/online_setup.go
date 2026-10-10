package session

import (
	"fmt"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// The online lobby's configuration (DESIGN_MULTIPLAYER §16.6). Every value
// below is Nanolathe lobby policy, not a retail record: the seat-to-row
// mapping, the default names, the colour rule and the team numbering.

// OnlineTeamNone is the lobby team of a seat on no team. Teams 1..5 are the
// configuration's ally groups 0..4; none is the unassigned group 5.
const OnlineTeamNone = 0

// OnlineMaxTeam is the highest lobby team number.
const OnlineMaxTeam = 5

// onlineColors is how many player colours a seat may hold, the configuration
// row's 0..9.
const onlineColors = 10

// OnlineSurvivalMaxSurvivors is how many human survivors an online Survival
// battle seats: the single-player survivor layout's human and buddy rows
// (DESIGN_SURVIVAL §4.1), each a human online.
const OnlineSurvivalMaxSurvivors = 1 + SurvivalMaxBuddies

// OnlineSeat is one present lobby seat, in slot order (DESIGN_MULTIPLAYER
// §16.6). Team is OnlineTeamNone or 1..OnlineMaxTeam; Survival ignores it.
// Side indexes the frozen catalog's sides in their compiled order, the order
// OnlineSides names them. Color is the player's colour, 0..9, held by no
// other seat.
type OnlineSeat struct {
	Team  uint8
	Side  uint8
	Color uint8
}

// OnlineComputer is one computer row the room host added to its base
// configuration (DESIGN_MULTIPLAYER §6.6): its lobby team (as OnlineSeat's,
// ignored in Survival), side, stored colour, controller and difficulty, 0
// easy, 1 medium or 2 hard. Only the host adds computers, so every one is
// hosted by seat 0 and borrows its perspective.
type OnlineComputer struct {
	Team  uint8
	Side  uint8
	Color uint8
	// Kind is the computer's controller: Classic plays the bound rule set's
	// own planner, Modern the Modern AI computer player.
	Kind       ai.Controller
	Difficulty uint8
}

// OnlineMatchSetup is what a lobby decides beyond the host's frozen content:
// the game type, map, seats with their teams and sides, the host's computers,
// the host's seed pair and, for Survival, its options. Every seat is a human
// under Modern gameplay.
type OnlineMatchSetup struct {
	Survival bool
	MapName  string
	Seats    []OnlineSeat
	// Computers are the host's computer rows in their stored order, as
	// OnlineComputersOf reads them from the room's base configuration. They
	// follow the seats, so a human keeps its relay slot as its row.
	Computers        []OnlineComputer
	SimSeed, CRTSeed uint32
	SurvivalOptions  SurvivalOptions
	// SideCount is how many sides the frozen catalog defines,
	// len(OnlineSides(cat)). Every seat's Side must be below it.
	SideCount int
}

// Rows is the configuration's row count for the setup: one human row per
// seat, one computer row per computer and, in Survival, the attacker row
// after them. It is the player count the map-entry code selects the map's
// schema for (OnlineMapSchema); in a skirmish it is also the count the map's
// start positions must hold (OnlineMapCapacity).
func (s OnlineMatchSetup) Rows() int {
	if s.Survival {
		return len(s.Seats) + len(s.Computers) + 1
	}
	return len(s.Seats) + len(s.Computers)
}

// NewOnlineMatchRequest builds the match configuration request for an
// online lobby: one human row per seat in slot order, then one computer row
// per computer in its stored order, and, for Survival, the attacker row as
// SurvivalConfigFor builds it. A human row carries the participant identity
// whose first byte is its slot plus one (room's own Participants are not
// read), the nickname "Player n" for slot n-1, the seat's side and colour,
// and the skirmish default resources. A computer row is hosted by seat 0 and
// carries its controller, its own difficulty, its side, an empty nickname and
// the default resources. A skirmish row's ally group is its team's (team t is
// group t-1, no team is the unassigned group); every survivor, computer
// survivors included, shares the Survival team.
//
// Colours resolve here, for every seat alike: the humans' colours are their
// own and must differ; a computer keeps its stored colour when no human and
// no earlier computer keeping its own holds it, and otherwise, in stored
// order, takes the lowest colour no row holds; the attacker then takes the
// first colour no survivor holds, red when free (SurvivalConfigFor,
// DESIGN_SURVIVAL §4.1). A human-only setup builds exactly the rows it always
// has.
//
// The rule words and the unit limit are the single-player skirmish defaults
// under Modern, as DirectSkirmishConfig writes them; options and room are as
// NewMatchConfigRequest takes them. room.MapSchema must be the schema the
// map-entry code selects for setup.Rows() players; OnlineMapSchema resolves
// it.
//
// A skirmish seats 2..10 humans and at most ten rows, and refuses one team
// holding every row [08 R-SKIR-01 §12]; whether the map offers that many
// start positions is the lobby's cap (OnlineMapCapacity), which composition
// checks again. Survival seats 2..3 human survivors and at most three
// survivors in all. A colour above 9, or one two seats share, is refused.
func NewOnlineMatchRequest(setup OnlineMatchSetup, options SkirmishEntryOptions, room MatchRoomInputs) (MatchConfigRequest, error) {
	n := len(setup.Seats)
	most := SkirmishMaxPlayers
	if setup.Survival {
		most = OnlineSurvivalMaxSurvivors
	}
	if n < matchMinSeats || n > most {
		return MatchConfigRequest{}, matchFieldError("setup.seats", fmt.Sprintf("%d..%d seats", matchMinSeats, most))
	}
	if n+len(setup.Computers) > most {
		return MatchConfigRequest{}, matchFieldError("setup.computers", fmt.Sprintf("at most %d players, seats and computers together", most))
	}
	if setup.SideCount < 1 || setup.SideCount > 256 {
		return MatchConfigRequest{}, matchFieldError("setup.sideCount", "the frozen catalog's side count, len(OnlineSides(cat)), 1..256")
	}
	if strings.TrimSpace(setup.MapName) == "" {
		return MatchConfigRequest{}, matchFieldError("setup.mapName", "a map name")
	}
	cfg := DirectSkirmishConfig(setup.MapName)
	cfg.Gameplay = gameplay.Modern
	cfg.RNGSimSeed, cfg.RNGCrtSeed = setup.SimSeed, setup.CRTSeed
	players := make([]SkirmishPlayer, n, n+len(setup.Computers))
	for i, seat := range setup.Seats {
		path := fmt.Sprintf("setup.seats[%d]", i)
		if seat.Team > OnlineMaxTeam {
			return MatchConfigRequest{}, matchFieldError(path+".team", fmt.Sprintf("%d for none or 1..%d", OnlineTeamNone, OnlineMaxTeam))
		}
		if int(seat.Side) >= setup.SideCount {
			return MatchConfigRequest{}, matchFieldError(path+".side", fmt.Sprintf("a side below the catalog's %d sides", setup.SideCount))
		}
		if seat.Color >= onlineColors {
			return MatchConfigRequest{}, matchFieldError(path+".color", fmt.Sprintf("0..%d", onlineColors-1))
		}
		for j := range i {
			if setup.Seats[j].Color == seat.Color {
				return MatchConfigRequest{}, matchFieldError(path+".color", fmt.Sprintf("a colour no other seat holds, not seat %d's", j))
			}
		}
		players[i] = SkirmishPlayer{
			Controller: SkirmishControllerHuman,
			Side:       int(seat.Side),
			Color:      int(seat.Color),
			AllyGroup:  onlineAllyGroup(seat.Team),
			Metal:      SkirmishDefaultMetal,
			Energy:     SkirmishDefaultEnergy,
			Nickname:   fmt.Sprintf("Player %d", i+1),
		}
		room.Participants[i] = MatchParticipantID{byte(i + 1)}
	}
	var difficulties [SkirmishMaxPlayers]uint8
	colors, err := onlineComputerColors(setup)
	if err != nil {
		return MatchConfigRequest{}, err
	}
	for k, c := range setup.Computers {
		path := fmt.Sprintf("setup.computers[%d]", k)
		if c.Team > OnlineMaxTeam {
			return MatchConfigRequest{}, matchFieldError(path+".team", fmt.Sprintf("%d for none or 1..%d", OnlineTeamNone, OnlineMaxTeam))
		}
		if int(c.Side) >= setup.SideCount {
			return MatchConfigRequest{}, matchFieldError(path+".side", fmt.Sprintf("a side below the catalog's %d sides", setup.SideCount))
		}
		if c.Kind != ai.ControllerClassic && c.Kind != ai.ControllerModern {
			return MatchConfigRequest{}, matchFieldError(path+".kind", "Classic or Modern")
		}
		if c.Difficulty > 2 {
			return MatchConfigRequest{}, matchFieldError(path+".difficulty", "0 easy, 1 medium or 2 hard")
		}
		difficulties[n+k] = c.Difficulty
		players = append(players, SkirmishPlayer{
			Controller: SkirmishControllerComputer,
			Side:       int(c.Side),
			Color:      int(colors[k]),
			AllyGroup:  onlineAllyGroup(c.Team),
			Metal:      SkirmishDefaultMetal,
			Energy:     SkirmishDefaultEnergy,
			AI:         c.Kind,
		})
	}
	rows := len(players)
	for i := n; i < len(room.Participants); i++ {
		room.Participants[i] = MatchParticipantID{}
	}
	for i := range cfg.Players {
		cfg.Players[i] = SkirmishPlayer{}
	}
	cfg.NumPlayers = rows
	if setup.Survival {
		layout := SurvivalConfigFor(setup.MapName, players, setup.SurvivalOptions)
		cfg.Survival = layout.Survival
		cfg.NumPlayers = rows + 1
		for i := range players {
			players[i].AllyGroup = layout.Players[i].AllyGroup
		}
		cfg.Players[rows] = layout.Players[rows]
	}
	copy(cfg.Players[:rows], players)
	return newMatchConfigRequest(cfg, options, room, &difficulties)
}

// onlineComputerColors resolves the computers' colours against the seats'
// (NewOnlineMatchRequest): a stored colour no human holds, and no earlier
// computer keeping its own, is kept; each other computer, in stored order,
// takes the lowest colour no seat or computer holds. Ten rows at most never
// run out of the ten colours.
func onlineComputerColors(setup OnlineMatchSetup) ([]uint8, error) {
	var held [onlineColors]bool
	for _, seat := range setup.Seats {
		if seat.Color < onlineColors {
			held[seat.Color] = true
		}
	}
	colors := make([]uint8, len(setup.Computers))
	kept := make([]bool, len(setup.Computers))
	for k, c := range setup.Computers {
		if c.Color >= onlineColors {
			return nil, matchFieldError(fmt.Sprintf("setup.computers[%d].color", k), fmt.Sprintf("0..%d", onlineColors-1))
		}
		if !held[c.Color] {
			held[c.Color], colors[k], kept[k] = true, c.Color, true
		}
	}
	for k := range setup.Computers {
		if kept[k] {
			continue
		}
		for c := uint8(0); c < onlineColors; c++ {
			if !held[c] {
				held[c], colors[k] = true, c
				break
			}
		}
	}
	return colors, nil
}

// OnlineComputersOf reads the computer rows of a room's base configuration
// in their stored order, the rows every seat composes after its present
// humans (DESIGN_MULTIPLAYER §16.6): each row's team from its ally group
// (group g is team g+1, the unassigned group no team), side, stored colour,
// controller and difficulty. Survival's survivor team reads as no team.
// Rows of other roles are skipped.
func OnlineComputersOf(r MatchConfigRequest) []OnlineComputer {
	var out []OnlineComputer
	for i := range r.Seats {
		seat := &r.Seats[i]
		if seat.Role != MatchRoleComputer {
			continue
		}
		team := uint8(OnlineTeamNone)
		if r.SessionKind == MatchOnlineSkirmish && seat.AllyGroup < SkirmishDefaultAllyGroup {
			team = seat.AllyGroup + 1
		}
		out = append(out, OnlineComputer{Team: team, Side: seat.Side, Color: seat.Color, Kind: seat.ComputerKind, Difficulty: seat.Difficulty})
	}
	return out
}

// onlineAllyGroup is a lobby team's ally group: team t is group t-1 and no
// team the unassigned group.
func onlineAllyGroup(team uint8) int {
	if team == OnlineTeamNone {
		return SkirmishDefaultAllyGroup
	}
	return int(team) - 1
}

// OnlineMapCapacity is the most players an online skirmish on mapName can
// seat: the largest start-position count among its network schemas in the
// compiled catalog's map header, at most 10. Survival ignores start
// positions.
func OnlineMapCapacity(cat *content.Catalog, mapName string) (int, error) {
	if cat == nil {
		return 0, fmt.Errorf("nanolathe: online map capacity: logical path %s, providers searched [catalog], expected a compiled catalog", mapName)
	}
	header := cat.Maps[content.CanonicalKey(mapName)]
	if header == nil {
		return 0, fmt.Errorf("nanolathe: online map capacity: logical path %s, providers searched [catalog maps], expected a compiled map header", mapName)
	}
	best := 0
	for _, schema := range header.Schemas {
		if formats.NetworkSchemaRank(schema.Type) != 0 && schema.StartPosCount > best {
			best = schema.StartPosCount
		}
	}
	if best == 0 {
		return 0, fmt.Errorf("nanolathe: online map capacity: logical path %s, providers searched [catalog map schemas], expected a network schema with start positions", header.LogicalOTA)
	}
	return min(best, SkirmishMaxPlayers), nil
}

// OnlineMapSchema is the configuration's MapSchema for mapName and rows
// players (OnlineMatchSetup.Rows): the index, in the compiled catalog's map
// header, of the network schema the map-entry code selects for that player
// count, which admission requires (ValidateMatchInputs).
func OnlineMapSchema(fs vfs.FSOps, cat *content.Catalog, mapName string, rows int) (uint32, error) {
	if fs == nil || cat == nil {
		return 0, fmt.Errorf("nanolathe: online map schema: logical path %s, providers searched [content], expected a mounted file system and its compiled catalog", mapName)
	}
	m, err := mission.LoadWithType(fs, mission.TypeSkirmish, mapName, 0, rows, nil)
	if err != nil {
		return 0, err
	}
	header := cat.Maps[content.CanonicalKey(m.TerrainKey)]
	if header == nil {
		return 0, fmt.Errorf("nanolathe: online map schema: logical path %s, providers searched [catalog maps], expected a compiled map header", m.TerrainKey)
	}
	index := mapSchemaIndex(header, m.Schema.Name)
	if int(index) >= len(header.Schemas) {
		return 0, fmt.Errorf("nanolathe: online map schema: logical path %s, providers searched [catalog map schemas], expected the selected schema %q", header.LogicalOTA, m.Schema.Name)
	}
	return index, nil
}

// OnlineSides names the frozen catalog's sides in index order, the order an
// OnlineSeat's Side counts: each side's authored name, such as ARM or CORE,
// or "" for a side without one.
func OnlineSides(cat *content.Catalog) []string {
	if cat == nil {
		return nil
	}
	names := make([]string, len(cat.Sides))
	for i, side := range cat.Sides {
		if side != nil {
			names[i] = strings.TrimSpace(side.Name)
		}
	}
	return names
}
