package gui

import "github.com/nanolathe/nanolathe/internal/input"

// Kind is the stored control-type byte [02 §6 "Interface panel files (.gui)"] [07 §4].
// Retail gadgets have 347-byte record identity [07 §4][02 §6 "Control-kind mapping"]; Go uses named fields per I13.
type Kind uint8

const (
	KindPanel     Kind = 0  // background/panel with -1 centering and BackTile fallback [07 §4]
	KindButton    Kind = 1  // button with staged frames and | labels [07 §4]
	KindListBox   Kind = 2  // listbox [07 §4][02 §6]
	KindTextBox   Kind = 3  // text input, name capped at 127 bytes [07 §4]
	KindScrollBar Kind = 4  // scrollbar [07 §4][02 §6]
	KindLabel     Kind = 5  // label [02 §6]
	KindSurface   Kind = 6  // blank surface [02 §6]
	KindFont      Kind = 7  // font selector [02 §6]
	KindSlider    Kind = 8  // slider synthesizing two scrollbar children [07 §4] TODO(question): exact numeric id not enumerated in accessible spec; 8 chosen as first free id
	KindText      Kind = 9  // text case [07 §4] TODO(question)
	KindZero      Kind = 10 // unnamed zeroing case [07 §4] TODO(question)
	KindEmbedded1 Kind = 11 // embedded-file case [07 §4] TODO(question)
	KindPicture   Kind = 12 // picture-box style control [02 §6][07 §4]
	KindRepeat    Kind = 13 // repeating/decrementing runtime family 12/13 mapped [07 §4] — also covers second embedded-file / single-purpose slot
	KindSingle1   Kind = 14 // single-purpose placeholder [07 §4] TODO(question)
	KindSingle2   Kind = 15 // single-purpose placeholder [07 §4] TODO(question)
)

// RuntimeFamily maps a stored Kind to its runtime dispatch family [07 §4].
// Families 1,2,3,4,5,6,12,13 have distinct runtime paths; others return 0 (no dedicated family).
func (k Kind) RuntimeFamily() uint8 {
	switch k {
	case KindButton:
		return 1 // clickable with callback result [07 §4]
	case KindListBox:
		return 2 // stateful [07 §4]
	case KindTextBox:
		return 3 // focusable text editor [07 §4]
	case KindScrollBar:
		return 4 // dedicated update [07 §4]
	case KindLabel:
		return 5 // association-capable [07 §4]
	case KindSurface:
		return 6 // callback-producing [07 §4]
	case KindPicture:
		return 12 // repeating/decrementing [07 §4]
	case KindRepeat:
		return 13 // timed/range animating [07 §4]
	default:
		return 0
	}
}

// Rect is a logical-space rectangle [02 §6][07 §1].
// xpos,ypos,width,height are stored as int16 [02 §6]; we keep int32 per I13.
// -1 centres on axis, -2 anchors to far edge [02 §6][07 §4]; resolution uses 640×480 [02 §1][07 §1].
// TODO(question): no general display-scale conversion contract is established [07 §4]; HUD is laid out at logical size and scaled at present.
type Rect struct {
	X, Y int32 // placed position after sentinel resolution
	W, H int32 // stored widths honored [PLAN_12] — verbatim authored width/height

	RawX, RawY int32 // authored xpos/ypos before sentinel [02 §6]
}

