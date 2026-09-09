package features

import (
	"encoding/binary"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func newEmptyTerrain(w, h int) *world.Terrain {
	attrs := make([]formats.TNTAttribute, w*h)
	for i := range attrs {
		attrs[i] = formats.TNTAttribute{Height: 10, Feature: world.PlotFeatureNone}
	}
	plot := world.ExpandPlot(attrs, w, h)
	return &world.Terrain{
		CellW:       int32(w),
		CellH:       int32(h),
		Plot:        plot,
		FeatureDefs: []*content.FeatureDef{},
		SeaLevel:    10,
		Gravity:     numeric.Fixed(0x1FDB),
	}
}

func TestRestoreAtCopiesNormalAnchorAndAnimationState(t *testing.T) {
	terrain := newEmptyTerrain(4, 4)
	def := featureDef("tree", 0, 0, 10)
	svc := NewService(terrain, nil, nil, nil)
	normal := make([]byte, 8)
	binary.LittleEndian.PutUint16(normal[4:], 3)
	binary.LittleEndian.PutUint16(normal[6:], 0x4321)
	if _, err := svc.RestoreAt(1, 2, def, 0, normal); err != nil {
		t.Fatal(err)
	}
	if got := terrain.PlotAt(1, 2).AnchorWord(); got != 0x4321 {
		t.Fatalf("normal anchor %#x, want %#x", got, 0x4321)
	}

	// An animating record with the burn selector re-runs ignition — one
	// simulation draw — and is then overwritten with the saved accumulator,
	// frame byte and countdown high nibble (low nibble zero)
	// [08 R-SAVE-FEATURE-01][05 R-FEAT-01 §9].
	terrain = newEmptyTerrain(4, 4)
	sim := rng.SimulationFromState(5)
	svc = NewService(terrain, &sim, nil, nil)
	burnable := featureDef("tree", 0, 0, 10)
	burnable.Filename = "trees"
	burnable.SeqNameBurn = "burn"
	burnable.SparkTime = 150
	stubSequences(svc, []int32{3, 0, 2}, nil, nil)
	anim := make([]byte, 10)
	binary.LittleEndian.PutUint16(anim[6:], 0x2211)
	anim[8], anim[9] = 1, 0xA0 // frame 1, selector 0 (burn), countdown high nibble 10
	before := sim.Draws()
	inst, err := svc.RestoreAt(1, 2, burnable, 1, anim)
	if err != nil || inst == nil || !inst.IsBurning || inst.IsAnimating || inst.DamageAccumulator != 0x2211 || inst.AnimationSelector != 0 {
		t.Fatalf("anim restore: inst=%#v err=%v", inst, err)
	}
	if got := sim.Draws() - before; got != 1 {
		t.Fatalf("restore consumed %d simulation draws, want ignition's one [05 R-FEAT-01 §9]", got)
	}
	if inst.BurnCountdown != 0xA0 {
		t.Fatalf("restored countdown %d, want the saved high nibble back in place: 0xA0", inst.BurnCountdown)
	}
	if inst.cursor.frame != 1 || inst.cursor.delay != 3 || !inst.cursor.running() {
		t.Fatalf("restored cursor frame %d delay %d, want the saved frame 1 with frame 0's delay 3 (the delay is not saved)", inst.cursor.frame, inst.cursor.delay)
	}
	if !terrain.PlotAt(1, 2).Occupied() {
		t.Fatal("animating restore did not attach the anchor instance bit")
	}

	terrain = newEmptyTerrain(4, 4)
	svc = NewService(terrain, nil, nil, nil)
	// The 3D record's five values are named [08 R-SAVE-FEATURE-01]: position at
	// 0x08..0x13, bank/heading at 0x14..0x17, pitch at 0x18..0x19, and the
	// damage accumulator at 0x06..0x07 with the other two families.
	threeD := make([]byte, 26)
	binary.LittleEndian.PutUint16(threeD[6:], 0xBEEF)
	wantPos := [3]numeric.Fixed{numeric.Fixed(0x0012_3456), numeric.Fixed(-0x0004_8000), numeric.Fixed(0x0078_9abc)}
	binary.LittleEndian.PutUint32(threeD[0x08:], uint32(int32(wantPos[0])))
	binary.LittleEndian.PutUint32(threeD[0x0c:], uint32(int32(wantPos[1])))
	binary.LittleEndian.PutUint32(threeD[0x10:], uint32(int32(wantPos[2])))
	binary.LittleEndian.PutUint16(threeD[0x14:], 0x1111)
	binary.LittleEndian.PutUint16(threeD[0x16:], 0x2222)
	binary.LittleEndian.PutUint16(threeD[0x18:], 0x3333)
	threeDInst, err := svc.RestoreAt(2, 1, def, 2, threeD)
	if err != nil || threeDInst == nil || threeDInst.DamageAccumulator != 0xBEEF {
		t.Fatalf("3D restore state: inst=%#v err=%v", threeDInst, err)
	}
	if threeDInst.X != wantPos[0] || threeDInst.Y != wantPos[1] || threeDInst.Z != wantPos[2] {
		t.Fatalf("3D restore position (%d,%d,%d), want the saved triple verbatim (%d,%d,%d)",
			threeDInst.X.Raw(), threeDInst.Y.Raw(), threeDInst.Z.Raw(),
			wantPos[0].Raw(), wantPos[1].Raw(), wantPos[2].Raw())
	}
	if (threeDInst.Orientation != Orientation{Bank: 0x1111, Heading: 0x2222, Pitch: 0x3333}) {
		t.Fatalf("3D restore orientation %+v, want the saved triple verbatim", threeDInst.Orientation)
	}
	if !terrain.PlotAt(2, 1).Occupied() {
		t.Fatal("3D restore did not attach the anchor instance bit")
	}
}

// TestRestoreAnimatingSelectorsBindTheirSequences locks the reload of a death
// or reclaim record: the selector re-runs the transition, which binds the
// definition's OWN sequence from the content metadata at frame 0, and the
// reader then writes the saved frame byte over it [08 R-SAVE-FEATURE-01]
// [05 R-FEAT-01 §5 step 5]. The record therefore completes — on the visit its
// remaining frames run out — and stamps the family's successor; it is not a
// record that can never finish. The countdown nibble is restored too, though
// nothing reads it for a non-burning record.
func TestRestoreAnimatingSelectorsBindTheirSequences(t *testing.T) {
	for _, tc := range []struct {
		name      string
		selector  uint8
		countdown uint8
		want      string
	}{
		{name: "death", selector: 1, countdown: 11, want: "dead"},
		{name: "reclaim", selector: 2, countdown: 12, want: "reclaimed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			terrain := newEmptyTerrain(4, 4)
			def := featureDef("source-"+tc.name, 0, 0, 10)
			def.Filename = "trees"
			def.SeqNameDie, def.SeqNameReclamate = "die", "reclamate"
			def.FeatureDeadDef = featureDef("dead", 0, 0, 10)
			def.FeatureDeadDef.Filename = "trees"
			def.FeatureReclamateDef = featureDef("reclaimed", 0, 0, 10)
			def.FeatureReclamateDef.Filename = "trees"
			terrain.FeatureDefs = []*content.FeatureDef{def, def.FeatureDeadDef, def.FeatureReclamateDef}
			svc := NewService(terrain, nil, nil, nil)
			// Delays 2, 5, 1: frame 1 saved. After the reload the cursor is at
			// frame 1 but holding frame 0's delay of 2 (not saved), so the
			// record ends after 2 + 1 = 3 visits, not the 5 + 1 a
			// perfect continuation would take — the documented loss.
			stubSequences(svc, nil, []int32{2, 5, 1}, []int32{2, 5, 1})
			data := make([]byte, 10)
			binary.LittleEndian.PutUint16(data[6:], 0x1234)
			data[8] = 1
			data[9] = tc.countdown<<4 | tc.selector
			inst, err := svc.RestoreAt(1, 1, def, 1, data)
			if err != nil || inst == nil || !inst.IsAnimating || inst.IsBurning || inst.DamageAccumulator != 0x1234 || inst.AnimationSelector != tc.selector {
				t.Fatalf("restore: inst=%#v err=%v", inst, err)
			}
			if inst.BurnCountdown != int32(tc.countdown)<<4 {
				t.Fatalf("restored countdown %#x, want the nibble in the high half %#x", inst.BurnCountdown, int32(tc.countdown)<<4)
			}
			if inst.cursor.frame != 1 || inst.cursor.delay != 2 {
				t.Fatalf("restored cursor frame %d delay %d, want frame 1 with frame 0's delay 2", inst.cursor.frame, inst.cursor.delay)
			}
			if !terrain.PlotAt(1, 1).Occupied() {
				t.Fatal("restored transition did not attach the anchor instance bit")
			}
			for visit := uint32(1); visit < 3; visit++ {
				svc.TickLifecycle(visit)
				if svc.InstanceAt(1, 1) != inst {
					t.Fatalf("visit %d replaced the restored record early", visit)
				}
			}
			svc.TickLifecycle(3)
			got := svc.InstanceAt(1, 1)
			if got == nil || got.Def == nil || string(got.Def.CanonicalKey) != tc.want {
				t.Fatalf("after the restored sequence ended the anchor holds %v, want %q [05 R-FEAT-01 §5 step 6]", got, tc.want)
			}
			if got.IsAnimating || got.onActive {
				t.Fatal("the successor inherited the animation record")
			}
		})
	}
}

