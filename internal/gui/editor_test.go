package gui

import "testing"

func fixedMeasure(w int) func(string) int {
	return func(s string) int { return len(s) * w }
}

func TestEffectiveMaxCharsCap128(t *testing.T) {
	// [07 §4] C4 authored max is 16-bit capped at 128.
	cases := []struct {
		raw  int16
		want int
	}{
		{0, 0},
		{1, 1},
		{10, 10},
		{127, 127},
		{128, 128},
		{129, 128},
		{200, 128},
		{1000, 128},
		{-1, 0},
		{-100, 0},
	}
	for _, c := range cases {
		if got := EffectiveMaxChars(c.raw); got != c.want {
			t.Fatalf("EffectiveMaxChars(%d)=%d want %d [07 §4] C4", c.raw, got, c.want)
		}
		if lim := CopyLimit(EffectiveMaxChars(c.raw)); lim != c.want-1 && c.want > 0 || c.want == 0 && lim != 0 {
			// CopyLimit is maxchars-1; for 0 it is 0.
			if c.want == 0 && lim != 0 {
				t.Fatalf("CopyLimit(%d)=%d want 0", c.want, lim)
			}
			if c.want > 0 && lim != c.want-1 {
				t.Fatalf("CopyLimit(%d)=%d want %d", c.want, lim, c.want-1)
			}
		}
	}
	// Gadget already caps at load time, but runtime also caps.
	g := &Gadget{MaxChars: 200}
	if eff := EffectiveMaxChars(g.MaxChars); eff != 128 {
		t.Fatalf("gadget MaxChars 200 eff=%d want 128 [07 §4]", eff)
	}
	g2 := &Gadget{MaxChars: 128}
	if lim := CopyLimit(EffectiveMaxChars(g2.MaxChars)); lim != 127 {
		t.Fatalf("limit 128 ->127 got %d", lim)
	}
}

