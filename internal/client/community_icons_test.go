package client

import (
	"encoding/binary"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

func TestCommunityIconConfigUsesAuthoredCategoryOrderAndPCXColors(t *testing.T) {
	a := iconUnit("a", "FIRST SECOND", 1)
	b := iconUnit("b", "SECOND", 2)
	c := iconUnit("c", "NOTHING", 3)
	cat := iconTestCatalog(a, b, c)
	registry, err := content.CompileCategories(cat.Units)
	if err != nil {
		t.Fatal(err)
	}
	cat.Categories = registry

	dir := t.TempDir()
	writeCommunityPCX(t, filepath.Join(dir, "first.pcx"), []byte{0, 42}, [][3]byte{{}, {255, 255, 255}})
	writeCommunityPCX(t, filepath.Join(dir, "second.pcx"), []byte{1, 0}, [][3]byte{{}, {255, 255, 255}})
	writeCommunityPCX(t, filepath.Join(dir, "unknown.pcx"), []byte{1, 9}, [][3]byte{{}, {255, 255, 255}})
	writeCommunityPCX(t, filepath.Join(dir, "none.pcx"), []byte{9, 9}, nil)
	config := `[Option]
UseDefaultIcon=false
FillColor=0
TransparentColor=9
SelectedColor=42
HoverColor=77
[Icon]
FIRST=first.pcx
SECOND=second.pcx
nothing=none.pcx
unknow=unknown.pcx
`
	path := filepath.Join(dir, "iconcfg.ini")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	icons, err := LoadStrategicIconCatalog(cat, path)
	if err != nil {
		t.Fatal(err)
	}

	first, _ := icons.Lookup("a", 1)
	second, _ := icons.Lookup("b", 2)
	unknown, _ := icons.Lookup("c", 3)
	if first.Atlas != second.Atlas || second.Atlas != unknown.Atlas {
		t.Fatal("configured icons did not share one atlas")
	}
	if first.communitySelected != 42 || first.communityHover != 77 {
		t.Fatalf("configured highlight colours = %d/%d", first.communitySelected, first.communityHover)
	}
	if !strings.Contains(first.Evidence[len(first.Evidence)-1], "FIRST=first.pcx") {
		t.Fatalf("first matching category did not win: %v", first.Evidence)
	}
	if !strings.Contains(second.Evidence[len(second.Evidence)-1], "SECOND=second.pcx") {
		t.Fatalf("second category evidence = %v", second.Evidence)
	}
	if !strings.Contains(unknown.Evidence[len(unknown.Evidence)-1], "unknow=unknown.pcx") {
		t.Fatalf("unknown fallback evidence = %v", unknown.Evidence)
	}
	firstOffset := (int(first.Rect.Y)*first.Atlas.Width + int(first.Rect.X)) * 4
	if got := first.Atlas.Pixels[firstOffset : firstOffset+8]; got[0] != 255 || got[6] != 255 {
		t.Fatalf("fill/selected masks = %v, want team then highlight", got)
	}
	team := color.RGBA{R: 20, G: 180, B: 70, A: 255}
	halo := color.RGBA{R: 230, G: 40, B: 190, A: 255}
	background := color.RGBA{R: 5, G: 10, B: 15, A: 255}
	plain := StrategicIconPreview(first, 2, team, halo, background, false)
	highlighted := StrategicIconPreview(first, 2, team, halo, background, true)
	if got := plain.RGBAAt(0, 0); got != team {
		t.Fatalf("fill pixel = %v, want team %v", got, team)
	}
	if got := plain.RGBAAt(1, 0); got != background {
		t.Fatalf("ordinary selected-color pixel = %v, want transparent/background %v", got, background)
	}
	if got := highlighted.RGBAAt(1, 0); got != halo {
		t.Fatalf("highlighted selected-color pixel = %v, want %v", got, halo)
	}
}

func TestCommunityIconUseDefaultDoesNotOpenCustomPCX(t *testing.T) {
	u := iconUnit("a", "ALL", 1)
	cat := iconTestCatalog(u)
	registry, err := content.CompileCategories(cat.Units)
	if err != nil {
		t.Fatal(err)
	}
	cat.Categories = registry
	dir := t.TempDir()
	path := filepath.Join(dir, "iconcfg.ini")
	if err := os.WriteFile(path, []byte("[Option]\nUseDefaultIcon=true\n[Icon]\nALL=missing.pcx\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	icons, err := LoadStrategicIconCatalog(cat, path)
	if err != nil {
		t.Fatalf("UseDefaultIcon opened custom art: %v", err)
	}
	d, ok := icons.Lookup("a", 1)
	if !ok || d.communityConfigured || d.Atlas == nil {
		t.Fatalf("built-in icon was not retained: ok=%v descriptor=%+v", ok, d)
	}
}

func TestCommunityIconConfigAcceptsSectionHeaderComments(t *testing.T) {
	u := iconUnit("a", "ALL", 1)
	cat := iconTestCatalog(u)
	registry, err := content.CompileCategories(cat.Units)
	if err != nil {
		t.Fatal(err)
	}
	cat.Categories = registry
	dir := t.TempDir()
	writeCommunityPCX(t, filepath.Join(dir, "all.pcx"), []byte{0, 1}, [][3]byte{{}, {255, 255, 255}})
	writeCommunityPCX(t, filepath.Join(dir, "unknown.pcx"), []byte{1, 0}, [][3]byte{{}, {255, 255, 255}})
	config := `[Option] ; General Settings
UseDefaultIcon=false
[Icon] # Custom Icons
ALL=all.pcx
Unknow=unknown.pcx
`
	path := filepath.Join(dir, "iconcfg.ini")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	icons, err := LoadStrategicIconCatalog(cat, path)
	if err != nil {
		t.Fatal(err)
	}
	d, ok := icons.Lookup("a", 1)
	if !ok || !d.communityConfigured || d.Atlas == nil {
		t.Fatalf("commented section headers did not load custom art: ok=%v descriptor=%+v", ok, d)
	}
	if len(d.Evidence) == 0 || !strings.Contains(d.Evidence[len(d.Evidence)-1], "ALL=all.pcx") {
		t.Fatalf("custom mapping evidence = %v", d.Evidence)
	}
}

func TestCommunityIconPathCaseFallbackIsPortable(t *testing.T) {
	dir := t.TempDir()
	authored := filepath.Join(dir, "MiXeD.PCX")
	writeCommunityPCX(t, authored, []byte{0, 1}, [][3]byte{{}, {255, 255, 255}})
	got, err := resolveCommunityIconPath(dir, "MIXED.pcx")
	if err != nil {
		t.Fatal(err)
	}
	if got != authored {
		t.Fatalf("case-insensitive fallback resolved %q, want directory spelling %q", got, authored)
	}

	// Case-sensitive hosts can represent two folded matches. Neither is a
	// portable winner unless the authored spelling exactly selects one.
	second := filepath.Join(dir, "MIXED.pcx")
	writeCommunityPCX(t, second, []byte{1, 0}, [][3]byte{{}, {255, 255, 255}})
	firstInfo, firstErr := os.Stat(authored)
	secondInfo, secondErr := os.Stat(second)
	if firstErr == nil && secondErr == nil && !os.SameFile(firstInfo, secondInfo) {
		if _, err := resolveCommunityIconPath(dir, "mixed.PcX"); err == nil || !strings.Contains(err.Error(), "ambiguous case-insensitive matches") {
			t.Fatalf("ambiguous folded path error = %v", err)
		}
		got, err := resolveCommunityIconPath(dir, "MIXED.pcx")
		if err != nil || got != second {
			t.Fatalf("exact authored spelling did not win: path=%q err=%v", got, err)
		}
	}
}

func writeCommunityPCX(t *testing.T, path string, pixels []byte, palette [][3]byte) {
	t.Helper()
	if len(pixels) == 0 || len(pixels) > 65535 {
		t.Fatal("test PCX needs one nonempty row")
	}
	data := make([]byte, 128, 128+len(pixels)*2+768)
	data[0], data[1] = 0x0a, 5
	binary.LittleEndian.PutUint16(data[8:10], uint16(len(pixels)-1))
	binary.LittleEndian.PutUint16(data[66:68], uint16(len(pixels)))
	for _, p := range pixels {
		if p >= 0xc0 {
			data = append(data, 0xc1, p)
		} else {
			data = append(data, p)
		}
	}
	trailer := make([]byte, 768)
	for i, rgb := range palette {
		copy(trailer[i*3:i*3+3], rgb[:])
	}
	data = append(data, trailer...)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
