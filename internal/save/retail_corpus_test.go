package save

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"github.com/nanolathe/nanolathe/internal/clock"
)

// TestRetailBankHeaderCorpus locks the 34-byte HAPIBANK header and its
// independently authored field values [08 "Save-file organization"].
func TestRetailBankHeaderCorpus(t *testing.T) {
	b := NewBuilder(RetailTag)
	b.Add(SummaryAccount).SetInt("maxunits", 250)
	data := b.Bytes()
	if len(data) < BankHeaderSize {
		t.Fatalf("bank length %d is shorter than header", len(data))
	}
	if got := data[:8]; !bytes.Equal(got, []byte("HAPIBANK")) {
		t.Fatalf("magic %q want HAPIBANK", got)
	}
	poolOffset := binary.LittleEndian.Uint32(data[0x0c:])
	firstAccount := binary.LittleEndian.Uint32(data[0x10:])
	if firstAccount != BankHeaderSize {
		t.Fatalf("first account offset %d want %d", firstAccount, BankHeaderSize)
	}
	if poolOffset <= firstAccount || int(poolOffset) > len(data) {
		t.Fatalf("pool offset %d outside account area/file length %d", poolOffset, len(data))
	}
	if got := binary.LittleEndian.Uint32(data[0x08:]); got != 0 {
		t.Fatalf("retail tag offset %d want 0", got)
	}
	if got := binary.LittleEndian.Uint32(data[0x14:]); got != 1 {
		t.Fatalf("bank version %d want 1", got)
	}
	if data[0x18] != 0 {
		t.Fatalf("pool compression flag %d want raw", data[0x18])
	}
	if !bytes.Equal(data[0x19:BankHeaderSize], make([]byte, BankHeaderSize-0x19)) {
		t.Fatalf("reserved header bytes are not zero: %x", data[0x19:BankHeaderSize])
	}
	bank, err := OpenBytes(data, RetailTag)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	if bank.Tag != RetailTag || bank.Count() != 1 {
		t.Fatalf("opened bank tag/accounts = %q/%d", bank.Tag, bank.Count())
	}
}

