package client

import (
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	compiledmodel "github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func TestCommunityPreviewPieceListFacingPrecedesGlobal(t *testing.T) {
	def := &content.UnitDef{PreviewPieces: "body turret", PreviewPiecesE: "east_only"}
	if got := communityPreviewPieceList(def, 1); got != "east_only" {
		t.Fatalf("east list = %q", got)
	}
	if got := communityPreviewPieceList(def, 2); got != "body turret" {
		t.Fatalf("north fallback = %q", got)
	}
	def.PreviewPiecesE = "  "
	if got := communityPreviewPieceList(def, 1); got != "body turret" {
		t.Fatalf("empty east did not fall through: %q", got)
	}
	parsed := parseCommunityPreviewPieces(" Body,TuRRet; leg\nWAKE1 ")
	for _, name := range []string{"body", "turret", "leg", "wake1"} {
		if _, ok := parsed[name]; !ok {
			t.Fatalf("missing case-folded piece %q in %#v", name, parsed)
		}
	}
}

func TestCommunityPreviewDroppedParentStillTraversesChild(t *testing.T) {
	primitive := compiledmodel.Primitive{VertexIndices: []uint16{0, 1, 2}}
	src := &unitModel{compiled: &compiledmodel.Model{Root: 0, Pieces: []compiledmodel.Piece{
		{Name: "muzzle", Parent: -1, Children: []int{1}, Primitives: []compiledmodel.Primitive{primitive}},
		{Name: "body", Parent: 0, Primitives: []compiledmodel.Primitive{primitive}},
	}}}
	filtered := filterCommunityPreviewModel(src, "")
	if len(filtered.compiled.Pieces[0].Primitives) != 0 {
		t.Fatal("ephemeral parent faces survived")
	}
	if len(filtered.compiled.Pieces[0].Children) != 1 || len(filtered.compiled.Pieces[1].Primitives) != 1 {
		t.Fatal("dropping a parent also dropped its child traversal")
	}
	whitelisted := filterCommunityPreviewModel(src, "muzzle")
	if len(whitelisted.compiled.Pieces[0].Primitives) != 1 || len(whitelisted.compiled.Pieces[1].Primitives) != 0 {
		t.Fatal("whitelist did not select only its named piece")
	}
}

func TestCommunityPreviewMissingSubstituteFallsBackAndReportsOnce(t *testing.T) {
	fs := vfs.New()
	defer fs.Close()
	base := &unitModel{compiled: &compiledmodel.Model{Name: "base", Pieces: []compiledmodel.Piece{{Name: "base"}}}}
	c := &Client{modelFS: fs}
	for range 2 {
		if got := c.communityPreviewModel(base, "base", "missing.3do"); got != base {
			t.Fatal("missing substitute did not fall back to base")
		}
	}
	if len(c.artDiagnostics) != 1 || c.artDiagnostics[0].Path != "objects3d/missing.3do" {
		t.Fatalf("diagnostics = %#v", c.artDiagnostics)
	}
	if _, ok := c.communityPreviewModels[communityPreviewModelKey{fs: fs, name: "objects3d/missing.3do"}]; !ok {
		t.Fatal("missing substitute was not negatively cached")
	}
}

func TestCommunityPreviewLoadsBareSubstituteWithExtension(t *testing.T) {
	authored := &formats.ThreeDO{Root: 0, Objects: []formats.ThreeDOObject{{
		Version: 1, Selection: -1, Name: "alternate", Parent: -1, FirstChild: -1, NextSibling: -1,
		Vertices:   []formats.ThreeDOVertex{{}, {X: 1}, {Y: 1}},
		Primitives: []formats.ThreeDOPrimitive{{ColorIndex: 1, IsColored: 1, VertexIndices: []uint16{0, 1, 2}}},
	}}}
	data, err := formats.EncodeThreeDO(authored)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "objects3d", "alternate.3do")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 1); err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	base := &unitModel{compiled: &compiledmodel.Model{Name: "base", Pieces: []compiledmodel.Piece{{Name: "base"}}}}
	c := &Client{modelFS: fs}
	got := c.communityPreviewModel(base, "base", "alternate.3do")
	if got == nil || got == base || got.compiled == nil || got.compiled.Name != "objects3d/alternate.3do" {
		t.Fatalf("substitute = %#v", got)
	}
	if len(c.artDiagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %#v", c.artDiagnostics)
	}
}

func TestCommunityPreviewRetailFullAndWireframe(t *testing.T) {
	fs := vfs.New()
	if err := fs.MountGameDirectory(testsupport.RetailRoot(t)); err != nil {
		t.Skipf("retail assets not mountable: %v", err)
	}
	defer fs.Close()
	tables, err := palette.Load(fs)
	if err != nil {
		t.Skipf("retail palette not loadable: %v", err)
	}
	c, err := New(Options{Width: 192, Height: 160})
	if err != nil {
		t.Fatal(err)
	}
	c.SetPalette(tables)
	c.SetModelFS(fs)
	def := &content.UnitDef{UnitName: "armcom", ObjectName: "armcom", BMCode: 1, ZBuffer: true}

	full := captureCommunityPreviewStyle(t, c, def, CommunityPreviewFull)
	if len(c.list.ModelCommands()) == 0 {
		t.Fatal("full preview recorded no model")
	}
	wire := captureCommunityPreviewStyle(t, c, def, CommunityPreviewWireframe)
	if foregroundPixels(wire, tables.Base[0]) == 0 {
		t.Fatal("wireframe preview rendered no edges")
	}
	if path := os.Getenv("NANOLATHE_COMMUNITY_PREVIEW_CAPTURE"); path != "" {
		contact := image.NewRGBA(image.Rect(0, 0, 384, 160))
		draw.Draw(contact, image.Rect(0, 0, 192, 160), full, image.Point{}, draw.Src)
		draw.Draw(contact, image.Rect(192, 0, 384, 160), wire, image.Point{}, draw.Src)
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, contact); err != nil {
			f.Close()
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func foregroundPixels(img *image.RGBA, background [4]uint8) int {
	count := 0
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i] != background[0] || img.Pix[i+1] != background[1] || img.Pix[i+2] != background[2] {
			count++
		}
	}
	return count
}

func captureCommunityPreviewStyle(t *testing.T, c *Client, def *content.UnitDef, style CommunityPreviewStyle) *image.RGBA {
	t.Helper()
	const width, height = 192, 160
	c.width, c.height, c.recordW, c.recordH = width, height, width, height
	c.indexed = make([]uint8, width*height)
	c.rgba = make([]byte, width*height*4)
	c.cam = &camera.Camera{ViewW: width, ViewH: height, MapW: width, MapH: height, Scale: camera.ViewScale(2).Norm()}
	c.list.Reset()
	c.list.RecordClear()
	c.list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: width, H: height}, Index: 0, Style: drawlist.FillSolid})
	x := numeric.Fixed(int64(width/2) << 16)
	z := numeric.Fixed(int64(height/2) << 16)
	if !c.DrawCommunityBuildPreview(CommunityPreviewOptions{Definition: def, Heading: 32768, X: x, Z: z, Style: style, ColorKnown: true}) {
		t.Fatalf("style %d was not drawn", style)
	}
	c.list.Replay(c.classicSink())
	c.convertIndexedToRGBA()
	out := image.NewRGBA(image.Rect(0, 0, width, height))
	copy(out.Pix, c.rgba)
	return out
}
