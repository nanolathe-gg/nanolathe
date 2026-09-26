package client

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// A content-selected logo bank replaces LOGOS, including when both banks
// supply the same texture. Other ten-frame banks remain ordinary animation.
func TestContentSelectedTeamBank(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "textures"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, bank := range []struct {
		file, entry string
		base        byte
	}{{"logos", "team", 20}, {"alternate", "team", 40}, {"ordinary", "animated", 60}} {
		frames := make([]formats.GAFWriteFrame, 10)
		for i := range frames {
			frames[i] = formats.GAFWriteFrame{Width: 1, Height: 1, Duration: 1, Pixels: []byte{bank.base + byte(i)}}
		}
		data, err := formats.EncodeGAF([]formats.GAFWriteEntry{{Name: bank.entry, Loop: true, Frames: frames}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "textures", bank.file+".gaf"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fs := vfs.New()
	t.Cleanup(func() { fs.Close() })
	if err := fs.MountDirectory(root, 1); err != nil {
		t.Fatal(err)
	}
	r, err := NewModelTextureRegistry(fs, nil, nil, 0, `TEXTURES\ALTERNATE.GAF`)
	if err != nil {
		t.Fatal(err)
	}
	team, ok := r.resolve("team")
	if !ok || team.kind != texTeam || team.entry.Frames[7].Frame.Pixels[0] != 47 {
		t.Fatalf("selected team texture = %+v", team)
	}
	ordinary, ok := r.resolve("animated")
	if !ok || ordinary.kind != texAnimated {
		t.Fatal("ordinary ten-frame animation became a team texture")
	}
	for range 20 {
		r.StepPhase7()
	}
	if len(r.players) != 0 || team.entry.Frames[7].Frame.Pixels[0] != 47 {
		t.Fatal("team colours advanced with the texture clock")
	}
	cl := &Client{}
	cl.SetModelFS(fs, "textures/alternate.gaf")
	if ref, ok := resolveTextureRef(cl.texIndex, cl.logoIndex, "team"); !ok || ref.kind != texTeam {
		t.Fatal("standalone composition ignored the selected team bank")
	}
}
