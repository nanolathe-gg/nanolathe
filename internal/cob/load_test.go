package cob

import (
	"bytes"
	"encoding/binary"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/vfs"
)

// makeCOB builds a synthetic COB file per [fmt cob] with header 44 bytes,
// code at header end, then tables, then strings contiguous.
// This is a test helper; bytes are authored, never copied retail bytes.
// Statics defaults to 0.
func makeCOB(code []uint32, scriptNames []string, scriptIndexes []uint32, pieceNames []string) []byte {
	return makeCOBWithStatics(0, code, scriptNames, scriptIndexes, pieceNames)
}

// makeCOBWithStatics builds a synthetic COB with explicit NumberOfStatics [fmt cob] [04 §4.2].
func makeCOBWithStatics(statics uint32, code []uint32, scriptNames []string, scriptIndexes []uint32, pieceNames []string) []byte {
	nScripts := uint32(len(scriptNames))
	nPieces := uint32(len(pieceNames))
	codeLen := uint32(len(code))
	// Offsets per [fmt cob] "All offsets are absolute file offsets."
	offCode := uint32(44)
	offScriptIdx := offCode + codeLen*4
	offScriptNameOff := offScriptIdx + nScripts*4
	offPieceNameOff := offScriptNameOff + nScripts*4
	strStart := offPieceNameOff + nPieces*4

	// Compute string offsets.
	scriptOffs := make([]uint32, nScripts)
	pieceOffs := make([]uint32, nPieces)
	totalStr := 0
	for _, s := range scriptNames {
		totalStr += len(s) + 1
	}
	for _, s := range pieceNames {
		totalStr += len(s) + 1
	}
	totalSize := int(strStart) + totalStr
	buf := make([]byte, totalSize)
	// Header [fmt cob] 11 × u32
	binary.LittleEndian.PutUint32(buf[0x00:], 4)                // VersionSignature
	binary.LittleEndian.PutUint32(buf[0x04:], nScripts)         // NumberOfScripts
	binary.LittleEndian.PutUint32(buf[0x08:], nPieces)          // NumberOfPieces
	binary.LittleEndian.PutUint32(buf[0x0C:], codeLen)          // CodeLength
	binary.LittleEndian.PutUint32(buf[0x10:], statics)          // NumberOfStatics [fmt cob] "Count of static variables"
	binary.LittleEndian.PutUint32(buf[0x14:], 0)                // Always_0
	binary.LittleEndian.PutUint32(buf[0x18:], offScriptIdx)     // OffsetToScriptCodeIndexArray
	binary.LittleEndian.PutUint32(buf[0x1C:], offScriptNameOff) // OffsetToScriptNameOffsetArray
	binary.LittleEndian.PutUint32(buf[0x20:], offPieceNameOff)  // OffsetToPieceNameOffsetArray
	binary.LittleEndian.PutUint32(buf[0x24:], offCode)          // OffsetToScriptCode
	if nScripts > 0 {
		// OffsetToFirstScriptName equals first script name offset in all observed files [fmt cob]
		binary.LittleEndian.PutUint32(buf[0x28:], strStart)
	} else {
		binary.LittleEndian.PutUint32(buf[0x28:], 0)
	}
	// Code words [fmt cob] little-endian u32
	for i, w := range code {
		binary.LittleEndian.PutUint32(buf[int(offCode)+i*4:], w)
	}
	// Prepare string pool.
	cur := strStart
	for i, name := range scriptNames {
		scriptOffs[i] = cur
		copy(buf[cur:], name)
		buf[cur+uint32(len(name))] = 0
		cur += uint32(len(name)) + 1
	}
	for i, name := range pieceNames {
		pieceOffs[i] = cur
		copy(buf[cur:], name)
		buf[cur+uint32(len(name))] = 0
		cur += uint32(len(name)) + 1
	}
	// Tables
	for i, idx := range scriptIndexes {
		binary.LittleEndian.PutUint32(buf[int(offScriptIdx)+i*4:], idx)
	}
	for i, off := range scriptOffs {
		binary.LittleEndian.PutUint32(buf[int(offScriptNameOff)+i*4:], off)
	}
	for i, off := range pieceOffs {
		binary.LittleEndian.PutUint32(buf[int(offPieceNameOff)+i*4:], off)
	}
	// Fix first-script-name offset to match actual first script name offset if present.
	if nScripts > 0 {
		binary.LittleEndian.PutUint32(buf[0x28:], scriptOffs[0])
	}
	return buf
}

