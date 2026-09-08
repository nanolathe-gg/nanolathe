package input

import "testing"

func pointerEvent(sequence int) PointerEvent {
	return PointerEvent{Kind: LeftDown, X: int32(sequence), Y: int32(sequence + 100), Timestamp: uint32(sequence)}
}

func TestPointerRingWrapAndFullRefusal(t *testing.T) {
	var ring PointerRing
	for sequence := 0; sequence < pointerRingSlots-1; sequence++ {
		if !ring.Enqueue(pointerEvent(sequence)) {
			t.Fatalf("enqueue %d failed", sequence)
		}
	}
	read, write := ring.read, ring.write
	if ring.Enqueue(pointerEvent(pointerRingSlots - 1)) {
		t.Fatal("enqueue succeeded for full ring")
	}
	if ring.read != read || ring.write != write {
		t.Fatalf("full enqueue changed indices: read=%d write=%d, want read=%d write=%d", ring.read, ring.write, read, write)
	}

	for sequence := 0; sequence < 7; sequence++ {
		event, ok := ring.Dequeue()
		if !ok || event.X != int32(sequence) {
			t.Fatalf("dequeue %d = %+v, %v", sequence, event, ok)
		}
	}
	for sequence := pointerRingSlots - 1; sequence < pointerRingSlots-1+7; sequence++ {
		if !ring.Enqueue(pointerEvent(sequence)) {
			t.Fatalf("wrapped enqueue %d failed", sequence)
		}
	}
	for sequence := 7; sequence < pointerRingSlots-1+7; sequence++ {
		event, ok := ring.Dequeue()
		if !ok || event.X != int32(sequence) {
			t.Fatalf("wrapped dequeue %d = %+v, %v", sequence, event, ok)
		}
	}
	if ring.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", ring.Len())
	}
}

func TestPointerRingFlush(t *testing.T) {
	var ring PointerRing
	if !ring.Enqueue(pointerEvent(1)) || !ring.Enqueue(pointerEvent(2)) {
		t.Fatal("setup enqueue failed")
	}
	ring.Flush()
	if ring.Len() != 0 {
		t.Fatalf("Len() after Flush = %d, want 0", ring.Len())
	}
	if event, ok := ring.Dequeue(); ok || event != (PointerEvent{}) {
		t.Fatalf("Dequeue() after Flush = %+v, %v", event, ok)
	}
	if !ring.Enqueue(pointerEvent(3)) {
		t.Fatal("enqueue after Flush failed")
	}
}