// Gadget is a compiled GUI control. Retail identity is a fixed 347-byte record [07 §4][02 §6];
// Go uses these named fields per I13, not packing.
type Gadget struct {
	// Common header [02 §6 "COMMON"] — every gadget.
	Kind          Kind   // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	Name          string // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	Assoc         int32  // assoc/group index [02 §6]
	Rect          Rect   // xpos,ypos,width,height stored as int16 [02 §6]
	Attribs       uint32 // 32-bit attribute word [02 §6]
	ColorF        uint16 // masked to 16 bits [02 §6]
	ColorB        uint16 // masked to 16 bits [02 §6]
	TextureNumber uint8  // stored as byte [02 §6]
	FontNumber    uint8  // stored as byte [02 §6]
	Active        uint8  // byte, 0 hidden [02 §6][07 §3]
	CommonAttribs uint8  // byte [02 §6]
	Help          string // help string [02 §6]
	GAFFile       int16  // gaffile stored as 16-bit [02 §6] TODO(question): not parsed by formats fillCommon, read from common.gaffile manually

	// Type-specific fields, read per control kind [02 §6][07 §4].
	// Not every kind uses every field; unused stays zero/empty.
	Status    int16    // button status [02 §6]
	Text      string   // button label, scrollbar text etc. [02 §6]
	Labels    []string // staged button labels split by '|' [07 §4]
	QuickKey  byte     // button quickkey stored as byte [02 §6]
	GrayedOut int16    // button grayedout stored as 16-bit [02 §6] — also grayed bit in attribs [07 §3]
	Stages    uint8    // button stages stored as byte [02 §6]

	Range    int16 // scrollbar/list range stored as 16-bit [02 §6]
	KnobPos  int16 // scrollbar knobpos [02 §6]
	KnobSize int16 // scrollbar knobsize [02 §6]
	Thick    int32 // scrollbar thick 32-bit [02 §6]

	Link     string // label link [02 §6]
	FileName string // font filename [02 §6]
	HotOrNot int32  // blank surface hotornot [02 §6]
	Nuttin   int32  // compound control [02 §6] TODO(question): semantics unknown, retained

	ItemHeight int16 // listbox itemheight rare [02 §6][fmt gui]
	MaxChars   int16 // textbox maxchars stored as 16-bit capped at 128 [07 §4]

	// Art resolution order: own named GAF entry first, then side-specific interface GAF, then built-in fallback [07 §4][02 §6].
	// Own entry is Name; Fallback is BackTile chain for panel [07 §4].
	Art         string // primary art name (Name) [02 §6]
	FallbackArt string // TODO(question): full BackTile fallback chain order not established [07 §4]

	// Provenance
	SourceName string // original TDF section name like GADGET0
}

// Header holds the panel header keys from GADGET0 [02 §6].
type Header struct {
	TotalGadgets int16  // totalgadgets stored as 16-bit [02 §6]
	Panel        string // panel background art, BackTile fallback [07 §4]
	CrDefault    string // crdefault 16 bytes [02 §6]
	EscDefault   string // escdefault 16 bytes [02 §6]
	DefaultFocus string // defaultfocus 16 bytes [02 §6]
	VersionMajor uint8
	VersionMinor uint8
	VersionRev   uint8
	HasVersion   bool
}

// Window is a compiled GUI panel. GADGET0 is the panel header; its rect is also Window.Rect [02 §6].
// Gadgets includes the header as index 0 for provenance; consumers that want only controls can slice Gadgets[1:].
type Window struct {
	Name    string   // logical file path like guis/MAINMENU.GUI
	Rect    Rect     // header rect after clamping to stay on-screen [02 §6]
	Gadgets []Gadget // all gadgets in file order [07 §4] — header at 0
	Focus   int      // index into Gadgets of default focus, -1 if none [02 §6]
	Header  Header   // header fields
}

// Event is a GUI input event for dispatch. WU-12-2 will expand runtime families 1,2,3,4,5,6,12,13 [07 §4].
// We keep a minimal compatibility shape now so load.go compiles and tests can drive HitTest.
// TODO(question): latch 0xB (TELEPORT) and 0xE (MOBILEBUILD) can be armed by paths other than the button chain [07 §9]; only button path wired here.
type Event struct {
	X, Y  int32
	Key   input.Token
	Mouse input.MouseRecord
	// TODO(question): OEM key aliases 0xBA..0xC0 and 0xDB..0xDE not established [07 §2]
}

// Result is the outcome of dispatch [07 §3].
type Result struct {
	Handled bool
	Index   int // gadget index that handled event, -1 if none
}

// ArtSources returns the art resolution order for a gadget: own entry, side GAF, fallback [07 §4].
func (g *Gadget) ArtSources() []string {
	srcs := []string{}
	if g.Art != "" {
		srcs = append(srcs, g.Art)
	}
	// TODO(question): side-specific interface GAF name not known at load time; placeholder for side GAF lookup.
	// Side GAF would be resolved via content.SideDef.IntGAF per side [02 §6].
	if g.FallbackArt != "" {
		srcs = append(srcs, g.FallbackArt)
	}
	// Built-in fallback chain always ends with BackTile for panel [07 §4].
	if g.Kind == KindPanel && g.FallbackArt != "BackTile" {
		// Ensure BackTile is last fallback if not already present.
		found := false
		for _, s := range srcs {
			if s == "BackTile" {
				found = true
				break
			}
		}
		if !found {
			srcs = append(srcs, "BackTile")
		}
	}
	return srcs
}
