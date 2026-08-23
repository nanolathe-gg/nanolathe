package formats

import (
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/vfs"
)

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

func LoadOTA(data []byte) (*OTA, error) {
	document, err := ParseTDF(data)
	if err != nil {
		return nil, fmt.Errorf("ota: parse: %w", err)
	}
	global := document.Root.Section("GlobalHeader")
	if global == nil {
		return nil, fmt.Errorf("ota: missing GlobalHeader")
	}
	result := &OTA{
		Document:           document,
		Global:             global,
		MissionName:        otaValue(global, "missionname"),
		MissionDescription: otaValue(global, "missiondescription"),
		Memory:             otaValue(global, "memory"),
		NumPlayers:         otaValue(global, "numplayers"),
		Size:               otaValue(global, "size"),
	}
	for _, section := range global.Sections() {
		if !strings.HasPrefix(strings.ToLower(section.Name), "schema ") {
			continue
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

func LoadOTAFile(fs vfs.FSOps, name string) (*OTA, error) {
	data, err := readVFSWithLimit(fs, name, int64(DefaultTDFLimits().MaxBytes))
	if err != nil {
		return nil, err
	}
	return LoadOTA(data)
}

func (o *OTA) HasNetworkSchema() bool {
	if o == nil {
		return false
	}
	for _, schema := range o.Schemas {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(schema.Type)), "network") {
			return true
		}
	}
	return false
}

func otaValue(section *Section, key string) string {
	if section == nil {
		return ""
	}
	value, _ := section.LastValue(key)
	return strings.TrimSpace(value)
}
