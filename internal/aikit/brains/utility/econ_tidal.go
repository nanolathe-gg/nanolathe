package utility

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
)

// Water layout (tidal_field switch, README §13.3).
//
// Every water building was requested at its product's anchor, the valid
// site nearest the naval base, which is where the shipyard stands: the
// executor then packed tidal generators, floating makers and storage
// around the yard (on coast to coast, 28 tidal generators ringed two
// shipyards), and the exit guard does not protect a shipyard (its units
// do not walk out). With the switch:
//
//   - The water economy is requested in fields away from the yards: sites
//     fieldDist from the naval base in eight directions, none within
//     fieldClear of the naval base or of another yard site, or in any yard
//     site's exit lane (factories release their units toward +Z), each
//     workable by every builder class that could work the product's own
//     anchor. Fields fill in blocks of fieldBlock, nearest home first, so
//     no field grows back to a yard.
//   - The first shipyard stands at the naval base as before; later ones
//     (tech 2 included) go to yard sites yardDist out on the same sea, at
//     least yardApart from each other and out of each other's lanes, so
//     yards do not wall each other in either; a ring site is built only by
//     the classes that reach it from home (the naval constructors).

const (
	fieldDist  = 440 // world units from the naval base to a field's centre
	fieldClear = 300 // no field site this close to a yard site
	fieldLane  = 520 // length of the kept exit lane in front of a yard (+Z)
	fieldBlock = 6   // buildings per field before the next one is used
	yardDist   = 760 // world units from the naval base to a later yard's site
	yardApart  = 400 // yard sites at least this far apart
	maxFields  = 8
)

// fieldDirs are the eight compass directions (×1000).
var fieldDirs = [maxFields][2]int32{{1000, 0}, {707, 707}, {0, 1000}, {-707, 707}, {-1000, 0}, {-707, -707}, {0, -1000}, {707, -707}}

// waterFields are one product's field (or yard) sites, nearest home first.
type waterFields struct {
	n    int32
	x, z [maxFields]int32
}

func (f *waterFields) add(x, z, hx, hz int32) {
	if f.n >= maxFields {
		return
	}
	dh := aikit.Dist2(x, z, hx, hz)
	pos := f.n
	for pos > 0 && aikit.Dist2(f.x[pos-1], f.z[pos-1], hx, hz) > dh {
		f.x[pos], f.z[pos] = f.x[pos-1], f.z[pos-1]
		pos--
	}
	f.x[pos], f.z[pos] = x, z
	f.n++
}

// inLane reports whether a footprint centred at (x, z) of half-size
// (hx, hz) world units lies in the +Z exit lane of a yard at (yx, yz).
func inLane(x, z, hx, hz, yx, yz, laneHalf int32) bool {
	return absI32(x-yx) <= laneHalf+hx && z+hz >= yz && z-hz <= yz+fieldLane
}

