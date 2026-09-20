package film

import (
	"bytes"
	"embed"
	"encoding/json"
	"image"
	"image/draw"
	"image/png"
	"sync"
)

// These are presentation assets, independent of the retail game's fonts.
// Provenance, reproduction instructions and OFL terms accompany the atlases.
//
//go:embed assets/*.png assets/*.json assets/OFL.txt assets/README.md
var fontAssets embed.FS

type atlasGlyph struct {
	X, Y, W, H int
	Left, Top  int
	Advance    float64
	levels     []*image.Gray
}

type atlasFace struct {
	Cap     float64
	Glyphs  map[string]*atlasGlyph
	Kerning map[string]float64
}

var filmFaces = sync.OnceValue(func() map[string]*atlasFace {
	faces := make(map[string]*atlasFace)
	for _, name := range []string{"display", "body"} {
		metadata, err := fontAssets.ReadFile("assets/" + name + ".json")
		if err != nil {
			panic(err)
		}
		face := new(atlasFace)
		if err := json.Unmarshal(metadata, face); err != nil {
			panic(err)
		}
		data, err := fontAssets.ReadFile("assets/" + name + ".png")
		if err != nil {
			panic(err)
		}
		atlas, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			panic(err)
		}
		for _, g := range face.Glyphs {
			if g.W == 0 || g.H == 0 {
				continue
			}
			// A transparent border prevents neighbouring glyphs from bleeding
			// into the filtered edge. Mips preserve coverage in small captions.
			base := image.NewGray(image.Rect(0, 0, g.W+4, g.H+4))
			draw.Draw(base, image.Rect(2, 2, g.W+2, g.H+2), atlas, image.Pt(g.X, g.Y), draw.Src)
			g.levels = append(g.levels, base)
			for base.Bounds().Dx() > 8 && base.Bounds().Dy() > 8 {
				next := image.NewGray(image.Rect(0, 0, (base.Bounds().Dx()+1)/2, (base.Bounds().Dy()+1)/2))
				for y := 0; y < next.Bounds().Dy(); y++ {
					for x := 0; x < next.Bounds().Dx(); x++ {
						sum := 0
						for dy := 0; dy < 2; dy++ {
							for dx := 0; dx < 2; dx++ {
								sum += int(base.GrayAt(x*2+dx, y*2+dy).Y)
							}
						}
						next.Pix[y*next.Stride+x] = uint8((sum + 2) / 4)
					}
				}
				g.levels = append(g.levels, next)
				base = next
			}
		}
		faces[name] = face
	}
	return faces
})

func textFace(name string) *atlasFace {
	if name == "body" {
		return filmFaces()["body"]
	}
	return filmFaces()["display"]
}

// Unsupported characters retain a space's advance, preserving the existing
// script contract instead of collapsing the rest of the line.
func (f *atlasFace) glyph(r rune) (*atlasGlyph, rune) {
	if g, ok := f.Glyphs[string(r)]; ok {
		return g, r
	}
	return f.Glyphs[" "], ' '
}
