package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func TestRadarStepPublishesAuthoritativeSensorIdentity(t *testing.T) {
	def := &content.UnitDef{
		DefinitionHeader:  content.DefinitionHeader{CanonicalKey: "radar"},
		MaxDamage:         100,
		RadarDistance:     320,
		Stealth:           true,
		OnOffable:         true,
		ActivateWhenBuilt: true,
	}
	w := newSessionFixtureWorld(4, nil)
	h, err := w.Create(def, 1, numeric.Fixed(10<<16), 0, numeric.Fixed(12<<16))
	if err != nil {
		t.Fatalf("create unit: %v", err)
	}
	vis := visibility.New(&world.Terrain{CellW: 64, CellH: 64}, visibility.ModeHistoryEnabled|visibility.ModeCurrentEnabled)
	econ := &economy.Service{}
	econ.Players[0].Exists = true
	econ.Players[1].Exists = true
	s := &Session{Units: w, Vis: vis, Econ: econ}
	s.stepSensorPhase(7)
	inputs := vis.SensorInputs()
	if len(inputs) != 1 {
		t.Fatalf("sensor inputs = %+v, want one live unit", inputs)
	}
	got := inputs[0]
	if got.ID != uint16(h) || !got.Stealth || !got.OnOffable || !got.Active || got.X != numeric.Fixed(10<<16) || got.Z != numeric.Fixed(12<<16) {
		t.Fatalf("sensor identity/state = %+v, want handle=%d stealth+active+onoffable", got, h)
	}
}

func TestPublishSnapshotCarriesCommittedUnitActivation(t *testing.T) {
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "switchable"}, MaxDamage: 1, OnOffable: true}
	w := newSessionFixtureWorld(2, nil)
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create unit: %v", err)
	}
	w.Unit(h).Activated = true
	s := &Session{Snapshot: frame.NewBuffer(), Units: w, LocalOwner: 0}
	s.publishSnapshot(1)
	cur := s.Snapshot.Current()
	if cur == nil || len(cur.Units) != 1 || !cur.Units[0].Activated {
		t.Fatalf("published activation = %#v, want one active committed unit", cur)
	}
	w.Unit(h).Activated = false
	if !cur.Units[0].Activated {
		t.Fatal("mutating live activation changed the committed frame")
	}
}

func TestPublishSnapshotCarriesFeatureNoDrawUnderGray(t *testing.T) {
	terrain := strictMinimalTerrain()
	def := &content.FeatureDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "gray-feature"},
		FootprintX:       1,
		FootprintZ:       1,
		Damage:           100,
		NoDrawUnderGray:  true,
	}
	featureSvc := features.NewService(terrain, nil, nil, nil)
	if featureSvc.PlaceAt(2, 2, def) == nil {
		t.Fatal("feature placement failed")
	}
	s := &Session{Snapshot: frame.NewBuffer(), Features: featureSvc, LocalOwner: 0}
	s.publishSnapshot(1)
	cur := s.Snapshot.Current()
	if cur == nil || len(cur.Features) != 1 || !cur.Features[0].NoDrawUnderGray {
		t.Fatalf("published feature = %#v, want nodrawundergray", cur)
	}
	// The frame owns the definition flag; mutating the catalog object after
	// publication must not change the committed presentation input [I6].
	def.NoDrawUnderGray = false
	if !cur.Features[0].NoDrawUnderGray {
		t.Fatal("mutating live feature definition changed committed frame")
	}
}

func TestPublishSnapshotDoesNotAllocateMissingOrderQueue(t *testing.T) {
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "no-orders"}, MaxDamage: 1}
	w := newSessionFixtureWorld(2, nil)
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create unit: %v", err)
	}
	u := w.Unit(h)
	if u == nil || u.Orders != nil {
		t.Fatalf("fixture queue = %v, want absent", u)
	}
	s := &Session{Snapshot: frame.NewBuffer(), Units: w, LocalOwner: 0}
	s.publishSnapshot(1)
	if u.Orders != nil {
		t.Fatalf("publication allocated order queue %T", u.Orders)
	}
}