// TestRestoreAnimatingSelectorWithoutSequenceReplacesAtOnce is the reload
// side of [05 R-FEAT-01 §5] step 3: a saved death record whose definition
// names no (or no resolvable) `seqnamedie` re-runs a transition that
// replaces immediately, so the cell holds `featuredead` after the load and
// there is no record for the reader to overwrite. A saved burn record whose
// definition cannot ignite simply rests [05 R-FEAT-01 §9 step 1].
func TestRestoreAnimatingSelectorWithoutSequenceReplacesAtOnce(t *testing.T) {
	terrain := newEmptyTerrain(4, 4)
	def := featureDef("bare", 0, 0, 10)
	def.Filename = "trees"
	def.SeqNameDie = "die" // named, but no metadata resolves it
	def.FeatureDeadDef = featureDef("stump", 0, 0, 10)
	terrain.FeatureDefs = []*content.FeatureDef{def, def.FeatureDeadDef}
	sim := rng.SimulationFromState(9)
	svc := NewService(terrain, &sim, nil, nil)
	data := make([]byte, 10)
	data[8], data[9] = 3, 0x51
	if _, err := svc.RestoreAt(1, 1, def, 1, data); err != nil {
		t.Fatal(err)
	}
	got := svc.InstanceAt(1, 1)
	if got == nil || got.Def != def.FeatureDeadDef || got.IsAnimating {
		t.Fatalf("anchor holds %v, want the immediate featuredead successor", got)
	}
	burn := make([]byte, 10)
	burn[8], burn[9] = 3, 0x50 // selector 0, but `seqnameburn` is unnamed
	before := sim.Draws()
	if _, err := svc.RestoreAt(2, 2, def, 1, burn); err != nil {
		t.Fatal(err)
	}
	if got := svc.InstanceAt(2, 2); got == nil || got.Def != def || got.IsBurning || got.onActive {
		t.Fatalf("anchor holds %v, want the feature at rest", got)
	}
	if sim.Draws() != before {
		t.Fatal("a refused ignition drew from the simulation stream")
	}
}

