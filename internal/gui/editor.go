package gui

// Text-editor admission and clipboard paste per [07 §4] C4 and [07 §2] clipboard
// contract — authored max capped at 128, printable gate, input-filter bit 0x02,
// allowed-set with space/underscore/apostrophe exceptions, width rule
// currentLen+newLen <= controlWidth-4 for typed insertion, paste copies at most
// maxchars-1 then trims trailing bytes until rendered width fits.

const (
	editorMaxBuffer = 128  // [07 §4] C4
	editorFilterBit = 0x02 // [07 §4] C4
	editorWidthPad  = 4    // [07 §4] C4
	editorCFText    = 1    // CF_TEXT [07 §2]
)

// allowedSet is the filtered charset; exceptions are space, underscore,
// apostrophe which are admitted even when filtering is on [07 §4] C4.
// TODO(question): full allowed-set not enumerated; alphanumeric is the
// minimal closed set that satisfies the exception semantics and retail save-name
// behaviour; punctuation beyond exceptions is intentionally rejected.
var allowedSet [256]bool

func init() {
	for c := byte('0'); c <= byte('9'); c++ {
		allowedSet[c] = true
	}
	for c := byte('A'); c <= byte('Z'); c++ {
		allowedSet[c] = true
	}
	for c := byte('a'); c <= byte('z'); c++ {
		allowedSet[c] = true
	}
	// Wire the dispatch hook [07 §4] C4.
	EditorAdmissionHook = func(g *Gadget, ch byte) bool {
		if g == nil {
			return false
		}
		return CanInsert(g, g.Text, ch, EditorMeasure)
	}
}

// EditorMeasure is the pluggable rendered-width measurer [07 §4] C4.
// It mirrors internal/client.MeasureText(FNT,text) when a font is available;
// the fallback is bytes*6 (a stable approximation per I13 presentation
// separation) so admission is still deterministic without a font.
var EditorMeasure func(string) int = func(s string) int { return len(s) * 6 }

// EffectiveMaxChars returns the authored 16-bit maximum capped at 128 [07 §4] C4.
func EffectiveMaxChars(raw int16) int {
	if raw > editorMaxBuffer {
		return editorMaxBuffer
	}
	if raw < 0 {
		return 0
	}
	return int(raw)
}

// CopyLimit returns maxchars-1 bounded to >=0 [07 §4] C4.
func CopyLimit(effective int) int {
	if effective <= 0 {
		return 0
	}
	n := effective - 1
	if n < 0 {
		return 0
	}
	return n
}

// IsPrintable reports whether a byte is in the printable range admitted by the
// editor [07 §4] C4. Retail admits printable bytes; non-printable (<0x20,
// 0x7F, >=0x80) are rejected before filtering or width checks.
func IsPrintable(b byte) bool {
	return b >= 0x20 && b <= 0x7E // [07 §4] C4
}

// IsAllowedWithFilter applies the input-filter bit 0x02 and the allowed-set
// test with exceptions for space, underscore and apostrophe [07 §4] C4.
func IsAllowedWithFilter(b byte, attribs uint32, commonAttribs uint8) bool {
	filterOn := (attribs&editorFilterBit) != 0 || (uint32(commonAttribs)&editorFilterBit) != 0 // [07 §4] C4
	if !filterOn {
		return true
	}
	if b == ' ' || b == '_' || b == '\'' { // exceptions [07 §4] C4
		return true
	}
	return allowedSet[b]
}

// measureOrFallback returns a non-nil measurer.
func measureOrFallback(m func(string) int) func(string) int {
	if m != nil {
		return m
	}
	if EditorMeasure != nil {
		return EditorMeasure
	}
	return func(s string) int { return len(s) * 6 }
}

// CanInsert reports whether byte ch can be admitted into curText for gadget g
// per [07 §4] C4: capped maxchars-1, printable, filter bit 0x02 + allowed-set
// with space/underscore/apostrophe exceptions, width rule
// currentLen+newLen <= controlWidth-4 where lengths are rendered widths.
func CanInsert(g *Gadget, curText string, ch byte, measure func(string) int) bool {
	if g == nil {
		return false
	}
	if !IsPrintable(ch) { // [07 §4] C4
		return false
	}
	eff := EffectiveMaxChars(g.MaxChars) // [07 §4] C4
	limit := CopyLimit(eff)              // maxchars-1 [07 §4] C4
	if len(curText) >= limit {           // already at cap, no room for one more [07 §4]
		return false
	}
	if len(curText)+1 > limit { // would exceed maxchars-1 [07 §4]
		return false
	}
	if !IsAllowedWithFilter(ch, g.Attribs, g.CommonAttribs) { // [07 §4] C4
		return false
	}
	m := measureOrFallback(measure)
	// width rule currentLen+newLen <= controlWidth-4 [07 §4] C4
	cw := int(g.Rect.W)
	avail := cw - editorWidthPad
	if cw <= 0 {
		// No authored width — width rule is vacuous; admission depends only on cap/filter.
		return true
	}
	if avail < 0 {
		avail = 0
	}
	curW := m(curText)
	newW := m(string([]byte{ch}))
	// Retail re-measures total; sum is equivalent for additive FNT advances.
	if curW+newW > avail {
		return false
	}
	// Also ensure the concatenated width fits when measured jointly (defensive
	// for non-additive measurers).
	if m(curText+string([]byte{ch})) > avail {
		return false
	}
	return true
}

