package orders

import "testing"

func TestDescriptorTable(t *testing.T) {
	tbl := Table()
	if len(tbl) != 68 {
		t.Fatalf("table len %d, want 68", len(tbl))
	}
	if tbl[0].Name != "" {
		t.Fatalf("index 0 name %q, want empty sentinel", tbl[0].Name)
	}
	// Spot-check five IDs against [04 §3.1] table.
	checks := map[string]int{
		"Move_Ground":  -1, // we don't hardcode index, just lookup
		"Attack_Chase": -1,
		"SelfDestruct": -1,
		"Patrol":       -1,
		"Wait":         -1,
	}
	for name := range checks {
		id := Lookup(name)
		if id == 0 {
			t.Fatalf("Lookup %q returned 0 sentinel", name)
		}
		if DescriptorFor(id).Name != name {
			t.Fatalf("DescriptorFor Lookup %q got %q", name, DescriptorFor(id).Name)
		}
	}
	// Verify sorted case-sensitive byte comparison.
	for i := 1; i < len(tbl); i++ {
		if tbl[i-1].Name >= tbl[i].Name {
			t.Fatalf("table not sorted at %d: %q >= %q", i-1, tbl[i-1].Name, tbl[i].Name)
		}
	}
	// Gate mask spot-check
	if DescriptorFor(Lookup("Move_Ground")).StaticGate != 0x402 {
		t.Fatalf("Move_Ground gate %x, want 0x402", DescriptorFor(Lookup("Move_Ground")).StaticGate)
	}
	if DescriptorFor(Lookup("SelfDestruct")).StaticGate != 0x40040 {
		t.Fatalf("SelfDestruct gate %x, want 0x40040", DescriptorFor(Lookup("SelfDestruct")).StaticGate)
	}
}
