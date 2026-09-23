package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func TestModelMaterialExplicitArtAndPacket(t *testing.T) {
	for _, name := range []string{"unknown", "32xlogos", "glow", "energy1", "armbld1"} {
		if modelTextureMaterial(name) != drawlist.ModelMaterialDefault {
			t.Fatalf("unannotated art %q has a material", name)
		}
	}
	for _, tc := range []struct {
		name string
		want uint8
	}{{"CoLoRsLt", drawlist.ModelMaterialMetal}, {"colorsmd", drawlist.ModelMaterialMetal}, {"colorsdk", drawlist.ModelMaterialMetal}, {"colordk2", drawlist.ModelMaterialMetal}, {"MeTaL3c", drawlist.ModelMaterialMetal}, {"corsea6d", drawlist.ModelMaterialMetal}, {"CAMOB3", drawlist.ModelMaterialPaint}, {"bluenoise4", drawlist.ModelMaterialPaint}} {
		if got := modelTextureMaterial(tc.name); got != tc.want {
			t.Fatalf("%s material = %d, want %d", tc.name, got, tc.want)
		}
		p := newScreenPoly(4)
		p.material = tc.want
		g := modelGeometryPacketAt([]screenPoly{p}, 1, 1, 0, 0, 0, 0, 1, true, drawlist.ModelFallbackNone)
		if g.Faces[0].Material != tc.want || g.Clone().Faces[0].Material != tc.want {
			t.Fatal("packet or clone lost material")
		}
		p.material = drawlist.ModelMaterialDefault
		fillModelPacket(g, make([]drawlist.ModelVertex, 4), []screenPoly{p}, 1, 1, 0, 0, 0, 0, 1, true, drawlist.ModelFallbackNone, nil)
		if g.Faces[0].Material != drawlist.ModelMaterialDefault {
			t.Fatal("reused packet retained the previous material")
		}
	}
}

// restoreMaterialTable puts the table in force back after a test replaces it.
// Putting it back is an install like any other, so the generation advances too;
// otherwise a byte cached under the replacement's generation would survive the
// restore and answer for a table that is no longer installed.
func restoreMaterialTable(t *testing.T) {
	t.Helper()
	before := materialTable.Load()
	t.Cleanup(func() {
		materialTable.Store(before)
		materialGeneration.Add(1)
	})
}

// A resolved texture reference carries its finish so the per-face compose path
// never lowercases a texture name (DESIGN_GPU_RENDERER §29.1). The cached byte
// must agree with the table in force for every spelling, and an install must
// invalidate it.
func TestTextureReferenceCachesMaterialAnnotation(t *testing.T) {
	restoreMaterialTable(t)
	c := testModelTextureClient()
	names := []string{"CoLoRsLt", "metal3a", "MeTaL3C", "CAMOB3", "bluenoise4", "MySheet", "unknown", ""}
	refs := make([]texRef, len(names))
	for i, name := range names {
		refs[i], _ = c.resolveModelTexture(name)
		if got, want := refs[i].materialAnnotation(name), modelTextureMaterial(name); got != want {
			t.Fatalf("cached material for %q = %d, want %d", name, got, want)
		}
	}

	// An override replaces the table whole. Every byte cached above was read
	// from the previous one, so every one of them must be resolved again.
	authored := []byte("[materials]\n\t{\n\tmetal3a=paint;\n\tMYSHEET=metal;\n\t}\n")
	if err := SetMaterialTable("test", authored, "test"); err != nil {
		t.Fatal(err)
	}
	for i, name := range names {
		if got, want := refs[i].materialAnnotation(name), modelTextureMaterial(name); got != want {
			t.Fatalf("after the install, stale cached material for %q = %d, want %d", name, got, want)
		}
	}
	if got := refs[1].materialAnnotation(names[1]); got != drawlist.ModelMaterialPaint {
		t.Fatalf("metal3a did not follow the install: %d", got)
	}
	if got := refs[0].materialAnnotation(names[0]); got != drawlist.ModelMaterialDefault {
		t.Fatalf("colorslt survived a replacement that dropped it: %d", got)
	}

	// A reference resolved after the install caches the new table's byte.
	fresh, _ := c.resolveModelTexture("MySheet")
	if fresh.material != drawlist.ModelMaterialMetal || fresh.materialAnnotation("MySheet") != drawlist.ModelMaterialMetal {
		t.Fatalf("reference resolved under the override cached %d", fresh.material)
	}
}