func TestPublishSnapshotCarriesRadarOwnerPalettes(t *testing.T) {
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "radar-palette"}, MaxDamage: 1}
	w := newSessionFixtureWorld(4, nil)
	if _, err := w.Create(def, 0, 0, 0, 0); err != nil {
		t.Fatalf("create owner-zero unit: %v", err)
	}
	if _, err := w.Create(def, 1, numeric.Fixed(1<<16), 0, 0); err != nil {
		t.Fatalf("create other-owner unit: %v", err)
	}
	// The palette is the player record's logo byte, which battle entry writes
	// and the save persists [08 R-SKIR-01 §2]; see player_record.go.
	s := &Session{
		Snapshot: frame.NewBuffer(), Units: w, LocalOwner: 0,
		Econ: &economy.Service{},
	}
	s.Econ.Players[0].Exists, s.Econ.Players[0].Logo = true, 0
	s.Econ.Players[1].Exists, s.Econ.Players[1].Logo = true, 7
	s.publishSnapshot(1)
	cur := s.Snapshot.Current()
	if cur == nil || len(cur.Radar.Contacts) != 2 {
		t.Fatalf("radar contacts = %#v, want two unit contacts", cur)
	}
	zero, other := cur.Radar.Contacts[0], cur.Radar.Contacts[1]
	if zero.Owner != 0 || !zero.OwnerKnown || !zero.PaletteKnown || zero.Palette != 0 {
		t.Fatalf("owner-zero radar palette = %+v, want known frame 0", zero)
	}
	if other.Owner != 1 || !other.OwnerKnown || !other.PaletteKnown || other.Palette != 7 {
		t.Fatalf("other-owner radar palette = %+v, want known frame 7", other)
	}
	// Publication owns the palette selector; changing the live player record
	// after publication must not alter the committed contact.
	s.Econ.Players[1].Logo = 3
	if cur.Radar.Contacts[1].Palette != 7 {
		t.Fatal("mutating live player color changed committed radar palette")
	}

	// Neutral/unknown contacts have no player-record selector and therefore no
	// owner art. The helper is the same path used by projectile/feature contacts.
	neutral, known := radarOwnerPalette(s, 10, true)
	if known || neutral != 0 {
		t.Fatalf("neutral radar palette = (%d, %t), want unknown zero", neutral, known)
	}
	unknown, known := radarOwnerPalette(s, 1, false)
	if known || unknown != 0 {
		t.Fatalf("unknown radar palette = (%d, %t), want unknown zero", unknown, known)
	}
}

// TestRadarStepLeavesStatusWithSingleActivePlayer locks the sensor phase's
// outermost gate [R-VIS-01 §4] "Gate": with one active player none of the five
// passes runs, so the seen, sonar and jammed bits keep whatever value they
// already carry.
//
// Correction (2026-08-30). This test was named ...ClearsSeen... and asserted
// "single-player SeenBit = %#x, want clear" — that the skip path scrubs the
// marker. Nothing in retail clears it there: the phase's own first pass is the
// only clear writer besides the radar-jam callback, and neither runs when the
// gate rejects.
func TestRadarStepLeavesStatusWithSingleActivePlayer(t *testing.T) {
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "seen"}, MaxDamage: 1}
	w := newSessionFixtureWorld(4, nil)
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create unit: %v", err)
	}
	vis := visibility.New(&world.Terrain{CellW: 64, CellH: 64}, visibility.ModeHistoryEnabled|visibility.ModeCurrentEnabled)
	econ := &economy.Service{}
	econ.Players[0].Exists = true
	s := &Session{Units: w, Vis: vis, Econ: econ, visStatus: map[int]uint32{int(h): visibility.SeenBit}}
	s.stepSensorPhase(8)
	if got := s.visStatus[int(h)]; got&visibility.SeenBit == 0 {
		t.Fatalf("single-player SeenBit = %#x, want the pre-existing bit untouched [R-VIS-01 §4]", got)
	}
}

