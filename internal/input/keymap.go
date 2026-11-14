package input

import (
	"errors"
	"fmt"
	"strings"
)

// Keyboard rebinding for the battle's shortcut tokens
// (docs/DESIGN_INTERFACE_HUD_INPUT.md §3.6 "Rebinding"). A KeyMap rewrites a
// queued token before any battle consumer reads it, so the palette, TALK's
// opener and the residual hotkey table keep their retail vocabulary and never
// learn that a key was moved. It is a host preference: nothing here reaches
// the simulation, a digest or a save [I6].

// Chord is one key press as the battle's shortcut tokens identify it. The
// producer folds Alt+key into the plain key and composes Ctrl only into
// letters, digits and function keys, so a chord carries Ctrl and Shift and
// nothing else [07 §2][07 R-CAM-01 §14].
type Chord struct {
	Key         Key
	Ctrl, Shift bool
}

// Action groups, in display order.
const (
	GroupOrders    = "Orders"
	GroupSelection = "Selection"
	GroupCamera    = "Camera"
	GroupGame      = "Game"
)

// The keyboard profiles. Retail is the retail keys; Community is the same
// keys, since ProTA's documented controls change what keys do, not which keys
// do it; Zero adds TA Zero's documented Z for the previous build page.
const (
	ProfileRetail    = "retail"
	ProfileCommunity = "community"
	ProfileZero      = "zero"
)

// Action is one rebindable battle shortcut and the retail keys that reach it.
type Action struct {
	ID, Label, Group string
	// Default holds the retail keys. The first is the token a rebound key
	// emits unless another default matches the pressed chord's Shift.
	Default []Chord
	// Fixed actions are shown for reference only. Their keys are held-key
	// queries or modifier-read multiplexers that the token rewrite cannot
	// reach, and no action may take them.
	Fixed bool
	// AnyShift marks a command-palette order key. The palette matches its
	// authored letter without regard to case and the order reads Shift live,
	// so a letter bound to it also answers with Shift held, and that press
	// emits the shifted letter [07 R-WGT-01 §3].
	AnyShift bool
	// DragKey marks a key read only while a build row or grid holds the
	// pointer (DESIGN_INTERFACE_HUD_INPUT §3.11). A row is usually queued
	// with Shift held, so a letter bound to it also answers with Shift held,
	// as an order key does; the press emits the action's own token.
	DragKey bool
	// shiftUp marks a row that acts only with Shift up (F1 and F2 in the
	// battle's dispatcher); a shifted chord could never reach it, so none is
	// bound to it.
	shiftUp bool
}

// Actions returns the catalogue in display order.
func Actions() []Action {
	out := make([]Action, len(catalogue))
	for i, a := range catalogue {
		a.Default = append([]Chord(nil), a.Default...)
		out[i] = a
	}
	return out
}

// LookupAction returns the catalogued action with this identifier.
func LookupAction(id string) (Action, bool) {
	i, ok := actionIndex[id]
	if !ok {
		return Action{}, false
	}
	a := catalogue[i]
	a.Default = append([]Chord(nil), a.Default...)
	return a, true
}

// Profiles lists the keyboard profiles in display order.
func Profiles() []string { return []string{ProfileRetail, ProfileCommunity, ProfileZero} }

// normalizeProfile maps a stored profile name onto a known profile; the empty
// and unknown names are retail.
func normalizeProfile(name string) string {
	switch p := strings.ToLower(strings.TrimSpace(name)); p {
	case ProfileCommunity, ProfileZero:
		return p
	}
	return ProfileRetail
}

// ProfileDefaults returns every action's keys under the profile, Fixed
// actions included.
func ProfileDefaults(profile string) map[string][]Chord {
	out := make(map[string][]Chord, len(catalogue))
	for _, a := range catalogue {
		out[a.ID] = profileKeys(normalizeProfile(profile), a)
	}
	return out
}

func profileKeys(profile string, a Action) []Chord {
	keys := append([]Chord(nil), a.Default...)
	if profile == ProfileZero {
		keys = append(keys, zeroExtras[a.ID]...)
	}
	return keys
}

