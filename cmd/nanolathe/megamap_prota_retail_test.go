//go:build retail

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
)

// The megamap's pictures come from the same resolved icon configuration as
// the strategic icons: a mounted ProTA supplies its own Icon/iconcfg.ini with
// the reserved `unknow`, `nothing` and `nukeicon` rows, and category rows win
// over `unknow` for a unit they contain (DESIGN_INTERFACE_HUD_INPUT §3.15).
func TestRetailProTAMegamapIconBank(t *testing.T) {
	value := os.Getenv("NANOLATHE_MOD_ROOTS_PROTA")
	if strings.TrimSpace(value) == "" {
		t.Skip("NANOLATHE_MOD_ROOTS_PROTA is unset")
	}
	var modRoots []string
	for _, root := range filepath.SplitList(value) {
		if strings.TrimSpace(root) != "" {
			modRoots = append(modRoots, root)
		}
	}
	retail := testsupport.RetailRoot(t)
	cs, err := openContent(Options{Root: retail, Roots: append([]string{retail}, modRoots...)})
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	cat, err := cs.compileCatalog(nil)
	if err != nil {
		t.Fatal(err)
	}
	path, err := megamapIconConfigPath("", strategicIconSearchRoots(cs))
	if err != nil || path == "" {
		t.Fatalf("icon configuration not discovered: %q %v", path, err)
	}
	bank, err := client.LoadMegamapIconBank(cat, path, retailPaletteForTest(t, cs))
	if err != nil {
		t.Fatal(err)
	}
	if !bank.Configured || bank.Nothing() == nil || bank.Nuke() == nil {
		t.Fatalf("configured=%v nothing=%v nuke=%v", bank.Configured, bank.Nothing() != nil, bank.Nuke() != nil)
	}
	unknown := bank.Unit("", 0)
	kbot, ok := cat.Category("KBOT")
	if !ok || kbot.IsZero() {
		t.Skip("mounted content has no KBOT category")
	}
	for _, def := range cat.UnitRecords() {
		if def != nil && def.UnitDefID > 0 && def.UnitDefID <= 65535 && kbot.Contains(def.UnitDefID) {
			if icon := bank.Unit(def.CanonicalKey, uint16(def.UnitDefID)); icon == nil || icon == unknown {
				t.Fatalf("%s: KBOT member drew the unknow picture", def.CanonicalKey)
			}
			return
		}
	}
	t.Fatal("no KBOT member found")
}