func TestAdmissionMatrix(t *testing.T) {
	// [07 §4] C4 filter bit on/off, each allowed-set exception, boundary widths.
	measure := fixedMeasure(6) // 6 px per byte; additive.

	// Helper to make gadget with filter and width.
	mk := func(max int16, attribs uint32, w int32) *Gadget {
		return &Gadget{MaxChars: max, Attribs: attribs, Rect: Rect{W: w}, Text: ""}
	}

	// Filter OFF: all printable bytes admitted (subject to cap/width).
	gOff := mk(20, 0, 100) // available 96, cap 19.
	for _, ch := range []byte{'A', 'z', '0', ' ', '_', '\'', '!', '@', '#', '.'} {
		if !CanInsert(gOff, "", ch, measure) {
			t.Fatalf("filter off should admit printable %q [07 §4]", ch)
		}
	}
	// Non-printable should be rejected even with filter off.
	for _, ch := range []byte{0x1F, 0x00, 0x7F, 0x80, 0x0A, 0x0D} {
		if CanInsert(gOff, "", ch, measure) {
			t.Fatalf("non-printable %02x should be rejected [07 §4]", ch)
		}
	}

	// Filter ON (bit 0x02): allowed set alnum plus exceptions.
	gOn := mk(20, 0x02, 100)
	// Alnum should pass.
	for _, ch := range []byte{'A', 'Z', 'a', 'z', '0', '9'} {
		if !CanInsert(gOn, "", ch, measure) {
			t.Fatalf("filter on should admit alnum %q [07 §4]", ch)
		}
	}
	// Exceptions should pass even though not alnum.
	for _, ch := range []byte{' ', '_', '\''} {
		if !CanInsert(gOn, "", ch, measure) {
			t.Fatalf("filter on should admit exception %q [07 §4]", ch)
		}
	}
	// Other printable punctuation should be filtered.
	for _, ch := range []byte{'!', '@', '#', '.', ',', '-', '+', ':', ';', '"', '(', ')'} {
		if CanInsert(gOn, "", ch, measure) {
			t.Fatalf("filter on should reject disallowed printable %q [07 §4]", ch)
		}
	}

	// CommonAttribs filter bit also enables filtering.
	gCommon := &Gadget{MaxChars: 20, CommonAttribs: 0x02, Rect: Rect{W: 100}}
	if CanInsert(gCommon, "", '!', measure) {
		t.Fatalf("CommonAttribs 0x02 should also filter '!' [07 §4]")
	}
	if !CanInsert(gCommon, "", 'A', measure) {
		t.Fatalf("CommonAttribs 0x02 should allow alnum")
	}
	if !CanInsert(gCommon, "", ' ', measure) {
		t.Fatalf("CommonAttribs 0x02 should allow space exception")
	}

	// Capacity boundary: maxchars-1.
	gCap := mk(5, 0, 100) // eff 5 => limit 4
	measureWide := fixedMeasure(1)
	cur := "ABC" // len 3
	if !CanInsert(gCap, cur, 'D', measureWide) {
		t.Fatalf("should admit when len 3 ->4 at limit 4 [07 §4]")
	}
	curFull := "ABCD" // len 4 == limit
	if CanInsert(gCap, curFull, 'E', measureWide) {
		t.Fatalf("should reject when at maxchars-1 already [07 §4]")
	}
	// At exactly limit-1, new char makes limit.
	cur = "AB" // 2 ->3
	if !CanInsert(gCap, cur, 'C', measureWide) {
		t.Fatalf("len 2 ->3 should admit")
	}

	// Width rule boundary: currentLen+newLen <= controlWidth-4 [07 §4]
	// controlWidth 20 => avail 16. measure 6 per char => 2 chars=12 fits, 3 chars=18 exceeds.
	gWidth := mk(20, 0, 20)                    // avail 16, cap large.
	cur = "AB"                                 // width 12
	if !CanInsert(gWidth, cur, 'C', measure) { // cur 12 + new 6 =18 >16 => reject
		// Actually AB=12, C=6 =>18 >16 so should reject.
	} else {
		t.Fatalf("width rule should reject when cur+new exceeds avail [07 §4]")
	}
	// Test exactly at boundary: avail 16, cur 10? Let's craft.
	cur = "A" // width 6
	// A(6)+B(6)=12 <=16 => admit
	if !CanInsert(gWidth, cur, 'B', measure) {
		t.Fatalf("width exactly within avail should admit [07 §4]")
	}
	// Now test boundary equality: avail 16, cur width 10 (not multiple of 6) use custom measure.
	custom := func(s string) int { return len(s) * 8 } // 8 per char
	gWidth2 := mk(20, 0, 20)                           // avail 16, custom 8 per char => 1 char 8 fits, 2 chars 16 fits exactly, 3 chars 24 >16 reject.
	if !CanInsert(gWidth2, "A", 'B', custom) {         // 8+8=16 <=16 admit
		t.Fatalf("boundary equality should admit (16<=16) [07 §4]")
	}
	if CanInsert(gWidth2, "AB", 'C', custom) { // 16+8=24>16 reject
		t.Fatalf("boundary exceed should reject [07 §4]")
	}
	// Zero/negative avail: controlWidth <=4 => avail 0 => only empty? But cur empty +1 char width 6 >0 => reject.
	gNarrow := mk(20, 0, 4) // avail 0
	if CanInsert(gNarrow, "", 'A', measure) {
		t.Fatalf("avail 0 should reject any insertion [07 §4]")
	}
	gNarrow2 := mk(20, 0, 3) // avail 0 (clamped)
	if CanInsert(gNarrow2, "", 'A', measure) {
		t.Fatalf("avail negative clamped to 0 should reject [07 §4]")
	}
	// Width vacuous when no authored width (0) => admission based on cap only.
	gNoWidth := mk(20, 0, 0)
	if !CanInsert(gNoWidth, "", '!', measure) {
		t.Fatalf("no width (0) should not enforce width rule [07 §4]")
	}
}

func TestPasteTrimToWidthLoop(t *testing.T) {
	// [07 §4] C4 paste copies at most maxchars-1 then trims until width fits [07 §2].
	measure := fixedMeasure(10)                   // 10 px per byte
	g := &Gadget{MaxChars: 10, Rect: Rect{W: 25}} // eff 10 => limit 9, controlWidth 25

	// Clipboard shorter than limit, but width exceeds -> trim loop.
	clip := "ABCDEFGH" // len 8 width 80 >25 => trim to 2 chars width 20 <=25
	got := PasteText(g, clip, measure)
	want := "AB"
	if got != want {
		t.Fatalf("paste trim loop: got %q want %q [07 §4] (clip %q len %d width %d cw %d)", got, want, clip, len(clip), measure(clip), g.Rect.W)
	}
	// Already fits: no trim.
	g2 := &Gadget{MaxChars: 10, Rect: Rect{W: 100}} // avail large
	got = PasteText(g2, clip, measure)              // width 80 <=100 => no trim
	if got != clip {
		t.Fatalf("paste no trim when fits: got %q want %q", got, clip)
	}
	// Trim iteratively one at a time: verify loop removes one byte per iteration.
	// Clipboard "ABC" width 30, cw 25 => remove C => "AB" width 20 fits => one removal after first measure.
	g3 := &Gadget{MaxChars: 10, Rect: Rect{W: 25}}
	got = PasteText(g3, "ABC", measure)
	if got != "AB" {
		t.Fatalf("paste trim one byte: got %q want %q", got, "AB")
	}
	// Edge: controlWidth zero => no trim constraint via cw<=0 path returns copy.
	gZero := &Gadget{MaxChars: 10, Rect: Rect{W: 0}}
	got = PasteText(gZero, "ABCDE", measure)
	if got != "ABCDE" {
		t.Fatalf("paste with cw 0 should not trim: got %q", got)
	}
	// Limit exceeds width triage: clipboard 20 bytes limit 9 caps before trim.
	gCap := &Gadget{MaxChars: 10, Rect: Rect{W: 100}} // limit 9, width 90 fits
	long := "0123456789ABCDEFGHIJ"                    // 20 bytes
	got = PasteText(gCap, long, measure)              // limit 9 => "012345678" width 90 <=100 => no further trim
	if got != long[:9] {
		t.Fatalf("paste limit before trim: got %q want %q", got, long[:9])
	}
	// Long clip + small width => both limit and trim.
	gBoth := &Gadget{MaxChars: 10, Rect: Rect{W: 25}} // limit 9, width 25 => after cap "012345678" width 90 >25 => trim to 2
	got = PasteText(gBoth, long, measure)
	if got != "01" {
		t.Fatalf("paste limit then width trim: got %q want %q", got, "01")
	}
}

