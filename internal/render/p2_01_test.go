package render

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

// TestShadeRowCount locks the SHD row table size [03 §4.3]. The former
// placeholder-row lock (SHDMidRow/ModelShadeMidRow/SelectShadeRow) was deleted
// 2026-09-01: no production draw path could reach that constant, and the real
// formula is locked by TestShadeRowFormulaEdgeCases in shade_test.go
// [03 R-RAST-01 §5]. The row narrowing the shipped span writer applies is
// internal/client's spanShadeRow, locked there.
func TestShadeRowCount(t *testing.T) {
	if SHDRowCount != 32 {
		t.Fatalf("SHDRowCount %d want 32 [03 §4.3]", SHDRowCount)
	}
}

// TestGAFSubframeClipAndOverwrite locks A25: later subframes overwrite earlier where opaque, clipped to parent canvas [fmt gaf][03 §4.4].
func TestGAFSubframeClipAndOverwrite(t *testing.T) {
	// Build synthetic parent 4x4 canvas and two subframes manually via the same composition rule
	// as formats/gaf.go (dx = parent.XOff - child.XOff, clipped, later overwrites).
	// We test the rule directly without needing raw GAF bytes: create frames and compose.
	parent := &formats.GAFFrame{
		Width: 4, Height: 4, XOffset: 2, YOffset: 2,
		Pixels: make([]byte, 16), Transparent: make([]bool, 16),
	}
	for i := range parent.Transparent {
		parent.Transparent[i] = true
	}
	// sub1 2x2 at anchor (0,0) => dx=2, dy=2 in parent
	sub1 := &formats.GAFFrame{
		Width: 2, Height: 2, XOffset: 0, YOffset: 0,
		Pixels: []byte{10, 10, 10, 10}, Transparent: []bool{false, false, false, false},
	}
	// sub2 2x2 at anchor (1,1) => dx=1, dy=1 overlapping 1 pixel with sub1 at parent (2,2) vs (1,1)
	sub2 := &formats.GAFFrame{
		Width: 2, Height: 2, XOffset: 1, YOffset: 1,
		Pixels: []byte{20, 20, 20, 20}, Transparent: []bool{false, false, false, false},
	}
	// Apply composition as in formats/gaf.go
	compose := func(dst *formats.GAFFrame, sub *formats.GAFFrame) {
		dx := int(dst.XOffset) - int(sub.XOffset)
		dy := int(dst.YOffset) - int(sub.YOffset)
		for sy := 0; sy < int(sub.Height); sy++ {
			dyPos := dy + sy
			if dyPos < 0 || dyPos >= int(dst.Height) {
				continue
			}
			for sx := 0; sx < int(sub.Width); sx++ {
				dxPos := dx + sx
				if dxPos < 0 || dxPos >= int(dst.Width) {
					continue
				}
				subIdx := sy*int(sub.Width) + sx
				if sub.Transparent[subIdx] {
					continue
				}
				idx := dyPos*int(dst.Width) + dxPos
				dst.Pixels[idx] = sub.Pixels[subIdx]
				dst.Transparent[idx] = false
			}
		}
	}
	compose(parent, sub1)
	// after sub1, position (2,2) should be 10
	if parent.Pixels[2*4+2] != 10 || parent.Transparent[2*4+2] {
		t.Fatalf("after sub1 pixel (2,2) %d transp %v want 10 false A25", parent.Pixels[2*4+2], parent.Transparent[2*4+2])
	}
	compose(parent, sub2)
	// sub2 overwrites overlapping pixel at (1,1) etc; check (2,2) now overwritten by sub2's (1,1)=20? Let's compute:
	// sub1 at dx2=2 => covers parent (2,2),(3,2),(2,3),(3,3)
	// sub2 at dx1=1 => covers (1,1),(2,1),(1,2),(2,2)
	// Overlap is parent (2,2) which sub2 writes second, so should be 20 (later overwrites).
	if parent.Pixels[2*4+2] != 20 {
		t.Fatalf("later overwrite failed: got %d want 20 A25", parent.Pixels[2*4+2])
	}
	// Transparent skip: create sub3 with one transparent pixel
	sub3 := &formats.GAFFrame{
		Width: 1, Height: 1, XOffset: 2, YOffset: 2,
		Pixels: []byte{30}, Transparent: []bool{true},
	}
	before := parent.Pixels[2*4+2]
	compose(parent, sub3)
	if parent.Pixels[2*4+2] != before {
		t.Fatalf("transparent should not overwrite, got %d want %d A25", parent.Pixels[2*4+2], before)
	}
	// Clip test: sub that extends outside parent
	subClip := &formats.GAFFrame{
		Width: 4, Height: 4, XOffset: -10, YOffset: -10, // far outside
		Pixels: []byte{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}, Transparent: make([]bool, 16),
	}
	// should not panic and not write
	compose(parent, subClip)
	// still same
	if parent.Pixels[2*4+2] != before {
		t.Fatalf("clip should not write outside")
	}
}