// The embedded annotation is the whole table: every classification the renderer
// applies comes from the authored file, so a name missing from it is a
// regression in the data, not in the code.
func TestEmbeddedMaterialTableCoversTheAuthoredClassification(t *testing.T) {
	table, _, err := parseContentTable(embeddedMaterialTDF)
	if err != nil {
		t.Fatalf("embedded material annotation does not parse: %v", err)
	}
	metal := []string{"colorslt", "colorsmd", "colorsdk", "colordk2", "metal3a", "metal3b", "metal3c", "metal3d", "graynoise1", "graynoise2", "graynoise3", "graynoise4", "graynoise5", "arm01b", "arm01c", "arm01d", "corsea5a", "corsea5b", "corsea5c", "corsea5d", "corsea6a", "corsea6b", "corsea6c", "corsea6d"}
	paint := []string{"camob2", "camob3", "camob4", "camob5", "camob6", "camoflage", "descamo2", "descamo3", "descamo4", "corcam4b", "corcam4c", "corcam4d", "corcam5c", "corcam5d", "corcam6c", "corcam6d", "camod01", "camod02", "camoe01", "camoe02", "armcam2a", "bluenoise1", "bluenoise2", "bluenoise3", "bluenoise4"}
	for _, name := range metal {
		if table[name] != drawlist.ModelMaterialMetal {
			t.Fatalf("%s is not annotated metal", name)
		}
	}
	for _, name := range paint {
		if table[name] != drawlist.ModelMaterialPaint {
			t.Fatalf("%s is not annotated paint", name)
		}
	}
	if len(table) != len(metal)+len(paint) {
		t.Fatalf("embedded table annotates %d textures, want %d", len(table), len(metal)+len(paint))
	}
}

func TestMaterialTableOverrideReplacesTheEmbeddedTable(t *testing.T) {
	restoreMaterialTable(t)
	authored := []byte("[materials]\n\t{\n\tmetal3a=paint;\n\tMYSHEET=metal;\n\tcamob3=none;\n\t}\n")
	if err := SetMaterialTable("test", authored, "test"); err != nil {
		t.Fatal(err)
	}
	// The override replaces the table whole rather than merging into it.
	if got := modelTextureMaterial("MetAl3a"); got != drawlist.ModelMaterialPaint {
		t.Fatalf("override did not reclassify metal3a: %d", got)
	}
	if got := modelTextureMaterial("mysheet"); got != drawlist.ModelMaterialMetal {
		t.Fatalf("override did not annotate its own name: %d", got)
	}
	for _, gone := range []string{"camob3", "corsea6d", "bluenoise4"} {
		if got := modelTextureMaterial(gone); got != drawlist.ModelMaterialDefault {
			t.Fatalf("%s survived the replacement as %d", gone, got)
		}
	}
}

func TestMalformedMaterialOverrideKeepsTheEmbeddedTable(t *testing.T) {
	restoreMaterialTable(t)
	for _, bad := range [][]byte{
		[]byte("this is not a TDF document"),
		[]byte("[somethingelse]\n\t{\n\tmetal3a=metal;\n\t}\n"),
		[]byte("[materials]\n\t{\n\t}\n"),
	} {
		err := SetMaterialTable(MaterialTablePath, bad, "packa, packb")
		if err == nil {
			t.Fatalf("malformed override %q was accepted", bad)
		}
		// One diagnostic shape [AGENTS.md §Diagnostics].
		want := "nanolathe: material annotation override is unreadable: logical path " + MaterialTablePath + ", providers searched [packa, packb], expected "
		if !strings.HasPrefix(err.Error(), want) {
			t.Fatalf("diagnostic shape:\n got %q\nwant prefix %q", err.Error(), want)
		}
		// The embedded classification is still what the renderer sees.
		if modelTextureMaterial("corsea6d") != drawlist.ModelMaterialMetal || modelTextureMaterial("camob3") != drawlist.ModelMaterialPaint {
			t.Fatal("a broken override dropped the embedded annotation")
		}
	}
}

