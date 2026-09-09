package content

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
)

func TestTNTHeaderLiteSkipsAbsentMinimapPointer(t *testing.T) {
	data := make([]byte, 0x40)
	binary.LittleEndian.PutUint32(data[0:], 0x2000)
	binary.LittleEndian.PutUint32(data[0x28:], ^uint32(0))
	// Canonical slot 11 is clear, so the pointer is unrelated metadata.
	fs := newFixtureFS(t, fixtureFile{path: "maps/no-mini.tnt", data: string(data)})
	h, err := loadTNTHeaderLite(fs, "maps/no-mini.tnt")
	if err != nil {
		t.Fatal(err)
	}
	if h.MiniMapPresent || h.MinimapWidth != 0 || h.MinimapHeight != 0 {
		t.Fatalf("absent minimap probe = %+v", h)
	}
}

func TestMapSchemaProbeStopsAtFirstGapAndIgnoresInertGlobalFields(t *testing.T) {
	inertSchemaKey := "mo" + "hometal"
	ota, err := formats.LoadOTA([]byte(fmt.Sprintf(`[GlobalHeader]
{
 missionname=campaign-only;
 solarstrength=99;
 [Schema 0]
 {
  type=Easy;
  %s=41;
  humanmetal=7;
 }
 [Schema 2]
 {
  type=Hard;
  humanmetal=99;
 }
}
`, inertSchemaKey)))
	if err != nil {
		t.Fatalf("LoadOTA: %v", err)
	}
	m := compileMapHeader("maps/gap.ota", "maps/gap.tnt", Provenance{}, Provenance{}, ota, tntHeaderLite{})
	if len(m.Schemas) != 1 {
		t.Fatalf("schemas = %d, want only Schema 0 before first gap", len(m.Schemas))
	}
	if m.Schemas[0].Name != "Schema 0" || m.Schemas[0].Type != "Easy" {
		t.Fatalf("schema 0 = %#v", m.Schemas[0])
	}
	if m.MissionName != "" || m.SolarStrength != 0 {
		t.Fatalf("inert global fields entered map record: mission=%q solar=%d", m.MissionName, m.SolarStrength)
	}
	if m.Schemas[0].MohoMetal != 0 {
		t.Fatalf("inert schema field entered map record: %d", m.Schemas[0].MohoMetal)
	}
}

func TestMapSchemaProjectionUsesResolvedType(t *testing.T) {
	ota, err := formats.LoadOTA([]byte(`[GlobalHeader] {
 [Schema 0] { Type=Easy; type=Network 2; }
 [Schema 2] { Type=Network 4; }
 }`))
	if err != nil {
		t.Fatal(err)
	}
	m := compileMapHeader("maps/test.ota", "maps/test.tnt", Provenance{}, Provenance{}, ota, tntHeaderLite{})
	if len(m.Schemas) != 1 || m.Schemas[0].Type != "Network 2" || m.Schemas[0].Type != ota.Schemas[0].Type {
		t.Fatalf("compiled projection: %+v", m.Schemas)
	}
}
