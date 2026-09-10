package movement

// stampAll computes the separable footprint/ring minimum [04 R-SLOPE-01 §3].
// Inputs remain fixed throughout this synchronous rebuild. Counting blocked and
// non-clear cells lets each window advance without reclassifying overlapping
// cells or changing the strict full-stamp bounds.
func (l *ClassLayer) stampAll() {
	// Host diagnostic, counted before the early return so a caller that
	// clears the layer wholesale is counted as the full rebuild it is.
	l.fullStamps++
	fx, fz := l.footprintSize()
	if fx > l.W || fz > l.H {
		clear(l.cells)
		return
	}
	size := int(l.W * l.H)
	if cap(l.stampScratch) < size {
		l.stampScratch = make([]uint8, size)
	} else {
		l.stampScratch = l.stampScratch[:size]
	}
	if cap(l.stampRows) < size {
		l.stampRows = make([]uint8, size)
	} else {
		l.stampRows = l.stampRows[:size]
	}
	for z := int32(0); z < l.H; z++ {
		for x := int32(0); x < l.W; x++ {
			l.stampScratch[z*l.W+x] = l.classifyCell(x, z)
		}
	}
	for z := int32(0); z < l.H; z++ {
		row := l.stampScratch[z*l.W : (z+1)*l.W]
		var window layerWindow
		for x := int32(0); x < fx; x++ {
			window.add(row[x], 1)
		}
		for x := int32(0); x < l.W; x++ {
			value := LayerBlocked
			if x+fx <= l.W {
				value = window.value()
				if value == LayerClear && (x == 0 || x+fx == l.W || row[x-1] != LayerClear || row[x+fx] != LayerClear) {
					value = LayerSteep
				}
				window.add(row[x], -1)
				if x+fx < l.W {
					window.add(row[x+fx], 1)
				}
			}
			l.stampRows[z*l.W+x] = value
		}
	}
	for x := int32(0); x < l.W; x++ {
		var window layerWindow
		for z := int32(0); z < fz; z++ {
			window.add(l.stampRows[z*l.W+x], 1)
		}
		for z := int32(0); z < l.H; z++ {
			value := LayerBlocked
			if z+fz <= l.H {
				value = window.value()
				if value == LayerClear && (z == 0 || z+fz == l.H || l.stampRows[(z-1)*l.W+x] != LayerClear || l.stampRows[(z+fz)*l.W+x] != LayerClear) {
					value = LayerSteep
				}
				window.add(l.stampRows[z*l.W+x], -1)
				if z+fz < l.H {
					window.add(l.stampRows[(z+fz)*l.W+x], 1)
				}
			}
			l.setValue(x, z, value)
		}
	}
}

type layerWindow struct{ blocked, nonclear int32 }

func (w *layerWindow) add(value uint8, delta int32) {
	if value == LayerBlocked {
		w.blocked += delta
	}
	if value != LayerClear {
		w.nonclear += delta
	}
}
func (w layerWindow) value() uint8 {
	if w.blocked > 0 {
		return LayerBlocked
	}
	if w.nonclear > 0 {
		return LayerSteep
	}
	return LayerClear
}
