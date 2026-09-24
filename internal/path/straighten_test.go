package path

import "testing"

// cannedSearch finishes on its second slice with a fixed route.
type cannedSearch struct {
	route  []Point
	slices int
}

func (c *cannedSearch) Resume(int) ([]Point, Status, bool) {
	c.slices++
	if c.slices < 2 {
		return nil, 0, false
	}
	return c.route, 0, true
}
func (c *cannedSearch) Notified() Status     { return 0 }
func (c *cannedSearch) SetupSteps() int      { return 0 }
func (c *cannedSearch) Popped() int          { return 7 }
func (c *cannedSearch) Start() Cell          { return Cell{} }
func (c *cannedSearch) Config() SearchConfig { return SearchConfig{} }
func (c *cannedSearch) Release()             {}

// routeCells builds route points for a 1x1 footprint from anchor cells.
func routeCells(cells ...Cell) []Point {
	out := make([]Point, len(cells))
	for i, c := range cells {
		out[i] = worldPoint(c, Point{X: 1, Z: 1})
	}
	return out
}

func straighten(t *testing.T, blocked map[Cell]bool, route []Point) ([]Point, *straightenSearch) {
	t.Helper()
	s := &straightenSearch{Search: &cannedSearch{route: route}, cfg: SearchConfig{FootPrintX: 1, FootPrintZ: 1, PassableValue: func(c Cell) uint8 {
		if blocked[c] {
			return 0
		}
		return 3
	}}}
	if _, _, done := s.Resume(100); done {
		t.Fatal("straightening published before the retail search finished")
	}
	out, _, done := s.Resume(100)
	if !done {
		t.Fatal("straightening did not publish on the slice the retail search finished")
	}
	return out, s
}

// A sawtooth between two rows, five cells apart, becomes one row.
func TestStraightenRemovesSawtoothTurns(t *testing.T) {
	route := routeCells(Cell{0, 10}, Cell{5, 15}, Cell{10, 10}, Cell{15, 15}, Cell{20, 10}, Cell{25, 15}, Cell{30, 10})
	out, s := straighten(t, nil, route)
	want := routeCells(Cell{0, 10}, Cell{10, 10}, Cell{20, 10}, Cell{30, 10})
	if len(out) != len(want) {
		t.Fatalf("straightened to %v, want %v", out, want)
	}
	for i := range want {
		if out[i] != want[i] {
			t.Fatalf("point %d = %v, want %v", i, out[i], want[i])
		}
	}
	if s.Popped() <= 7 {
		t.Fatal("probed cells were not charged as work")
	}
}

// A blocked cell on the straight line keeps the retail turn, and a turn whose
// neighbours share neither a row nor a column is never shortcut.
func TestStraightenKeepsBlockedAndDiagonalTurns(t *testing.T) {
	route := routeCells(Cell{0, 10}, Cell{5, 15}, Cell{10, 10})
	if out, _ := straighten(t, map[Cell]bool{{7, 10}: true}, route); len(out) != 3 {
		t.Fatalf("a blocked shortcut was taken: %v", out)
	}
	diag := routeCells(Cell{0, 0}, Cell{5, 5}, Cell{10, 12})
	if out, _ := straighten(t, nil, diag); len(out) != 3 {
		t.Fatalf("a diagonal approach was shortcut: %v", out)
	}
	long := routeCells(Cell{0, 10}, Cell{10, 15}, Cell{20, 10})
	if out, _ := straighten(t, nil, long); len(out) != 3 {
		t.Fatalf("a shortcut longer than the span was taken: %v", out)
	}
}
