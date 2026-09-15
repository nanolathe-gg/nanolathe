package main

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
)

func resourceGeoFixture(t *testing.T) (*battleSession, *client.Client, *fakeMillisSource, pool.Handle) {
	t.Helper()
	b, cl, ms, builder := resourceFixture(t, false)
	u := *b.cat.Units["armsolar"]
	u.CanonicalKey, u.DefinitionHeader.CanonicalKey, u.UnitName = "geo", "geo", "geo"
	u.FootprintX, u.FootprintZ, u.YardMap = 4, 4, "G"
	u.SoundCategory, u.EnergyUse = "", -100
	b.cat.Units["geo"] = &u
	b.cat.BuildMenus["armcons"].Buttons = append(b.cat.BuildMenus["armcons"].Buttons, "geo")
	fd := &content.FeatureDef{FootprintX: 1, FootprintZ: 1, Geothermal: true, Indestructible: true}
	fd.CanonicalKey = "vent"
	b.cat.Features["vent"] = fd
	b.sess.World.FeatureNames = []string{"vent"}
	b.sess.World.FeatureDefs = []*content.FeatureDef{fd}
	b.sess.Features = features.NewService(b.sess.World, nil, nil, nil)
	if b.sess.Features.PlaceAt(20, 20, fd) == nil {
		t.Fatal("place vent")
	}
	b.sess.Step(b.sess.Clock.ScaledAnchor + 1)
	return b, cl, ms, builder
}

func TestResourceGeothermalNearVentQueuesCenteredPlant(t *testing.T) {
	for _, fog := range []bool{false, true} {
		for _, offset := range []int64{0, 24} {
			t.Run(fmt.Sprintf("fog=%v_offset=%d", fog, offset), func(t *testing.T) {
				b, cl, ms, _ := resourceGeoFixture(t)
				if fog {
					cur, _ := b.currentSnapshot()
					written := b.sess.Snapshot.BeginWrite()
					*written = *cur
					written.Visibility = frame.VisibilityView{W: 32, H: 32, Valid: true, CoverageBytes: true, Visible: make([]uint8, 32*32)}
					if err := b.sess.Snapshot.Publish(cur.Tick + 1); err != nil {
						t.Fatal(err)
					}
				}
				x, y := o5ScreenWorld(b.cam, numeric.FixedFromInt(328+offset), 0, numeric.FixedFromInt(328))
				resourceClickAt(b, cl, x, y, true)
				ms.ms += 100
				resourceClickAt(b, cl, x, y, true)
				cmds := resourceBuildCommands(b.sess)
				if len(cmds) != 1 || cmds[0].Product != "geo" || !cmds[0].Queued || !cmds[0].AppendOnly || cmds[0].WX != numeric.FixedFromInt(336) || cmds[0].WZ != numeric.FixedFromInt(336) || b.resourceQueueFeedback == nil {
					t.Fatalf("vent did not queue its aligned plant: %+v", cmds)
				}
			})
		}
	}
}

func TestResourceGeothermalYardClassification(t *testing.T) {
	b, _, _, _ := resourceGeoFixture(t)
	u := *b.cat.Units["geo"]
	for _, tc := range []struct {
		yard string
		want bool
	}{{"G", true}, {"Gooo oooo oooo oooo", true}, {"ooooooooooooooooG", false}, {"g", false}, {"", false}} {
		u.YardMap = tc.yard
		if got := len(resourceGeoYard(&u)) != 0; got != tc.want {
			t.Fatalf("yard %q: geo=%v want %v", tc.yard, got, tc.want)
		}
	}
	// A solar-like name does not let a vent requirement leak into ground builds.
	b.cat.Units["geo"].SoundCategory = "TEST_SOLAR"
	b.cat.BuildMenus["armcons"].Buttons = []string{"geo"}
	_, solar, geo := b.resourceProducts("armcons")
	if solar != nil || geo == nil {
		t.Fatal("vent-dependent solar category misclassified")
	}
}

func TestResourceGeothermalAvailabilityAndRange(t *testing.T) {
	b, cl, ms, _ := resourceGeoFixture(t)
	x, y := o5ScreenWorld(b.cam, numeric.FixedFromInt(400), 0, numeric.FixedFromInt(328))
	site, ok := b.resourceSite(x, y)
	if !ok || site.product.CanonicalKey != "armsolar" {
		t.Fatal("distant ground should still select solar")
	}
	b.cat.BuildMenus["armcons"].Buttons = []string{"armsolar"}
	x, y = o5ScreenWorld(b.cam, numeric.FixedFromInt(328), 0, numeric.FixedFromInt(328))
	if _, ok := b.resourceSite(x, y); ok {
		t.Fatal("vent without build capability should not become solar")
	}
	b.cat.BuildMenus["armcons"].Buttons = []string{"geo"}
	cl.SetEnhanced(false)
	resourceClickAt(b, cl, x, y, true)
	ms.ms += 100
	resourceClickAt(b, cl, x, y, true)
	if len(resourceBuildCommands(b.sess)) != 0 {
		t.Fatal("classic must not use the geothermal shortcut")
	}
}

