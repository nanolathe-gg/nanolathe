package combat

import (
	"bytes"
	"encoding/hex"
	"io"
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func combatCheckpointContext(t *testing.T, weapons ...*content.WeaponDef) *CheckpointContext {
	t.Helper()
	cat := &content.Catalog{Weapons: make(map[string]*content.WeaponDef)}
	for _, w := range weapons {
		cat.Weapons[w.CanonicalKey] = w
	}
	fs := vfs.New()
	if err := fs.MountDirectory(t.TempDir(), 10); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fs.Close() })
	sources, err := content.CaptureSimulationSources(fs, "")
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := content.FreezeSimulationInputs(sources, content.SimulationInputRequest{Catalog: cat})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := inputs.CheckpointKeys()
	if err != nil {
		t.Fatal(err)
	}
	return NewCheckpointContext(units.NewCheckpointContext(keys), world.NewCheckpointContext(keys))
}

func combatCheckpointBytes(t *testing.T, s *Service, c *CheckpointContext) []byte {
	t.Helper()
	if _, err := s.CollectCheckpointReferences(c); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := s.WriteCheckpoint(checkpoint.NewEncoder(&out), c); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func checkpointHex(t *testing.T, value string) []byte {
	t.Helper()
	b, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The literal vector is authored independently of the writer. In particular,
// Fixed carries high bits, handles take four bytes, raw WeaponID is signed,
// and the raw state byte is not rebuilt from its deliberately disagreeing flags.
func TestCheckpointProjectileVector(t *testing.T) {
	p := Projectile{
		AutomaticAttackBurst: true, BeamLatch: false, BurstDeadline: 0x11223344, BurstRemaining: -2,
		CacheCellX: -3, CacheCellZ: 4, CachedFloorHeight: -5, CreationTick: 6, Dead: true, ExpiryTick: 7,
		GroundAttackBurst: true, MeteorPitch: 0x8899, MuzzlePiece: -9, OldMarker: -10, OrderedBurst: true,
		Pitch: 0xabcd, Pos: Vec3{-11, 0x10000000c, 13}, PropellerYaw: 14, Roll: 15, Shooter: 0xf123,
		ShooterSide: 16, SmokeDeadline: 17, Speed: -18, StartPos: Vec3{19, -20, 21}, State69: 0xe2,
		StoredPlanarDistance: -22, TargetPos: Vec3{23, 24, -25}, TargetProjectile: 0xabcd, TargetUnit: 0xfedc,
		TwoPhase: true, Velocity: Vec3{26, -27, 28}, WeaponID: -29, Yaw: 0xdead,
	}
	var out bytes.Buffer
	e := checkpoint.NewEncoder(&out)
	writeCheckpointProjectile(e, &p, "projectile")
	if err := e.Err(); err != nil {
		t.Fatal(err)
	}
	want := checkpointHex(t, "010044332211fefffffffdffffff04000000fbff060000000107000000019988f7fff6ff01cdabf5ffffffffffffff0c000000010000000d000000000000000e000f0023f100001011000000eeffffffffffffff1300000000000000ecffffffffffffff1500000000000000e2eaffffffffffffff17000000000000001800000000000000e7ffffffffffffffcdab0000dcfe0000011a00000000000000e5ffffffffffffff1c00000000000000e3ffffffadde")
	if !bytes.Equal(out.Bytes(), want) {
		t.Fatalf("got %x\nwant %x", out.Bytes(), want)
	}
}

// This whole-owner vector fixes framing and service field ordering without
// using any production leaf to construct the expectation. Community's resolved
// zero table is 32 bools and 14 i64s, and an empty projectile is 180 bytes.
func TestCheckpointCombatOwnerVector(t *testing.T) {
	s := NewServiceWithProjectileCapacity(1)
	s.OpaqueLiquidMode = true
	s.TransportDeaths.hasTick = true
	s.TransportDeaths.lastTick = 0x11223344
	s.communityAreaBuilt = true
	s.communityAreaBuiltTick = 2
	s.communityAreaCells = []communityAreaCell{{count: -3, head: 4, stamp: 5, tail: 6}}
	s.communityAreaGenCounter = 7
	s.communityAreaHeight = 8
	s.communityAreaHitGen = []uint32{9, 10}
	s.communityAreaLimit = 11
	s.communityAreaNodes = []communityAreaNode{{next: -12, unit: 0xabcd}}
	s.communityAreaStamp = 13
	s.communityAreaWidth = 14
	s.deathNotified = map[pool.Handle]*units.Unit{0xffff: nil, 1: nil}
	s.doubleShot = true
	s.halfShot = true
	s.modernNextProjectileTick = 15
	s.modernTick = 16
	s.scanCursor.next[0] = -17
	s.shootAll = true
	s.targets.gate[9] = true
	s.targets.lastRebuild[0] = 18
	s.targets.primary[0] = []pool.Handle{0xffff, 2}
	s.targets.secondary[9] = []pool.Handle{3}
	want := checkpointHex(t,
		strings.Repeat("00", 144)+ // resolved Community
			strings.Repeat("00", 9)+"01"+"0000"+ // ports, liquid, wind/reaction
			"01000000"+strings.Repeat("00", 180)+"00"+ // Records and Rules
			"010000000000000000000000000000000100000000"+ // Slots capacity/count/dead
			"014433221100000000"+"0000"+ // transport and final ports
			"010200000001000000fdffffff040000000500000006000000"+
			"070000000800000002000000090000000a0000000b00000001000000f4ffffffcdab0000"+
			"0d0000000e0000000200000001000000010000000000ffff0000010000000000"+
			"0101000000000f00000010000000"+
			"efffffffffffffff"+strings.Repeat("00", 72)+ // cursors
			"01"+ // the +ShootAll gate
			strings.Repeat("00", 9)+"01"+"12000000"+strings.Repeat("00", 36)+
			"02000000ffff000002000000"+strings.Repeat("00", 36)+ // primary
			strings.Repeat("00", 36)+"0100000003000000")
	got := combatCheckpointBytes(t, s, combatCheckpointContext(t))
	if !bytes.Equal(got, want) {
		t.Fatalf("owner got (%d) %x\nwant (%d) %x", len(got), got, len(want), want)
	}
}

func TestCheckpointCombatReferencesAndLiveIncoming(t *testing.T) {
	weapon := &content.WeaponDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "pulse"}, ID: 7}
	c := combatCheckpointContext(t, weapon)
	s := NewServiceWithProjectileCapacity(3)
	s.Slots.Reserve()
	s.Slots.Reserve()
	s.Slots.MarkDead(1)
	a := &units.Unit{Handle: 9, AllocationSerial: 1}
	b := &units.Unit{Handle: 9, AllocationSerial: 2}
	d := &units.Unit{Handle: 2, AllocationSerial: 3}
	f := &units.Unit{Handle: 3, AllocationSerial: 4}
	poison := &units.Unit{Handle: 8}
	s.TransportDeaths.passengers = []transportDeathPassenger{{unit: a, handle: 99, typeID: 101, health: -3}}
	s.deathNotified = map[pool.Handle]*units.Unit{8: b, 2: d, 1: a}
	s.incoming[0] = incomingShot{target: b, shooter: f, weapon: weapon, beamInvalid: true, motion: modernBeamMotion{heading: 0xabcd, mode: 2, speed: -3, velocity: Vec3{4, -5, 0x100000006}}}
	s.incoming[1] = incomingShot{target: a, shooter: b}
	s.incoming[2] = incomingShot{target: poison, weapon: &content.WeaponDef{}}
	if n, err := s.CollectCheckpointReferences(c); n != 4 || err != nil {
		t.Fatalf("collect %d %v", n, err)
	}
	if got := c.Units.Allocations.Values(); !reflect.DeepEqual(got, []*units.Unit{a, d, b, f}) {
		t.Fatal("root order or stale allocation identity changed")
	}
	if n, err := s.CollectCheckpointReferences(c); n != 0 || err != nil {
		t.Fatalf("repeat %d %v", n, err)
	}
	before := combatCheckpointBytes(t, s, c)
	s.incoming[2] = incomingShot{}
	if !bytes.Equal(before, combatCheckpointBytes(t, s, c)) {
		t.Fatal("incoming tail retained")
	}
	s.incoming[0].beamInvalid = false
	if bytes.Equal(before, combatCheckpointBytes(t, s, c)) {
		t.Fatal("dead-prefix prediction omitted")
	}
	s.incoming[0].beamInvalid = true
	// A distinct pointer with equal weapon fields is not an admitted definition.
	copyWeapon := *weapon
	s.incoming[0].weapon = &copyWeapon
	if _, err := s.CollectCheckpointReferences(c); err == nil {
		t.Fatal("unadmitted equal weapon accepted")
	}
	s.incoming[0].weapon = weapon
	var out bytes.Buffer
	writeCheckpointIncoming(checkpoint.NewEncoder(&out), c, s.incoming[0], "incoming")
	// Admitted catalog family 1, weapon ordinal 7, literal key weapon/pulse.
	want := checkpointHex(t, "01cdab02fdffffffffffffff0400000000000000fbffffffffffffff06000000010000000100040000000100030000000101070000000c000000776561706f6e2f70756c7365")
	if !bytes.Equal(out.Bytes(), want) {
		t.Fatalf("incoming got %x want %x", out.Bytes(), want)
	}
	out.Reset()
	s.TransportDeaths.writeCheckpoint(checkpoint.NewEncoder(&out), c)
	want = checkpointHex(t, "00000000000100000063000000fdffffff65000000010001000000")
	if !bytes.Equal(out.Bytes(), want) {
		t.Fatalf("transport got %x want %x", out.Bytes(), want)
	}
	// A writer cannot silently collect a new allocation itself.
	fresh := NewCheckpointContext(units.NewCheckpointContext(c.Units.Keys), c.World)
	if err := s.WriteCheckpoint(checkpoint.NewEncoder(io.Discard), fresh); err == nil {
		t.Fatal("undiscovered reference accepted")
	}
}