// TestRetailAccountGroupsCorpus verifies the 32-byte account header, strict
// integer/double/string/box group order, and 16-byte box descriptor [08
// "Save-file organization"].
func TestRetailAccountGroupsCorpus(t *testing.T) {
	b := NewBuilder(RetailTag)
	ac := b.Add("Corpus")
	ac.SetInt("I", -7)
	ac.SetDouble("D", math.Pi)
	ac.SetString("S", "value")
	ac.AppendBox("Blob", 0, []byte{0xaa, 0xbb, 0xcc})
	data := b.Bytes()
	first := int(binary.LittleEndian.Uint32(data[0x10:]))
	if got := len(data[first:]); got <= AccountHeaderSize {
		t.Fatalf("account payload length %d too short", got)
	}
	header := data[first : first+AccountHeaderSize]
	if got := binary.LittleEndian.Uint32(header[0:]); got != 32+8+12+8+16+3 {
		t.Fatalf("account span %d want %d", got, 32+8+12+8+16+3)
	}
	if got := binary.LittleEndian.Uint32(header[4:]); got != b.PoolOffset("Corpus") {
		t.Fatalf("account name offset %d want %d", got, b.PoolOffset("Corpus"))
	}
	if got := binary.LittleEndian.Uint32(header[8:]); got != 1 {
		t.Fatalf("integer count %d want 1", got)
	}
	if got := binary.LittleEndian.Uint32(header[0x0c:]); got != 1 {
		t.Fatalf("double count %d want 1", got)
	}
	if got := binary.LittleEndian.Uint32(header[0x10:]); got != 1 {
		t.Fatalf("string count %d want 1", got)
	}
	if got := binary.LittleEndian.Uint32(header[0x14:]); got != 1 {
		t.Fatalf("box count %d want 1", got)
	}
	if got := binary.LittleEndian.Uint32(header[0x18:]); got != 0 {
		t.Fatalf("compression flag %d want 0", got)
	}
	if !bytes.Equal(header[0x1c:0x20], make([]byte, 4)) {
		t.Fatalf("account reserved bytes are not zero: %x", header[0x1c:0x20])
	}
	body := data[first+AccountHeaderSize : first+int(binary.LittleEndian.Uint32(header[0:]))]
	if got := binary.LittleEndian.Uint32(body[0:]); got != b.PoolOffset("I") {
		t.Fatalf("integer name offset %d want %d", got, b.PoolOffset("I"))
	}
	if got := int32(binary.LittleEndian.Uint32(body[4:])); got != -7 {
		t.Fatalf("integer value %d want -7", got)
	}
	if got := binary.LittleEndian.Uint32(body[8:]); got != b.PoolOffset("D") {
		t.Fatalf("double name offset %d want %d", got, b.PoolOffset("D"))
	}
	if got := math.Float64frombits(binary.LittleEndian.Uint64(body[12:])); got != math.Pi {
		t.Fatalf("double value %.17g want %.17g", got, math.Pi)
	}
	if got := binary.LittleEndian.Uint32(body[20:]); got != b.PoolOffset("S") {
		t.Fatalf("string name offset %d want %d", got, b.PoolOffset("S"))
	}
	if got := binary.LittleEndian.Uint32(body[24:]); got != b.PoolOffset("value") {
		t.Fatalf("string value offset %d want %d", got, b.PoolOffset("value"))
	}
	box := body[28:44]
	if got := binary.LittleEndian.Uint32(box[0:]); got != b.PoolOffset("Blob") {
		t.Fatalf("box name offset %d want %d", got, b.PoolOffset("Blob"))
	}
	if got := binary.LittleEndian.Uint32(box[4:]); got != 0 {
		t.Fatalf("named box number %d want 0", got)
	}
	payloadOffset := binary.LittleEndian.Uint32(box[8:])
	if got := data[payloadOffset : payloadOffset+3]; !bytes.Equal(got, []byte{0xaa, 0xbb, 0xcc}) {
		t.Fatalf("box payload %x", got)
	}
	if got := binary.LittleEndian.Uint32(box[12:]); got != 3 {
		t.Fatalf("box length %d want 3", got)
	}

	bank, err := OpenBytes(data, RetailTag)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	parsed, ok := bank.Account("Corpus")
	if !ok || len(parsed.Ints) != 1 || len(parsed.Doubles) != 1 || len(parsed.Strings) != 1 || len(parsed.Boxes) != 1 {
		t.Fatalf("parsed groups = %+v", parsed)
	}
}

