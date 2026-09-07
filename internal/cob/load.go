// This file: the loader [fmt cob] [02 "Compiled script archive (COB)"]
// [04 §4.1].
//
// Retail loads a COB file whole into one allocation and relocates absolute
// file offsets in place [02 "Compiled script archive (COB)"] [04 §4.1]. This
// loader keeps that byte layout as the contract (I13) and decodes per
// [fmt cob] into an immutable Program.

package cob

import (
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/vfs"
)

// MaxProgramStaticBytes is Nanolathe's host-safety limit for one VM's static
// storage. It is not a recovered retail limit: COB has no file-backed static
// data with which to bound its declared count [fmt cob "Header"].
const MaxProgramStaticBytes = 4 << 20

// ValidateProgram rejects an externally supplied program whose static count
// cannot be represented within the supported per-VM memory budget. It is
// called before every mutable VM allocation, including restore [02 R-MALF-01
// §8].
func ValidateProgram(prog *Program) error {
	if prog == nil {
		return nil
	}
	if prog.Statics < 0 || uint64(prog.Statics) > uint64(MaxProgramStaticBytes/4) {
		return fmt.Errorf("cob: static storage %d words exceeds supported %d-byte VM budget", prog.Statics, MaxProgramStaticBytes)
	}
	return nil
}

// Program is an immutable compiled script definition [PLAN_06 cob] [fmt cob].
// Code is the opcode word array. Scripts maps script name to word index into
// Code (word index relative to start of code section, not byte offset).
// Pieces is the ordered piece name table. Statics is the count of static
// variables the unit needs [fmt cob] "NumberOfStatics" — zero-initialized by
// the engine [fmt cob] [04 §4.2]; carried on the immutable Program to avoid
// reparse in the VM.
// ScriptsByID is the script code index array in table order 0..numScripts-1,
// preserved for the VM's script-id operand (E shape) [04 §4.3] C12.
type Program struct {
	Code           []uint32
	Scripts        map[string]int
	Pieces         []string
	Statics        int
	ScriptsByID    []int  // [fmt cob] ScriptCodeIndexArray in table order [04 §4.3]
	SourceChecksum uint32 // four-accumulator checksum of original raw COB bytes [02 "Content checksum"]
}

// ContentChecksum is the retail four-accumulator byte checksum. Each lane is
// eight bits and wraps independently; the packed result is lane order
// additive, xor, indexed-additive, indexed-xor [02 "Content checksum"].
func ContentChecksum(data []byte) uint32 {
	var add, xor, indexedAdd, indexedXor uint8
	for i, b := range data {
		add += b
		xor ^= b
		indexedAdd += uint8((i & 0xff) ^ int(b))
		indexedXor ^= uint8((i + int(b)) & 0xff)
	}
	return uint32(add) | uint32(xor)<<8 | uint32(indexedAdd)<<16 | uint32(indexedXor)<<24
}

