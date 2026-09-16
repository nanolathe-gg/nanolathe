package content

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// An authored download with a mismatched resource name remains discoverable
// without preventing an unrelated stock opening (DESIGN_CONTENT_VFS §2.3).
func TestSecondaryWarningReachesCatalogWithoutBlockingStockOpening(t *testing.T) {
	fs := vfs.New()
	defer fs.Close()
	if err := fs.MountGameDirectory(testsupport.RetailRoot(t)); err != nil {
		t.Fatal(err)
	}
	definition := `[UNITINFO]{Version=3.1; Copyright=Copyright 1997 Humongous Entertainment. All rights reserved.; UnitName=review_missing; ObjectName=armcom; Side=ARM;}`
	var bank bytes.Buffer
	if err := vfs.WriteArchive(&bank, []vfs.ArchiveFile{{Path: "units/review_download.fbi", Data: []byte(definition)}}, vfs.ArchiveWriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.MountArchiveReader("secondary-mismatch.ufo", bytes.NewReader(bank.Bytes()), int64(bank.Len()), 1000, vfs.ArchiveOptions{}); err != nil {
		t.Fatal(err)
	}
	cat, err := Compile(fs)
	if err != nil {
		t.Fatal(err)
	}
	u, ok := cat.Unit("review_missing")
	if !ok || !u.DiscoveryOnly {
		t.Fatal("catalog lost the incomplete download's discovery identity")
	}
	found := false
	for _, warning := range cat.Warnings {
		if strings.Contains(warning, "logical path units/review_missing.fbi") && strings.Contains(warning, "units/review_download.fbi from provider secondary-mismatch.ufo") {
			found = true
		}
	}
	if !found {
		t.Fatalf("catalog did not publish the secondary diagnostic: %v", cat.Warnings)
	}
	// The unrelated stock opening is untouched by the mismatched download:
	// the side's own commander still compiles as a complete, playable
	// definition with its program and weapon links resolved. There is no
	// separate admission gate to consult — a definition either survives the
	// compile with its links, or it is refused where a unit is created
	// (DESIGN_CONTENT_VFS §3.5).
	if len(cat.Sides) == 0 || cat.Sides[0] == nil {
		t.Fatal("compiled corpus lost its side records")
	}
	commander, ok := cat.Unit(cat.Sides[0].Commander)
	if !ok || commander == nil || commander.DiscoveryOnly {
		t.Fatalf("stock commander %q lost its playable identity", cat.Sides[0].Commander)
	}
	if commander.Script == nil || commander.Weapon1Def == nil {
		t.Fatalf("stock commander %q lost its script or weapon links", commander.CanonicalKey)
	}
}
