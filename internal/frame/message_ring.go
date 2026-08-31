package frame

import "github.com/nanolathe/nanolathe/internal/pool"

// MessageLine is one immutable copy of the battle message-line ring entry.
// The ring is presentation state; it is not part of authoritative simulation
// and is rebuilt by the client from committed semantic events [07 R-HUD-03
// §14.3][I6].
type MessageLine struct {
	Text        string
	StoredTick  uint32
	SourceUnit  pool.Handle
	SpeakerSlot uint8
	Class       uint8
}

// MessageRing is the fixed 30-entry output ring shared by unit captions and
// chat. TextLines is the authored modulus (the visible budget is TextLines-1)
// and TextScroll is the authored lifetime in seconds [07 R-HUD-03 §14.3].
type MessageRing struct {
	Entries    [30]MessageLine
	Producer   uint16
	Display    uint16
	TextLines  uint16
	TextScroll uint16
}

func NewMessageRing() *MessageRing {
	return &MessageRing{TextLines: 10, TextScroll: 10}
}

func (r *MessageRing) Configure(textLines, textScroll uint16) {
	if r == nil {
		return
	}
	if textLines > 30 {
		textLines = 30
	}
	r.TextLines, r.TextScroll = textLines, textScroll
}

// Append follows the retail bounded-copy and oldest-line drop rules. The
// speaker sentinel is 10: captions and local chat use it and therefore do not
// request a logo or arrival cue [07 R-HUD-03 §14.3].
func (r *MessageRing) Append(text string, class uint8, source pool.Handle, speaker uint8, tick uint32) bool {
	if r == nil || text == "" || r.TextLines == 0 {
		return false
	}
	limit := r.TextLines
	if limit > 30 {
		limit = 30
	}
	if limit < 1 {
		return false
	}
	next := uint16((uint32(r.Producer) + 1) % 30)
	if uint16((uint32(r.Producer)+1)%uint32(limit)) == r.Display {
		r.Display = uint16((uint32(r.Display) + 1) % 30)
	}
	if len(text) > 63 {
		text = text[:63]
	}
	r.Entries[r.Producer] = MessageLine{Text: text, StoredTick: tick, SourceUnit: source, SpeakerSlot: speaker, Class: class & 0x0f}
	r.Producer = next
	return true
}

// Expire advances the visible head while its strict age bound has elapsed.
func (r *MessageRing) Expire(currentTick uint32) {
	if r == nil || r.TextLines == 0 || r.Display == r.Producer {
		return
	}
	for r.Display != r.Producer {
		line := r.Entries[r.Display]
		deadline := line.StoredTick + (uint32(r.TextScroll)+1)*30
		if deadline >= currentTick {
			return
		}
		r.Entries[r.Display] = MessageLine{}
		r.Display = uint16((uint32(r.Display) + 1) % 30)
	}
}

// Visible returns lines in drawing order, oldest first. It enforces the
// textLines-1 on-screen budget and never exposes stale ring slots.
func (r *MessageRing) Visible() []MessageLine {
	if r == nil || r.TextLines <= 1 || r.Display == r.Producer {
		return nil
	}
	visibleLimit := int(r.TextLines - 1)
	if visibleLimit > 29 {
		visibleLimit = 29
	}
	backward := make([]MessageLine, 0, visibleLimit)
	idx := r.Producer
	for len(backward) < visibleLimit {
		idx = uint16((uint32(idx) + 29) % 30)
		backward = append(backward, r.Entries[idx])
		if idx == r.Display {
			break
		}
	}
	out := make([]MessageLine, len(backward))
	for i := range backward {
		out[len(backward)-1-i] = backward[i]
	}
	return out
}
