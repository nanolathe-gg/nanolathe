package mission

import (
	"errors"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
)

// Schema is the selected schema variant [08 "Schema choice"].
// Name is the section name as authored, e.g. "Schema 0".
// StartPositions is the number of StartPos specials in the schema.
// Selection happens before placement records are instantiated, so the
// caller obtains the Schema name first and feeds it to the placement
// builder later [08 "Schema choice"] (C4).
type Schema struct {
	Name           string
	StartPositions int
}

var (
	// ErrNoGlobalHeader is emitted verbatim when [GlobalHeader] is missing [08 "Schema choice"].
	ErrNoGlobalHeader = errors.New("Very bad news! No MSG!")
	// ErrNoSuitableSchema is emitted verbatim when no candidate matches [08 "Schema choice"].
	ErrNoSuitableSchema = errors.New("No suitable schema type...")
)

// SelectSchemaForType selects a schema per [08 "Schema choice"] (C3, C4).
//
// The MODE decides which candidate list is tried, not the OTA's contents:
// campaign mode tries the three difficulty literals in a difficulty-dependent
// permutation, and skirmish/multiplayer modes try `Network 1` through
// `Network 4`. An OTA carrying both families is common, and inferring the mode
// from which family is present lets a skirmish load pick up `Easy` — with it,
// the wrong placements, start positions and starting resources, and no error.
//
// Types 2 and 3 both join the raw OTA path directly [08 "Mission type
// dispatch"] and so share the network candidate list.
//
// Selection is performed before placement records are instantiated; the
// returned Schema.Name is fed to the placement builder later (C4).
func SelectSchemaForType(o *formats.OTA, typ Type, difficulty, players int) (Schema, error) {
	if o == nil || o.Global == nil {
		return Schema{}, ErrNoGlobalHeader
	}
	if typ == TypeCampaign {
		return SelectCampaignSchema(o, difficulty)
	}
	return SelectNetworkSchema(o, players)
}

// SelectSchema selects a schema without a mission type, inferring the mode
// from which schema families the OTA carries.
//
// This is a diagnostic convenience for tools that hold an OTA and no session:
// the load path calls SelectSchemaForType, because the mode is what retail
// dispatches on. Prefer SelectSchemaForType wherever the type is known.
func SelectSchema(o *formats.OTA, difficulty, players int) (Schema, error) {
	if o == nil || o.Global == nil {
		return Schema{}, ErrNoGlobalHeader
	}
	hasCampaign, hasNetwork := detectKinds(o)
	switch {
	case hasCampaign && !hasNetwork:
		return SelectCampaignSchema(o, difficulty)
	case hasNetwork && !hasCampaign:
		return SelectNetworkSchema(o, players)
	case hasCampaign && hasNetwork:
		if difficulty >= 0 && difficulty <= 2 {
			if s, err := SelectCampaignSchema(o, difficulty); err == nil {
				return s, nil
			}
		}
		return SelectNetworkSchema(o, players)
	default:
		return Schema{}, ErrNoSuitableSchema
	}
}

// SelectCampaignSchema selects a campaign schema by difficulty [08 "Schema choice"].
// Difficulty 0 tries Easy,Medium,Hard; 1 tries Medium,Easy,Hard; 2 tries Hard,Medium,Easy.
// Comparison of Type is case-insensitive [08 "Schema choice"].
// Any other difficulty value fails with ErrNoSuitableSchema.
// Missing GlobalHeader yields ErrNoGlobalHeader.
func SelectCampaignSchema(o *formats.OTA, difficulty int) (Schema, error) {
	if o == nil || o.Global == nil {
		return Schema{}, ErrNoGlobalHeader
	}
	var order []string // [08 "Schema choice"] difficulty-dependent permutation
	switch difficulty {
	case 0:
		order = []string{"Easy", "Medium", "Hard"}
	case 1:
		order = []string{"Medium", "Easy", "Hard"}
	case 2:
		order = []string{"Hard", "Medium", "Easy"}
	default:
		return Schema{}, ErrNoSuitableSchema
	}
	for _, cand := range order {
		for _, sch := range o.Schemas {
			if strings.EqualFold(strings.TrimSpace(sch.Type), cand) {
				return Schema{Name: sch.Name, StartPositions: countStartPositions(sch.Section)}, nil
			}
		}
	}
	return Schema{}, ErrNoSuitableSchema
}

// SelectNetworkSchema selects a Network 1..4 schema by StartPos count [08 "Schema choice"].
// Candidates are tried in fixed order Network 1..4. Each candidate counts its StartPos
// special records [fmt ota]. Accepted when count == playerCount (nonzero lobby entries)
// OR playerCount is zero OR — as fallback while no exact match found — candidate's
// count is the largest seen so far. First accepted name is returned; failure yields
// ErrNoSuitableSchema. Type comparison is case-insensitive. Missing GlobalHeader
// yields ErrNoGlobalHeader.
func SelectNetworkSchema(o *formats.OTA, playerCount int) (Schema, error) {
	if o == nil || o.Global == nil {
		return Schema{}, ErrNoGlobalHeader
	}
	candidates := []string{"Network 1", "Network 2", "Network 3", "Network 4"} // [08 "Schema choice"]
	var best Schema
	best.StartPositions = -1
	for _, cand := range candidates {
		sch := findSchemaByType(o, cand)
		if sch == nil {
			continue
		}
		cnt := countStartPositions(sch.Section) // [fmt ota] specials StartPos; [08 "Schema choice"] StartPos counting
		if playerCount == 0 {
			return Schema{Name: sch.Name, StartPositions: cnt}, nil
		}
		if cnt == playerCount {
			return Schema{Name: sch.Name, StartPositions: cnt}, nil
		}
		if cnt > best.StartPositions {
			best = Schema{Name: sch.Name, StartPositions: cnt}
		}
	}
	if best.StartPositions >= 0 {
		return best, nil
	}
	return Schema{}, ErrNoSuitableSchema
}

func findSchemaByType(o *formats.OTA, typ string) *formats.OTASchema {
	for i := range o.Schemas {
		if strings.EqualFold(strings.TrimSpace(o.Schemas[i].Type), typ) {
			return &o.Schemas[i]
		}
	}
	return nil
}

func detectKinds(o *formats.OTA) (hasCampaign, hasNetwork bool) {
	for _, s := range o.Schemas {
		t := strings.ToLower(strings.TrimSpace(s.Type))
		switch t {
		case "easy", "medium", "hard":
			hasCampaign = true
		case "network 1", "network 2", "network 3", "network 4":
			hasNetwork = true
		}
	}
	return
}

func countStartPositions(sec *formats.Section) int {
	if sec == nil {
		return 0
	}
	specials := sec.Section("specials") // [fmt ota]
	if specials == nil {
		return 0
	}
	n := 0
	for _, sub := range specials.Sections() {
		if v, ok := sub.FirstValue("specialwhat"); ok {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(v)), "startpos") {
				n++
			}
		}
	}
	return n
}
