package formats

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/vfs"
)

type GUI struct {
	Gadgets []Gadget
	Header  GUIHeader
	Binary  bool
	// Repaired records that the source needed the bounded EOF-tail repair
	// below. Document is the TDF tree the gadgets came from — including the
	// repaired one — so a caller never has to parse the bytes a second time
	// and reach a different answer. A binary GUI gets an empty tree, never a
	// nil one.
	Repaired bool
	Document *Document
}

type GUIHeader struct {
	TotalGadgets int
	Panel        string
	CrDefault    string
	EscDefault   string
	DefaultFocus string
	VersionMajor int
	VersionMinor int
	VersionRev   int
	HasVersion   bool
}

type Gadget struct {
	SourceName string
	Common     CommonGadget
	Fields     map[string]string
	Section    *SectionView
}

type CommonGadget struct {
	ID, Assoc                        int
	Name                             string
	X, Y                             int
	Width, Height                    int
	Attributes                       int
	ColorForeground, ColorBackground int
	TextureNumber, FontNumber        int
	Active, CommonAttributes         int
	Help                             string
}

// SectionView is a small generic view used for GUI-specific nested records
// such as VERSION. Unknown fields remain available rather than being dropped.
type SectionView struct {
	Name   string
	Fields map[string]string
	Child  []*SectionView
}

func LoadGUI(data []byte) (*GUI, error) {
	document, err := ParseTDF(data)
	if err != nil && isBinaryGUI(data) {
		return loadBinaryGUI(data), nil
	}
	repaired := false
	if err != nil && strings.Contains(err.Error(), "unterminated section") {
		// A retail SCORE.GUI ends after the final assignment without the
		// enclosing gadget brace. Recover only this bounded EOF-tail case;
		// other structural errors remain hard failures.
		if end := guiTail(data); end >= 0 && data[end] == ';' {
			repairedData := make([]byte, len(data)+2)
			copy(repairedData, data)
			repairedData[len(data)] = '\n'
			repairedData[len(data)+1] = '}'
			document, err = ParseTDF(repairedData)
			if err == nil {
				repaired = true
			}
		}
	}
	if err != nil {
		return nil, err
	}
	gui := &GUI{Repaired: repaired, Document: document}
	for _, section := range document.Root.Sections() {
		gadget := Gadget{SourceName: section.OriginalName, Fields: map[string]string{}, Section: makeSectionView(section)}
		common := section.Section("common")
		if common == nil {
			return nil, fmt.Errorf("gui: gadget %q has no COMMON section", section.OriginalName)
		}
		for _, item := range section.Assignments() {
			gadget.Fields[item.Key] = item.Value
		}
		for _, item := range common.Assignments() {
			gadget.Fields["common."+item.Key] = item.Value
		}
		if err := fillCommon(&gadget.Common, common); err != nil {
			return nil, fmt.Errorf("gui: gadget %q: %w", section.OriginalName, err)
		}
		// Preserve version subsection if present on this gadget (typically GADGET0).
		if version := section.Section("version"); version != nil {
			for _, item := range version.Assignments() {
				gadget.Fields["version."+item.Key] = item.Value
			}
		}
		gui.Gadgets = append(gui.Gadgets, gadget)
	}
	if len(gui.Gadgets) == 0 {
		return nil, fmt.Errorf("gui: no gadgets")
	}
	// Header lives on GADGET0 per 02: totalgadgets/panel/crdefault/escdefault/defaultfocus + optional [VERSION].
	if len(gui.Gadgets) > 0 {
		header := &gui.Gadgets[0]
		if value, ok := header.Fields["totalgadgets"]; ok {
			gui.Header.TotalGadgets = atoi(value)
		}
		if value, ok := header.Fields["panel"]; ok {
			gui.Header.Panel = value
		}
		if value, ok := header.Fields["crdefault"]; ok {
			gui.Header.CrDefault = value
		}
		if value, ok := header.Fields["escdefault"]; ok {
			gui.Header.EscDefault = value
		}
		if value, ok := header.Fields["defaultfocus"]; ok {
			gui.Header.DefaultFocus = value
		} else if value, ok := header.Fields["defaultFocus"]; ok {
			gui.Header.DefaultFocus = value
		}
		if _, ok := header.Fields["version.major"]; ok {
			gui.Header.HasVersion = true
			gui.Header.VersionMajor = atoi(header.Fields["version.major"])
			gui.Header.VersionMinor = atoi(header.Fields["version.minor"])
			gui.Header.VersionRev = atoi(header.Fields["version.revision"])
		}
		// Inclusive gadget convention: totalgadgets=N admits GADGET0..GADGETN (N+1 total).
		// Retail files follow this; we validate but tolerate extra/missing for mods.
		if gui.Header.TotalGadgets > 0 && len(gui.Gadgets) != gui.Header.TotalGadgets+1 {
			// Preserve file but record mismatch via Repaired flag is not appropriate; instead
			// allow load but caller can inspect Header.TotalGadgets vs len(Gadgets).
		}
	}
	return gui, nil
}

