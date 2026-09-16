package input

import "testing"

func TestTokenRingPreservesOrderAndRefusesTheThirtiethSlot(t *testing.T) {
	var q TokenRing
	for i := 0; i < 29; i++ {
		if !q.Enqueue(Token{Kind: TokenText, Rune: rune('a' + i)}) {
			t.Fatalf("enqueue %d refused before usable capacity", i)
		}
	}
	if q.Enqueue(Token{Kind: TokenText, Rune: 'z'}) {
		t.Fatal("thirtieth ring position was admitted; one slot is reserved [07 §2]")
	}
	for i := 0; i < 29; i++ {
		got, ok := q.Dequeue()
		if !ok || got.Rune != rune('a'+i) {
			t.Fatalf("token %d = %#v ok=%v, want producer order", i, got, ok)
		}
	}
	if _, ok := q.Dequeue(); ok {
		t.Fatal("empty ring returned a token")
	}
}

func TestStateTokensStaySeparateFromHeldKeys(t *testing.T) {
	s := NewState()
	s.Kbd.SetKey(KeyLeft, true)
	if !s.EnqueueToken(Token{Kind: TokenText, Rune: 'R'}) || !s.EnqueueToken(Token{Kind: TokenEdit, Key: KeyBackspace}) {
		t.Fatal("enqueue token")
	}
	if !s.Kbd.KeyHeld(KeyLeft) || s.PendingTokens() != 2 {
		t.Fatalf("held/token state = %v/%d, want separate live held state and queue", s.Kbd.KeyHeld(KeyLeft), s.PendingTokens())
	}
	got := s.DrainTokens()
	if len(got) != 2 || got[0].Rune != 'R' || got[1].Key != KeyBackspace || !s.Kbd.KeyHeld(KeyLeft) {
		t.Fatalf("drain = %#v held=%v", got, s.Kbd.KeyHeld(KeyLeft))
	}
}

func TestStatePeekAndDiscardPreserveUnconsumedTail(t *testing.T) {
	s := NewState()
	s.EnqueueToken(Token{Kind: TokenEdit, Key: KeyEscape})
	s.EnqueueToken(Token{Kind: TokenText, Rune: 'x'})
	peek := s.PeekTokens()
	if len(peek) != 2 || peek[0].Key != KeyEscape || peek[1].Rune != 'x' {
		t.Fatalf("peek=%#v", peek)
	}
	if got := s.DiscardTokens(1); got != 1 {
		t.Fatalf("discarded %d, want one", got)
	}
	peek = s.PeekTokens()
	if len(peek) != 1 || peek[0].Rune != 'x' {
		t.Fatalf("post-Escape tail=%#v", peek)
	}
}

func TestPastePayloadSurvivesQueueCopiesAndIsReleased(t *testing.T) {
	var q TokenRing
	token := Token{Kind: TokenEdit, Key: KeyV, Ctrl: true, Clipboard: ClipboardText{Text: "+showranges", Available: true}}
	q.Enqueue(token)
	token.Clipboard.Text = "later clipboard"
	peek := q.Peek()
	if len(peek) != 1 || peek[0].Clipboard.Text != "+showranges" {
		t.Fatalf("queued clipboard changed: %v", peek)
	}
	peek[0].Clipboard.Text = "changed peek"
	got, ok := q.Dequeue()
	if !ok || got.Clipboard.Text != "+showranges" || q.items[0] != (Token{}) {
		t.Fatalf("dequeued clipboard=%+v retained slot=%+v", got, q.items[0])
	}
	q.Enqueue(token)
	q.Discard(1)
	if q.items[1] != (Token{}) {
		t.Fatal("discarded token retained clipboard text")
	}
}
