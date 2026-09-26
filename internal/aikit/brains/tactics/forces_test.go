package tactics

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func weapon(dmg, reload int32, f func(w *content.WeaponDef)) *content.WeaponDef {
	w := &content.WeaponDef{ID: 1, DamageDefault: dmg, ReloadTime: reload, Range: 400}
	if f != nil {
		f(w)
	}
	return w
}

func info(def *content.UnitDef, role aikit.Role, dps, airDPS int32) *aikit.UnitInfo {
	return &aikit.UnitInfo{Def: def, Role: role | aikit.RoleMobile, DPS: dps, AirDPS: airDPS, HP: 300, Value: 150, Speed: 200}
}

// Unit kinds come from authored flags: a dropped weapon makes a bomber even
// when its damage table credits anti-air, torpedoes alone a torpedo plane,
// and a walker whose water depth reaches the sea level is amphibious on
// that map (the sea floor is never below height zero).
func TestClassOf(t *testing.T) {
	const sea = 85
	bomb := weapon(100, 90, func(w *content.WeaponDef) { w.Dropped = true })
	gun := weapon(20, 30, nil)
	aa := weapon(20, 30, func(w *content.WeaponDef) { w.ToAirWeapon = true })
	torp := weapon(200, 90, func(w *content.WeaponDef) { w.WaterWeapon = true })
	for _, c := range []struct {
		name string
		u    *aikit.UnitInfo
		want uint8
	}{
		{"fighter", info(&content.UnitDef{CanFly: true, Weapon1Def: aa}, aikit.RoleAir|aikit.RoleCombat, 20, 20), ukFighter},
		{"bomber with anti-air credit", info(&content.UnitDef{CanFly: true, Weapon1Def: bomb}, aikit.RoleAir|aikit.RoleCombat, 80, 80), ukBomber},
		{"gunship", info(&content.UnitDef{CanFly: true, Weapon1Def: gun}, aikit.RoleAir|aikit.RoleCombat, 20, 0), ukGunship},
		{"torpedo plane", info(&content.UnitDef{CanFly: true, Weapon1Def: torp}, aikit.RoleAir|aikit.RoleCombat, 66, 0), ukTorpedo},
		{"air scout", info(&content.UnitDef{CanFly: true}, aikit.RoleAir|aikit.RoleScout, 0, 0), ukAirScout},
		{"air constructor", info(&content.UnitDef{CanFly: true}, aikit.RoleAir|aikit.RoleBuilder, 0, 0), ukOther},
		{"ship", info(&content.UnitDef{MinWaterDepth: 3, MaxWaterDepth: 10000, Weapon1Def: gun}, aikit.RoleNaval|aikit.RoleCombat, 20, 0), ukNaval},
		{"hovercraft", info(&content.UnitDef{CanHover: true, MaxWaterDepth: 10000, MinWaterDepth: -10000, Weapon1Def: gun}, aikit.RoleHover|aikit.RoleCombat, 20, 0), ukHover},
		{"amphibious tank", info(&content.UnitDef{MaxWaterDepth: 100, MinWaterDepth: -10000, Weapon1Def: gun}, aikit.RoleCombat, 20, 0), ukAmphib},
		{"tank", info(&content.UnitDef{MaxWaterDepth: 12, MinWaterDepth: -10000, Weapon1Def: gun}, aikit.RoleCombat, 20, 0), ukGround},
	} {
		if got := classOf(c.u, sea).kind; got != c.want {
			t.Errorf("%s: kind %s, want %s", c.name, kindNames[got], kindNames[c.want])
		}
	}
	// A submarine cannot shell a coast.
	sub := classOf(info(&content.UnitDef{MinWaterDepth: 15, MaxWaterDepth: 10000, Weapon1Def: torp}, aikit.RoleNaval|aikit.RoleCombat, 66, 0), sea)
	if sub.surfDPS != 0 || !sub.torp {
		t.Errorf("submarine surface dps %d torpedo %v, want 0 true", sub.surfDPS, sub.torp)
	}
}

func testArmy(w, h int32) (*Army, *aikit.MapInfo) {
	m := &aikit.MapInfo{SectorW: w, SectorH: h, WorldW: w * aikit.SectorWorld, WorldH: h * aikit.SectorWorld}
	a := &Army{P: DefaultParams()}
	n := w * h
	a.water = make([]uint8, n)
	a.wdist = make([]uint8, n)
	a.bfs = make([]int32, 0, n)
	a.zoneW = (w + zoneSectors - 1) / zoneSectors
	a.zoneH = (h + zoneSectors - 1) / zoneSectors
	a.zones = make([]zone, a.zoneW*a.zoneH)
	a.aa = aikit.NewGrid(m)
	a.aaMem = aikit.NewGrid(m)
	a.aaMemK = make([]int64, len(a.aaMem.V))
	a.seaHurt = aikit.NewGrid(m)
	a.seaHurtK = make([]int64, len(a.seaHurt.V))
	a.goals = make([]goalRec, 0, maxGoals)
	return a, m
}