// setupWaterFields computes the yard sites and the field sites of every
// water economy building in our tree (setup, after analyzeTerrain).
func (s *shared) setupWaterFields(k *aikit.Kit, com *aikit.UnitInfo) {
	t := &s.terr
	m := k.Map
	if !t.ready || !t.navOK || com == nil || t.navCls < 0 {
		return
	}
	// The shipyard the naval base was chosen for (analyzeTerrain).
	var yard *aikit.UnitInfo
	for _, p := range com.Builds {
		if p.Role.Has(aikit.RoleFactory) && s.info[p.Index].water {
			yard = p
			break
		}
	}
	if yard == nil {
		return
	}
	laneHalf := yard.FootX*8 + 64
	nc := &t.cls[t.navCls]
	// Yard sites: the naval base, then the ring on the same sea.
	var yards waterFields
	yards.x[0], yards.z[0], yards.n = t.navX, t.navZ, 1
	lo, hi := waterBand(yard)
	yf := 8 * max32(yard.FootX, yard.FootZ)
	for _, d := range fieldDirs {
		x, z, ok := m.DepthSite(t.navX+d[0]*yardDist/1000, t.navZ+d[1]*yardDist/1000, yard.FootX, yard.FootZ, lo, hi, 255, 240, nc.r, nc.home)
		if !ok {
			continue
		}
		good := true
		for j := int32(0); j < yards.n && good; j++ {
			good = aikit.Dist2(x, z, yards.x[j], yards.z[j]) >= yardApart*yardApart &&
				!inLane(x, z, yf, yf, yards.x[j], yards.z[j], laneHalf) && !inLane(yards.x[j], yards.z[j], yf, yf, x, z, laneHalf)
		}
		// Workable from home by at least one class that can work the
		// yard's own anchor (usually the naval constructors; the
		// commander on the shore rarely reaches 760 wu out). canWorkSite
		// sends only those classes there.
		var cls uint64
		for c := range t.cls {
			rc := &t.cls[c]
			if c < 64 && rc.siteOK[yard.Index] && rc.r.Dist(rc.home, x, z, 96+yf) >= 0 {
				cls |= 1 << uint(c)
			}
		}
		if good && cls != 0 && yards.n < maxFields {
			yards.x[yards.n], yards.z[yards.n] = x, z
			s.yardCls[yards.n] = cls
			yards.n++
		}
	}
	s.yards = yards
	for i, u := range k.Table.Units {
		if t.inTree[i] && s.info[i].water && u.Role.Has(aikit.RoleFactory) {
			s.yardDefs = append(s.yardDefs, int32(i))
		}
	}
	s.fields = make([]waterFields, len(k.Table.Units))
	for i, u := range k.Table.Units {
		si := &s.info[i]
		if !t.inTree[i] || !si.water || !t.siteOK[i] || u.Role.Has(aikit.RoleMobile) ||
			!u.Role.Any(aikit.RoleEnergy|aikit.RoleMetalMaker|aikit.RoleStorage) || u.Role.Any(aikit.RoleFactory|aikit.RoleExtractor|aikit.RoleDefense) {
			continue
		}
		lo, hi := waterBand(u)
		reach := int32(96 + 8*max32(u.FootX, u.FootZ))
		hx, hz := u.FootX*8, u.FootZ*8
		f := &s.fields[i]
		for _, d := range fieldDirs {
			x, z, ok := m.DepthSite(t.navX+d[0]*fieldDist/1000, t.navZ+d[1]*fieldDist/1000, u.FootX, u.FootZ, lo, hi, 255, 240, nil, 0)
			if !ok {
				continue
			}
			good := true
			for j := int32(0); j < yards.n && good; j++ {
				good = aikit.Dist2(x, z, yards.x[j], yards.z[j]) >= fieldClear*fieldClear && !inLane(x, z, hx, hz, yards.x[j], yards.z[j], laneHalf)
			}
			// Workable by every class that can work the product's anchor.
			for c := range t.cls {
				rc := &t.cls[c]
				if good && rc.siteOK[i] && rc.r.Dist(rc.home, x, z, reach) < 0 {
					good = false
				}
			}
			for j := int32(0); j < f.n && good; j++ {
				good = aikit.Dist2(x, z, f.x[j], f.z[j]) >= 160*160
			}
			if good {
				f.add(x, z, m.HomeX, m.HomeZ)
			}
		}
	}
	for i := range s.fields {
		if s.fields[i].n > 0 {
			s.fieldDefs = append(s.fieldDefs, int32(i))
		}
	}
}

// fieldSite returns where a water building is requested: a water economy
// building at the field its class has reached (blocks of fieldBlock over
// every water economy building we own, framed or committed); a shipyard
// after the first at the next yard site. ok is false otherwise.
func (s *shared) fieldSite(p *aikit.UnitInfo) (int32, int32, bool) {
	if p.Role.Has(aikit.RoleFactory) {
		if j := s.yardIndex(); j > 0 {
			return s.yards.x[j], s.yards.z[j], true
		}
		return 0, 0, false // the first yard: its own anchor at the naval base
	}
	if int(p.Index) >= len(s.fields) {
		return 0, 0, false
	}
	f := &s.fields[p.Index]
	if f.n == 0 {
		return 0, 0, false
	}
	var n int32
	for _, i := range s.fieldDefs {
		n += s.count[i]
	}
	j := (n / fieldBlock) % f.n
	return f.x[j], f.z[j], true
}

// yardIndex is the yard site the next shipyard goes to: 0 (the naval
// base) for the first, then the ring in turn; 0 without a ring.
func (s *shared) yardIndex() int32 {
	if s.yards.n < 2 {
		return 0
	}
	var n int32
	for _, i := range s.yardDefs {
		n += s.count[i]
	}
	if n == 0 {
		return 0
	}
	return 1 + (n-1)%(s.yards.n-1)
}

// yardWorkable reports whether a builder of class c (in region reg) can
// build the next shipyard at its yard site (tidal_field): a ring site only
// by the classes that reach it from home.
func (s *shared) yardWorkable(c, reg int32) bool {
	j := s.yardIndex()
	if j == 0 || c < 0 {
		return true
	}
	return reg == s.terr.cls[c].home && c < 64 && s.yardCls[j]&(1<<uint(c)) != 0
}

func absI32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
