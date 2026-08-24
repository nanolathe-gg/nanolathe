package audio

import (
	"github.com/nanolathe/nanolathe/internal/pool"
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
	audioThreshold  uint8
	speechThreshold uint8
	soundEnabled    bool
	speechEnabled   bool
	onPlay          func(alias string, slot Slot, unit pool.Handle)
	onSpeech        func(line string)
	resolver        func(pool.Handle) (*Category, string, bool)
	categories      map[pool.Handle]*Category
	unitNames       map[pool.Handle]string
	alive           map[pool.Handle]bool
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

// nextAllowed is the per-slot next-allowed frame cache [03 §8.3] (C15).
var nextAllowed [24]uint32

// ResetCooldowns clears the global per-slot cooldowns, for tests.
func ResetCooldowns() {
	for i := range nextAllowed {
		nextAllowed[i] = 0
	}
}

// SlotStatic returns the static table entry for a slot [03 §8.3].
func SlotStatic(s Slot) (key, speech string, priority int8, cooldown uint32, ok bool) {
	if int(s) < 0 || int(s) >= len(slotTable) {
		return "", "", 0, 0, false
	}
	e := slotTable[s]
	return e.Key, e.Speech, e.Priority, e.Cooldown, true
}

// NextAllowed returns the mutable next-allowed frame for a slot.
func NextAllowed(s Slot) uint32 {
	if int(s) < 0 || int(s) >= len(nextAllowed) {
		return 0
	}
	return nextAllowed[s]
}

// NewQueue creates a queue with permissive defaults [03 §8.3].
func NewQueue() *Queue {
	q := &Queue{
		audioThreshold:  10,
		speechThreshold: 10,
		soundEnabled:    true,
		speechEnabled:   true,
		crtState:        1,
		categories:      make(map[pool.Handle]*Category),
		unitNames:       make(map[pool.Handle]string),
		alive:           make(map[pool.Handle]bool),
	}
	return q
}

func (q *Queue) ensureInit() {
	if q.categories == nil {
		q.categories = make(map[pool.Handle]*Category)
	}
	if q.unitNames == nil {
		q.unitNames = make(map[pool.Handle]string)
	}
	if q.alive == nil {
		q.alive = make(map[pool.Handle]bool)
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
	}
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
}

// drawCRT advances the presentation CRT stream once and returns 0..0x7FFF
// [01 §7.2] (I4). Audio variant selection uses the CRT stream (C19).
func (q *Queue) drawCRT() uint32 {
	if q == nil {
		return 0
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
	if now < nextAllowed[s] {
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
	// always draw variant even if not audible [03 §8.3] (I4)
	draw := q.drawCRT()
	var cat *Category
	var unitName string
	var alive bool
	if q.resolver != nil {
		cat, unitName, alive = q.resolver(e.Unit)
	} else {
		cat = q.categories[e.Unit]
		unitName = q.unitNames[e.Unit]
		alive = q.alive[e.Unit]
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
	if audible && len(variants) > 0 && int(10-q.audioThreshold) < int(info.Priority) && q.soundEnabled {
		if q.onPlay != nil && alias != "" {
			q.onPlay(alias, e.Slot, e.Unit)
		}
		nextAllowed[e.Slot] = now + info.Cooldown*cooldownUnit
	}
	if showText && int(10-q.speechThreshold) < int(info.Priority) && q.speechEnabled {
		line := e.Text
		if line == "" {
			if caption != "" {
				line = caption
			} else {
				line = info.Speech
			}
		}
		if line != "" {
			if alive && unitName != "" {
				line = unitName + ": " + line
			} else if alive && unitName == "" {
				// still print line without prefix if name empty but alive
			} else if !alive {
				// retail prints prefixed only when unit still alive; if dead, print line without prefix?
				// However spec says prefixed when still alive; for dead, print line as is (no prefix)
			}
			if q.onSpeech != nil {
				// only prefix when alive and name non-empty per spec
				q.onSpeech(line)
			}
		}
	}
}
