package gui

import "github.com/nanolathe-gg/nanolathe/formats"

// Kind is the stored control-type byte [02 §6 "Interface panel files (.gui)"] [07 §4].
// Retail gadgets have 347-byte record identity [07 §4][02 §6 "Control-kind mapping"]; Go uses named fields per I13.
type Kind uint8

// The kind byte and the arms it selects are the builder's fourteen-entry
// switch, indexed 0..13 after an unsigned "> 13" bounds test
// [07 R-WGT-01 §12]. Keys 6, 9 and 10 and every value above 13 do no build
// work; their records are still parsed and still serviced. Kind 9 has no arm,
// no per-kind keys [07 R-WGT-01 §11] and no traced runtime role, so it gets no
// name here — an id without a behavior is not a control type.
const (
	KindPanel      Kind = 0  // panel: window GAF, centring offset, panel art own -> common -> BackTile [07 R-WGT-01 §12]
	KindButton     Kind = 1  // button with staged frames and | labels [07 R-WGT-01 §3]
	KindListBox    Kind = 2  // listbox; assoc peers share the larger itemheight [07 R-WGT-01 §12]
	KindTextBox    Kind = 3  // text input; the builder caps maxchars at 127 [07 R-WGT-01 §12]
	KindScrollBar  Kind = 4  // scrollbar and slider are one kind [07 R-WGT-01 §5]
	KindLabel      Kind = 5  // label; an empty link makes it inert [07 R-WGT-01 §7]
	KindSurface    Kind = 6  // blank surface with a per-pass callback and hotornot [07 R-WGT-01 §8]
	KindFont       Kind = 7  // font: loads <font directory>\<filename>.FNT whole [07 R-WGT-01 §12]
	KindRawFile    Kind = 8  // raw file: loads the authored filename verbatim; nothing reads it [07 R-WGT-01 §12]
	KindLine       Kind = 10 // line painter; reads nuttin and needs no build work [07 R-WGT-01 §8][R-WGT-01 §11]
	KindPanelAlias Kind = 11 // shares the panel arm with kind 0 [07 R-WGT-01 §12]
	KindPicture    Kind = 12 // picture box: frame 0 by name, own GAF then common [07 R-WGT-01 §12]
	KindScoreBar   Kind = 13 // score bar: next-due stamp = scaled timer + interval [07 R-WGT-01 §12]
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
		return 4 // scrollbar/slider update [07 §4][07 R-WGT-01 §5]
	case KindLabel:
		return 5 // link redirection [07 §4][07 R-WGT-01 §7]
	case KindSurface:
		return 6 // per-pass callback and hotornot click [07 §4][07 R-WGT-01 §8]
	case KindPicture:
		return 12 // repeating/decrementing [07 §4]
	case KindScoreBar:
		return 13 // timed/range animating [07 §4]
	default:
		return 0
	}
}

// Rect is a logical-space rectangle [02 §6][07 §1].
// xpos,ypos,width,height are stored as int16 [02 §6]; we keep int32 per I13.
// -1 centres on axis, -2 anchors to far edge [02 §6][07 §4]; resolution uses 640×480 [02 §1][07 §1].
// [07 §1] fixes the authored resolution at 640x480 and [07 §4] gives no
// display-scale conversion, because retail has none to give: the interface is
// authored at that one size. Laying the HUD out at logical size and scaling at
// present is our own choice for other window sizes, not a gap.
type Rect struct {
	X, Y int32 // placed position after sentinel resolution
	W, H int32 // stored widths honored [PLAN_12] — verbatim authored width/height

	RawX, RawY int32 // authored xpos/ypos before sentinel [02 §6]
}

