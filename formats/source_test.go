package formats

import (
	"encoding/binary"
	"testing"
)

func TestTNTDecodesSimulationSourceSectionsAndSentinels(t *testing.T) {
	data := make([]byte, 0x40+2+16+1024+132+8+3)
	put := func(off int, value uint32) { binary.LittleEndian.PutUint32(data[off:], value) }
	put(0, 0x2000)
	put(4, 2)
	put(8, 2)
	put(0xc, 0x40)
	put(0x10, 0x42)
	put(0x14, 0x52)
	put(0x18, 1)
	put(0x1c, 1)
	put(0x20, 0x452)
	put(0x24, 75)
	put(0x28, 0x4d6)
	put(0x2c, 1)
	binary.LittleEndian.PutUint16(data[0x40:], 0)
	data[0x42] = 17
	binary.LittleEndian.PutUint16(data[0x43:], 0xffff)
	data[0x45] = 1
	data[0x46] = 18
	binary.LittleEndian.PutUint16(data[0x47:], 0xfffe)
	data[0x49] = 2
	data[0x4a] = 19
	binary.LittleEndian.PutUint16(data[0x4b:], 0xfffc)
	data[0x52] = 3
	binary.LittleEndian.PutUint32(data[0x452:], 7)
	copy(data[0x456:], "tree\x00")
	put(0x4d6, 1)
	put(0x4da, 3)
	data[0x4de], data[0x4df], data[0x4e0] = 9, 8, 7
	tnt, err := LoadTNT(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(tnt.TileIndices) != 1 || tnt.TileIndices[0] != 0 || len(tnt.Attributes) != 4 || tnt.Attributes[1].Feature != 0xfffe || tnt.Attributes[2].Feature != 0xfffc {
		t.Fatalf("decoded TNT = %+v", tnt)
	}
	if len(tnt.TileGraphics) != 1024 || len(tnt.FeatureTable) != 1 || tnt.FeatureTable[0].Name != "tree" || tnt.MinimapWidth != 1 || tnt.Minimap[2] != 7 {
		t.Fatalf("decoded TNT sections = %+v", tnt)
	}
	if len(tnt.Raw) != len(data) || &tnt.TileIndexBytes[0] != &tnt.Raw[0x40] || &tnt.TileGraphics[0] != &tnt.Raw[0x52] || &tnt.Minimap[0] != &tnt.Raw[0x4de] {
		t.Fatal("TNT decoded views are not backed by the owned source buffer")
	}
}

func TestTNTRejectsOversizedAttributeGridBeforeAllocation(t *testing.T) {
	data := make([]byte, 0x40)
	binary.LittleEndian.PutUint32(data[4:], 8192)
	binary.LittleEndian.PutUint32(data[8:], 8192)
	if _, err := LoadTNT(data); err == nil {
		t.Fatal("TNT accepted an oversized attribute grid")
	}
}

func TestThreeDODecodesHierarchyAndPrimitiveIndexes(t *testing.T) {
	data := make([]byte, 320)
	put := func(off int, value int32) { binary.LittleEndian.PutUint32(data[off:], uint32(value)) }
	put(0, 1)
	put(4, 1)
	put(8, 1)
	put(12, 0)
	put(28, 300)
	put(36, 104)
	put(40, 116)
	put(48, 154)
	put(104, 1)
	put(108, 2)
	put(112, 3)
	put(116, 5)
	put(120, 3)
	put(128, 148)
	put(144, 1)
	binary.LittleEndian.PutUint16(data[148:], 0)
	binary.LittleEndian.PutUint16(data[150:], 0)
	binary.LittleEndian.PutUint16(data[152:], 0)
	put(154, 1)
	put(158, 0)
	put(162, 0)
	put(166, -1)
	put(170, 65536)
	put(174, 0)
	put(178, 0)
	put(182, 300)
	put(186, 0)
	put(190, 0)
	put(194, 0)
	put(198, 0)
	put(202, 0)
	copy(data[300:], "base\x00")
	three, err := LoadThreeDO(data)
	if err != nil {
		t.Fatal(err)
	}
	if three.Root != 0 || len(three.Objects) != 2 || three.Objects[0].FirstChild != 1 || three.Objects[1].Parent != 0 || three.Objects[0].Primitives[0].VertexIndices[0] != 0 {
		t.Fatalf("decoded 3DO = %+v", three)
	}
	if len(three.Raw) != len(data) {
		t.Fatal("3DO did not retain its owned source buffer")
	}
}

func TestThreeDORejectsCycles(t *testing.T) {
	data := make([]byte, 104)
	binary.LittleEndian.PutUint32(data[0:], 1)
	binary.LittleEndian.PutUint32(data[28:], 80)
	binary.LittleEndian.PutUint32(data[48:], 52)
	binary.LittleEndian.PutUint32(data[52:], 1)
	binary.LittleEndian.PutUint32(data[80:], 0)
	binary.LittleEndian.PutUint32(data[100:], 52)
	if _, err := LoadThreeDO(data); err == nil {
		t.Fatal("cycle was accepted")
	}
}

func TestRetailTDFSectionTerminatorAndGUIFallbacks(t *testing.T) {
	document, err := ParseTDF([]byte(`[ROOT] { [INNER] { value=1; }; };`))
	if err != nil {
		t.Fatal(err)
	}
	value, ok := document.Root.Section("root").Section("inner").FirstValue("value")
	if !ok || value != "1" {
		t.Fatal("semicolon-terminated nested section was not parsed")
	}

	gui, err := LoadGUI([]byte(`[GADGET0] { [COMMON] { id=1; name=score; } text=OK;`))
	if err != nil {
		t.Fatal(err)
	}
	if !gui.Repaired || len(gui.Gadgets) != 1 || gui.Gadgets[0].Fields["text"] != "OK" {
		t.Fatalf("repaired GUI = %+v", gui)
	}

	binaryGUI := append([]byte{0, 0xcd, 0, 0}, []byte("HEADER\x00Victory or Failure")...)
	gui, err = LoadGUI(binaryGUI)
	if err != nil {
		t.Fatal(err)
	}
	if !gui.Binary || len(gui.Gadgets) < 2 {
		t.Fatalf("binary GUI = %+v", gui)
	}
}

func TestTDFRejectsUnboundedDocuments(t *testing.T) {
	limits := DefaultTDFLimits()
	depth := limits
	depth.MaxDepth = 1
	if _, err := ParseTDFWithLimits([]byte(`[A]{[B]{value=1;};}`), depth); err == nil {
		t.Fatal("TDF accepted excessive section nesting")
	}
	items := limits
	items.MaxItems = 1
	if _, err := ParseTDFWithLimits([]byte(`a=1;b=2;`), items); err == nil {
		t.Fatal("TDF accepted excessive item count")
	}
	if _, err := ParseTDF(make([]byte, limits.MaxBytes+1)); err == nil {
		t.Fatal("TDF accepted an oversized document")
	}
}

func TestImageDecodersRejectUnboundedDimensions(t *testing.T) {
	gaf := make([]byte, 112)
	binary.LittleEndian.PutUint32(gaf[4:], 1)
	binary.LittleEndian.PutUint32(gaf[12:], 16)
	binary.LittleEndian.PutUint16(gaf[16:], 1)
	binary.LittleEndian.PutUint32(gaf[56:], 64)
	binary.LittleEndian.PutUint16(gaf[64:], 0xffff)
	binary.LittleEndian.PutUint16(gaf[66:], 0xffff)
	binary.LittleEndian.PutUint32(gaf[80:], uint32(len(gaf)))
	if _, err := LoadGAF(gaf); err == nil {
		t.Fatal("GAF accepted an unbounded frame")
	}

	pcx := make([]byte, 128+769)
	pcx[0], pcx[1], pcx[2], pcx[3], pcx[65] = 0x0a, 5, 1, 8, 1
	binary.LittleEndian.PutUint16(pcx[8:], 0xfffe)
	binary.LittleEndian.PutUint16(pcx[10:], 0xfffe)
	binary.LittleEndian.PutUint16(pcx[66:], 0xffff)
	pcx[len(pcx)-769] = 0x0c
	if _, err := LoadPCX(pcx); err == nil {
		t.Fatal("PCX accepted an unbounded image")
	}

	const height = 0x1000
	const width = 255
	const byteCount = (width*height + 7) / 8
	fnt := make([]byte, 516+1+byteCount)
	binary.LittleEndian.PutUint16(fnt[0:], height)
	for code := 0; code < 17; code++ {
		binary.LittleEndian.PutUint16(fnt[4+code*2:], 516)
	}
	fnt[516] = width
	if _, err := LoadFNT(fnt); err == nil {
		t.Fatal("FNT accepted unbounded glyph data")
	}
}
