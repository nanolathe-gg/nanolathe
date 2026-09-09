//go:build retail

package formats_test

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// corpusFile is one stock file's verdict and the scalar its format is
// measured by: bytes for TDF, cells for TNT, entries for GAF, objects for 3DO.
// items and depth are TDF-only. A file no format measures leaves them zero and
// contributes only its parse verdict.
type corpusFile struct {
	ext   string
	path  string
	err   error
	value int
	items int
	depth int
}

// parseAndMeasure runs the loader the extension names and, where a
// Default*Limits cap exists, measures the scalar the cap is written against.
func parseAndMeasure(ext string, data []byte) corpusFile {
	f := corpusFile{ext: ext}
	switch ext {
	case ".tdf", ".fbi", ".ota":
		doc, err := formats.ParseTDF(data)
		if err != nil {
			f.err = err
			return f
		}
		f.value = len(data)
		var walk func(s *formats.Section, d int)
		walk = func(s *formats.Section, d int) {
			if d > f.depth {
				f.depth = d
			}
			f.items += len(s.Items)
			for _, item := range s.Items {
				if item.Kind == formats.NestedSection && item.Section != nil {
					walk(item.Section, d+1)
				}
			}
		}
		walk(doc.Root, 0)
	case ".gui":
		_, f.err = formats.LoadGUI(data)
	case ".pal":
		_, f.err = formats.LoadPAL(data)
	case ".tnt":
		tnt, err := formats.LoadTNT(data)
		if err != nil {
			f.err = err
			return f
		}
		f.value = int(tnt.Width) * int(tnt.Height)
	case ".3do":
		model, err := formats.LoadThreeDO(data)
		if err != nil {
			f.err = err
			return f
		}
		f.value = len(model.Objects)
	case ".gaf":
		gaf, err := formats.LoadGAF(data)
		if err != nil {
			f.err = err
			return f
		}
		f.value = len(gaf.Entries)
	case ".pcx":
		_, f.err = formats.LoadPCX(data)
	case ".fnt":
		_, f.err = formats.LoadFNT(data)
	case ".wav":
		// [fmt wav]: two stock files are not RIFF — HONK.WAV is raw 8-bit mono
		// PCM and SING.WAV uses the DIGI/HSHD/SDAT container. LoadAudio accepts
		// all three containers as retail must.
		_, f.err = formats.LoadAudio(data)
	}
	return f
}