func TestLoadValidMinimal(t *testing.T) {
	code := []uint32{0x10065000} // return [fmt cob]
	scripts := []string{"Create"}
	indexes := []uint32{0}
	pieces := []string{"base"}
	data := makeCOB(code, scripts, indexes, pieces)
	prog, err := Load(data)
	if err != nil {
		t.Fatalf("Load valid minimal: %v", err)
	}
	if len(prog.Code) != 1 || prog.Code[0] != 0x10065000 {
		t.Fatalf("Code = %v want [0x10065000]", prog.Code)
	}
	if len(prog.Pieces) != 1 || prog.Pieces[0] != "base" {
		t.Fatalf("Pieces = %v want [base]", prog.Pieces)
	}
	if got, want := prog.Scripts["Create"], 0; got != want {
		t.Fatalf("Scripts[Create]=%d want %d", got, want)
	}
	// NumberOfStatics carried as Program.Statics [fmt cob] [04 §4.2]; zero-initialized by engine.
	if prog.Statics != 0 {
		t.Fatalf("Statics=%d want 0", prog.Statics)
	}
	if prog.SourceChecksum != ContentChecksum(data) {
		t.Fatalf("SourceChecksum=%#x want %#x", prog.SourceChecksum, ContentChecksum(data))
	}
	// Deterministic map iteration check via sorted keys (I1): ensure Scripts map has expected size.
	if len(prog.Scripts) != 1 {
		t.Fatalf("Scripts len=%d want 1", len(prog.Scripts))
	}
}

func TestLoadStatics(t *testing.T) {
	code := []uint32{0x10065000}
	scripts := []string{"Create"}
	indexes := []uint32{0}
	pieces := []string{"base"}
	for _, want := range []int{0, 1, 3, 7} {
		data := makeCOBWithStatics(uint32(want), code, scripts, indexes, pieces)
		prog, err := Load(data)
		if err != nil {
			t.Fatalf("Load statics %d: %v", want, err)
		}
		if prog.Statics != want {
			t.Fatalf("Statics=%d want %d", prog.Statics, want)
		}
	}
}

func TestLoadRejectsExcessiveStaticStorageBeforeVMAllocation(t *testing.T) {
	data := makeCOBWithStatics(uint32(MaxProgramStaticBytes/4+1), []uint32{0x10065000}, []string{"Create"}, []uint32{0}, []string{"base"})
	if _, err := Load(data); err == nil || !strings.Contains(err.Error(), "static storage") {
		t.Fatalf("excessive static count error = %v", err)
	}
	vm := NewVM(&Program{Statics: MaxProgramStaticBytes/4 + 1})
	if vm.Program() != nil {
		t.Fatal("VM accepted excessive external static count")
	}
	valid := NewVM(&Program{})
	valid.SetProgram(&Program{Statics: MaxProgramStaticBytes/4 + 1})
	if valid.Program() == nil || len(valid.Diagnostics()) == 0 {
		t.Fatal("unchecked invalid external program left no diagnostic")
	}
}

func TestLoadValidMultipleScripts(t *testing.T) {
	// Example shaped like CORTRUCK.COB [fmt cob] "version=4, 3 scripts, 1 piece, 165 code words"
	// but scaled down: 3 scripts at indexes 0,1,2 with piece base.
	code := []uint32{0x10021001, 0x00000000, 0x10065000, 0x10022000, 0x10065000, 0x10065000}
	scripts := []string{"SmokeUnit", "Create", "Killed"}
	indexes := []uint32{0, 2, 4}
	pieces := []string{"base"}
	data := makeCOB(code, scripts, indexes, pieces)
	prog, err := Load(data)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(prog.Code) != len(code) {
		t.Fatalf("Code len %d want %d", len(prog.Code), len(code))
	}
	// Every script name maps to valid code offset within bounds [fmt cob].
	for _, name := range scripts {
		idx, ok := prog.Scripts[name]
		if !ok {
			t.Fatalf("missing script %q", name)
		}
		if idx < 0 || idx >= len(prog.Code) {
			t.Fatalf("script %q index %d out of bounds len %d", name, idx, len(prog.Code))
		}
	}
	if len(prog.Pieces) != 1 || prog.Pieces[0] != "base" {
		t.Fatalf("Pieces %v", prog.Pieces)
	}
	// Piece count matches name table.
	if len(prog.Pieces) != len(pieces) {
		t.Fatalf("piece count %d want %d", len(prog.Pieces), len(pieces))
	}
	// Sorted keys deterministic (I1).
	keys := make([]string, 0, len(prog.Scripts))
	for k := range prog.Scripts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	wantKeys := []string{"Create", "Killed", "SmokeUnit"}
	sort.Strings(wantKeys)
	if len(keys) != len(wantKeys) {
		t.Fatalf("keys %v want %v", keys, wantKeys)
	}
	for i := range keys {
		if keys[i] != wantKeys[i] {
			t.Fatalf("keys %v want %v", keys, wantKeys)
		}
	}
}

