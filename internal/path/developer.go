package path

// DeveloperSearchAvailable reports whether the shared search table has ever
// been initialized. Its most recent generation survives release, but no older
// searches are retained for inspection [03 §3.12][04 R-PATH-01 §1].
func (s *Scheduler) DeveloperSearchAvailable() bool {
	return s != nil && s.workspace.gen != 0
}

// DeveloperSearchCell returns copied status and direction bytes from the
// shared table. Empty/outside cells have no marks; stale generations do not
// belong to the current search [03 §3.12]. No table is lent or reset here.
func (s *Scheduler) DeveloperSearchCell(x, z int32) (status, direction uint8) {
	if !s.DeveloperSearchAvailable() {
		return 0, 0
	}
	w := &s.workspace
	if i, ok := w.slot(Cell{X: x, Z: z}); ok && w.slots[i].gen == w.gen {
		e := w.slots[i].e
		return e.status, e.dir
	}
	return 0, 0
}
