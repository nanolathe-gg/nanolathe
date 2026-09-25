package ebitenapp

import (
	"slices"
	"time"
)

const (
	fpsHistoryDuration = 30 * time.Second
	fpsLiveDuration    = 500 * time.Millisecond
	// 8,192 samples cover 30 seconds through 273 presented frames/s. Faster
	// displays retain the most recent samples within the same bounded memory.
	fpsHistoryCapacity = 8192
)

type fpsSample struct {
	at       time.Time
	interval time.Duration
	draw     time.Duration
	sim      time.Duration
	blend    time.Duration
	record   time.Duration
	submit   time.Duration
}

// fpsCounter records completed modern presentations, excluding cap-skipped
// Draw callbacks (DESIGN_GPU_RENDERER §13.5). The bounded history serves both
// a 30-second spike graph and a 500 ms median for the live readouts.
type fpsCounter struct {
	target      time.Duration
	completed   time.Time
	history     []fpsSample
	next        int
	count       int
	liveScratch [6][]time.Duration
}

func (c *fpsCounter) observe(now time.Time, draw, sim, blend, record, submit time.Duration) {
	if c.completed.IsZero() {
		c.completed = now
		return
	}
	if interval := now.Sub(c.completed); interval > 0 {
		if c.history == nil {
			c.history = make([]fpsSample, fpsHistoryCapacity)
		}
		c.history[c.next] = fpsSample{at: now, interval: interval, draw: draw, sim: sim, blend: blend, record: record, submit: submit}
		c.next = (c.next + 1) % len(c.history)
		if c.count < len(c.history) {
			c.count++
		}
	}
	c.completed = now
}

type fpsLiveSummary struct {
	interval time.Duration
	draw     time.Duration
	sim      time.Duration
	blend    time.Duration
	record   time.Duration
	submit   time.Duration
	frames   int
}

// live takes the median in the recent window. Phase zeros mean that phase did
// not run on a presented frame (notably the 30 Hz sim at 60 FPS), so only
// positive durations enter each phase median. The cadence and Draw medians use
// every recent completed frame. Scratch slices are reused across draws.
func (c *fpsCounter) live(now time.Time) fpsLiveSummary {
	for i := range c.liveScratch {
		c.liveScratch[i] = c.liveScratch[i][:0]
	}
	var frames int
	for i := 0; i < c.count; i++ {
		index := (c.next - 1 - i + len(c.history)) % len(c.history)
		sample := c.history[index]
		age := now.Sub(sample.at)
		if age > fpsLiveDuration {
			break
		}
		if age < 0 {
			continue
		}
		frames++
		values := [...]time.Duration{sample.interval, sample.draw, sample.sim, sample.blend, sample.record, sample.submit}
		for metric, value := range values {
			if value > 0 {
				c.liveScratch[metric] = append(c.liveScratch[metric], value)
			}
		}
	}
	var medians [6]time.Duration
	for metric, values := range c.liveScratch {
		if len(values) == 0 {
			continue
		}
		slices.Sort(values)
		middle := len(values) / 2
		medians[metric] = values[middle]
		if len(values)%2 == 0 {
			medians[metric] = (values[middle-1] + values[middle]) / 2
		}
	}
	return fpsLiveSummary{interval: medians[0], draw: medians[1], sim: medians[2], blend: medians[3], record: medians[4], submit: medians[5], frames: frames}
}

type fpsGraphColumn struct {
	interval time.Duration
	draw     time.Duration
	sim      time.Duration
	blend    time.Duration
	record   time.Duration
	submit   time.Duration
}

type fpsGraphSummary struct {
	latest       fpsSample
	peakInterval time.Duration
	peakDraw     time.Duration
	peakSim      time.Duration
	peakBlend    time.Duration
	peakRecord   time.Duration
	peakSubmit   time.Duration
	late, frames int
}

// graph groups samples by elapsed time, retaining each phase's worst duration
// in a pixel column so a brief stall stays visible for 30 seconds.
func (c *fpsCounter) graph(now time.Time, target time.Duration, columns []fpsGraphColumn) fpsGraphSummary {
	var summary fpsGraphSummary
	for i := 0; i < c.count; i++ {
		index := (c.next - 1 - i + len(c.history)) % len(c.history)
		sample := c.history[index]
		age := now.Sub(sample.at)
		if age > fpsHistoryDuration {
			break
		}
		if age < 0 || len(columns) == 0 {
			continue
		}
		if summary.frames == 0 {
			summary.latest = sample
		}
		summary.frames++
		if sample.interval > summary.peakInterval {
			summary.peakInterval = sample.interval
		}
		if sample.draw > summary.peakDraw {
			summary.peakDraw = sample.draw
		}
		if sample.sim > summary.peakSim {
			summary.peakSim = sample.sim
		}
		if sample.blend > summary.peakBlend {
			summary.peakBlend = sample.blend
		}
		if sample.record > summary.peakRecord {
			summary.peakRecord = sample.record
		}
		if sample.submit > summary.peakSubmit {
			summary.peakSubmit = sample.submit
		}
		if target > 0 && sample.interval > target+time.Millisecond {
			summary.late++
		}
		column := len(columns) - 1 - int(age.Nanoseconds()*int64(len(columns))/fpsHistoryDuration.Nanoseconds())
		if column < 0 {
			column = 0
		}
		if sample.interval > columns[column].interval {
			columns[column].interval = sample.interval
		}
		if sample.draw > columns[column].draw {
			columns[column].draw = sample.draw
		}
		if sample.sim > columns[column].sim {
			columns[column].sim = sample.sim
		}
		if sample.blend > columns[column].blend {
			columns[column].blend = sample.blend
		}
		if sample.record > columns[column].record {
			columns[column].record = sample.record
		}
		if sample.submit > columns[column].submit {
			columns[column].submit = sample.submit
		}
	}
	return summary
}