func featureDef(name string, metal, energy, damage int32) *content.FeatureDef {
	return &content.FeatureDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey(name)},
		Metal:            metal,
		Energy:           energy,
		Damage:           damage,
		FootprintX:       1,
		FootprintZ:       1,
	}
}

// tickFeature runs the two explicit feature phase owners in retail order:
// reproduction/motion first, then burning and sinking lifecycle.
func tickFeature(s *Service, tick uint32) {
	s.TickMotion(tick)
	s.TickLifecycle(tick)
}

func TestReproduceCursorDescentWrapSkipNeverScanTop(t *testing.T) {
	w, h := 3, 3
	total := w * h
	terrain := newEmptyTerrain(w, h)
	sim := rng.SimulationFromState(1)
	svc := NewService(terrain, &sim, nil, nil)
	// Place real feature at every cell except top (idx total-1 =8)
	def := featureDef("treeA", 0, 0, 10)
	def.Reproduce = 0
	def.ReproduceArea = 2
	def.CanonicalKey = content.CanonicalKey("treeA")
	// ensure def is known
	for idx := 0; idx < total-1; idx++ {
		cx := idx % w
		cz := idx / w
		svc.spawnFeatureAt(cx, cz, def)
	}
	// Cursor starts at total-1
	if svc.Cursor() != total-1 {
		t.Fatalf("initial cursor %d want %d", svc.Cursor(), total-1)
	}
	visited := []int{}
	for i := 0; i < 20; i++ {
		tickFeature(svc, uint32(i))
		visited = append(visited, svc.LastReproIdx)
	}
	for _, v := range visited {
		if v == total-1 {
			t.Fatalf("top cell %d was scanned, should never be scanned [06 §13.1]", total-1)
		}
	}
	// Expected descent: 7,6,5,4,3,2,1,0,-1,7,6,5,...
	expected := []int{7, 6, 5, 4, 3, 2, 1, 0, -1, 7, 6, 5, 4, 3, 2, 1, 0, -1, 7, 6}
	for i, exp := range expected {
		if visited[i] != exp {
			t.Fatalf("tick %d visited %d want %d", i, visited[i], exp)
		}
	}
}