// TestFormatCoverage parses every file of a known type in the reference
// install and measures the ones whose format carries a Default*Limits cap.
// It is PLAN_01's real gate: fixtures cannot find the malformations retail's
// own data carries, and a parser that rejects shipped content is wrong no
// matter what the spec says.
//
// It also carries the P1-I09 claim that the fault guards in formats/* (HPI
// cipher, TDF blanking comment offsets, GAF/TNT/3DO reloc, PCX/WAV bounds
// checks) are outside stock-reachable behavior: nothing in the corpus fails to
// parse, and the largest TDF, TNT and 3DO the corpus holds are far under the
// caps. That half used to be a second whole-corpus walk in
// TestCorpusFormatLimits_Retail, which re-read and re-parsed every .tdf, .tnt,
// .gaf and .3do this test had just read and parsed, for measurements this one
// can take on the way past.
//
// The whole corpus is the contract, not a sample — the claim is that NO stock
// file reaches a fault guard, which no sample can carry — so what makes it
// affordable is one bounded worker pool over every record at once, rather than
// one goroutine per extension where two extensions hold three quarters of the
// files. Results come back in manifest order, so a diagnostic names the same
// file on every run.
//
// Reference install at ~/TotalAnnihilation (base+CC+BT+patch 3.1) is the
// supported test set.
func TestFormatCoverage(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fileSystem := vfs.New()
	if err := fileSystem.MountGameDirectory(root); err != nil {
		t.Fatalf("mount: %v", err)
	}
	t.Cleanup(func() { _ = fileSystem.Close() })

	records, err := fileSystem.Manifest(vfs.ManifestOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// palettes/guipal.pcx is a 1024-byte raw palette carrying a .pcx extension:
	// its manufacturer byte is 0x00, so retail's own decoder rejects it too.
	// It is content, not a parser defect.
	notPCX := map[string]bool{"palettes/guipal.pcx": true}

	type job struct {
		ext  string
		path string
	}
	var jobsList []job
	for _, record := range records {
		if notPCX[record.LogicalPath] {
			continue
		}
		ext := strings.ToLower(filepath.Ext(record.LogicalPath))
		switch ext {
		case ".tdf", ".fbi", ".ota", ".gui", ".pal", ".tnt", ".3do", ".gaf", ".pcx", ".fnt", ".wav":
			// .cob joins this walk when formats gains a COB loader (phase 6
			// owns it); there is nothing to exercise today.
		default:
			continue
		}
		jobsList = append(jobsList, job{ext: ext, path: record.LogicalPath})
	}

	results := make([]corpusFile, len(jobsList))
	workers := runtime.GOMAXPROCS(0)
	if workers > 4 {
		workers = 4
	}
	if workers > len(jobsList) {
		workers = len(jobsList)
	}
	queue := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range queue {
				j := jobsList[idx]
				data, err := fileSystem.ReadFileLimit(j.path, 64<<20)
				if err != nil {
					results[idx] = corpusFile{ext: j.ext, path: j.path, err: err}
					continue
				}
				f := parseAndMeasure(j.ext, data)
				f.path = j.path
				results[idx] = f
			}
		}()
	}
	for i := range jobsList {
		queue <- i
	}
	close(queue)
	wg.Wait()

	// Per-extension reduction, in manifest order so the ten reported failures
	// and the "largest" paths are the same ones on every run.
	type extStat struct {
		parsed    int
		failures  []string
		maxValue  int
		maxPath   string
		maxItems  int
		maxDepth  int
		failCount int
	}
	stats := map[string]*extStat{}
	var exts []string
	for _, f := range results {
		s := stats[f.ext]
		if s == nil {
			s = &extStat{}
			stats[f.ext] = s
			exts = append(exts, f.ext)
		}
		if f.err != nil {
			s.failCount++
			if len(s.failures) < 10 {
				s.failures = append(s.failures, fmt.Sprintf("%s: %v", f.path, f.err))
			}
			continue
		}
		s.parsed++
		if f.value > s.maxValue {
			s.maxValue, s.maxPath = f.value, f.path
		}
		if f.items > s.maxItems {
			s.maxItems = f.items
		}
		if f.depth > s.maxDepth {
			s.maxDepth = f.depth
		}
	}
	sort.Strings(exts)
	for _, ext := range exts {
		s := stats[ext]
		t.Logf("%-6s parsed=%d failed=%d largest=%d in %s", ext, s.parsed, s.failCount, s.maxValue, s.maxPath)
		for _, failure := range s.failures {
			t.Errorf("%s %s", ext, failure)
		}
	}

	// The measured maxima against the caps the parsers refuse at [P1-I09]. A
	// stock file at or over a cap would mean a fault guard sits inside
	// stock-reachable behavior.
	tdfLimits := formats.DefaultTDFLimits()
	maxTDFBytes, maxTDFItems, maxTDFDepth := 0, 0, 0
	var maxTDFPath string
	for _, ext := range []string{".tdf", ".fbi", ".ota"} {
		s := stats[ext]
		if s == nil {
			continue
		}
		if s.maxValue > maxTDFBytes {
			maxTDFBytes, maxTDFPath = s.maxValue, s.maxPath
		}
		if s.maxItems > maxTDFItems {
			maxTDFItems = s.maxItems
		}
		if s.maxDepth > maxTDFDepth {
			maxTDFDepth = s.maxDepth
		}
	}
	t.Logf("TDF maxBytes=%d (limit %d) maxItems=%d (limit %d) maxDepth=%d (limit %d) in %s",
		maxTDFBytes, tdfLimits.MaxBytes, maxTDFItems, tdfLimits.MaxItems, maxTDFDepth, tdfLimits.MaxDepth, maxTDFPath)
	if maxTDFBytes >= tdfLimits.MaxBytes {
		t.Errorf("max TDF bytes %d hits limit %d", maxTDFBytes, tdfLimits.MaxBytes)
	}
	if maxTDFItems >= tdfLimits.MaxItems {
		t.Errorf("max TDF items %d hits limit %d", maxTDFItems, tdfLimits.MaxItems)
	}
	if maxTDFDepth >= tdfLimits.MaxDepth {
		t.Errorf("max TDF depth %d hits limit %d", maxTDFDepth, tdfLimits.MaxDepth)
	}

	if s := stats[".tnt"]; s != nil {
		limit := int(formats.DefaultTNTLimits().MaxCells)
		t.Logf("TNT maxCells=%d (limit %d) in %s", s.maxValue, limit, s.maxPath)
		if s.maxValue >= limit {
			t.Errorf("max TNT cells %d hits limit %d", s.maxValue, limit)
		}
	}
	if s := stats[".3do"]; s != nil {
		limit := int(formats.DefaultThreeDOLimits().MaxObjects)
		t.Logf("3DO maxObjects=%d (limit %d) in %s", s.maxValue, limit, s.maxPath)
		if s.maxValue >= limit {
			t.Errorf("max 3DO objects %d hits limit %d", s.maxValue, limit)
		}
	}
	if s := stats[".gaf"]; s != nil {
		// GAF has no authored entry cap. The measurement is recorded for
		// whoever later proposes one.
		t.Logf("GAF maxEntries=%d in %s (no fixed cap)", s.maxValue, s.maxPath)
	}
}
