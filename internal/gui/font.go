package gui

import (
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/vfs"
)

// NoFontRecord is FontRecord's result when a font number selects no kind-7
// record: the painters then select the common (window) font
// [07 R-WGT-01 §6].
const NoFontRecord = -1

// FontRecord returns the gadget index of the window's n-th kind-7 font record,
// counting from zero in gadget order, where n is `fontNumber` read as a
// **signed** byte; NoFontRecord when no record matches.
//
// This is the walk every per-gadget font selector performs — the shared
// select-font-by-gadget helper and the label, button, listbox and text-input
// painters each run it: a counter starts at 0, every kind-7 record compares
// the counter against the gadget's font number and either matches or
// advances it, so font number 0 selects the window's first font record. A
// font number at or above the record count matches nothing, and so does any
// value 128..255, which the signed read makes negative and the non-negative
// counter never equals — the stock 132 and 205 select no record even in a
// window that authors some [07 R-WGT-01 §6][03 R-FONT-01 §5].
func (w *Window) FontRecord(fontNumber uint8) int {
	if w == nil {
		return NoFontRecord
	}
	want := int(int8(fontNumber))
	seen := 0
	for i := range w.Gadgets {
		if w.Gadgets[i].Kind != KindFont {
			continue
		}
		if seen == want {
			return i
		}
		seen++
	}
	return NoFontRecord
}

// Font returns the FNT the font number selects: the file the matching kind-7
// record's file slot names, loaded through the VFS on first use and cached on
// the window. A font number that selects no record, a record whose file is
// missing or unreadable, and a nil VFS all yield nil — retail loads the
// record's file at GUI parse, and a null load leaves the active-font setter
// ignoring the handle, so the previously active font (the common font) stays
// [07 R-WGT-01 §12][03 R-FONT-01 §5].
func (w *Window) Font(fs vfs.FSOps, fontNumber uint8) *formats.FNT {
	index := w.FontRecord(fontNumber)
	if index == NoFontRecord || fs == nil {
		return nil
	}
	if w.fonts == nil {
		w.fonts = make(map[int]*formats.FNT)
	}
	if font, loaded := w.fonts[index]; loaded {
		return font
	}
	var font *formats.FNT
	if path := w.Gadgets[index].FilePath; path != "" {
		if loaded, err := formats.LoadFNTFile(fs, path); err == nil {
			font = loaded
		}
	}
	w.fonts[index] = font
	return font
}
