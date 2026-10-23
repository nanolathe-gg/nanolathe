package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/film"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// These are settings composition roles, not gameplay classes. Preferred stock
// names keep the measured stock shots; replacements use authored capabilities
// and SIDEDATA (DESIGN_INTERFACE_HUD_INPUT §3.17), never a mod-name table.
type nlUnitRole uint8

const (
	nlLand nlUnitRole = iota
	nlKbot
	nlHover
	nlShip
	nlSubmarine
	nlAircraft
	nlEmissiveMobile
	nlEmissiveTower
	nlTower
	nlBattery
	nlConstructor
	nlFactory
	nlPower
	nlMetalStore
	nlEnergyStore
	nlRadar
	nlExtractor
	nlWaterMetalStore
	nlWaterEnergyStore
	nlWaterExtractor
	nlWaterTower
	nlSonar
	nlCommander
	nlMetalMaker
)

func nlRole(name string) nlUnitRole {
	switch content.CanonicalKey(name) {
	case "armcom", "corcom":
		return nlCommander
	case "armck", "armcv", "armfark", "armnanotc", "corck", "corcv":
		return nlConstructor
	case "armlab", "armvp", "corlab", "corvp":
		return nlFactory
	case "armsolar", "armwin", "armfus", "corsolar", "corfus":
		return nlPower
	case "armmstor", "cormstor":
		return nlMetalStore
	case "armestor", "corestor":
		return nlEnergyStore
	case "armrad", "armarad", "armmark", "corrad", "corarad":
		return nlRadar
	case "armmex", "armmoho", "cormex":
		return nlExtractor
	case "armmakr", "armmmkr", "cormakr", "cormmkr":
		return nlMetalMaker
	case "armuwms":
		return nlWaterMetalStore
	case "armuwes":
		return nlWaterEnergyStore
	case "armuwmex":
		return nlWaterExtractor
	case "armtl":
		return nlWaterTower
	case "armsonar":
		return nlSonar
	case "armllt", "armhlt", "armanni", "corllt", "corhlt":
		return nlEmissiveTower
	case "armguard", "corpun", "armbrtha", "armsilo", "corsilo":
		return nlBattery
	case "armzeus", "armfav", "corpyro", "corfav", "corak":
		return nlEmissiveMobile
	case "armpw", "armrock", "armham", "armwar", "armjeth", "armmav", "armsnipe", "armaak", "armflea", "armspid", "armvader", "armspy", "corstorm", "corthud", "corcan", "corcrash", "corkrog":
		return nlKbot
	case "armanac", "armah", "corah", "armmh", "corsnap":
		return nlHover
	case "armfig", "armthund", "armhawk", "armpnix", "armbrawl", "armpeep", "corveng", "corshad", "corvamp", "corhurc", "corape", "corfink":
		return nlAircraft
	case "armbats", "armcrus", "armroy", "armpt", "armship", "corbats", "corcrus", "corroy", "corpt":
		return nlShip
	case "armsub", "armsubk", "corsub", "corshark":
		return nlSubmarine
	default:
		return nlLand
	}
}

type nlRoster struct {
	cat          *content.Catalog
	factions     [2]string
	keys         []string
	resolved     map[string]string
	selected     map[string]bool
	visible      map[string]bool
	factionUnits map[string]map[string]bool
}

