package path

import "testing"

func TestDeveloperSearchReadsLatestGenerationWithoutLending(t *testing.T) {
	s := &Scheduler{}
	if s.DeveloperSearchAvailable() {
		t.Fatal("unused workspace reported available")
	}
	ix := newMapIndex()
	ix.bindWorkspace(s.Workspace(), Rect{Min: Cell{}, Max: Cell{X: 3, Z: 3}}, true)
	ix.set(Cell{X: 1, Z: 2}, entry{status: 4 | 8, dir: 7})
	generation := s.workspace.gen
	ix.release()
	status, dir := s.DeveloperSearchCell(1, 2)
	if status != 12 || dir != 7 || s.workspace.lent || s.workspace.gen != generation {
		t.Fatalf("released shared state changed: status=%d dir=%d", status, dir)
	}
	ix.bindWorkspace(s.Workspace(), Rect{Min: Cell{}, Max: Cell{X: 3, Z: 3}}, true)
	if status, dir := s.DeveloperSearchCell(1, 2); status != 0 || dir != 0 {
		t.Fatal("previous search leaked into new generation")
	}
}
