package save

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func minimalBattleBuilder() *Builder {
	b := NewBuilder("")
	WriteSummary(b, Summary{Campaign: "c", Mission: "m", MapName: "map", Players: 1, Gametype: 1, IsBattle: true})
	WriteCamera(b, Camera{XPosition: 1, ZPosition: 2})
	players := builderAccount(b, PlayersAccount)
	players.AppendBox(GameTimeBoxName, 0, make([]byte, 28))
	WriteUnitsHeader(b, 0)
	_ = WriteFeatureTypeNames(b, []string{"tree"})
	features := builderAccount(b, FeaturesAccount)
	features.SetInt("Number of Normal Features", 0)
	features.SetInt("Number of Animating Features", 0)
	features.SetInt("Number of 3D Features", 0)
	b.Add("Metal").AppendBox("Plotmap", 0, []byte{1})
	b.Add("PlayerFeatures").AppendBox("Plotmap", 0, []byte{2})
	b.Add("Mapping").AppendBox("Data", 0, []byte{3})
	WriteMeteorScalars(b, MeteorScalars{})
	return b
}

func openImageBank(t *testing.T, b *Builder) *Bank {
	t.Helper()
	bank, err := OpenBytes(b.Bytes(), RetailTag)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	return bank
}

func TestDecodeBattleImageMinimalAndDetached(t *testing.T) {
	b := minimalBattleBuilder()
	bank := openImageBank(t, b)
	image, err := DecodeBattleImage(bank)
	if err != nil {
		t.Fatalf("DecodeBattleImage: %v", err)
	}
	if image.Summary.Gametype != 1 || len(image.Features.TypeNames) != 1 {
		t.Fatalf("unexpected image summary/features: %+v %+v", image.Summary, image.Features)
	}
	if !bytes.Equal(image.Scheduler[:], make([]byte, 28)) {
		t.Fatal("scheduler prefix not retained")
	}
	if len(image.Metal) != 1 {
		t.Fatalf("metal len=%d want 1", len(image.Metal))
	}
	image.Metal[0] = 9
	if bankData, _ := bank.Account("Metal"); bankData == nil || bankData.Boxes[0].Data[0] != 1 {
		t.Fatal("image aliases bank data")
	}
}

func TestDecodeBattleImageSchedulerPrefixAndShort(t *testing.T) {
	b := minimalBattleBuilder()
	players := builderAccount(b, PlayersAccount)
	players.Boxes[0].Data = append(make([]byte, 28), 4, 5)
	image, err := DecodeBattleImage(openImageBank(t, b))
	if err != nil || image == nil {
		t.Fatalf("extended scheduler rejected: %v", err)
	}
	b2 := minimalBattleBuilder()
	players2 := builderAccount(b2, PlayersAccount)
	players2.Boxes[0].Data = make([]byte, 27)
	if _, err := DecodeBattleImage(openImageBank(t, b2)); err == nil {
		t.Fatal("short scheduler accepted")
	}
}

func TestDecodeBattleImageRejectsDuplicateUnitIDs(t *testing.T) {
	b := minimalBattleBuilder()
	WriteUnitsHeader(b, 2)
	units := builderAccount(b, UnitsAccount)
	first := make([]byte, UnitBoxSize)
	second := make([]byte, UnitBoxSize)
	binary.LittleEndian.PutUint16(first[0x21:], 7)
	binary.LittleEndian.PutUint16(second[0x21:], 7)
	units.AppendBox("", 0, first)
	units.AppendBox("", 1, second)
	if _, err := DecodeBattleImage(openImageBank(t, b)); err == nil {
		t.Fatal("duplicate unit IDs accepted")
	}
}

func TestDecodeBattleImageRejectsZeroIDAndUnitReferenceCycle(t *testing.T) {
	b := minimalBattleBuilder()
	WriteUnitsHeader(b, 1)
	units := builderAccount(b, UnitsAccount)
	units.AppendBox("", 0, make([]byte, UnitBoxSize))
	if _, err := DecodeBattleImage(openImageBank(t, b)); err == nil {
		t.Fatal("zero stable ID accepted")
	}

	b2 := minimalBattleBuilder()
	WriteUnitsHeader(b2, 2)
	units2 := builderAccount(b2, UnitsAccount)
	first := make([]byte, UnitBoxSize)
	second := make([]byte, UnitBoxSize)
	binary.LittleEndian.PutUint16(first[0x21:], 1)
	binary.LittleEndian.PutUint16(second[0x21:], 2)
	binary.LittleEndian.PutUint16(first[0x89:], 2)
	binary.LittleEndian.PutUint16(second[0x8b:], 1)
	units2.AppendBox("", 0, first)
	units2.AppendBox("", 1, second)
	if _, err := DecodeBattleImage(openImageBank(t, b2)); err == nil {
		t.Fatal("cyclic unit references accepted")
	}
}