// Gadget is a compiled GUI control. Retail identity is a fixed 347-byte record [07 §4][02 §6];
// Go uses these named fields per I13, not packing.
type Gadget struct {
	// Common header [02 §6 "COMMON"] — every gadget.
	Kind          Kind   // Authored gadget ID [02 §6].
	Name          string // Authored gadget name [02 §6][07 §4].
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
	GAFFile       int16  // gaffile stored as 16-bit [02 §6]; read from common.gaffile here because formats' fillCommon does not cover it

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
	FileName string // authored filename, kinds 7 and 8 [02 §6][07 R-WGT-01 §11]
	// FilePath is the gadget's file slot: the path the builder opens for a
	// kind-7 font (`<font directory>\<filename>.FNT`) or a kind-8 raw file
	// (the authored `filename` verbatim). Both arms write the same slot
	// [07 R-WGT-01 §12].
	FilePath string
	HotOrNot int32 // blank surface hotornot, bit 0 of the surface flag word [02 §6][07 R-WGT-01 §11]
	// `nuttin` is the kind-10 line gadget's key, stored as a 32-bit word at the
	// start of the text field [07 R-WGT-01 §11]. This used to carry an open
	// question — whether the line painter reads it as the attribute-4
	// (outline) second X coordinate — closed by an instruction-level trace of
	// the painter: it never reads the text field's `nuttin` slot at all. The
	// outline case's second X coordinate is `x + w - 1`, the gadget's own
	// rect, computed the same way as the horizontal line's endpoint and left
	// unmodified on that path [07 R-WGT-01 §8]. `nuttin` is parsed and
	// retained losslessly; no traced reader consumes it.
	Nuttin int32 // kind-10 line gadget [02 §6][07 R-WGT-01 §11]

	ItemHeight int16 // listbox itemheight rare [02 §6][fmt gui]
	MaxChars   int16 // textbox maxchars stored as 16-bit capped at 128 [07 §4]

	// Art resolution order: own named GAF entry first, then side-specific interface GAF, then built-in fallback [07 §4][02 §6].
	// Own entry is Name; Fallback is BackTile chain for panel [07 §4].
	Art         string // primary art name (Name) [02 §6]
	FallbackArt string // built-in fallback, last link of [07 §4]'s own-entry -> side GAF -> built-in chain
	// ArtFrame selects one frame inside Art. Authored gadgets leave it 0 —
	// the picture box blits frame 0 of the entry named by the gadget
	// [07 R-WGT-01 §12] — and the builder's synthesized slider arrows carry
	// frames base+6 and base+8 of SLIDERS here [07 R-WGT-01 §5].
	ArtFrame int32

	// ButtonArt retains the immutable entry selected by the runtime window
	// builder. Its identity includes its GAF provider; resolving its name again
	// could select a different provider [07 R-WGT-01 §3]. These fields are not
	// authored GUI data. A resolved nil entry means the builder found no art.
	ButtonArt         *formats.GAFEntry
	ButtonArtResolved bool
	// ExternalArt is the per-gadget GAF entry selected by an odd gaffile
	// prepass. A resolved nil records an absent resource/entry and prevents a
	// later builder arm from falling through to ordinary art [07 R-WGT-01 §3].
	ExternalArt         *formats.GAFEntry
	ExternalArtResolved bool

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
	Name string // logical file path like guis/MAINMENU.GUI
	Rect Rect   // header rect after clamping to stay on-screen [02 §6]
	// OriginX/OriginY are the header's authored placement. Retail stores
	// controls in window-local coordinates; the renderer and hit tester add
	// this origin when placing every non-header gadget [02 §6][07 §4].
	OriginX int32
	OriginY int32
	Gadgets []Gadget // all gadgets in file order [07 §4] — header at 0
	Focus   int      // index into Gadgets of default focus, -1 if none [02 §6]
	Header  Header   // header fields

	// fonts caches the FNT each kind-7 record's file slot loads, keyed by
	// gadget index and filled by Font on first use; a nil value records a
	// load that failed, so a missing file is looked up once [07 R-WGT-01 §12].
	fonts map[int]*formats.FNT
}

// PlacedRect returns a gadget's screen-space rectangle. Gadget.Rect remains
// the authored/local rectangle so format and GUI tests can inspect it without
// losing provenance. The header itself is already screen-space.
func (w *Window) PlacedRect(index int) Rect {
	if w == nil || index < 0 || index >= len(w.Gadgets) {
		return Rect{}
	}
	r := w.Gadgets[index].Rect
	if index != 0 {
		r.X += w.OriginX
		r.Y += w.OriginY
	}
	return r
}

// ArtSource is one (GAF, entry name) hop in a gadget's art resolution chain
// [07 §4]. GAF is a hint for which GAF this hop searches, not a resolved
// file handle: "" means the window's own page/panel art GAF — the same
// default root a caller already holds — and any other value names the
// specific support GAF that hop searches instead.
type ArtSource struct {
	GAF  string
	Name string
}

// ArtSources returns the art resolution order for a gadget: its own named
// entry in the window's own art GAF, then that same entry in the
// side-specific interface GAF, then the built-in fallback [07 §4]. This is
// the same three-link chain — page, then side intGAF, then common — that
// cmd/nanolathe's gadgetArtEntry and modalGadgetFrameState walk in
// production (landed under WU-19-37); this function mirrors their file
// order for callers that want it without a battle-HUD instance.
//
// sideIntGAF is the side's interface GAF handle, the same
// content.SideDef.IntGAF [02 §6] the three battle panel frames
// (PANELTOP/PANELSIDE/PANELBOT) are drawn from. Pass "" when no side is
// known; the middle link is then skipped, matching production's nil-GAF
// skip in the same search loop.
func (g *Gadget) ArtSources(sideIntGAF string) []ArtSource {
	var srcs []ArtSource
	name := g.Art
	if name == "" {
		name = g.Name
	}
	if name != "" {
		srcs = append(srcs, ArtSource{Name: name})
		if sideIntGAF != "" {
			srcs = append(srcs, ArtSource{GAF: sideIntGAF, Name: name})
		}
	}
	if g.FallbackArt != "" {
		srcs = append(srcs, ArtSource{Name: g.FallbackArt})
	}
	// Built-in fallback chain always ends with BackTile for panel [07 §4].
	if g.Kind == KindPanel {
		found := false
		for _, s := range srcs {
			if s.Name == "BackTile" {
				found = true
				break
			}
		}
		if !found {
			srcs = append(srcs, ArtSource{Name: "BackTile"})
		}
	}
	return srcs
}
