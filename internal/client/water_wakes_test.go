package client

import (
	"math"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Authored fixture: the occupancy callback enables two opposite vector cues;
// StartMoving repeats them every 100 ms. Create writes a sentinel which the
// isolated presentation routine must never run (GPU design §26.3).
func wakeFixtureProgram() *cob.Program {
	const push = 0x10021001
	p := &cob.Program{Pieces: []string{"wake"}, Statics: 2, Scripts: make(map[string]int)}
	add := func(name string, words ...uint32) {
		p.Scripts[name] = len(p.Code)
		p.ScriptsByID = append(p.ScriptsByID, len(p.Code))
		p.Code = append(p.Code, words...)
	}
	add("Create", push, 99, 0x10023004, 1, push, 0, 0x10065000)
	add("setSFXoccupy", 0x10022000, 0x10021002, 0, 0x10023004, 0, push, 0, 0x10065000)
	loop := uint32(len(p.Code))
	add("StartMoving", 0x10021004, 0, push, 2, 0x10055000, 0x10066000, loop+15,
		push, 3, 0x1000f000, 0, push, 5, 0x1000f000, 0,
		push, 100, 0x10013000, 0x10064000, loop)
	return p
}

func wakeScene(t *testing.T) (*Client, *frame.Frame) {
	t.Helper()
	c, err := New(Options{Width: 320, Height: 240})
	if err != nil {
		t.Fatal(err)
	}
	c.SetTerrain(&world.Terrain{CellW: 32, CellH: 32, Plot: make([]world.PlotCell, 32*32)})
	c.enhanced = true
	c.cam = &camera.Camera{Scale: 1}
	c.models = map[string]*unitModel{"wake-fixture": {
		compiled: &model.Model{Root: 0, Pieces: []model.Piece{{Name: "wake", Parent: -1,
			Vertices: [][3]numeric.Fixed{{-4 * numeric.FixedOne, 0, -3 * numeric.FixedOne}, {4 * numeric.FixedOne, 0, 3 * numeric.FixedOne}}}}},
		pieceByName: map[string]int{"wake": 0},
	}}
	c.hoverScripts = map[uint16]*cob.Program{1: wakeFixtureProgram()}
	f := &frame.Frame{Tick: 1, Units: []frame.UnitView{{
		InstanceID: 1, Slot: 1, DefID: 1, Model: "wake-fixture", CanHover: true, MoverMode: moverModeGrounded,
		FootX: 2, FootZ: 3, Waterline: 4, HullYExtent: 12 * numeric.FixedOne,
		X: 32 * numeric.FixedOne, Z: 32 * numeric.FixedOne,
	}}}
	return c, f
}

func stepWake(c *Client, f *frame.Frame, x, z int32) {
	f.Tick++
	f.Units[0].X, f.Units[0].Z = numeric.Fixed(x)*numeric.FixedOne, numeric.Fixed(z)*numeric.FixedOne
	c.placeSurfaceWakes(f)
}

// Observe the initial position, then move through the fixture's first sleep.
func startWake(c *Client, f *frame.Frame) {
	c.placeSurfaceWakes(f)
	for range 3 {
		stepWake(c, f, int32(f.Units[0].X/numeric.FixedOne)+1, int32(f.Units[0].Z/numeric.FixedOne))
	}
}

type wakeCollector struct {
	trailCollector
	marks []drawlist.SurfaceWake
}

func (s *wakeCollector) SurfaceWakes(v drawlist.SurfaceWakes) { s.marks = append(s.marks, v.Marks...) }
func recordWakes(c *Client) []drawlist.SurfaceWake {
	c.list.Reset()
	c.drawSurfaceWakes()
	s := &wakeCollector{}
	c.list.Replay(s)
	return s.marks
}

func TestSurfaceWakeScriptCadenceAndMovingDirections(t *testing.T) {
	c, f := wakeScene(t)
	startWake(c, f)
	initial := len(c.wakes.marks)
	if initial == 0 {
		t.Fatal("moving hover emitted no authored cues")
	}
	script := c.wakes.units[1]
	if script == nil || script.vm.SimulationRNG() != nil || script.vm.DebugSnapshot().Statics[1] != 0 {
		t.Fatal("visual script ran Create or bound simulation RNG")
	}
	positive, negative := false, false
	for _, m := range c.wakes.marks {
		positive = positive || m.vx > 0
		negative = negative || m.vx < 0
		if math.Abs(math.Hypot(m.vx, m.vz)-0.5) > 1e-9 || math.Abs(m.vx+4*m.vz/3) > 1e-9 {
			t.Fatalf("unexpected vector speed: %+v", m)
		}
	}
	if !positive || !negative {
		t.Fatal("type 3 and type 5 did not produce opposite directions")
	}
	c.placeSurfaceWakes(f)
	if len(c.wakes.marks) != initial {
		t.Fatal("repeated committed tick duplicated emission")
	}
	for range 2 {
		stepWake(c, f, int32(f.Units[0].X/numeric.FixedOne)+1, 32)
	}
	if len(c.wakes.marks) != initial {
		t.Fatal("wake loop ignored authored sleep")
	}
	stepWake(c, f, int32(f.Units[0].X/numeric.FixedOne)+1, 32)
	if len(c.wakes.marks) != 2*initial {
		t.Fatal("moving hover failed to repeat authored cues")
	}
}

// The committed pose, including model-Z inversion, owns emitter placement.
func TestSurfaceWakeUsesCommittedPiecePose(t *testing.T) {
	base, bf := wakeScene(t)
	moved, mf := wakeScene(t)
	mf.Units[0].Pieces = []frame.PieceView{{Name: "wake", Tx: 8 * numeric.FixedOne, Tz: 6 * numeric.FixedOne}}
	startWake(base, bf)
	startWake(moved, mf)
	if len(base.wakes.marks) == 0 || len(moved.wakes.marks) != len(base.wakes.marks) {
		t.Fatal("pose fixture emitted no matching cues")
	}
	for i, a := range base.wakes.marks {
		b := moved.wakes.marks[i]
		if b.x-a.x != 8*numeric.FixedOne || b.z-a.z != -6*numeric.FixedOne || a.vx != b.vx || a.vz != b.vz {
			t.Fatal("visual VM replaced the committed emitter pose")
		}
	}
}

func TestSurfaceDustAgeUsesPresentationFraction(t *testing.T) {
	c, f := wakeScene(t)
	startWake(c, f)
	base := recordWakes(c)
	if len(base) == 0 {
		t.Fatal("fixture emitted no marks")
	}
	c.interpolation = true
	c.SetTickFraction(0)
	zero := recordWakes(c)
	if len(zero) != len(base) || zero[0] != base[0] {
		t.Fatal("zero fraction changed recording")
	}
	c.SetTickFraction(0.5)
	half := recordWakes(c)
	if len(half) != len(base) || half[0].Age <= base[0].Age || half[0].Alpha >= base[0].Alpha || half[0].CrossY != base[0].CrossY {
		t.Fatal("fraction must advance fading without expanding the tiny specks")
	}
	c.SetTickFraction(0)
	f.Units = nil
	for range 20 {
		f.Tick++
		c.placeSurfaceWakes(f)
	}
	aged := recordWakes(c)
	if len(aged) != len(base) || aged[0].Alpha <= 0 || aged[0].Alpha >= base[0].Alpha || aged[0].X == base[0].X {
		t.Fatal("retained marks did not drift and fade")
	}
	for range 60 {
		f.Tick++
		c.placeSurfaceWakes(f)
	}
	if len(recordWakes(c)) != 0 {
		t.Fatal("expired marks still recorded")
	}
}

func TestSurfaceWakeAdmissionAndResume(t *testing.T) {
	for _, reason := range []string{"hidden", "carried", "airborne", "unfinished", "ordinary", "no identity", "building", "water", "lava water", "acid water", "off map", "Original", "no script", "no model"} {
		t.Run(reason, func(t *testing.T) {
			c, f := wakeScene(t)
			saved := f.Units[0]
			switch reason {
			case "hidden":
				f.Units[0].Owner = 1
			case "carried":
				f.Units[0].Carrier = 2
			case "airborne":
				f.Units[0].MoverMode = 2
			case "unfinished":
				f.Units[0].BuildRemaining = 0.5
			case "ordinary":
				f.Units[0].CanHover = false
			case "no identity":
				f.Units[0].InstanceID = 0
			case "building":
				f.Units[0].IsBuilding = true
			case "water":
				c.terrain.SeaLevel = 10
			case "lava water":
				c.terrain.SeaLevel = 10
				c.terrain.LavaWorld = true
			case "acid water":
				c.terrain.SeaLevel = 10
				c.terrain.WaterDoesDamage = 1
				c.terrain.WaterDamage = 1
			case "off map":
				f.Units[0].Z = -numeric.FixedOne
			case "Original":
				c.enhanced = false
			case "no script":
				c.hoverScripts = nil
			case "no model":
				f.Units[0].Model = "missing"
			}
			c.placeSurfaceWakes(f)
			if len(c.wakes.marks) != 0 || len(c.wakes.units) != 0 {
				t.Fatal("ineligible source created visual spray")
			}
			f.Units[0] = saved
			c.terrain.SeaLevel = 0
			c.enhanced = true
			c.hoverScripts = map[uint16]*cob.Program{1: wakeFixtureProgram()}
			f.Tick++
			startWake(c, f)
			if len(c.wakes.marks) == 0 {
				t.Fatal("eligible moving reappearance did not start spray")
			}
		})
	}
}

func TestSurfaceWakeHistoryResetsAndRemovesHiddenVM(t *testing.T) {
	c, f := wakeScene(t)
	startWake(c, f)
	first := len(c.wakes.marks)
	f.Units[0].Owner = 1
	f.Tick++
	c.placeSurfaceWakes(f)
	if len(c.wakes.units) != 0 || len(c.wakes.marks) != first {
		t.Fatal("hidden hover retained active VM or emitted spray")
	}
	f.Units[0].Owner = 0
	f.Tick++
	startWake(c, f)
	if len(c.wakes.marks) != 2*first {
		t.Fatal("reappearance failed to begin a fresh script")
	}
	for _, tick := range []uint32{0, 20} {
		f.Tick = tick
		c.placeSurfaceWakes(f)
		if len(c.wakes.marks) != 0 {
			t.Fatal("tick discontinuity retained or emitted marks")
		}
	}
	f.ViewingPlayer = 1
	f.Units[0].Owner = 1
	c.placeSurfaceWakes(f)
	if len(c.wakes.marks) != 0 {
		t.Fatal("viewer change retained or emitted marks")
	}
}

func TestSurfaceDustGroundAdmissionIgnoresHoverBob(t *testing.T) {
	c, f := wakeScene(t)
	for i := range c.terrain.Plot {
		c.terrain.Plot[i].SetHeight(20)
	}
	c.terrain.PlotAt(2, 2).SetHeight(4)
	f.Units[0].Y = 18 * numeric.FixedOne
	startWake(c, f)
	if len(c.wakes.marks) == 0 {
		t.Fatal("grounded hover lost spray because model height differs from terrain")
	}
	c.terrain.SeaLevel = 30
	if len(recordWakes(c)) != 0 {
		t.Fatal("retained spray drew over submerged terrain")
	}
}

func TestSurfaceWakeStorageBoundAndSourceReset(t *testing.T) {
	c, f := wakeScene(t)
	for i := range wakeRingSize * 2 {
		c.wakes.push(surfaceWakeMark{born: uint32(i)})
	}
	if len(c.wakes.marks) != wakeRingSize || cap(c.wakes.marks) > wakeRingSize*2 {
		t.Fatal("wake ring grew beyond bounded storage")
	}
	// Shared inert state fills the cap without allocating thousands of VMs.
	state := &hoverWakeScript{tick: f.Tick}
	c.wakes.units = make(map[uint64]*hoverWakeScript)
	for i := range wakeTrackerLimit {
		c.wakes.units[uint64(i+2)] = state
	}
	c.placeSurfaceWakes(f)
	if len(c.wakes.units) != wakeTrackerLimit || c.wakes.units[1] != nil {
		t.Fatal("tracker cap admitted another script")
	}
	f.Tick++
	f.Units = nil
	c.placeSurfaceWakes(f)
	if len(c.wakes.units) != 0 {
		t.Fatal("absent unit trackers retained")
	}
	c.SetSnapshot(frame.NewBuffer())
	if len(c.wakes.marks) != 0 || c.wakes.valid {
		t.Fatal("source replacement retained wake history")
	}
}

// Established bounded asset contract: these stock wake routines use the medium
// static initialized by setSFXoccupy, with no Create, engine port, or RNG needed.
// The decision to run them on land is Modern renderer policy (GPU design §26.3).
func TestRetailHoverWakeScriptIsolation(t *testing.T) {
	fs := vfs.New()
	if err := fs.MountGameDirectory(testsupport.RetailRoot(t)); err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	for _, name := range []string{"armsh", "corch", "armamph"} {
		t.Run(name, func(t *testing.T) {
			prog, found, err := cob.LoadFromFS(fs, name)
			if err != nil || !found {
				t.Fatalf("load script: found=%v err=%v", found, err)
			}
			compiled, err := model.Load(fs, "objects3d/"+name+".3do")
			if err != nil {
				t.Fatal(err)
			}
			m := &unitModel{compiled: compiled, pieceByName: make(map[string]int)}
			for i, p := range compiled.Pieces {
				m.pieceByName[strings.ToLower(p.Name)] = i
			}
			script := newHoverWakeScript(prog, m, 1)
			if script == nil || len(script.emissions) == 0 {
				t.Fatal("stock wake loop needs uninitialized gameplay state")
			}
			initial := append([]hoverWakeEmission(nil), script.emissions...)
			hasForward, hasReverse := false, false
			for _, e := range initial {
				if !strings.HasPrefix(strings.ToLower(prog.Pieces[e.piece]), "wake") {
					t.Fatal("non-wake routine executed")
				}
				if len(compiled.Pieces[script.pieceMap[e.piece]].Vertices) != 2 {
					t.Fatal("stock wake is not an authored vector pair")
				}
				if name == "armamph" && e.kind != 2 || name != "armamph" && e.kind != 3 && e.kind != 5 {
					t.Fatalf("unexpected stock wake type %d", e.kind)
				}
				hasForward = hasForward || e.kind == 3
				hasReverse = hasReverse || e.kind == 5
			}
			if name != "armamph" && (!hasForward || !hasReverse) {
				t.Fatal("stock hover lost paired directions")
			}
			script.emissions = nil
			interval := 9
			if name == "armamph" {
				interval = 7
			}
			for range interval - 1 {
				script.vm.Drain(1)
			}
			if len(script.emissions) != 0 {
				t.Fatal("stock loop emitted before authored sleep elapsed")
			}
			script.vm.Drain(1)
			if len(script.emissions) != len(initial) {
				t.Fatal("stock wake loop did not repeat after authored sleep")
			}
			for range 30 {
				script.emissions = nil
				script.vm.Drain(1)
			}
			if script.vm.SimulationRNG() != nil || len(script.vm.DebugSnapshot().Diagnostics) != 0 {
				t.Fatal("stock wake routine required RNG or raised VM diagnostics")
			}
		})
	}
}

// Battles resolve models by retained definition ID; names are only a preview fallback.
func TestSurfaceWakeUsesBattleModelRegistry(t *testing.T) {
	c, f := wakeScene(t)
	key := modelTextureLoadKey{kind: modelLoadUnit, id: "1"}
	c.modelTextures = &ModelTextureRegistry{
		loads:    map[modelTextureLoadKey]*unitModel{key: c.models[f.Units[0].Model]},
		unitByID: map[uint16]modelTextureLoadKey{1: key},
	}
	startWake(c, f)
	if len(c.wakes.marks) == 0 {
		t.Fatal("battle definition model produced no hover spray")
	}
}

func TestSurfaceWakeIdleStopAndResume(t *testing.T) {
	c, f := wakeScene(t)
	c.placeSurfaceWakes(f)
	for range 12 {
		f.Tick++
		f.Units[0].Y += numeric.FixedOne
		f.Units[0].Heading += 256
		c.placeSurfaceWakes(f)
	}
	if len(c.wakes.marks) != 0 {
		t.Fatal("idle hover bob or rotation emitted land spray")
	}
	startWake(c, f)
	count := len(c.wakes.marks)
	if count == 0 {
		t.Fatal("movement did not enable spray")
	}
	f.Tick++
	c.placeSurfaceWakes(f)
	if len(c.wakes.marks) != count || len(recordWakes(c)) == 0 {
		t.Fatal("stopping emitted spray or erased its fading tail")
	}
	for range 70 {
		f.Tick++
		c.placeSurfaceWakes(f)
	}
	if len(c.wakes.marks) != count || len(recordWakes(c)) != 0 {
		t.Fatal("stationary hover kept emitting after its tail faded")
	}
	startWake(c, f)
	if len(c.wakes.marks) <= count {
		t.Fatal("movement did not resume spray")
	}
}
