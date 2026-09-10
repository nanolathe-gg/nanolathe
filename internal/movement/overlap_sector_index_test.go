package movement

import (
	"sort"
	"testing"
)

// The clear's overlap scan walks the sector-bucket index rather than every live
// unit [04 R-COLL-01 §4A]. This file is the equivalence proof: a randomized but
// fully deterministic program of stamps, clears and finalisations is driven
// against a fixture grid, and at every clear the candidate sequence the bucket
// sweep produces is compared with the sequence the gather-filter-sort form
// produced — walk every live candidate, reject by rectangle intersection and
// sector span, then sort column-major with the link sequence descending inside
// one record.
//
// The gather-filter-sort form lives here and nowhere else. It is the contract
// the index has to reproduce, not a fallback the engine may take.

// indexLCG is a throwaway generator for the test program's choices. It is not
// a simulation stream and never touches one [I4]; it is here so the program is
// reproducible from a literal seed with no dependency.
type indexLCG uint64

func (r *indexLCG) next(n int) int {
	*r = *r*6364136223846793005 + 1442695040888963407
	return int((uint64(*r) >> 33) % uint64(n))
}

// bruteForceOverlapSequence is the pre-index scan: gather every live candidate
// the binding offers, reject by rectangle intersection and by sector span, then
// sort into the sector sweep — sector column ascending, sector row ascending,
// and inside one record the link sequence descending, stably, so candidates
// with no filing keep their live-unit order [04 R-COLL-01 §4A][04 R-COLL-01 §11].
func bruteForceOverlapSequence(g *OccupancyGrid, f *filingFixture, clearing int) []int {
	anchor, fx, fz, ok := g.overlap.OverlapRect(clearing)
	if !ok {
		return nil
	}
	if fx <= 0 {
		fx = 1
	}
	if fz <= 0 {
		fz = 1
	}
	sxLo, sxHi := sectorOfCell(anchor.X)-1, sectorOfCell(anchor.X+int32(fx))+1
	szLo, szHi := sectorOfCell(anchor.Z)-1, sectorOfCell(anchor.Z+int32(fz))+1
	if sxLo > sxHi {
		return nil
	}
	type cand struct {
		id     int
		sx, sz int32
		seq    uint64
	}
	var gathered []cand
	f.VisitOverlapCandidates(func(id int) {
		if id == clearing || id <= 0 {
			return
		}
		a, cfx, cfz, ok := f.OverlapRect(id)
		if !ok || !rectsIntersect(anchor, fx, fz, a, cfx, cfz) {
			return
		}
		var sx, sz int32
		var filed bool
		var seq uint64
		if fl := f.OverlapFiling(id); fl != nil && fl.Filed {
			sx, sz, filed, seq = fl.SX, fl.SZ, !fl.OffMap, fl.Seq
		} else {
			sx, sz, filed = g.unitSector(id, a, cfx, cfz, f)
		}
		if !filed || sx < sxLo || sx > sxHi || sz < szLo || sz > szHi {
			return
		}
		gathered = append(gathered, cand{id: id, sx: sx, sz: sz, seq: seq})
	})
	sort.SliceStable(gathered, func(i, j int) bool {
		if gathered[i].sx != gathered[j].sx {
			return gathered[i].sx < gathered[j].sx
		}
		if gathered[i].sz != gathered[j].sz {
			return gathered[i].sz < gathered[j].sz
		}
		return gathered[i].seq > gathered[j].seq
	})
	out := make([]int, 0, len(gathered))
	for _, c := range gathered {
		out = append(out, c.id)
	}
	return out
}

// TestSectorBucketWalkMatchesTheGatherAndSort drives a deterministic program of
// stamps, clears and finalisations and checks every clear's candidate sequence.
func TestSectorBucketWalkMatchesTheGatherAndSort(t *testing.T) {
	const slots = 24
	f := newFilingFixture(slots)
	g, _ := sectorGrid(f.sectorFixture)
	g.AttachOverlap(f, func(owner uint8) uint8 { return owner })

	// The restamp only records: leaving the cells alone keeps the two sequences
	// comparable, because neither can then change what the other would have
	// gathered.
	f.restamp = func(int) {}

	present := make([]bool, slots)
	place := func(id int, anchor Cell, fx, fz int16) {
		f.owner[id] = activeState
		f.rect[id] = overlapRect{anchor: anchor, fx: fx, fz: fz, ok: true}
		f.posX[id] = int32(int64(anchor.X)*worldUnitsPerCell + int64(fx)*worldUnitsPerCell/2)
		f.posZ[id] = int32(int64(anchor.Z)*worldUnitsPerCell + int64(fz)*worldUnitsPerCell/2)
		if !present[id] {
			present[id] = true
			f.live = append(f.live, id)
		}
	}
	drop := func(id int) {
		present[id] = false
		f.rect[id] = overlapRect{}
		for i, v := range f.live {
			if v == id {
				f.live = append(f.live[:i], f.live[i+1:]...)
				break
			}
		}
	}

	rng := indexLCG(0x5eed1234)
	checked, nonEmpty := 0, 0
	for step := 0; step < 4000; step++ {
		id := 1 + rng.next(slots-1)
		switch rng.next(5) {
		case 0, 1, 2: // move: clear the old rectangle, then stamp the new one
			if !present[id] {
				place(id, Cell{X: int32(rng.next(40)), Z: int32(rng.next(40))}, int16(1+rng.next(3)), int16(1+rng.next(3)))
				g.StampPlane(PlaneGround, f.rect[id].anchor, f.rect[id].fx, f.rect[id].fz, id)
				continue
			}
			r := f.rect[id]
			// Raise the bits the scan consumes: the clearing unit must be a
			// host for the scan to run at all, and every other candidate must
			// be an intruder for the restamp to record it.
			f.host[id] = true
			for _, other := range f.live {
				if other != id {
					f.intruder[other] = true
				}
			}
			want := bruteForceOverlapSequence(g, f, id)
			f.restamped = nil
			g.ClearPlane(PlaneGround, r.anchor, r.fx, r.fz, id)
			checked++
			if len(want) > 0 {
				nonEmpty++
			}
			if !equalIntSlices(f.restamped, want) {
				t.Fatalf("step %d clearing %d: bucket sweep %v, gather-and-sort %v [04 R-COLL-01 §4A]", step, id, f.restamped, want)
			}
			place(id, Cell{X: int32(rng.next(40)), Z: int32(rng.next(40))}, r.fx, r.fz)
			g.StampPlane(PlaneGround, f.rect[id].anchor, f.rect[id].fx, f.rect[id].fz, id)
		case 3: // finalisation: clear, unlink, and take the record away
			if !present[id] {
				continue
			}
			r := f.rect[id]
			g.ClearPlane(PlaneGround, r.anchor, r.fx, r.fz, id)
			g.ForgetFiling(id)
			drop(id)
		case 4: // a record that exists but has never stamped: unfiled
			if present[id] {
				continue
			}
			place(id, Cell{X: int32(rng.next(40)), Z: int32(rng.next(40))}, int16(1+rng.next(3)), int16(1+rng.next(3)))
		}
	}
	if checked < 200 || nonEmpty < 50 {
		t.Fatalf("the program exercised %d clears of which %d had candidates; it is not testing the sweep", checked, nonEmpty)
	}
}

func equalIntSlices(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
