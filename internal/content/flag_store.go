package content

import "github.com/nanolathe/nanolathe/formats"

// storedFlag models a definition's one-bit store after the integer accessor,
// not the accessor's general nonzero truth test [02 R-KEYS-01 §5].
func storedFlag(section *formats.Section, key string, fallback bool) bool {
	var def int32
	if fallback {
		def = 1
	}
	return section.IntValue(key, def)&1 != 0
}