// Load parses COB bytes per [fmt cob] container reference.
// Header is 44 bytes = 11 × u32 little-endian [fmt cob] "Header (44 bytes, 11 × u32)".
// Offsets are absolute file offsets, code addresses are word indexes relative
// to start of code section [fmt cob] "Every field ... little-endian u32. All
// offsets are absolute file offsets. Code addresses ... are word indexes".
func Load(data []byte) (*Program, error) {
	const headerSize = 44 // [fmt cob] 11 × u32
	if len(data) < headerSize {
		return nil, fmt.Errorf("cob: header too short %d < %d", len(data), headerSize)
	}
	sourceChecksum := ContentChecksum(data)
	readU32 := func(off int) uint32 { return binary.LittleEndian.Uint32(data[off:]) }

	version := readU32(0x00)    // VersionSignature [fmt cob]
	numScripts := readU32(0x04) // NumberOfScripts [fmt cob]
	numPieces := readU32(0x08)  // NumberOfPieces [fmt cob]
	codeLen := readU32(0x0C)    // CodeLength [fmt cob] "Length of the code section in u32 words"
	numStatics := readU32(0x10) // NumberOfStatics [fmt cob] — carried as Program.Statics, zero-initialized by engine [fmt cob] [04 §4.2]
	// Word 0x14 is the record count for the trailing 8-byte record table whose
	// pointer is word 0x28 — it is NOT a reserved word, and 0x28 is NOT a
	// separate "first script name" offset. The retail loader relocates five
	// table pointers and, for this count, biases the second dword of each
	// 8-byte record [02 "Compiled script archive (COB)"], [02 R-MALF-01 §8],
	// [fmt cob "Header"]. Both are zero/coincidental in the whole retail corpus
	// — the count is 0 in all 835 shipped scripts, which leaves the pointer
	// indistinguishable from the start of the string pool — which is how the
	// two words acquired their old names.
	numTrailingRecords := readU32(0x14)
	offScriptCodeIndexArray := readU32(0x18)  // script entry-point table [fmt cob]
	offScriptNameOffsetArray := readU32(0x1C) // script name table [fmt cob]
	offPieceNameOffsetArray := readU32(0x20)  // piece name table [fmt cob]
	offScriptCode := readU32(0x24)            // code array [fmt cob]
	offTrailingRecords := readU32(0x28)       // trailing record table [fmt cob]

	if version != 4 {
		// [fmt cob] "4 for TA. (Kingdoms uses other versions; not covered here.)"
		// TAK header is 52 bytes / 13 words when VersionSignature == 6 [fmt cob] "TAK-only opcodes".
		// Divergence (I11), noted: retail never reads the version word at all
		// [02 R-MALF-01 §8]. We reject a non-4 file rather than walking a TAK
		// layout as if it were a TA one.
		return nil, fmt.Errorf("cob: unsupported version %d want 4", version)
	}

	// Guard against overflow of count*4.
	if numScripts > 1<<28 || numPieces > 1<<28 || codeLen > 1<<28 {
		return nil, fmt.Errorf("cob: implausibly large counts scripts=%d pieces=%d codeLen=%d", numScripts, numPieces, codeLen)
	}
	if uint64(numStatics) > uint64(MaxProgramStaticBytes/4) {
		return nil, fmt.Errorf("cob: static storage %d words exceeds supported %d-byte VM budget", numStatics, MaxProgramStaticBytes)
	}
	fileLen := uint32(len(data))

	// Helper to validate table regions [fmt cob] offsets are absolute file offsets.
	validateTable := func(name string, off uint32, count uint32) error {
		if count == 0 {
			return nil
		}
		if off < headerSize && off != 0 {
			// Offsets must point outside header, but allow 0 sentinel only when count==0 (already returned).
			// Still allow any offset within file; just validate bounds.
		}
		if off > fileLen {
			return fmt.Errorf("cob: %s offset %#x beyond file size %#x", name, off, fileLen)
		}
		if off%4 != 0 {
			return fmt.Errorf("cob: %s offset %#x not 4-byte aligned", name, off)
		}
		need := count * 4
		if off+need < off {
			return fmt.Errorf("cob: %s offset overflow", name)
		}
		if off+need > fileLen {
			return fmt.Errorf("cob: %s table [%#x + %#x] exceeds file size %#x", name, off, need, fileLen)
		}
		return nil
	}
	if err := validateTable("script code index array", offScriptCodeIndexArray, numScripts); err != nil {
		return nil, err
	}
	if err := validateTable("script name offset array", offScriptNameOffsetArray, numScripts); err != nil {
		return nil, err
	}
	if err := validateTable("piece name offset array", offPieceNameOffsetArray, numPieces); err != nil {
		return nil, err
	}
	// Code array bounds [fmt cob] "Code (u32 words × code_len) ... entry points via index table".
	if codeLen > 0 {
		if offScriptCode > fileLen {
			return nil, fmt.Errorf("cob: code offset %#x beyond file size %#x", offScriptCode, fileLen)
		}
		if offScriptCode%4 != 0 {
			return nil, fmt.Errorf("cob: code offset %#x not 4-byte aligned", offScriptCode)
		}
		need := codeLen * 4
		if offScriptCode+need < offScriptCode {
			return nil, fmt.Errorf("cob: code table overflow")
		}
		if offScriptCode+need > fileLen {
			return nil, fmt.Errorf("cob: code array [%#x + %#x] exceeds file size %#x", offScriptCode, need, fileLen)
		}
	} else {
		// Zero-length code section: still validate offset is within file if non-zero.
		if offScriptCode != 0 && offScriptCode > fileLen {
			return nil, fmt.Errorf("cob: code offset %#x beyond file size %#x", offScriptCode, fileLen)
		}
	}
	// The trailing record table: bounds only. Retail relocates its records but
	// nothing in the recovered image reads one, and the count is zero in every
	// shipped script, so Nanolathe carries no representation of them
	// [02 "Compiled script archive (COB)"]. A non-zero count is accepted (retail
	// accepts it); the records are simply not parsed. What an 8-byte record
	// means is an Unknown recorded in [fmt cob], not a code marker, because no
	// retail data reaches it.
	if offTrailingRecords != 0 && offTrailingRecords > fileLen {
		return nil, fmt.Errorf("cob: trailing record table offset %#x beyond file size %#x", offTrailingRecords, fileLen)
	}
	if numTrailingRecords != 0 {
		if numTrailingRecords > 1<<28 {
			return nil, fmt.Errorf("cob: implausibly large trailing record count %d", numTrailingRecords)
		}
		need := numTrailingRecords * 8
		if offTrailingRecords+need < offTrailingRecords || offTrailingRecords+need > fileLen {
			return nil, fmt.Errorf("cob: trailing record table [%#x + %#x] exceeds file size %#x", offTrailingRecords, need, fileLen)
		}
	}

	// Relocation applied per [04 §4.1] [02 "Compiled script archive (COB)"]: every offset in
	// the file is absolute from file start and would be biased by the load base in retail.
	// Here we interpret them as indices into data.

	// Decode code words [fmt cob] little-endian u32.
	code := make([]uint32, codeLen)
	for i := uint32(0); i < codeLen; i++ {
		off := offScriptCode + i*4
		code[i] = binary.LittleEndian.Uint32(data[off:])
	}

	// Decode script code indexes.
	scriptIndexes := make([]uint32, numScripts)
	for i := uint32(0); i < numScripts; i++ {
		off := offScriptCodeIndexArray + i*4
		scriptIndexes[i] = binary.LittleEndian.Uint32(data[off:])
		if scriptIndexes[i] >= codeLen && codeLen != 0 {
			return nil, fmt.Errorf("cob: script %d code index %d out of bounds (codeLen %d)", i, scriptIndexes[i], codeLen)
		}
		if codeLen == 0 && scriptIndexes[i] != 0 {
			return nil, fmt.Errorf("cob: script %d code index %d with empty code section", i, scriptIndexes[i])
		}
	}

	// Decode script name offsets.
	scriptNameOffs := make([]uint32, numScripts)
	for i := uint32(0); i < numScripts; i++ {
		off := offScriptNameOffsetArray + i*4
		scriptNameOffs[i] = binary.LittleEndian.Uint32(data[off:])
		if scriptNameOffs[i] >= fileLen {
			return nil, fmt.Errorf("cob: script %d name offset %#x beyond file", i, scriptNameOffs[i])
		}
	}
	// Decode piece name offsets.
	pieceNameOffs := make([]uint32, numPieces)
	for i := uint32(0); i < numPieces; i++ {
		off := offPieceNameOffsetArray + i*4
		pieceNameOffs[i] = binary.LittleEndian.Uint32(data[off:])
		if pieceNameOffs[i] >= fileLen {
			return nil, fmt.Errorf("cob: piece %d name offset %#x beyond file", i, pieceNameOffs[i])
		}
	}

	// Helper to read NUL-terminated string at absolute offset [fmt cob] "NUL-terminated script names".
	readCString := func(off uint32) (string, error) {
		if off >= fileLen {
			return "", fmt.Errorf("cob: string offset %#x beyond file", off)
		}
		end := off
		for end < fileLen && data[end] != 0 {
			end++
		}
		if end >= fileLen {
			return "", fmt.Errorf("cob: string at %#x missing NUL terminator", off)
		}
		// Empty names are allowed? For script names they are not expected, but piece names could be?
		return string(data[off:end]), nil
	}

	scripts := make(map[string]int, numScripts)
	// Deterministic iteration: ascending index order (I1).
	for i := uint32(0); i < numScripts; i++ {
		name, err := readCString(scriptNameOffs[i])
		if err != nil {
			return nil, fmt.Errorf("cob: script %d name: %w", i, err)
		}
		if name == "" {
			return nil, fmt.Errorf("cob: script %d has empty name", i)
		}
		if _, exists := scripts[name]; exists {
			return nil, fmt.Errorf("cob: duplicate script name %q", name)
		}
		scripts[name] = int(scriptIndexes[i])
	}

	pieces := make([]string, numPieces)
	for i := uint32(0); i < numPieces; i++ {
		name, err := readCString(pieceNameOffs[i])
		if err != nil {
			return nil, fmt.Errorf("cob: piece %d name: %w", i, err)
		}
		if name == "" {
			return nil, fmt.Errorf("cob: piece %d has empty name", i)
		}
		pieces[i] = name
	}

	// Piece count must match name table; already ensured via numPieces length.

	byID := make([]int, len(scriptIndexes))
	for i, v := range scriptIndexes {
		byID[i] = int(v)
	}

	prog := &Program{
		Code:           code,
		Scripts:        scripts,
		Pieces:         pieces,
		Statics:        int(numStatics),
		ScriptsByID:    byID,
		SourceChecksum: sourceChecksum,
	}
	if err := ValidateProgram(prog); err != nil {
		return nil, err
	}
	return prog, nil
}

