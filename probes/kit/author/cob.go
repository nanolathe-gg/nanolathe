package author

// COB writer — container per research/formats/cob.md (version 4, 44-byte
// header, u32 code words, word-indexed entry points).

// Opcodes used by the kit's minimal scripts ([fmt cob] "Instruction set").
const (
	OpPushConstant = 0x10021001
	OpAllocLocal   = 0x10022000
	OpReturn       = 0x10065000
)

// COBScript is one named entry point with its code words.
type COBScript struct {
	Name string
	Code []uint32
}

// MinimalScripts returns the two entry points every probe unit carries:
//
//	Create()                  { return 0; }
//	Killed(severity, corpse)  { return 0; }
//
// Killed declares its two parameter cells with alloc-local and leaves the
// corpse cell zero — the same shape as the retail CORTRUCK example.
func MinimalScripts() []COBScript {
	return []COBScript{
		{Name: "Create", Code: []uint32{OpPushConstant, 0, OpReturn}},
		{Name: "Killed", Code: []uint32{OpAllocLocal, OpAllocLocal, OpPushConstant, 0, OpReturn}},
	}
}

// COBBytes serialises scripts and piece names. Statics = 0.
func COBBytes(scripts []COBScript, pieces []string) []byte {
	var code []uint32
	entry := make([]uint32, len(scripts))
	for i, s := range scripts {
		entry[i] = uint32(len(code))
		code = append(code, s.Code...)
	}
	var b buf
	b.u32(4)                    // 0x00 VersionSignature
	b.u32(uint32(len(scripts))) // 0x04 NumberOfScripts
	b.u32(uint32(len(pieces)))  // 0x08 NumberOfPieces
	b.u32(uint32(len(code)))    // 0x0C CodeLength (u32 words)
	b.u32(0)                    // 0x10 NumberOfStatics
	b.u32(0)                    // 0x14 Always_0
	pIndex := b.off()           // 0x18 OffsetToScriptCodeIndexArray
	b.u32(0)
	pNames := b.off() // 0x1C OffsetToScriptNameOffsetArray
	b.u32(0)
	pPieces := b.off() // 0x20 OffsetToPieceNameOffsetArray
	b.u32(0)
	pCode := b.off() // 0x24 OffsetToScriptCode
	b.u32(0)
	pFirst := b.off() // 0x28 OffsetToFirstScriptName
	b.u32(0)
	check(b.off() == 44, "cob: header size")

	b.patchU32(pCode, b.off())
	for _, w := range code {
		b.u32(w)
	}
	b.patchU32(pIndex, b.off())
	for _, e := range entry {
		b.u32(e)
	}
	b.patchU32(pNames, b.off())
	nameSlots := b.off()
	for range scripts {
		b.u32(0)
	}
	b.patchU32(pPieces, b.off())
	pieceSlots := b.off()
	for range pieces {
		b.u32(0)
	}
	for i, s := range scripts {
		at := b.off()
		if i == 0 {
			b.patchU32(pFirst, at)
		}
		b.patchU32(nameSlots+uint32(i)*4, at)
		b.cstr(s.Name)
	}
	for i, p := range pieces {
		b.patchU32(pieceSlots+uint32(i)*4, b.off())
		b.cstr(p)
	}
	return b.Bytes()
}