// TestRetailSummaryDefaultsAndOrder locks the established Summary defaults,
// typed item grouping, and multiplayer-only defaults [08 "Summary"].
func TestRetailSummaryDefaultsAndOrder(t *testing.T) {
	b := NewBuilder(RetailTag)
	ac := b.Add(SummaryAccount)
	ac.SetInt("BUILD DATE:fixture", 0)
	ac.SetInt("BUILD TIME:fixture", 0)
	ac.SetInt("maxunits", int32(0x10005))
	ac.SetString("Campaign", "camp")
	ac.SetString("Mission", "mission")
	ac.SetString("Map", "map")
	ac.SetString("Difficulty", "wrong wire type")
	ac.SetInt("Gametype", 2)
	data := b.Bytes()
	bank, err := OpenBytes(data, RetailTag)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	s, ok := ReadSummary(bank)
	if !ok {
		t.Fatal("ReadSummary missing")
	}
	if s.BuildDateKey != "BUILD DATE:fixture" || s.BuildTimeKey != "BUILD TIME:fixture" {
		t.Fatalf("build keys = %q/%q", s.BuildDateKey, s.BuildTimeKey)
	}
	if s.MaxUnits != 5 {
		t.Fatalf("maxunits %x want 5", s.MaxUnits)
	}
	if s.Campaign != "camp" || s.Mission != "mission" || s.MapName != "map" {
		t.Fatalf("metadata = %+v", s)
	}
	if s.Difficulty != 0 || s.Side != 0 || s.Players != 0 || s.Thumbs != 0 {
		t.Fatalf("missing scalar defaults = %+v", s)
	}
	if !s.IsMultiplayer || s.CommanderDeath != 1 || s.Location != 1 || s.Mapping != 1 || s.LineOfSight != 1 || s.LineOfSightType != 1 {
		t.Fatalf("multiplayer defaults = %+v", s)
	}
	if s.BetweenMissions != 0 || !s.IsBattle {
		t.Fatalf("battle defaults = %+v", s)
	}
	if got := []string{ac.Ints[0].Name, ac.Ints[1].Name, ac.Ints[2].Name, ac.Ints[3].Name}; got[0] != "BUILD DATE:fixture" || got[1] != "BUILD TIME:fixture" || got[2] != "maxunits" || got[3] != "Gametype" {
		t.Fatalf("integer item order = %v", got)
	}
	if got := []string{ac.Strings[0].Name, ac.Strings[1].Name, ac.Strings[2].Name, ac.Strings[3].Name}; got[0] != "Campaign" || got[1] != "Mission" || got[2] != "Map" || got[3] != "Difficulty" {
		t.Fatalf("string item order = %v", got)
	}
}

func TestRetailCameraMissingFieldDefaultsZero(t *testing.T) {
	b := NewBuilder(RetailTag)
	b.Add(CameraAccount).SetDouble("X Position", 12.5)
	bank, err := OpenBytes(b.Bytes(), RetailTag)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	c, ok := ReadCamera(bank)
	if !ok || c.XPosition != 12.5 || c.ZPosition != 0 {
		t.Fatalf("camera = %+v, ok=%v", c, ok)
	}
}

func TestRetailGameTimeAndPlayerGate(t *testing.T) {
	clk := &clock.State{Requested: 10, Active: 10, GlobalTick: 1234}
	b := NewBuilder(RetailTag)
	WriteGameTime(b, clk)
	p := b.Add("Player0")
	p.SetInt("Controller", 2)
	bank, err := OpenBytes(b.Bytes(), RetailTag)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	if _, ok := ReadGameTime(bank); !ok || len(bankAccountBox(t, bank, PlayersAccount, GameTimeBoxName)) != 28 {
		t.Fatal("valid 28-byte GameTime was not accepted")
	}
	if got := ReadAllPlayerSlots(bank); len(got) != 1 || got[0].Controller != 2 {
		t.Fatalf("player gate result = %+v", got)
	}

	short := NewBuilder(RetailTag)
	short.Add(PlayersAccount).AppendBox(GameTimeBoxName, 0, []byte{1, 2, 3})
	short.Add("Player0").SetInt("Controller", 2)
	shortBank, err := OpenBytes(short.Bytes(), RetailTag)
	if err != nil {
		t.Fatalf("OpenBytes short: %v", err)
	}
	if _, ok := ReadGameTime(shortBank); ok || len(ReadAllPlayerSlots(shortBank)) != 0 {
		t.Fatal("short GameTime must gate all Player%i records")
	}
}

func TestRetailAlliancesExactSizeAndSelfByte(t *testing.T) {
	b := NewBuilder(RetailTag)
	data := bytes.Repeat([]byte{0}, 11)
	data[4] = 1
	b.Add(PlayersAccount).AppendBox(AlliancesBoxName, 0, data)
	bank, err := OpenBytes(b.Bytes(), RetailTag)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	got, ok := ReadAlliances(bank, 2)
	if !ok || len(got) != 11 || got[2] != 1 || got[4] != 1 {
		t.Fatalf("alliances = %v, ok=%v", got, ok)
	}
	wrong := NewBuilder(RetailTag)
	wrong.Add(PlayersAccount).AppendBox(AlliancesBoxName, 0, bytes.Repeat([]byte{1}, 12))
	wrongBank, err := OpenBytes(wrong.Bytes(), RetailTag)
	if err != nil {
		t.Fatalf("OpenBytes wrong length: %v", err)
	}
	if _, ok := ReadAlliances(wrongBank, 0); ok {
		t.Fatal("12-byte Alliances box must be rejected")
	}
}

