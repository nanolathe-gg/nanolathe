package session

import (
	"encoding/binary"
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"testing"
)

func TestCommunityRestoreRotatesBeforeBinding(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		def := &content.UnitDef{UnitName: "lab", BMCode: 0, FootprintX: 2, FootprintZ: 3, YardMap: "cooooo", Rotations: content.FacingSouth | content.FacingEast, Limit: -1}
		def.CanonicalKey = "lab"
		def.Script = &cob.Program{Code: []uint32{0x10065000}, Scripts: map[string]int{}}
		cat := &content.Catalog{Units: map[string]*content.UnitDef{"lab": def}}
		w := units.NewSliced(4, cat)
		build := &construction.Service{Rules: construction.CommunityRules{}, Community: community.Features{StructureRotation: enabled}}
		seen := false
		w.SetCOBBinder(func(u *units.Unit) error {
			seen = true
			want := int16(2)
			if enabled {
				want = 3
			}
			if u.FootprintSizeX != want {
				t.Fatalf("enabled=%v binder footprint=%d want=%d", enabled, u.FootprintSizeX, want)
			}
			return nil
		})
		data := make([]byte, save.UnitBoxSize)
		copy(data, "lab")
		binary.LittleEndian.PutUint16(data[0x39:], 32768+16384)
		_, err := allocateRetailUnit(w, cat, save.UnitRecord{StableID: 1, Data: data}, build)
		if err != nil {
			t.Fatal(err)
		}
		if !seen {
			t.Fatal("creator did not bind the reserved unit")
		}
	}
}

func TestQueuedRotationSnapshotRetainsFootprint(t *testing.T) {
	def := &content.UnitDef{UnitName: "lab", FootprintX: 2, FootprintZ: 3}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"lab": def}}
	q := orders.SnapshotQueue{Primary: []orders.SnapshotNode{{BuildProduct: "lab", BuildFacing: units.FacingEast}}}
	got := appendOrderQueueView(nil, q, cat)[0].Primary[0]
	if got.FootX != 3 || got.FootZ != 2 || got.BuildFacing != uint8(units.FacingEast) {
		t.Fatalf("queued footprint=%dx%d facing=%d", got.FootX, got.FootZ, got.BuildFacing)
	}
}
