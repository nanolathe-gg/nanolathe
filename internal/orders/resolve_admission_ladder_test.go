// This file is the external test package for internal/orders. It exists so the
// carriable test can be exercised against its real owner: internal/movement
// implements §10.2's nine-reject admission and imports internal/orders, so only
// an external test package can hold both.
package orders_test

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

type ladderCOBFS struct{}

func (ladderCOBFS) Open(string) (vfs.File, error) { return nil, vfs.ErrNotFound }
func (ladderCOBFS) ReadFileLimit(name string, _ int64) ([]byte, error) {
	if strings.HasPrefix(strings.ToLower(name), "scripts/") {
		return ladderCOB(), nil
	}
	return nil, vfs.ErrNotFound
}
func (ladderCOBFS) ReadDir(string) ([]vfs.EntryInfo, error) { return nil, vfs.ErrNotFound }
func (ladderCOBFS) Stat(string) (vfs.EntryInfo, error)      { return vfs.EntryInfo{}, vfs.ErrNotFound }
func (ladderCOBFS) CacheStamp(string) (string, error)       { return "orders-ladder-fixture", nil }

// ladderCOB is an authored program whose Create script returns immediately
// [R-COB-04 §8][fmt cob].
func ladderCOB() []byte {
	const headerSize = 44
	buf := make([]byte, 64)
	u32 := func(off, value uint32) { binary.LittleEndian.PutUint32(buf[off:], value) }
	u32(0x00, 4)
	u32(0x04, 1)
	u32(0x0c, 1)
	u32(0x18, headerSize)
	u32(0x1c, headerSize+4)
	u32(0x24, headerSize+8)
	u32(headerSize+4, headerSize+12)
	u32(headerSize+8, 0x10065000) // RETURN [04 §4.3]
	copy(buf[headerSize+12:], "Create\x00")
	return buf
}

func ladderTerrain(sea uint8) *world.Terrain {
	t := &world.Terrain{CellW: 32, CellH: 32, SeaLevel: sea, Plot: make([]world.PlotCell, 32*32)}
	for i := range t.Plot {
		t.Plot[i].SetFeature(world.PlotFeatureNone)
		t.Plot[i].SetHeight(10)
		t.Plot[i].SetMinHeight(10)
		t.Plot[i].SetMaxHeight(10)
	}
	return t
}

