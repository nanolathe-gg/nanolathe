package ui

import (
	"github.com/nanolathe/nanolathe/internal/gui"
	"testing"
)

// Named defaults bind the first record, while Space preserves a later focused
// duplicate; the returned callback name keeps its authored bytes [07 R-FE-02 §5]
// [07 R-WGT-01 §2].
func TestDefaultKeyActionPreservesSelectedRecord(t *testing.T) {
	w := &gui.Window{Focus: 1, Header: gui.Header{CrDefault: " SAME ", DefaultFocus: " SAME "}, Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel},
		{Kind: gui.KindButton, Name: " SAME ", Active: 1},
		{Kind: gui.KindButton, Name: " SAME ", Active: 1},
	}}
	p := NewPanel(w)
	p.SetFocus(2)
	if a := p.DefaultKeyAction(true); a.Kind != ActionActivate || a.Index != 1 || a.Gadget != " SAME " {
		t.Fatalf("Enter action=%+v", a)
	}
	p.SetActiveAt(1, false)
	for _, enter := range []bool{false, true} {
		if a := p.DefaultKeyAction(enter); a.Kind != ActionActivate || a.Index != 2 || a.Gadget != " SAME " {
			t.Fatalf("enter=%v action=%+v", enter, a)
		}
	}
	p.SetFocus(0)
	if a := p.DefaultKeyAction(false); a.Kind != ActionNone || a.Index != -1 {
		t.Fatalf("header action=%+v", a)
	}
}
