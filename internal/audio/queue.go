package audio

import (
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

// Slot is a sound event slot [03 §8.3]. Slot 0 is an unused sentinel.
type Slot uint8

// Category is a retail sound category [03 §8.3] (I13).
// Retail identity is 352 bytes: 64-byte name and 24 twelve-byte event rows
// indexed by slot where 0 is sentinel and 1..23 are real events. A row holds
// a variant count and two parallel arrays of 64-byte strings (alias and
// speech caption). Go stores named fields without layout assumptions (I13).
type Category struct {
	Name string // up to 64 bytes [03 §8.3] (I13)
	Rows [24]Row
}

// Row is one event row of a sound category [03 §8.3] (I13).
type Row struct {
	Variants []string // ordered variant aliases, each 64-byte string truncated [03 §8.3]
	Captions []string // parallel captions per variant
}

// Entry is one queued cue [03 §8.3].
type Entry struct {
	Slot     Slot
	Frame    uint32
	Unit     pool.Handle
	Text     string
	Priority int8
}

// Queue is the eight-entry arbitration queue [03 §8.3].
type Queue struct {
	Entries  [8]Entry
	Count    int
	BaseTime uint32

	now             uint32
	crtState        uint32
	crt             *rng.CRT
	audioThreshold  uint8
	speechThreshold uint8
	soundEnabled    bool
	speechEnabled   bool
	sing            bool
	effectsVolume   float32
	soundFlags      uint8
	backendEnabled  bool
	gateInitialized bool
	nextAllowed     [24]uint32
	onPlay          func(alias string, slot Slot, unit pool.Handle)
	onSpeech        func(line string)
	onCaption       func(line string, slot Slot, unit pool.Handle)
	resolver        func(pool.Handle) (*Category, string, bool)
	categories      map[pool.Handle]*Category
	unitNames       map[pool.Handle]string
	alive           map[pool.Handle]bool
	chatEnabled     map[pool.Handle]bool
}

const (
	queueCapacity = 8
	drainWindow   = 30 // [03 §8.3]
	cooldownUnit  = 30 // [03 §8.3]
	variantRange  = 32768
)

// slotInfo is the per-slot priority and cooldown table [03 §8.3].
// Priorities and cooldowns are static and global across every category (C15).
type slotInfo struct {
	Key      string
	Speech   string
	Priority int8
	Cooldown uint32
}

// [03 §8.3] static 23 rows.
var slotTable = [24]slotInfo{
	0:  {"", "", 0, 0},
	1:  {"select", "", 10, 0},
	2:  {"underattack", "Under Attack", 9, 20},
	3:  {"activate", "", 4, 2},
	4:  {"deactivate", "", 4, 2},
	5:  {"ok", "", 5, 1},
	6:  {"arrived", "Arrived", 3, 4},
	7:  {"cant", "Cannot Comply", 8, 1},
	8:  {"unitcomplete", "Nanolathe Complete", 3, 3},
	9:  {"build", "", 4, 2},
	10: {"repair", "", 3, 1},
	11: {"working", "", 2, 1},
	12: {"load", "", 7, 1},
	13: {"unload", "", 7, 1},
	14: {"cloak", "Cloaked", 7, 1},
	15: {"uncloak", "Visible", 7, 1},
	16: {"capture", "", 4, 1},
	17: {"count5", "five", 10, 0},
	18: {"count4", "four", 10, 0},
	19: {"count3", "three", 10, 0},
	20: {"count2", "two", 10, 0},
	21: {"count1", "one", 10, 0},
	22: {"count0", "zero", 10, 0},
	23: {"canceldestruct", "Self destruct terminated", 10, 0},
}

// SlotStatic returns the static table entry for a slot [03 §8.3].
func SlotStatic(s Slot) (key, speech string, priority int8, cooldown uint32, ok bool) {
	if int(s) < 0 || int(s) >= len(slotTable) {
		return "", "", 0, 0, false
	}
	e := slotTable[s]
	return e.Key, e.Speech, e.Priority, e.Cooldown, true
}

// ToggleSing changes only the audible unit-voice alias [03 R-AUD-01 §3].
func (q *Queue) ToggleSing() bool {
	if q == nil {
		return false
	}
	q.sing = !q.sing
	return q.sing
}

// NewQueue creates a queue with permissive defaults [03 §8.3].
func NewQueue() *Queue {
	q := &Queue{
		audioThreshold:  10,
		speechThreshold: 10,
		soundEnabled:    true,
		speechEnabled:   true,
		crtState:        1,
		crt:             func() *rng.CRT { v := rng.NewCRT(1); return &v }(),
		effectsVolume:   1,
		soundFlags:      0x47,
		backendEnabled:  true,
		gateInitialized: true,
		categories:      make(map[pool.Handle]*Category),
		unitNames:       make(map[pool.Handle]string),
		alive:           make(map[pool.Handle]bool),
		chatEnabled:     make(map[pool.Handle]bool),
	}
	return q
}

func (q *Queue) ensureInit() {
	if q == nil {
		return
	}
	if q.categories == nil {
		q.categories = make(map[pool.Handle]*Category)
	}
	if q.unitNames == nil {
		q.unitNames = make(map[pool.Handle]string)
	}
	if q.alive == nil {
		q.alive = make(map[pool.Handle]bool)
	}
	if q.chatEnabled == nil {
		q.chatEnabled = make(map[pool.Handle]bool)
	}
	// defaults if zero values
	if !q.soundEnabled && !q.speechEnabled && q.audioThreshold == 0 && q.speechThreshold == 0 && q.crtState == 0 {
		// zero value queue from literal; install permissive defaults
		q.audioThreshold = 10
		q.speechThreshold = 10
		q.soundEnabled = true
		q.speechEnabled = true
		if q.crtState == 0 {
			q.crtState = 1
		}
	}
	if q.crtState == 0 {
		q.crtState = 1
	}
	if !q.gateInitialized {
		// A zero-value Queue is usable with the same enabled defaults as
		// NewQueue. Explicit gate configuration can restore silence later.
		q.effectsVolume = 1
		q.soundFlags = 0x47
		q.backendEnabled = true
		q.gateInitialized = true
	}
}

// SetNow sets the current frame used by Insert without explicit frame [03 §8.3] (C16).
func (q *Queue) SetNow(frame uint32) {
	if q != nil {
		q.now = frame
	}
}

// Seed sets the presentation CRT seed [01 §7.2] (I4). Variant selection uses the
// fifteen-bit CRT draw [03 §8.3] (C17, C19).
func (q *Queue) Seed(seed uint32) {
	if q != nil {
		q.crtState = seed
		v := rng.NewCRT(seed)
		q.crt = &v
	}
}

// SetCRTRandom injects the session-owned presentation stream. DET-01: the
// queue now copies the state so presentation variant draws do not affect the
// authoritative session CRT [01 §7.2][03 §8.3]. Presentation draws are
// presentation-only and must not leak into sim.
func (q *Queue) SetCRTRandom(r *rng.CRT) {
	if q != nil && r != nil {
		v := *r
		q.crt = &v
		q.crtState = v.State
	} else if q != nil {
		q.crt = nil
	}
}

// CRTRandom is the presentation copy of the CRT stream this queue draws
// variant selections from, nil when none was installed [I4].
func (q *Queue) CRTRandom() *rng.CRT {
	if q == nil {
		return nil
	}
	return q.crt
}

// ConfigureBackendGates supplies the effects-volume, sound-flags, and device
// gates applied after queue crowding thresholds. Flags bits 0..2 are the
// established master sound gate; exact secondary meanings remain unknown.
func (q *Queue) ConfigureBackendGates(effectsVolume float32, soundFlags uint8, backendEnabled bool) {
	if q == nil {
		return
	}
	q.effectsVolume, q.soundFlags, q.backendEnabled = effectsVolume, soundFlags, backendEnabled
	q.gateInitialized = true
}

// ConfigureThresholdGauges converts the 0..10 menu gauges to their byte
// thresholds (five units per gauge step) [03 §8.3].
func (q *Queue) ConfigureThresholdGauges(audioGauge, speechGauge uint8) {
	if q == nil {
		return
	}
	if audioGauge > 10 {
		audioGauge = 10
	}
	if speechGauge > 10 {
		speechGauge = 10
	}
	q.audioThreshold, q.speechThreshold = audioGauge*5, speechGauge*5
}

// Configure sets the crowding thresholds and enable flags [03 §8.3] (C17).
func (q *Queue) Configure(audioThreshold, speechThreshold uint8, soundEnabled, speechEnabled bool) {
	if q == nil {
		return
	}
	q.audioThreshold = audioThreshold
	q.speechThreshold = speechThreshold
	q.soundEnabled = soundEnabled
	q.speechEnabled = speechEnabled
}

// OnPlay sets the playback sink for audible cues.
func (q *Queue) OnPlay(fn func(alias string, slot Slot, unit pool.Handle)) {
	if q != nil {
		q.onPlay = fn
	}
}

// OnSpeech sets the speech-line sink.
func (q *Queue) OnSpeech(fn func(line string)) {
	if q != nil {
		q.onSpeech = fn
	}
}

// OnCaption sets the semantic message-line sink. It runs only after the same
// caption, liveness, crowding and slot-resolution gates as speech output; the
// presentation client owns the resulting ring [07 R-HUD-03 §14].
func (q *Queue) OnCaption(fn func(line string, slot Slot, unit pool.Handle)) {
	if q != nil {
		q.onCaption = fn
	}
}

// SetResolver sets a custom unit resolver for category lookup [03 §8.3] (C17).
func (q *Queue) SetResolver(fn func(pool.Handle) (*Category, string, bool)) {
	if q != nil {
		q.resolver = fn
	}
}

// Register registers a unit's category and liveness for Resolve [03 §8.3] (C17).
func (q *Queue) Register(unit pool.Handle, cat *Category, name string, alive bool) {
	if q == nil || unit == 0 {
		return
	}
	q.ensureInit()
	if cat != nil {
		q.categories[unit] = cat
	} else {
		delete(q.categories, unit)
	}
	if name != "" {
		q.unitNames[unit] = name
	} else {
		delete(q.unitNames, unit)
	}
	q.alive[unit] = alive
	q.chatEnabled[unit] = alive
}

// SetChatEnabled updates the live unit chat latch used by speech emission.
func (q *Queue) SetChatEnabled(unit pool.Handle, enabled bool) {
	if q == nil || unit == 0 {
		return
	}
	q.ensureInit()
	q.chatEnabled[unit] = enabled
}

// drawCRT advances the presentation CRT stream once and returns 0..0x7FFF
// [01 §7.2] (I4). Audio variant selection uses the CRT stream (C19).
func (q *Queue) drawCRT() uint32 {
	if q == nil {
		return 0
	}
	if q.crt != nil {
		v := uint32(q.crt.Rand())
		q.crtState = q.crt.State
		return v
	}
	q.crtState = q.crtState*214013 + 2531011
	return (q.crtState >> 16) & 0x7FFF
}

// Insert inserts a cue [03 §8.3] (C16).
// Drop if current frame before slot's next-allowed frame; drop if duplicate slot;
// if full resolve last entry silently; then insert sorted descending priority after equals.
func (q *Queue) Insert(s Slot, unit pool.Handle, text string) {
	q.ensureInit()
	if q == nil || s == 0 || int(s) >= len(slotTable) {
		return
	}
	now := q.now
	q.insertAt(now, s, unit, text)
}

// InsertAt inserts with explicit frame, for tests and presentation callers that
// know the rendered frame. It follows [03 §8.3] (C16) verbatim.
func (q *Queue) InsertAt(frame uint32, s Slot, unit pool.Handle, text string) bool {
	if q == nil {
		return false
	}
	q.ensureInit()
	if s == 0 || int(s) >= len(slotTable) {
		return false
	}
	return q.insertAt(frame, s, unit, text)
}

func (q *Queue) insertAt(now uint32, s Slot, unit pool.Handle, text string) bool {
	if now < q.nextAllowed[s] {
		return false
	}
	for i := 0; i < q.Count; i++ {
		if q.Entries[i].Slot == s {
			return false
		}
	}
	if q.Count == queueCapacity {
		last := q.Entries[q.Count-1]
		q.resolve(last, now, false, true)
		q.Count--
	}
	pri := slotTable[s].Priority
	e := Entry{Slot: s, Frame: now, Unit: unit, Text: text, Priority: pri}
	// insert sorted descending priority, after equals for FIFO (C16)
	pos := 0
	for pos < q.Count && q.Entries[pos].Priority >= e.Priority {
		pos++
	}
	copy(q.Entries[pos+1:q.Count+1], q.Entries[pos:q.Count])
	q.Entries[pos] = e
	q.Count++
	return true
}

// Drain drains once per rendered frame outside simulation [03 §8.3] (C18).
func (q *Queue) Drain(frame uint32) {
	if q == nil || q.Count == 0 {
		return
	}
	q.ensureInit()
	q.now = frame
	head := q.Entries[0]
	var audible bool
	if frame < q.BaseTime+drainWindow {
		audible = false
	} else {
		audible = true
		q.BaseTime = frame
	}
	q.resolve(head, frame, audible, true)
	copy(q.Entries[:q.Count-1], q.Entries[1:q.Count])
	q.Entries[q.Count-1] = Entry{}
	q.Count--
}

// resolve implements Resolve [03 §8.3] (C17).
func (q *Queue) resolve(e Entry, now uint32, audible, showText bool) {
	if q == nil {
		return
	}
	info := slotTable[e.Slot]
	var cat *Category
	var unitName string
	var alive bool
	if q.resolver != nil {
		cat, unitName, alive = q.resolver(e.Unit)
	} else {
		cat = q.categories[e.Unit]
		unitName = q.unitNames[e.Unit]
		alive = q.alive[e.Unit]
		alive = alive && q.chatEnabled[e.Unit]
		// if unit was never registered but handle non-zero, treat as alive with empty name for tests that don't register
		if _, ok := q.alive[e.Unit]; !ok && e.Unit != 0 {
			// keep alive false unless explicitly registered; speech prefix requires alive check,
			// so unregistered units won't get prefix — matches retail alive check
			alive = false
		}
	}
	var variants []string
	var captions []string
	if cat != nil && int(e.Slot) < len(cat.Rows) {
		row := cat.Rows[e.Slot]
		variants = row.Variants
		captions = row.Captions
	}
	var alias string
	var caption string
	// Variant selection is presentation-random even for a silent resolve, and
	// the draw is UNCONDITIONAL: it happens on every resolve, before any gate
	// and before the variant count is looked at, so the stream advances with
	// queue pops rather than with audible successes [03 §8.3]. A zero count
	// simply produces no pick. Corrected 2026-09-04 (WU-19-155); this code
	// previously skipped the draw for an empty row behind an open-question marker
	// calling the point unknown, which it was not — and the empty row is the
	// common case, since the reference install authors no `load` or `unload`
	// variant in any of its 120 categories.
	//
	// It cannot reach the simulation either way: the queue draws a private
	// presentation copy of the stream [01 §7.2][I4].
	draw := q.drawCRT()
	if len(variants) > 0 {
		idx := int(draw) * len(variants) / variantRange
		if idx >= len(variants) {
			idx = len(variants) - 1
		}
		alias = variants[idx]
		if idx < len(captions) {
			caption = captions[idx]
		}
	}
	audioCrowding := int16(10) - int16(q.audioThreshold)
	if audible && len(variants) > 0 && audioCrowding < int16(info.Priority) && q.soundEnabled && q.soundFlags&0x40 != 0 {
		// Re-arm on the audible gate even when dispatch has no path or sink.
		q.nextAllowed[e.Slot] = now + info.Cooldown*cooldownUnit
		// Master backend gates apply only to dispatch, after the crowding gate.
		if q.effectsVolume != 0 && q.soundFlags&7 != 0 && q.backendEnabled && q.onPlay != nil {
			// Variant selection and all voice gates still precede the override;
			// the caption keeps its selected variant [03 R-AUD-01 §3].
			if q.sing {
				alias = "sing"
				if (now/30)&7 == 0 {
					alias = "honk"
				}
			}
			q.onPlay(alias, e.Slot, e.Unit)
		}
	}
	speechCrowding := int16(10) - int16(q.speechThreshold)
	if showText && speechCrowding < int16(info.Priority) && q.speechEnabled {
		line := e.Text
		if line == "" {
			if caption != "" {
				line = caption
			} else {
				line = info.Speech
			}
		}
		if line != "" && alive && unitName != "" {
			line = unitName + ": " + line
			if q.onSpeech != nil {
				q.onSpeech(line)
			}
			if q.onCaption != nil {
				q.onCaption(line, e.Slot, e.Unit)
			}
		}
	}
}
