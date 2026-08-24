package path

import "testing"

func TestCellPointRectTypes(t *testing.T) {
	c := Cell{X: 10, Z: -3}
	if c.X != 10 || c.Z != -3 {
		t.Fatalf("Cell fields")
	}
	p := Point{X: 100, Z: 200}
	if p.X != 100 {
		t.Fatalf("Point fields")
	}
	r := Rect{Min: Cell{0, 0}, Max: Cell{5, 5}}
	if r.Max.X != 5 {
		t.Fatalf("Rect fields")
	}
}

func TestStatusConstants(t *testing.T) {
	if StatusAlreadySatisfied != 0x100 {
		t.Fatalf("StatusAlreadySatisfied want 0x100 got %#x", StatusAlreadySatisfied)
	}
	if StatusRejected != 0x200 {
		t.Fatalf("StatusRejected want 0x200 got %#x", StatusRejected)
	}
}

func TestGoalInterfaceCompiles(t *testing.T) {
	var _ Goal = (*mutableGoal)(nil)
}