// zeroExtras are the keys TA Zero's author documents beyond retail's: `Z`
// steps the build menu back like `,` (research/extensions/ta-zero-engine.md,
// "Documented engine-level behavior").
var zeroExtras = map[string][]Chord{
	"prevBuildPage": {{Key: KeyZ}},
}

// KeyMap is a profile with the player's rebound actions applied. A nil map is
// the retail keys.
type KeyMap struct {
	profile string
	// keys holds each non-Fixed action's chords; owner is its inverse, with
	// the Fixed actions' chords added so they cannot be taken.
	keys  map[string][]Chord
	owner map[Chord]string
}

// NewKeyMap starts from the profile ("retail" for "" and unknown names) and
// rebinds each overridden action in catalogue order, so the result does not
// depend on the map's iteration order. Unknown actions and chords that are
// not distinct battle keys are ignored.
func NewKeyMap(profile string, overrides map[string][]Chord) *KeyMap {
	m := &KeyMap{profile: normalizeProfile(profile), keys: make(map[string][]Chord, len(catalogue))}
	for _, a := range catalogue {
		if !a.Fixed {
			m.keys[a.ID] = profileKeys(m.profile, a)
		}
	}
	m.rebuild()
	for _, a := range catalogue {
		if keys, ok := overrides[a.ID]; ok && !a.Fixed {
			m.bind(a, keys)
		}
	}
	return m
}

// Profile is the normalized profile name.
func (m *KeyMap) Profile() string {
	if m == nil {
		return ProfileRetail
	}
	return m.profile
}

// Keys returns the chords bound to an action, or nil for an unknown one.
func (m *KeyMap) Keys(id string) []Chord {
	i, ok := actionIndex[id]
	if !ok {
		return nil
	}
	a := catalogue[i]
	if m == nil || a.Fixed {
		return append([]Chord(nil), a.Default...)
	}
	return append([]Chord{}, m.keys[id]...)
}

// Owner returns the action a pressed chord reaches: its explicit binding,
// else, for a shifted letter, the AnyShift order bound to the plain letter.
func (m *KeyMap) Owner(c Chord) (string, bool) {
	if m == nil {
		return retailOwnerOf(c)
	}
	if id, ok := m.owner[c]; ok {
		return id, true
	}
	if plain, ok := unshifted(c); ok {
		if id, ok := m.owner[plain]; ok && catalogue[actionIndex[id]].anyShiftLetter() {
			return id, true
		}
	}
	return "", false
}

// Rebind binds exactly keys to the action. A chord belongs to one action, so
// each chord another action held is taken from it; displaced lists those
// actions in the order their chords were taken. Chords that are not distinct
// battle keys, and chords of Fixed actions, are ignored; when that leaves
// nothing of a non-empty request the action keeps its keys. An empty request
// unbinds the action. A shifted chord is ignored for a row that acts only
// with Shift up. Fixed and unknown actions are never rebound.
func (m *KeyMap) Rebind(id string, keys []Chord) (displaced []string) {
	i, ok := actionIndex[id]
	if m == nil || !ok || catalogue[i].Fixed {
		return nil
	}
	return m.bind(catalogue[i], keys)
}

// Reset returns the action to the profile's keys, taking them back from any
// action that holds one now.
func (m *KeyMap) Reset(id string) {
	i, ok := actionIndex[id]
	if m == nil || !ok || catalogue[i].Fixed {
		return
	}
	m.bind(catalogue[i], profileKeys(m.profile, catalogue[i]))
}

// Overrides returns only the actions whose keys differ from the profile's,
// for saving. An unbound action appears with an empty, non-nil list.
func (m *KeyMap) Overrides() map[string][]Chord {
	out := map[string][]Chord{}
	if m == nil {
		return out
	}
	for _, a := range catalogue {
		if a.Fixed {
			continue
		}
		if keys := m.keys[a.ID]; !sameChords(keys, profileKeys(m.profile, a)) {
			out[a.ID] = append([]Chord{}, keys...)
		}
	}
	return out
}