func TestDecodeBattleImageRejectsOrderGapAndPreservesOpaqueBytes(t *testing.T) {
	b := minimalBattleBuilder()
	WriteUnitsHeader(b, 1)
	units := builderAccount(b, UnitsAccount)
	unit := make([]byte, UnitBoxSize)
	binary.LittleEndian.PutUint16(unit[0x21:], 9)
	binary.LittleEndian.PutUint32(unit[0x23:], 1)
	units.AppendBox("", 0, unit)
	order := make([]byte, OrderBoxSize)
	binary.LittleEndian.PutUint16(order[0:], 9)
	order[0x2e] = 0xab
	if err := WriteOrderBox(b, 9, 1, order); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeBattleImage(openImageBank(t, b)); err == nil {
		t.Fatal("order sequence gap accepted")
	}

	b2 := minimalBattleBuilder()
	WriteUnitsHeader(b2, 1)
	units2 := builderAccount(b2, UnitsAccount)
	unit2 := make([]byte, UnitBoxSize)
	binary.LittleEndian.PutUint16(unit2[0x21:], 9)
	binary.LittleEndian.PutUint32(unit2[0x23:], 0)
	unit2[0xA0] = 0xcd
	units2.AppendBox("", 0, unit2)
	image, err := DecodeBattleImage(openImageBank(t, b2))
	if err != nil {
		t.Fatalf("opaque image rejected: %v", err)
	}
	if image.Units.Records[0].Data[0xA0] != 0xcd {
		t.Fatal("opaque unit byte was not retained")
	}
}

func TestDecodeBattleImagePreservesFeatureOrderSubtypeAndScript(t *testing.T) {
	b := minimalBattleBuilder()
	units := builderAccount(b, UnitsAccount)
	WriteUnitsHeader(b, 1)
	unit := make([]byte, UnitBoxSize)
	binary.LittleEndian.PutUint16(unit[0x21:], 9)
	binary.LittleEndian.PutUint32(unit[0x23:], 1)
	units.AppendBox("", 0, unit)
	main := make([]byte, OrderBoxSize)
	binary.LittleEndian.PutUint16(main[0:], 9)
	binary.LittleEndian.PutUint32(main[4:], 2)
	binary.LittleEndian.PutUint32(main[0x26:], 7)
	if err := WriteOrderBox(b, 9, 0, main); err != nil {
		t.Fatal(err)
	}
	subtype := make([]byte, OrderSubtypeCode2)
	binary.LittleEndian.PutUint16(subtype[8:], 9)
	binary.LittleEndian.PutUint16(subtype[0x1a:], 0)
	units.AppendBox("u0009m0000g", 0, subtype)
	units.AppendBox("u0009m0000_name", 0, []byte("move"))
	units.AppendBox("Script0", 0, bytes.Repeat([]byte{0xa5}, ScriptSnapshotSize))
	units.SetString("UTYPENAME   7", "builder")

	features := builderAccount(b, FeaturesAccount)
	features.Boxes[0].Data[4] = 0
	features.Boxes[0].Data[5] = 'x'
	normal := make([]byte, 8)
	binary.LittleEndian.PutUint16(normal[0:], 4)
	binary.LittleEndian.PutUint16(normal[2:], 5)
	binary.LittleEndian.PutUint16(normal[4:], 0)
	normal[6] = 0x7b
	features.SetInt("Number of Normal Features", 1)
	features.AppendBox(featureNormalBox, 0, normal)
	image, err := DecodeBattleImage(openImageBank(t, b))
	if err != nil {
		t.Fatalf("DecodeBattleImage: %v", err)
	}
	if image.Features.Normal[0].X != 4 || image.Features.Normal[0].Z != 5 || image.Features.Normal[0].Data[6] != 0x7b {
		t.Fatal("feature record was not preserved")
	}
	if len(image.Units.Orders) != 1 || image.Units.Orders[0].SubtypeCode != 2 || image.Units.Orders[0].Subtype[8] != 9 {
		t.Fatal("order subtype was not preserved")
	}
	if image.Units.Orders[0].BuildTypeName != "builder" {
		t.Fatalf("build name = %q, want builder", image.Units.Orders[0].BuildTypeName)
	}
	if len(image.Units.Scripts) != 1 || image.Units.Scripts[0].Data[0] != 0xa5 {
		t.Fatal("script bytes were not preserved")
	}
	if image.Features.TypeNames[0] != "tree" {
		t.Fatalf("feature C string = %q, want tree", image.Features.TypeNames[0])
	}
}