// TestRadarContactsAreTheSoleCircleSource replaces
// TestRadarCirclesDropWhenSourceIsCleanedUp, retired 2026-08-30.
//
// What the retired test locked: that the sensor phase published a separate
// circle list onto the committed frame, and that the publisher dropped an entry
// whose source unit had been freed since the pass ran. There is no such list
// any more. [03 §3.10]'s 2026-08-29 correction establishes the circles as
// presentation drawn by the CONTACTS pass and retracts the reading that gave
// the sensor phase three rasterizing callback tables, so the stale-source
// hazard the test guarded cannot arise: a circle now exists only as a
// distance on a contact record, and a contact record only exists for a live
// unit.
func TestRadarContactsAreTheSoleCircleSource(t *testing.T) {
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "circle"}, MaxDamage: 1, RadarDistance: 50}
	w := newSessionFixtureWorld(4, nil)
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create unit: %v", err)
	}
	w.Unit(h).Activated = true
	w.Unit(h).Flags |= 0x10 // the authoritative selected bit [07 §9]
	vis := visibility.New(&world.Terrain{CellW: 64, CellH: 64}, visibility.ModeHistoryEnabled|visibility.ModeCurrentEnabled)
	s := &Session{Units: w, Vis: vis, Snapshot: frame.NewBuffer(), LocalOwner: 0}
	s.publishSnapshot(1)
	cur := s.Snapshot.Current()
	if cur == nil || len(cur.Radar.Contacts) != 1 || cur.Radar.Contacts[0].RadarDistance != 50 {
		t.Fatalf("selected unit contact = %#v, want its authored 50 [03 §3.9]", cur)
	}
	// A freed unit publishes no contact, and therefore no circle.
	w.Unit(h).Alive = false
	s.publishSnapshot(2)
	if got := s.Snapshot.Current(); got == nil || len(got.Radar.Contacts) != 0 {
		t.Fatalf("cleaned source contacts = %#v, want none", got)
	}
}

func TestRadarProjectileContactUsesOwnerAndLocalVisibility(t *testing.T) {
	vis := visibility.New(&world.Terrain{CellW: 64, CellH: 64}, visibility.ModeHistoryEnabled|visibility.ModeCurrentEnabled)
	combatSvc := &combat.Service{}
	h, ok := combatSvc.Reserve()
	if !ok {
		t.Fatal("reserve projectile")
	}
	combatSvc.Records[int(h)-1] = combat.Projectile{Pos: combat.Vec3{X: 0, Y: 0, Z: 0}, ShooterSide: 1}
	s := &Session{Snapshot: frame.NewBuffer(), Combat: combatSvc, Vis: vis, LocalOwner: 0}
	s.publishSnapshot(1)
	cur := s.Snapshot.Current()
	if cur == nil || len(cur.Radar.Contacts) != 1 {
		t.Fatalf("radar contacts = %#v", cur)
	}
	got := cur.Radar.Contacts[0]
	if got.Owner != 1 || !got.OwnerKnown || got.Visible {
		t.Fatalf("enemy projectile contact = %+v, want owner 1, known, hidden without LOS", got)
	}
	// A zero side with no shooter is unresolved, not local-player ownership.
	combatSvc.Records[int(h)-1].ShooterSide = 0
	s.publishSnapshot(2)
	got = s.Snapshot.Current().Radar.Contacts[0]
	if got.Owner != combat.NeutralSide || got.OwnerKnown || got.Visible {
		t.Fatalf("unowned projectile contact = %+v, want neutral/unknown/hidden", got)
	}
	// An unresolved nonzero shooter with a zero side is equally ambiguous; it
	// must not become known local-player-zero ownership.
	combatSvc.Records[int(h)-1].Shooter = pool.Handle(99)
	s.publishSnapshot(3)
	got = s.Snapshot.Current().Radar.Contacts[0]
	if got.Owner != combat.NeutralSide || got.OwnerKnown || got.Visible {
		t.Fatalf("unresolved shooter contact = %+v, want neutral/unknown/hidden", got)
	}
}

