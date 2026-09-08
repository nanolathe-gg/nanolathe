package gui

import "testing"

func TestAssignButtonQuickKeyUsesCurrentWholeWindowKeys(t *testing.T) {
	w := &Window{Gadgets: []Gadget{
		{Kind: KindButton, Text: "Alpha", QuickKey: 'A'},
		{Kind: KindLabel, Link: "target", QuickKey: 'l'},
		{Kind: KindButton, Text: "Alps", QuickKey: 'A'},
	}}
	AssignButtonQuickKey(w, 0)
	if got := w.Gadgets[0].QuickKey; got != 'p' {
		t.Fatalf("key = %q, want p after later records exclude A and label excludes l", got)
	}
}

func TestQuickKeyAssignmentRecordRules(t *testing.T) {
	w := &Window{Gadgets: []Gadget{
		{Kind: KindButton, Text: "Keep", QuickKey: 'K', Attribs: 0x10000},
		{Kind: KindButton, Text: "Stage", QuickKey: 'S', Stages: 1},
		{Kind: KindButton, Text: "", QuickKey: 'E'},
		{Kind: KindLabel, Text: "Caption", QuickKey: 'C'},
		{Kind: KindLabel, Text: "Linked", Link: "target", QuickKey: 'L'},
	}}
	AssignButtonQuickKey(w, 0)
	AssignButtonQuickKey(w, 1)
	AssignButtonQuickKey(w, 2)
	AssignLinkedLabelQuickKey(w, 3)
	AssignLinkedLabelQuickKey(w, 4)
	if got := []byte{w.Gadgets[0].QuickKey, w.Gadgets[1].QuickKey, w.Gadgets[2].QuickKey, w.Gadgets[3].QuickKey, w.Gadgets[4].QuickKey}; string(got) != "K\x00ECL" {
		t.Fatalf("keys = %q, want preserve/staged/empty/unlinked/linked rules", got)
	}
}

func TestPreclearAndExtendedBytes(t *testing.T) {
	w := &Window{Gadgets: []Gadget{
		{Kind: KindButton, QuickKey: 'A'},
		{Kind: KindLabel, QuickKey: 0xC4},
		{Kind: KindButton, Text: string([]byte{0xE4, 0xC4, 0})},
	}}
	PreclearButtonQuickKeys(w)
	if w.Gadgets[0].QuickKey != 0 || w.Gadgets[1].QuickKey != 0xC4 {
		t.Fatalf("preclear changed button/label keys to %#x/%#x", w.Gadgets[0].QuickKey, w.Gadgets[1].QuickKey)
	}
	AssignButtonQuickKey(w, 2)
	if got := w.Gadgets[2].QuickKey; got != 0xE4 {
		t.Fatalf("extended key = %#x, want 0xe4; no code-page fold", got)
	}
}

func TestLinkedLabelTreatsNULLeadingLinkAsEmpty(t *testing.T) {
	w := &Window{Gadgets: []Gadget{{Kind: KindLabel, Link: "\x00target", Text: "Label", QuickKey: 'Q'}}}
	AssignLinkedLabelQuickKey(w, 0)
	if got := w.Gadgets[0].QuickKey; got != 'Q' {
		t.Fatalf("NUL-leading link key = %q, want unchanged Q", got)
	}
}

func TestCloneWindowKeepsParsedKeysSeparateFromRuntimeBuild(t *testing.T) {
	authored := &Window{Gadgets: []Gadget{{Kind: KindButton, Text: "Keep", QuickKey: 'K', Labels: []string{"one"}}}}
	runtime := CloneWindow(authored)
	PreclearButtonQuickKeys(runtime)
	runtime.Gadgets[0].Labels[0] = "changed"
	if authored.Gadgets[0].QuickKey != 'K' || authored.Gadgets[0].Labels[0] != "one" {
		t.Fatalf("runtime mutation changed parsed source: %+v", authored.Gadgets[0])
	}
}