// LoadFromFS loads a compiled script for unitName via VFS [fmt cob] [04 §4.1].
// It tries logical paths scripts/<unitName>.cob case-insensitively (VFS cleanPath is case-folded).
// Returns (nil, false, nil) when no COB exists for the unit, so callers can create an empty fallback VM without error.
// An existing file that fails to parse returns (nil, true, error).
func LoadFromFS(fs vfs.FSOps, unitName string) (*Program, bool, error) {
	if fs == nil || strings.TrimSpace(unitName) == "" {
		return nil, false, nil
	}
	// VFS is case-insensitive via cleanPath lowercasing; use lowercased logical path [vfs/path.go].
	name := strings.TrimSpace(unitName)
	// Try canonical lower form; VFS will fold again, but keep deterministic.
	candidates := []string{
		"scripts/" + strings.ToLower(name) + ".cob",
		"scripts/" + name + ".cob",
		"scripts/" + strings.ToUpper(name) + ".cob",
	}
	for _, p := range candidates {
		data, err := fs.ReadFileLimit(p, 4<<20) // 4 MiB limit [fmt cob] COB size bounded
		if err != nil {
			continue
		}
		prog, err := Load(data)
		if err != nil {
			return nil, true, fmt.Errorf("cob: %s: %w", p, err)
		}
		return prog, true, nil
	}
	return nil, false, nil
}

