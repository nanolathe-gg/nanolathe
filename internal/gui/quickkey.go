package gui

import "github.com/nanolathe-gg/nanolathe/formats"

// PreclearButtonQuickKeys clears only button keys. It is the builder's
// whole-window prelude; labels retain their current key for collision checks
// [07 R-WGT-01 §3].
func PreclearButtonQuickKeys(w *Window) {
	if w == nil {
		return
	}
	for i := range w.Gadgets {
		if w.Gadgets[i].Kind == KindButton {
			w.Gadgets[i].QuickKey = 0
		}
	}
}

// AssignButtonQuickKey applies the button arm's assignment to one current
// record. Callers must exclude the per-gadget-GAF and slider-arrow paths,
// which do not enter this arm [07 R-WGT-01 §3].
func AssignButtonQuickKey(w *Window, index int) {
	if w == nil || index < 0 || index >= len(w.Gadgets) || w.Gadgets[index].Kind != KindButton {
		return
	}
	g := &w.Gadgets[index]
	if g.Attribs&0x10000 != 0 {
		return
	}
	if g.Stages != 0 {
		g.QuickKey = 0
		return
	}
	if captionEmpty(g.Text) {
		return
	}
	g.QuickKey = 0
	g.QuickKey = firstFreeCaptionKey(w, g.Text)
}

// AssignLinkedLabelQuickKey applies the label arm's assignment. An unlinked
// label is unchanged; a linked label clears then searches, including for an
// empty caption [07 R-WGT-01 §3][07 R-WGT-01 §7].
func AssignLinkedLabelQuickKey(w *Window, index int) {
	if w == nil || index < 0 || index >= len(w.Gadgets) || w.Gadgets[index].Kind != KindLabel {
		return
	}
	g := &w.Gadgets[index]
	if nulTerminatedEmpty(g.Link) {
		return
	}
	g.QuickKey = 0
	g.QuickKey = firstFreeCaptionKey(w, g.Text)
}

// CloneWindow makes one fresh runtime record set from an immutable parsed
// window. Builders mutate this copy; the cached parsed definition keeps its
// authored keys for a later open [07 R-WGT-01 §3].
func CloneWindow(source *Window) *Window {
	if source == nil {
		return nil
	}
	clone := *source
	clone.Gadgets = append([]Gadget(nil), source.Gadgets...)
	for i := range clone.Gadgets {
		clone.Gadgets[i].Labels = append([]string(nil), source.Gadgets[i].Labels...)
	}
	if source.fonts != nil {
		clone.fonts = make(map[int]*formats.FNT, len(source.fonts))
		for index, font := range source.fonts {
			clone.fonts[index] = font
		}
	}
	return &clone
}

func captionEmpty(text string) bool {
	for i := 0; i < len(text); i++ {
		if text[i] == 0 {
			return i == 0
		}
	}
	return len(text) == 0
}

func nulTerminatedEmpty(text string) bool {
	return len(text) == 0 || text[0] == 0
}

func firstFreeCaptionKey(w *Window, caption string) byte {
	for i := 0; i < len(caption); i++ {
		candidate := caption[i]
		if candidate == 0 {
			break
		}
		if candidate == ' ' || quickKeyTaken(w, candidate) {
			continue
		}
		return candidate
	}
	return 0
}

// quickKeyTaken compares candidates under the deliberately byte-local fold:
// the established locale trace changes A..Z only, and extended bytes compare
// only with themselves [07 R-WGT-01 §3].
func quickKeyTaken(w *Window, candidate byte) bool {
	needle := formats.FoldASCIIByte(candidate)
	for i := range w.Gadgets {
		g := w.Gadgets[i]
		if (g.Kind == KindButton || g.Kind == KindLabel) && g.QuickKey != 0 && formats.FoldASCIIByte(g.QuickKey) == needle {
			return true
		}
	}
	return false
}