// Translate maps a physical token onto the token the battle understands.
// A chord bound to an action becomes that action's retail token; a press
// that is already one of the action's retail keys passes unchanged, which is
// what makes the retail profile the identity. A chord that is some action's
// retail key but reaches no action now is dropped (false): moving Attack
// from A to Q stops A from attacking. Every other token passes unchanged.
func (m *KeyMap) Translate(t Token) (Token, bool) {
	if m == nil {
		return t, true
	}
	c, ok := ChordOf(t)
	if !ok {
		return t, true
	}
	if id, ok := m.Owner(c); ok {
		a := catalogue[actionIndex[id]]
		if a.Fixed || a.retailKey(c) {
			return t, true
		}
		return a.logical(c), true
	}
	if _, ok := retailOwnerOf(c); ok {
		return Token{}, false
	}
	return t, true
}

// TranslatePending rewrites the oldest queued token of s through Translate,
// in place, so every consumer that peeks the ring's head this pass sees the
// same translated token. A dropped token is removed and reported; the caller
// treats it as claimed, so the next queued token waits for the next pass
// exactly as it would behind any consumed key [07 §2].
func (m *KeyMap) TranslatePending(s *State) (dropped bool) {
	if m == nil || s == nil || s.tokens.Len() == 0 {
		return false
	}
	q := &s.tokens
	out, keep := m.Translate(q.items[q.read])
	if !keep {
		q.Dequeue()
		return true
	}
	q.items[q.read] = out
	return false
}

// bind is Rebind for a known, non-Fixed action.
func (m *KeyMap) bind(a Action, keys []Chord) (displaced []string) {
	norm := make([]Chord, 0, len(keys))
	for _, k := range keys {
		c, ok := k.Normalize()
		if !ok {
			continue
		}
		if a.anyShiftLetter() && c.Shift && isLetter(c.Key) {
			c.Shift = false
		}
		if a.shiftUp && c.Shift {
			continue
		}
		if owner, taken := m.owner[c]; taken && catalogue[actionIndex[owner]].Fixed {
			continue
		}
		if !containsChord(norm, c) {
			norm = append(norm, c)
		}
	}
	if len(keys) != 0 && len(norm) == 0 {
		return nil
	}
	for _, c := range norm {
		owner, taken := m.owner[c]
		if !taken || owner == a.ID {
			continue
		}
		m.keys[owner] = removeChord(m.keys[owner], c)
		if !containsString(displaced, owner) {
			displaced = append(displaced, owner)
		}
	}
	m.keys[a.ID] = norm
	m.rebuild()
	return displaced
}

// anyShiftLetter reports whether a letter bound to the action also answers
// with Shift held: an order key or a build-drag key.
func (a Action) anyShiftLetter() bool { return a.AnyShift || a.DragKey }

func (m *KeyMap) rebuild() {
	m.owner = make(map[Chord]string, len(catalogue)*2)
	for _, a := range catalogue {
		keys := m.keys[a.ID]
		if a.Fixed {
			keys = a.Default
		}
		for _, c := range keys {
			if _, taken := m.owner[c]; !taken {
				m.owner[c] = a.ID
			}
		}
	}
}

// retailKey reports whether the pressed chord is one of the action's own
// retail keys, counting an AnyShift order's shifted letter.
func (a Action) retailKey(c Chord) bool {
	if containsChord(a.Default, c) {
		return true
	}
	plain, ok := unshifted(c)
	return ok && a.AnyShift && containsChord(a.Default, plain)
}

// logical is the retail token a rebound press of this action emits: the first
// default, shifted for an AnyShift order when the press was, or otherwise the
// first default whose Shift matches the press, so a speed key bound to a plain
// letter still emits `=` rather than `+`.
func (a Action) logical(pressed Chord) Token {
	target := a.Default[0]
	if a.AnyShift {
		if isLetter(target.Key) && !target.Ctrl {
			target.Shift = pressed.Shift
		}
	} else {
		for _, d := range a.Default {
			if d.Shift == pressed.Shift {
				target = d
				break
			}
		}
	}
	t, _ := tokenOf(target)
	return t
}