func TestReproduceDrawCountAtZero(t *testing.T) {
	w, h := 2, 2 // total 4, top 3 never scanned
	terrain := newEmptyTerrain(w, h)
	def := featureDef("treeZero", 0, 0, 10)
	def.Reproduce = 0 // stock-inert but RNG-live [06 §13.1] I4
	def.ReproduceArea = 10
	def.CanonicalKey = content.CanonicalKey("treeZero")
	// Use non-zero animating =0 so eligible
	def.Animating = 0
	sim := rng.SimulationFromState(12345)
	svc := NewService(terrain, &sim, nil, nil)
	// Place at idx 1 (1,0) which will be visited when cursor 2->1
	svc.spawnFeatureAt(1, 0, def)
	svc.SetCursor(2)
	before := svc.sim().Draws()
	tickFeature(svc, 0)
	after := svc.sim().Draws()
	if after-before != 1 {
		t.Fatalf("reproduce=0 eligible visit should consume exactly 1 sim draw [06 §13.1] I4, got %d", after-before)
	}
	// Ensure no new feature spawned
	if len(svc.Instances()) != 1 {
		t.Fatalf("reproduce 0 should not spawn, instances %d", len(svc.Instances()))
	}
	// Also ensure that ineligible cells (no feature) draw 0
	// Next tick visits idx 0 which has no feature (empty) -> no draw
	before = svc.sim().Draws()
	// cursor currently 1, next tick visits 0 (empty)
	tickFeature(svc, 1)
	after = svc.sim().Draws()
	if after != before {
		t.Fatalf("ineligible empty cell should not draw, draws %d -> %d", before, after)
	}
	// Next tick visits -1 skip (wrap to 3) -> no draw
	before = after
	tickFeature(svc, 2) // visits skip cell 3 (top)
	after = svc.sim().Draws()
	if after != before {
		t.Fatalf("wrap-skip cell should not draw, draws %d -> %d", before, after)
	}
}

func TestReproduceRollPassPlacementOffsets(t *testing.T) {
	w, h := 5, 5
	terrain := newEmptyTerrain(w, h)
	def := featureDef("treePass", 0, 0, 10)
	def.Reproduce = 100   // always pass
	def.ReproduceArea = 4 // dx in -2..1, dz in -2..1
	def.CanonicalKey = content.CanonicalKey("treePass")
	sim := rng.SimulationFromState(42)
	svc := NewService(terrain, &sim, nil, nil)
	// Place source at (2,2) idx 12
	svc.spawnFeatureAt(2, 2, def)
	// Ensure cursor will visit 12 next
	svc.SetCursor(13)
	// Copy sim state to predict
	beforeSim := *svc.sim()
	copySim := beforeSim
	roll := copySim.Uint32n(100)
	if roll >= 100 {
		t.Fatal("roll should pass with reproduce 100")
	}
	dx := int(copySim.Uint32n(4)) - 2
	dz := int(copySim.Uint32n(4)) - 2
	expX := 2 + dx
	expZ := 2 + dz
	inBounds := expX >= 0 && expX < w && expZ >= 0 && expZ < h
	// Count draws before
	drawsBefore := beforeSim.Draws()
	tickFeature(svc, 10)
	drawsAfter := svc.sim().Draws()
	if drawsAfter-drawsBefore != 3 {
		t.Fatalf("passing roll should consume 3 draws (roll+dx+dz) [06 §13.1], got %d", drawsAfter-drawsBefore)
	}
	if inBounds {
		// If target was source itself (0,0) then not free, no spawn. Handle.
		if expX == 2 && expZ == 2 {
			if len(svc.Instances()) != 1 {
				t.Fatalf("self-target should not spawn, instances %d", len(svc.Instances()))
			}
			return
		}
		targetIdx := expZ*w + expX
		cell := terrain.Plot[targetIdx]
		if cell.IsEmpty() {
			t.Fatalf("expected spawn at %d,%d offset %d,%d but cell empty", expX, expZ, dx, dz)
		}
		if len(svc.Instances()) != 2 {
			t.Fatalf("expected 2 instances after reproduce, got %d", len(svc.Instances()))
		}
	} else {
		// Out of bounds, no spawn
		if len(svc.Instances()) != 1 {
			t.Fatalf("out of bounds should not spawn, instances %d", len(svc.Instances()))
		}
	}
}

