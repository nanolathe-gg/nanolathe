package ebitenapp

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestClipboardUTF16TextStopsAtNULWithinTheBound(t *testing.T) {
	for _, tc := range []struct {
		units []uint16
		want  string
	}{
		{nil, ""},
		{[]uint16{0}, ""},
		{utf16.Encode([]rune("ABC234\x00stale")), "ABC234"},
		{utf16.Encode([]rune("one\r\ntwo")), "one\r\ntwo"}, // no terminator inside the block
		{utf16.Encode([]rune("é😀")), "é😀"},                 // decoded faithfully; the portable boundary rejects it
		{[]uint16{'a', 0xd800, 'b'}, "a\ufffdb"},           // an unpaired surrogate
	} {
		if got := clipboardUTF16Text(tc.units); got != tc.want {
			t.Fatalf("units %v decoded %q, want %q", tc.units, got, tc.want)
		}
	}
	// A block with no NUL within the bound yields exactly the bound.
	units := slices.Repeat([]uint16{'x'}, clipboardReadLimit+5)
	units[clipboardReadLimit+1] = 0
	if got := clipboardUTF16Text(units); len(got) != clipboardReadLimit || strings.Trim(got, "x") != "" {
		t.Fatalf("unterminated block decoded %d bytes, want %d", len(got), clipboardReadLimit)
	}
}

func TestClipboardCommandsFollowTheSessionAndPath(t *testing.T) {
	installed := func(names ...string) func(string) (string, error) {
		return func(name string) (string, error) {
			if slices.Contains(names, name) {
				return "/opt/bin/" + name, nil
			}
			return "", errors.New("not found")
		}
	}
	env := func(vars ...string) func(string) string {
		return func(name string) string {
			if slices.Contains(vars, name) {
				return "set"
			}
			return ""
		}
	}
	summary := func(commands []clipboardCommand) string {
		var words []string
		for _, c := range commands {
			word := strings.Join(append([]string{c.path}, c.args...), " ")
			if c.emptyIsNone {
				word += " (empty is none)"
			}
			words = append(words, word)
		}
		return strings.Join(words, "; ")
	}
	every := installed("wl-paste", "wl-copy", "xclip", "xsel")
	for _, tc := range []struct {
		name      string
		getenv    func(string) string
		lookPath  func(string) (string, error)
		paste     string
		copyWords string
	}{
		{"wayland with an X server", env("WAYLAND_DISPLAY", "DISPLAY"), every,
			"/opt/bin/wl-paste --no-newline; /opt/bin/xclip -selection clipboard -o; /opt/bin/xsel --clipboard --output (empty is none)",
			"/opt/bin/wl-copy; /opt/bin/xclip -selection clipboard -i; /opt/bin/xsel --clipboard --input (empty is none)"},
		{"wayland alone", env("WAYLAND_DISPLAY"), every, "/opt/bin/wl-paste --no-newline", "/opt/bin/wl-copy"},
		{"X11 alone", env("DISPLAY"), every,
			"/opt/bin/xclip -selection clipboard -o; /opt/bin/xsel --clipboard --output (empty is none)",
			"/opt/bin/xclip -selection clipboard -i; /opt/bin/xsel --clipboard --input (empty is none)"},
		{"X11 with xsel only", env("DISPLAY"), installed("xsel"),
			"/opt/bin/xsel --clipboard --output (empty is none)", "/opt/bin/xsel --clipboard --input (empty is none)"},
		{"wayland without wl-clipboard", env("WAYLAND_DISPLAY"), installed("xclip", "xsel"), "", ""},
		{"a paste-only install", env("WAYLAND_DISPLAY"), installed("wl-paste"), "/opt/bin/wl-paste --no-newline", ""},
		{"no display server", env(), every, "", ""},
	} {
		if got := summary(clipboardCommands(false, tc.getenv, tc.lookPath)); got != tc.paste {
			t.Errorf("%s: paste commands %q, want %q", tc.name, got, tc.paste)
		}
		if got := summary(clipboardCommands(true, tc.getenv, tc.lookPath)); got != tc.copyWords {
			t.Errorf("%s: copy commands %q, want %q", tc.name, got, tc.copyWords)
		}
	}
}

func TestCappedOutputKeepsThePrefixAndAcceptsEveryWrite(t *testing.T) {
	output := cappedOutput{limit: 5}
	for _, chunk := range []string{"ab", "cdef", "ghij", ""} {
		if n, err := output.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("write %q = %d, %v; want every byte accepted", chunk, n, err)
		}
	}
	if string(output.text) != "abcde" {
		t.Fatalf("kept %q, want the first five bytes", output.text)
	}
}
