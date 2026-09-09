package session

import (
	"slices"
	"testing"

	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/vfs"
)

const bitmapOnlyFlag = 0x20

// bitmapExplosionFixture binds a Create body that invokes explode on child
// piece 1. It exercises the production pre-Create binding rather than calling
// the sink through a test-only VM hook [04 R-COB-04 §1][R-CB-01 §4].
func bitmapExplosionFixture(t *testing.T, flags uint32, y numeric.Fixed) (*Session, *units.Unit) {
	t.Helper()
	root := t.TempDir()
	writeCompositionModel(t, root, "fixture", 1)
	writeCompositionCOBProgram(t, root, "bitmapunit", []uint32{
		0x10005000, 1, // show child: bitmap-only must leave this draw bit set
		0x10021001, flags,
		0x10071000, 1,
		0x10065000,
	}, []string{"Create"}, []uint32{0}, []string{"modelroot", "modelchild"})
	fs := vfs.New()
	if err := fs.MountDirectory(root, 10); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fs.Close() })
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "bitmapunit"},
		UnitName:         "bitmapunit",
		ObjectName:       "fixture",
		BMCode:           1,
		MaxDamage:        10,
		Limit:            -1,
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	terrain := minimalTerrain()
	terrain.SeaLevel = 10
	s := &Session{
		Catalog:        cat,
		World:          terrain,
		Clock:          &clock.State{GlobalTick: 17},
		rngSim:         rng.NewSimulation(71),
		rngCrt:         rng.NewCRT(19),
		rngInitialized: true,
		publication:    newPublicationState(frame.NewEventBuffer(frame.Limits{})),
		strips:         newStripTable(),
	}
	w := units.NewSliced(8, cat)
	s.Units = w
	w.SetCOBSource(fs, globalCobLoader)
	w.SetCOBBinder(func(u *units.Unit) error { return s.bindUnitCOB(fs, u) })
	h, err := w.Create(def, 0, numeric.FixedFromInt(40), y, numeric.FixedFromInt(80))
	if err != nil {
		t.Fatalf("create bitmap unit: %v", err)
	}
	u := w.Unit(h)
	if u == nil {
		t.Fatal("created bitmap unit missing")
	}
	return s, u
}

// TestCOBBitmapExplosionBindsBeforeCreate locks the bounded adapter's full
// callback path: bitmap-only keeps the source visible, consumes no simulation
// random values, resolves the child with the piece-port helper, and admits the
// selected bits in script-bit order with calculated table 2 [04 R-COB-04 §1,
// §4].
func TestCOBBitmapExplosionBindsBeforeCreate(t *testing.T) {
	flags := uint32(bitmapOnlyFlag | 0x3f00)
	s, u := bitmapExplosionFixture(t, flags, numeric.FixedFromInt(11))
	if got := s.SimRNG().Draws(); got != 0 {
		t.Fatalf("bitmap-only Create consumed %d simulation draws, want 0", got)
	}
	if len(u.RenderPieceFlags) < 2 || u.RenderPieceFlags[1]&1 == 0 {
		t.Fatalf("bitmap-only explode hid source piece: flags=%#v", u.RenderPieceFlags)
	}
	views := s.publication.effects.Snapshot()
	if len(views) != 6 {
		t.Fatalf("bitmap records = %d, want 6", len(views))
	}
	origin, ok := u.COBBinding().ComposePiece(1, u.Move.Heading, u.Move.Pitch, u.Move.Bank)
	if !ok {
		t.Fatal("fixture child did not resolve through the piece-port locator")
	}
	wantGraphic := []string{"explosion", "explode2", "explode3", "explode4", "explode5", "nuke1"}
	for i, view := range views {
		if view.Graphic != wantGraphic[i] || view.Source != u.Handle || view.Piece != 1 ||
			view.X != u.X.Add(origin[0]) || view.Y != u.Y.Add(origin[1]) || view.Z != u.Z.Add(origin[2]) {
			t.Fatalf("bitmap record %d = %+v, want %s at child world point", i, view, wantGraphic[i])
		}
		if !view.HasCalculatedFlash || view.CalculatedTable != 2 || !view.ActiveB || len(view.DurationsB) != len(render.FlashFrameDurations(2)) {
			t.Fatalf("bitmap record %d calculated player = %+v, want active table 2", i, view)
		}
	}
	if got := len(s.strips.strips[9]); got != len(views) {
		t.Fatalf("above-sea bitmap explosions built %d land-dust emitters, want %d", got, len(views))
	}
}

// TestCOBBitmapExplosionHydratesAfterCreateTimingBinding exercises the
// production sequence where Create admits bitmap art before the client binds
// authored GAF timing. The existing record gains its named primary without a
// second admission or a calculated-flash change [04 R-COB-04 §1][03 §1].
func TestCOBBitmapExplosionHydratesAfterCreateTimingBinding(t *testing.T) {
	s, _ := bitmapExplosionFixture(t, bitmapOnlyFlag|0x100, numeric.FixedFromInt(11))
	before := s.publication.effects.Snapshot()
	if len(before) != 1 || before[0].ActiveA || !before[0].ActiveB {
		t.Fatalf("pre-binding bitmap view = %+v, want unresolved named art and live table 2", before)
	}
	id, secondarySeq := before[0].ID, before[0].SeqB
	secondary := append([]int32(nil), before[0].DurationsB...)
	resolved := 0
	s.SetEffectTimingResolver(func(e render.Event) (render.FrameTiming, bool) {
		resolved++
		if e.AssetID != "fx" || e.Graphic != "explosion" {
			return render.FrameTiming{}, false
		}
		return render.FrameTiming{Durations: []int32{2, 3}}, true
	})
	after := s.publication.effects.Snapshot()
	if resolved != 1 || len(after) != 1 || after[0].ID != id {
		t.Fatalf("timing binding changed bitmap admission: calls=%d views=%+v", resolved, after)
	}
	if !after[0].ActiveA || len(after[0].DurationsA) != 2 || !after[0].ActiveB ||
		after[0].SeqB != secondarySeq || !slices.Equal(after[0].DurationsB, secondary) {
		t.Fatalf("timing binding players = %+v, want active named art and unchanged calculated flash", after[0])
	}
}