func TestSuccessorHopsAndSentinel(t *testing.T) {
	w, h := 4, 4
	terrain := newEmptyTerrain(w, h)
	// Create three defs with distinct successors
	defA := featureDef("A", 10, 20, 30)
	defA.CanonicalKey = content.CanonicalKey("A")
	defB := featureDef("B", 5, 5, 5)
	defB.CanonicalKey = content.CanonicalKey("B")
	defC := featureDef("C", 1, 1, 1)
	defC.CanonicalKey = content.CanonicalKey("C")
	// Link successors
	defA.FeatureDeadDef = defB
	defA.FeatureReclamateDef = defC
	defA.FeatureBurntDef = nil // no burnt successor -> removal
	defA.FeatureDead = "B"
	defA.FeatureReclamate = "C"
	defA.FootprintX = 1
	defA.FootprintZ = 1
	defB.FootprintX = 1
	defB.FootprintZ = 1
	defC.FootprintX = 1
	defC.FootprintZ = 1
	sim := rng.SimulationFromState(1)
	svc := NewService(terrain, &sim, nil, nil)
	// Place A at 1,1
	svc.spawnFeatureAt(1, 1, defA)
	idx := 1 + 1*w
	if terrain.Plot[idx].IsEmpty() {
		t.Fatal("A not placed")
	}
	// CauseDead should hop to B
	svc.RemoveFeatureAt(1, 1, CauseDead)
	if terrain.Plot[idx].Feature() == world.PlotFeatureNone {
		t.Fatal("dead successor B not stamped")
	}
	if svc.InstanceAt(1, 1) == nil || svc.InstanceAt(1, 1).Def != defB {
		t.Fatal("dead hop should spawn B")
	}
	// CauseReclaim from B? B has no reclaim successor (nil) -> should remove to free sentinel
	// But we placed B, so test reclaim hop from A again: recreate A
	svc.clearFootprint(1, 1, defB)
	svc.spawnFeatureAt(1, 1, defA)
	svc.RemoveFeatureAt(1, 1, CauseReclaim)
	if svc.InstanceAt(1, 1) == nil || svc.InstanceAt(1, 1).Def != defC {
		t.Fatal("reclaim hop should spawn C")
	}
	// CauseBurnt from A (which has nil) -> removal to free sentinel
	svc.clearFootprint(1, 1, defC)
	svc.spawnFeatureAt(1, 1, defA)
	svc.RemoveFeatureAt(1, 1, CauseBurnt)
	if !terrain.Plot[idx].IsEmpty() {
		t.Fatalf("burnt with no successor should return to free sentinel 0xFFFF, got %04x", terrain.Plot[idx].Feature())
	}
	if svc.InstanceAt(1, 1) != nil {
		t.Fatal("no instance after final removal")
	}
	// Sentinel handling: removal should clear fringe as well
	// Create 2x2 footprint feature
	defBig := featureDef("Big", 0, 0, 10)
	defBig.CanonicalKey = content.CanonicalKey("Big")
	defBig.FootprintX = 2
	defBig.FootprintZ = 2
	svc.spawnFeatureAt(0, 0, defBig)
	// Check fringe cells are stamped
	if !terrain.Plot[1].IsFringe() || !terrain.Plot[w].IsFringe() || !terrain.Plot[w+1].IsFringe() {
		t.Fatal("fringe not stamped for 2x2")
	}
	// Resolve via signed offset should work [SPEC_CONFLICTS SC6]
	if feat, ok := world.ResolveFeature(terrain.Plot, w, h, 1, 0); !ok || terrain.FeatureDefs[feat] != defBig {
		t.Fatalf("fringe resolve failed, ok %v feat %d", ok, feat)
	}
	if feat, ok := world.ResolveFeature(terrain.Plot, w, h, 0, 1); !ok || terrain.FeatureDefs[feat] != defBig {
		t.Fatalf("fringe resolve 0,1 failed")
	}
	// Removal should clear all to free
	svc.RemoveFeatureAt(0, 0, CauseDead)
	for dz := 0; dz < 2; dz++ {
		for dx := 0; dx < 2; dx++ {
			px, pz := dx, dz
			pi := pz*w + px
			if !terrain.Plot[pi].IsEmpty() {
				t.Fatalf("clear did not free cell %d,%d feature %04x", px, pz, terrain.Plot[pi].Feature())
			}
		}
	}
}

