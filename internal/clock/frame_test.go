package clock

import "testing"

type scriptedMillisSource struct {
	samples []uint32
	index   int
}

func (s *scriptedMillisSource) Millis32() uint32 {
	if s == nil || len(s.samples) == 0 {
		return 0
	}
	if s.index >= len(s.samples) {
		return s.samples[len(s.samples)-1]
	}
	v := s.samples[s.index]
	s.index++
	return v
}

func TestScaledNowBoundariesAndWrap(t *testing.T) {
	tests := []struct {
		name string
		ms   uint32
		want int32
	}{
		{name: "zero", ms: 0, want: 0},
		{name: "before first", ms: 33, want: 0},
		{name: "first", ms: 34, want: 1},
		{name: "before second", ms: 999, want: 29},
		{name: "one second", ms: 1000, want: 30},
		{name: "maximum source", ms: ^uint32(0), want: 128849018},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ScaledNow(test.ms); got != test.want {
				t.Fatalf("ScaledNow(%d) = %d, want %d", test.ms, got, test.want)
			}
		})
	}
}

func TestScriptedSourceDrivesBudgetThroughScaledNow(t *testing.T) {
	source := &scriptedMillisSource{samples: []uint32{0, 33, 34, 67}}
	state := State{Requested: 10, Active: 10}
	want := []int{0, 0, 1, 1}
	for i, expected := range want {
		got := state.AdvanceSP(ScaledNow(source.Millis32()))
		if got != expected {
			t.Fatalf("sample %d budget = %d, want %d", i, got, expected)
		}
	}
}

func TestScriptedSamplesProduceStableSchedulerImage(t *testing.T) {
	samples := []uint32{0, 34, 67, 1000, 1001}
	leftSource := &scriptedMillisSource{samples: samples}
	rightSource := &scriptedMillisSource{samples: samples}
	left := State{Requested: 10, Active: 10}
	right := State{Requested: 10, Active: 10}
	for range samples {
		left.AdvanceSP(ScaledNow(leftSource.Millis32()))
		right.AdvanceSP(ScaledNow(rightSource.Millis32()))
	}
	if left.SaveBox() != right.SaveBox() {
		t.Fatalf("identical scripted samples produced different scheduler images")
	}
}

func TestSourceWrapProducesSignedNegativeDelta(t *testing.T) {
	source := &scriptedMillisSource{samples: []uint32{^uint32(0), 0}}
	state := State{Requested: 10, Active: 10}
	state.AdvanceSP(ScaledNow(source.Millis32()))
	if got := state.AdvanceSP(ScaledNow(source.Millis32())); got != 0 {
		t.Fatalf("wrapped source budget = %d, want 0", got)
	}
	if state.Delta >= 0 {
		t.Fatalf("wrapped source delta = %d, want negative", state.Delta)
	}
}
