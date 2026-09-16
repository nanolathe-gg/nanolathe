package formats

import (
	"errors"
	"fmt"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

// ErrMissingOTAHeader distinguishes a parsed definition rejected by map
// discovery from fatal authored-text syntax errors [02 R-MAP-01 §2].
var ErrMissingOTAHeader = errors.New("ota: missing GlobalHeader")

// OTA contains the map metadata needed by menus and the game-start path.
// The full TDF document remains available for mission-specific consumers.
type OTA struct {
	Document           *Document
	Global             *Section
	MissionName        string
	MissionDescription string
	Memory             string
	NumPlayers         string
	Size               string
	Schemas            []OTASchema
}

// OTASchema is one `Schema N` section of a map or mission: the per-difficulty
// starting resources and AI profile, with the parsed section kept for the
// keys the loader reads later [fmt ota].
type OTASchema struct {
	Name           string
	Type           string
	AIProfile      string
	HumanMetal     string
	HumanEnergy    string
	ComputerMetal  string
	ComputerEnergy string
	Section        *Section
}

// LoadOTA parses a map or mission definition. A file without a GlobalHeader
// section is an error [fmt ota].
func LoadOTA(data []byte) (*OTA, error) {
	document, err := ParseTDF(data)
	if err != nil {
		return nil, fmt.Errorf("ota: parse: %w", err)
	}
	global := document.Root.Section("GlobalHeader")
	if global == nil {
		return nil, ErrMissingOTAHeader
	}
	result := &OTA{
		Document:           document,
		Global:             global,
		MissionName:        languageValue(global, "missionname"),
		MissionDescription: languageValue(global, "missiondescription"),
		Memory:             otaValue(global, "memory"),
		NumPlayers:         otaValue(global, "numplayers"),
		Size:               otaValue(global, "size"),
	}
	// The semantic projection stops at the first missing index; the document
	// retains every raw section for tooling [02 R-MAP-01 §4].
	for i := 0; ; i++ {
		section := global.Section(fmt.Sprintf("Schema %d", i))
		if section == nil {
			break
		}
		result.Schemas = append(result.Schemas, OTASchema{
			Name:           section.OriginalName,
			Type:           otaValue(section, "type"),
			AIProfile:      otaValue(section, "aiprofile"),
			HumanMetal:     otaValue(section, "humanmetal"),
			HumanEnergy:    otaValue(section, "humanenergy"),
			ComputerMetal:  otaValue(section, "computermetal"),
			ComputerEnergy: otaValue(section, "computerenergy"),
			Section:        section,
		})
	}
	return result, nil
}

func languageValue(section *Section, key string) string {
	if section == nil {
		return ""
	}
	value, _ := section.LanguageString("", key, "")
	return value
}

// LoadOTAFile reads and parses a map or mission definition from the VFS.
func LoadOTAFile(fs vfs.FSOps, name string) (*OTA, error) {
	data, err := readVFSWithLimit(fs, name, int64(DefaultTDFLimits().MaxBytes))
	if err != nil {
		return nil, err
	}
	return LoadOTA(data)
}

// HasNetworkSchema reports whether any schema declares a network type.
func (o *OTA) HasNetworkSchema() bool {
	if o == nil {
		return false
	}
	for _, schema := range o.Schemas {
		if NetworkSchemaRank(schema.Type) != 0 {
			return true
		}
	}
	return false
}

func otaValue(section *Section, key string) string {
	if section == nil {
		return ""
	}
	value, _ := section.StringValue(key, "")
	return value
}

// NetworkSchemaRank returns the exact type's preference rank, or zero for an
// unrecognized type. Input is an already resolved OTA string; extra whitespace
// is not removed here [02 R-MAP-01 §4].
func NetworkSchemaRank(typ string) int {
	switch FoldASCII(typ) {
	case "network 1":
		return 1
	case "network 2":
		return 2
	case "network 3":
		return 3
	case "network 4":
		return 4
	default:
		return 0
	}
}