func TestSinkVelocityAndFloor(t *testing.T) {
	w, h := 2, 2
	terrain := newEmptyTerrain(w, h)
	// Set floor pair for cell 0,0 to 10/20 => avg 15
	terrain.Plot[0].SetMinHeight(10)
	terrain.Plot[0].SetMaxHeight(20)
	// Sea level 30 (above floor) to make submerged start true (floor <= sea)
	terrain.SeaLevel = 30
	terrain.Gravity = numeric.Fixed(0x1FDB)
	def := featureDef("wreck", 0, 0, 10)
	def.CanonicalKey = content.CanonicalKey("wreck")
	sim := rng.SimulationFromState(1)
	svc := NewService(terrain, &sim, nil, nil)
	inst := svc.spawnFeatureAt(0, 0, def)
	// Initiate sinking at submerged position
	inst.Y = numeric.Fixed(30 * 65536) // start above sea? Actually start Y at 30, floor 15, sea 30 => Y == sea
	// For submerged start, Y should be at floor+? But we will manually set IsSinking
	svc.StartSinking(inst, false)
	if inst.Vy != -11468 {
		t.Fatalf("sinking vy must be -11468 fixed [05 \"Feature sinking and water interaction\"], got %d", inst.Vy.Raw())
	}
	// Verify floor derived correctly via CoarseHeightAt
	floor := terrain.CoarseHeightAt(0, 0)
	if floor != numeric.Fixed(15*65536) {
		t.Fatalf("floor derived from min/max average [02 \"Terrain file\"], got %d want %d", floor.Raw(), 15*65536)
	}
	// Simulate descent: Y 30 -> ticks
	inst.Y = numeric.Fixed(25 * 65536) // above floor, below sea? sea is 30*65536, so Y 25 <30 and >15 => constant -11468 latch
	inst.Vy = -11468
	inst.IsSinking = true
	inst.Settled = false
	// One tick should integrate Y += Vy and keep Vy latched
	prevY := inst.Y
	svc.TickLifecycle(1)
	if inst.Y.Raw() != prevY.Raw()-11468 {
		t.Fatalf("per-tick descent should integrate Vy, got %d want %d", inst.Y.Raw(), prevY.Raw()-11468)
	}
	if inst.Vy != -11468 {
		t.Fatalf("while above floor and below water Vy must remain -11468, got %d", inst.Vy.Raw())
	}
	// Continue until settling
	for i := 0; i < 200; i++ {
		svc.TickLifecycle(uint32(2 + i))
		if inst.Settled {
			break
		}
	}
	if !inst.Settled {
		t.Fatal("wreck should have settled")
	}
	if inst.Y.Raw() != floor.Raw() {
		t.Fatalf("settling should hard-snap Y to floor average fraction discarded [05 ...], got %d want %d", inst.Y.Raw(), floor.Raw())
	}
	if inst.Vy != 0 {
		t.Fatalf("settled velocity should be zero, got %d", inst.Vy.Raw())
	}
	// Above-surface gravity test: set Y above sea
	inst2 := svc.spawnFeatureAt(1, 0, def)
	terrain.Plot[1].SetMinHeight(10)
	terrain.Plot[1].SetMaxHeight(10)     // floor 10
	terrain.Plot[1+w*0].SetMinHeight(10) // already? but ensure floor 10
	// Place at 1,0 floor 10, sea 30 => Y above sea
	inst2.Y = numeric.Fixed(40 * 65536)
	// A zero velocity is the dormant move, never a fall [05 R-FEAT-01 §10]
	// pass 3; a record already moving in the air accelerates under gravity.
	inst2.Vy = -1
	inst2.IsSinking = true
	inst2.Settled = false
	// Should accelerate with gravity (Vy becomes more negative)
	prevVy := inst2.Vy
	svc.TickLifecycle(300)
	if inst2.Vy.Raw() >= prevVy.Raw() {
		t.Fatalf("above surface gravity should accelerate downward (Vy more negative), got %d from %d", inst2.Vy.Raw(), prevVy.Raw())
	}
}

