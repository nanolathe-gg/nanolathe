package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Construction seeds only the viewing owner's sonar exemption. A skipped
// one-player sensor phase preserves it [03 R-VIS-01 §4].
func TestSensorStatusConstructionAndSkippedPass(t *testing.T) {
	s := visibilityFixture(t, true)
	s.RegisterAll()
	s.Vis.SetViewingPlayer(visibility.PlayerID(s.ViewingOwner))
	def := s.Catalog.Units["armcom"]
	for _, owner := range []uint8{s.ViewingOwner, 0} {
		h, err := s.Units.Create(def, owner, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		want := uint32(0)
		if owner == s.ViewingOwner {
			want = visibility.SonarBit
		}
		if got := s.Units.Unit(h).Flags & (visibility.FriendlyMask | visibility.JammedBit); got != want {
			t.Fatalf("owner %d constructed sensor status = %#x, want %#x", owner, got, want)
		}
	}
	s.Econ.Players[0].Exists = false
	s.stepSensorPhase(1)
	for _, u := range s.Units.Iter() {
		want := uint32(0)
		if u.Owner == s.ViewingOwner {
			want = visibility.SonarBit
		}
		if got := u.Flags & (visibility.FriendlyMask | visibility.JammedBit); got != want {
			t.Fatalf("skipped pass changed owner %d sensor status to %#x, want %#x", u.Owner, got, want)
		}
	}
}

// Sensor bits survive the real save encoder and restore publication before
// another sensor phase. The proximity bit is deliberately not persisted
// [03 R-VIS-01 §4][08 R-SAVE-02 §6].
func TestSensorStatusSaveRestoreBeforeNextPass(t *testing.T) {
	src, srcIDs := newRestoreCoreFixture(t, 1)
	base := visibilityFixture(t, true)
	src.World, src.Vis = base.World, base.Vis
	src.Econ.Players[0].Exists, src.Econ.Players[1].Exists = true, true
	src.Vis.SetViewingPlayer(0)
	u := src.Units.Unit(srcIDs[0].handle)
	u.X, u.Z, u.Y = 128<<16, 128<<16, -1<<16
	src.stepSensorPhase(4)
	u.Flags |= visibility.DecloakBit
	resolve := func(h pool.Handle) (uint16, bool) { return uint16(h), h == u.Handle }
	data, err := units.RetailUnitImage(u, 0, resolve, resolve, units.RetailUnitWriterScratch{})
	if err != nil {
		t.Fatal(err)
	}
	decoded := &units.Unit{}
	if err := units.RetailUnitBase(decoded, data); err != nil {
		t.Fatal(err)
	}
	if got := decoded.Flags & (visibility.FriendlyMask | visibility.DecloakBit); got != visibility.FriendlyMask {
		t.Errorf("encoded live sensor status = %#x, want friendly pair with no proximity bit", got)
	}
	dst, dstIDs := newRestoreCoreFixture(t, 1)
	dst.World = base.World
	dst.Vis = visibility.New(dst.World, 0)
	stage := &RetailBattleStage{Session: dst, StableUnit: stableUnitMap(dstIDs), Image: &save.BattleImage{
		HumanPlayer:    1,
		Metal:          make([]byte, len(dst.World.Plot)),
		PlayerFeatures: make([]byte, len(dst.World.Plot)/2),
		Mapping:        make([]byte, len(dst.Vis.WordMask())*2),
		Units:          save.UnitImage{Records: []save.UnitRecord{{StableID: dstIDs[0].stableID, Data: data}}},
	}}
	if err := RestoreRetailBattleCore(stage); err != nil {
		t.Fatal(err)
	}
	// Admit every projected tile; only the restored sonar exemption can pass
	// the fully submerged first hull probe for this foreign-owned unit.
	for i := range dst.Vis.WordMask() {
		dst.Vis.WordMask()[i] = 1 << 1
	}
	restored := dst.Units.Unit(dstIDs[0].handle)
	if !dst.IsUnitVisible(1, restored) {
		t.Fatal("restored sonar exemption missing before the next sensor phase")
	}
}

// A freed slot's detached sensor snapshot must never supply the replacement
// unit's contact bits [03 R-VIS-01 §4][P0-16 §6.3].
func TestSensorStatusReplacementDoesNotInheritContact(t *testing.T) {
	s := visibilityFixture(t, true)
	s.RegisterAll()
	s.Vis.SetViewingPlayer(visibility.PlayerID(s.ViewingOwner))
	def := s.Catalog.Units["armcom"]
	h, err := s.Units.Create(def, 0, 128<<16, 0, 128<<16)
	if err != nil {
		t.Fatal(err)
	}
	old := s.Units.Unit(h)
	s.Vis.SetMode(0)
	s.Vis.RebuildAll(nil)
	s.stepSensorPhase(1)
	unpublishOne(s, old)
	s.Units.FreeImmediate(h)
	next, err := s.Units.Create(def, 0, 800<<16, 0, 800<<16)
	if err != nil {
		t.Fatal(err)
	}
	if next != h {
		t.Fatal("fixture did not reuse the freed slot")
	}
	s.publishSnapshot(2)
	contact, ok := radarContactFor(s.Snapshot.Current(), next)
	if !ok {
		t.Fatal("replacement contact missing")
	}
	if contact.Status&visibility.FriendlyMask != 0 {
		t.Fatalf("replacement inherited sensor bits: %#x", contact.Status)
	}
}

// Selected circle presentation reads current activation, independently of the
// sensor phase's due deadline [03 §3.9 "Selected-unit circle gate"].
func TestSensorCircleActivationBetweenPasses(t *testing.T) {
	s := visibilityFixture(t, true)
	s.RegisterAll()
	s.Vis.SetViewingPlayer(visibility.PlayerID(s.ViewingOwner))
	def := s.Catalog.Units["armcom"]
	def.OnOffable, def.RadarDistance = true, 500
	h, err := s.Units.Create(def, s.ViewingOwner, 128<<16, 0, 128<<16)
	if err != nil {
		t.Fatal(err)
	}
	u := s.Units.Unit(h)
	u.Flags |= 0x10
	u.SetActivationEdge(true)
	s.stepSensorPhase(1)
	u.SetActivationEdge(false)
	s.publishSnapshot(2)
	c, ok := radarContactFor(s.Snapshot.Current(), h)
	if !ok || c.Active || c.RangeStatus || c.RadarDistance != 0 {
		t.Fatalf("deactivated unit retains circles: %+v", c)
	}
	s.stepSensorPhase(3)
	u.SetActivationEdge(true)
	s.publishSnapshot(4)
	c, ok = radarContactFor(s.Snapshot.Current(), h)
	if !ok || !c.Active || !c.RangeStatus || c.RadarDistance != def.RadarDistance {
		t.Fatalf("activated unit lacks circles: %+v", c)
	}
}

// Initial placement precedes RegisterAll. The production allocation binder
// must seed status before its OnCreate observer, not rely on that later hook
// [03 R-VIS-01 §4][08 R-ENTRY-01 §3].
func TestSensorStatusInitialPlacementBeforeRegisterAll(t *testing.T) {
	dir := t.TempDir()
	writeCompositionModel(t, dir, "fixture", 1)
	writeCompositionCOB(t, dir, "testunit", []string{"modelroot", "modelchild"})
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 10); err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "testunit"}, UnitName: "testunit", ObjectName: "fixture", MaxDamage: 10, Limit: -1, BMCode: 1}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	w, err := newSlicedWorldWithCOB(cat, fs)
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{Catalog: cat, World: minimalTerrain(), Mission: syntheticMission(), Units: w, Econ: &economy.Service{}, ViewingOwner: 1, LocalOwner: 1}
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatal(err)
	}
	w.OnCreate = func(_ pool.Handle, u *units.Unit) {
		want := uint32(0)
		if u.Owner == s.ViewingOwner {
			want = visibility.SonarBit
		}
		if got := u.Flags & (visibility.FriendlyMask | visibility.JammedBit); got != want {
			t.Fatalf("initial owner %d visible to creation observer with status %#x, want %#x", u.Owner, got, want)
		}
	}
	for _, owner := range []uint8{1, 0} {
		if _, err := w.Create(def, owner, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	s.RegisterAll()
	if _, err := w.Create(def, 1, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
}
