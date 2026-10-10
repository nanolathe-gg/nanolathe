package ebitenapp

import (
	"slices"
	"unicode/utf16"
)

// The host-neutral parts of the Windows and unix clipboard bridges live here,
// untagged, so their tests run on every host.

// clipboardReadLimit bounds one native clipboard read: UTF-16 units on
// Windows, bytes from a helper program on unix desktops. A paste keeps far
// less (at most maxchars − 1 bytes [07 §2]); the bound only stops a huge
// clipboard from costing a frame. It is a host bound, not a retail value.
const clipboardReadLimit = 64 << 10

// clipboardUTF16Text decodes a CF_UNICODETEXT block, which ends at its first
// NUL, reading at most clipboardReadLimit units even when no NUL comes.
// Unpaired surrogates decode to U+FFFD, which the portable boundary then
// rejects with the rest of the unmapped text.
func clipboardUTF16Text(units []uint16) string {
	units = units[:min(len(units), clipboardReadLimit)]
	if end := slices.Index(units, 0); end >= 0 {
		units = units[:end]
	}
	return string(utf16.Decode(units))
}

// clipboardHelper is one clipboard program's paste and copy invocations; the
// first word of each is looked up on PATH.
type clipboardHelper struct {
	// display is the environment variable that names the display server the
	// program talks to; the helper is tried only in a session that sets it.
	display     string
	paste, copy []string
	// emptyIsNone marks a program whose empty output cannot be told from a
	// clipboard with no text, so empty output is taken as no text and the
	// editor is preserved [07 §2].
	emptyIsNone bool
}

// clipboardHelpers is the preference order: the Wayland program first, then
// the two X11 programs, which also serve a Wayland session's X server.
var clipboardHelpers = []clipboardHelper{
	{display: "WAYLAND_DISPLAY", paste: []string{"wl-paste", "--no-newline"}, copy: []string{"wl-copy"}},
	{display: "DISPLAY", paste: []string{"xclip", "-selection", "clipboard", "-o"}, copy: []string{"xclip", "-selection", "clipboard", "-i"}},
	{display: "DISPLAY", paste: []string{"xsel", "--clipboard", "--output"}, copy: []string{"xsel", "--clipboard", "--input"}, emptyIsNone: true},
}

// clipboardCommand is one runnable helper invocation.
type clipboardCommand struct {
	path        string
	args        []string
	emptyIsNone bool
}

// clipboardCommands lists, in preference order, the paste or copy
// invocations this session can run: the helper's display server is set in
// the environment and its program is on PATH.
func clipboardCommands(copying bool, getenv func(string) string, lookPath func(string) (string, error)) []clipboardCommand {
	var commands []clipboardCommand
	for _, helper := range clipboardHelpers {
		if getenv(helper.display) == "" {
			continue
		}
		argv := helper.paste
		if copying {
			argv = helper.copy
		}
		path, err := lookPath(argv[0])
		if err != nil {
			continue
		}
		commands = append(commands, clipboardCommand{path: path, args: argv[1:], emptyIsNone: helper.emptyIsNone})
	}
	return commands
}

// cappedOutput keeps the first limit bytes a helper prints and discards the
// rest. It accepts every write, so the helper never stalls on a full pipe and
// exits on its own well inside the wait bound.
type cappedOutput struct {
	text  []byte
	limit int
}

func (c *cappedOutput) Write(p []byte) (int, error) {
	if room := c.limit - len(c.text); room > 0 {
		c.text = append(c.text, p[:min(room, len(p))]...)
	}
	return len(p), nil
}