// Water knowledge spreads as a sector distance: a land target is coastal
// (hittable by ships) within navalReach of known water, and ships fight
// from the nearest known water sector.
func TestWaterDistance(t *testing.T) {
	a, m := testArmy(20, 10)
	a.markWater(m, 2*aikit.SectorWorld+5, 5*aikit.SectorWorld+5)
	a.updateWaterDist(m)
	if d := a.wdist[m.Sector(6*aikit.SectorWorld, 5*aikit.SectorWorld)]; d != 4 {
		t.Errorf("distance four sectors east = %d, want 4", d)
	}
	near := &aikit.Remembered{Info: &aikit.UnitInfo{}, Building: true, X: 5*aikit.SectorWorld + 5, Z: 5*aikit.SectorWorld + 5}
	far := &aikit.Remembered{Info: &aikit.UnitInfo{}, Building: true, X: 9*aikit.SectorWorld + 5, Z: 5*aikit.SectorWorld + 5}
	a.classes = []uclass{{}}
	if !a.navalHittable(m, near) || a.navalHittable(m, far) {
		t.Errorf("hittable: near %v far %v, want true false", a.navalHittable(m, near), a.navalHittable(m, far))
	}
	x, z, ok := a.waterNear(m, near.X, near.Z)
	if !ok || m.Sector(x, z) != m.Sector(2*aikit.SectorWorld, 5*aikit.SectorWorld) {
		t.Errorf("water access (%d,%d) ok=%v", x, z, ok)
	}
	if _, _, ok := a.waterNear(m, far.X, far.Z); ok {
		t.Errorf("water access found beyond the search radius")
	}
}

// Only the part of the remembered enemy fleet that is out of sight is
// added at a naval target, fully near where it was seen, half elsewhere.
func TestFleetReserve(t *testing.T) {
	a, _ := testArmy(40, 40)
	ship := &aikit.UnitInfo{DPS: 100, HP: 3000, Range: 600, Value: 1000}
	a.fleetMem.add(ship, 3000, 1000)
	a.fleetMem.add(ship, 3000, 1000)
	a.enemyFleet.add(ship, 3000, 1000) // one of the two is in sight
	a.fleetX, a.fleetZ = 1000, 1000
	b := &core.Board{EnemyX: 4000, EnemyZ: 4000}
	var near, far force
	a.fleetReserve(b, 1200, 1200, &near)
	a.fleetReserve(b, 4000, 1000, &far)
	if near.dps != 100 || near.hp != 3000 {
		t.Errorf("near reserve dps %d hp %d, want 100 3000", near.dps, near.hp)
	}
	if far.dps != 50 || far.hp != 1500 {
		t.Errorf("far reserve dps %d hp %d, want 50 1500", far.dps, far.hp)
	}
}

// A strike picks the undefended economy over a richer target under
// anti-air, and never flies a sortie expected to lose most of the wing.
func TestStrikeTarget(t *testing.T) {
	a, m := testArmy(40, 40)
	mex := &aikit.UnitInfo{Index: 0, Role: aikit.RoleExtractor, Value: 100, HP: 200}
	fac := &aikit.UnitInfo{Index: 1, Role: aikit.RoleFactory, Value: 900, HP: 400}
	a.classes = []uclass{{}, {}}
	o := &aikit.Obs{Tick: 1000, Memory: []aikit.Remembered{
		{H: 1, Info: mex, X: 3000, Z: 3000, LastSeen: 900, Building: true},
		{H: 2, Info: fac, X: 500, Z: 2500, LastSeen: 900, Building: true},
	}}
	b := &core.Board{O: o, Tick: 1000}
	// Heavy anti-air over the factory only (off the extractor's flight line).
	a.aa.AddDisc(500, 2500, 400, 400)
	s := &a.sq[sqStrike]
	s.id = sqStrike
	w := wing{n: 3, value: 660, hp: 960, pass: 540, speed: 240}
	if !a.strikeTarget(b, s, &w, 500, 500, 0) || s.tgtH != 1 {
		t.Fatalf("chose %d, want the undefended extractor (1)", s.tgtH)
	}
	// Anti-air everywhere: no sortie.
	a.aa.AddDisc(3000, 3000, 400, 400)
	s.tgtH = 0
	if a.strikeTarget(b, s, &w, 500, 500, 0) {
		t.Errorf("flew into anti-air at both targets (chose %d)", s.tgtH)
	}
	_ = m
}

// islandMap is two flat islands (height 60) on a sea (level 50, floor 10):
// x 4..19 and x 44..59 cells, rows 4..23, on a 64×32-cell map.
func islandMap() *aikit.MapInfo {
	w, h := 64, 32
	attrs := make([]formats.TNTAttribute, w*h)
	for z := 0; z < h; z++ {
		for x := 0; x < w; x++ {
			v := uint8(10)
			if z >= 4 && z < 24 && ((x >= 4 && x < 20) || (x >= 44 && x < 60)) {
				v = 60
			}
			attrs[z*w+x] = formats.TNTAttribute{Height: v, Feature: world.PlotFeatureNone}
		}
	}
	ter := &world.Terrain{CellW: int32(w), CellH: int32(h), SeaLevel: 50, Plot: world.ExpandPlot(attrs, w, h)}
	return aikit.AnalyzeMap(ter, [][2]int32{{12 * 16, 12 * 16}, {52 * 16, 12 * 16}}, 2, 2, 12*16, 12*16)
}