// TestBurningDamageCoupling locks the burn event's stream use and the inert
// burning record: the event draws once for a surviving candidate (and once
// more for that candidate's ignition), a burning filename-based feature pays
// no reclaim, and further blast on it is discarded [05 "Feature burning"]
// [05 R-FEAT-01 §8 step 8][05 R-FEAT-01 §11].
func TestBurningDamageCoupling(t *testing.T) {
	w, h := 7, 7
	terrain := newEmptyTerrain(w, h)
	burnDef := featureDef("treeBurn", 0, 0, 10)
	burnDef.Filename = "trees"
	burnDef.Flamable = true
	burnDef.SeqNameBurn = "burnGaf"
	burnDef.SparkTime = 120
	burnDef.SpreadChance = 100
	candDef := featureDef("cand", 0, 0, 10)
	candDef.Filename = "trees"
	candDef.Flamable = true
	candDef.SeqNameBurn = "burnGaf2"
	candDef.SparkTime = 120
	candDef.SpreadChance = 100 // always ignite when drawn [05 ...]
	sim := rng.SimulationFromState(100)
	crt := rng.CRTFromState(200)
	wind := world.NewWind(0, 0)
	wind.DirX = 0
	wind.DirZ = 0
	svc := NewService(terrain, &sim, &crt, wind)
	stubSequences(svc, longBurn(), nil, nil)
	svc.spawnFeatureAt(3, 3, burnDef)
	burnInst := svc.InstanceAt(3, 3)
	if burnInst == nil {
		t.Fatal("burnInst not placed")
	}
	// Make it burning with countdown 1 so the next visit fires the event.
	startBurning(svc, burnInst, longBurn(), 1)
	// One candidate at (4,3), a cell with a feature word and no record.
	svc.spawnFeatureAt(4, 3, candDef)
	svc.clearFootprint(4, 3, candDef)
	terrain.Plot[3*w+4].SetFeature(svc.featureIndexForDef(candDef))
	terrain.Plot[3*w+4].SetFlagByte(0)
	beforeDraws := svc.sim().Draws()
	tickFeature(svc, 0)
	if got := svc.sim().Draws() - beforeDraws; got != 2 {
		t.Fatalf("the burn event drew %d times, want the candidate's spread draw plus its ignition's countdown draw", got)
	}
	if svc.InstanceAt(4, 3) == nil || !svc.InstanceAt(4, 3).IsBurning {
		t.Fatal("candidate at 4,3 should have ignited via spread [05 \"Feature burning\"]")
	}
	// A burning filename-based feature cannot be reclaimed and is immune to
	// further blast.
	burnInst2 := svc.InstanceAt(3, 3)
	if burnInst2 == nil || !burnInst2.IsBurning {
		t.Fatal("burnInst2 missing")
	}
	metal, energy := svc.Reclaim(&units.Unit{}, burnInst2, 0)
	if metal != 0 || energy != 0 {
		t.Fatalf("burning filename-based feature should not be reclaimable [05 \"Feature burning\"], got %v %v", metal, energy)
	}
	if svc.DamageFeature(3, 3, 1000) {
		t.Fatal("burning filename-based should be immune to blast damage [05 \"Feature burning\"]")
	}
}

func TestReclaimYields(t *testing.T) {
	w, h := 3, 3
	terrain := newEmptyTerrain(w, h)
	def := featureDef("reclaimTree", 100, 50, 20)
	def.CanonicalKey = content.CanonicalKey("reclaimTree")
	def.Reclaimable = true
	def.Indestructible = false
	def.FootprintX = 1
	def.FootprintZ = 1
	// Successor after reclaim
	succ := featureDef("stump", 0, 0, 5)
	succ.CanonicalKey = content.CanonicalKey("stump")
	succ.FootprintX = 1
	succ.FootprintZ = 1
	def.FeatureReclamateDef = succ
	def.FeatureReclamate = "stump"
	sim := rng.SimulationFromState(1)
	svc := NewService(terrain, &sim, nil, nil)
	inst := svc.spawnFeatureAt(1, 1, def)
	u := &units.Unit{Handle: 1}
	metal, energy := svc.Reclaim(u, inst, 0)
	if metal != 100 || energy != 50 {
		t.Fatalf("reclaim should return full pools metal 100 energy 50 [05 \"Feature reclaim\"], got %v %v", metal, energy)
	}
	// Check successor placed
	if svc.InstanceAt(1, 1) == nil || svc.InstanceAt(1, 1).Def != succ {
		t.Fatalf("reclaim should replace with reclaimed successor [05 \"Removal and successor replacement\"]")
	}
	// Indestructible should not reclaim
	def2 := featureDef("indestr", 10, 10, 10)
	def2.CanonicalKey = content.CanonicalKey("indestr")
	def2.Reclaimable = true
	def2.Indestructible = true
	inst2 := svc.spawnFeatureAt(0, 0, def2)
	m2, e2 := svc.Reclaim(u, inst2, 0)
	if m2 != 0 || e2 != 0 {
		t.Fatalf("indestructible should block reclaim, got %v %v", m2, e2)
	}
	// Non-reclaimable
	def3 := featureDef("noreclaim", 10, 10, 10)
	def3.CanonicalKey = content.CanonicalKey("noreclaim")
	def3.Reclaimable = false
	inst3 := svc.spawnFeatureAt(2, 2, def3)
	m3, e3 := svc.Reclaim(u, inst3, 0)
	if m3 != 0 || e3 != 0 {
		t.Fatalf("non-reclaimable should not yield, got %v %v", m3, e3)
	}
	// No successor => removal to free sentinel
	def4 := featureDef("leaves", 5, 5, 5)
	def4.CanonicalKey = content.CanonicalKey("leaves")
	def4.Reclaimable = true
	def4.FeatureReclamateDef = nil
	inst4 := svc.spawnFeatureAt(0, 1, def4)
	idx := 1*w + 0
	svc.Reclaim(u, inst4, 0)
	if !terrain.Plot[idx].IsEmpty() {
		t.Fatalf("reclaim with no successor should free cell to 0xFFFF, got %04x", terrain.Plot[idx].Feature())
	}
}

