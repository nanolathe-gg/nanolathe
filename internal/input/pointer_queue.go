package input

const pointerRingSlots = 20

// PointerRing retains semantic button records in producer order. One slot is
// reserved, so at most 19 records are pending [07 §2][01 R-PLAT-01 §6].
type PointerRing struct {
	items [pointerRingSlots]PointerEvent
	read  uint8
	write uint8
}

// Enqueue appends a classified button record. A full ring leaves both indices
// unchanged and refuses the new record.
func (q *PointerRing) Enqueue(event PointerEvent) bool {
	if q == nil {
		return false
	}
	if _, _, ok := event.Kind.button(); !ok {
		return false
	}
	next := (q.write + 1) % pointerRingSlots
	if next == q.read {
		return false
	}
	q.items[q.write] = event
	q.write = next
	return true
}

// Dequeue returns the oldest queued button record.
func (q *PointerRing) Dequeue() (PointerEvent, bool) {
	if q == nil || q.read == q.write {
		return PointerEvent{}, false
	}
	event := q.items[q.read]
	q.read = (q.read + 1) % pointerRingSlots
	return event, true
}

// Len reports the number of pending button records.
func (q *PointerRing) Len() int {
	if q == nil {
		return 0
	}
	if q.write >= q.read {
		return int(q.write - q.read)
	}
	return pointerRingSlots - int(q.read-q.write)
}

// Flush discards all queued button records.
func (q *PointerRing) Flush() {
	if q != nil {
		q.read = q.write
	}
}
