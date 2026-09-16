package input

// TokenKind distinguishes translated text from an editing key. Tokens are
// ordered input events; held state remains on KeyboardState [07 §2].
type TokenKind uint8

const (
	TokenNone TokenKind = iota
	TokenText
	TokenEdit
)

// ClipboardText is an immutable clipboard snapshot attached to a paste token.
// Available distinguishes successful empty text from an unavailable format or
// failed read; only the former clears the editor [07 §2].
type ClipboardText struct {
	Text      string
	Available bool
}

// Token is one platform-neutral keyboard event. Text carries the platform's
// translated rune unchanged. The kind-3 editor accepts only its established
// byte domain; it performs no Unicode-to-legacy mapping [07 §2][07 R-WGT-01 §6].
type Token struct {
	Kind TokenKind
	Rune rune
	Key  Key
	// Ctrl distinguishes translated Ctrl-letter, digit and function-key tokens
	// from their plain counterparts, independently of later held state [07 §2].
	Ctrl bool
	// Clipboard accompanies Insert and Ctrl+V, without a screen-specific host call.
	Clipboard ClipboardText
}

const tokenRingSlots = 30

// TokenRing is the retail-shaped keyboard queue: one reserved ring slot leaves
// 29 tokens pending at most. A full queue refuses the new token and preserves
// the old history [07 §2].
type TokenRing struct {
	items [tokenRingSlots]Token
	read  uint8
	write uint8
}

// Enqueue appends one non-empty token when the reserved-slot ring permits it.
func (q *TokenRing) Enqueue(token Token) bool {
	if q == nil || token.Kind == TokenNone {
		return false
	}
	next := (q.write + 1) % tokenRingSlots
	if next == q.read {
		return false
	}
	q.items[q.write] = token
	q.write = next
	return true
}

// Dequeue returns the oldest queued token.
func (q *TokenRing) Dequeue() (Token, bool) {
	if q == nil || q.read == q.write {
		return Token{}, false
	}
	token := q.items[q.read]
	q.items[q.read] = Token{}
	q.read = (q.read + 1) % tokenRingSlots
	return token, true
}

// Len is the number of pending tokens.
func (q *TokenRing) Len() int {
	if q == nil {
		return 0
	}
	if q.write >= q.read {
		return int(q.write - q.read)
	}
	return tokenRingSlots - int(q.read-q.write)
}

// Drain returns the pending sequence in producer order and empties the ring.
func (q *TokenRing) Drain() []Token {
	if q == nil {
		return nil
	}
	out := make([]Token, 0, q.Len())
	for {
		token, ok := q.Dequeue()
		if !ok {
			return out
		}
		out = append(out, token)
	}
}

// Peek returns the pending sequence in producer order without consuming it.
// A consumer uses Discard after it has acted on the returned prefix, which
// keeps tokens following Escape available for the next service pass [07 R-WGT-01 §12].
func (q *TokenRing) Peek() []Token {
	if q == nil || q.Len() == 0 {
		return nil
	}
	out := make([]Token, 0, q.Len())
	for at := q.read; at != q.write; at = (at + 1) % tokenRingSlots {
		out = append(out, q.items[at])
	}
	return out
}

// Discard removes at most n oldest tokens and reports how many it removed.
func (q *TokenRing) Discard(n int) int {
	if q == nil || n <= 0 {
		return 0
	}
	removed := 0
	for removed < n && q.read != q.write {
		q.items[q.read] = Token{}
		q.read = (q.read + 1) % tokenRingSlots
		removed++
	}
	return removed
}