func TestCheckpointCombatResidualsAndRetainedState(t *testing.T) {
	c := combatCheckpointContext(t)
	baseline := combatCheckpointBytes(t, NewServiceWithProjectileCapacity(2), c)
	for name, edit := range map[string]func(*Service){
		"tail cached cell":   func(s *Service) { s.Records[1].CacheCellX = -1 },
		"tail raw target":    func(s *Service) { s.Records[1].TargetProjectile = 0xffff },
		"tail marker":        func(s *Service) { s.Records[1].OldMarker = -1 },
		"tail floor":         func(s *Service) { s.Records[1].CachedFloorHeight = -1 },
		"tail raw weapon":    func(s *Service) { s.Records[1].WeaponID = -999 },
		"tail flags":         func(s *Service) { s.Records[1].State69 = 0xff },
		"area stamp":         func(s *Service) { s.communityAreaStamp = 1 },
		"area generation":    func(s *Service) { s.communityAreaGenCounter = 1 },
		"area hits":          func(s *Service) { s.communityAreaHitGen = []uint32{0xffffffff} },
		"area stale cell":    func(s *Service) { s.communityAreaCells = []communityAreaCell{{stamp: 0xffffffff, head: -1}} },
		"area stale node":    func(s *Service) { s.communityAreaNodes = []communityAreaNode{{unit: 0xffff, next: -1}} },
		"transport tick":     func(s *Service) { s.TransportDeaths.lastTick = 1 },
		"modern tick":        func(s *Service) { s.modernTick = 1 },
		"target stale raw":   func(s *Service) { s.targets.primary[0] = []pool.Handle{0xffff} },
		"target secondary":   func(s *Service) { s.targets.secondary[9] = []pool.Handle{0} },
		"target gate":        func(s *Service) { s.targets.gate[0] = true },
		"target rebuild":     func(s *Service) { s.targets.lastRebuild[0] = 1 },
		"scan cursor":        func(s *Service) { s.scanCursor.next[0] = -1 },
		"resolved community": func(s *Service) { s.Community.AreaDamageOverflow = true },
	} {
		t.Run(name, func(t *testing.T) {
			s := NewServiceWithProjectileCapacity(2)
			edit(s)
			if bytes.Equal(baseline, combatCheckpointBytes(t, s, c)) {
				t.Fatal("retained mutation lost")
			}
		})
	}
	s := NewServiceWithProjectileCapacity(2)
	s.Slots.Reserve()
	s.Slots.Reserve()
	s.Records[0].WeaponID = 1
	s.Records[1].WeaponID = 2
	before := combatCheckpointBytes(t, s, c)
	s.Slots.MarkDead(1)
	dead := combatCheckpointBytes(t, s, c)
	if bytes.Equal(before, dead) || s.Records[0].Dead {
		t.Fatal("pool flags normalized or omitted")
	}
	s.Records[0], s.Records[1] = s.Records[1], s.Records[0]
	if bytes.Equal(dead, combatCheckpointBytes(t, s, c)) {
		t.Fatal("physical record order omitted")
	}
	s.targets.primary[0] = []pool.Handle{9, 3, 9}
	before = combatCheckpointBytes(t, s, c)
	s.targets.primary[0] = []pool.Handle{3, 9, 9}
	if bytes.Equal(before, combatCheckpointBytes(t, s, c)) {
		t.Fatal("target list sorted")
	}
	s.communityAreaNodes = []communityAreaNode{{unit: 2}, {unit: 1}}
	before = combatCheckpointBytes(t, s, c)
	s.communityAreaNodes[0], s.communityAreaNodes[1] = s.communityAreaNodes[1], s.communityAreaNodes[0]
	if bytes.Equal(before, combatCheckpointBytes(t, s, c)) {
		t.Fatal("area insertion order omitted")
	}
}