// PasteText implements the CF_TEXT paste contract [07 §2] C4: copies at most
// maxchars-1 bytes (the smaller of clipboard allocation size and maxchars-1),
// keeps termination (buffer is zero-filled first in retail; Go string length
// encodes termination), then trims trailing bytes until rendered width fits the
// control width, re-measuring each iteration [07 §2][07 §4].
func PasteText(g *Gadget, clipboard string, measure func(string) int) string {
	if g == nil {
		return ""
	}
	eff := EffectiveMaxChars(g.MaxChars) // [07 §4] C4
	limit := CopyLimit(eff)              // maxchars-1 [07 §4][07 §2]
	if limit <= 0 {
		return ""
	}
	// Copy length is smaller of clipboard size and maxchars-1 [07 §2]; copy
	// proceeds dword then byte steps — equivalent to byte copy here.
	n := len(clipboard)
	if n > limit {
		n = limit
	}
	buf := clipboard[:n] // keeps termination: byte at n is conceptually zero [07 §2]
	m := measureOrFallback(measure)
	cw := int(g.Rect.W)
	if cw <= 0 {
		return buf
	}
	// After pasting, if rendered width exceeds control width, trailing bytes
	// are removed one at a time — re-measuring each iteration — until it fits [07 §2][07 §4].
	for len(buf) > 0 && m(buf) > cw {
		buf = buf[:len(buf)-1]
	}
	return buf
}

// PasteIntoBuffer copies PasteText's result into the fixed 128-byte edit buffer
// simulation, zero-filling first and maintaining termination [07 §2]. It returns
// the new length. This is the CF_TEXT equivalent with no OS calls per WU-12-3.
func PasteIntoBuffer(buf *[editorMaxBuffer]byte, g *Gadget, clipboard string, measure func(string) int) int {
	if buf == nil || g == nil {
		return 0
	}
	// Zero-filled first [07 §2].
	for i := range buf {
		buf[i] = 0
	}
	text := PasteText(g, clipboard, measure)
	n := len(text)
	if n > editorMaxBuffer-1 {
		n = editorMaxBuffer - 1
		text = text[:n]
	}
	copy(buf[:], text)
	// Keep termination: byte at n stays zero [07 §2].
	return n
}

// InsertIfAdmitted attempts to append ch to curText if CanInsert holds; returns
// the new text and whether admission succeeded [07 §4] C4.
func InsertIfAdmitted(g *Gadget, curText string, ch byte, measure func(string) int) (string, bool) {
	if !CanInsert(g, curText, ch, measure) {
		return curText, false
	}
	return curText + string([]byte{ch}), true
}

// Editor is mutable text-editor state for a gadget [07 §4] C4.
// It holds the current buffer contents and delegates admission/paste to the
// package functions so width measurement and filter logic stay in one place.
type Editor struct {
	Gadget  *Gadget
	Text    string
	Measure func(string) int
}

// NewEditor creates an editor for g; measure may be nil to use EditorMeasure.
func NewEditor(g *Gadget, measure func(string) int) *Editor {
	if measure == nil {
		measure = EditorMeasure
	}
	var t string
	if g != nil {
		t = g.Text
	}
	return &Editor{Gadget: g, Text: t, Measure: measure}
}

// CanInsert reports whether ch would be admitted at the current Text end [07 §4] C4.
func (e *Editor) CanInsert(ch byte) bool {
	if e == nil || e.Gadget == nil {
		return false
	}
	return CanInsert(e.Gadget, e.Text, ch, e.Measure)
}

// Insert appends ch if admitted [07 §4] C4.
func (e *Editor) Insert(ch byte) bool {
	if e == nil || e.Gadget == nil {
		return false
	}
	next, ok := InsertIfAdmitted(e.Gadget, e.Text, ch, e.Measure)
	if !ok {
		return false
	}
	e.Text = next
	if e.Gadget != nil {
		e.Gadget.Text = next
	}
	return true
}

// Paste replaces the editor contents with the clipboard per CF_TEXT [07 §2][07 §4] C4.
func (e *Editor) Paste(clipboard string) {
	if e == nil || e.Gadget == nil {
		return
	}
	next := PasteText(e.Gadget, clipboard, e.Measure)
	e.Text = next
	e.Gadget.Text = next
}

// PasteIntoBuffer pastes into a 128-byte buffer simulation [07 §2] C4.
func (e *Editor) PasteIntoBuffer(buf *[editorMaxBuffer]byte, clipboard string) int {
	if e == nil || e.Gadget == nil || buf == nil {
		return 0
	}
	return PasteIntoBuffer(buf, e.Gadget, clipboard, e.Measure)
}