// CachedLoader memoizes per-unit Programs to avoid repeated VFS reads and reparse [PLAN_06].
type CachedLoader struct {
	cache map[string]*Program
}

// NewCachedLoader returns an empty loader cache.
func NewCachedLoader() *CachedLoader { return &CachedLoader{cache: make(map[string]*Program)} }

// Load returns the unit's Program, parsing it on the first request and serving
// the memoized copy afterwards. The second result is false when no COB exists
// for the unit, which is not an error. A nil loader parses every time.
func (c *CachedLoader) Load(fs vfs.FSOps, unitName string) (*Program, bool, error) {
	if c == nil {
		return LoadFromFS(fs, unitName)
	}
	ck := strings.ToLower(strings.TrimSpace(unitName))
	if prog, ok := c.cache[ck]; ok {
		if prog == nil {
			return nil, false, nil
		}
		return prog, true, nil
	}
	prog, found, err := LoadFromFS(fs, unitName)
	if err != nil {
		return nil, true, err
	}
	if found && prog != nil {
		if c.cache == nil {
			c.cache = make(map[string]*Program)
		}
		c.cache[ck] = prog
		return prog, true, nil
	}
	if c.cache == nil {
		c.cache = make(map[string]*Program)
	}
	c.cache[ck] = nil // negative cache for missing script
	return nil, false, nil
}