func TestLoadEmpty(t *testing.T) {
	data := makeCOB(nil, nil, nil, nil)
	prog, err := Load(data)
	if err != nil {
		t.Fatalf("empty Load: %v", err)
	}
	if len(prog.Code) != 0 {
		t.Fatalf("Code len %d want 0", len(prog.Code))
	}
	if len(prog.Scripts) != 0 {
		t.Fatalf("Scripts len %d want 0", len(prog.Scripts))
	}
	if len(prog.Pieces) != 0 {
		t.Fatalf("Pieces len %d want 0", len(prog.Pieces))
	}
	if prog.Statics != 0 {
		t.Fatalf("Statics=%d want 0", prog.Statics)
	}
}

func TestLoadCodeOffsetRelocation(t *testing.T) {
	// Relocation: offset is absolute file offset per [02 "Compiled script archive (COB)"]
	// Build a file where code is not immediately after header but padded.
	code := []uint32{0x10065000, 0x10065000}
	scripts := []string{"Create"}
	indexes := []uint32{1}
	pieces := []string{"base"}
	data := makeCOB(code, scripts, indexes, pieces)
	// Insert 16 bytes padding after header by shifting code offset.
	// Instead of patching, craft manually.
	pad := 16
	origCodeOff := binary.LittleEndian.Uint32(data[0x24:])
	newCodeOff := origCodeOff + uint32(pad)
	// Expand buffer.
	newData := make([]byte, len(data)+pad)
	copy(newData[:44], data[:44])
	binary.LittleEndian.PutUint32(newData[0x24:], newCodeOff)
	// Adjust table offsets.
	origScriptIdx := binary.LittleEndian.Uint32(data[0x18:])
	origScriptName := binary.LittleEndian.Uint32(data[0x1C:])
	origPieceName := binary.LittleEndian.Uint32(data[0x20:])
	binary.LittleEndian.PutUint32(newData[0x18:], origScriptIdx+uint32(pad))
	binary.LittleEndian.PutUint32(newData[0x1C:], origScriptName+uint32(pad))
	binary.LittleEndian.PutUint32(newData[0x20:], origPieceName+uint32(pad))
	// Fix first script name offset.
	origFirst := binary.LittleEndian.Uint32(data[0x28:])
	if origFirst != 0 {
		binary.LittleEndian.PutUint32(newData[0x28:], origFirst+uint32(pad))
	}
	// Copy code and tables and strings with shift.
	copy(newData[newCodeOff:], data[origCodeOff:])
	// Also fix string offsets inside tables (they are absolute file offsets).
	for i := 0; i < len(scripts); i++ {
		offPos := int(origScriptName) + i*4
		old := binary.LittleEndian.Uint32(data[offPos:])
		binary.LittleEndian.PutUint32(newData[int(origScriptName+uint32(pad))+i*4:], old+uint32(pad))
	}
	for i := 0; i < len(pieces); i++ {
		offPos := int(origPieceName) + i*4
		old := binary.LittleEndian.Uint32(data[offPos:])
		binary.LittleEndian.PutUint32(newData[int(origPieceName+uint32(pad))+i*4:], old+uint32(pad))
	}
	// Insert zero padding.
	for i := 44; i < int(newCodeOff); i++ {
		newData[i] = 0
	}
	prog, err := Load(newData)
	if err != nil {
		t.Fatalf("relocated Load: %v", err)
	}
	if len(prog.Code) != 2 || prog.Code[1] != 0x10065000 {
		t.Fatalf("relocated Code %v", prog.Code)
	}
	if prog.Scripts["Create"] != 1 {
		t.Fatalf("relocated script index %d", prog.Scripts["Create"])
	}
}

func TestLoadHeaderTooShort(t *testing.T) {
	_, err := Load([]byte{0x04, 0x00})
	if err == nil || !strings.Contains(err.Error(), "header too short") {
		t.Fatalf("want header too short, got %v", err)
	}
}

