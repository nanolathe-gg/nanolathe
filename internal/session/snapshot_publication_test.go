package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/construction"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/presentation"
	"github.com/nanolathe/nanolathe/internal/snapshot"
	"github.com/nanolathe/nanolathe/internal/units"
)

func TestSnapshotContainsProjectileRenderState(t *testing.T) {
	w := &content.WeaponDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "testbeam"},
		ID:               17,
		Model:            "cannon.3do",
		RenderType:       6,
		Ballistic:        true,
		SmokeTrail:       true,
		WeaponTimer:      33,
	}
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{w.CanonicalKey: w}}
	cat.RebuildWeaponIndex()
	combatSvc := &combat.Service{}
	h, ok := combatSvc.Reserve()
	if !ok {
		t.Fatal("reserve projectile")
	}
	p := &combatSvc.Records[int(h)-1]
	p.WeaponID = w.ID
	p.Shooter = 9
	p.Pos = combat.Vec3{X: 11, Y: 12, Z: 13}
	p.StartPos = combat.Vec3{X: 21, Y: 22, Z: 23}
	p.TargetPos = combat.Vec3{X: 31, Y: 32, Z: 33}
	p.Velocity = combat.Vec3{X: 41, Y: 42, Z: 43}
	p.Yaw, p.Pitch = 44, 45
	p.CreationTick, p.ExpiryTick = 5, 38
	p.BurstRemaining, p.MuzzlePiece = 2, 7
	p.TargetUnit = 3

	s := &Session{Catalog: cat, Combat: combatSvc, Snapshot: &snapshot.Buffer{}}
	s.publishSnapshot(10)
	_, frame, ok := s.Snapshot.Read()
	if !ok || len(frame.Projectiles) != 1 {
		t.Fatalf("projectile frame = %#v, ok=%v", frame, ok)
	}
	got := frame.Projectiles[0]
	if got.Handle != h || got.X != p.Pos.X || got.Y != p.Pos.Y || got.Z != p.Pos.Z || got.Shooter != p.Shooter {
		t.Fatalf("identity/position = %+v", got)
	}
	if got.StartX != p.StartPos.X || got.TailZ != p.StartPos.Z || got.TargetY != p.TargetPos.Y || got.VZ != p.Velocity.Z {
		t.Fatalf("endpoints/velocity = %+v", got)
	}
	if got.Yaw != uint16(p.Yaw) || got.Pitch != uint16(p.Pitch) || got.CreationTick != p.CreationTick || got.ExpiryTick != p.ExpiryTick || got.MuzzlePiece != int32(p.MuzzlePiece) {
		t.Fatalf("orientation/timing = %+v", got)
	}
	if got.Family != int32(combat.CreationBallistic) || got.RenderType != w.RenderType || got.Model != w.Model || got.Graphic != w.Model || !got.SmokeTrail || got.Lifetime != 0 {
		t.Fatalf("authored render metadata = %+v", got)
	}
	if got.Selector != -1 {
		t.Fatalf("selector = %d, want explicit suppression sentinel", got.Selector)
	}
}

func TestSnapshotPublishesEventsInAdmissionOrderExactlyOnce(t *testing.T) {
	c := presentation.NewCollector(presentation.Limits{})
	if !c.EmitImpact(presentation.Event{Tick: 4, Graphic: "first"}) || !c.EmitExplosion(presentation.Event{Tick: 4, Graphic: "second"}) {
		t.Fatal("admit presentation events")
	}
	s := &Session{Snapshot: &snapshot.Buffer{}, Presentation: c}
	s.publishSnapshot(4)
	_, first, ok := s.Snapshot.Read()
	if !ok || len(first.Events) != 2 {
		t.Fatalf("first events = %#v, ok=%v", first, ok)
	}
	if first.Events[0].Graphic != "first" || first.Events[1].Graphic != "second" || first.Events[0].Sequence >= first.Events[1].Sequence {
		t.Fatalf("event order = %+v", first.Events)
	}
	if first.EventAdmissionsDropped != 0 || len(c.Events()) != 0 || c.Dropped() != 0 {
		t.Fatalf("collector not reset after publication: dropped=%d events=%d", c.Dropped(), len(c.Events()))
	}
	s.publishSnapshot(5)
	_, second, ok := s.Snapshot.Read()
	if !ok || len(second.Events) != 0 {
		t.Fatalf("events repeated on next frame = %#v, ok=%v", second, ok)
	}
}

func TestSnapshotPublishesConstructionLink(t *testing.T) {
	builderDef := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "builder"}, UnitName: "builder", Builder: true, MaxDamage: 100, FootprintX: 2, FootprintZ: 2}
	productDef := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "product"}, UnitName: "product", MaxDamage: 200, FootprintX: 1, FootprintZ: 1}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{builderDef.CanonicalKey: builderDef, productDef.CanonicalKey: productDef}}
	unitsWorld := units.New(8, cat)
	builder, err := unitsWorld.Create(builderDef, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	product, err := unitsWorld.Create(productDef, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	productUnit := unitsWorld.Unit(product)
	productUnit.Remaining = 0.5
	productUnit.Health = 80
	build := construction.NewService(nil, cat, unitsWorld, nil)
	build.SetBuilderLink(product, builder)
	s := &Session{Catalog: cat, Units: unitsWorld, Build: build, Snapshot: &snapshot.Buffer{}}
	s.publishSnapshot(9)
	_, frame, ok := s.Snapshot.Read()
	if !ok || len(frame.Builds) != 1 {
		t.Fatalf("build frame = %#v, ok=%v", frame, ok)
	}
	got := frame.Builds[0]
	if got.Builder != builder || got.Product != product || got.ProductKey != productDef.CanonicalKey || got.Remaining != productUnit.Remaining || got.Health != productUnit.Health || got.FootX != 1 || got.FootZ != 1 {
		t.Fatalf("build publication = %+v", got)
	}
}
