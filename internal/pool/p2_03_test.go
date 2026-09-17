package pool

import "testing"

func TestAllocatorFailureZeroFill(t *testing.T) {
	// P2-03 allocator failure and zero-fill: pool 500 folklore vs 2000+1 cap,
	// handle nil return, zero-fill new record
	p := NewUnitsSliced(2) // cap 2*10+1 =21
	// Fill slice for player 0 (slots 1..2)
	h1, ok := p.AllocForPlayerWithDef(0, 10, true, 2)
	if !ok {
		t.Fatalf("first alloc should succeed")
	}
	h2, ok := p.AllocForPlayerWithDef(0, 10, true, 2)
	if !ok {
		t.Fatalf("second alloc should succeed")
	}
	// Third should fail limit=2
	_, ok = p.AllocForPlayerWithDef(0, 10, true, 2)
	if ok {
		t.Fatalf("third alloc should fail per-def limit")
	}
	// Ensure zero-fill: new record after free should be zeroed except retained slotIndex
	p.Free(h1)
	if p.SlotIndex(h1) != uint16(h1) {
		t.Fatalf("slotIndex not retained after free [P0-16] got %d want %d", p.SlotIndex(h1), h1)
	}
	if p.DefID(h1) != 0 {
		t.Fatalf("defID not cleared after free")
	}
	// Realloc should reuse lowest-free (h1) and zero defID prior is 0 then stamp new defID
	h3, ok := p.AllocForPlayerWithDef(0, 20, true, 2)
	if !ok {
		t.Fatalf("realloc after free should succeed")
	}
	if h3 != h1 {
		t.Fatalf("realloc should reuse lowest-free %d got %d", h1, h3)
	}
	if p.DefID(h3) != 20 {
		t.Fatalf("defID after realloc %d want 20", p.DefID(h3))
	}
	_ = h2
}

func TestCapacityFormula(t *testing.T) {
	// P2-03: cap = limit*10+1 not 500 folklore, stock ~2000-5001
	if got := CapacityForLimit(200); got != 2001 {
		t.Fatalf("CapacityForLimit 200 = %d want 2001 [P0-16]", got)
	}
	if got := UsableCapacityForLimit(200); got != 2000 {
		t.Fatalf("Usable 200 = %d want 2000", got)
	}
	// Ensure TotalRecords includes slot 0
	p := NewUnitsSliced(200)
	if p.TotalRecords() != 2001 {
		t.Fatalf("TotalRecords %d want 2001", p.TotalRecords())
	}
	if p.Capacity() != 2000 {
		t.Fatalf("Capacity %d want 2000", p.Capacity())
	}
}

func TestForcedSlotBoundsCheckVsRetailNoCheck(t *testing.T) {
	// P2-03 corrupt save box: header offset bounds-check vs retail no-check
	// For pool, forcedSlot OOB vs occupied diverges: Nanolathe bounds-checks
	p := NewUnitsSliced(3) // slices 1..3, 4..6, ...
	// OOB: slot 10 is player 3, not player 0
	if _, ok := p.AllocForcedWithDef(0, 1, Handle(10), false, 0); ok {
		t.Fatalf("forced OOB should fail [P0-16] bounds-check divergence documented")
	}
	// Occupied should fail
	h, _ := p.AllocForPlayerWithDef(0, 1, false, 0)
	if _, ok := p.AllocForcedWithDef(0, 1, h, false, 0); ok {
		t.Fatalf("forced occupied should fail")
	}
	// Correct forced within slice and free succeeds
	p2 := NewUnitsSliced(5)
	// allocate slot 2 then free, then forced realloc same slot
	h2, _ := p2.AllocForPlayerWithDef(0, 1, false, 0)
	p2.Free(h2)
	if h3, ok := p2.AllocForcedWithDef(0, 1, h2, false, 0); !ok || h3 != h2 {
		t.Fatalf("forced free slot should succeed, got %v %d", ok, h3)
	}
}