func TestResourceGeothermalSpacingKeepsRequiredYardOnVent(t *testing.T) {
	b, cl, ms, _ := resourceGeoFixture(t)
	// Reserve the centered plant's eastern edge, leaving room west of the vent.
	if err := b.DispatchMobileBuild("armsolar", numeric.FixedFromInt(368), 0, numeric.FixedFromInt(336), true); err != nil {
		t.Fatal(err)
	}
	x, y := o5ScreenWorld(b.cam, numeric.FixedFromInt(352), 0, numeric.FixedFromInt(328))
	for i := 0; i < 2; i++ {
		resourceClickAt(b, cl, x, y, true)
		ms.ms += 100
		resourceClickAt(b, cl, x, y, true)
	}
	cmds := resourceBuildCommands(b.sess)
	if len(cmds) != 2 || cmds[1].Product != "geo" {
		t.Fatalf("occupied vent should not queue a second plant: %+v", cmds)
	}
	if cmds[1].WX != numeric.FixedFromInt(320) || cmds[1].WZ != numeric.FixedFromInt(336) {
		t.Fatalf("plant not shifted to solar edge: %+v", cmds[1])
	}
	// An asymmetric custom yard must align its actual G cell with the vent.
	b.cat.Units["geo"].YardMap = "Gooooooooooooooo"
	site, ok := resourceVentSite(b.cat.Units["geo"], resourceRect{20, 20, 1, 1})
	if !ok || site.x != 20 || site.z != 20 {
		t.Fatalf("custom yard not aligned: %+v", site)
	}
	r := resourceRect{19, 19, 4, 4}
	if resourceCoversVent(r, site.deposit, resourceGeoYard(site.product)) {
		t.Fatal("ordinary yard cell cannot satisfy vent alignment")
	}
}

func TestResourceGeothermalRetailCapabilities(t *testing.T) {
	cat, _ := retailcat.Shared(t)
	b := &battleSession{cat: cat}
	for _, side := range []struct{ builder, product string }{{"armck", "armgeo"}, {"corck", "corgeo"}} {
		_, _, geo := b.resourceProducts(side.builder)
		if geo == nil || geo.CanonicalKey != side.product {
			t.Fatalf("%s geothermal capability = %v", side.builder, geo)
		}
		yard := resourceGeoYard(geo)
		if !resourceCoversVent(resourceRect{20, 20, geo.FootprintX, geo.FootprintZ}, resourceRect{20, 20, 1, 1}, yard) {
			t.Fatal("retail plant yard did not admit a vent")
		}
	}
}

func TestResourceGeothermalChoosesNearestVentAtFractionalGroundPoint(t *testing.T) {
	b, _, _, _ := resourceGeoFixture(t)
	f := &frame.Frame{Features: []frame.FeatureView{
		{DefName: "vent", CX: 20, CZ: 20, FootX: 1, FootZ: 1},
		{DefName: "vent", CX: 20, CZ: 22, FootX: 1, FootZ: 1},
	}}
	// A sloped terrain pick can lie between whole pixels. The southern vent
	// is closer; flooring each signed distance would incorrectly tie them.
	site, ok := b.nearbyResourceVent(f, b.cat.Units["geo"], numeric.FixedFromInt(328), numeric.FixedFromInt(344)+numeric.FixedFromInt(1)/4)
	if !ok || site.deposit.z != 22 {
		t.Fatalf("did not choose closer southern vent: %+v", site)
	}
}

// The quick-build gesture always starts a move, even where an ordinary
// contextual click on custom content would reclaim the vent.
func TestResourceReclaimableVentStartsReplaceableMove(t *testing.T) {
	b, cl, ms, builder := resourceGeoFixture(t)
	b.cat.Features["vent"].Reclaimable = true
	b.sess.Step(b.sess.Clock.ScaledAnchor + 1)
	x, y := o5ScreenWorld(b.cam, numeric.FixedFromInt(328), 0, numeric.FixedFromInt(328))
	_, _, pos := b.pickTarget(x, y)
	if !pos.HasFeature {
		t.Fatal("fixture must expose a reclaimable vent")
	}
	resourceClickAt(b, cl, x, y, true)
	sequence := b.resourceClick.moveSequence
	b.sess.Step(b.sess.Clock.ScaledAnchor + 1)
	q := orders.QueueForUnit(b.sess.Units.Unit(builder))
	if q.Head() == nil || q.Head().ID != orders.Lookup("Move_Ground") || q.Head().HumanMoveSequence != sequence {
		t.Fatalf("first click did not issue a replaceable move: %+v", q.Head())
	}
	ms.ms += 100
	resourceClickAt(b, cl, x, y, true)
	b.sess.Step(b.sess.Clock.ScaledAnchor + 1)
	if q.LenPrimary() != 1 || !orders.IsMobileBuild(q.Head().ID) || q.Head().BuildDefKey != "geo" {
		t.Fatalf("second click left a contextual order before the plant: %+v", q.Primary())
	}
}
