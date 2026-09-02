package frame

import (
	"fmt"

	"github.com/nanolathe/nanolathe/internal/pool"
)

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
	// Visited is the per-record bit the F3 message-source jump walks
	// [07 R-CAM-01 §2].
	Visited bool
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

// MessageClassSpeed is the ring kind the game-speed announcement is posted
// under [07 R-CAM-01 §3].
const MessageClassSpeed uint8 = 2

// messageSilenceMarker is the silence byte value that suppresses the
// `MessageArrived` cue. The speed announcement posts silent, and retail's
// format string carries the same byte as its trailing character
// [07 R-CAM-01 §3][07 R-CAM-01 §7 "textlines"].
const messageSilenceMarker = '\n'

// SpeedAnnouncement is the text the game-speed setter posts to the ring
// whenever the clamped target actually changes [07 R-CAM-01 §3]:
//
//	speed == 10 -> translate("Game Speed Normal")
//	otherwise   -> sprintf("%s  %c%d\n", translate("Game Speed"),
//	                       (speed-10 > 0) ? '+' : ' ', speed-10)
//
// The offset from normal is what is printed, and the `%c` is a space for zero
// and negative offsets — so a slower speed reads `Game Speed   -2`, with three
// spaces, and a faster one `Game Speed  +3`. Go's `%+d` cannot produce the
// first form, which is why the two halves are formatted separately here. The
// trailing newline of retail's format string is the silence marker the poster
// tests and is not part of the drawn line, so it is not included.
func SpeedAnnouncement(speed int) string {
	if speed == 10 {
		return "Game Speed Normal"
	}
	offset := speed - 10
	sign := byte(' ')
	if offset > 0 {
		sign = '+'
	}
	return fmt.Sprintf("Game Speed  %c%d", sign, offset)
}

// PostSilent appends a line that plays no arrival cue. The speed announcement
// is the caller of record [07 R-CAM-01 §3].
func (r *MessageRing) PostSilent(text string, class uint8, tick uint32) bool {
	// The silence byte is carried by the poster, not by the stored text; the
	// ring itself has no cue to suppress here, so the marker is named only to
	// keep the contract visible [07 R-CAM-01 §7 "textlines"].
	_ = messageSilenceMarker
	return r.Append(text, class, 0, 10, tick)
}

// Clear resets the ring, producer and display index. It is F12's whole action
// [07 R-CAM-01 §2].
func (r *MessageRing) Clear() {
	if r == nil {
		return
	}
	r.Entries = [30]MessageLine{}
	r.Producer, r.Display = 0, 0
}

// ClearVisited clears the visited bit on all thirty records [07 R-CAM-01 §2].
func (r *MessageRing) ClearVisited() {
	if r == nil {
		return
	}
	for i := range r.Entries {
		r.Entries[i].Visited = false
	}
}

// NextUnvisitedSource returns the first record whose source unit is alive and
// whose visited bit is clear, marks it visited, and reports its source unit
// [07 R-CAM-01 §2]. The walk starts at the display index and runs forward
// through the thirty records so the oldest visible line is offered first;
// alive reports whether a source handle still names a live unit.
func (r *MessageRing) NextUnvisitedSource(alive func(pool.Handle) bool) (pool.Handle, bool) {
	if r == nil || alive == nil {
		return 0, false
	}
	for step := 0; step < 30; step++ {
		idx := uint16((uint32(r.Display) + uint32(step)) % 30)
		line := &r.Entries[idx]
		if line.Visited || line.SourceUnit == 0 || !alive(line.SourceUnit) {
			continue
		}
		line.Visited = true
		return line.SourceUnit, true
	}
	return 0, false
}
