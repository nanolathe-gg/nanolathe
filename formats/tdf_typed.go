package formats

import (
	"strconv"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// The typed accessor family [02 §4 "Typed accessors"]. Every typed read locates
// its key the same way and applies exactly one conversion rule, so a caller
// cannot accidentally mix, say, floating and fixed-point semantics for one
// field. Two consequences the catalog compilers depend on:
//
//   - only an absent key returns the caller's default; an authored zero or
//     unparsable integer text returns zero even with a nonzero default;
//   - the fixed-point accessor's default is stored VERBATIM, so it is already
//     in 16.16 units: a default of 65536 means an authored 1.0.

// IntValue applies retail's CRT decimal conversion: optional sign, leading
// whitespace, decimal digits, trailing junk ignored, zero for unparsable text,
// and no hexadecimal syntax. An absent key returns the caller's default.
func (s *Section) IntValue(key string, def int32) int32 {
	raw, ok := s.FirstValue(key)
	if !ok {
		return def
	}
	return ParseTDFInteger(raw)
}

// FloatValue applies the CRT decimal floating conversion. An absent key returns
// the caller's default.
func (s *Section) FloatValue(key string, def float64) float64 {
	raw, ok := s.FirstValue(key)
	if !ok {
		return def
	}
	return parseTDFFloat(raw)
}

// FixedValue converts an authored floating value into 16.16 by multiplying by
// 65,536 and truncating toward zero. The default is stored verbatim because it
// is already in 16.16 units.
func (s *Section) FixedValue(key string, def int32) int32 {
	raw, ok := s.FirstValue(key)
	if !ok {
		return def
	}
	return FixedFromAuthored(parseTDFFloat(raw))
}

// FixedFromAuthored is the one authored-float-to-16.16 conversion. Callers that
// also scale (weapon velocities scale by 65536/30, for instance) must scale
// before this truncating store, never after [02 "Weapon record"].
func FixedFromAuthored(value float64) int32 {
	return numeric.TruncateFloat64ToLow32(value * 65536.0)
}

// StringValue reports whether the key was found. Unlike the numeric accessors,
// a string field CAN distinguish an authored empty value from a missing key,
// and retail copies the caller's default without applying the length limit.
func (s *Section) StringValue(key, def string) (string, bool) {
	raw, ok := s.FirstValue(key)
	if !ok {
		return def, false
	}
	return raw, true
}

// LanguageString tries `<language><key>` first, then the plain key, then the
// string-accessor rules [02 §3].
func (s *Section) LanguageString(language, key, def string) (string, bool) {
	if language != "" {
		if value, ok := s.StringValue(language+key, ""); ok {
			return value, true
		}
	}
	return s.StringValue(key, def)
}

// RawValue returns the stored text and whether the key was authored at all,
// which is how a caller distinguishes an authored key from a missing one.
func (s *Section) RawValue(key string) (string, bool) { return s.FirstValue(key) }

// BoolValue provides a nonzero integer truth test. Retail has no universal
// boolean parser; packed catalog flags instead retain the integer low bit
// [02 R-KEYS-01 §5].
func (s *Section) BoolValue(key string, def bool) bool {
	raw, ok := s.FirstValue(key)
	if !ok {
		return def
	}
	return ParseTDFInteger(raw) != 0
}

// parseTDFFloat mirrors the CRT decimal floating conversion: leading
// whitespace, optional sign, digits with an optional fraction and exponent,
// trailing junk ignored, zero for unparsable text.
// TODO(question): compare retail CRT overflow and unusual exponent text with
// this host parser; retain the existing zero-on-error policy until traced
// [02 "Missing and unknown"].
func parseTDFFloat(value string) float64 {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	end := 0
	seenDigit, seenDot, seenExp := false, false, false
	for end < len(trimmed) {
		c := trimmed[end]
		switch {
		case c >= '0' && c <= '9':
			seenDigit = true
		case (c == '+' || c == '-') && (end == 0 || (trimmed[end-1] == 'e' || trimmed[end-1] == 'E')):
		case c == '.' && !seenDot && !seenExp:
			seenDot = true
		case (c == 'e' || c == 'E') && seenDigit && !seenExp:
			seenExp = true
		default:
			goto done
		}
		end++
	}
done:
	if !seenDigit {
		return 0
	}
	parsed, err := strconv.ParseFloat(strings.TrimRight(trimmed[:end], "eE+-"), 64)
	if err != nil {
		return 0
	}
	return parsed
}