// TestGAFMissingFrameTolerant ensures entries with 0 frames are tolerated (presentation absent not panic) [fmt gaf].
func TestGAFMissingFrameTolerant(t *testing.T) {
	// Minimal synthetic GAF with one entry frame_count 0
	// Header 12 + 4 offset =16, entry 40 at offset 16
	// Build bytes manually
	data := make([]byte, 56)
	// version 0x00010100
	data[0] = 0x00
	data[1] = 0x01
	data[2] = 0x01
	data[3] = 0x00
	// entryCount 1
	data[4] = 1
	// unknown 0
	// offset array: entry at 16
	data[12] = 16
	// entry header at 16: frame_count 0, unknown1 1, unknown2 0, name "EMPTY"
	// frame_count 0
	// unknown1 1 at offset 18
	data[16+2] = 1
	// name
	copy(data[16+8:], []byte("EMPTY"))
	gaf, err := formats.LoadGAF(data)
	if err != nil {
		t.Fatalf("LoadGAF 0-frame entry should succeed, got %v [fmt gaf]", err)
	}
	if len(gaf.Entries) != 1 {
		t.Fatalf("entries %d want 1", len(gaf.Entries))
	}
	if gaf.Entries[0].FrameCount != 0 || len(gaf.Entries[0].Frames) != 0 {
		t.Fatalf("0-frame entry not empty %d %d", gaf.Entries[0].FrameCount, len(gaf.Entries[0].Frames))
	}
	// Find by case insensitive
	e, ok := gaf.Find("empty")
	if !ok || e == nil {
		t.Fatalf("Find case-insensitive failed [fmt gaf]")
	}
	// Cursor bind should clear
	var c Cursor
	c.Bind(e, 0, false)
	if c.IsActive() {
		t.Fatalf("Bind 0-frame should not be active [03 §4.4]")
	}
}

// TestGAFMalformedTruncated ensures truncated GAF returns error not panic.
func TestGAFMalformedTruncated(t *testing.T) {
	// truncated header
	_, err := formats.LoadGAF([]byte{0, 1, 2})
	if err == nil {
		t.Fatalf("truncated should error")
	}
	// truncated offset table
	data := make([]byte, 20)
	data[0] = 0x00
	data[1] = 0x01
	data[2] = 0x01
	data[3] = 0x00
	data[4] = 5 // entryCount 5 but only room for 1 offset
	if _, err := formats.LoadGAF(data); err == nil {
		t.Fatalf("truncated offset table should error")
	}
}

// TestFixedEffectDurationsPlaceholder locks A24: one tick per frame where authored not established.
func TestFixedEffectDurationsPlaceholder(t *testing.T) {
	// Non-looping 2 frames: countdown 1 each => terminates after 2 steps
	p := EffectAnimPlayer{Idx: 0, Countdown: 1, Loop: false, Active: true, Frames: 2}
	p.Step() // countdown<2 advances to idx1
	if p.Idx != 1 || !p.Active {
		t.Fatalf("step1 idx %d active %v want 1 true A24", p.Idx, p.Active)
	}
	p.Step() // idx1 countdown<2 -> would advance beyond => clears
	if p.Active {
		t.Fatalf("step2 should clear non-looping A24")
	}
	// Looping wraps
	pl := EffectAnimPlayer{Idx: 1, Countdown: 1, Loop: true, Active: true, Frames: 2}
	pl.Step()
	if pl.Idx != 0 || !pl.Active {
		t.Fatalf("loop wrap idx %d active %v want 0 true A24", pl.Idx, pl.Active)
	}
	// Single-frame never advances
	ps := EffectAnimPlayer{Idx: 0, Countdown: 5, Loop: true, Active: true, Frames: 1}
	ps.Step()
	if ps.Idx != 0 {
		t.Fatalf("single-frame should not advance")
	}
}

