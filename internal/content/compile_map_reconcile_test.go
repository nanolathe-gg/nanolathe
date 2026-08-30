package content

import (
	"fmt"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
)

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
