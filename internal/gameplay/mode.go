// Package gameplay names Nanolathe's explicit simulation policy choices.
package gameplay

import "fmt"

// Mode is independent of the presentation renderer. The zero value selects Modern.
type Mode string

const (
	Modern   Mode = "modern"
	Strict31 Mode = "strict-3.1"
)

func (m Mode) Normalize() Mode {
	if m == Strict31 {
		return Strict31
	}
	return Modern
}

func Parse(text string) (Mode, error) {
	switch Mode(text) {
	case Modern, Strict31:
		return Mode(text), nil
	}
	return Modern, fmt.Errorf("nanolathe: invalid gameplay mode: logical path <command line>, providers searched [gameplay], expected modern or strict-3.1")
}