func TestLoadBadVersion(t *testing.T) {
	data := makeCOB([]uint32{0x10065000}, []string{"Create"}, []uint32{0}, []string{"base"})
	binary.LittleEndian.PutUint32(data[0x00:], 6) // TAK version [fmt cob]
	if _, err := Load(data); err == nil || !strings.Contains(err.Error(), "unsupported version") {
		t.Fatalf("want version error, got %v", err)
	}
}

// TestLoadTrailingRecordCount locks what header word 0x14 actually is: the
// record count for the trailing 8-byte record table at word 0x28, not a
// reserved word [02 "Compiled script archive (COB)"] [fmt cob "Header"].
// Retail relocates those records and accepts any count, so a non-zero count
// whose table lies inside the file must LOAD; only a table that runs off the
// end is refused, and that refusal is the I11 bounds-check exception.
//
// This test replaces TestLoadBadAlwaysZero, which asserted the opposite
// (retired 2026-09-04, WU-19-155: the loader was rejecting a shape retail
// reads, under a name the field does not have).
func TestLoadTrailingRecordCount(t *testing.T) {
	data := makeCOB([]uint32{0x10065000}, []string{"Create"}, []uint32{0}, []string{"base"})
	// One 8-byte record living inside the file: point the table at the code
	// array, which is 44 bytes in and long enough for the bounds test.
	withRecords := append([]byte(nil), data...)
	withRecords = append(withRecords, make([]byte, 8)...)
	binary.LittleEndian.PutUint32(withRecords[0x14:], 1)
	binary.LittleEndian.PutUint32(withRecords[0x28:], uint32(len(data)))
	if _, err := Load(withRecords); err != nil {
		t.Fatalf("a trailing record table inside the file must load, got %v", err)
	}

	// The same count with the table hanging off the end is refused.
	offEnd := append([]byte(nil), data...)
	binary.LittleEndian.PutUint32(offEnd[0x14:], 1)
	binary.LittleEndian.PutUint32(offEnd[0x28:], uint32(len(data)-4))
	if _, err := Load(offEnd); err == nil || !strings.Contains(err.Error(), "trailing record table") {
		t.Fatalf("want trailing record bounds error, got %v", err)
	}
}

func TestLoadCodeOffsetOutOfBounds(t *testing.T) {
	data := makeCOB([]uint32{0x10065000}, []string{"Create"}, []uint32{0}, []string{"base"})
	binary.LittleEndian.PutUint32(data[0x24:], 0xFFFFFFF0)
	if _, err := Load(data); err == nil {
		t.Fatalf("want code offset bounds error")
	}
}

func TestLoadScriptIndexOutOfBounds(t *testing.T) {
	data := makeCOB([]uint32{0x10065000}, []string{"Create"}, []uint32{0}, []string{"base"})
	// Patch script code index to 5 which is >= codeLen 1
	off := binary.LittleEndian.Uint32(data[0x18:])
	binary.LittleEndian.PutUint32(data[off:], 5)
	if _, err := Load(data); err == nil || !strings.Contains(err.Error(), "out of bounds") {
		t.Fatalf("want script index bounds, got %v", err)
	}
}

func TestLoadNameOffsetOutOfBounds(t *testing.T) {
	data := makeCOB([]uint32{0x10065000}, []string{"Create"}, []uint32{0}, []string{"base"})
	off := binary.LittleEndian.Uint32(data[0x1C:])
	binary.LittleEndian.PutUint32(data[off:], uint32(len(data)+10))
	if _, err := Load(data); err == nil {
		t.Fatalf("want name offset bounds error")
	}
}

func TestLoadMissingNUL(t *testing.T) {
	// Use zero pieces so the only strings are the script name; corrupting its
	// NUL leaves no later NUL to terminate it.
	data := makeCOB([]uint32{0x10065000}, []string{"Create"}, []uint32{0}, nil)
	off := binary.LittleEndian.Uint32(data[0x1C:])
	strOff := binary.LittleEndian.Uint32(data[off:])
	for i := int(strOff); i < len(data); i++ {
		if data[i] == 0 {
			data[i] = 'X'
			break
		}
	}
	// Fill any remaining NULs so readCString finds no terminator.
	for i := int(strOff); i < len(data); i++ {
		if data[i] == 0 {
			data[i] = 'Y'
		}
	}
	if _, err := Load(data); err == nil || !strings.Contains(err.Error(), "missing NUL") {
		t.Fatalf("want missing NUL, got %v", err)
	}
}

