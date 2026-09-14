package frame

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
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
	// Visited is flag-byte bit 0x10: the "already jumped to since the last
	// exhaustion" bit F3's scan tests and sets [07 R-CAM-01 §14 "F3's leading
	// clear is a different bit from the visited bit"].
	Visited bool
	// Jumped is flag-byte bit 0x20, which F3 clears on every record before it
	// scans and then sets on the record it lands on, so it marks the record
	// most recently jumped to. It is a different bit from Visited: reading the
	// two writes as one bit made the "not yet visited" test and the retry arm
	// unreachable.
	//
	// Its reader is the message column painter, and it decides that line's
	// colour: the marked record draws in colour-map entry 10, every other line
	// in entry 15. This previously carried an open-question marker, "what reads
	// bit 0x20 is not traced. A composer highlight of the message line just
	// jumped to is the natural candidate" — the candidate was right, and it is now traced
	// [07 R-CAM-01 §14 "the jumped-to line is the highlighted line"].
	Jumped bool
}

// Logical colour-map entries the message column paints its lines in: the
// record F3 most recently jumped to is highlighted, every other line is the
// ordinary battle-text entry. The painter installs the pair (foreground, skip
// colour 254) once per line before drawing it
// [07 R-CAM-01 §14 "the jumped-to line is the highlighted line"][03 R-FONT-01 §4].
const (
	MessageLineLogicalColor       byte = 15
	MessageLineJumpedLogicalColor byte = 10
)

// LogicalColor is the colour-map entry this line draws in.
func (l MessageLine) LogicalColor() byte {
	if l.Jumped {
		return MessageLineJumpedLogicalColor
	}
	return MessageLineLogicalColor
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

// NewMessageRing returns a ring carrying retail's missing-value defaults for
// textlines and textscroll, both 10 [02 "registry preference (Total
// Annihilation key)"].
func NewMessageRing() *MessageRing {
	return &MessageRing{TextLines: 10, TextScroll: 10}
}

// Configure installs the player's persisted line and scroll counts. textLines
// is clamped to the ring's thirty entries.
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
	// The poster replaces only the routing nibble of the flag byte; F3's
	// visited and destination marks survive slot reuse [07 R-HUD-03 §14.3].
	line := &r.Entries[r.Producer]
	line.Text, line.StoredTick, line.SourceUnit = text, tick, source
	line.SpeakerSlot, line.Class = speaker, class&0x0f
	r.Producer = next
	return true
}

// RetireOne advances the display index by one when its strict age bound has
// elapsed. The executor calls it once per outer pump, including a pump with no
// sub-ticks, so a backlog retires one record at a time [01 R-PLAT-02 §§7,8].
//
// Retiring changes only the display index. The old record remains available to
// the ring's record-level operations until a later poster replaces its slot.
func (r *MessageRing) RetireOne(currentTick uint32) bool {
	if r == nil || r.TextLines == 0 || r.Display == r.Producer {
		return false
	}
	line := r.Entries[r.Display]
	deadline := line.StoredTick + (uint32(r.TextScroll)+1)*30
	if deadline >= currentTick {
		return false
	}
	r.Display = uint16((uint32(r.Display) + 1) % 30)
	return true
}

// Expire retires every already-overdue record for direct maintenance callers.
// The host-pump path must use RetireOne so it preserves the executor's
// one-record cadence [01 R-PLAT-02 §8].
func (r *MessageRing) Expire(currentTick uint32) {
	for r.RetireOne(currentTick) {
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

// Clear resets only the producer and display cursors. Hidden records and
// settings survive both F12 and battle entry [07 R-HUD-03 §14.3].
func (r *MessageRing) Clear() {
	if r == nil {
		return
	}
	r.Producer, r.Display = 0, 0
}

// ClearVisited clears bit 0x10, the visited bit, on all thirty records. It is
// the retry arm of F3: the scan runs it once when every live-source message has
// already been visited [07 R-CAM-01 §14].
func (r *MessageRing) ClearVisited() {
	if r == nil {
		return
	}
	for i := range r.Entries {
		r.Entries[i].Visited = false
	}
}

// ClearJumped clears bit 0x20 on all thirty records. It is F3's *leading*
// clear, run before the scan starts, and it is a different bit from the one
// the scan tests [07 R-CAM-01 §14].
func (r *MessageRing) ClearJumped() {
	if r == nil {
		return
	}
	for i := range r.Entries {
		r.Entries[i].Jumped = false
	}
}

// NextUnvisitedSource returns the first record whose source unit id is nonzero,
// whose visited bit (0x10) is clear and whose unit is alive; on a hit it sets
// both bits (`|= 0x30`) and reports the source unit [07 R-CAM-01 §14].
//
// The walk runs from the display index *toward the producer index*, wrapping at
// 30, so the oldest displayed message is offered first and the empty slots
// beyond the producer are never scanned. alive reports whether a source handle
// still names a live unit.
func (r *MessageRing) NextUnvisitedSource(alive func(pool.Handle) bool) (pool.Handle, bool) {
	if r == nil || alive == nil {
		return 0, false
	}
	for idx := r.Display; idx != r.Producer; idx = uint16((uint32(idx) + 1) % 30) {
		line := &r.Entries[idx]
		if line.Visited || line.SourceUnit == 0 || !alive(line.SourceUnit) {
			continue
		}
		line.Visited = true
		line.Jumped = true
		return line.SourceUnit, true
	}
	return 0, false
}
