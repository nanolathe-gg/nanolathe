package content

import "sort"

// UnitRecords returns every non-sentinel record in catalog-index order,
// including duplicate and empty names [02 R-CAT-01 §§4–5]. The slice is a
// copy; its definitions remain immutable by convention, like Catalog.Units.
// Hand-built map-only catalogs retain their sorted-key fixture order.
func (c *Catalog) UnitRecords() []*UnitDef {
	if c == nil {
		return nil
	}
	return append([]*UnitDef(nil), c.unitRecordView()...)
}

func (c *Catalog) unitRecordView() []*UnitDef {
	if c.unitRecords != nil {
		return c.unitRecords
	}
	return unitMapRecords(c.Units)
}

func unitMapRecords(units map[string]*UnitDef) []*UnitDef {
	keys := make([]string, 0, len(units))
	for key := range units {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	records := make([]*UnitDef, 0, len(keys))
	for _, key := range keys {
		records = append(records, units[key])
	}
	return records
}

// firstUnitNames projects retail's runtime lookup onto the final names. A
// secondary parse may rename a record after sorting, so a present name can be
// unreachable by the unchanged lower-bound search [02 R-CAT-01 §5].
func firstUnitNames(records []*UnitDef) map[string]*UnitDef {
	units := make(map[string]*UnitDef, len(records))
	for _, u := range records {
		if u == nil {
			continue
		}
		key := CanonicalKey(u.UnitName)
		start, count := 0, len(records)
		for count > 0 {
			half := count / 2
			mid := start + half
			if asciiFoldContent(records[mid].UnitName) < asciiFoldContent(u.UnitName) {
				start = mid + 1
				count -= half + 1
			} else {
				count = half
			}
		}
		if start < len(records) && asciiFoldContent(records[start].UnitName) == asciiFoldContent(u.UnitName) {
			units[key] = records[start]
		}
	}
	return units
}

// sortUnitRecords preserves retail's equal-name permutation: median-of-three
// partitioning of ranges larger than sixteen swaps equal keys, followed by
// insertion that moves only strictly greater predecessors [02 R-CAT-01 §5].
// It is deterministic without adding a non-retail tie-breaker [I1].
func sortUnitRecords(records []*UnitDef) {
	less := func(a, b *UnitDef) bool { return asciiFoldContent(a.UnitName) < asciiFoldContent(b.UnitName) }
	var partition func(int, int)
	partition = func(lo, hi int) {
		for hi-lo > 16 {
			a, b, c := records[lo], records[lo+(hi-lo)/2], records[hi-1]
			var pivot *UnitDef
			if less(a, b) {
				if less(b, c) {
					pivot = b
				} else if less(a, c) {
					pivot = c
				} else {
					pivot = a
				}
			} else {
				if less(a, c) {
					pivot = a
				} else if less(b, c) {
					pivot = c
				} else {
					pivot = b
				}
			}
			left, right := lo, hi
			for {
				for less(records[left], pivot) {
					left++
				}
				right--
				for less(pivot, records[right]) {
					right--
				}
				if left >= right {
					break
				}
				records[left], records[right] = records[right], records[left]
				left++
			}
			if left-lo < hi-left {
				partition(lo, left)
				lo = left
			} else {
				partition(left, hi)
				hi = left
			}
		}
	}
	partition(0, len(records))
	// Insertion over the whole range is equivalent to retail's first-sixteen
	// guarded pass and remaining unguarded pass; the bounds check changes no
	// comparisons or moves on a partitioned range.
	for i := 1; i < len(records); i++ {
		u, j := records[i], i
		for j > 0 && less(u, records[j-1]) {
			records[j] = records[j-1]
			j--
		}
		records[j] = u
	}
}