func TestRadarFeatureContactUsesPlacerOwnerAndExtents(t *testing.T) {
	terrain := &world.Terrain{CellW: 64, CellH: 64, Plot: make([]world.PlotCell, 64*64)}
	vis := visibility.New(terrain, visibility.ModeHistoryEnabled|visibility.ModeCurrentEnabled)
	local := uint8(0)
	owned := frame.FeatureView{Owner: local, OwnerKnown: true, CX: 4, CZ: 4, FootX: 1, FootZ: 1, Y: 0}
	if !radarFeatureVisible(&Session{Vis: vis, LocalOwner: local}, owned) {
		t.Fatal("local placer owner should bypass feature LOS")
	}
	unknown := frame.FeatureView{Owner: combat.NeutralSide, OwnerKnown: false, CX: 4, CZ: 4, FootX: 1, FootZ: 1, Y: 0}
	if radarFeatureVisible(&Session{Vis: vis, LocalOwner: local}, unknown) {
		t.Fatal("unknown feature owner must not bypass LOS")
	}
	unknownZero := frame.FeatureView{Owner: 0, OwnerKnown: false, CX: 4, CZ: 4, FootX: 1, FootZ: 1, Y: 0}
	if radarFeatureVisible(&Session{Vis: vis, LocalOwner: local}, unknownZero) {
		t.Fatal("unknown owner-zero feature must not bypass LOS")
	}
	inst := &features.Instance{Terrain: terrain, CX: 4, CZ: 4}
	if owner, known := featureOwnerSelector(inst); known || owner != combat.NeutralSide {
		t.Fatalf("zero placer selector = (%d, %t), want neutral/unknown", owner, known)
	}
}

func TestRadarSelectedRangeStatusGatePreservesSlotOrder(t *testing.T) {
	defs := []*content.UnitDef{
		{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "selected"}, MaxDamage: 1, RadarDistance: 100, OnOffable: false},
		{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "off"}, MaxDamage: 1, RadarDistance: 200, OnOffable: true, ActivateWhenBuilt: false},
		{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "unselected"}, MaxDamage: 1, RadarDistance: 300, OnOffable: false},
	}
	w := newSessionFixtureWorld(4, nil)
	for i, def := range defs {
		h, err := w.Create(def, 0, numeric.Fixed(int64(i+1)<<16), 0, 0)
		if err != nil {
			t.Fatalf("create unit %d: %v", i, err)
		}
		if i < 2 {
			w.Unit(h).Flags |= 0x10 // authoritative selected bit [07 §9]
		}
	}
	w.Unit(3).Flags &^= 0x10
	s := &Session{Snapshot: frame.NewBuffer(), Units: w, LocalOwner: 0}
	s.publishSnapshot(1)
	contacts := s.Snapshot.Current().Radar.Contacts
	if len(contacts) != 3 || contacts[0].Handle >= contacts[1].Handle || contacts[1].Handle >= contacts[2].Handle {
		t.Fatalf("contact order = %+v, want ascending handles", contacts)
	}
	if !contacts[0].Selected || contacts[0].Status&0x10 == 0 || !contacts[0].RangeStatus || contacts[0].RadarDistance != 100 {
		t.Fatalf("selected active range = %+v", contacts[0])
	}
	if !contacts[1].Selected || contacts[1].Status&0x10 == 0 || contacts[1].RangeStatus || contacts[1].RadarDistance != 0 {
		t.Fatalf("inactive onoffable range = %+v", contacts[1])
	}
	if contacts[2].Selected || contacts[2].Status&0x10 != 0 || contacts[2].RangeStatus || contacts[2].RadarDistance != 0 {
		t.Fatalf("unselected range = %+v", contacts[2])
	}
}

