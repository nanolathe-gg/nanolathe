package session

import (
	"math"
	"testing"
)

// TestScoreTickDivisorIsSixty locks the WU-19-14 correction to Score:
// retail divides the global tick by 60 (unsigned integer division), not
// 1800 and not a floating-point division [08 R-CAMP-01 §7]
// [08 R-CAMP-01 §11 point 3]. With timemul=1.0 and killmul/kills zero, the
// time term steps from 0 to 1 exactly at tick 60 and stays 1 through tick
// 61 (61/60 truncates to 1, not 1.0166...).
func TestScoreTickDivisorIsSixty(t *testing.T) {
	cases := []struct {
		ticks uint32
		want  int
	}{
		{59, 0},
		{60, 1},
		{61, 1},
	}
	for _, c := range cases {
		if got := Score(0, 0, c.ticks, 1.0); got != c.want {
			t.Fatalf("Score(0, 0, %d, 1.0) = %d, want %d", c.ticks, got, c.want)
		}
	}
}

// TestScoreDefaultsAbsentAreZero locks [08 R-CAMP-01 §11 point 3]: with both
// killmul and timemul at their absent-key default of 0.0, every score is 0
// regardless of kills or elapsed ticks.
func TestScoreDefaultsAbsentAreZero(t *testing.T) {
	if got := Score(7, 0, 6000, 0); got != 0 {
		t.Fatalf("Score with both multipliers absent (0) = %d, want 0", got)
	}
}

// TestScoreTruncatesEachProductSeparately locks the order of operations in
// [08 R-CAMP-01 §7]: __ftol(time × timemul) + __ftol(kills × killmul), each
// product truncated toward zero BEFORE the sum — not the sum truncated once.
// ticks=60 (time term 1×0.6=0.6) and kills=1, killmul=0.6 (kill term 0.6)
// truncate to 0 and 0 (sum 0) under retail's rule; summing the untruncated
// products first (1.2) and truncating once would wrongly yield 1.
func TestScoreTruncatesEachProductSeparately(t *testing.T) {
	const ticks = 60 // ticks/60 == 1
	const timemul = float32(0.6)
	const killmul = float32(0.6)
	const kills = 1

	got := Score(kills, killmul, ticks, timemul)
	if got != 0 {
		t.Fatalf("Score(%d, %v, %d, %v) = %d, want 0 (separate truncation)", kills, killmul, ticks, timemul, got)
	}

	// Sanity: confirm the two orders really do differ for this fixture, so
	// this test would actually catch a regression to sum-then-truncate.
	sumFirst := int(math.Trunc(float64(1)*float64(timemul) + float64(kills)*float64(killmul)))
	if sumFirst == got {
		t.Fatalf("fixture does not distinguish truncate-then-sum from sum-then-truncate (both %d); pick different values", got)
	}
}

// TestWonLatchOnlySetsBitsAndLostLatchClearsTheFirstWinBit locks the two
// terminal latch writes of [08 R-TRIG-01 §6] "Countdown and latch". The won
// latch is three ORs — ending, 0x10, 0x20 — and clears nothing, so it leaves a
// lose bit standing; the lost latch ORs 0x40 and clears 0x10 only, leaving
// 0x20 wherever a prior win set it. The direction that used to be wrong is the
// won one: it also cleared 0x40.
func TestWonLatchOnlySetsBitsAndLostLatchClearsTheFirstWinBit(t *testing.T) {
	l := NewEndLatch()
	l.Bits = LatchBitLose
	l.Win()
	if l.Bits != LatchBitLose|LatchBitWin1|LatchBitWin2 {
		t.Fatalf("won latch bits = %#x, want %#x — the won path clears nothing",
			l.Bits, LatchBitLose|LatchBitWin1|LatchBitWin2)
	}

	l = NewEndLatch()
	l.Bits = LatchBitWin1 | LatchBitWin2
	l.Lose()
	if l.Bits != LatchBitWin2|LatchBitLose {
		t.Fatalf("lost latch bits = %#x, want %#x — the lost path clears 0x10 only",
			l.Bits, LatchBitWin2|LatchBitLose)
	}
}

// TestScoreClampsNegativeToZero locks the `< 0 → 0` clamp [08 R-CAMP-01 §7].
func TestScoreClampsNegativeToZero(t *testing.T) {
	if got := Score(0, -5, 600, 0); got != 0 {
		t.Fatalf("Score with a negative product = %d, want clamp to 0", got)
	}
}