// TestRetailDuplicateAccountMerge uses an independently authored bank image
// to lock duplicate-account merge, last-scalar-wins, and repeated-box append
// behavior [08 "Save-file organization"].
func TestRetailDuplicateAccountMerge(t *testing.T) {
	data := duplicateAccountFixture()
	bank, err := OpenBytes(data, RetailTag)
	if err != nil {
		t.Fatalf("OpenBytes duplicate account fixture: %v", err)
	}
	if bank.Count() != 1 {
		t.Fatalf("merged account count %d want 1", bank.Count())
	}
	ac, ok := bank.Account(PlayersAccount)
	if !ok {
		t.Fatal("merged Players account missing")
	}
	if got, ok := ac.Int("Human Player"); !ok || got != 3 {
		t.Fatalf("last duplicate scalar = %d, ok=%v; want 3,true", got, ok)
	}
	if got, ok := ac.BoxData(AlliancesBoxName, 0); !ok || !bytes.Equal(got, []byte{1, 2, 3, 4}) {
		t.Fatalf("appended duplicate box = %v, ok=%v", got, ok)
	}
}

func bankAccountBox(t *testing.T, bank *Bank, account, name string) []byte {
	t.Helper()
	ac, ok := bank.Account(account)
	if !ok {
		t.Fatalf("account %q missing", account)
	}
	data, ok := ac.BoxData(name, 0)
	if !ok {
		t.Fatalf("box %q missing", name)
	}
	return data
}

func duplicateAccountFixture() []byte {
	const (
		first = BankHeaderSize
		span  = 32 + 8 + 16 + 2
	)
	pool := []byte("Total Annihilation 3.0\x00Players\x00Human Player\x00Alliances\x00")
	playersOff := uint32(len("Total Annihilation 3.0\x00"))
	humanOff := playersOff + uint32(len("Players\x00"))
	alliancesOff := humanOff + uint32(len("Human Player\x00"))
	poolOffset := uint32(first + span*2)
	image := make([]byte, int(poolOffset)+len(pool))
	copy(image[poolOffset:], pool)
	copy(image[:8], []byte("HAPIBANK"))
	binary.LittleEndian.PutUint32(image[0x08:], 0)
	binary.LittleEndian.PutUint32(image[0x0c:], poolOffset)
	binary.LittleEndian.PutUint32(image[0x10:], first)
	binary.LittleEndian.PutUint32(image[0x14:], 1)
	writeDuplicateAccount := func(at int, playerValue int32, left, right byte) {
		binary.LittleEndian.PutUint32(image[at:], span)
		binary.LittleEndian.PutUint32(image[at+4:], playersOff)
		binary.LittleEndian.PutUint32(image[at+8:], 1)
		binary.LittleEndian.PutUint32(image[at+0x14:], 1)
		body := at + AccountHeaderSize
		binary.LittleEndian.PutUint32(image[body:], humanOff)
		binary.LittleEndian.PutUint32(image[body+4:], uint32(playerValue))
		descriptor := body + 8
		binary.LittleEndian.PutUint32(image[descriptor:], alliancesOff)
		payload := descriptor + 16
		binary.LittleEndian.PutUint32(image[descriptor+8:], uint32(payload))
		binary.LittleEndian.PutUint32(image[descriptor+12:], 2)
		image[payload] = left
		image[payload+1] = right
	}
	writeDuplicateAccount(first, 2, 1, 2)
	writeDuplicateAccount(first+span, 3, 3, 4)
	return image
}