// TestRadarGameplayVisibilityUsesStealthAndTheInstanceCloakBit locks the two
// distinct sensor inputs. `stealth` is the definition flag, read straight
// through [03 R-VIS-01 §5]. Hidden is the INSTANCE cloaked bit, which only a
// paid settlement pass sets [05 R-ECO-01 §9] — `init_cloaked` alone must NOT
// hide the unit, because it seeds the cloak REQUEST and nothing more
// [03 R-VIS-01 §6] (WU-19-92).
func TestRadarGameplayVisibilityUsesStealthAndTheInstanceCloakBit(t *testing.T) {
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "stealth"}, MaxDamage: 1, Stealth: true, InitCloaked: true}
	w := newSessionFixtureWorld(4, nil)
	h, err := w.Create(def, 1, numeric.Fixed(10<<16), 0, numeric.Fixed(10<<16))
	if err != nil {
		t.Fatalf("create unit: %v", err)
	}
	vis := visibility.New(&world.Terrain{CellW: 64, CellH: 64}, visibility.ModeHistoryEnabled|visibility.ModeCurrentEnabled)
	econ := &economy.Service{}
	econ.Players[0].Exists, econ.Players[1].Exists = true, true
	s := &Session{Units: w, Vis: vis, Econ: econ}
	s.stepSensorPhase(1)
	inputs := vis.SensorInputs()
	if len(inputs) != 1 || !inputs[0].Stealth {
		t.Fatalf("gameplay stealth state = %+v, want stealth", inputs)
	}
	if inputs[0].Hidden {
		t.Fatal("init_cloaked alone hid the unit; only a paid settlement pass sets the instance bit [05 R-ECO-01 §9]")
	}
	// The settlement's transition, on a pass the owner paid for.
	w.Unit(h).Hidden = true
	s.stepSensorPhase(2)
	inputs = vis.SensorInputs()
	if len(inputs) != 1 || !inputs[0].Hidden {
		t.Fatalf("gameplay cloak state = %+v, want hidden after a paid pass", inputs)
	}
}

// TestDebugDisplayModeCyclesAndPublishesUnchanged locks the byte's three traced
// writers [03 §3.12][07 R-CAM-01 §9]: battle entry and film-mode exit store zero,
// and film mode's `m` key cycles 0,1,2,3,4,0. The publisher copies the value
// through unchanged.
func TestDebugDisplayModeCyclesAndPublishesUnchanged(t *testing.T) {
	s := &Session{Snapshot: frame.NewBuffer()}
	if s.DebugDisplayMode != 0 {
		t.Fatalf("initial mode = %d, want the explicit off value", s.DebugDisplayMode)
	}
	for want := 1; want <= 4; want++ {
		s.CycleDebugDisplayMode()
		if int(s.DebugDisplayMode) != want {
			t.Fatalf("cycle %d gave %d", want, s.DebugDisplayMode)
		}
	}
	s.CycleDebugDisplayMode()
	if s.DebugDisplayMode != 0 {
		t.Fatalf("cycle past 4 gave %d, want the wrap to 0", s.DebugDisplayMode)
	}
	s.CycleDebugDisplayMode()
	s.CycleDebugDisplayMode()
	s.publishSnapshot(1)
	if got := s.Snapshot.Current().Radar.MarkerMode; got != 2 {
		t.Fatalf("published mode = %d, want the session's 2", got)
	}
	s.ResetDebugDisplayMode()
	s.publishSnapshot(2)
	if got := s.Snapshot.Current().Radar.MarkerMode; got != 0 {
		t.Fatalf("published mode = %d, want the reset 0", got)
	}
}

func TestSnapshotVisibilityOwnsMasksAcrossBeginWrite(t *testing.T) {
	vis := visibility.New(&world.Terrain{CellW: 64, CellH: 64}, visibility.ModeHistoryEnabled|visibility.ModeCurrentEnabled)
	vis.Publish(0, 0, 0, 0, 0)
	serviceWord := vis.WordMask()[0]
	s := &Session{Snapshot: frame.NewBuffer(), Vis: vis, LocalOwner: 0}
	s.publishSnapshot(1)
	committed := s.Snapshot.Current()
	if committed == nil || len(committed.Visibility.WordVisible) == 0 {
		t.Fatal("visibility was not published")
	}
	committedWord := committed.Visibility.WordVisible[0]
	write := s.Snapshot.BeginWrite()
	write.Visibility.WordVisible = append(write.Visibility.WordVisible, 0xffff)
	write.Visibility.WordVisible[0] = 0
	if vis.WordMask()[0] != serviceWord {
		t.Fatalf("write slot mutated visibility service: got %x want %x", vis.WordMask()[0], serviceWord)
	}
	if committed.Visibility.WordVisible[0] != committedWord {
		t.Fatalf("write/reset mutated prior committed visibility: got %x want %x", committed.Visibility.WordVisible[0], committedWord)
	}
}