func TestPasteTerminationPreservation(t *testing.T) {
	// [07 §2] buffer zero-filled, copy at most maxchars-1 keeps termination, trim keeps it.
	g := &Gadget{MaxChars: 6, Rect: Rect{W: 100}} // limit 5
	measure := fixedMeasure(6)
	clip := "HELLO WORLD" // 11 bytes, limit 5 => "HELLO"
	got := PasteText(g, clip, measure)
	if got != "HELLO" {
		t.Fatalf("termination after cap: got %q want %q", got, "HELLO")
	}
	if len(got) != 5 {
		t.Fatalf("len after cap = %d want 5 [07 §4]", len(got))
	}
	// Verify buffer simulation keeps zero termination beyond length [07 §2].
	var buf [128]byte
	// Fill with sentinel to ensure zero-fill works.
	for i := range buf {
		buf[i] = 0xFF
	}
	n := PasteIntoBuffer(&buf, g, clip, measure)
	if n != 5 {
		t.Fatalf("PasteIntoBuffer n=%d want 5", n)
	}
	if string(buf[:n]) != "HELLO" {
		t.Fatalf("buffer content %q want %q", string(buf[:n]), "HELLO")
	}
	if buf[n] != 0 {
		t.Fatalf("termination byte at %d = %02x want 00 [07 §2]", n, buf[n])
	}
	for i := n + 1; i < 128; i++ {
		if buf[i] != 0 {
			t.Fatalf("buffer beyond termination at %d = %02x want 00 [07 §2]", i, buf[i])
		}
	}
	// Paste with width trim also preserves termination.
	gNarrow := &Gadget{MaxChars: 10, Rect: Rect{W: 15}} // meas 6 per byte => "ABC" width 18 >15 trim to "AB"
	var buf2 [128]byte
	n = PasteIntoBuffer(&buf2, gNarrow, "ABC", measure)
	if string(buf2[:n]) != "AB" {
		t.Fatalf("trimmed buffer %q want AB", string(buf2[:n]))
	}
	if buf2[n] != 0 {
		t.Fatalf("trimmed termination =%02x want 00", buf2[n])
	}
	// Empty clipboard keeps termination (empty string, buf[0]==0)
	var buf3 [128]byte
	for i := range buf3 {
		buf3[i] = 0xAA
	}
	n = PasteIntoBuffer(&buf3, g, "", measure)
	if n != 0 || buf3[0] != 0 {
		t.Fatalf("empty paste should be empty terminated, n %d buf0 %02x", n, buf3[0])
	}
	// Limit zero => empty with termination.
	gZero := &Gadget{MaxChars: 1, Rect: Rect{W: 100}} // limit 0
	var buf4 [128]byte
	n = PasteIntoBuffer(&buf4, gZero, "ABC", measure)
	if n != 0 {
		t.Fatalf("limit 0 should produce empty, n %d", n)
	}
	if buf4[0] != 0 {
		t.Fatalf("limit 0 buffer not zero-terminated")
	}
}

