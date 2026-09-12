package mission

import "strconv"

// missionArgScanner consumes adjacent prefixes, and a failed field prevents
// all later assignments [04 §3.6][08 "Argument parsing"]. It is local to this
// grammar: the TDF numeric accessor accepts different exponent spellings.
type missionArgScanner struct {
	text   string
	pos    int
	failed bool
}

func (s *missionArgScanner) start() bool {
	if s.failed {
		return false
	}
	for s.pos < len(s.text) {
		c := s.text[s.pos]
		if c != ' ' && (c < '\t' || c > '\r') {
			break
		}
		s.pos++
	}
	return true
}

func missionDigit(c byte) bool { return c >= '0' && c <= '9' }

func (s *missionArgScanner) integer(dst *int32) bool {
	if !s.start() {
		return false
	}
	negative := false
	if s.pos < len(s.text) && (s.text[s.pos] == '+' || s.text[s.pos] == '-') {
		negative = s.text[s.pos] == '-'
		s.pos++
	}
	begin := s.pos
	var value uint32
	for s.pos < len(s.text) && missionDigit(s.text[s.pos]) {
		value = value*10 + uint32(s.text[s.pos]-'0')
		s.pos++
	}
	if s.pos == begin {
		s.failed = true
		return false
	}
	if negative {
		value = -value
	}
	*dst = int32(value) // Decimal accumulation and negation wrap [04 §3.6].
	return true
}

func (s *missionArgScanner) float(dst *float32) bool {
	if !s.start() {
		return false
	}
	begin := s.pos
	if s.pos < len(s.text) && (s.text[s.pos] == '+' || s.text[s.pos] == '-') {
		s.pos++
	}
	digits := 0
	for s.pos < len(s.text) && missionDigit(s.text[s.pos]) {
		s.pos++
		digits++
	}
	if s.pos < len(s.text) && s.text[s.pos] == '.' {
		s.pos++
		for s.pos < len(s.text) && missionDigit(s.text[s.pos]) {
			s.pos++
			digits++
		}
	}
	if digits == 0 {
		s.failed = true
		return false
	}
	end := s.pos
	if s.pos < len(s.text) && (s.text[s.pos] == 'e' || s.text[s.pos] == 'E') {
		s.pos++
		if s.pos < len(s.text) && (s.text[s.pos] == '+' || s.text[s.pos] == '-') {
			s.pos++
		}
		exponent := s.pos
		for s.pos < len(s.text) && missionDigit(s.text[s.pos]) {
			s.pos++
		}
		if s.pos > exponent {
			end = s.pos
		}
		// Incomplete exponents are consumed, but conversion keeps the
		// mantissa value [08 "Argument parsing"].
	}
	// TODO(question): exact rounding of adversarial decimals through retail's
	// intermediate conversion is untraced. Use Go's binary32 rounding until
	// that arithmetic is established; grammar and overflow are established
	// [fmt ota]. This result is promoted only after the binary32 boundary.
	value, _ := strconv.ParseFloat(s.text[begin:end], 32)
	*dst = float32(value) // Range overflow succeeds with signed infinity [04 §3.6].
	return true
}

func (s *missionArgScanner) name(dst *string, underscore bool) bool {
	if !s.start() {
		return false
	}
	begin := s.pos
	for s.pos < len(s.text) {
		c := s.text[s.pos]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || missionDigit(c) || c == '.' || underscore && c == '_') {
			break
		}
		s.pos++
	}
	if s.pos == begin {
		s.failed = true
		return false
	}
	*dst = s.text[begin:s.pos]
	return true
}

func missionCoordinateSeeds() (float32, float32) {
	// TODO(question): failed unchecked coordinate scans can retain run-specific
	// temporary contents [04 §3.6]. OTA text cannot settle those values; an
	// execution-state trace would only establish one run. Keep the existing
	// bounded host policy of zero for untouched coordinates, not a retail seed.
	return 0, 0
}