func TestSnapshotPublicationUsesWordCoverageWhenBytesDisabled(t *testing.T) {
	terrain := &world.Terrain{CellW: 64, CellH: 64}
	vis := visibility.New(terrain, visibility.ModeHistoryEnabled)
	unitsPool := newSessionFixtureWorld(4, nil)
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "foreign"}, MaxDamage: 1}
	if _, err := unitsPool.Create(def, 1, 0, 0, 0); err != nil {
		t.Fatalf("create foreign unit: %v", err)
	}
	s := &Session{Snapshot: frame.NewBuffer(), Units: unitsPool, Vis: vis, LocalOwner: 0}
	s.publishSnapshot(1)
	got := s.Snapshot.Current()
	if got == nil || got.Visibility.CoverageBytes {
		t.Fatalf("coverage mode = %#v, want word-grid mode", got)
	}
	if len(got.Visibility.Visible) != 0 {
		t.Fatalf("disabled byte coverage published %d byte cells", len(got.Visibility.Visible))
	}
	if len(got.Visibility.WordVisible) == 0 || len(got.Units) != 1 {
		t.Fatalf("publication missing word mask or unit: visibility=%#v units=%d", got.Visibility, len(got.Units))
	}
	if client.SnapshotVisible(got, got.Units[0], 0) {
		t.Fatal("foreign unit bypassed disabled byte coverage through permissive fill")
	}
	foreignFeature := frame.FeatureView{Owner: 1, OwnerKnown: true, CX: 0, CZ: 0, FootX: 1, FootZ: 1, Y: 0}
	if radarFeatureVisible(s, foreignFeature) {
		t.Fatal("foreign feature bypassed disabled byte coverage through permissive fill")
	}
}

func TestSnapshotPublishFailurePreservesStagedEvents(t *testing.T) {
	c := frame.NewEventBuffer(frame.Limits{})
	if !c.EmitImpact(frame.Event{Tick: 4, Graphic: "pending"}) {
		t.Fatal("admit event")
	}
	s := &Session{Snapshot: frame.NewBuffer(), publication: newPublicationState(c)}
	s.publishSnapshot(4)
	if !c.EmitExplosion(frame.Event{Tick: 4, Graphic: "must-remain"}) {
		t.Fatal("admit duplicate-tick event")
	}
	// publishSnapshot treats a duplicate tick as an impossible session
	// invariant and panics only after copying, never resetting, staging.
	func() {
		defer func() { _ = recover() }()
		s.publishSnapshot(4)
	}()
	events := c.Events()
	if len(events) != 1 || events[0].Graphic != "must-remain" {
		t.Fatalf("failed publication discarded staged event: %+v", events)
	}
	c.Reset()
	if !c.EmitImpact(frame.Event{Tick: 3, Graphic: "retrograde-must-remain"}) {
		t.Fatal("admit retrograde event")
	}
	func() {
		defer func() { _ = recover() }()
		s.publishSnapshot(3)
	}()
	events = c.Events()
	if len(events) != 1 || events[0].Graphic != "retrograde-must-remain" {
		t.Fatalf("retrograde publication discarded staged event: %+v", events)
	}
}