func TestBurningSmokeOnlyGated(t *testing.T) {
	w, h := 2, 2
	terrain := newEmptyTerrain(w, h)
	def := featureDef("burnSmoke", 0, 0, 10)
	def.CanonicalKey = content.CanonicalKey("burnSmoke")
	def.Filename = "trees"
	def.Flamable = true
	def.SeqNameBurn = "burn"
	def.SparkTime = 120
	def.BurnWeapon = ""
	sim := rng.SimulationFromState(1)
	crt := rng.CRTFromState(1)
	svc := NewService(terrain, &sim, &crt, nil)
	svc.spawnFeatureAt(0, 0, def)
	inst := svc.InstanceAt(0, 0)
	startBurning(svc, inst, []int32{100}, 10) // countdown not zero, so no spread event this tick
	// Tick 0 smoke true, Tick 1 smoke false: both should advance the cursor
	// (one frame, delay 100 → 99 → 98) and decrement the countdown.
	tickFeature(svc, 0) // smoke true
	if inst.cursor.delay != 99 || inst.BurnCountdown != 9 {
		t.Fatalf("burn tick 0 failed, delay %d countdown %d", inst.cursor.delay, inst.BurnCountdown)
	}
	tickFeature(svc, 1) // smoke false, but still should advance
	if inst.cursor.delay != 98 || inst.BurnCountdown != 8 {
		t.Fatalf("burn tick 1 (smoke gated only) should still advance animation and countdown every tick [05 \"Feature burning\"], got delay %d countdown %d", inst.cursor.delay, inst.BurnCountdown)
	}
	// Verify smoke only gates smoke emission (CRT draws), not sim draws for countdown/spread
}

func TestWindEmbersZeroWindNoDraws(t *testing.T) {
	w, h := 7, 7
	terrain := newEmptyTerrain(w, h)
	burnDef := featureDef("windBurn", 0, 0, 10)
	burnDef.CanonicalKey = content.CanonicalKey("windBurn")
	burnDef.Filename = "trees"
	burnDef.Flamable = true
	burnDef.SeqNameBurn = "burn"
	burnDef.SparkTime = 120
	burnDef.BurnWeapon = ""
	sim := rng.SimulationFromState(500)
	windZero := world.NewWind(0, 0)
	windZero.DirX = 0
	windZero.DirZ = 0
	svcZero := NewService(terrain, &sim, nil, windZero)
	svcZero.spawnFeatureAt(3, 3, burnDef)
	instZero := svcZero.InstanceAt(3, 3)
	startBurning(svcZero, instZero, longBurn(), 1)
	// Place no candidates to avoid spread draws, just test wind embers zero wind collapses to no draws [05 "Feature burning"]
	before := svcZero.sim().Draws()
	tickFeature(svcZero, 0) // fires burn event, wind embers with zero wind should not draw
	after := svcZero.sim().Draws()
	// Should have drawn only for neighbourhood spread if any candidates survived? But we have no candidates, so only possible draws are from spread (none) and wind (zero => none) and burn weapon (none)
	// So draws should be 0 for this tick? Actually countdown decrement itself doesn't draw except for spread. The fire event with zero wind and no candidates draws 0.
	if after != before {
		t.Fatalf("zero wind should collapse 5 probes onto origin skipped, no draws [05 \"Feature burning\"], got %d draws", after-before)
	}
}
