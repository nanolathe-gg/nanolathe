package main

import (
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/visibility"
)

// TestBattleCommandsPublishQueueAndShiftOverlay locks the typed command
// boundary and the immutable queue presentation contract [07 §9][I6]. The
// fixture is authored test data; no retail bytes or alternate content source
// is required.
func TestBattleCommandsPublishQueueAndShiftOverlay(t *testing.T) {
	cat, builderDef, targetDef, productKey := queueFixtureCatalog()
	world := testWorldON05(32, 32)
	unitsWorld := units.NewSliced(32, cat)
	// Keep all authored points inside the logical 640x480 viewport after the
	// camera's presentation origin is removed; negative screen coordinates are
	// clamped by BattleController and would not exercise production picking.
	builderHandle, err := unitsWorld.Create(builderDef, 0, numeric.Fixed(160<<16), 0, numeric.Fixed(64<<16))
	if err != nil {
		t.Fatal(err)
	}
	targetHandle, err := unitsWorld.Create(targetDef, 1, numeric.Fixed(320<<16), 0, numeric.Fixed(160<<16))
	if err != nil {
		t.Fatal(err)
	}

	s := &session.Session{
		State:      session.StateBattle,
		Catalog:    cat,
		World:      world,
		Units:      unitsWorld,
		LocalOwner: 0,
		Clock:      &clock.State{Requested: 10, Active: 10},
		Snapshot:   &frame.Buffer{},
		Vis:        visibility.New(world, 0),
	}
	s.Vis.SetLocal(0)
	b := &battleSession{
		sess: s,
		cat:  cat,
		cam:  &camera.Camera{ViewW: 640, ViewH: 480, MapW: 512, MapH: 512},
	}

	// Establish the first immutable frame, then select through the typed
	// command path. The fixture unit handles are known from the allocator's
	// initial setup, never injected after command admission begins.
	advanceQueueFixture(t, s)
	controller := NewBattleController(b)
	sx, sy := queueScreenPos(b.cam, builderHandle, unitsWorld)
	controller.Step(BattleInputFrame{MouseX: sx, MouseY: sy, Buttons: BattleMouseButtons{Left: true}, Elapsed: 1.0 / 30.0}, nil)
	controller.Step(BattleInputFrame{MouseX: sx, MouseY: sy, Elapsed: 1.0 / 30.0}, nil)
	f, ok := b.currentSnapshot()
	if !ok || f.Selection.Primary != builderHandle || f.CommandPage.Builder != builderHandle {
		t.Fatalf("selection/page not published through authoritative boundary: ok=%v selection=%+v page=%+v", ok, f.Selection, f.CommandPage)
	}

	// Build, move, and attack are admitted in that order through the same
	// typed command boundary as the Ebitengine adapter. The test does not
	// depend on a panel or mutate queue state directly.
	if err := b.DispatchMobileBuild(productKey, numeric.Fixed(240<<16), 0, numeric.Fixed(80<<16), false); err != nil {
		t.Fatalf("typed mobile build admission failed: %v", err)
	}
	advanceQueueFixture(t, s)

	moveX, moveY := o5ScreenWorld(b.cam, numeric.Fixed(280<<16), 0, numeric.Fixed(80<<16))
	controller.Step(BattleInputFrame{PressedKeys: []input.Key{input.KeyM}, HeldKeys: []input.Key{input.KeyM}, Modifiers: BattleModifiers{Shift: true}, Elapsed: 1.0 / 30.0}, nil)
	controller.Step(BattleInputFrame{HeldKeys: []input.Key{input.KeyShift}, Modifiers: BattleModifiers{Shift: true}, Elapsed: 1.0 / 30.0}, nil)
	queueClick(t, controller, moveX, moveY, BattleModifiers{Shift: true})

	attackX, attackY := queueScreenPos(b.cam, targetHandle, unitsWorld)
	controller.Step(BattleInputFrame{PressedKeys: []input.Key{input.KeyA}, HeldKeys: []input.Key{input.KeyA, input.KeyShift}, Modifiers: BattleModifiers{Shift: true}, Elapsed: 1.0 / 30.0}, nil)
	controller.Step(BattleInputFrame{HeldKeys: []input.Key{input.KeyShift}, Modifiers: BattleModifiers{Shift: true}, Elapsed: 1.0 / 30.0}, nil)
	queueClick(t, controller, attackX, attackY, BattleModifiers{Shift: true})

	// Stockpile uses the production N command and the same held-Shift input,
	// exercising the independent secondary chain without fabricating a node.
	controller.Step(BattleInputFrame{PressedKeys: []input.Key{input.KeyN}, HeldKeys: []input.Key{input.KeyN, input.KeyShift}, Modifiers: BattleModifiers{Shift: true}, Elapsed: 1.0 / 30.0}, nil)
	controller.Step(BattleInputFrame{HeldKeys: []input.Key{input.KeyShift}, Modifiers: BattleModifiers{Shift: true}, Elapsed: 1.0 / 30.0}, nil)
	advanceQueueFixture(t, s)

	f, ok = b.currentSnapshot()
	if !ok {
		t.Fatal("no immutable frame after command replay")
	}
	queue, ok := queueForUnit(f, builderHandle)
	if !ok {
		t.Fatalf("builder queue absent from immutable frame: %+v", f.OrderQueues)
	}
	if len(queue.Primary) < 3 {
		t.Fatalf("primary queue=%+v, want build/move/attack sequence", queue.Primary)
	}
	if queue.Primary[0].Kind != "MobileBuild" || queue.Primary[0].BuildProduct != productKey {
		t.Fatalf("primary[0]=%+v, want authored mobile build %q", queue.Primary[0], productKey)
	}
	if queue.Primary[0].FootX != 3 || queue.Primary[0].FootZ != 2 {
		t.Fatalf("build footprint=%dx%d, want authored 3x2", queue.Primary[0].FootX, queue.Primary[0].FootZ)
	}
	if queue.Primary[1].Kind != "Move_Ground" || queue.Primary[1].GoalX != numeric.Fixed(280<<16) || queue.Primary[1].GoalZ != numeric.Fixed(80<<16) {
		t.Fatalf("primary[1]=%+v, want fixed-point move endpoint", queue.Primary[1])
	}
	if !strings.HasPrefix(queue.Primary[2].Kind, "Attack_") || queue.Primary[2].Target != targetHandle {
		t.Fatalf("primary[2]=%+v, want targeted attack", queue.Primary[2])
	}
	if len(queue.Secondary) != 1 || queue.Secondary[0].Kind != "BuildWeapon" || queue.Secondary[0].BuildCount != 1 {
		t.Fatalf("secondary queue=%+v, want one stockpile BuildWeapon node", queue.Secondary)
	}

	// Pure overlay inspection consumes only the immutable frame. BuildRect is
	// supplied from the published footprint, and Project preserves fixed-point
	// world coordinates at the presentation boundary.
	project := func(x, y, z numeric.Fixed) hud.QueuePoint {
		return hud.QueuePoint{X: int32(x >> 16), Y: int32(z >> 16)}
	}
	buildRect := func(o frame.OrderView) (hud.QueueRect, bool) {
		if o.FootX <= 0 || o.FootZ <= 0 {
			return hud.QueueRect{}, false
		}
		left, top := int32(o.GoalX>>16), int32(o.GoalZ>>16)
		return hud.QueueRect{Left: left, Top: top, Right: left + int32(o.FootX)*16, Bottom: top + int32(o.FootZ)*16}, true
	}
	beforeHash := authoritativeFrameHash(t, f)
	held := hud.QueueOverlay(f, hud.QueueOverlayOptions{
		Tick:        f.Tick,
		ShiftHeld:   true,
		LocalOwner:  f.Selection.LocalPlayer,
		HoveredUnit: f.Selection.Primary,
		Project:     project,
		BuildRect:   buildRect,
	})
	if len(held) == 0 {
		t.Fatal("held Shift produced no immutable queue overlay instructions")
	}
	var marker bool
	var moveEndpoint bool
	for _, op := range held {
		if op.Kind == hud.QueuePrimitiveMarker {
			marker = true
			if len(op.Segments) != 8 {
				t.Fatalf("build marker has %d segments, want eight", len(op.Segments))
			}
		}
		if op.Kind == hud.QueuePrimitiveDash && op.Index == queue.Primary[1].Index {
			if op.B != (hud.QueuePoint{X: 280, Y: 80}) {
				t.Fatalf("move overlay endpoint=%+v, want fixed-point goal (280,80)", op.B)
			}
			moveEndpoint = true
		}
	}
	if !marker {
		t.Fatal("held Shift omitted the authored build marker")
	}
	if !moveEndpoint {
		t.Fatal("held Shift omitted the queued move route endpoint")
	}
	if released := hud.QueueOverlay(f, hud.QueueOverlayOptions{Tick: f.Tick, LocalOwner: f.Selection.LocalPlayer, Project: project, BuildRect: buildRect}); released != nil {
		t.Fatalf("released Shift returned overlay instructions: %+v", released)
	}
	if afterHash := authoritativeFrameHash(t, f); beforeHash != afterHash {
		t.Fatalf("presentation-only Shift inspection changed authoritative state hash: before=%x after=%x", beforeHash, afterHash)
	}
}