func TestSnapshotWarmPublicationReusesFrameStorage(t *testing.T) {
	c := frame.NewEventBuffer(frame.Limits{MaxEvents: 2, MaxEffectEvents: 2})
	s := &Session{Snapshot: frame.NewBuffer(), publication: newPublicationState(c)}
	for tick := uint32(1); tick <= 3; tick++ {
		if !c.EmitExplosion(frame.Event{Tick: tick, Graphic: "steady"}) {
			t.Fatal("warm event admission")
		}
		s.publishSnapshot(tick)
	}
	next := uint32(4)
	allocs := testing.AllocsPerRun(20, func() {
		if !c.EmitExplosion(frame.Event{Tick: next, Graphic: "steady"}) {
			t.Fatal("steady event admission")
		}
		s.publishSnapshot(next)
		next++
	})
	if allocs != 0 {
		t.Fatalf("warm committed publication allocated %v times", allocs)
	}
}

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

	s := &Session{Catalog: cat, Combat: combatSvc, Snapshot: &frame.Buffer{}}
	s.publishSnapshot(10)
	frame := s.Snapshot.Current()
	if frame == nil || len(frame.Projectiles) != 1 {
		t.Fatalf("projectile frame = %#v, ok=%v", frame, ok)
	}
	got := frame.Projectiles[0]
	if got.Handle != h || got.X != p.Pos.X || got.Y != p.Pos.Y || got.Z != p.Pos.Z || got.Shooter != p.Shooter {
		t.Fatalf("identity/position = %+v", got)
	}
	if got.StartX != p.StartPos.X || got.TailZ != p.StartPos.Z || got.TargetY != p.TargetPos.Y || got.VZ != p.Velocity.Z {
		t.Fatalf("endpoints/velocity = %+v", got)
	}
	// The frame carries retail's yaw word — combat's stored yaw plus half a
	// turn [06 R-WPN-05 §11] — and the pitch unchanged.
	if got.Yaw != combat.RetailYaw(p.Yaw) || got.Yaw == uint16(p.Yaw) || got.Pitch != uint16(p.Pitch) || got.CreationTick != p.CreationTick || got.ExpiryTick != p.ExpiryTick || got.MuzzlePiece != int32(p.MuzzlePiece) {
		t.Fatalf("orientation/timing = %+v", got)
	}
	// Lifetime carried a zero "explicitly unknown" marker until [06 R-WFX-01 §4]
	// named the lifetime-scaled render type's divisor: it is `weapontimer`.
	if got.Family != int32(combat.CreationBallistic) || got.RenderType != w.RenderType || got.Model != w.Model || got.Graphic != w.Model || !got.SmokeTrail || got.Lifetime != w.WeaponTimer {
		t.Fatalf("authored render metadata = %+v", got)
	}
	// The selector is the weapon record's `color` BYTE, not a placeholder: it
	// is the render-type-4 sequence selector and, read as a signed value, the
	// 255 the stock `earthquake` authors becomes the −1 that suppresses that
	// case [06 R-WFX-01 §1][06 R-WFX-01 §4]. `color` and `color2` are also the
	// palette indices the beam and lightning strokes are drawn in, so both
	// travel with a presence bit. This assertion used to demand the
	// unconditional −1 the publication wrote before any of that was carried —
	// which suppressed every gun, shell and laser in the game at the dispatch.
	if got.Selector != int32(w.Color) || !got.HasPrimaryColor || got.PrimaryColor != uint8(w.Color) || !got.HasSecondaryColor || got.SecondaryColor != uint8(w.Color2) {
		t.Fatalf("colour/selector = selector %d, primary %d/%v, secondary %d/%v; want the authored `color`/`color2` bytes",
			got.Selector, got.PrimaryColor, got.HasPrimaryColor, got.SecondaryColor, got.HasSecondaryColor)
	}
	w.Color = 255
	s.publishSnapshot(11)
	if again := s.Snapshot.Current().Projectiles[0]; again.Selector != -1 || again.PrimaryColor != 255 {
		t.Fatalf("an authored `color` of 255 published selector %d colour %d, want the −1 suppression sentinel over the unchanged byte", again.Selector, again.PrimaryColor)
	}
}

func TestSnapshotPublishesEventsInAdmissionOrderExactlyOnce(t *testing.T) {
	c := frame.NewEventBuffer(frame.Limits{})
	if !c.EmitImpact(frame.Event{Tick: 4, Graphic: "first"}) || !c.EmitExplosion(frame.Event{Tick: 4, Graphic: "second"}) {
		t.Fatal("admit presentation events")
	}
	s := &Session{Snapshot: &frame.Buffer{}, publication: newPublicationState(c)}
	s.publication.effects.Advance(4, c.StagingEvents())
	s.publishSnapshot(4)
	first := s.Snapshot.Current()
	if first == nil || len(first.Events) != 2 {
		t.Fatalf("first events = %#v", first)
	}
	if first.Events[0].Graphic != "first" || first.Events[1].Graphic != "second" || first.Events[0].Sequence >= first.Events[1].Sequence {
		t.Fatalf("event order = %+v", first.Events)
	}
	if len(c.Events()) != 0 || c.Dropped() != 0 {
		t.Fatalf("collector not reset after publication: dropped=%d events=%d", c.Dropped(), len(c.Events()))
	}
	s.publishSnapshot(5)
	second := s.Snapshot.Current()
	if second == nil || len(second.Events) != 0 {
		t.Fatalf("events repeated on next frame = %#v", second)
	}
}