// A mounted install supplying the logical path replaces the table; no such file
// is the ordinary case and leaves the embedded one alone.
func TestLoadMaterialTableFromMountedContent(t *testing.T) {
	restoreMaterialTable(t)
	dir := t.TempDir()
	empty := vfs.New()
	if err := empty.MountDirectory(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = empty.Close() })
	if err := LoadMaterialTable(empty); err != nil {
		t.Fatalf("an install with no override reported an error: %v", err)
	}
	if modelTextureMaterial("corsea6d") != drawlist.ModelMaterialMetal {
		t.Fatal("no override must leave the embedded annotation in force")
	}

	pack := t.TempDir()
	if err := os.MkdirAll(filepath.Join(pack, "nanolathe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, MaterialTablePath), []byte("[materials]\n\t{\n\tmysheet=metal;\n\t}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mounted := vfs.New()
	if err := mounted.MountDirectory(pack, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mounted.Close() })
	if err := LoadMaterialTable(mounted); err != nil {
		t.Fatal(err)
	}
	if modelTextureMaterial("MySheet") != drawlist.ModelMaterialMetal {
		t.Fatal("the mounted override was not installed")
	}
	if modelTextureMaterial("corsea6d") != drawlist.ModelMaterialDefault {
		t.Fatal("the mounted override did not replace the embedded table")
	}
}

// restoreGlowFamilies puts the family strengths in force back after a test
// installs an override.
func restoreGlowFamilies(t *testing.T) {
	t.Helper()
	before := glowFamilies.Load()
	t.Cleanup(func() { glowFamilies.Store(before) })
}

// The annotation file's [effects] section sets the content pack's effect family
// strengths (DESIGN_GPU_RENDERER §19.4). A file with only [effects] keeps the
// texture table in force; one with only [materials] restores the default
// families; values clamp to 0..200 and a non-number rejects the whole file.
func TestMaterialOverrideEffectsSection(t *testing.T) {
	restoreMaterialTable(t)
	restoreGlowFamilies(t)
	var cl *Client
	if w, n, g := cl.GlowFamilies(); w != 100 || n != 100 || g != 100 {
		t.Fatalf("embedded families %d/%d/%d, want 100 each", w, n, g)
	}

	effectsOnly := []byte("[effects]\n\t{\n\tNanolathe=35;\n\tground=250;\n\tweapons=-5;\n\tfootprints=40;\n\ttracks=250;\n\tfuture=7;\n\t}\n")
	if err := SetMaterialTable("test", effectsOnly, "test"); err != nil {
		t.Fatalf("an [effects]-only override was rejected: %v", err)
	}
	if w, n, g := cl.GlowFamilies(); w != 0 || n != 35 || g != GlowFamilyMax {
		t.Fatalf("[effects]-only families %d/%d/%d, want 0/35/%d", w, n, g, GlowFamilyMax)
	}
	if feet, tracks := cl.trailFamilyStrength(trailFeet), cl.trailFamilyStrength(trailTracks); feet != 40 || tracks != 200 {
		t.Fatalf("trail families %d/%d, want 40/200", feet, tracks)
	}
	if modelTextureMaterial("corsea6d") != drawlist.ModelMaterialMetal {
		t.Fatal("an override without [materials] dropped the texture table in force")
	}

	both := []byte("[materials]\n\t{\n\tmysheet=metal;\n\t}\n[effects]\n\t{\n\tweapons=80;\n\t}\n")
	if err := SetMaterialTable("test", both, "test"); err != nil {
		t.Fatal(err)
	}
	if w, n, g := cl.GlowFamilies(); w != 80 || n != 100 || g != 100 {
		t.Fatalf("families with both sections %d/%d/%d, want 80/100/100", w, n, g)
	}
	if modelTextureMaterial("mysheet") != drawlist.ModelMaterialMetal || modelTextureMaterial("corsea6d") != drawlist.ModelMaterialDefault {
		t.Fatal("the [materials] section did not replace the table")
	}

	materialsOnly := []byte("[materials]\n\t{\n\tmetal3a=metal;\n\t}\n")
	if err := SetMaterialTable("test", materialsOnly, "test"); err != nil {
		t.Fatal(err)
	}
	if w, n, g := cl.GlowFamilies(); w != 100 || n != 100 || g != 100 {
		t.Fatalf("a [materials]-only override left families %d/%d/%d, want the defaults", w, n, g)
	}
	if feet, tracks := cl.trailFamilyStrength(trailFeet), cl.trailFamilyStrength(trailTracks); feet != 100 || tracks != 100 {
		t.Fatalf("material-only override left trail families %d/%d, want defaults", feet, tracks)
	}

	bad := []byte("[effects]\n\t{\n\tnanolathe=lots;\n\t}\n")
	if err := SetMaterialTable(MaterialTablePath, bad, "pack"); err == nil {
		t.Fatal("a non-numeric family strength was accepted")
	}
	if _, n, _ := cl.GlowFamilies(); n != 100 {
		t.Fatalf("a rejected override changed the nanolathe family to %d", n)
	}
}
