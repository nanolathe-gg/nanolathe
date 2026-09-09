package features

import "github.com/nanolathe-gg/nanolathe/internal/content"

// eventCursor is the live animation cursor of a sprite EVENT record — the
// burn, death or reclaim animation an instance is playing [05 R-FEAT-01 §10].
// Retail keeps one such cursor per attached instance (plus a shadow cursor,
// which has no simulation effect and is not modelled); it is the ONE
// authoritative word set for the record's progress: the feature phase advances
// it, presentation draws the frame it names, the battle save writes its frame
// byte and the reload writes that byte back [08 R-SAVE-FEATURE-01].
//
// A cursor is (sequence pointer, frame index, per-frame delay countdown). The
// sequence pointer is the delay table here: a nil table is retail's null
// pointer, i.e. a finished or never-started cursor. The loop byte is not
// carried because the loader forces it to zero for every event sequence
// [05 R-FEAT-01 §1][05 "Feature burning"], so reaching the frame count always
// finishes.
type eventCursor struct {
	// delays is the bound sequence's per-frame delay words [fmt gaf], in
	// frame order. It is immutable content metadata handed through the
	// Service.SequenceFrames seam and is never written here.
	delays []int32
	frame  int32
	delay  int32
}

// start binds a sequence at frame 0, the way ignition and the die/reclaim
// transition do [05 R-FEAT-01 §5 step 5][05 R-FEAT-01 §9 step 3]. The delay
// countdown is loaded from frame 0's word: that is what makes the lifetime in
// visits the sum over frames of max(delay, 1) the same section states.
func (c *eventCursor) start(delays []int32) {
	c.delays = delays
	c.frame = 0
	c.delay = 0
	if len(delays) > 0 {
		c.delay = delays[0]
	}
}

// running reports whether the cursor still holds a sequence pointer.
func (c *eventCursor) running() bool { return c.delays != nil }

// advance is one visit of the cursor [05 R-FEAT-01 §10] pass 1, whose rule
// pass 3 applies to every event cursor: if the delay is below two, step the
// frame — when it reaches the frame count the (forced-zero) loop byte clears
// the sequence pointer and the cursor is finished, otherwise reload the delay
// from the new frame's word; else decrement the delay. It reports whether this
// visit finished the sequence, which is the visit the record completes on.
func (c *eventCursor) advance() (finished bool) {
	if c.delays == nil {
		return false
	}
	if c.delay < 2 {
		c.frame++
		// Retail tests the frame for equality with the count. A frame index
		// past the count can only come from a save whose frame byte outruns
		// the sequence the current asset resolves to; the retail result of
		// that is not traced, and the Go bound is the safe reading of "the
		// sequence has no such frame".
		if c.frame >= int32(len(c.delays)) {
			c.delays = nil
			return true
		}
		c.delay = c.delays[c.frame]
		return false
	}
	c.delay--
	return false
}

// visitIndex is the first visit of the cursor's current frame under the
// max(delay, 1) cadence — the index content.SimArt.FeatureSequence and the
// presentation cache both map back onto exactly this frame. It is how a
// frame-and-delay cursor is handed to consumers that count visits, without a
// second progress word being kept anywhere.
func (c *eventCursor) visitIndex() int32 {
	visit := int32(0)
	for i := int32(0); i < c.frame && int(i) < len(c.delays); i++ {
		d := c.delays[i]
		if d < 1 {
			d = 1
		}
		visit += d
	}
	return visit
}

// EventSequenceName is the sequence a selector names on a definition:
// `seqnameburn` for the burn record, `seqnamedie` for the death transition and
// `seqnamereclamate` for the reclaim transition [05 R-FEAT-01 §5 step 2]
// [05 R-FEAT-01 §9 step 1]. It is exported so the session's metadata binder
// and this package agree on one mapping.
func EventSequenceName(def *content.FeatureDef, selector uint8) string {
	if def == nil {
		return ""
	}
	switch selector {
	case featureAnimSelectorBurn:
		return def.SeqNameBurn
	case featureAnimSelectorDie:
		return def.SeqNameDie
	case featureAnimSelectorReclaim:
		return def.SeqNameReclamate
	}
	return ""
}

// eventShadowName is the shadow twin of EventSequenceName, drawn beneath the
// event frame by presentation [03 R-RAST-01 §6] and never timed by the
// simulation.
func eventShadowName(def *content.FeatureDef, selector uint8) string {
	if def == nil {
		return ""
	}
	switch selector {
	case featureAnimSelectorBurn:
		return def.SeqNameBurnShad
	case featureAnimSelectorDie:
		return def.SeqNameDieShad
	case featureAnimSelectorReclaim:
		return def.SeqNameReclamateShad
	}
	return ""
}

// sequenceDelays resolves the delay table of the sequence a selector names on
// a definition, or nil when the definition names none or the name does not
// resolve in the battle's content metadata. A nil result is "no sequence" to
// every caller: the transition then replaces at once (§5 step 3) and ignition
// refuses (§9 step 1).
//
// TODO(question): [05 R-FEAT-01 §1] leaves the parser's miss path Unknown —
// what a `seqname*` value that names an entry absent from its GAF bank stores
// (the shipped corpus has no such case). This build treats it as an absent
// sequence, the reading under which nothing freezes; a static trace of the
// bank lookup's miss return would settle it.
func (s *Service) sequenceDelays(def *content.FeatureDef, selector uint8) []int32 {
	if s == nil || def == nil || s.SequenceFrames == nil {
		return nil
	}
	if EventSequenceName(def, selector) == "" {
		return nil
	}
	key := sequenceKey{def: def, selector: selector}
	if delays, seen := s.sequences[key]; seen {
		return delays
	}
	delays := s.SequenceFrames(def, selector)
	if len(delays) == 0 {
		delays = nil
	}
	if s.sequences == nil {
		s.sequences = make(map[sequenceKey][]int32)
	}
	// The table is immutable content metadata, so one lookup per definition
	// and selector is the whole cost; the map is a cache read by key and never
	// ranged (I1).
	s.sequences[key] = delays
	return delays
}

// sequenceKey identifies one definition's event sequence in the cache above.
type sequenceKey struct {
	def      *content.FeatureDef
	selector uint8
}