func newNLRoster(cat *content.Catalog, sess *session.Session) *nlRoster {
	r := &nlRoster{cat: cat, resolved: make(map[string]string), selected: make(map[string]bool), visible: make(map[string]bool), factionUnits: make(map[string]map[string]bool)}
	if cat == nil {
		return r
	}
	r.keys = cat.SortedUnitKeys()
	for _, sd := range cat.Sides {
		if sd == nil {
			continue
		}
		members := make(map[string]bool)
		r.factionUnits[content.CanonicalKey(sd.Name)] = members
		pending := []string{content.CanonicalKey(sd.Commander)}
		for len(pending) > 0 {
			name := pending[0]
			pending = pending[1:]
			if members[name] {
				continue
			}
			members[name] = true
			if menu := cat.BuildMenus[name]; menu != nil {
				products := menu.Buttons
				if menu.AuthoredButtons != nil {
					products = menu.AuthoredButtons
				}
				for _, product := range products {
					pending = append(pending, content.CanonicalKey(product))
				}
			}
			pending = append(pending, nlVisibleProducts(cat, name)...)
		}
		pending = []string{content.CanonicalKey(sd.Commander)}
		root := pending[0]
		for len(pending) > 0 {
			name := pending[0]
			pending = pending[1:]
			if r.visible[name] {
				continue
			}
			r.visible[name] = true
			pending = append(pending, nlVisibleProducts(cat, name)...)
			// Once an actual visible factory/builder is reached, its complete
			// authored membership is useful faction evidence even when the
			// products' GUI slots are authored in a physical page.
			if name != root {
				if menu := cat.BuildMenus[name]; menu != nil {
					products := menu.Buttons
					if menu.AuthoredButtons != nil {
						products = menu.AuthoredButtons
					}
					for _, product := range products {
						pending = append(pending, content.CanonicalKey(product))
					}
				}
			}
		}
	}
	for side := range r.factions {
		index := side
		if sess != nil && sess.Econ != nil {
			owner := sess.LocalOwner
			if side == 1 {
				owner = sess.EnemyOwner
			}
			if int(owner) < len(sess.Econ.Players) {
				index = int(sess.Econ.Players[owner].Side)
			}
		}
		if len(cat.Sides) > 0 {
			index %= len(cat.Sides)
			if sd := cat.Sides[index]; sd != nil {
				r.factions[side] = sd.Name
			}
		}
	}
	return r
}

func nlWeapon(u *content.UnitDef, accept func(*content.WeaponDef) bool) bool {
	for _, w := range []*content.WeaponDef{u.Weapon1Def, u.Weapon2Def, u.Weapon3Def} {
		if !content.IsWeaponInactive(w) && !w.Stockpile && !w.Interceptor && uint16(w.DamageDefault) > 0 && accept(w) {
			return true
		}
	}
	return false
}

func nlEmissive(w *content.WeaponDef) bool {
	return w.RenderType == render.RenderTypeBeam || w.RenderType == render.RenderTypeSegmented || w.RenderType == render.RenderTypeLifetimeGAF || w.BeamWeapon
}

func (r *nlRoster) fits(u *content.UnitDef, role nlUnitRole) bool {
	if u == nil || u.IsFeature || u.DiscoveryOnly || u.FootprintX <= 0 || u.FootprintZ <= 0 {
		return false
	}
	if role == nlCommander {
		return u.Commander
	}
	if u.Commander {
		return false
	}
	rules, err := world.PlacementRulesForUnit(r.cat, u)
	if err != nil {
		return false
	}
	wet := rules.MinWaterDepth > 0
	mobile := u.BMCode != 0 && u.CanMove && u.MaxVelocity > 0
	armed := nlWeapon(u, func(w *content.WeaponDef) bool { return !w.ToAirWeapon && !w.WaterWeapon })
	land := mobile && !u.CanFly && !wet && !u.Builder && armed
	structure := u.BMCode == 0 && !wet
	switch role {
	case nlLand:
		return land && !u.Upright
	case nlKbot:
		return land && u.Upright
	case nlHover:
		return mobile && !u.CanFly && !wet && !u.Builder && u.CanHover && nlWeapon(u, func(*content.WeaponDef) bool { return true })
	case nlShip:
		return mobile && !u.CanFly && wet && u.Floater && !u.Builder && nlWeapon(u, func(w *content.WeaponDef) bool { return !w.ToAirWeapon })
	case nlSubmarine:
		return mobile && !u.CanFly && wet && !u.Floater && !u.Builder && nlWeapon(u, func(w *content.WeaponDef) bool { return w.WaterWeapon })
	case nlAircraft:
		return mobile && u.CanFly && !u.Builder && nlWeapon(u, func(*content.WeaponDef) bool { return true })
	case nlEmissiveMobile:
		return mobile && !u.CanFly && !wet && !u.Builder && nlWeapon(u, nlEmissive)
	case nlEmissiveTower:
		return structure && !u.Builder && nlWeapon(u, func(w *content.WeaponDef) bool { return w.Turret && !w.ToAirWeapon && !w.WaterWeapon && nlEmissive(w) })
	case nlTower:
		return structure && !u.Builder && nlWeapon(u, func(w *content.WeaponDef) bool { return w.Turret && !w.ToAirWeapon && !w.WaterWeapon })
	case nlBattery:
		return structure && !u.Builder && nlWeapon(u, func(w *content.WeaponDef) bool {
			return w.Ballistic && !w.ToAirWeapon && !w.WaterWeapon && uint16(w.AreaOfEffect) > 16 && w.Range > 0
		})
	case nlConstructor:
		return mobile && !u.CanFly && !wet && u.Builder && uint16(u.WorkerTime) > 0 && len(r.products(u)) > 0
	case nlFactory:
		return structure && u.Builder && len(r.products(u)) > 0
	case nlPower:
		return structure && (u.EnergyMake > u.EnergyUse || u.EnergyUse < 0 || u.WindGenerator > 0)
	case nlMetalStore, nlWaterMetalStore:
		return u.BMCode == 0 && wet == (role == nlWaterMetalStore) && u.MetalStorage > 0
	case nlEnergyStore, nlWaterEnergyStore:
		return u.BMCode == 0 && wet == (role == nlWaterEnergyStore) && u.EnergyStorage > 0
	case nlRadar:
		return structure && u.RadarDistance > 0
	case nlExtractor, nlWaterExtractor:
		return u.BMCode == 0 && wet == (role == nlWaterExtractor) && u.ExtractsMetal > 0
	case nlWaterTower:
		return u.BMCode == 0 && wet && nlWeapon(u, func(w *content.WeaponDef) bool { return w.WaterWeapon })
	case nlSonar:
		return u.BMCode == 0 && wet && u.SonarDistance > 0
	case nlMetalMaker:
		return structure && content.SmartMakerCandidate(u)
	}
	return false
}

