package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

type consumedWreckFixture struct {
	service     *Service
	builder     *units.Unit
	productDef  *content.UnitDef
	root        *world.PlotCell
	target      *world.PlotCell
	productType uint32
	animation   uint16
}

func newConsumedWreckFixture(t *testing.T, rules Rules, enabled, wreck bool) consumedWreckFixture {
	t.Helper()
	productDef := &content.UnitDef{UnitName: "wreckvictim", MaxDamage: 900, UnitLimit: -1}
	productDef.CanonicalKey = content.CanonicalKey(productDef.UnitName)
	builderDef := &content.UnitDef{UnitName: "wreckbuilder", MaxDamage: 100, UnitLimit: -1, CanResurrect: true}
	builderDef.CanonicalKey = content.CanonicalKey(builderDef.UnitName)
	cat := &content.Catalog{Units: map[string]*content.UnitDef{
		productDef.CanonicalKey: productDef,
		builderDef.CanonicalKey: builderDef,
	}}
	unitWorld := newConstructionFixtureWorld(8, cat)
	builderHandle, err := unitWorld.Create(builderDef, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create resurrection builder: %v", err)
	}
	terrain := &world.Terrain{CellW: 4, CellH: 4, Plot: make([]world.PlotCell, 16)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	terrain.FeatureDefs = []*content.FeatureDef{{FootprintX: 2, FootprintZ: 1, Reclaimable: wreck}}
	root := terrain.PlotAt(2, 2)
	root.SetFeature(0)
	const animation = uint16(0x4321)
	root.SetAnchorWord(animation)
	root.SetOccupied(true)
	target := terrain.PlotAt(3, 2)
	target.SetFeature(world.PlotFeatureFringe)
	target.SetAnchorSigned(-1, 0)
	target.SetOccupied(true)
	service := NewService(terrain, cat, unitWorld, nil)
	service.Rules = rules
	service.Community = community.Features{ResurrectionFinalization: enabled}
	service.Allocator = func(owner uint8, def *content.UnitDef, x, y, z numeric.Fixed) (*units.Unit, error) {
		h, createErr := unitWorld.Create(def, owner, x, y, z)
		if createErr != nil {
			return nil, createErr
		}
		product := unitWorld.Unit(h)
		// Keep distinct pre-finalization values so an abandoned unit is
		// observable without relying on the allocator's ordinary defaults.
		product.Remaining = 0.75
		product.Health = 44
		// This models the trigger fixed by CP-FIX-1: creation consumes the
		// wreck before the transplant's mandatory post-create reread.
		root.SetFeature(world.PlotFeatureNone)
		root.SetAnchorWord(0)
		root.SetOccupied(false)
		target.SetFeature(world.PlotFeatureNone)
		target.SetAnchorWord(0)
		target.SetOccupied(false)
		return product, nil
	}
	productType, ok := cat.UnitDefIndex(productDef.CanonicalKey)
	if !ok || productType == 0 {
		t.Fatal("fixture product has no stable catalog identity")
	}
	return consumedWreckFixture{
		service: service, builder: unitWorld.Unit(builderHandle), productDef: productDef,
		root: root, target: target, productType: productType, animation: animation,
	}
}

func (f consumedWreckFixture) resurrect(t *testing.T, request ResurrectionRequest) ResurrectionResult {
	t.Helper()
	result, err := f.service.Resurrect(
		f.builder, f.target, f.productDef,
		world.CellToWorld(2), 0, world.CellToWorld(2), nil, request,
	)
	if err != nil {
		t.Fatalf("resurrect consumed wreck: %v", err)
	}
	if result.Unit == nil {
		t.Fatal("successful allocation was not returned")
	}
	return result
}

func TestStrictResurrectionAbandonsCreatedUnitWhenWreckRereadFails(t *testing.T) {
	f := newConsumedWreckFixture(t, StrictRules{}, false, true)
	result := f.resurrect(t, ResurrectionRequest{
		PriorTarget: f.builder.Handle,
		OrderedType: f.productType,
		BindTarget:  func(product *units.Unit) pool.Handle { return product.Handle },
	})
	if result.Finalized {
		t.Fatal("Strict finalized a resurrection after the post-create wreck reread failed")
	}
	if result.Unit.Remaining != 0.75 || result.Unit.Health != 44 {
		t.Fatalf("abandoned product changed to remaining=%v health=%d, want allocator state 0.75/44", result.Unit.Remaining, result.Unit.Health)
	}
	if f.root.Feature() != world.PlotFeatureNone || f.root.AnchorWord() != 0 {
		t.Fatalf("Strict restored consumed wreck bytes: definition=%#x animation=%#x", f.root.Feature(), f.root.AnchorWord())
	}
}

func TestCommunityResurrectionRecoversConsumedWreckFinalization(t *testing.T) {
	f := newConsumedWreckFixture(t, CommunityRules{}, true, true)
	prior := f.builder.Handle
	result := f.resurrect(t, ResurrectionRequest{
		PriorTarget: prior,
		OrderedType: f.productType,
		BindTarget:  func(product *units.Unit) pool.Handle { return product.Handle },
	})
	if !result.Finalized {
		t.Fatal("Community did not recover a consumed wreck with every source guard satisfied")
	}
	if result.Unit.Remaining != 0 || result.Unit.Health != 1 {
		t.Fatalf("recovered product remaining=%v health=%d, want 0/1", result.Unit.Remaining, result.Unit.Health)
	}
	if f.root.Feature() != world.PlotFeatureNone {
		t.Fatalf("Community restored wreck definition %#x; consumed wreck must remain absent", f.root.Feature())
	}
	if f.root.AnchorWord() != f.animation {
		t.Fatalf("Community animation word=%#x, want preserved snapshot %#x", f.root.AnchorWord(), f.animation)
	}
}

func TestModernResurrectionInheritsCommunityRecovery(t *testing.T) {
	f := newConsumedWreckFixture(t, &ModernRules{}, true, true)
	result := f.resurrect(t, validConsumedWreckRequest(f))
	if !result.Finalized || result.Unit.Remaining != 0 || result.Unit.Health != 1 {
		t.Fatalf("Modern inherited recovery = %+v, want finalized product at 0/1", result)
	}
	if f.root.Feature() != world.PlotFeatureNone || f.root.AnchorWord() != f.animation {
		t.Fatalf("Modern recovery root definition=%#x animation=%#x, want absent/%#x", f.root.Feature(), f.root.AnchorWord(), f.animation)
	}
}

func TestCommunityResurrectionRecoveryRefusesMissingGuards(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		wreck   bool
		request func(consumedWreckFixture) ResurrectionRequest
	}{
		{
			name: "feature switch disabled", enabled: false, wreck: true,
			request: validConsumedWreckRequest,
		},
		{
			name: "pre-create feature lacks wreck flag", enabled: true, wreck: false,
			request: validConsumedWreckRequest,
		},
		{
			name: "no order", enabled: true, wreck: true,
			request: func(f consumedWreckFixture) ResurrectionRequest {
				return ResurrectionRequest{PriorTarget: f.builder.Handle, OrderedType: f.productType}
			},
		},
		{
			name: "null new target", enabled: true, wreck: true,
			request: func(f consumedWreckFixture) ResurrectionRequest {
				return ResurrectionRequest{PriorTarget: f.builder.Handle, OrderedType: f.productType, BindTarget: func(*units.Unit) pool.Handle { return 0 }}
			},
		},
		{
			name: "new target does not resolve", enabled: true, wreck: true,
			request: func(f consumedWreckFixture) ResurrectionRequest {
				return ResurrectionRequest{PriorTarget: f.builder.Handle, OrderedType: f.productType, BindTarget: func(*units.Unit) pool.Handle { return pool.Handle(0xffff) }}
			},
		},
		{
			name: "unchanged target", enabled: true, wreck: true,
			request: func(f consumedWreckFixture) ResurrectionRequest {
				return ResurrectionRequest{PriorTarget: f.builder.Handle, OrderedType: f.productType, BindTarget: func(*units.Unit) pool.Handle { return f.builder.Handle }}
			},
		},
		{
			name: "zero ordered type", enabled: true, wreck: true,
			request: func(f consumedWreckFixture) ResurrectionRequest {
				request := validConsumedWreckRequest(f)
				request.OrderedType = 0
				return request
			},
		},
		{
			name: "created type mismatch", enabled: true, wreck: true,
			request: func(f consumedWreckFixture) ResurrectionRequest {
				request := validConsumedWreckRequest(f)
				request.OrderedType++
				return request
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newConsumedWreckFixture(t, CommunityRules{}, tc.enabled, tc.wreck)
			result := f.resurrect(t, tc.request(f))
			if result.Finalized {
				t.Fatal("Community recovered without every CP-FIX-1 guard")
			}
			if result.Unit.Remaining != 0.75 || result.Unit.Health != 44 {
				t.Fatalf("refused recovery changed product to remaining=%v health=%d", result.Unit.Remaining, result.Unit.Health)
			}
			if f.root.Feature() != world.PlotFeatureNone || f.root.AnchorWord() != 0 {
				t.Fatalf("refused recovery changed consumed root: definition=%#x animation=%#x", f.root.Feature(), f.root.AnchorWord())
			}
		})
	}
}

func validConsumedWreckRequest(f consumedWreckFixture) ResurrectionRequest {
	return ResurrectionRequest{
		PriorTarget: f.builder.Handle,
		OrderedType: f.productType,
		BindTarget:  func(product *units.Unit) pool.Handle { return product.Handle },
	}
}
