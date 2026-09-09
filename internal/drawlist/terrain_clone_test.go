package drawlist

import (
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"testing"
)

func TestCloneOwnsTerrainCamera(t *testing.T) {
	original := camera.Camera{X: 7, Z: 11, Scale: camera.ViewScaleDetail, ViewW: 32, ViewH: 24}
	cam := original
	var list List
	list.RecordTerrain(Terrain{Cam: &cam, OriginX: 7, OriginY: 11, DstW: 32, DstH: 24})
	list.RecordTerrain(Terrain{})
	saved := list.Clone()
	cam.X = 99
	if saved.terrain[0].Cam == &cam || *saved.terrain[0].Cam != original {
		t.Fatal("clone borrows live camera")
	}
	saved.terrain[0].Cam.Z = 88
	if cam.Z != original.Z {
		t.Fatal("clone mutation reached original camera")
	}
	if saved.terrain[1].Cam != nil {
		t.Fatal("nil projection changed")
	}
	if saved.terrain[0].OriginX != 7 || saved.terrain[0].OriginY != 11 || saved.terrain[0].DstW != 32 || saved.terrain[0].DstH != 24 {
		t.Fatal("modern projection scalars changed")
	}
}