// products uses complete authored membership before rule admission, so compared
// scenes keep the same requested products. Ordinary construction still admits
// them under the bound rule set. A preview never gives a constructor
// an arbitrary same-faction building (research/extensions/build-menus.md
// "Repeated membership records without placement keys").
func (r *nlRoster) products(builder *content.UnitDef) []string {
	if r.cat == nil || builder == nil {
		return nil
	}
	menu := r.cat.BuildMenus[content.CanonicalKey(builder.UnitName)]
	if menu == nil {
		return nil
	}
	products := menu.AuthoredButtons
	if products == nil {
		products = menu.Buttons
	}
	out := []string{}
	for _, name := range products {
		u, ok := r.cat.Unit(name)
		if ok && u != nil && !u.IsFeature && !u.DiscoveryOnly {
			out = append(out, content.CanonicalKey(name))
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// Compact footprints keep the measured camera compositions useful. Canonical
// ties are stable and do not consume either simulation RNG.
func (r *nlRoster) pick(preferred string, side int, role nlUnitRole, allowed []string) string {
	if r.cat == nil {
		return ""
	}
	candidates := r.keys
	if allowed != nil {
		candidates = allowed
	}
	fits := func(name string) bool {
		u, ok := r.cat.Unit(name)
		return ok && r.inFaction(u, side) && r.fits(u, role)
	}
	pref := content.CanonicalKey(preferred)
	if (allowed == nil || slices.Contains(allowed, pref)) && fits(pref) {
		return pref
	}
	best, score := "", int64(1<<62)
	for _, name := range candidates {
		if !fits(name) {
			continue
		}
		u, _ := r.cat.Unit(name)
		s := int64(u.FootprintX)*int64(u.FootprintZ)*65536 + int64(max(u.BuildCostMetal, 0))
		if role == nlBattery && nlWeapon(u, func(w *content.WeaponDef) bool { return w.Ballistic && w.Accuracy == 0 && w.SprayAngle == 0 }) {
			s -= 1 << 50
		}
		if r.visible[content.CanonicalKey(name)] {
			s -= 1 << 45
		}
		if role == nlConstructor && !nlWeapon(u, func(*content.WeaponDef) bool { return true }) {
			s -= 1 << 44
		}
		if s < score {
			best, score = content.CanonicalKey(name), s
		}
	}
	return best
}

func (r *nlRoster) resolve(preferred string) string {
	key := content.CanonicalKey(preferred)
	if value, ok := r.resolved[key]; ok {
		return value
	}
	if r.selected[key] {
		return key
	}
	if r.cat == nil {
		return ""
	}
	// Names already supplied by a builder's menu are catalog identities.
	if !strings.HasPrefix(key, "arm") && !strings.HasPrefix(key, "cor") {
		if _, ok := r.cat.Unit(key); ok {
			return key
		}
	}
	side := 0
	if strings.HasPrefix(key, "cor") {
		side = 1
	}
	role := nlRole(key)
	name := r.pick(key, side, role, nil)
	if name == "" {
		switch role {
		case nlKbot, nlHover, nlEmissiveMobile:
			name = r.pick("", side, nlLand, nil)
		case nlEmissiveTower:
			name = r.pick("", side, nlTower, nil)
		}
	}
	r.resolved[key] = name
	return name
}

func (st *nlStage) requireUnit(name string) string {
	if name != "" {
		name = content.CanonicalKey(name)
		if st.roster != nil {
			st.roster.selected[name] = true
		}
		if !slices.Contains(st.required, name) {
			st.required = append(st.required, name)
		}
	}
	return name
}

func (st *nlStage) name(preferred string) string {
	if st.roster == nil {
		return st.requireUnit(preferred)
	}
	name := st.roster.resolve(preferred)
	if name == "" || !st.roster.selected[content.CanonicalKey(preferred)] && !st.roster.fits(st.roster.cat.Units[name], nlRole(preferred)) {
		side := 0
		if strings.HasPrefix(content.CanonicalKey(preferred), "cor") {
			side = 1
		}
		message := fmt.Sprintf("The %s preview faction has no %s for this preview.", st.roster.factions[side], nlRoleLabel(nlRole(preferred)))
		if name != "" {
			message = fmt.Sprintf("The %s preview faction has no %s; compatible units are shown.", st.roster.factions[side], nlRoleLabel(nlRole(preferred)))
		}
		st.limit(message)
	}
	return st.requireUnit(name)
}

func (st *nlStage) names(preferred []string) []string {
	out := make([]string, 0, len(preferred))
	for _, name := range preferred {
		if resolved := st.name(name); resolved != "" {
			out = append(out, resolved)
		}
	}
	return out
}

func nlRoleLabel(role nlUnitRole) string {
	return [...]string{"ground combat vehicles", "upright combat units", "armed hovercraft", "surface warships", "armed submarines", "combat aircraft", "mobile beam or lightning weapons", "beam or lightning towers", "ground weapon towers", "ballistic splash artillery", "mobile constructors with build products", "factories with build products", "power structures", "metal storage", "energy storage", "radar structures", "metal extractors", "underwater metal storage", "underwater energy storage", "underwater extractors", "underwater weapon towers", "sonar structures", "commanders", "metal makers"}[role]
}

func (st *nlStage) limit(message string) {
	if !slices.Contains(st.limits, message) {
		st.limits = append(st.limits, message)
	}
}
func (st *nlStage) previewLimit() string      { return strings.Join(st.limits, " ") }
func (st *nlStage) placementUnitName() string { return st.placement }

func (st *nlStage) buildProduct(builder *content.UnitDef, preferred string) string {
	if st.roster == nil {
		return st.requireUnit(preferred)
	}
	products := st.roster.products(builder)
	if builder == nil || len(products) == 0 {
		st.limit("This content has no compatible authored construction product at this worksite.")
		return ""
	}
	name := st.roster.pick(preferred, 0, nlRole(preferred), products)
	if name == "" {
		for _, product := range products {
			pd, _ := st.roster.cat.Unit(product)
			p, err := world.PlacementRulesForUnit(st.roster.cat, pd)
			if err == nil && p.MinWaterDepth <= 0 && pd.BMCode == 0 && pd.BuildTime > 0 && pd.ExtractsMetal == 0 && !strings.Contains(strings.ToLower(pd.YardMap), "g") {
				name = product
				break
			}
		}
	}
	if name == "" {
		st.limit("This content has no compatible authored construction product at this worksite.")
	}
	return st.requireUnit(name)
}

// stageNLPreviewFixture owns settings composition separately from the pinned
// film fixture. authored precedes rule preparation and mutators, keeping both
// halves of a compare on the same roster (DESIGN_INTERFACE_HUD_INPUT §3.17).
func stageNLPreviewFixture(preset nlPreset, sess *session.Session, authored *content.Catalog) (*nlStage, error) {
	if authored == nil {
		authored = sess.Catalog
	}
	st := &nlStage{s: sess, roster: newNLRoster(authored, sess)}
	for _, u := range sess.Units.Iter() {
		if u != nil && u.Alive && u.Def != nil {
			st.requireUnit(u.Def.UnitName)
		}
	}
	var err error
	if preset.scene.Kind == "skirmish" {
		st.cx, st.cz, err = filmSkirmishAnchor(sess)
		st.placement = st.requireUnit(st.roster.pick("armllt", 0, nlTower, nil))
		if st.placement == "" {
			st.limit("This content has no compatible ground weapon tower for the placement preview.")
		}
	} else {
		err = st.stageBattle(preset.scene)
	}
	if err != nil {
		return nil, err
	}
	if preset.stage != nil {
		preset.stage(st)
	}
	st.live = true
	return st, nil
}

// assetUnitNames includes declarations made before scheduling, queued products,
// existing commanders/schema units and the ghost. Hand-built stages retain full
// preparation because their future event closure is not established.
func (st *nlStage) assetUnitNames() []string {
	if st == nil || st.roster == nil {
		return nil
	}
	out := append([]string{}, st.required...)
	slices.Sort(out)
	return slices.Compact(out)
}

// stageBattle retains stock formation choreography, resolving its units through
// SIDEDATA and capabilities. Film/benchmark fixtures remain pinned.
func (st *nlStage) stageBattle(scene film.Scene) error {
	roster, err := filmRoster(scene.Roster)
	if err != nil {
		return err
	}
	st.cx, st.cz, _, err = filmBattleCentre(scene, st.s.World)
	if err != nil {
		return err
	}
	columns, pitch, gap := max(scene.Columns, 1), int32(scene.Pitch), int32(scene.Gap)
	if scene.Columns <= 0 {
		columns = 8
	}
	if pitch <= 0 {
		pitch = 48
	}
	if gap <= 0 {
		gap = 320
	}
	wasLive := st.live
	st.live = true // the film formation tests ground, not reservations between ranks
	defer func() { st.live = wasLive }()
	for side := range roster {
		var names []string
		if scene.PerSide > 0 {
			names = st.names(roster[side])
		}
		if len(names) == 0 && scene.PerSide > 0 {
			for _, role := range []nlUnitRole{nlLand, nlKbot} {
				if name := st.roster.pick("", side, role, nil); name != "" {
					names = []string{st.requireUnit(name)}
					break
				}
			}
		}
		owner, facing := st.s.LocalOwner, int32(-1)
		if side == 1 {
			owner, facing = st.s.EnemyOwner, 1
		}
		rows := int32((scene.PerSide + columns - 1) / columns)
		for i := 0; i < scene.PerSide && len(names) > 0; i++ {
			col, row := int32(i%columns), int32(i/columns)
			x := st.cx + facing*gap + facing*col*pitch + row%2*pitch/2*facing
			z := st.cz + row*pitch - rows/2*pitch + col%2*pitch/3
			u := st.airOrUnit(names[i%len(names)], owner, x, z, 0)
			if u != nil {
				code, goal := 2, st.cx-facing*gap/2
				if u.Def.CanFly {
					code, goal = 9, st.cx-facing*(gap+300)
				} else if scene.Patrol {
					code = 9
				}
				filmOrder(st.s, u, code, nlFixed(goal), nlFixed(z))
			}
		}
		if scene.Air > 0 {
			air := st.names(filmAircraft[side])
			for i := 0; i < scene.Air && len(air) > 0; i++ {
				k := int32(i)
				x, z := st.cx+facing*(gap+150+k*137%650), st.cz+k*211%760-380
				if u := st.airOrUnit(air[i%len(air)], owner, x, z, 0); u != nil {
					filmOrder(st.s, u, 9, nlFixed(st.cx-facing*(gap+250+k*53%400)), nlFixed(z+k*97%240-120))
				}
			}
		}
		if scene.Buildings > 0 || scene.Builders > 0 || scene.Factories {
			return fmt.Errorf("nanolathe: preview composition failed: logical path <settings scene>, providers searched [authored catalog], expected a fixture without rear production")
		}
	}
	return nil
}

// Aircraft use the ordinary airborne creator and authored cruise altitude
// [04 §10.1], just as the film fixture does.
func (st *nlStage) airOrUnit(name string, owner uint8, x, z, rings int32) *units.Unit {
	def, ok := st.s.Catalog.Unit(name)
	if !ok || def == nil {
		return nil
	}
	st.requireUnit(name)
	if !def.CanFly {
		return st.unit(name, owner, x, z, rings)
	}
	fx, fz := nlFixed(x), nlFixed(z)
	h, err := st.s.Units.CreateWithMoverMode(def, owner, fx, movement.CruiseAltitudeForOffset(st.s.World, fx, fz, def.CruiseAlt), fz, 2)
	if err != nil {
		return nil
	}
	u := st.s.Units.Unit(h)
	if st.s.Movement != nil {
		st.s.Movement.EnsureUnit(u)
	}
	st.s.BindStagedOrderQueue(u)
	return u
}

func nlBatteryWeapon(u *content.UnitDef) *content.WeaponDef {
	if u == nil {
		return nil
	}
	for _, w := range []*content.WeaponDef{u.Weapon1Def, u.Weapon2Def, u.Weapon3Def} {
		if !content.IsWeaponInactive(w) && w.Ballistic && !w.Stockpile && !w.ToAirWeapon && !w.WaterWeapon && uint16(w.AreaOfEffect) > 16 && uint16(w.DamageDefault) > 0 {
			return w
		}
	}
	return nil
}

func (r *nlRoster) blastTarget(preferred string, weapon *content.WeaponDef) string {
	if weapon == nil {
		return ""
	}
	accept := func(u *content.UnitDef) bool {
		if !r.fits(u, nlLand) && !r.fits(u, nlKbot) {
			return false
		}
		damage := uint16(weapon.DamageDefault)
		if v, ok := weapon.DamageOverride(u.UnitName); ok {
			damage = uint16(v)
		}
		return u.MaxDamage > int32(damage)
	}
	if u, ok := r.cat.Unit(preferred); ok && r.inFaction(u, 1) && accept(u) {
		return content.CanonicalKey(preferred)
	}
	best, score := "", int64(1<<62)
	for _, name := range r.keys {
		u, _ := r.cat.Unit(name)
		if u == nil || !r.inFaction(u, 1) || !accept(u) {
			continue
		}
		s := int64(u.FootprintX)*int64(u.FootprintZ)*65536 + int64(u.MaxDamage)
		if s < score {
			best, score = name, s
		}
	}
	return best
}

func (st *nlStage) compatibleBattle() {
	// A visual-family gap falls back to actual mobile combat, not a blank frame.
	scene := film.Scene{Kind: "battle", Roster: "armor", PerSide: 24, Columns: 6, Gap: 180, Anchor: []int32{st.cx, st.cz}}
	if err := st.stageBattle(scene); err != nil {
		st.limit(err.Error())
	}
	st.sentinels()
}

// The base menu and download records with real visible slots form the human
// build tree. Repeated membership-only records are not visible choices
// (research/extensions/build-menus.md "Twelve authored product slots"). They
// remain valid authored products, but do not outrank the content's human roster.
func nlVisibleProducts(cat *content.Catalog, builder string) []string {
	var names []string
	if menu := cat.BuildMenus[content.CanonicalKey(builder)]; menu != nil {
		names = menu.BaseButtons()
	}
	for _, p := range cat.DownloadPlacements {
		if p.BuilderResolved && p.ProductResolved && p.VisiblePage() >= 1 && strings.EqualFold(p.Builder, builder) {
			names = append(names, p.Product)
		}
	}
	for i := range names {
		names[i] = content.CanonicalKey(names[i])
	}
	slices.Sort(names)
	return slices.Compact(names)
}

func (r *nlRoster) inFaction(u *content.UnitDef, side int) bool {
	if u == nil {
		return false
	}
	faction := r.factions[side]
	return faction == "" || strings.EqualFold(u.Side, faction) || r.factionUnits[content.CanonicalKey(faction)][content.CanonicalKey(u.UnitName)]
}

// Each row reserves a constructor and its west/east/south sites. Their authored
// half-footprints and the composition's gaps determine the row spacing; stock
// retains its measured 100-pixel pitch. Placement remains the ordinary validator
// [04 §6.2, §6.3], and builders still approach sites normally [05 R-WORK-01 §12].
func (st *nlStage) crewRows(builder *content.UnitDef, products []string) [3][2]int32 {
	pitch := int32(100)
	for _, name := range products {
		if pd, ok := st.s.Catalog.Unit(name); ok {
			pitch = max(pitch, builder.FootprintZ*8+pd.FootprintZ*24+18)
		}
	}
	if pitch == 100 {
		return nlCrewRows
	}
	pitch = (pitch + 15) / 16 * 16
	return [3][2]int32{{-10, 5 - pitch}, {10, 5}, {-10, 5 + pitch}}
}
