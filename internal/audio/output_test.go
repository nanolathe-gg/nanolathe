package audio

type outputPlay struct {
	sample *Sample
	volume float64
	pan    float64
}

type outputSpy struct {
	plays []outputPlay
}

func (s *outputSpy) PlaySample(sample *Sample, volume, pan float64) error {
	s.plays = append(s.plays, outputPlay{sample: sample, volume: volume, pan: pan})
	return nil
}
