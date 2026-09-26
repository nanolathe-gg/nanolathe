package utility

import "testing"

// The tower audit resolves each owed think at the next one: only the
// commander priced a tower (the front rules leave it to the constructor):
// com_veto; the constructor priced one and did something else: outscored;
// it took the order: built, and the think was ordered.
func TestDefenseAuditReasons(t *testing.T) {
	w := newDefWorld(DefaultParams())
	d := w.d
	s := w.e.s
	pl := w.e.plan()
	a := &s.zones.audit
	step := func(tick uint32) {
		w.think(tick, w.base())
		pl.refresh(s, w.b)
	}
	step(7 * 1800)
	if !a.owed {
		t.Fatalf("minute 7: no tower owed (deficit %d, reference %d)", pl.deficit[dcGround], pl.cRef[dcGround])
	}
	w.e.defenseCand(w.b, &w.obs.Own[0], d.llt) // the commander: vetoed
	step(7*1800 + 15)
	if got := a.pn[0][priceCom]; got != 1 || a.n[0][owedPriced] != 1 {
		t.Errorf("commander only: com_veto %d, want 1 (counts %v %v)", got, a.pn[0], a.n[0])
	}
	if c := w.e.defenseCand(w.b, &w.obs.Own[1], d.rl); c.score <= minScore {
		t.Fatalf("constructor missile tower scored %d", c.score)
	}
	step(7*1800 + 30)
	if got := a.pn[0][priceOutscored]; got != 1 {
		t.Errorf("priced, not chosen: outscored %d, want 1 (counts %v)", got, a.pn[0])
	}
	w.e.defenseCand(w.b, &w.obs.Own[1], d.rl)
	con := &w.obs.Own[1]
	c := s.commitOf(con)
	*c = commitment{def: con.Info, gen: con.Gen, kind: cDefense, prod: d.rl, x: pl.x[dcGround], z: pl.z[dcGround], tick: s.tick, score: 500}
	step(7*1800 + 45)
	if got := a.pn[0][priceBuilt]; got != 1 || a.n[0][owedOrdered] != 1 {
		t.Errorf("ordered: built %d, want 1 (counts %v %v)", got, a.pn[0], a.n[0])
	}
	if got := a.owedN[0]; got != 3 || a.spells[0] != 1 || a.spell[0] != 30 {
		t.Errorf("owed thinks %d (want 3), spells %d over %d ticks (want 1 over 30)", got, a.spells[0], a.spell[0])
	}
}
