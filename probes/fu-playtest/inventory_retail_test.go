//go:build retail

package fuplaytest

// Inventory probe for the fu-playtest acceptance round: which stock campaign
// missions author a non-default visibility pair, and which stock TNTs exist
// for an authored OTA to pair with by name [fmt ota] [08 R-SKIR-01 §4].
//
// It observes only; the written table goes to $NANOLATHE_FU_PLAYTEST_DIR when
// set and is otherwise logged.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe/nanolathe/vfs"
)

func TestInventoryStockCampaignVisibilityKeys(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatal(err)
	}
	defer fs.Close()

	var lines []string
	camps, err := fs.RetailReadDir("camps")
	if err != nil {
		t.Fatal(err)
	}
	nonDefault := 0
	for _, entry := range camps {
		if entry.IsDir || !strings.EqualFold(filepath.Ext(entry.Name), ".tdf") {
			continue
		}
		campaign, err := mission.DiscoverCampaign(fs, entry.Path)
		if err != nil {
			lines = append(lines, fmt.Sprintf("%s: discover: %v", entry.Path, err))
			continue
		}
		for _, stub := range campaign.Missions {
			name := stub.MissionFile
			if dot := strings.LastIndex(name, "."); dot >= 0 {
				name = name[:dot]
			}
			ota, err := formats.LoadOTAFile(fs, "maps/"+name+".ota")
			if err != nil {
				lines = append(lines, fmt.Sprintf("%s MISSION%d %q: ota: %v", entry.Path, stub.Index, stub.MissionFile, err))
				continue
			}
			g := mission.DecodeMissionGlobals(ota.Global)
			if g.Mapping != 1 || g.LineOfSight != 1 {
				nonDefault++
			}
			lines = append(lines, fmt.Sprintf("%s MISSION%d missionfile=%s mapping=%d lineofsight=%d", entry.Path, stub.Index, stub.MissionFile, g.Mapping, g.LineOfSight))
		}
	}
	maps, err := fs.RetailReadDir("maps")
	if err != nil {
		t.Fatal(err)
	}
	var tnts []string
	for _, entry := range maps {
		if !entry.IsDir && strings.EqualFold(filepath.Ext(entry.Name), ".tnt") {
			tnts = append(tnts, entry.Name)
		}
	}
	sort.Strings(tnts)
	lines = append(lines, fmt.Sprintf("stock TNT count=%d", len(tnts)))
	lines = append(lines, tnts...)
	lines = append(lines, fmt.Sprintf("campaign missions with a non-1/1 visibility pair: %d", nonDefault))

	out := strings.Join(lines, "\n") + "\n"
	if dir := os.Getenv("NANOLATHE_FU_PLAYTEST_DIR"); dir != "" {
		if err := os.WriteFile(filepath.Join(dir, "inventory.txt"), []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Log(out)
}

// TestInventorySherwoodGeometry logs the authored size of the stock SHERWOOD
// map so the fu-playtest visibility fixture can place its units inside it.
func TestInventorySherwoodGeometry(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	ota, err := formats.LoadOTAFile(fs, "maps/SHERWOOD.ota")
	if err != nil {
		t.Skipf("stock SHERWOOD.ota unavailable: %v", err)
	}
	size, _ := ota.Global.StringValue("size", "")
	players, _ := ota.Global.StringValue("numplayers", "")
	t.Logf("SHERWOOD size=%q numplayers=%q schemas=%d", size, players, len(ota.Global.Sections()))
	for _, sec := range ota.Global.Sections() {
		typ, _ := sec.StringValue("Type", "")
		specials := 0
		if sp := sec.Section("specials"); sp != nil {
			specials = len(sp.Sections())
			for _, s := range sp.Sections() {
				name, _ := s.StringValue("specialwhat", "")
				x := s.IntValue("XPos", -1)
				z := s.IntValue("ZPos", -1)
				t.Logf("  %s special %s at %d,%d", sec.Name, name, x, z)
			}
		}
		t.Logf("  schema %s type=%q specials=%d", sec.Name, typ, specials)
	}
}

// TestInventoryFactoryFlags logs the compiled builder/canmove/canfly words of
// the stock factories and builders the fu-playtest censuses classify.
func TestInventoryFactoryFlags(t *testing.T) {
	cat, _ := retailcat.Shared(t)
	for _, name := range []string{"CORVP", "CORLAB", "CORAP", "ARMVP", "ARMLAB", "ARMAP", "CORCV", "CORCK", "ARMCK", "CORCOM", "ARMCOM", "ARMPW", "CORAK", "ARMPEEP", "CORFINK"} {
		def, ok := cat.Unit(name)
		if !ok || def == nil {
			t.Logf("%s: absent", name)
			continue
		}
		_, menu := cat.BuildMenus[content.CanonicalKey(name)]
		t.Logf("%s: builder=%v canmove=%v canfly=%v commander=%v footprint=%dx%d buildmenu=%v", name, def.Builder, def.CanMove, def.CanFly, def.Commander, def.FootprintX, def.FootprintZ, menu)
	}
}
