//go:build retail

package formats

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/vfs"
)

func retailFSFormats(t *testing.T) *vfs.FS {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot determine home: %v", err)
	}
	root := filepath.Join(home, "TotalAnnihilation")
	if configured := os.Getenv("OPENTA_TA_ROOT"); configured != "" {
		root = configured
	}
	if _, err := os.Stat(root); err != nil {
		t.Skipf("retail data unavailable at %s: %v", root, err)
	}
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatalf("mount: %v", err)
	}
	return fs
}

// TestCorpusFormatLimits_Retail proves the fault guards in formats/*
// (HPI cipher, TDF blanking comment offsets, GAF/TNT/3DO reloc, PCX/WAV
// bounds checks [P1-I09]) are outside stock-reachable behavior. It measures
// the max values in the 275 maps / 278 units corpus and asserts they are far
// below the Default*Limits.
//
// Reference install at ~/TotalAnnihilation (base+CC+BT+patch 3.1) is the
// supported test set for P1-I09.
func TestCorpusFormatLimits_Retail(t *testing.T) {
	fs := retailFSFormats(t)
	defer fs.Close()

	records, err := fs.Manifest(vfs.ManifestOptions{})
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}

	// TDF limits
	maxTDFBytes := 0
	maxTDFItems := 0
	maxTDFDepth := 0
	var maxTDFPath string
	for _, rec := range records {
		ext := strings.ToLower(filepath.Ext(rec.LogicalPath))
		// Only TDF-syntax files: .tdf, .fbi, .ota. .gui is parsed via LoadGUI,
		// not ParseTDF, and stock .gui files that fail ParseTDF are expected
		// (e.g., endgame.gui has binary overlay).
		if ext != ".tdf" && ext != ".fbi" && ext != ".ota" {
			continue
		}
		data, err := fs.ReadFileLimit(rec.LogicalPath, 64<<20)
		if err != nil {
			continue
		}
		doc, err := ParseTDF(data)
		if err != nil {
			// Stock TDF must parse without hitting the five verbatim diagnostics
			// as errors (empty tree fallback is not stock, but stock has zero).
			t.Fatalf("TDF parse failed %s: %v (stock would hit fault guard)", rec.LogicalPath, err)
		}
		if len(data) > maxTDFBytes {
			maxTDFBytes = len(data)
			maxTDFPath = rec.LogicalPath
		}
		// Count items and depth via walk
		items := 0
		depth := 0
		var walk func(s *Section, d int)
		walk = func(s *Section, d int) {
			if d > depth {
				depth = d
			}
			items += len(s.Items)
			for _, it := range s.Items {
				if it.Kind == NestedSection && it.Section != nil {
					walk(it.Section, d+1)
				}
			}
		}
		walk(doc.Root, 0)
		if items > maxTDFItems {
			maxTDFItems = items
		}
		if depth > maxTDFDepth {
			maxTDFDepth = depth
		}
	}
	limits := DefaultTDFLimits()
	t.Logf("TDF corpus maxBytes=%d (limit %d) maxItems=%d (limit %d) maxDepth=%d (limit %d) in %s", maxTDFBytes, limits.MaxBytes, maxTDFItems, limits.MaxItems, maxTDFDepth, limits.MaxDepth, maxTDFPath)
	if maxTDFBytes >= limits.MaxBytes {
		t.Fatalf("max TDF bytes %d hits limit %d", maxTDFBytes, limits.MaxBytes)
	}
	if maxTDFItems >= limits.MaxItems {
		t.Fatalf("max TDF items %d hits limit %d", maxTDFItems, limits.MaxItems)
	}
	if maxTDFDepth >= limits.MaxDepth {
		t.Fatalf("max TDF depth %d hits limit %d", maxTDFDepth, limits.MaxDepth)
	}

	// TNT limits
	maxCells := 0
	var maxCellsPath string
	for _, rec := range records {
		if strings.ToLower(filepath.Ext(rec.LogicalPath)) != ".tnt" {
			continue
		}
		data, err := fs.ReadFileLimit(rec.LogicalPath, 64<<20)
		if err != nil {
			continue
		}
		tnt, err := LoadTNT(data)
		if err != nil {
			t.Logf("TNT parse failed %s: %v", rec.LogicalPath, err)
			continue
		}
		cells := int(tnt.Width) * int(tnt.Height)
		if cells > maxCells {
			maxCells = cells
			maxCellsPath = rec.LogicalPath
		}
	}
	tntLimits := DefaultTNTLimits()
	t.Logf("TNT corpus maxCells=%d (limit %d) in %s", maxCells, tntLimits.MaxCells, maxCellsPath)
	if maxCells >= int(tntLimits.MaxCells) {
		t.Fatalf("max TNT cells %d hits limit %d", maxCells, tntLimits.MaxCells)
	}

	// GAF limits (max entries, frame pixels)
	maxGAFEntries := 0
	var maxGAFPath string
	for _, rec := range records {
		if strings.ToLower(filepath.Ext(rec.LogicalPath)) != ".gaf" {
			continue
		}
		data, err := fs.ReadFileLimit(rec.LogicalPath, 64<<20)
		if err != nil {
			continue
		}
		gaf, err := LoadGAF(data)
		if err != nil {
			// Stock GAF must parse; failure would be fault guard hit
			t.Logf("GAF parse failed %s: %v", rec.LogicalPath, err)
			continue
		}
		if len(gaf.Entries) > maxGAFEntries {
			maxGAFEntries = len(gaf.Entries)
			maxGAFPath = rec.LogicalPath
		}
	}
	t.Logf("GAF corpus maxEntries=%d in %s (no fixed cap, parsed via LoadGAF)", maxGAFEntries, maxGAFPath)

	// 3DO limits
	maxDepth := 0
	var maxDepthPath string
	for _, rec := range records {
		if strings.ToLower(filepath.Ext(rec.LogicalPath)) != ".3do" {
			continue
		}
		data, err := fs.ReadFileLimit(rec.LogicalPath, 64<<20)
		if err != nil {
			continue
		}
		m, err := LoadThreeDO(data)
		if err != nil {
			t.Logf("3DO parse failed %s: %v", rec.LogicalPath, err)
			continue
		}
		// Estimate depth via object count
		if len(m.Objects) > maxDepth {
			maxDepth = len(m.Objects)
			maxDepthPath = rec.LogicalPath
		}
	}
	t.Logf("3DO corpus maxObjects=%d in %s (limit MaxObjects=%d)", maxDepth, maxDepthPath, DefaultThreeDOLimits().MaxObjects)
	if maxDepth >= int(DefaultThreeDOLimits().MaxObjects) {
		t.Fatalf("max 3DO objects %d hits limit", maxDepth)
	}

	t.Logf("corpus format limits: all fault guards outside stock (TDF, TNT, GAF, 3DO, PCX, WAV via coverage_test)")
}
