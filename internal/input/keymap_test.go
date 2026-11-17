package input

import (
	"slices"
	"testing"
)

func chord(t *testing.T, s string) Chord {
	t.Helper()
	c, err := ParseChord(s)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mustToken(t *testing.T, s string) Token {
	t.Helper()
	tok, ok := tokenOf(chord(t, s))
	if !ok {
		t.Fatalf("%s queues no token", s)
	}
	return tok
}

// TestRetailKeyMapIsIdentity: with no overrides the retail and community
// profiles hand every catalogued token, and every token outside the
// catalogue, to the battle unchanged — including a paste token's clipboard.
func TestRetailKeyMapIsIdentity(t *testing.T) {
	var tokens []Token
	for _, a := range Actions() {
		for _, c := range a.Default {
			tok, ok := tokenOf(c)
			if !ok {
				t.Fatalf("%s default %s queues no token", a.ID, c)
			}
			if back, ok := ChordOf(tok); !ok || back != c {
				t.Fatalf("%s default %s reads back as %v", a.ID, c, back)
			}
			tokens = append(tokens, tok)
			if a.AnyShift {
				shifted := c
				shifted.Shift = true
				tok, _ = tokenOf(shifted)
				tokens = append(tokens, tok)
			}
		}
	}
	tokens = append(tokens,
		Token{Kind: TokenText, Rune: 'z'}, Token{Kind: TokenText, Rune: 'N'}, Token{Kind: TokenText, Rune: '\t'},
		Token{Kind: TokenText, Rune: '\r'}, Token{Kind: TokenText, Rune: 'é'}, Token{Kind: TokenEdit, Key: KeyHome},
		Token{Kind: TokenEdit, Key: KeyF9, Ctrl: true}, Token{Kind: TokenEdit, Key: KeyV, Ctrl: true, Clipboard: ClipboardText{Text: "x", Available: true}},
	)
	var nilMap *KeyMap
	for _, m := range []*KeyMap{NewKeyMap("", nil), NewKeyMap(ProfileRetail, nil), NewKeyMap(ProfileCommunity, map[string][]Chord{}), nilMap} {
		for _, tok := range tokens {
			if got, ok := m.Translate(tok); !ok || got != tok {
				t.Fatalf("%s: %+v became %+v (%v)", m.Profile(), tok, got, ok)
			}
		}
		if n := len(m.Overrides()); n != 0 {
			t.Fatalf("%s: %d overrides with nothing rebound", m.Profile(), n)
		}
	}
}

// TestRebindMovesActionAndSuppressesOldKey: Attack moved from A to Q answers
// on q and Shift+Q with the palette's letter, and A no longer attacks.
func TestRebindMovesActionAndSuppressesOldKey(t *testing.T) {
	m := NewKeyMap(ProfileRetail, nil)
	if displaced := m.Rebind("attack", []Chord{chord(t, "q")}); len(displaced) != 0 {
		t.Fatalf("q displaced %v", displaced)
	}
	for press, want := range map[string]Token{
		"q": {Kind: TokenText, Rune: 'a'}, "shift+q": {Kind: TokenText, Rune: 'A'},
	} {
		if got, ok := m.Translate(mustToken(t, press)); !ok || got != want {
			t.Errorf("%s = %+v %v, want %+v", press, got, ok, want)
		}
	}
	for _, press := range []string{"a", "shift+a"} {
		if got, ok := m.Translate(mustToken(t, press)); ok {
			t.Errorf("%s still reaches the battle as %+v", press, got)
		}
	}
	if got := m.Overrides(); len(got) != 1 || !slices.Equal(got["attack"], []Chord{chord(t, "q")}) {
		t.Errorf("overrides = %v, want attack: q only", got)
	}
	// A rebound speed key emits the default whose Shift matches the press.
	m.Rebind("speedUp", []Chord{chord(t, "j"), chord(t, "shift+j")})
	if got, _ := m.Translate(mustToken(t, "j")); got.Rune != '=' {
		t.Errorf("j = %q, want =", got.Rune)
	}
	if got, _ := m.Translate(mustToken(t, "shift+j")); got.Rune != '+' {
		t.Errorf("J = %q, want +", got.Rune)
	}
	// The queued head is rewritten in place; a dropped head leaves the ring.
	s := NewState()
	s.EnqueueToken(mustToken(t, "a"))
	s.EnqueueToken(mustToken(t, "q"))
	if !m.TranslatePending(s) || s.PendingTokens() != 1 {
		t.Fatal("the unbound a was not dropped from the ring")
	}
	if m.TranslatePending(s) || s.PeekTokens()[0] != (Token{Kind: TokenText, Rune: 'a'}) {
		t.Fatalf("q was not rewritten in place: %+v", s.PeekTokens())
	}
}

// TestRebindDisplacesTheChordsOwner: a chord belongs to one action, and the
// saved overrides rebuild the same map.
func TestRebindDisplacesTheChordsOwner(t *testing.T) {
	m := NewKeyMap(ProfileRetail, nil)
	if displaced := m.Rebind("attack", []Chord{chord(t, "t")}); !slices.Equal(displaced, []string{"trackNext"}) {
		t.Fatalf("displaced = %v, want trackNext", displaced)
	}
	if keys := m.Keys("trackNext"); keys == nil || len(keys) != 0 {
		t.Fatalf("trackNext keys = %v, want none", keys)
	}
	if got, _ := m.Translate(mustToken(t, "t")); got.Rune != 'a' {
		t.Fatalf("t = %+v, want the attack letter", got)
	}
	// Shift+T still follows the previous unit: an explicit binding outranks
	// the order's shifted letter.
	if got, _ := m.Translate(mustToken(t, "shift+t")); got.Rune != 'T' {
		t.Fatalf("T = %+v, want T unchanged", got)
	}
	rebuilt := NewKeyMap(ProfileRetail, m.Overrides())
	for _, a := range Actions() {
		if !slices.Equal(rebuilt.Keys(a.ID), m.Keys(a.ID)) {
			t.Errorf("%s: rebuilt %v, want %v", a.ID, rebuilt.Keys(a.ID), m.Keys(a.ID))
		}
	}
	m.Reset("trackNext")
	if !slices.Equal(m.Keys("trackNext"), []Chord{chord(t, "t")}) || len(m.Keys("attack")) != 0 {
		t.Fatalf("reset: trackNext %v attack %v, want t and none", m.Keys("trackNext"), m.Keys("attack"))
	}
	// Fixed keys are never taken, and a request of nothing else is refused.
	if displaced := m.Rebind("attack", []Chord{chord(t, "1"), chord(t, "left")}); displaced != nil || len(m.Keys("attack")) != 0 {
		t.Fatalf("attack took a fixed key: %v", m.Keys("attack"))
	}
	if m.Rebind("recallGroup", []Chord{chord(t, "q")}); !slices.Equal(m.Keys("recallGroup"), mustChords("1", "2", "3", "4", "5", "6", "7", "8", "9")) {
		t.Fatal("a fixed action was rebound")
	}
	// F2 acts only with Shift up, so a shifted chord cannot reach it.
	if m.Rebind("options", []Chord{chord(t, "shift+o")}); !slices.Equal(m.Keys("options"), []Chord{chord(t, "f2")}) {
		t.Fatalf("options = %v, want F2 kept", m.Keys("options"))
	}
}

// TestZeroProfileZPagesBack: TA Zero's documented Z steps the build menu back
// like `,`; retail leaves z alone.
func TestZeroProfileZPagesBack(t *testing.T) {
	z := Token{Kind: TokenText, Rune: 'z'}
	zero := NewKeyMap("Zero", nil)
	if got, ok := zero.Translate(z); !ok || got != (Token{Kind: TokenText, Rune: ','}) {
		t.Fatalf("zero: z = %+v %v, want ,", got, ok)
	}
	if got, ok := zero.Translate(Token{Kind: TokenText, Rune: ','}); !ok || got.Rune != ',' {
		t.Fatalf("zero: , = %+v %v", got, ok)
	}
	if zero.Profile() != ProfileZero || len(zero.Overrides()) != 0 {
		t.Fatalf("zero profile %q overrides %v", zero.Profile(), zero.Overrides())
	}
	if !slices.Equal(Profiles(), []string{ProfileRetail, ProfileCommunity, ProfileZero}) {
		t.Fatalf("profiles = %v", Profiles())
	}
	defaults := ProfileDefaults(ProfileZero)
	if !slices.Equal(defaults["prevBuildPage"], mustChords(",", "pagedown", "z")) || !slices.Equal(ProfileDefaults("")["prevBuildPage"], mustChords(",", "pagedown")) {
		t.Fatalf("previous-page keys: zero %v, retail %v", defaults["prevBuildPage"], ProfileDefaults("")["prevBuildPage"])
	}
	if len(defaults) != len(Actions()) {
		t.Fatalf("zero defaults cover %d of %d actions", len(defaults), len(Actions()))
	}
	if got, ok := NewKeyMap(ProfileRetail, nil).Translate(z); !ok || got != z {
		t.Fatalf("retail: z = %+v %v, want z", got, ok)
	}
}

// TestParseChordRoundTrip: the written form reads back to the same chord,
// and combinations the token stream cannot tell apart are refused.
func TestParseChordRoundTrip(t *testing.T) {
	for written, want := range map[string]string{
		"ctrl+a": "ctrl+a", "Shift+T": "shift+t", "F5": "f5", "q": "q", ",": ",", "pause": "pause",
		"tab": "tab", "!": "shift+1", "+": "shift+=", "ctrl++": "", "~": "shift+`", "control+F8": "ctrl+f8",
		"PgUp": "pageup", " esc ": "escape",
	} {
		c, err := ParseChord(written)
		if want == "" {
			if err == nil {
				t.Errorf("%q parsed as %s", written, c)
			}
			continue
		}
		if err != nil || c.String() != want {
			t.Errorf("%q = %q (%v), want %q", written, c.String(), err, want)
		}
	}
	for _, bad := range []string{"", "alt+a", "ctrl+tab", "shift+f5", "ctrl+shift+a", "hyper+x", "numpad", "shift+space", "é"} {
		if c, err := ParseChord(bad); err == nil {
			t.Errorf("%q parsed as %s", bad, c)
		}
	}
	for _, a := range Actions() {
		for _, c := range a.Default {
			if back, err := ParseChord(c.String()); err != nil || back != c {
				t.Errorf("%s: %s reads back as %v (%v)", a.ID, c, back, err)
			}
		}
	}
	for c, want := range map[Chord]string{
		chord(t, "ctrl+a"): "Ctrl+A", chord(t, "shift+t"): "Shift+T", chord(t, "f5"): "F5",
		chord(t, ","): ",", chord(t, "pause"): "Pause", chord(t, "shift+1"): "!", chord(t, "q"): "Q",
	} {
		if got := c.Label(); got != want {
			t.Errorf("%s label = %q, want %q", c, got, want)
		}
	}
	if c, ok := (Chord{Key: KeyTab, Ctrl: true, Shift: true}).Normalize(); !ok || c != (Chord{Key: KeyTab}) {
		t.Errorf("Ctrl+Shift+Tab normalizes to %v", c)
	}
	if _, ok := (Chord{Key: KeyShift}).Normalize(); ok {
		t.Error("Shift alone is a chord")
	}
}

// TestBuildGapKeysRebind: a letter bound to a build-drag gap action answers
// with Shift held too, emitting the action's shifted or plain bracket, and
// the old bracket no longer reaches the gap.
func TestBuildGapKeysRebind(t *testing.T) {
	m := NewKeyMap(ProfileRetail, nil)
	m.Rebind("buildGapWider", []Chord{chord(t, "q")})
	for in, want := range map[rune]rune{'q': ']', 'Q': '}'} {
		if got, ok := m.Translate(Token{Kind: TokenText, Rune: in}); !ok || got != (Token{Kind: TokenText, Rune: want}) {
			t.Fatalf("%q became %+v (%v), want %q", in, got, ok, want)
		}
	}
	if _, ok := m.Translate(Token{Kind: TokenText, Rune: ']'}); ok {
		t.Fatal("the unbound ] still reaches the gap")
	}
	if got := m.Overrides()["buildGapWider"]; !slices.Equal(got, mustChords("q")) {
		t.Fatalf("saved binding %v", got)
	}
}