func queueFixtureCatalog() (*content.Catalog, *content.UnitDef, *content.UnitDef, string) {
	weapon := &content.WeaponDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "stockpile"}, ID: 1, Stockpile: true, ReloadTime: 60}
	builder := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "builder"}, UnitName: "builder", Builder: true, CanMove: true, CanAttack: true, FootprintX: 2, FootprintZ: 2, MaxDamage: 100, Weapon1Def: weapon}
	product := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "product"}, UnitName: "product", FootprintX: 3, FootprintZ: 2, YardMap: "oooooo", MaxDamage: 100}
	target := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "target"}, UnitName: "target", CanMove: true, FootprintX: 2, FootprintZ: 2, MaxDamage: 100}
	cat := &content.Catalog{
		Units:      map[string]*content.UnitDef{builder.CanonicalKey: builder, product.CanonicalKey: product, target.CanonicalKey: target},
		Weapons:    map[string]*content.WeaponDef{weapon.CanonicalKey: weapon},
		BuildMenus: map[string]*content.BuildMenuPage{builder.CanonicalKey: {Buttons: []string{product.CanonicalKey}}},
	}
	return cat, builder, target, product.CanonicalKey
}

func advanceQueueFixture(t *testing.T, s *session.Session) {
	t.Helper()
	if s == nil || s.Clock == nil {
		t.Fatal("queue fixture has no clock")
	}
	s.Step(s.Clock.ScaledAnchor + 1)
}