func TestEditorHookWiring(t *testing.T) {
	// Verify dispatch hook is bound and respects filter + width [07 §4].
	if EditorAdmissionHook == nil {
		t.Fatalf("EditorAdmissionHook not wired [07 §4]")
	}
	g := &Gadget{MaxChars: 10, Attribs: 0x02, Rect: Rect{W: 100}, Text: ""}
	measureBkup := EditorMeasure
	EditorMeasure = fixedMeasure(6)
	defer func() { EditorMeasure = measureBkup }()
	if EditorAdmissionHook(g, 'A') != true {
		t.Fatalf("hook should admit alnum with filter on")
	}
	if EditorAdmissionHook(g, '!') != false {
		t.Fatalf("hook should reject '!' with filter on [07 §4]")
	}
	if EditorAdmissionHook(g, ' ') != true {
		t.Fatalf("hook exception space should be admitted")
	}
	// Width via hook: set narrow width to force reject.
	g.Rect.W = 10 // avail 6, char width 6 fits exactly for empty -> 0+6<=6 true
	if EditorAdmissionHook(g, 'A') != true {
		t.Fatalf("hook width exactly fits should admit")
	}
	g.Rect.W = 9 // avail 5 <6 => reject
	if EditorAdmissionHook(g, 'A') != false {
		t.Fatalf("hook width exceed should reject [07 §4]")
	}
	// Cap via hook: fill to limit.
	g.Rect.W = 100
	g.MaxChars = 3 // limit 2
	g.Text = "AB"  // already at limit
	if EditorAdmissionHook(g, 'C') != false {
		t.Fatalf("hook at cap should reject [07 §4]")
	}
}

func TestEditorStruct(t *testing.T) {
	g := &Gadget{MaxChars: 10, Attribs: 0, Rect: Rect{W: 100}, Text: "Hi"}
	ed := NewEditor(g, fixedMeasure(6))
	if ed.Text != "Hi" {
		t.Fatalf("NewEditor text %q want Hi", ed.Text)
	}
	if !ed.CanInsert('!') {
		t.Fatalf("Editor CanInsert should admit '!' without filter")
	}
	if !ed.Insert('!') || ed.Text != "Hi!" {
		t.Fatalf("Editor Insert failed, text %q", ed.Text)
	}
	// Filtered editor.
	g2 := &Gadget{MaxChars: 10, Attribs: 0x02, Rect: Rect{W: 100}, Text: ""}
	ed2 := NewEditor(g2, fixedMeasure(6))
	if ed2.CanInsert('!') {
		t.Fatalf("filtered editor should reject '!'")
	}
	if ed2.Insert('!') {
		t.Fatalf("filtered Insert should fail")
	}
	if ed2.Insert('A') != true || ed2.Text != "A" {
		t.Fatalf("filtered Insert A failed")
	}
	// Paste via struct replaces.
	ed2.Paste("HELLO")
	if ed2.Text != "HELLO" {
		t.Fatalf("Editor Paste want HELLO got %q", ed2.Text)
	}
	if g2.Text != "HELLO" {
		t.Fatalf("Gadget.Text not updated after paste")
	}
	// Paste with trim.
	g3 := &Gadget{MaxChars: 10, Rect: Rect{W: 12}, Text: ""}
	ed3 := NewEditor(g3, fixedMeasure(10)) // 10 per char => W12 fits only 1 char
	ed3.Paste("ABC")
	if ed3.Text != "A" {
		t.Fatalf("trimmed paste want A got %q", ed3.Text)
	}
}

func TestInsertIfAdmittedAndCap(t *testing.T) {
	g := &Gadget{MaxChars: 128, Rect: Rect{W: 1000}}
	// Build up to 127 via inserts; 128th should fail because limit is 127 (maxchars-1).
	cur := ""
	measure := fixedMeasure(1)
	for i := 0; i < 127; i++ {
		var ok bool
		cur, ok = InsertIfAdmitted(g, cur, 'A', measure)
		if !ok {
			t.Fatalf("insert %d should succeed [07 §4]", i)
		}
	}
	if len(cur) != 127 {
		t.Fatalf("after 127 inserts len=%d want 127", len(cur))
	}
	if _, ok := InsertIfAdmitted(g, cur, 'A', measure); ok {
		t.Fatalf("128th insert should be rejected at maxchars-1 [07 §4]")
	}
	// Paste at cap also respects limit.
	g2 := &Gadget{MaxChars: 128, Rect: Rect{W: 1000}}
	clip := string(make([]byte, 200))
	for i := range clip {
		clip = clip[:i] + "A" + clip[i+1:]
	}
	// Build a 200-byte 'A' string.
	long := ""
	for i := 0; i < 200; i++ {
		long += "A"
	}
	got := PasteText(g2, long, measure)
	if len(got) != 127 {
		t.Fatalf("paste at cap 128 should produce 127, got %d", len(got))
	}
}