func TestSnapshotPublishesActiveEffectsFromOrderedEvents(t *testing.T) {
	c := frame.NewEventBuffer(frame.Limits{})
	if !c.EmitNanolathe(frame.Event{
		Tick: 4, Source: 2, Target: 3, Piece: 6, EffectID: 6, Lifetime: 1,
		X: 11, Y: 12, Z: 13, TargetX: 21, TargetY: 22, TargetZ: 23,
	}) {
		t.Fatal("admit nanolathe event")
	}
	s := &Session{Snapshot: &frame.Buffer{}, publication: newPublicationState(c)}
	s.publication.effects.Advance(4, c.StagingEvents())
	s.publishSnapshot(4)
	frame := s.Snapshot.Current()
	if frame == nil || len(frame.Effects) != 1 {
		t.Fatalf("effects = %#v", frame.Effects)
	}
	got := frame.Effects[0]
	if got.ID != frame.Events[0].ID || got.EventSeq != frame.Events[0].Sequence || got.EffectID != 6 || got.Piece != 6 || got.X != 11 || got.TargetZ != 23 {
		t.Fatalf("effect publication = %+v events=%+v", got, frame.Events)
	}
	// Both Events and Effects are one-shot windows derived from the same
	// ordered admission: each visual event produces exactly one EffectView for
	// that tick, and neither persists beyond the window [F-P0-034][03 §1] C5.
	s.publication.effects.Advance(5, nil)
	s.publishSnapshot(5)
	next := s.Snapshot.Current()
	if next == nil || len(next.Events) != 0 || len(next.Effects) != 0 {
		t.Fatalf("effect/event lifecycle = %+v", next)
	}
}

func TestSnapshotPublishesConstructionLink(t *testing.T) {
	builderDef := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "builder"}, UnitName: "builder", Builder: true, MaxDamage: 100, FootprintX: 2, FootprintZ: 2}
	productDef := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "product"}, UnitName: "product", MaxDamage: 200, FootprintX: 1, FootprintZ: 1}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{builderDef.CanonicalKey: builderDef, productDef.CanonicalKey: productDef}}
	unitsWorld := newSessionFixtureWorld(8, cat)
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
	s := &Session{Catalog: cat, Units: unitsWorld, Build: build, Snapshot: &frame.Buffer{}}
	s.publishSnapshot(9)
	frame := s.Snapshot.Current()
	if frame == nil || len(frame.Builds) != 1 {
		t.Fatalf("build frame = %#v", frame)
	}
	got := frame.Builds[0]
	if got.Builder != builder || got.Product != product || got.ProductKey != productDef.CanonicalKey || got.Remaining != productUnit.Remaining || got.Health != productUnit.Health || got.FootX != 1 || got.FootZ != 1 {
		t.Fatalf("build publication = %+v", got)
	}
}

func TestSnapshotQueuePublishesBuildFootprint(t *testing.T) {
	product := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "testproduct"},
		FootprintX:       3,
		FootprintZ:       2,
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{product.CanonicalKey: product}}
	owner := pool.Handle(4)
	queue := orders.NewQueueWith([]*orders.Node{{
		ID:          orders.Lookup("MobileBuild"),
		Owner:       owner,
		GoalX:       numeric.Fixed(16 << 16),
		GoalZ:       numeric.Fixed(24 << 16),
		BuildDefKey: product.CanonicalKey,
	}}, nil)
	views := appendOrderQueueView(nil, orders.SnapshotQueueOf(queue, owner, nil), cat)
	view := views[0]
	if len(view.Primary) != 1 {
		t.Fatalf("published queue length = %d, want 1", len(view.Primary))
	}
	got := view.Primary[0]
	if got.BuildProduct != product.CanonicalKey || got.FootX != 3 || got.FootZ != 2 {
		t.Fatalf("published build geometry = %+v, want product footprint 3x2", got)
	}
}
