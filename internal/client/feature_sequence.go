package client

import (
	"sort"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/content"
)

// The feature-sequence accessor: the one place the simulation's feature phase
// reads authored animation art.
//
// [05 R-FEAT-01 §10] makes two simulation facts depend on a GAF entry. The
// smoke jitter of pass 3a scales its two CRT draws by the CURRENT burn frame's
// width and height, and a die, reclaim or burn animation ends on the visit
// whose cursor advance clears the sequence pointer — a lifetime in visits of
// the sum over the entry's frames of max(delay, 1). Both are asset data, and
// the only GAF cache in this build is the presentation one below.
//
// Two properties make that safe to read from an authoritative phase. The
// answer depends on nothing but the file's bytes, so it is identical in every
// run over the same install (I4); and every answer is memoised per
// "filename|sequence", including the failures, so the sim path is a map lookup
// and never a load. WarmFeatureSequences resolves the whole catalog up front so
// even the first visit finds its entry already compiled.

// featureSequenceFrame is one frame's contribution: the geometry pass 3a
// scales by, and the delay that decides how many visits it holds for.
type featureSequenceFrame struct {
	w, h       int32
	xoff, yoff int32
	delay      int32
	// art is the decoded frame the feature pass blits when this cursor is the
	// one in charge — a cell with a live event record draws the instance's own
	// cursor frames [03 R-RAST-01 §6].
	art *formats.GAFFrame
}

// featureSequenceInfo is a compiled entry. A nil entry in the cache is a
// memoised miss — an absent file, an absent sequence, or an empty one.
type featureSequenceInfo struct {
	visits int32
	frames []featureSequenceFrame
}

func featureSequenceKey(filename, sequence string) string {
	return strings.ToLower(strings.TrimSpace(filename)) + "|" + strings.ToLower(strings.TrimSpace(sequence))
}

// FeatureSequence is the session's feature-sequence resolver. It reports the
// geometry of the frame the cursor is on after `visit` visits, and the whole
// entry's lifetime in visits; ok is false when the sequence does not resolve,
// and the caller then has no geometry and no length.
//
// The visit index walks the same cadence the cursor does: frame i holds for
// max(delay, 1) visits [05 R-FEAT-01 §10] pass 1. A visit at or past the
// entry's end reports the last frame, which is where a cursor sits on the
// visit that finishes it.
func (c *Client) FeatureSequence(filename, sequence string, visit int32) (w, h, xoff, yoff, visits int32, ok bool) {
	info := c.featureSequenceInfo(filename, sequence)
	if info == nil || len(info.frames) == 0 {
		return 0, 0, 0, 0, 0, false
	}
	f := info.frames[len(info.frames)-1]
	if visit < 0 {
		visit = 0
	}
	elapsed := int32(0)
	for i := range info.frames {
		elapsed += holdVisits(info.frames[i].delay)
		if visit < elapsed {
			f = info.frames[i]
			break
		}
	}
	return f.w, f.h, f.xoff, f.yoff, info.visits, true
}

// featureEventFrame is the drawn half of the same cache: the frame an event
// cursor sits on after `visit` visits, or nil when the sequence does not
// resolve. It walks the identical max(delay, 1) cadence FeatureSequence
// reports geometry for, so the frame the simulation timed the record from and
// the frame the client paints are always the same one [05 R-FEAT-01 §10].
//
// Nothing is invented for a miss: an unresolved sequence paints no pixels, as
// every other unresolved feature entry does [I9].
func (c *Client) featureEventFrame(filename, sequence string, visit int32) *formats.GAFFrame {
	info := c.featureSequenceInfo(filename, sequence)
	if info == nil || len(info.frames) == 0 {
		return nil
	}
	if visit < 0 {
		visit = 0
	}
	elapsed := int32(0)
	for i := range info.frames {
		elapsed += holdVisits(info.frames[i].delay)
		if visit < elapsed {
			return info.frames[i].art
		}
	}
	// Past the end the cursor sits on the last frame, which is where it is on
	// the visit that finishes the record.
	return info.frames[len(info.frames)-1].art
}

// WarmFeatureSequences compiles every feature definition's four sequences
// ahead of the battle, so no visit of the feature phase ever waits on a load.
// It is called once from the shell, off the simulation path; the catalog map
// is walked in sorted key order so a warm pass is reproducible.
func (c *Client) WarmFeatureSequences(defs map[string]*content.FeatureDef) {
	if c == nil || defs == nil {
		return
	}
	keys := make([]string, 0, len(defs))
	for key := range defs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		def := defs[key]
		if def == nil || def.Filename == "" {
			continue
		}
		// Only the three EVENT sequences: those are what the feature phase
		// reads. The rest sequence stays on the draw path's own lazy load,
		// which keeps this pass off every GAF that holds nothing but idle art.
		for _, seq := range [...]string{def.SeqNameBurn, def.SeqNameDie, def.SeqNameReclamate} {
			if seq != "" {
				c.featureSequenceInfo(def.Filename, seq)
			}
		}
	}
}

// featureSequenceInfo compiles one entry, memoising both hits and misses.
func (c *Client) featureSequenceInfo(filename, sequence string) *featureSequenceInfo {
	if c == nil || filename == "" || sequence == "" {
		return nil
	}
	key := featureSequenceKey(filename, sequence)
	if c.featureSeqs == nil {
		c.featureSeqs = map[string]*featureSequenceInfo{}
	}
	if info, seen := c.featureSeqs[key]; seen {
		return info
	}
	info := c.compileFeatureSequence(filename, sequence)
	c.featureSeqs[key] = info
	return info
}

func (c *Client) compileFeatureSequence(filename, sequence string) *featureSequenceInfo {
	gaf, err := c.featureGAFFor(filename)
	if err != nil || gaf == nil {
		return nil
	}
	entry, found := gaf.Find(sequence)
	if !found || entry == nil || len(entry.Frames) == 0 {
		return nil
	}
	info := &featureSequenceInfo{frames: make([]featureSequenceFrame, 0, len(entry.Frames))}
	for i := range entry.Frames {
		ref := entry.Frames[i]
		if ref.Frame == nil {
			continue
		}
		hold := holdVisits(int32(ref.Value))
		info.frames = append(info.frames, featureSequenceFrame{
			w:     int32(ref.Frame.Width),
			h:     int32(ref.Frame.Height),
			xoff:  int32(ref.Frame.XOffset),
			yoff:  int32(ref.Frame.YOffset),
			delay: int32(ref.Value),
			art:   ref.Frame,
		})
		info.visits += hold
	}
	if len(info.frames) == 0 {
		return nil
	}
	return info
}

// holdVisits is the `max(delay, 1)` of [05 R-FEAT-01 §10]: a frame whose
// authored delay word is zero still occupies one visit, because the cursor
// advance steps the frame when the delay is below two and reloads it from the
// new frame.
func holdVisits(delay int32) int32 {
	if delay < 1 {
		return 1
	}
	return delay
}
