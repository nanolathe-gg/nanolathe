package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/snapshot"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestProductionInputShiftQueueReplayO5 is the O5 phase gate. It uses the
// same typed battle/session command boundary as the Ebitengine controller;
// fixture construction happens only before the replay starts. No queue,
// selection, or snapshot slice is written by the test after command admission.
// The synthetic definitions are authored fixture data, not retail assumptions.
func TestProductionInputShiftQueueReplayO5(t *testing.T) {
	cat, builderDef, targetDef, productKey := o5QueueCatalog()
	world := testWorldON05(32, 32)
	unitsWorld := units.New(32, cat)
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
		Snapshot:   &snapshot.Buffer{},
	}
	s.SetTraceEnabled(true)
	b := &battleSession{
		sess:                   s,
		cat:                    cat,
		cam:                    &camera.Camera{ViewW: 640, ViewH: 480, MapW: 512, MapH: 512},
		requireCommandDispatch: true,
		latch:                  input.LatchNormal,
	}
	b.commandDispatchFn = func(cmd battleCommand) error {
		human, ok := b.sessionHumanCommand(cmd)
		if !ok {
			return fmt.Errorf("O5 replay: unsupported command %d", cmd.Kind)
		}
		return s.EnqueueHumanCommand(human)
	}

	// Establish the first immutable frame, then select through the typed
	// production command path. The fixture unit handles are known from the
	// allocator's initial setup, never injected after replay commands begin.
	o5Advance(t, s)
	controller := NewBattleController(b)
	sx, sy := o5ScreenPos(b.cam, builderHandle, unitsWorld)
	controller.Step(BattleInputFrame{MouseX: sx, MouseY: sy, Buttons: BattleMouseButtons{Left: true}, Elapsed: 1.0 / 30.0}, nil)
	controller.Step(BattleInputFrame{MouseX: sx, MouseY: sy, Elapsed: 1.0 / 30.0}, nil)
	frame, ok := b.currentSnapshot()
	if !ok || frame.Selection.Primary != builderHandle || frame.CommandPage.Builder != builderHandle {
		t.Fatalf("selection/page not published through authoritative boundary: ok=%v selection=%+v page=%+v", ok, frame.Selection, frame.CommandPage)
	}

	// Build, move, and attack are admitted in that order through the same
	// logical input seam as the Ebitengine adapter. The test may inspect the
	// presentation-owned fallback rail to choose its authored product button,
	// but it never calls a Dispatch* method directly or mutates queue state.
	controller.Step(BattleInputFrame{PressedKeys: []input.Key{input.KeyB}, HeldKeys: []input.Key{input.KeyB}, Elapsed: 1.0 / 30.0}, nil)
	controller.Step(BattleInputFrame{Elapsed: 1.0 / 30.0}, nil)
	var productButton panelButton
	for _, btn := range b.panelButtons {
		if btn.Kind == "build" && content.CanonicalKey(btn.Name) == content.CanonicalKey(productKey) {
			productButton = btn
			break
		}
	}
	if productButton.Name == "" {
		t.Fatalf("authored product %q was not exposed by the immutable build page: buttons=%+v", productKey, b.panelButtons)
	}
	o5Click(t, controller, productButton.X+panelButtonW/2, productButton.Y+panelButtonH/2, BattleModifiers{})
	if b.buildDef != content.CanonicalKey(productKey) {
		t.Fatalf("product click armed %q, want %q", b.buildDef, productKey)
	}
	buildX, buildY := o5ScreenWorld(b.cam, numeric.Fixed(240<<16), 0, numeric.Fixed(80<<16))
	controller.Step(BattleInputFrame{MouseX: buildX, MouseY: buildY, Elapsed: 1.0 / 30.0}, nil)
	o5Click(t, controller, buildX, buildY, BattleModifiers{})

	moveX, moveY := o5ScreenWorld(b.cam, numeric.Fixed(280<<16), 0, numeric.Fixed(80<<16))
	controller.Step(BattleInputFrame{PressedKeys: []input.Key{input.KeyM}, HeldKeys: []input.Key{input.KeyM}, Modifiers: BattleModifiers{Shift: true}, Elapsed: 1.0 / 30.0}, nil)
	controller.Step(BattleInputFrame{HeldKeys: []input.Key{input.KeyShift}, Modifiers: BattleModifiers{Shift: true}, Elapsed: 1.0 / 30.0}, nil)
	o5Click(t, controller, moveX, moveY, BattleModifiers{Shift: true})

	attackX, attackY := o5ScreenPos(b.cam, targetHandle, unitsWorld)
	controller.Step(BattleInputFrame{PressedKeys: []input.Key{input.KeyA}, HeldKeys: []input.Key{input.KeyA, input.KeyShift}, Modifiers: BattleModifiers{Shift: true}, Elapsed: 1.0 / 30.0}, nil)
	controller.Step(BattleInputFrame{HeldKeys: []input.Key{input.KeyShift}, Modifiers: BattleModifiers{Shift: true}, Elapsed: 1.0 / 30.0}, nil)
	o5Click(t, controller, attackX, attackY, BattleModifiers{Shift: true})

	// Stockpile uses the production N command and the same held-Shift input,
	// exercising the independent secondary chain without fabricating a node.
	controller.Step(BattleInputFrame{PressedKeys: []input.Key{input.KeyN}, HeldKeys: []input.Key{input.KeyN, input.KeyShift}, Modifiers: BattleModifiers{Shift: true}, Elapsed: 1.0 / 30.0}, nil)
	controller.Step(BattleInputFrame{HeldKeys: []input.Key{input.KeyShift}, Modifiers: BattleModifiers{Shift: true}, Elapsed: 1.0 / 30.0}, nil)
	o5Advance(t, s)

	frame, ok = b.currentSnapshot()
	if !ok {
		t.Fatal("no immutable frame after O5 command replay")
	}
	queue, ok := o5Queue(frame, builderHandle)
	if !ok {
		t.Fatalf("builder queue absent from immutable frame: %+v", frame.OrderQueues)
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
	buildRect := func(o snapshot.OrderView) (hud.QueueRect, bool) {
		if o.FootX <= 0 || o.FootZ <= 0 {
			return hud.QueueRect{}, false
		}
		left, top := int32(o.GoalX>>16), int32(o.GoalZ>>16)
		return hud.QueueRect{Left: left, Top: top, Right: left + int32(o.FootX)*16, Bottom: top + int32(o.FootZ)*16}, true
	}
	beforeHash := o5AuthoritativeHash(t, s, frame)
	held := hud.QueueOverlay(frame, hud.QueueOverlayOptions{
		Tick:        frame.Tick,
		ShiftHeld:   true,
		LocalOwner:  frame.Selection.LocalPlayer,
		HoveredUnit: frame.Selection.Primary,
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
	if released := hud.QueueOverlay(frame, hud.QueueOverlayOptions{Tick: frame.Tick, LocalOwner: frame.Selection.LocalPlayer, Project: project, BuildRect: buildRect}); released != nil {
		t.Fatalf("released Shift returned overlay instructions: %+v", released)
	}
	if afterHash := o5AuthoritativeHash(t, s, frame); beforeHash != afterHash {
		t.Fatalf("presentation-only Shift inspection changed authoritative state/trace hash: before=%x after=%x", beforeHash, afterHash)
	}
}

func o5QueueCatalog() (*content.Catalog, *content.UnitDef, *content.UnitDef, string) {
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

func o5Advance(t *testing.T, s *session.Session) {
	t.Helper()
	if s == nil || s.Clock == nil {
		t.Fatal("O5 replay has no clock")
	}
	s.Step(s.Clock.ScaledAnchor + 1)
}

func o5ScreenPos(cam *camera.Camera, handle pool.Handle, unitsWorld *units.World) (int32, int32) {
	u := unitsWorld.Unit(handle)
	if u == nil {
		return 0, 0
	}
	x, y := cam.WorldToScreen(u.X, u.Y, u.Z)
	return x - camera.OriginX, y - camera.OriginY
}

func o5ScreenWorld(cam *camera.Camera, x, y, z numeric.Fixed) (int32, int32) {
	if cam == nil {
		return 0, 0
	}
	sx, sy := cam.WorldToScreen(x, y, z)
	return sx - camera.OriginX, sy - camera.OriginY
}

func o5Click(t *testing.T, controller *BattleController, x, y int32, modifiers BattleModifiers) {
	t.Helper()
	if controller == nil {
		t.Fatal("nil battle controller")
	}
	controller.Step(BattleInputFrame{MouseX: x, MouseY: y, Buttons: BattleMouseButtons{Left: true}, Modifiers: modifiers, Elapsed: 1.0 / 30.0}, nil)
	controller.Step(BattleInputFrame{MouseX: x, MouseY: y, Modifiers: modifiers, Elapsed: 1.0 / 30.0}, nil)
}

func o5Queue(frame *snapshot.Frame, unit pool.Handle) (snapshot.OrderQueueView, bool) {
	if frame == nil {
		return snapshot.OrderQueueView{}, false
	}
	for _, q := range frame.OrderQueues {
		if q.Unit == unit {
			return q, true
		}
	}
	return snapshot.OrderQueueView{}, false
}

func o5AuthoritativeHash(t *testing.T, s *session.Session, frame *snapshot.Frame) [32]byte {
	t.Helper()
	state, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("marshal immutable frame: %v", err)
	}
	trace, err := json.Marshal(s.TraceEvents())
	if err != nil {
		t.Fatalf("marshal trace: %v", err)
	}
	return sha256.Sum256(append(state, trace...))
}