func TestDecodeBattleImageRejectsUnsupportedSubtypeAndDuplicateScripts(t *testing.T) {
	b := minimalBattleBuilder()
	WriteUnitsHeader(b, 1)
	units := builderAccount(b, UnitsAccount)
	unit := make([]byte, UnitBoxSize)
	binary.LittleEndian.PutUint16(unit[0x21:], 9)
	binary.LittleEndian.PutUint32(unit[0x23:], 1)
	units.AppendBox("", 0, unit)
	main := make([]byte, OrderBoxSize)
	binary.LittleEndian.PutUint16(main[0:], 9)
	binary.LittleEndian.PutUint32(main[4:], 7)
	if err := WriteOrderBox(b, 9, 0, main); err != nil {
		t.Fatal(err)
	}
	units.AppendBox("u0009m0000g", 0, []byte{1})
	if _, err := DecodeBattleImage(openImageBank(t, b)); err == nil {
		t.Fatal("unsupported subtype code accepted")
	}

	b2 := minimalBattleBuilder()
	units2 := builderAccount(b2, UnitsAccount)
	units2.AppendBox("Script0", 0, bytes.Repeat([]byte{1}, ScriptSnapshotSize))
	units2.AppendBox("Script0", 1, bytes.Repeat([]byte{2}, ScriptSnapshotSize))
	if _, err := DecodeBattleImage(openImageBank(t, b2)); err == nil {
		t.Fatal("duplicate script index accepted")
	}
}

func TestDecodeBattleImageRejectsFeatureOrderAndDuplicateAnchors(t *testing.T) {
	b := minimalBattleBuilder()
	features := builderAccount(b, FeaturesAccount)
	features.SetInt("Number of Normal Features", 2)
	first := make([]byte, 8)
	second := make([]byte, 8)
	binary.LittleEndian.PutUint16(first[2:], 2)
	binary.LittleEndian.PutUint16(first[0:], 3)
	binary.LittleEndian.PutUint16(second[2:], 1)
	binary.LittleEndian.PutUint16(second[0:], 4)
	features.AppendBox(featureNormalBox, 0, append(first, second...))
	if _, err := DecodeBattleImage(openImageBank(t, b)); err == nil {
		t.Fatal("out-of-order feature anchors accepted")
	}

	b2 := minimalBattleBuilder()
	features2 := builderAccount(b2, FeaturesAccount)
	features2.SetInt("Number of Normal Features", 1)
	features2.SetInt("Number of Animating Features", 1)
	normal := make([]byte, 8)
	animating := make([]byte, 10)
	binary.LittleEndian.PutUint16(normal[0:], 3)
	binary.LittleEndian.PutUint16(normal[2:], 4)
	binary.LittleEndian.PutUint16(animating[0:], 3)
	binary.LittleEndian.PutUint16(animating[2:], 4)
	features2.AppendBox(featureNormalBox, 0, normal)
	features2.AppendBox(featureAnimatingBox, 0, animating)
	if _, err := DecodeBattleImage(openImageBank(t, b2)); err == nil {
		t.Fatal("duplicate feature anchor accepted")
	}
}

func TestDecodeBattleImageAllowsAbsentFeatureTypeNames(t *testing.T) {
	b := minimalBattleBuilder()
	features := builderAccount(b, FeaturesAccount)
	features.Boxes = nil
	features.SetInt("Number of Normal Features", 1)
	record := make([]byte, 8)
	binary.LittleEndian.PutUint16(record[0:], 3)
	binary.LittleEndian.PutUint16(record[2:], 4)
	binary.LittleEndian.PutUint16(record[4:], 99)
	features.AppendBox(featureNormalBox, 0, record)
	image, err := DecodeBattleImage(openImageBank(t, b))
	if err != nil {
		t.Fatalf("absent feature names rejected: %v", err)
	}
	if image.Features.HasTypeNames || len(image.Features.TypeNames) != 0 {
		t.Fatal("absent feature names reported as present")
	}
	if image.Features.Normal[0].TypeID != 99 {
		t.Fatalf("feature type ID = %d, want 99", image.Features.Normal[0].TypeID)
	}
}
