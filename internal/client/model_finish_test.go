package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

func TestModelMaterialExplicitArtAndPacket(t *testing.T) {
	for _, name := range []string{"unknown", "colorslt", "32xlogos", "glow", "energy1", "armbld1"} {
		if modelTextureMaterial(name) != drawlist.ModelMaterialDefault {
			t.Fatalf("unannotated art %q has a material", name)
		}
	}
	for _, tc := range []struct {
		name string
		want uint8
	}{{"MeTaL3c", drawlist.ModelMaterialMetal}, {"corsea6d", drawlist.ModelMaterialMetal}, {"CAMOB3", drawlist.ModelMaterialPaint}, {"bluenoise4", drawlist.ModelMaterialPaint}} {
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
