package gpurender

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func checkTransientFrameDevicePixels() error {
	p := fixturePalette()
	r, err := NewChecked(&p, 16, 16)
	if err != nil {
		return err
	}
	defer r.ResetSources()
	data, err := formats.EncodeGAF([]formats.GAFWriteEntry{{Name: "burst", Frames: []formats.GAFWriteFrame{{Width: 3, Height: 2, Pixels: []byte{7, 9, 11, 13, 15, 17}, Transparent: []bool{false, true, false, false, false, false}}}}})
	if err != nil {
		return err
	}
	eager, err := formats.LoadGAF(data)
	if err != nil {
		return err
	}
	root, err := os.MkdirTemp("", "nanolathe-transient-fixture-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	if err := os.WriteFile(filepath.Join(root, "effect.gaf"), data, 0600); err != nil {
		return err
	}
	fs := vfs.New()
	defer fs.Close()
	if err := fs.MountDirectory(root, 0); err != nil {
		return err
	}
	source, err := formats.LoadGAFSourceFile(fs, "effect.gaf", int64(len(data)), formats.DefaultGAFLimits())
	if err != nil {
		return err
	}
	var expected []byte
	for pass := 0; pass < 4; pass++ {
		f := eager.Entries[0].Frames[0].Frame
		if pass != 0 {
			f, err = source.Frame("burst", 0, 6)
			if err != nil {
				return err
			}
		}
		var list drawlist.List
		list.RecordClear()
		list.RecordSprite(drawlist.Sprite{Frame: f, X: 3, Y: 4, Kind: drawlist.BlitKeyed})
		list.RecordSprite(drawlist.Sprite{Frame: f, X: 4, Y: 4, Kind: drawlist.BlitTinted})
		list.RecordExpand()
		out := r.Execute(&list, 16, 16)
		pixels := make([]byte, 16*16*4)
		out.ReadPixels(pixels)
		if pass == 0 {
			expected = pixels
		} else if !bytes.Equal(expected, pixels) {
			return fmt.Errorf("transient upload changed device pixels on pass %d", pass)
		}
		// Retire all transient uploads, then resolve identical authored bytes to
		// new CPU identities. This checks release/reload and padded sampling.
		r.scene.transient.trim(0, 0, false, func(img *ebiten.Image) { img.Deallocate() })
		if len(r.scene.frames) != 1 || r.scene.transient.bytes != 0 || len(r.scene.transient.index) != 0 {
			return fmt.Errorf("transient upload escaped into persistent atlas")
		}
	}
	return captureEscalationEffectRoots()
}

type effectCaptureStage struct {
	frame      *formats.GAFFrame
	x, y, w, h int
}

func (s effectCaptureStage) DrawUI(c *client.Client, _ client.UIFrame) {
	c.UIFillRect(0, 0, s.w, s.h, 32)
	c.UIBlitAnchor(s.frame, s.x, s.y)
}

// Optional inspection artifacts use ordinary classic composition and replay
// that exact recorded list through modern. The largest root canvas in each
// audited bank exercises the upload sizes that motivated on-demand loading.
func captureEscalationEffectRoots() error {
	root, outDir := os.Getenv("NANOLATHE_ESC_GAF_ROOT"), os.Getenv("NANOLATHE_ESC_GAF_CAPTURE")
	if root == "" || outDir == "" {
		return nil
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return err
	}
	fs := vfs.New()
	defer fs.Close()
	base := os.Getenv("NANOLATHE_RETAIL_ROOT")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		base = filepath.Join(home, "TotalAnnihilation")
	}
	if err := fs.MountGameDirectories([]string{base, root}); err != nil {
		return err
	}
	pal, err := palette.Load(fs)
	if err != nil {
		return err
	}
	for _, name := range []string{"esc_nuke_a_02", "esc_nuke_x_01", "esc_weap_x_01"} {
		if err := captureEscalationEffectRoot(fs, pal, name, outDir); err != nil {
			return err
		}
	}
	return nil
}
func captureEscalationEffectRoot(fs vfs.FSOps, pal *palette.Tables, name, outDir string) error {
	limits := formats.DefaultGAFLimits()
	limits.MaxDecodedPixels, limits.MaxExpandedPixels = 512<<20, 512<<20
	source, err := formats.LoadGAFSourceFile(fs, "anims/"+name+".gaf", 256<<20, limits)
	if err != nil {
		return err
	}
	entry := &source.Metadata().Entries[0]
	selected := 0
	for i, ref := range entry.Frames {
		if uint64(ref.Frame.Width)*uint64(ref.Frame.Height) > uint64(entry.Frames[selected].Frame.Width)*uint64(entry.Frames[selected].Frame.Height) {
			selected = i
		}
	}
	f, err := source.Frame(entry.Name, selected, 32<<20)
	if err != nil {
		return err
	}
	bounds := image.Rectangle{}
	first := true
	var visit func(*formats.GAFFrame)
	visit = func(f *formats.GAFFrame) {
		if len(f.Subframes) > 0 {
			for _, child := range f.Subframes {
				visit(child)
			}
			return
		}
		r := image.Rect(-int(f.XOffset), -int(f.YOffset), -int(f.XOffset)+int(f.Width), -int(f.YOffset)+int(f.Height))
		if first {
			bounds = r
			first = false
		} else {
			bounds = bounds.Union(r)
		}
	}
	visit(f)
	w, h := bounds.Dx()+32, bounds.Dy()+32
	cl, err := client.New(client.Options{Width: w, Height: h})
	if err != nil {
		return err
	}
	cl.SetPalette(pal)
	cl.SetUIStage(effectCaptureStage{f, 16 - bounds.Min.X, 16 - bounds.Min.Y, w, h})
	snapshot := cl.ComposeFrameSnapshot()
	r, err := NewChecked(pal, w, h)
	if err != nil {
		return err
	}
	defer r.ResetSources()
	img := r.Execute(&snapshot.List, w, h)
	if img == nil {
		return fmt.Errorf("missing effect capture %s", name)
	}
	modern := image.NewRGBA(image.Rect(0, 0, w, h))
	img.ReadPixels(modern.Pix)
	classic := image.NewRGBA(modern.Bounds())
	copy(classic.Pix, snapshot.RGBA)
	for _, item := range []struct {
		name string
		img  image.Image
	}{{"classic", classic}, {"modern", modern}} {
		path := filepath.Join(outDir, fmt.Sprintf("%s-frame-%02d-%s.png", name, selected, item.name))
		file, err := os.Create(path)
		if err != nil {
			return err
		}
		err = png.Encode(file, item.img)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