func atoi(value string) int {
	var result int
	_, _ = fmt.Sscanf(strings.TrimSpace(value), "%d", &result)
	return result
}

func isBinaryGUI(data []byte) bool {
	return len(data) >= 16 && data[0] == 0 && data[1] == 0xcd && bytes.Contains(data[:min(len(data), 256)], []byte("HEADER"))
}

func guiTail(data []byte) int {
	for index := len(data) - 1; index >= 0; index-- {
		switch data[index] {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return index
		}
	}
	return -1
}

func loadBinaryGUI(data []byte) *GUI {
	labels := binaryGUILabels(data)
	if len(labels) == 0 {
		labels = []string{"binary GUI resource"}
	}
	// A binary GUI has no TDF tree, but callers walk Document unconditionally,
	// so it gets an empty one rather than a nil to guard against.
	gui := &GUI{Binary: true, Gadgets: make([]Gadget, 0, len(labels)), Document: &Document{Root: &Section{}}}
	for index, label := range labels {
		id := 5
		if strings.EqualFold(label, "HEADER") {
			id = 0
		}
		fields := map[string]string{"text": label}
		if id == 0 {
			delete(fields, "text")
		}
		gui.Gadgets = append(gui.Gadgets, Gadget{
			SourceName: fmt.Sprintf("BINARY%d", index),
			Common:     CommonGadget{ID: id, Name: label, X: 32, Y: 24 + index*24, Width: 576, Height: 20, Active: 1},
			Fields:     fields,
		})
	}
	return gui
}

func binaryGUILabels(data []byte) []string {
	seen := make(map[string]bool)
	labels := make([]string, 0)
	for start := 0; start < len(data); {
		if !isGUIStringStart(data[start]) {
			start++
			continue
		}
		end := start + 1
		for end < len(data) && isGUIStringByte(data[end]) {
			end++
		}
		if end-start >= 4 {
			label := strings.TrimSpace(string(data[start:end]))
			if !seen[label] {
				seen[label] = true
				labels = append(labels, label)
			}
		}
		start = end
	}
	return labels
}

func isGUIStringStart(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func isGUIStringByte(value byte) bool {
	return isGUIStringStart(value) || value >= '0' && value <= '9' || value == ' ' || value == '_' || value == '-' || value == '!' || value == '?' || value == '.'
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func LoadGUIFile(fs vfs.FSOps, name string) (*GUI, error) {
	data, err := readVFS(fs, name)
	if err != nil {
		return nil, err
	}
	return LoadGUI(data)
}

func fillCommon(out *CommonGadget, section *Section) error {
	var err error
	out.Name, _ = section.LastValue("name")
	out.Help, _ = section.LastValue("help")
	if out.ID, err = guiInt(section, "id"); err != nil {
		return err
	}
	out.Assoc, err = guiInt(section, "assoc")
	if err != nil {
		return err
	}
	out.X, err = guiInt(section, "xpos")
	if err != nil {
		return err
	}
	out.Y, err = guiInt(section, "ypos")
	if err != nil {
		return err
	}
	out.Width, err = guiInt(section, "width")
	if err != nil {
		return err
	}
	out.Height, err = guiInt(section, "height")
	if err != nil {
		return err
	}
	out.Attributes, err = guiInt(section, "attribs")
	if err != nil {
		return err
	}
	out.ColorForeground, err = guiInt(section, "colorf")
	if err != nil {
		return err
	}
	out.ColorBackground, err = guiInt(section, "colorb")
	if err != nil {
		return err
	}
	out.TextureNumber, err = guiInt(section, "texturenumber")
	if err != nil {
		return err
	}
	out.FontNumber, err = guiInt(section, "fontnumber")
	if err != nil {
		return err
	}
	out.Active, err = guiInt(section, "active")
	if err != nil {
		return err
	}
	out.CommonAttributes, err = guiInt(section, "commonattribs")
	if err != nil {
		return err
	}
	return nil
}

func guiInt(section *Section, key string) (int, error) {
	value, present, err := section.Int(key)
	if err != nil || !present {
		return 0, nil
	}
	return int(value), nil
}

func makeSectionView(section *Section) *SectionView {
	view := &SectionView{Name: section.OriginalName, Fields: map[string]string{}}
	for _, item := range section.Items {
		if item.Kind == Assignment {
			view.Fields[item.Key] = item.Value
			continue
		}
		view.Child = append(view.Child, makeSectionView(item.Section))
	}
	return view
}