// retailOwnerOf is Owner for the retail keys.
func retailOwnerOf(c Chord) (string, bool) {
	if id, ok := retailOwner[c]; ok {
		return id, true
	}
	if plain, ok := unshifted(c); ok {
		if id, ok := retailOwner[plain]; ok && catalogue[actionIndex[id]].AnyShift {
			return id, true
		}
	}
	return "", false
}

// unshifted is a shifted plain letter's unshifted chord.
func unshifted(c Chord) (Chord, bool) {
	if c.Shift && !c.Ctrl && isLetter(c.Key) {
		return Chord{Key: c.Key}, true
	}
	return Chord{}, false
}

// ---------------------------------------------------------------------------
// Chords and tokens.

func isLetter(k Key) bool   { return k >= KeyA && k <= KeyZ }
func isDigit(k Key) bool    { return k >= Key0 && k <= Key9 }
func isFunction(k Key) bool { return k >= KeyF1 && k <= KeyF12 }

// composable keys are the ones the producer composes Ctrl into [07 §2].
func composable(k Key) bool { return isLetter(k) || isDigit(k) || isFunction(k) }

// editKey lists the keys the producer queues as edit tokens without Ctrl. It
// mirrors the platform adapter's special-key set (internal/platform/ebitenapp,
// keyboardTokenKey); every other key reaches the ring as a character.
func editKey(k Key) bool {
	switch k {
	case KeyBackspace, KeyDelete, KeyInsert, KeyHome, KeyEnd, KeyPrior, KeyNext, KeyPause,
		KeyLeft, KeyRight, KeyUp, KeyDown, KeyTab, KeyEnter, KeyEscape:
		return true
	}
	return isFunction(k)
}

// shiftedDigits are the characters Shift+0..9 translate to on the US layout
// the retail install assumes; retail's own label keys `!` `#` `*` are three of
// them [07 R-CAM-01 §14].
var shiftedDigits = [10]rune{')', '!', '@', '#', '$', '%', '^', '&', '*', '('}

// charPairs are the punctuation keys' unshifted and shifted characters.
var charPairs = map[Key][2]rune{
	KeyMinus:        {'-', '_'},
	KeyEqual:        {'=', '+'},
	KeyBackquote:    {'`', '~'},
	KeyComma:        {',', '<'},
	KeyPeriod:       {'.', '>'},
	KeyLeftBracket:  {'[', '{'},
	KeyRightBracket: {']', '}'},
}

// charOf is the character a character key translates to.
func charOf(k Key, shift bool) (rune, bool) {
	switch {
	case isLetter(k):
		if shift {
			return 'A' + rune(k-KeyA), true
		}
		return 'a' + rune(k-KeyA), true
	case isDigit(k):
		if shift {
			return shiftedDigits[k-Key0], true
		}
		return '0' + rune(k-Key0), true
	case k == KeySpace:
		return ' ', !shift
	}
	if pair, ok := charPairs[k]; ok {
		if shift {
			return pair[1], true
		}
		return pair[0], true
	}
	return 0, false
}

// Normalize returns the chord the battle can tell apart from this key press,
// for a key capture: Shift is dropped where the token cannot carry it (with
// Ctrl, on function and edit keys, on Space), Ctrl where the producer does
// not compose it, and the keypad's + and - become the characters they type.
// Modifier keys alone and keys the producer never queues are not chords.
func (c Chord) Normalize() (Chord, bool) {
	switch c.Key {
	case KeyNumpadAdd:
		c.Key, c.Shift = KeyEqual, true
	case KeyNumpadSubtract:
		c.Key, c.Shift = KeyMinus, false
	}
	if c.Ctrl {
		if composable(c.Key) {
			c.Shift = false
			return c, true
		}
		// Ctrl+Tab queues Tab, and Ctrl+` its plain character [07 §2].
		c.Ctrl, c.Shift = false, false
	}
	if editKey(c.Key) || c.Key == KeySpace {
		c.Shift = false
		return c, true
	}
	if _, ok := charOf(c.Key, c.Shift); ok {
		return c, true
	}
	return Chord{}, false
}