// Squad goals must be reachable for the members' movement class: a tank on
// the west island reaches its own island and shoots across a strait only
// as far as its range; a ship reaches both coasts.
func TestReachTables(t *testing.T) {
	m := islandMap()
	gun := weapon(20, 30, nil)
	tank := &aikit.UnitInfo{Index: 0, Side: "ARM", Role: aikit.RoleCombat | aikit.RoleMobile, FootX: 2, FootZ: 2, Range: 200, Value: 100,
		Def: &content.UnitDef{MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 10, MaxWaterSlope: 255, Weapon1Def: gun}}
	ship := &aikit.UnitInfo{Index: 1, Side: "ARM", Role: aikit.RoleCombat | aikit.RoleMobile | aikit.RoleNaval, FootX: 3, FootZ: 3, Range: 600, Value: 300,
		Def: &content.UnitDef{MaxWaterDepth: 10000, MinWaterDepth: 3, MaxSlope: 255, MaxWaterSlope: 255, Weapon1Def: gun}}
	k := &aikit.Kit{Side: "ARM", Table: &aikit.Table{Units: []*aikit.UnitInfo{tank, ship}}, Map: m}
	a := &Army{P: DefaultParams()}
	a.setupReach(k)
	if !a.reachReady || a.defCls[0] < 0 || a.defCls[1] < 0 || a.defCls[0] == a.defCls[1] {
		t.Fatalf("classes %v ready %v", a.defCls, a.reachReady)
	}
	tc := a.defCls[0]
	home := uint16(a.rcls[tc].r.At(12*16, 12*16))
	east := uint16(a.rcls[tc].r.At(52*16, 12*16))
	if home == 0 || east == 0 || home == east {
		t.Fatalf("tank regions home %d east %d", home, east)
	}
	if !a.regionNear(m, tc, home, 8*16, 20*16, 200) {
		t.Error("tank cannot reach its own island")
	}
	if a.regionNear(m, tc, home, 52*16, 12*16, 232) {
		t.Error("tank reaches the far island's centre across the sea")
	}
	// The east coast (x 44) is 24 cells (384 wu) of sea from the west coast
	// (x 20): out of a 200 wu gun's reach, within a 600 wu one.
	if a.regionNear(m, tc, home, 45*16, 12*16, 232) || !a.regionNear(m, tc, home, 45*16, 12*16, 632) {
		t.Error("strait reach wrong")
	}
	s := &squad{id: sqMain}
	s.addGroup(tc, home, 100, 200)
	b := &core.Board{K: k}
	if a.reachShare(b, s, 52*16, 12*16, 32) != 0 || a.reachShare(b, s, 10*16, 10*16, 32) != 1000 {
		t.Error("squad reach share wrong")
	}
	sc := a.defCls[1]
	sea := uint16(a.rcls[sc].r.At(32*16, 12*16))
	if sea == 0 || !a.regionNear(m, sc, sea, 52*16, 12*16, 632) || !a.regionNear(m, sc, sea, 12*16, 12*16, 632) {
		t.Error("ship cannot reach both coasts")
	}
}

// The sector tables are filled on first ask (none at Init) and hold what
// the eager table held: the two nearest regions around each sector centre.
func TestReachTablesLazy(t *testing.T) {
	m := islandMap()
	gun := weapon(20, 30, nil)
	tank := &aikit.UnitInfo{Index: 0, Side: "ARM", Role: aikit.RoleCombat | aikit.RoleMobile, FootX: 2, FootZ: 2, Range: 200, Value: 100,
		Def: &content.UnitDef{MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 10, MaxWaterSlope: 255, Weapon1Def: gun}}
	ship := &aikit.UnitInfo{Index: 1, Side: "ARM", Role: aikit.RoleCombat | aikit.RoleMobile | aikit.RoleNaval, FootX: 3, FootZ: 3, Range: 600, Value: 300,
		Def: &content.UnitDef{MaxWaterDepth: 10000, MinWaterDepth: 3, MaxSlope: 255, MaxWaterSlope: 255, Weapon1Def: gun}}
	k := &aikit.Kit{Side: "ARM", Table: &aikit.Table{Units: []*aikit.UnitInfo{tank, ship}}, Map: m}
	a := &Army{P: DefaultParams()}
	a.setupReach(k)
	for c := range a.rcls {
		for _, w := range a.rcls[c].done {
			if w != 0 {
				t.Fatal("sector tables filled at Init")
			}
		}
	}
	for c := range a.rcls {
		for s := int32(0); s < m.SectorW*m.SectorH; s++ {
			x, z := m.SectorCentre(s)
			r1, r2 := a.rcls[c].r.Near2(x, z, aikit.SectorWorld/2+16)
			if g1, g2 := a.sectorRegions(int8(c), s); g1 != uint16(r1) || g2 != uint16(r2) {
				t.Fatalf("class %d sector %d: %d %d, want %d %d", c, s, g1, g2, r1, r2)
			}
		}
	}
}
