package gui

import "strings"

// GadgetName is the byte span used by named lookup and screen callbacks.
// Retail compares at most 16 bytes and stops at a terminator; case and
// whitespace remain significant [07 R-FE-02 §5].
func GadgetName(name string) string {
	if len(name) > 16 {
		name = name[:16]
	}
	if end := strings.IndexByte(name, 0); end >= 0 {
		name = name[:end]
	}
	return name
}

// GadgetIndex returns the first exact named record after the window header,
// or -1 on a miss [07 R-FE-02 §5].
func (w *Window) GadgetIndex(name string) int {
	if w == nil {
		return -1
	}
	name = GadgetName(name)
	for i := 1; i < len(w.Gadgets); i++ {
		if GadgetName(w.Gadgets[i].Name) == name {
			return i
		}
	}
	return -1
}

// EnterDefaultIndex resolves the authored default, or the distinct prefix
// fallback used for an empty default [07 R-FE-01 §12]. Activity and grey
// admission belong to the key service, after this first-record selection.
func (w *Window) EnterDefaultIndex() int {
	if w == nil {
		return -1
	}
	return w.defaultIndex(w.Header.CrDefault, "OK", "NEXT")
}

// EscapeDefaultIndex resolves the authored escape default. An empty value
// selects the first PREV/Cancel button by case-insensitive prefix, without
// trimming its name or skipping inactive records [07 R-FE-01 §12].
func (w *Window) EscapeDefaultIndex() int {
	if w == nil {
		return -1
	}
	return w.defaultIndex(w.Header.EscDefault, "PREV", "Cancel")
}

func (w *Window) defaultIndex(name, first, second string) int {
	if GadgetName(name) != "" {
		return w.GadgetIndex(name)
	}
	for i := 1; i < len(w.Gadgets); i++ {
		g := w.Gadgets[i]
		if g.Kind == KindButton && (namePrefix(g.Name, first) || namePrefix(g.Name, second)) {
			return i
		}
	}
	return -1
}

func namePrefix(name, prefix string) bool {
	name = GadgetName(name)
	if len(name) < len(prefix) {
		return false
	}
	for i := range len(prefix) {
		a, b := name[i], prefix[i]
		if a >= 'A' && a <= 'Z' {
			a += 'a' - 'A'
		}
		if b >= 'A' && b <= 'Z' {
			b += 'a' - 'A'
		}
		if a != b {
			return false
		}
	}
	return true
}