// TestCarriableLadderIsTheNineRejectsInOrder walks §10.2's admission ladder one
// reject at a time: the pair starts failing several of the nine at once, and
// each step repairs exactly the reject that is currently firing. The reason the
// admission reports must therefore step down the ladder 1 → 9 and then admit —
// which is the ordering claim, since a reject evaluated out of order would name
// itself early [04 §10.2].
//
// The resolver's own carriable test is the same call through the queue binding
// [04 R-ORD-02 §1], asserted at both ends of the walk.
func TestCarriableLadderIsTheNineRejectsInOrder(t *testing.T) {
	const seaLevel = uint8(5)
	ter := ladderTerrain(seaLevel)
	fallback := movement.Profile{FootPrintX: 2, FootPrintZ: 2, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 255, BadWaterSlope: 127}
	sys := movement.NewSystem(ter, fallback, movement.NewOccupancyGrid())
	// A ship class: a positive MinWaterDepth is exactly what reject 7 refuses
	// to put on a ground carrier [04 §10.2].
	sys.SetClasses(map[string]*content.MovementClass{
		content.CanonicalKey("ship3x3"): {FootprintX: 3, FootprintZ: 3, MaxWaterDepth: 255, MinWaterDepth: 3, MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 255, BadWaterSlope: 127},
	})

	w := units.NewSliced(64, nil)
	w.SetCOBSource(ladderCOBFS{}, cob.NewCachedLoader())

	carrierDef := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("ladder_carrier")},
		MaxDamage:        100, CanMove: true,
		CanLoad: false, TransportCapacity: 0, TransportSize: 0, CanFly: false,
	}
	candidateDef := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("ladder_cargo")},
		MaxDamage:        100,
		// Reject 1 first; the rest are set up to fire in turn below.
		CantBeTransported: true,
		FootprintX:        4,
		CanMove:           false, CanFly: false, MovementClass: "",
		ModelTopFixed: 0,
	}

	cx, cz := world.CellToWorld(5), world.CellToWorld(5)
	dx, dz := world.CellToWorld(7), world.CellToWorld(5)
	ch, err := w.Create(carrierDef, 0, cx, numeric.Fixed(int64(10)<<16), cz)
	if err != nil {
		t.Fatalf("carrier create: %v", err)
	}
	// Reject 8 is "candidate Y + modelTop at or below sea level": the candidate
	// starts submerged so that step of the walk has something to report.
	dh, err := w.Create(candidateDef, 0, dx, numeric.Fixed(int64(2)<<16), dz)
	if err != nil {
		t.Fatalf("candidate create: %v", err)
	}
	carrier, candidate := w.Unit(ch), w.Unit(dh)
	// Reject 9 is the still-under-construction float.
	candidate.Remaining = 1

	// The resolver reaches the ladder through the binding, never through a
	// second copy of it [04 R-ORD-02 §1].
	orders.BindQueueBinding(carrier, &orders.QueueBinding{
		TransportAdmission: func(c, cand *units.Unit) bool {
			if c == nil || cand == nil {
				return false
			}
			return sys.CanTransport(c.Handle, cand.Handle, w).Allowed
		},
	})
	if id := orders.Resolve(6, carrier, candidate, nil); id != 0 {
		t.Fatalf("code 6 on an inadmissible candidate = %q, want reject [04 R-ORD-02 §1]", orders.DescriptorFor(id).Name)
	}

	steps := []struct {
		reject int
		reason string
		repair func()
	}{
		{1, "cantbetransported", func() { candidate.Def.CantBeTransported = false }},
		{2, "canload", func() { carrier.Def.CanLoad = true }},
		{3, "capacity", func() { carrier.Def.TransportCapacity = 1 }},
		{4, "too heavy", func() { carrier.Def.TransportSize = 4 }},
		{5, "no mover", func() {
			candidate.Def.MovementClass = "ship3x3"
			candidate.Def.BMCode = 1
			sys.EnsureUnit(candidate)
			candidate.Move.Mode = 2
			candidate.Move.ModeMirror = 2 // reject 6 next
		}},
		{6, "moving", func() {
			candidate.Move.Mode = 1
			candidate.Move.ModeMirror = 1
		}},
		{7, "ground carrier cannot load ship", func() { carrier.Def.CanFly = true }},
		{8, "submerged", func() { candidate.Y = numeric.Fixed(int64(20) << 16) }},
		{9, "under construction", func() { candidate.Remaining = 0 }},
	}
	for _, step := range steps {
		res := sys.CanTransport(ch, dh, w)
		if res.Allowed {
			t.Fatalf("reject %d: admission allowed before the ladder reached it [04 §10.2]", step.reject)
		}
		if res.Reason != step.reason {
			t.Fatalf("reject %d fired as %q, want %q — the first failing reject decides [04 §10.2]", step.reject, res.Reason, step.reason)
		}
		if id := orders.Resolve(6, carrier, candidate, nil); id != 0 {
			t.Fatalf("reject %d: code 6 resolved %q on an inadmissible candidate [04 R-ORD-02 §1]", step.reject, orders.DescriptorFor(id).Name)
		}
		step.repair()
	}

	if res := sys.CanTransport(ch, dh, w); !res.Allowed {
		t.Fatalf("all nine rejects repaired, admission still refuses: %q [04 §10.2]", res.Reason)
	}
	// The carrier is `canfly` by this point (reject 7's repair), so the air
	// twin is the expected name [04 R-ORD-02 §1].
	if got := orders.DescriptorFor(orders.Resolve(6, carrier, candidate, nil)).Name; got != "VTOL_Pickup" {
		t.Fatalf("code 6 on an admissible candidate = %q, want VTOL_Pickup [04 R-ORD-02 §1]", got)
	}
}

// TestCarriableWithoutTheAdmissionQueryRefuses locks the fail-closed arm: a
// queue with no admission query cannot answer §10.2's ladder, and answering it
// from the `cantbetransported` key alone (which is what the site did before
// WU-19-34) admits a submerged, airborne or unfinished candidate onto a full
// transport [04 §10.2].
func TestCarriableWithoutTheAdmissionQueryRefuses(t *testing.T) {
	w := units.NewSliced(8, nil)
	w.SetCOBSource(ladderCOBFS{}, cob.NewCachedLoader())
	carrier := &units.Unit{Handle: pool.Handle(1), Alive: true, Def: &content.UnitDef{CanLoad: true, MaxDamage: 100}}
	candidate := &units.Unit{Handle: pool.Handle(2), Alive: true, Def: &content.UnitDef{CantBeTransported: false, MaxDamage: 100}}
	orders.BindQueueBinding(carrier, &orders.QueueBinding{})
	if id := orders.Resolve(6, carrier, candidate, nil); id != 0 {
		t.Fatalf("code 6 with no admission query = %q, want reject [04 §10.2]", orders.DescriptorFor(id).Name)
	}
}