// tokenOf is the token the producer queues for a chord.
func tokenOf(c Chord) (Token, bool) {
	c, ok := c.Normalize()
	if !ok {
		return Token{}, false
	}
	if c.Ctrl {
		return Token{Kind: TokenEdit, Key: c.Key, Ctrl: true}, true
	}
	if editKey(c.Key) {
		return Token{Kind: TokenEdit, Key: c.Key}, true
	}
	r, _ := charOf(c.Key, c.Shift)
	return Token{Kind: TokenText, Rune: r}, true
}

// ChordOf identifies the chord that produced a token, for translation and for
// a capture read from the token stream. A character token's Shift is its
// case, and a shifted digit or punctuation character is read on the US
// layout; a token no chord produces reports false.
func ChordOf(t Token) (Chord, bool) {
	switch t.Kind {
	case TokenEdit:
		if t.Ctrl {
			if composable(t.Key) {
				return Chord{Key: t.Key, Ctrl: true}, true
			}
			return Chord{}, false
		}
		if editKey(t.Key) {
			return Chord{Key: t.Key}, true
		}
	case TokenText:
		return chordOfRune(t.Rune)
	}
	return Chord{}, false
}

func chordOfRune(r rune) (Chord, bool) {
	switch {
	case r >= 'a' && r <= 'z':
		return Chord{Key: KeyA + Key(r-'a')}, true
	case r >= 'A' && r <= 'Z':
		return Chord{Key: KeyA + Key(r-'A'), Shift: true}, true
	case r >= '0' && r <= '9':
		return Chord{Key: Key0 + Key(r-'0')}, true
	}
	for d, s := range shiftedDigits {
		if r == s {
			return Chord{Key: Key0 + Key(d), Shift: true}, true
		}
	}
	for _, k := range []Key{KeyMinus, KeyEqual, KeyBackquote, KeyComma, KeyPeriod, KeyLeftBracket, KeyRightBracket} {
		pair := charPairs[k]
		if r == pair[0] {
			return Chord{Key: k}, true
		}
		if r == pair[1] {
			return Chord{Key: k, Shift: true}, true
		}
	}
	// The battle decoder reads these control characters as their keys
	// (battleShortcutKeyboard), so a rebind treats them alike.
	switch r {
	case ' ':
		return Chord{Key: KeySpace}, true
	case '\t':
		return Chord{Key: KeyTab}, true
	case '\r', '\n':
		return Chord{Key: KeyEnter}, true
	case '\x1b':
		return Chord{Key: KeyEscape}, true
	}
	return Chord{}, false
}

// ---------------------------------------------------------------------------
// Written and displayed forms.

// keyNames are the canonical written key names, keyLabels the displayed ones
// where they differ from the upper-cased name, and keyByName the parser's
// table, which also takes a few common aliases.
var keyNames, keyLabels, keyByName = buildKeyNames()

func buildKeyNames() (names, labels map[Key]string, byName map[string]Key) {
	names, labels, byName = map[Key]string{}, map[Key]string{}, map[string]Key{}
	for k := KeyA; k <= KeyZ; k++ {
		names[k] = string(rune('a' + (k - KeyA)))
	}
	for k := Key0; k <= Key9; k++ {
		names[k] = string(rune('0' + (k - Key0)))
	}
	for k := KeyF1; k <= KeyF12; k++ {
		names[k] = fmt.Sprintf("f%d", k-KeyF1+1)
	}
	for _, e := range []struct {
		key         Key
		name, label string
	}{
		{KeyLeft, "left", "Left"}, {KeyUp, "up", "Up"}, {KeyRight, "right", "Right"}, {KeyDown, "down", "Down"},
		{KeyHome, "home", "Home"}, {KeyEnd, "end", "End"},
		{KeyPrior, "pageup", "Page Up"}, {KeyNext, "pagedown", "Page Down"},
		{KeyInsert, "insert", "Insert"}, {KeyDelete, "delete", "Delete"}, {KeyBackspace, "backspace", "Backspace"},
		{KeyTab, "tab", "Tab"}, {KeySpace, "space", "Space"}, {KeyEnter, "enter", "Enter"},
		{KeyEscape, "escape", "Esc"}, {KeyPause, "pause", "Pause"},
		{KeyMinus, "-", "-"}, {KeyEqual, "=", "="}, {KeyBackquote, "`", "`"},
		{KeyComma, ",", ","}, {KeyPeriod, ".", "."},
		{KeyLeftBracket, "[", "["}, {KeyRightBracket, "]", "]"},
	} {
		names[e.key], labels[e.key] = e.name, e.label
	}
	for k, name := range names {
		byName[name] = k
	}
	for alias, k := range map[string]Key{
		"esc": KeyEscape, "return": KeyEnter, "pgup": KeyPrior, "pgdn": KeyNext,
		"del": KeyDelete, "ins": KeyInsert, "minus": KeyMinus, "equal": KeyEqual,
		"equals": KeyEqual, "backquote": KeyBackquote, "grave": KeyBackquote,
		"comma": KeyComma, "period": KeyPeriod,
		"leftbracket": KeyLeftBracket, "rightbracket": KeyRightBracket,
	} {
		byName[alias] = k
	}
	return names, labels, byName
}

