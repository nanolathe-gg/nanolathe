//go:build retail

package client

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Alpha 5's ModFX bank exceeds the eager decoder's aggregate pixel budget.
// Loading timing and one large frame per entry must remain independent of
// that aggregate; see research/extensions/mod-engine-compatibility.md,
// "Authored rendering audit".
func TestZeroEffectBankLoadsWithinResidentBudgets(t *testing.T) {
	roots := os.Getenv("NANOLATHE_MOD_ROOTS_ZERO")
	if roots == "" {
		t.Skip("set NANOLATHE_MOD_ROOTS_ZERO for Zero's authored effects")
	}
	fs := vfs.New()
	t.Cleanup(func() { fs.Close() })
	if err := fs.MountGameDirectories(append([]string{testsupport.RetailRoot(t)}, filepath.SplitList(roots)...)); err != nil {
		t.Fatal(err)
	}
	c := &Client{modelFS: fs}
	bank := c.EffectBank("modfx")
	if bank == nil || len(bank.Entries) == 0 {
		t.Fatalf("Zero effect metadata rejected: %+v", c.artDiagnostics)
	}
	for _, entry := range bank.Entries {
		if len(entry.Frames) == 0 {
			continue
		}
		selected := 0
		for i, ref := range entry.Frames {
			if uint64(ref.Frame.Width)*uint64(ref.Frame.Height) > uint64(entry.Frames[selected].Frame.Width)*uint64(entry.Frames[selected].Frame.Height) {
				selected = i
			}
		}
		f, ok := c.effectFrame("modfx", entry.Name, int32(selected))
		if !ok || f == nil {
			t.Fatalf("%s frame %d unresolved: %+v", entry.Name, selected, c.artDiagnostics)
		}
		if len(entry.Frames[selected].Frame.Pixels) != 0 {
			t.Fatal("timing metadata retained decoded pixels")
		}
		a := c.effectArt
		if a.sources.bytes > effectSourceBytes || a.frames.bytes > effectFrameBytes || a.durableBytes > effectDurableBytes {
			t.Fatalf("cache exceeded its budget: %+v", c.effectCacheSnapshot())
		}
	}
	if len(c.artDiagnostics) != 0 {
		t.Fatalf("Zero effects reported diagnostics: %+v", c.artDiagnostics)
	}
}