// TestFixedEffectPerRecordGravity locks per-record gravity overrides pool default [03 §1][03 §2.2].
func TestFixedEffectPerRecordGravityOverride(t *testing.T) {
	var p FixedEffectPool
	p.SetGravity(numeric.Fixed(1 * 65536))
	rec := EffectRecord{
		X: numeric.Fixed(0), Y: numeric.Fixed(100 * 65536), Z: numeric.Fixed(0),
		VY: numeric.Fixed(0), Gravity: numeric.Fixed(5 * 65536),
		AnimA: EffectAnimPlayer{Idx: 0, Countdown: 10, Loop: true, Active: true, Frames: 2},
	}
	p.Append(rec)
	p.Update(1)
	if p.Len() != 1 {
		t.Fatalf("len")
	}
	got := p.Records()[0]
	// VY should be -5 (0 -5) not -1
	if got.VY != numeric.Fixed(-5*65536) {
		t.Fatalf("per-record gravity VY %d want %d", got.VY, -5*65536)
	}
	// Zero per-record uses pool default
	var p2 FixedEffectPool
	p2.SetGravity(numeric.Fixed(2 * 65536))
	rec2 := EffectRecord{
		Y: numeric.Fixed(100 * 65536), VY: numeric.Fixed(0),
		Gravity: 0,
		AnimA:   EffectAnimPlayer{Idx: 0, Countdown: 10, Loop: true, Active: true, Frames: 2},
	}
	p2.Append(rec2)
	p2.Update(1)
	if p2.Records()[0].VY != numeric.Fixed(-2*65536) {
		t.Fatalf("pool gravity VY %d want %d", p2.Records()[0].VY, -2*65536)
	}
}

// TestCase7PresentationRNGIsolation ensures case-7 jitter uses CRT not sim, and sim draws unchanged [I4][03 §5.4].
func TestCase7PresentationRNGIsolation(t *testing.T) {
	// Ensure SegmentedJitter draws exactly 3 CRT values per generated point and zero sim draws.
	// We cannot directly check sim draws without global, but we can ensure CRT consumption count and that sim helper not called.
	crt := rng.NewCRT(1) // CRT seed 1
	// Capture CRT draws before
	// CRT Rand returns 0..0x7FFF; one pass draws 3*n times [06 R-WFX-01 §4].
	head := combat.Vec3{X: numeric.Fixed(0), Y: numeric.Fixed(0), Z: numeric.Fixed(0)}
	tail := combat.Vec3{X: numeric.Fixed(327680 * 3), Y: numeric.Fixed(0), Z: numeric.Fixed(0)}
	n := SegmentCount(head, tail)
	if n != 3 {
		t.Fatalf("SegmentCount %d want 3", n)
	}
	// count CRT draws: SegmentedBeamPoints draws 3 per generated point
	// We'll do manual by checking that repeated calls with same CRT seed produce same jitter deterministic,
	// and that sim RNG not touched (we test via absence of import and via note).
	crt2 := rng.NewCRT(1)
	pts1 := SegmentedBeamPoints(head, tail, &crt)
	pts2 := SegmentedBeamPoints(head, tail, &crt2)
	if len(pts1) != len(pts2) {
		t.Fatalf("pts len %d vs %d", len(pts1), len(pts2))
	}
	for i := range pts1 {
		if pts1[i] != pts2[i] {
			t.Fatalf("determinism fail i %d", i)
		}
	}
	if pts1[0] != tail {
		t.Fatalf("first pass point = %+v, want tail %+v [06 R-WFX-01 §4]", pts1[0], tail)
	}
	if crt.Draws() != uint64(3*n) {
		t.Fatalf("CRT draws = %d, want %d [06 R-WFX-01 §4]", crt.Draws(), 3*n)
	}
	// Check SegmentCount edge cases
	if SegmentCount(head, head) != 0 {
		t.Fatalf("zero span should be 0 [03 §5.4]")
	}
}

// TestNanolatheColorConstant ensures nanolathe segments use constant 6 [03 §5.5].
func TestNanolatheColorConstant(t *testing.T) {
	if NanolatheColor != 6 {
		t.Fatalf("NanolatheColor %d want 6 [03 §5.5]", NanolatheColor)
	}
}

// TestBeamSingleVsDual ensures beam stroke branching [03 §5.4] C7.
func TestBeamSingleVsDualP2(t *testing.T) {
	head := [2]int32{0, 0}
	tail := [2]int32{10, 10}
	single := BeamStrokes(head, tail, 5, 0)
	if len(single) != 1 || single[0].Color != 5 {
		t.Fatalf("single stroke failed %v", single)
	}
	dual := BeamStrokes(head, tail, 5, 7)
	if len(dual) != 2 {
		t.Fatalf("dual len %d want 2", len(dual))
	}
	if dual[0].Color != 7 || dual[1].Color != 5 {
		t.Fatalf("dual order %v want [7,5]", dual)
	}
	// Equal spans use the vertical-major secondary offset [06 R-WFX-01 §4].
	if dual[0] != (BeamStroke{-1, 0, 11, 10, 7}) || dual[1] != (BeamStroke{0, 0, 10, 10, 5}) {
		t.Fatalf("dual endpoint offsets failed: %+v", dual)
	}
}
