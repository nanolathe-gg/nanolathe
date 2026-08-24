package formats_test

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/vfs"
)

// TestFormatCoverage parses every file of a known type in the reference
// install. It is PLAN_01's real gate: fixtures cannot find the malformations
// retail's own data carries, and a parser that rejects shipped content is
// wrong no matter what the spec says.
func TestFormatCoverage(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fileSystem := vfs.New()
	if err := fileSystem.MountGameDirectory(root); err != nil {
		t.Fatalf("mount: %v", err)
	}
	defer fileSystem.Close()

	records, err := fileSystem.Manifest(vfs.ManifestOptions{})
	if err != nil {
		t.Fatal(err)
	}

	parsed := map[string]int{}
	failures := map[string][]string{}
	note := func(ext string, path string, err error) {
		if err != nil {
			if len(failures[ext]) < 10 {
				failures[ext] = append(failures[ext], fmt.Sprintf("%s: %v", path, err))
			}
			return
		}
		parsed[ext]++
	}

	// palettes/guipal.pcx is a 1024-byte raw palette carrying a .pcx extension:
	// its manufacturer byte is 0x00, so retail's own decoder rejects it too.
	// It is content, not a parser defect.
	notPCX := map[string]bool{"palettes/guipal.pcx": true}

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
		data, err := fileSystem.ReadFileLimit(record.LogicalPath, 64<<20)
		if err != nil {
			note(ext, record.LogicalPath, err)
			continue
		}
		switch ext {
		case ".tdf", ".fbi", ".ota":
			_, err = formats.ParseTDF(data)
		case ".gui":
			_, err = formats.LoadGUI(data)
		case ".pal":
			_, err = formats.LoadPAL(data)
		case ".tnt":
			_, err = formats.LoadTNT(data)
		case ".3do":
			_, err = formats.LoadThreeDO(data)
		case ".gaf":
			_, err = formats.LoadGAF(data)
		case ".pcx":
			_, err = formats.LoadPCX(data)
		case ".fnt":
			_, err = formats.LoadFNT(data)
		case ".wav":
			// [fmt wav]: two stock files are not RIFF — HONK.WAV is raw
			// 8-bit mono PCM and SING.WAV uses the DIGI/HSHD/SDAT container.
			// LoadAudio accepts all three containers as retail must.
			_, err = formats.LoadAudio(data)
		}
		note(ext, record.LogicalPath, err)
	}

	exts := make([]string, 0, len(parsed)+len(failures))
	seen := map[string]bool{}
	for ext := range parsed {
		if !seen[ext] {
			exts, seen[ext] = append(exts, ext), true
		}
	}
	for ext := range failures {
		if !seen[ext] {
			exts, seen[ext] = append(exts, ext), true
		}
	}
	sort.Strings(exts)
	for _, ext := range exts {
		t.Logf("%-6s parsed=%d failed=%d", ext, parsed[ext], len(failures[ext]))
		for _, failure := range failures[ext] {
			t.Errorf("%s %s", ext, failure)
		}
	}
}
