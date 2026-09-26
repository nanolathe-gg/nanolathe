package community

import "testing"

func TestBuilderPlacementLimitOverride(t *testing.T) {
	o, err := ParseOverride("aiBuilderPlacementLimit=127")
	if err != nil {
		t.Fatal(err)
	}
	f, err := Resolve(false, o)
	if err != nil || f.AIBuilderPlacementLimit != 127 {
		t.Fatalf("resolve=%+v, %v", f, err)
	}
	strict, err := Resolve(true, o)
	if err != nil || strict != (Features{}) {
		t.Fatalf("Strict retained numeric override: %+v, %v", strict, err)
	}
	for _, bad := range []string{"aiBuilderPlacementLimit=-1", "aiBuilderPlacementLimit=2147483648"} {
		if _, err := ParseOverride(bad); err == nil {
			t.Fatalf("accepted unrepresentable limit %s", bad)
		}
	}
}