// String is the canonical written form ("ctrl+a", "shift+t", "f5", ","),
// which ParseChord reads back to the same chord.
func (c Chord) String() string {
	name := keyNames[c.Key]
	if name == "" {
		return ""
	}
	if c.Shift {
		name = "shift+" + name
	}
	if c.Ctrl {
		name = "ctrl+" + name
	}
	return name
}

// Label is the displayed form: "Ctrl+A", "Shift+T", "F5", ",", "Pause". A
// shifted digit or punctuation key shows the US-layout character it types.
func (c Chord) Label() string {
	label := keyLabels[c.Key]
	if label == "" {
		label = strings.ToUpper(keyNames[c.Key])
	}
	if label == "" {
		return ""
	}
	switch {
	case c.Ctrl:
		return "Ctrl+" + label
	case c.Shift && isLetter(c.Key):
		return "Shift+" + label
	case c.Shift:
		if r, ok := charOf(c.Key, true); ok {
			return string(r)
		}
		return "Shift+" + label
	}
	return label
}

// ParseChord reads a written chord: modifiers ("ctrl", "shift") and a key name
// joined by '+', in any case. A shifted character may be written as itself
// ("!", "~", "+"). Alt is refused because the battle cannot see it, as is any
// combination the token stream does not distinguish, such as "ctrl+tab" or
// "shift+f5".
func ParseChord(s string) (Chord, error) {
	text := strings.ToLower(strings.TrimSpace(s))
	if text == "" {
		return Chord{}, errors.New("nanolathe: key chord is empty")
	}
	var modPart, keyPart string
	switch {
	case text == "+":
		keyPart = "+"
	case strings.HasSuffix(text, "++"):
		modPart, keyPart = text[:len(text)-2], "+"
	default:
		if i := strings.LastIndexByte(text, '+'); i >= 0 {
			modPart, keyPart = text[:i], text[i+1:]
		} else {
			keyPart = text
		}
	}
	var c Chord
	if modPart != "" {
		for _, mod := range strings.Split(modPart, "+") {
			switch strings.TrimSpace(mod) {
			case "ctrl", "control":
				c.Ctrl = true
			case "shift":
				c.Shift = true
			case "alt":
				return Chord{}, fmt.Errorf("nanolathe: key chord %q: the battle folds Alt into the plain key", s)
			default:
				return Chord{}, fmt.Errorf("nanolathe: key chord %q: unknown modifier %q", s, mod)
			}
		}
	}
	keyPart = strings.TrimSpace(keyPart)
	if k, ok := keyByName[keyPart]; ok {
		c.Key = k
	} else if r := []rune(keyPart); len(r) == 1 {
		shifted, ok := chordOfRune(r[0])
		if !ok || !shifted.Shift || isLetter(shifted.Key) {
			return Chord{}, fmt.Errorf("nanolathe: key chord %q: unknown key %q", s, keyPart)
		}
		c.Key, c.Shift = shifted.Key, true
	} else {
		return Chord{}, fmt.Errorf("nanolathe: key chord %q: unknown key %q", s, keyPart)
	}
	n, ok := c.Normalize()
	if !ok || n != c {
		if ok {
			return Chord{}, fmt.Errorf("nanolathe: key chord %q: the battle receives it as %q", s, n.String())
		}
		return Chord{}, fmt.Errorf("nanolathe: key chord %q: not a battle key", s)
	}
	return c, nil
}

