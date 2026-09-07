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
	//lint:ignore ST1005 retail diagnostic text, reproduced verbatim [08 "Schema choice"]
	ErrNoGlobalHeader = errors.New("Very bad news! No MSG!")
	// ErrNoSuitableSchema is emitted verbatim when no candidate matches [08 "Schema choice"].
	//lint:ignore ST1005 retail diagnostic text, reproduced verbatim [08 "Schema choice"]
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
			if strings.EqualFold(sch.Type, cand) {
				return Schema{Name: sch.Name, StartPositions: countStartPositions(sch.Section)}, nil
			}
		}
	}
	return Schema{}, ErrNoSuitableSchema
}

// SelectNetworkSchema selects across every contiguous schema in network type
// preference order. Later exact matches replace earlier ones; fallback ties
// retain the first candidate. Browser admission omits this StartPos gate
// [02 R-MAP-01 §4].
func SelectNetworkSchema(o *formats.OTA, playerCount int) (Schema, error) {
	if o == nil || o.Global == nil {
		return Schema{}, ErrNoGlobalHeader
	}
	var best Schema
	for rank := 1; rank <= 4; rank++ {
		for _, sch := range o.Schemas {
			if formats.NetworkSchemaRank(sch.Type) != rank {
				continue
			}
			count := countStartPositions(sch.Section)
			if count != 0 && (count == playerCount || playerCount == 0 || (best.StartPositions < count && best.StartPositions != playerCount)) {
				best = Schema{Name: sch.Name, StartPositions: count}
			}
		}
	}
	if best.StartPositions != 0 {
		return best, nil
	}
	return Schema{}, ErrNoSuitableSchema
}

// StartingResources are the four starting-resource words of the schema the
// mission actually runs under [02 R-MAP-01 §5]. They are read with the chosen
// schema current — as integers, widened to single floats at the store, so an
// authored fraction is lost before it is ever a float — and they feed the
// battle-entry grant that writes both the live stocks and the storage bonus on
// the mission kind [08 R-ENTRY-01 §8 step 5][05 R-ECO-01 §4].
type StartingResources struct {
	HumanMetal     int32
	HumanEnergy    int32
	ComputerMetal  int32
	ComputerEnergy int32
}

// SchemaSection returns the OTA section of the schema this mission selected,
// or nil when the mission carries no OTA or the selected name names no section.
func (m *Mission) SchemaSection() *formats.Section {
	if m == nil || m.OTA == nil {
		return nil
	}
	for i := range m.OTA.Schemas {
		if strings.EqualFold(strings.TrimSpace(m.OTA.Schemas[i].Name), strings.TrimSpace(m.Schema.Name)) {
			return m.OTA.Schemas[i].Section
		}
	}
	// A mission whose selected name matches no section is still unambiguous
	// when the file authors exactly one schema: that is the schema the
	// placements and the terrain metal seed came from.
	if len(m.OTA.Schemas) == 1 {
		return m.OTA.Schemas[0].Section
	}
	return nil
}

// StartingResources resolves the mission's four starting-resource words.
//
// The keys are `[Schema N]` keys, not `[GlobalHeader]` keys: every one of the
// reference install's 635 schemas authors all four and the executable reads
// them with the chosen schema current [02 R-MAP-01 §5]. Reading them from the
// GlobalHeader returns the accessor default of zero for every stock mission —
// the same defect class as the AI's SurfaceMetal binding [08 R-AI-03 §4-A],
// and the reason Arm mission 2 opened with an empty treasury despite authoring
// `HumanMetal=1000;` in all three of its schemas.
//
// A GlobalHeader-authored word is still honoured, but only when the selected
// schema does not author the key and the GlobalHeader does: doc 02's combined
// OTA key table lists the four under one heading with both placements, so a
// hand-written mission that authors them globally is not a miss [02 §7]. A key
// absent from both is the accessor default, zero.
func (m *Mission) StartingResources() StartingResources {
	schema := m.SchemaSection()
	var global *formats.Section
	if m != nil && m.OTA != nil {
		global = m.OTA.Global
	}
	read := func(key string) int32 {
		if schema != nil {
			if _, ok := schema.FirstValue(key); ok {
				return schema.IntValue(key, 0)
			}
		}
		if global != nil {
			return global.IntValue(key, 0)
		}
		return 0
	}
	return StartingResources{
		HumanMetal:     read("HumanMetal"),
		HumanEnergy:    read("HumanEnergy"),
		ComputerMetal:  read("ComputerMetal"),
		ComputerEnergy: read("ComputerEnergy"),
	}
}

func detectKinds(o *formats.OTA) (hasCampaign, hasNetwork bool) {
	for _, s := range o.Schemas {
		if formats.NetworkSchemaRank(s.Type) != 0 {
			hasNetwork = true
		}
		t := strings.ToLower(s.Type)
		switch t {
		case "easy", "medium", "hard":
			hasCampaign = true
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
		if v, ok := sub.StringValue("specialwhat", ""); ok {
			if strings.HasPrefix(strings.ToLower(v), "startpos") {
				n++
			}
		}
	}
	return n
}
