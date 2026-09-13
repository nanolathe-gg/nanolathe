package content

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func TestCatalogRetainsRejectedMapWarning(t *testing.T) {
	fs := vfs.New()
	defer fs.Close()
	if err := fs.MountGameDirectory(testsupport.RetailRoot(t)); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := vfs.WriteArchive(&archive, []vfs.ArchiveFile{
		{Path: "maps/authored-scratch.ota", Data: []byte("[Scratch] {}")},
		{Path: "maps/authored-scratch.tnt", Data: []byte("must not read")},
	}, vfs.ArchiveWriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.MountArchiveReader("authored-map-pack.ufo", bytes.NewReader(archive.Bytes()), int64(archive.Len()), 1000, vfs.ArchiveOptions{}); err != nil {
		t.Fatal(err)
	}
	catalog, err := Compile(fs)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Maps["authored-scratch"] != nil {
		t.Fatal("rejected candidate entered catalog")
	}
	for _, warning := range catalog.Warnings {
		if strings.Contains(warning, "logical path maps/authored-scratch.ota, providers searched [authored-map-pack.ufo]") {
			return
		}
	}
	t.Fatalf("catalog dropped rejected map provenance: %v", catalog.Warnings)
}

func TestMapDiscoveryRejectsMissingHeaderBeforeTerrainRead(t *testing.T) {
	fs := newFixtureFS(t,
		fixtureFile{path: "maps/a-scratch.ota", data: "[Scratch] { note=unfinished; }"},
		fixtureFile{path: "maps/a-scratch.tnt", data: "unreadable terrain"},
		fixtureFile{path: "maps/z-playable.ota", data: "[GlobalHeader] { [Schema 0] { Type=Network 1; } }"},
		fixtureFile{path: "maps/z-playable.tnt", data: mapDiscoveryTerrainFixture()},
	)
	maps, warnings, err := compileMapsWithDiagnostics(fs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if maps["a-scratch"] != nil {
		t.Fatal("headerless candidate must not become a usable map [02 R-MAP-01 §9]")
	}
	playable := maps["z-playable"]
	if playable == nil || playable.TNTWidth != 64 || playable.TNTHeight != 32 {
		t.Fatalf("later map must retain real terrain metadata: %+v", playable)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "logical path maps/a-scratch.ota, providers searched [fixture.hpi]") {
		t.Fatalf("rejection diagnostics: %v", warnings)
	}
}

func TestMapDiscoveryKeepsCampaignSchemas(t *testing.T) {
	fs := newFixtureFS(t,
		fixtureFile{path: "maps/campaign.ota", data: "[GlobalHeader] { [Schema 0] { Type=Easy; } }"},
		fixtureFile{path: "maps/campaign.tnt", data: mapDiscoveryTerrainFixture()},
	)
	maps, err := CompileMaps(fs)
	if err != nil || maps["campaign"] == nil {
		t.Fatalf("catalog is shared with campaign; browser owns network filtering: %v, %v", maps, err)
	}
}

func TestMapDiscoveryKeepsSyntaxAndTerrainFailuresFatal(t *testing.T) {
	for _, test := range []struct {
		name, ota, tnt string
		wantParse      bool
	}{
		{"syntax", "[Scratch] { note=unterminated", mapDiscoveryTerrainFixture(), true},
		{"terrain", "[GlobalHeader] {}", "short", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fs := newFixtureFS(t,
				fixtureFile{path: "maps/bad.ota", data: test.ota},
				fixtureFile{path: "maps/bad.tnt", data: test.tnt},
			)
			maps, err := CompileMaps(fs)
			if err == nil || maps != nil {
				t.Fatalf("required content failure became usable map: %v, %v", maps, err)
			}
			var parse *formats.ParseError
			if test.wantParse && !errors.As(err, &parse) {
				t.Fatalf("syntax diagnostic lost: %v", err)
			}
		})
	}
}

func TestMapDiscoveryUsesParserByteLimit(t *testing.T) {
	// Authored comments may exceed the old compiler-only cap without exceeding
	// the format parser's host bound [02 R-MALF-01 §4].
	ota := "/*" + strings.Repeat("x", 1<<20) + "*/[GlobalHeader] {}"
	fs := newFixtureFS(t,
		fixtureFile{path: "maps/large.ota", data: ota},
		fixtureFile{path: "maps/large.tnt", data: mapDiscoveryTerrainFixture()},
	)
	if maps, err := CompileMaps(fs); err != nil || maps["large"] == nil {
		t.Fatalf("OTA below parser byte limit rejected: %v", err)
	}
	fs.files["maps/large.ota"] = strings.Repeat("x", formats.DefaultTDFLimits().MaxBytes+1)
	if _, err := CompileMaps(fs); err == nil {
		t.Fatal("parser host byte limit must remain fatal")
	}
}

type failingMapReadFS struct {
	*fixtureFS
	readErr error
}

func (f failingMapReadFS) ReadFileLimit(string, int64) ([]byte, error) { return nil, f.readErr }

func TestMapDiscoveryPreservesReadFailure(t *testing.T) {
	cause := errors.New("authored read failure")
	fs := failingMapReadFS{newFixtureFS(t,
		fixtureFile{path: "maps/unreadable.ota", data: "[GlobalHeader] {}"},
		fixtureFile{path: "maps/unreadable.tnt", data: mapDiscoveryTerrainFixture()},
	), cause}
	if _, err := CompileMaps(fs); !errors.Is(err, cause) {
		t.Fatalf("read failure lost: %v", err)
	}
}

var _ vfs.FSOps = failingMapReadFS{}

func mapDiscoveryTerrainFixture() string {
	data := make([]byte, 64)
	binary.LittleEndian.PutUint32(data, 0x2000)
	binary.LittleEndian.PutUint32(data[4:], 64)
	binary.LittleEndian.PutUint32(data[8:], 32)
	return string(data)
}
