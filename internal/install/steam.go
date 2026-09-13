package install

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

// Steam's old numeric-key form and current nested "path" form both describe
// library roots. Only absolute paths qualify; unrelated app IDs do not. A
// bounded read prevents malformed host metadata from consuming arbitrary memory.
func libraryPaths(path string) []string {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	const limit = 1 << 20
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || len(data) > limit {
		return nil
	}
	tokens, ok := vdfTokens(string(data))
	if !ok {
		return nil
	}
	var paths []string
	for i := 0; i+1 < len(tokens); i++ {
		_, numeric := strconv.ParseUint(tokens[i], 10, 32)
		if (strings.EqualFold(tokens[i], "path") || numeric == nil) && filepath.IsAbs(tokens[i+1]) {
			paths = append(paths, tokens[i+1])
		}
	}
	return paths
}

func vdfTokens(data string) ([]string, bool) {
	var tokens []string
	for i := 0; i < len(data); {
		if unicode.IsSpace(rune(data[i])) {
			i++
			continue
		}
		if strings.HasPrefix(data[i:], "//") {
			for i < len(data) && data[i] != '\n' {
				i++
			}
			continue
		}
		if data[i] == '{' || data[i] == '}' {
			tokens = append(tokens, data[i:i+1])
			i++
			continue
		}
		if data[i] != '"' {
			start := i
			for i < len(data) && !unicode.IsSpace(rune(data[i])) && data[i] != '{' && data[i] != '}' {
				i++
			}
			tokens = append(tokens, data[start:i])
			continue
		}
		i++
		var value strings.Builder
		closed := false
		for i < len(data) {
			c := data[i]
			i++
			if c == '"' {
				closed = true
				break
			}
			if c == '\\' && i < len(data) && (data[i] == '\\' || data[i] == '"') {
				c = data[i]
				i++
			}
			value.WriteByte(c)
		}
		if !closed {
			return nil, false
		}
		tokens = append(tokens, value.String())
	}
	return tokens, true
}