func TestCheckpointCombatExclusionsAndPurity(t *testing.T) {
	c := combatCheckpointContext(t)
	s := NewServiceWithProjectileCapacity(2)
	s.Slots.Reserve()
	s.Records[1].State69 = 0xfd
	u := &units.Unit{Handle: 4, AllocationSerial: 1}
	s.TransportDeaths.passengers = []transportDeathPassenger{{unit: u, handle: 8, health: -3, typeID: 6}}
	s.deathNotified = map[pool.Handle]*units.Unit{9: u, 2: nil}
	s.incoming[0].target = u
	s.communityAreaCells = []communityAreaCell{{head: -1, stamp: 77}}
	s.communityAreaNodes = []communityAreaNode{{unit: 4, next: -1}}
	s.communityAreaHitGen = []uint32{99}
	s.targets.primary[0] = []pool.Handle{8, 4}
	before := *s
	before.Records = append([]Projectile(nil), s.Records...)
	before.incoming = append([]incomingShot(nil), s.incoming...)
	before.TransportDeaths.passengers = append([]transportDeathPassenger(nil), s.TransportDeaths.passengers...)
	before.deathNotified = maps.Clone(s.deathNotified)
	before.communityAreaCells = append([]communityAreaCell(nil), s.communityAreaCells...)
	before.communityAreaNodes = append([]communityAreaNode(nil), s.communityAreaNodes...)
	before.communityAreaHitGen = append([]uint32(nil), s.communityAreaHitGen...)
	before.targets.primary[0] = append([]pool.Handle(nil), s.targets.primary[0]...)
	baseline := combatCheckpointBytes(t, s, c)
	if !reflect.DeepEqual(before, *s) {
		t.Fatal("capture mutated source state")
	}
	s.pendingAims = map[pendingKey]pendingAim{{Unit: 4, Slot: 2}: {ThreadIdx: 7, DispatchedTick: 99}}
	s.boxCentres = map[boxCentreKey]boxCentreEntry{{}: {}}
	s.weaponByIDCatalog = &content.Catalog{}
	s.weaponByID = func(int32) (*content.WeaponDef, bool) { panic("cache invoked") }
	s.presentationIDs = []uint64{9, 8}
	s.compactScratch = []int16{99}
	s.communityAreaSaturations = 99
	s.communityAreaUnits = []*units.Unit{u}
	s.candidateScratch = []Candidate{{}}
	s.targets.walkScratch = []*units.Unit{u}
	s.targetQuery = TargetQuery{Shooter: u}
	s.shotQuery = ShotQuery{Shooter: u}
	s.firingPosition = firingPositionObservation{shooter: u, tick: 97}
	if !bytes.Equal(baseline, combatCheckpointBytes(t, s, c)) {
		t.Fatal("excluded cache/scratch state retained")
	}
	empty := NewServiceWithProjectileCapacity(1)
	baseline = combatCheckpointBytes(t, empty, c)
	empty.incoming = nil
	if !bytes.Equal(baseline, combatCheckpointBytes(t, empty, c)) {
		t.Fatal("zero-count nil incoming differs")
	}
}

