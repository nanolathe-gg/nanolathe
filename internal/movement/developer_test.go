package movement

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

func TestDeveloperClassTiersAreDetachedReadOnly(t *testing.T) {
	s := NewSystem(flatTerrain(t, 4, 4, 0), Template(), NewOccupancyGrid())
	h := pool.Handle(1)
	setHandleRow(&s.profileNames, h, "test")
	if got := s.DeveloperTiers(h, nil); len(got) != 0 || s.ClassLayerFullStamps() != 0 {
		t.Fatal("query created a missing class")
	}
	layer := s.ensureLayerRegistry().For("test", Template())
	layer.setValue(1, 2, 2)
	before := append([]uint32(nil), layer.cells...)
	watermark, stamps := layer.Watermark(), s.ClassLayerFullStamps()
	got := s.DeveloperTiers(h, nil)
	if len(got) != 16 || got[2*4+1] != 2 {
		t.Fatal("packed tier projection lost row order or tier two")
	}
	got[2*4+1] = 3
	if !reflect.DeepEqual(before, layer.cells) || layer.Watermark() != watermark || s.ClassLayerFullStamps() != stamps {
		t.Fatal("observation mutated class layer")
	}
}