// ---------------------------------------------------------------------------
// The catalogue.

func sameChords(a, b []Chord) bool {
	if len(a) != len(b) {
		return false
	}
	for _, c := range a {
		if !containsChord(b, c) {
			return false
		}
	}
	return true
}

func containsChord(list []Chord, c Chord) bool {
	for _, x := range list {
		if x == c {
			return true
		}
	}
	return false
}

func removeChord(list []Chord, c Chord) []Chord {
	out := list[:0:0]
	for _, x := range list {
		if x != c {
			out = append(out, x)
		}
	}
	return out
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func mustChords(written ...string) []Chord {
	out := make([]Chord, len(written))
	for i, s := range written {
		c, err := ParseChord(s)
		if err != nil {
			panic(err)
		}
		out[i] = c
	}
	return out
}

// catalogue is every battle key the rewrite knows, from evidence only: the
// census rows of DESIGN_INTERFACE_HUD_INPUT §3.6 [07 R-CAM-01 §2][07 R-CAM-01 §14],
// the command palette's quick keys as ARMGEN.GUI and CORGEN.GUI author them
// (identical in both) [07 R-WGT-01 §3], and the host's own bindings on the
// same path (F9, F10, Page Up/Down, and the build-drag gap's `[` and `]`).
var catalogue = buildCatalogue()

func buildCatalogue() []Action {
	order := func(id, label, key string) Action {
		return Action{ID: id, Label: label, Group: GroupOrders, Default: mustChords(key), AnyShift: true}
	}
	actions := []Action{
		order("move", "Move", "m"),
		order("attack", "Attack", "a"),
		order("patrol", "Patrol", "p"),
		order("guard", "Guard", "g"),
		order("stop", "Stop", "s"),
		order("repair", "Repair", "r"),
		order("reclaim", "Reclaim", "e"),
		order("capture", "Capture", "c"),
		order("load", "Load", "l"),
		order("unload", "Unload", "u"),
		order("dgun", "D-gun", "d"),
		order("fireOrders", "Fire orders", "f"),
		order("moveOrders", "Move orders", "v"),
		order("onOff", "On/off", "x"),
		order("cloak", "Cloak", "k"),
		order("ordersPage", "Orders page", "o"),
		order("buildPage", "Build page", "b"),
		{ID: "prevBuildPage", Label: "Previous build page", Group: GroupOrders, Default: mustChords(",", "pagedown")},
		{ID: "nextBuildPage", Label: "Next build page", Group: GroupOrders, Default: mustChords(".", "pageup")},
		{ID: "selfDestruct", Label: "Self-destruct", Group: GroupOrders, Default: mustChords("ctrl+d")},
		{ID: "buildGapNarrower", Label: "Build drag: narrower gap", Group: GroupOrders, Default: mustChords("[", "{"), DragKey: true},
		{ID: "buildGapWider", Label: "Build drag: wider gap", Group: GroupOrders, Default: mustChords("]", "}"), DragKey: true},

		{ID: "selectAll", Label: "Select all units", Group: GroupSelection, Default: mustChords("ctrl+a")},
		{ID: "selectCommander", Label: "Select commander", Group: GroupSelection, Default: mustChords("ctrl+c")},
		{ID: "selectOnScreen", Label: "Select units on screen", Group: GroupSelection, Default: mustChords("ctrl+s")},
		{ID: "selectSameType", Label: "Select same type", Group: GroupSelection, Default: mustChords("ctrl+z")},
	}
	for _, letter := range "BEFGHIJKLMNOPQRTUVWXY" {
		actions = append(actions, Action{
			ID: "category" + string(letter), Label: "Select CTRL_" + string(letter), Group: GroupSelection,
			Default: mustChords("ctrl+" + strings.ToLower(string(letter))),
		})
	}
	actions = append(actions,
		Action{ID: "recallGroup", Label: "Recall group or build page", Group: GroupSelection, Fixed: true,
			Default: mustChords("1", "2", "3", "4", "5", "6", "7", "8", "9")},
		Action{ID: "assignGroup", Label: "Assign group", Group: GroupSelection, Fixed: true,
			Default: mustChords("ctrl+1", "ctrl+2", "ctrl+3", "ctrl+4", "ctrl+5", "ctrl+6", "ctrl+7", "ctrl+8", "ctrl+9")},
		Action{ID: "cancel", Label: "Cancel or deselect", Group: GroupSelection, Fixed: true, Default: mustChords("escape")},

		Action{ID: "trackNext", Label: "Follow next selected unit", Group: GroupCamera, Default: mustChords("t")},
		Action{ID: "trackPrevious", Label: "Follow previous selected unit", Group: GroupCamera, Default: mustChords("shift+t")},
		Action{ID: "nextUnit", Label: "Next unvisited unit", Group: GroupCamera, Default: mustChords("n")},
		Action{ID: "messageSource", Label: "Go to message source", Group: GroupCamera, Default: mustChords("f3")},
	)
	for slot := 1; slot <= 4; slot++ {
		key := fmt.Sprintf("f%d", slot+4)
		actions = append(actions,
			Action{ID: fmt.Sprintf("storeBookmark%d", slot), Label: fmt.Sprintf("Store camera bookmark %d", slot), Group: GroupCamera, Default: mustChords("ctrl+" + key)},
			Action{ID: fmt.Sprintf("recallBookmark%d", slot), Label: fmt.Sprintf("Recall camera bookmark %d", slot), Group: GroupCamera, Default: mustChords(key)},
		)
	}
	actions = append(actions,
		Action{ID: "zoomStep", Label: "Zoom step", Group: GroupCamera, Default: mustChords("f9")},
		Action{ID: "scroll", Label: "Scroll the map", Group: GroupCamera, Fixed: true, Default: mustChords("left", "up", "right", "down")},

		Action{ID: "options", Label: "Options menu", Group: GroupGame, Default: mustChords("f2"), shiftUp: true},
		Action{ID: "optionsTab", Label: "Options, resume or megamap", Group: GroupGame, Default: mustChords("tab")},
		Action{ID: "pause", Label: "Pause", Group: GroupGame, Default: mustChords("pause")},
		Action{ID: "speedUp", Label: "Game speed up", Group: GroupGame, Default: mustChords("+", "=")},
		Action{ID: "speedDown", Label: "Game speed down", Group: GroupGame, Default: mustChords("-", "_")},
		Action{ID: "labelUnits", Label: "Label every unit", Group: GroupGame, Default: mustChords("`", "~", "!", "#", "*")},
		Action{ID: "unitInfo", Label: "Unit information", Group: GroupGame, Default: mustChords("f1"), shiftUp: true},
		Action{ID: "scorePanel", Label: "Pin score panel", Group: GroupGame, Default: mustChords("f4")},
		Action{ID: "clearMessages", Label: "Clear messages", Group: GroupGame, Default: mustChords("f12")},
		Action{ID: "chat", Label: "Chat", Group: GroupGame, Default: mustChords("enter")},
		Action{ID: "renderer", Label: "Switch renderer", Group: GroupGame, Default: mustChords("f10")},
		Action{ID: "railSlide", Label: "Slide the side rails (hold)", Group: GroupGame, Fixed: true, Default: mustChords("space")},
	)
	return actions
}

// actionIndex and retailOwner index the catalogue by identifier and by
// retail key.
var actionIndex, retailOwner = indexCatalogue()

func indexCatalogue() (map[string]int, map[Chord]string) {
	index := make(map[string]int, len(catalogue))
	owner := make(map[Chord]string, len(catalogue)*2)
	for i, a := range catalogue {
		if _, dup := index[a.ID]; dup {
			panic("input: duplicate key action " + a.ID)
		}
		index[a.ID] = i
		for _, c := range a.Default {
			if prior, dup := owner[c]; dup {
				panic("input: key " + c.String() + " belongs to both " + prior + " and " + a.ID)
			}
			owner[c] = a.ID
		}
	}
	return index, owner
}