func TestCheckpointCombatRefusalBoundaries(t *testing.T) {
	c := combatCheckpointContext(t)
	for name, edit := range map[string]func(*Service){
		"ControlByte":    func(s *Service) { s.SetControlByte(func(uint8) uint8 { panic("called") }) },
		"DamageActivity": func(s *Service) { s.SetDamageActivity(func(*units.Unit, *units.Unit, uint32) { panic("called") }) },
		"DangerNotice":   func(s *Service) { s.SetDangerNotice(func(*units.Unit, *units.Unit, uint32) { panic("called") }) },
		"Events":         func(s *Service) { s.SetEvents(func(Event) { panic("called") }) },
		"Features":       func(s *Service) { s.Features = &features.Service{} },
		"HealthLost":     func(s *Service) { s.SetHealthLost(func(*units.Unit, *units.Unit, int32) { panic("called") }) },
		"ImpactNotice": func(s *Service) {
			s.SetImpactNotice(func(*units.Unit, *units.Unit, numeric.Angle, uint32) { panic("called") })
		},
		"InfectionThreat": func(s *Service) { s.SetInfectionThreat(func(*units.Unit) bool { panic("called") }) },
		"IsOffMapFiled":   func(s *Service) { s.SetIsOffMapFiled(func(pool.Handle) bool { panic("called") }) },
		"ProjectileWind":  func(s *Service) { s.ProjectileWind = &world.Wind{} },
		"Reaction":        func(s *Service) { s.Reaction = &ReactionSeams{} },
		"Rules":           func(s *Service) { s.Rules = checkpointUnknownRules{} },
		"Visibility": func(s *Service) {
			s.SetVisibility(func(visibility.PlayerID, visibility.Target) bool { panic("called") })
		},
		"VisitOffMapFiled":        func(s *Service) { s.SetVisitOffMapFiled(func(func(pool.Handle, uint64) bool) { panic("called") }) },
		"Records":                 func(s *Service) { s.Records = nil },
		"incoming":                func(s *Service) { s.Slots.Reserve(); s.incoming = nil },
		"impactStack":             func(s *Service) { s.impactStack = []pool.Handle{1} },
		"TransportDeaths.pending": func(s *Service) { s.TransportDeaths.pending = &units.Unit{} },
		"communityAreaCurrentGen": func(s *Service) { s.communityAreaCurrentGen = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			s := NewServiceWithProjectileCapacity(2)
			edit(s)
			if _, err := s.CollectCheckpointReferences(c); err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("collect error %v", err)
			}
			var out bytes.Buffer
			if err := s.WriteCheckpoint(checkpoint.NewEncoder(&out), c); err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("write error %v", err)
			}
			if out.Len() != 0 {
				t.Fatal("refused binding wrote payload")
			}
			// The cheap row must not attest, inspect or invoke these bindings.
			if err := s.AppendCheckpointSummary(&checkpoint.Summary{}); err != nil {
				t.Fatal(err)
			}
		})
	}
	s := NewServiceWithProjectileCapacity(2)
	for _, row := range [][]incomingShot{{}, {{}}, make([]incomingShot, 3)} {
		s.incoming = row
		if _, err := s.CollectCheckpointReferences(c); err == nil {
			t.Fatal("malformed incoming accepted")
		}
	}
	s = NewServiceWithProjectileCapacity(2)
	var typedNil *StrictRules
	s.Rules = typedNil
	if _, err := s.CollectCheckpointReferences(c); err == nil {
		t.Fatal("typed nil rule accepted")
	}
	s = NewServiceWithProjectileCapacity(2)
	for _, bad := range []*CheckpointContext{nil, {}, NewCheckpointContext(c.Units, nil), NewCheckpointContext(units.NewCheckpointContext(nil), c.World)} {
		if _, err := s.CollectCheckpointReferences(bad); err == nil {
			t.Fatal("missing context accepted")
		}
	}
	if _, err := (*Service)(nil).CollectCheckpointReferences(c); err == nil {
		t.Fatal("nil receiver accepted")
	}
	if err := s.WriteCheckpoint(nil, c); err == nil {
		t.Fatal("nil encoder accepted")
	}
}