func TestLoadDuplicateScriptName(t *testing.T) {
	code := []uint32{0x10065000, 0x10065000}
	data := makeCOB(code, []string{"Create", "Create"}, []uint32{0, 1}, []string{"base"})
	if _, err := Load(data); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("want duplicate error, got %v", err)
	}
}

func TestLoadUnalignedOffset(t *testing.T) {
	data := makeCOB([]uint32{0x10065000}, []string{"Create"}, []uint32{0}, []string{"base"})
	// Make code offset unaligned.
	binary.LittleEndian.PutUint32(data[0x24:], 45)
	if _, err := Load(data); err == nil || !strings.Contains(err.Error(), "aligned") {
		t.Fatalf("want aligned error, got %v", err)
	}
}

func TestLoadEmptyScriptName(t *testing.T) {
	// Build with empty script name string: first string byte is NUL.
	code := []uint32{0x10065000}
	data := makeCOB(code, []string{"Create"}, []uint32{0}, []string{"base"})
	off := binary.LittleEndian.Uint32(data[0x1C:])
	strOff := binary.LittleEndian.Uint32(data[off:])
	data[strOff] = 0 // empty
	if _, err := Load(data); err == nil || !strings.Contains(err.Error(), "empty name") {
		t.Fatalf("want empty name error, got %v", err)
	}
}

func TestLoadTruncatedTable(t *testing.T) {
	data := makeCOB([]uint32{0x10065000, 0x10065000}, []string{"A", "B"}, []uint32{0, 1}, []string{"p1", "p2"})
	// Truncate so piece name table is incomplete.
	trunc := data[:len(data)-5]
	if _, err := Load(trunc); err == nil {
		t.Fatalf("want truncated table error")
	}
	_ = bytes.Contains // keep import used
}

// Asset-guarded test: loads every COB via VFS and asserts structural
// relationships. Skips when retail assets are not opted in via
// testsupport.RetailRoot, so the suite passes on machines with no game
// installed.
func TestLoadRetailCOBs(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatalf("mount: %v", err)
	}
	defer fs.Close()

	records, err := fs.Manifest(vfs.ManifestOptions{})
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	// Filter logical paths for scripts/*.cob case-insensitive.
	var cobPaths []string
	for _, rec := range records {
		if strings.EqualFold(filepath.Ext(rec.LogicalPath), ".cob") && strings.HasPrefix(strings.ToLower(rec.LogicalPath), "scripts/") {
			cobPaths = append(cobPaths, rec.LogicalPath)
		}
	}
	if len(cobPaths) == 0 {
		t.Fatal("no COB files found via VFS; manifest may be wrong")
	}
	sort.Strings(cobPaths)

	var loaded int
	var failures []string
	for _, p := range cobPaths {
		data, err := fs.ReadFileLimit(p, 8<<20)
		if err != nil {
			if len(failures) < 5 {
				failures = append(failures, p+": read: "+err.Error())
			}
			continue
		}
		prog, err := Load(data)
		if err != nil {
			if len(failures) < 5 {
				failures = append(failures, p+": load: "+err.Error())
			}
			continue
		}
		// Every script name maps to valid code offset within bounds.
		for name, idx := range prog.Scripts {
			if idx < 0 || idx >= len(prog.Code) {
				t.Errorf("%s script %q index %d out of bounds len %d", p, name, idx, len(prog.Code))
			}
		}
		// Piece count matches name table: already ensured len(Pieces) == NumberOfPieces,
		// but also verify pieces are non-empty and no duplicate piece names case-insensitive?
		seen := make(map[string]bool)
		for _, name := range prog.Pieces {
			if name == "" {
				t.Errorf("%s empty piece name", p)
			}
			lower := strings.ToLower(name)
			if seen[lower] {
				t.Errorf("%s duplicate piece name %q (case-insensitive)", p, name)
			}
			seen[lower] = true
		}
		loaded++
	}
	if len(failures) > 0 {
		t.Fatalf("%d of %d COBs failed: %v", len(failures), len(cobPaths), failures)
	}
	t.Logf("loaded %d COBs via VFS; all script indexes within bounds and piece tables consistent", loaded)
	// No census counts asserted: the install may be base + expansions with varying file counts.
	// Relationship-only assertions per I14.
}