// TestCOBBitmapExplosionPoolAndSeaBoundary locks the two synchronous allocator
// decisions: equality with the sea plane does not produce class-7 land dust,
// and a full 300-record effect pool refuses every bitmap request before it can
// append that smoke [04 R-COB-04 §4][03 R-FX-01 §3].
func TestCOBBitmapExplosionPoolAndSeaBoundary(t *testing.T) {
	t.Run("physical refusal keeps its six-draw hide before bitmap", func(t *testing.T) {
		s, u := bitmapExplosionFixture(t, 0x100, numeric.FixedFromInt(11))
		if got := s.SimRNG().Draws(); got != 6 {
			t.Fatalf("physical refusal consumed %d simulation draws, want 6", got)
		}
		if len(u.RenderPieceFlags) < 2 || u.RenderPieceFlags[1]&1 != 0 {
			t.Fatalf("physical refusal did not hide source piece: flags=%#v", u.RenderPieceFlags)
		}
		if got := len(s.publication.effects.Snapshot()); got != 1 {
			t.Fatalf("bitmap after physical refusal produced %d records, want 1", got)
		}
	})
	t.Run("sea equality suppresses land dust", func(t *testing.T) {
		s, _ := bitmapExplosionFixture(t, bitmapOnlyFlag|0x100, numeric.FixedFromInt(10))
		if got := len(s.publication.effects.Snapshot()); got != 1 {
			t.Fatalf("sea-level bitmap record count = %d, want 1", got)
		}
		if got := len(s.strips.strips[9]); got != 0 {
			t.Fatalf("sea-level bitmap built %d land-dust emitters, want none", got)
		}
	})
	t.Run("fraction above sea stays in its equal whole unit", func(t *testing.T) {
		s, _ := bitmapExplosionFixture(t, bitmapOnlyFlag|0x100, numeric.FixedFromInt(10).Add(1))
		if got := len(s.strips.strips[9]); got != 0 {
			t.Fatalf("fractionally above sea bitmap built %d land-dust emitters, want none", got)
		}
	})

	t.Run("full pool refuses before smoke", func(t *testing.T) {
		root := t.TempDir()
		writeCompositionModel(t, root, "fixture", 1)
		writeCompositionCOBProgram(t, root, "bitmapunit", []uint32{
			0x10005000, 1, // show child before bitmap-only explode
			0x10021001, bitmapOnlyFlag | 0x100 | 0x400,
			0x10071000, 1,
			0x10065000,
		}, []string{"Create"}, []uint32{0}, []string{"modelroot", "modelchild"})
		fs := vfs.New()
		if err := fs.MountDirectory(root, 10); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { fs.Close() })
		def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "bitmapunit"}, UnitName: "bitmapunit", ObjectName: "fixture", BMCode: 1, MaxDamage: 10, Limit: -1}
		cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
		s := &Session{Catalog: cat, World: minimalTerrain(), Clock: &clock.State{GlobalTick: 17}, rngSim: rng.NewSimulation(71), rngCrt: rng.NewCRT(19), rngInitialized: true, publication: newPublicationState(frame.NewEventBuffer(frame.Limits{})), strips: newStripTable()}
		s.World.SeaLevel = 10
		for i := 0; i < render.EffectCapacity; i++ {
			if !s.publication.effects.Admit(0, frame.Event{Kind: frame.KindExplosion, Tick: 1}) {
				t.Fatalf("prefill admission %d failed", i)
			}
		}
		w := units.NewSliced(8, cat)
		s.Units = w
		w.SetCOBSource(fs, globalCobLoader)
		w.SetCOBBinder(func(u *units.Unit) error { return s.bindUnitCOB(fs, u) })
		h, err := w.Create(def, 0, 0, numeric.FixedFromInt(11), 0)
		if err != nil {
			t.Fatalf("create full-pool bitmap unit: %v", err)
		}
		u := w.Unit(h)
		if got := s.SimRNG().Draws(); got != 0 {
			t.Fatalf("full-pool bitmap-only Create consumed %d simulation draws, want 0", got)
		}
		if len(u.RenderPieceFlags) < 2 || u.RenderPieceFlags[1]&1 == 0 {
			t.Fatalf("full-pool bitmap-only explode hid source piece: flags=%#v", u.RenderPieceFlags)
		}
		if got := len(s.publication.effects.Snapshot()); got != render.EffectCapacity {
			t.Fatalf("full-pool bitmap effect count = %d, want %d", got, render.EffectCapacity)
		}
		if got := len(s.strips.strips[9]); got != 0 {
			t.Fatalf("refused bitmap requests built %d land-dust emitters", got)
		}
	})
}
