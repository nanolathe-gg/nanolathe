package session

import (
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/vfs"
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
		publication:    newPublicationState(frame.NewEventBuffer(frame.Limits{}), 0),
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

// TestCOBWholePieceExplosionPublishesDetachedSlot locks the physical Create
// path: the arena copies the child pose and source slot after hide, while a
// later same-slot unit supplies the current owner palette at publication
// [04 R-COB-04 §1, §2][P0-16].
func TestCOBWholePieceExplosionPublishesDetachedSlot(t *testing.T) {
	s, first := bitmapExplosionFixture(t, 0, numeric.FixedFromInt(11))
	if got := s.SimRNG().Draws(); got != 6 {
		t.Fatalf("physical Create simulation draws = %d, want 6", got)
	}
	if s.debris == nil {
		t.Fatal("whole-piece arena was not created")
	}
	if got := s.debris.SlotCount(); got != 1 {
		t.Fatalf("whole-piece slot count = %d, want 1", got)
	}
	if len(first.RenderPieceFlags) < 2 || first.RenderPieceFlags[1]&1 != 0 {
		t.Fatalf("physical explode left source visible: flags=%#v", first.RenderPieceFlags)
	}
	parts := s.debris.SnapshotInto(nil)
	if len(parts) != 1 || parts[0].Source != first.Handle || parts[0].PieceIndex != 1 {
		t.Fatalf("debris source = %+v, want child from slot %d", parts, first.Handle)
	}
	if parts[0].Angles != [3]uint16{} {
		t.Fatalf("debris angles = %#v, want copied child-only zero pose", parts[0].Angles)
	}
	s.Units.Destroy(first.Handle, units.DeathKilled)
	if result := s.Units.FinalizeDeath(first.Handle, 1); !result.Freed {
		t.Fatal("first source did not free")
	}
	// RawUnitRecord aliases the reused slot. Publication reads that current
	// record instead of keeping an admission-time palette.
	s.Econ = &economy.Service{}
	s.Econ.Players[0].Exists, s.Econ.Players[0].Logo = true, 7
	secondHandle, err := s.Units.Create(first.Def, 0, first.X, first.Y, first.Z)
	if err != nil {
		t.Fatalf("reuse source slot: %v", err)
	}
	if secondHandle != first.Handle {
		t.Fatalf("reused handle = %d, want %d", secondHandle, first.Handle)
	}
	s.Snapshot = frame.NewBuffer(frame.Capacities{Debris: 2})
	s.publishSnapshot(1)
	cur := s.Snapshot.Current()
	if cur == nil || len(cur.Debris) < 1 {
		t.Fatal("whole debris was not published")
	}
	got := cur.Debris[0]
	if got.RawSlot != first.Handle || !got.OwnerColorKnown || got.OwnerColor != 7 {
		t.Fatalf("published debris owner = %+v, want reused source slot and palette 7", got)
	}
}

// TestWholeDebrisGroundImpactAdmitsBeforeDust locks the phase-4 collision
// handoff: an above-sea stopped piece adds class-7 land dust only after its
// calculated-table-0 bitmap owns a fixed effect slot [04 R-COB-04 §2, §4].
func TestWholeDebrisGroundImpactAdmitsBeforeDust(t *testing.T) {
	admitStoppedPiece := func(t *testing.T, s *Session) {
		t.Helper()
		if !s.debris.Admit(render.DebrisRequest{
			Points:       make([][3]numeric.Fixed, 3),
			Position:     [3]numeric.Fixed{0, numeric.FixedFromInt(11), 0},
			Velocity:     [3]numeric.Fixed{0, numeric.FixedFromInt(-2), 0},
			Lifetime:     900,
			ExplodeOnHit: true,
		}) {
			t.Fatal("stopped whole debris admission failed")
		}
	}
	t.Run("admitted bitmap then dust", func(t *testing.T) {
		s, _ := bitmapExplosionFixture(t, 0, numeric.FixedFromInt(11))
		admitStoppedPiece(t, s)
		s.stepDebris(18)
		views := s.publication.effects.Snapshot()
		if len(views) != 1 || !views[0].HasCalculatedFlash || views[0].CalculatedTable != 0 {
			t.Fatalf("ground impact effects = %+v, want one calculated table-0 bitmap", views)
		}
		if got := len(s.strips.strips[9]); got != 1 {
			t.Fatalf("ground impact dust emitters = %d, want 1 after bitmap admission", got)
		}
	})
	t.Run("refused bitmap suppresses dust", func(t *testing.T) {
		s, _ := bitmapExplosionFixture(t, 0, numeric.FixedFromInt(11))
		for i := 0; i < render.EffectCapacity; i++ {
			if !s.publication.effects.Admit(0, frame.Event{Kind: frame.KindExplosion, Tick: 1}) {
				t.Fatalf("prefill admission %d failed", i)
			}
		}
		admitStoppedPiece(t, s)
		s.stepDebris(18)
		if got := len(s.strips.strips[9]); got != 0 {
			t.Fatalf("refused ground bitmap built %d dust emitters", got)
		}
	})
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
		s := &Session{Catalog: cat, World: minimalTerrain(), Clock: &clock.State{GlobalTick: 17}, rngSim: rng.NewSimulation(71), rngCrt: rng.NewCRT(19), rngInitialized: true, publication: newPublicationState(frame.NewEventBuffer(frame.Limits{}), 0), strips: newStripTable()}
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

// TestWholePieceAdmissionUsesCurrentPiecePoseNotRetainedTranslation locks the
// approved unconditional departure C27.1 of docs/DESIGN_UNITS_ORDERS_COB.md
// (authorized 2026-09-16), in exactly the case where retail differs: a piece
// the script has translated since it was last composed. Retail admits the unit
// position plus the piece's LAST RETAINED translation, a value written by model
// drawing and by the viewing-player-visible refresh visit `[04 R-COB-04 §2]`
// `[04 R-COB-04 §3]`, so a second admission after a script translation would
// reuse the first admission's point. This build recomposes the live pose, so the
// second admission moves by exactly the script's translation — render cadence
// and the viewing player stay out of the authoritative tick [I4] [I6] [I11].
//
// Admission consumes no simulation draws either way: the departure is the
// position only, never the six-draw seed.
func TestWholePieceAdmissionUsesCurrentPiecePoseNotRetainedTranslation(t *testing.T) {
	// Bitmap-only Create leaves the arena empty, so both admissions below are
	// this test's own and their order is the only thing under test.
	s, u := bitmapExplosionFixture(t, bitmapOnlyFlag, numeric.FixedFromInt(30))
	binding := u.COBBinding()
	u.Move.Heading, u.Move.Pitch, u.Move.Bank = 0, 0, 0
	binding.Model.Pieces[1].Translate = [3]numeric.Fixed{}
	binding.Model.Pieces[binding.Model.Root].Translate = [3]numeric.Fixed{}
	binding.VM.Pieces[1].Trans = [3]numeric.Fixed{}

	sink := &cobExplosionSink{presentation: &cobPresentationSink{session: s, publication: s.publication, source: u.Handle}}
	request := cob.WholePieceExplosion{PhysicalExplosion: cob.PhysicalExplosion{
		Source: cob.ExplosionSource{Identity: cob.ExplosionPieceIdentity{COBPiece: 1}},
	}}

	before := s.SimRNG().Draws()
	if !sink.AdmitWholePiece(request) {
		t.Fatal("whole-piece admission refused an unposed piece")
	}
	parts := s.debris.SnapshotInto(nil)
	if len(parts) != 1 {
		t.Fatalf("debris slots after first admission = %d, want 1", len(parts))
	}
	spawnPose := parts[0].Position
	if want := ([3]numeric.Fixed{u.X, u.Y, u.Z}); spawnPose != want {
		t.Fatalf("unposed admission = %v, want the unit point %v", spawnPose, want)
	}

	// The script now translates the piece. Nothing draws the unit and no
	// refresh visit runs, so retail's retained translation would still be the
	// zero pose above and its second admission would land on spawnPose.
	const dx, dy, dz = 7, 3, 2
	binding.VM.Pieces[1].Trans = [3]numeric.Fixed{dx << 16, dy << 16, dz << 16}
	if !sink.AdmitWholePiece(request) {
		t.Fatal("whole-piece admission refused a script-translated piece")
	}
	parts = s.debris.SnapshotInto(parts)
	if len(parts) != 2 {
		t.Fatalf("debris slots after second admission = %d, want 2", len(parts))
	}
	// ComposePiece already returns (x, y, −z), and pieceWorldPos adds it.
	want := [3]numeric.Fixed{
		u.X.Add(numeric.FixedFromInt(dx)),
		u.Y.Add(numeric.FixedFromInt(dy)),
		u.Z.Sub(numeric.FixedFromInt(dz)),
	}
	if parts[1].Position != want {
		t.Fatalf("translated admission = %v, want the live pose %v [C27.1]", parts[1].Position, want)
	}
	if parts[1].Position == spawnPose {
		t.Fatal("translated admission reused the earlier point: that is retail's retained translation, which C27.1 departs from")
	}
	if parts[0].Position != spawnPose {
		t.Fatalf("first debris record followed the live pose: admission must copy, not alias [I6]")
	}
	if got := s.SimRNG().Draws() - before; got != 0 {
		t.Fatalf("whole-piece admission consumed %d simulation draws, want 0: C27.1 moves the position only", got)
	}
}