func queueScreenPos(cam *camera.Camera, handle pool.Handle, unitsWorld *units.World) (int32, int32) {
	u := unitsWorld.Unit(handle)
	if u == nil {
		return 0, 0
	}
	x, y := cam.WorldToScreen(u.X, u.Y, u.Z)
	return x - camera.OriginX, y - camera.OriginY
}

// o5ScreenWorld is shared by the focused placement tests; it is a plain
// camera projection helper, not a second command path.
func o5ScreenWorld(cam *camera.Camera, x, y, z numeric.Fixed) (int32, int32) {
	if cam == nil {
		return 0, 0
	}
	sx, sy := cam.WorldToScreen(x, y, z)
	return sx - camera.OriginX, sy - camera.OriginY
}

func queueClick(t *testing.T, controller *BattleController, x, y int32, modifiers BattleModifiers) {
	t.Helper()
	if controller == nil {
		t.Fatal("nil battle controller")
	}
	controller.Step(BattleInputFrame{MouseX: x, MouseY: y, Buttons: BattleMouseButtons{Left: true}, Modifiers: modifiers, Elapsed: 1.0 / 30.0}, nil)
	controller.Step(BattleInputFrame{MouseX: x, MouseY: y, Modifiers: modifiers, Elapsed: 1.0 / 30.0}, nil)
}

func queueForUnit(f *frame.Frame, unit pool.Handle) (frame.OrderQueueView, bool) {
	if f == nil {
		return frame.OrderQueueView{}, false
	}
	for _, q := range f.OrderQueues {
		if q.Unit == unit {
			return q, true
		}
	}
	return frame.OrderQueueView{}, false
}

func authoritativeFrameHash(t *testing.T, frame *frame.Frame) [32]byte {
	t.Helper()
	state, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("marshal immutable frame: %v", err)
	}
	return sha256.Sum256(state)
}