// Allocation addresses are discovery keys only: equal graph shapes encode
// equally after relocation, while distinct allocations sharing a handle remain
// distinct retained edges. Combat does not serialize the allocation bodies.
func TestCheckpointCombatAllocationAliases(t *testing.T) {
	makeService := func(a, b *units.Unit) *Service {
		s := NewServiceWithProjectileCapacity(1)
		s.TransportDeaths.passengers = []transportDeathPassenger{{unit: a, handle: 7}, {unit: b, handle: 7}, {unit: a, handle: 7}}
		s.deathNotified = map[pool.Handle]*units.Unit{7: b}
		return s
	}
	a, b := &units.Unit{Handle: 7, AllocationSerial: 1}, &units.Unit{Handle: 7, AllocationSerial: 2}
	original := combatCheckpointBytes(t, makeService(a, b), combatCheckpointContext(t))
	relocated := combatCheckpointBytes(t, makeService(&units.Unit{Handle: 7, AllocationSerial: 1}, &units.Unit{Handle: 7, AllocationSerial: 2}), combatCheckpointContext(t))
	if !bytes.Equal(original, relocated) {
		t.Fatal("allocation addresses entered bytes")
	}
	aliased := combatCheckpointBytes(t, makeService(a, a), combatCheckpointContext(t))
	if bytes.Equal(original, aliased) {
		t.Fatal("allocation alias shape vanished")
	}
	s := makeService(a, b)
	before := combatCheckpointBytes(t, s, combatCheckpointContext(t))
	s.TransportDeaths.passengers[0], s.TransportDeaths.passengers[1] = s.TransportDeaths.passengers[1], s.TransportDeaths.passengers[0]
	if bytes.Equal(before, combatCheckpointBytes(t, s, combatCheckpointContext(t))) {
		t.Fatal("transport capture order vanished")
	}
}
