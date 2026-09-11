package session

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Death hands the dying owner's identity to the stamp, including player zero.
// Ordinary stamps use neutral ownership. The same identities are published
// after the feature and PlayerFeatures restore passes [05 R-FEAT-01 §3, §13]
// [03 §5.1.5][03 §3.9][08 R-SAVE-02 §12].
func TestCorpseOwnershipPublishesAndSurvivesPlayerFeaturesRestore(t *testing.T) {
	for _, owner := range []uint8{0, 1} {
		t.Run(fmt.Sprintf("owner %d", owner), func(t *testing.T) {
			s := newLoopTestSession(t, 0)
			wreck := &content.FeatureDef{
				DefinitionHeader: content.DefinitionHeader{CanonicalKey: "owned-wreck"},
				FootprintX:       1, FootprintZ: 1, Damage: 100, Object: "wreck", NoDrawUnderGray: true,
			}
			s.Catalog.Features[wreck.CanonicalKey] = wreck
			def := s.Catalog.Units["armcom"]
			def.Corpse = wreck.CanonicalKey
			h, err := s.Units.Create(def, owner, world.CellToWorld(4), 0, world.CellToWorld(4))
			if err != nil {
				t.Fatal(err)
			}
			u := s.Units.Unit(h)
			u.Health, u.Remaining = -10, 0
			u.SetScript(cob.NewVM(makeKilledProg(1)))
			s.Units.Destroy(h, units.DeathKilled)
			s.Units.FinalizeDeath(h, 1)
			if s.Features.InstanceAt(4, 4) == nil {
				t.Fatal("death did not produce a corpse")
			}
			if s.Features.PlaceAt(6, 6, wreck) == nil {
				t.Fatal("neutral feature placement refused")
			}

			checkPublished := func(s *Session) {
				t.Helper()
				s.ViewingOwner = owner
				s.Snapshot = frame.NewBuffer()
				s.Vis = visibility.New(s.World, visibility.ModeCurrentEnabled)
				s.publishSnapshot(1)
				cur := s.Snapshot.Current()
				if len(cur.Features) != 2 {
					t.Fatalf("published %d features, want corpse and neutral", len(cur.Features))
				}
				for _, f := range cur.Features {
					wantOwner := uint8(10)
					if f.CX == 4 {
						wantOwner = owner
					}
					if !f.OwnerKnown || f.Owner != wantOwner || !f.NoDrawUnderGray {
						t.Fatalf("published ownership/definition: %+v", f)
					}
					if s.Vis.VisiblePoint(visibility.PlayerID(owner), f.X, f.Y, f.Z) {
						t.Fatal("fixture feature has current LOS")
					}
					if radarFeatureVisible(s, f) != (wantOwner == owner) {
						t.Fatalf("owner %d feature did not follow owner-only visibility outside LOS", f.Owner)
					}
				}
			}
			checkPublished(s)
			image, err := s.Features.RetailFeatureImage()
			if err != nil {
				t.Fatal(err)
			}
			playerFeatures, err := s.World.RetailPlayerFeaturesImage()
			if err != nil {
				t.Fatal(err)
			}
			dst := &Session{World: minimalTerrain(), Catalog: s.Catalog}
			dst.Features = features.NewService(dst.World, nil, nil, nil)
			if err := restoreRetailFeatures(dst.Features, dst.Catalog, projectFeatureImage(image)); err != nil {
				t.Fatal(err)
			}
			// RestoreAt uses neutral ownership; the later PlayerFeatures account
			// restores the saved owner without changing the live-instance flag.
			if dst.World.PlotAt(4, 4).PlacerNibble() != 10 {
				t.Fatal("feature restore did not first install neutral ownership")
			}
			if err := dst.World.RestoreRetailPlayerFeatures(playerFeatures); err != nil {
				t.Fatal(err)
			}
			if !dst.World.PlotAt(4, 4).Occupied() {
				t.Fatal("PlayerFeatures restore erased corpse attachment")
			}
			checkPublished(dst)
		})
	}
}
