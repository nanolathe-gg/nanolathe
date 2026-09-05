package mission

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/vfs"
)

// UnitPlacement is the 36-byte retail unit placement record decoded into named
// fields per I13. Strings are pointers into a tail heap after the record array
// in retail; Go stores them as strings. X/Z/Y are fixed 16.16 dwords (authored
// <<16), Angle is heading 0..65535 via degrees conversion, flags pack into one
// byte with only the high bit consumed. [GAP T14] [08 "Mission placement record"] [02 "Map files"]
type UnitPlacement struct {
	UnitName       string // Unitname, pointer into tail heap in retail [GAP T14] [02 "Map files"]
	Ident          string // Ident, pointer into tail heap in retail [GAP T14]
	InitialMission string // InitialMission, pointer into tail heap in retail [02 "Map files"]

	X int32 // X fixed 16.16, authored XPos <<16 [GAP T14] [02 "Map files"]
	Z int32 // Z fixed 16.16, authored ZPos <<16 [GAP T14]
	Y int32 // Y fixed 16.16, authored YPos <<16 [GAP T14]

	Angle uint16 // heading 0..65535, trunc((int32)(degrees<<16)/360) [08 "Mission placement record"] [fmt ota]

	Player            int32 // Player, default 0, negatives clamped to 0 [02 "Map files"]
	HealthPercentage  int32 // HealthPercentage default 100 [02 "Map files"]
	BuildPriority     int32 // parsed but never read [08 "Mission placement record"] [C7]
	CreationCountdown int32 // CreationCountdown

	// Packed flag byte [08 "Mission placement record"] [02 "Map files"]:
	// bit0 MissionCriticalUnit, bit5 AiIgnore, bit6 AiPriorityTarget, bit7 Immunity (high bit).
	// Only the high bit is consumed by the creation reader [C7].
	MissionCriticalUnit bool // parsed but never read [C7]
	AiIgnore            bool // bit5 parsed but never read [C7] [08 "Mission placement record"]
	AiPriorityTarget    bool // bit6 parsed but never read [C7]
	Immune              bool // high bit 0x80, only consumed flag [C7] [08 "Mission placement record"]

	InitialGroup string // parsed but never read, low nibble in retail [C7] [08 "Mission placement record"]
	Kills        int32  // Kills veterancy, 0 default [fmt ota]

	RawFlags byte // raw packed flag byte as authored, for immunity isolation testing [C7]
}

// IsImmune reports whether the immunity high bit is set. This is the only
// placement flag byte the creation reader consumes [08 "Mission placement record"] [C7].
func (u UnitPlacement) IsImmune() bool { return u.Immune }

// IsImmuneFromFlags extracts the immunity high bit from a raw flag byte.
// Only bit 7 (0x80) matters; all other bits are retained but not acted on. [C7]
func IsImmuneFromFlags(flags byte) bool { return flags&0x80 != 0 }

// Special is the 12-byte retail special placement record decoded into named
// fields per I13. Kind 1 is StartPos; ID is parsed from the numeric suffix of
// specialwhat; X/Z are shorts. [GAP T14] [02 "Map files"]
type Special struct {
	Kind int32  // kind field, 1=StartPos [GAP T14]
	ID   int32  // id parsed from numeric suffix of specialwhat [GAP T14] [02 "Map files"]
	X    int16  // XPos short [GAP T14]
	Z    int16  // ZPos short [GAP T14]
	Name string // specialwhat original string, e.g. "StartPos1" [fmt ota]
}

// FeaturePlacement is the 136-byte retail feature placement record decoded
// into named fields per I13. Featurename is a 128-byte buffer; X/Z default to
// -1 and are cleared when negative before stamping. [GAP T14] [02 "Map files"] [08 "Mission placement record"]
type FeaturePlacement struct {
	Name string // Featurename, 128-byte buffer [GAP T14] [02 "Map files"]
	X    int32  // XPos integer default -1, cleared when negative [GAP T14] [02 "Map files"]
	Z    int32  // ZPos integer default -1, cleared when negative
	RawX int32  // authored X before clearing, for round-trip testing
	RawZ int32  // authored Z before clearing
}

// IsPlaced reports whether the feature has valid coordinates after clearing.
// Negative coordinates are cleared to -1 sentinel and are not stamped. [GAP T14]
func (f FeaturePlacement) IsPlaced() bool { return f.X >= 0 && f.Z >= 0 }

// WindBounds retains the selected map's wind minimum and maximum for the
// single battle-entry initializer owned by PLAN_14. It performs no RNG draws
// itself; the exact briefing-entry sequence is PLAN_14 C17.
// [08 "Wind initialization"] [C5]
type WindBounds struct {
	Min int32 // minwindspeed [02 "Map files"] [08 "Wind initialization"]
	Max int32 // maxwindspeed [02 "Map files"] [08 "Wind initialization"]
}

// DecodeWindBounds extracts wind bounds from the OTA GlobalHeader without
// performing any RNG draws. [C5] [08 "Wind initialization"] [02 "Map files"]
func DecodeWindBounds(global *formats.Section) WindBounds {
	if global == nil {
		return WindBounds{}
	}
	return WindBounds{
		Min: global.IntValue("minwindspeed", 0), // [02 "Map files"]
		Max: global.IntValue("maxwindspeed", 0), // [02 "Map files"]
	}
}

// UseOnlyBasePath is the campaign resource area for UseOnlyUnits routing.
// [08 "Mission placement record"] [C8] [02 "Map files"]
const UseOnlyBasePath = "camps/useonly"

// UseOnlyPath returns the resolved logical path for a UseOnlyUnits value
// routed through the resource resolver into the campaign camps\useonly area.
// An empty input returns empty (no restriction). The path uses forward
// slashes for VFS; retail uses backslash camps\useonly but the resolver
// normalizes. [C8] [08 "Mission placement record"] [fmt ota]
func UseOnlyPath(useOnlyUnits string) string {
	trimmed := strings.TrimSpace(useOnlyUnits)
	if trimmed == "" {
		return ""
	}
	// If the value already looks like a path, preserve the basename? Retail
	// stores a TDF filename with extension in camps/useonly/ [fmt ota].
	// We just join with the base path; the caller can normalize case if needed.
	trimmed = strings.TrimPrefix(trimmed, "/")
	trimmed = strings.TrimPrefix(trimmed, "\\")
	// Normalize backslashes to slashes for VFS.
	trimmed = strings.ReplaceAll(trimmed, "\\", "/")
	// Strip any leading camps/useonly prefix to avoid doubling.
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "camps/useonly/") {
		trimmed = trimmed[len("camps/useonly/"):]
	} else if strings.HasPrefix(lower, "camps\\useonly\\") {
		trimmed = trimmed[len("camps\\useonly\\"):]
	}
	if trimmed == "" {
		return ""
	}
	return UseOnlyBasePath + "/" + trimmed
}

// DecodeUseOnlyUnits reads the UseOnlyUnits key from the OTA GlobalHeader
// and returns the routed logical path via UseOnlyPath. It is not inert.
// [C8] [08 "Mission placement record"] [fmt ota]
func DecodeUseOnlyUnits(global *formats.Section) string {
	if global == nil {
		return ""
	}
	v, _ := global.StringValue("useonlyunits", "") // [02 "Map files"] [fmt ota]
	return UseOnlyPath(v)
}

// LoadUseOnlyNames opens the routed UseOnlyUnits file and returns the unit
// names it lists, in file order, together with whether the file was present
// [08 R-ENTRY-01 §2 step 4].
//
// The file is a TDF whose top-level sections are named for the units the
// mission allows; the sections carry no keys. Retail matches each section name
// against a definition's `unitname` case-insensitively and takes the first
// matching record. A missing file is not an error: it leaves every definition
// creatable, which is why presence is reported separately from the list.
//
// An empty path is "no restriction file authored" and reports absent without
// touching the VFS.
//
// A *malformed* file is not an empty allow-list. The marker that stood here
// said retail's "lenient parser would yield no sections, which would clear
// every definition and leave an empty catalog", and called the reader's error
// path untraced. Both halves were wrong: retail's TDF reader is not lenient,
// and the path is closed. Every one of its five syntax diagnostics is handed
// to the fatal channel — a system-modal box, then exit(1) — with no error
// return and no partially-built tree [02 R-MALF-01 §4][fmt tdf]. The only
// recoverable failure is a missing or zero-length file, which yields no tree.
//
// So the empty-catalog outcome the marker feared cannot be reached through a
// syntax error: retail never gets as far as clearing the available bits. It is
// reachable only through a file that parses cleanly and names nothing, and
// there it is genuinely what retail does — the loader clears the bit on every
// definition when the file *exists* and re-sets it only for the sections the
// file names [08 R-ENTRY-01 §2 step 4]. Returning the parse error is the
// faithful shape of the fatal arm (a loader cannot exit the process for the
// caller), and a clean parse with no sections is reported as present-with-no-
// names so the caller reproduces the clear-everything arm instead of silently
// ignoring the file.
func LoadUseOnlyNames(fs vfs.FSOps, path string) ([]string, bool, error) {
	path = strings.TrimSpace(path)
	if path == "" || fs == nil {
		return nil, false, nil
	}
	data, err := fs.ReadFileLimit(path, int64(formats.DefaultTDFLimits().MaxBytes))
	if err != nil {
		// Absent (or unreadable) restriction file: every definition stays
		// creatable [08 R-ENTRY-01 §2 step 4].
		return nil, false, nil
	}
	doc, err := formats.ParseTDF(data)
	if err != nil {
		return nil, false, fmt.Errorf("nanolathe: unit-restriction file parse failed: logical path %s, providers searched [vfs], expected TDF sections naming allowed units: %w", path, err)
	}
	if doc == nil || doc.Root == nil {
		return nil, true, nil
	}
	sections := doc.Root.Sections()
	names := make([]string, 0, len(sections))
	for _, sec := range sections {
		if sec == nil {
			continue
		}
		name := strings.TrimSpace(sec.OriginalName)
		if name == "" {
			name = strings.TrimSpace(sec.Name)
		}
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	return names, true, nil
}

// DecodeUnitPlacements decodes unit placements from the selected schema's
// [units] section. Coordinates are shifted left 16 to fixed 16.16, Angle
// converts via trunc(degrees*65536/360), Player negatives clamped to 0,
// HealthPercentage defaults 100, and flag bits are retained with only the
// high bit (immunity) consumed. [GAP T14] [08 "Mission placement record"] [02 "Map files"] [C6] [C7]
func DecodeUnitPlacements(schema *formats.Section) []UnitPlacement {
	if schema == nil {
		return nil
	}
	unitsSec := schema.Section("units")
	if unitsSec == nil {
		return nil
	}
	var out []UnitPlacement
	for _, sec := range unitsSec.Sections() {
		u := decodeUnitSection(sec)
		out = append(out, u)
	}
	return out
}

func decodeUnitSection(sec *formats.Section) UnitPlacement {
	var u UnitPlacement
	u.UnitName, _ = sec.StringValue("Unitname", "")
	u.Ident, _ = sec.StringValue("Ident", "")
	u.InitialMission, _ = sec.StringValue("InitialMission", "")

	// X/Z/Y shifted left 16 to 16.16 fixed [GAP T14] [02 "Map files"]
	x := sec.IntValue("XPos", 0)
	z := sec.IntValue("ZPos", 0)
	y := sec.IntValue("YPos", 0)
	u.X = x << 16
	u.Z = z << 16
	u.Y = y << 16

	// Angle: the INTEGER accessor with default 0, then the one signed division
	// of HeadingFromDegrees [08 "Mission placement record"][08 R-TRIG-01 §9].
	//
	// This read used to branch: an integral-looking string took the retail
	// conversion, anything else took a second, float-truncating copy of the
	// formula. Retail has no such branch — the key is read by the same integer
	// accessor as XPos/YPos/ZPos and Player, so a fractional authored value is
	// consumed by the accessor and never reaches the conversion as a fraction.
	// The float copy also disagreed with the retail conversion outside
	// -32768..32767, which is the only place the two can differ at all.
	u.Angle = HeadingFromDegrees(sec.IntValue("Angle", 0))

	// Player defaults 0, negatives clamped to 0 [02 "Map files"]
	p := sec.IntValue("Player", 0)
	if p < 0 {
		p = 0
	}
	u.Player = p
	u.HealthPercentage = sec.IntValue("HealthPercentage", 100) // default 100 [02 "Map files"]
	u.BuildPriority = sec.IntValue("BuildPriority", 0)         // parsed but never read [C7]
	u.CreationCountdown = sec.IntValue("CreationCountdown", 0)
	u.Kills = sec.IntValue("Kills", 0)

	// Packed flag byte: bits per the decompile reader census
	// (notes/campaign/00_missions.md §4.1, address-backed): bit0 = nonzero
	// InitialGroup, bit4 = MissionCriticalUnit, bit5 = AiIgnore, bit6 =
	// AiPriorityTarget, bit7 = Immunity. Only the high bit is consumed [C7].
	var flags byte
	if sec.IntValue("MissionCriticalUnit", 0) != 0 {
		flags |= 1 << 4 // bit4 [08 "Mission placement record"]
		u.MissionCriticalUnit = true
	}
	if sec.IntValue("AiIgnore", 0) != 0 {
		flags |= 1 << 5 // bit5 [08 "Established AI-facing data"] [C7]
		u.AiIgnore = true
	}
	if sec.IntValue("AiPriorityTarget", 0) != 0 {
		flags |= 1 << 6 // bit6 [C7]
		u.AiPriorityTarget = true
	}
	if sec.IntValue("Immunity", 0) != 0 {
		flags |= 1 << 7 // high bit 0x80 [C7] [08 "Mission placement record"]
		u.Immune = true
	}
	u.RawFlags = flags

	u.InitialGroup, _ = sec.StringValue("InitialGroup", "") // parsed but never read [C7]
	return u
}

// startPosPrefix is the eight-character, case-insensitive prefix that makes a
// special a start position; everything else in a `[specials]` block is dropped
// by retail and carries no start-position number [08 R-TRIG-01 §9].
const startPosPrefix = "startpos"

// DecodeSpecials decodes 12-byte special records from the selected schema's
// [specials] section in authored order. Kind 1 is StartPos; ID is the STORED
// start-position number, which is one less than the authored label
// [08 R-TRIG-01 §9] [fmt ota].
func DecodeSpecials(schema *formats.Section) []Special {
	if schema == nil {
		return nil
	}
	specialsSec := schema.Section("specials")
	if specialsSec == nil {
		return nil
	}
	var out []Special
	// The counter runs over the start-position records in file order and is
	// what a record with no digit after `StartPos` takes instead of a suffix
	// [08 R-TRIG-01 §9], so it lives here rather than in the per-record decode.
	counter := int32(0)
	for _, sec := range specialsSec.Sections() {
		s := decodeSpecialSection(sec, &counter)
		out = append(out, s)
	}
	return out
}

func decodeSpecialSection(sec *formats.Section, counter *int32) Special {
	var s Special
	sw, _ := sec.StringValue("specialwhat", "")
	s.Name = sw
	trimmed := strings.TrimSpace(sw)
	if strings.HasPrefix(strings.ToLower(trimmed), startPosPrefix) {
		s.Kind = 1 // 1=StartPos [08 R-TRIG-01 §9]
		*counter++
		s.ID = startPosStoredNumber(trimmed[len(startPosPrefix):], *counter)
	}
	// A record that is not a start position keeps its authored coordinates for
	// diagnostics but carries no number: retail keeps no such record at all, so
	// nothing downstream may key on one [08 R-TRIG-01 §9].
	s.X = int16(sec.IntValue("XPos", 0))
	s.Z = int16(sec.IntValue("ZPos", 0))
	return s
}

// startPosStoredNumber turns the text after `StartPos` into the number the
// specials array stores [08 R-TRIG-01 §9]: the suffix is parsed as an integer
// when its first character is a digit, otherwise the record takes the running
// counter; the stored number is that value minus one when it is positive, and
// the value itself otherwise. `StartPos0` and `StartPos1` therefore both name
// stored position zero, and slot i under identity placement takes
// `StartPos<i+1>` [08 R-ENTRY-01 §5].
//
// Before WU-19-205 this extracted a trailing digit run, gave every alphabetic
// suffix zero, and kept the authored label rather than the stored number. Both
// halves were wrong in the same direction: `StartPosA`/`StartPosB` collided on
// a number no consumer ever asks for, and `StartPos0` was rejected as missing
// (review finding R10).
//
// TODO(question): when a file mixes numeric and non-numeric labels, does the
// counter advance on every start-position record — so `StartPos5, StartPosA`
// gives the second record 2 — or only on the records that consume it, giving
// it 1? [08 R-TRIG-01 §9] and [fmt ota] both say "a running counter starting
// at 1 in file order" without separating the two, and no stock map authors a
// non-numeric label, so nothing in the corpus decides it. Implemented as the
// first reading, which is the one that cannot collide with an authored
// numeric label. A static trace of the specials reader's counter increment
// would settle it.
func startPosStoredNumber(suffix string, counter int32) int32 {
	value := counter
	if len(suffix) > 0 && suffix[0] >= '0' && suffix[0] <= '9' {
		value = formats.ParseTDFInteger(suffix)
	}
	if value > 0 {
		return value - 1
	}
	return value
}

// DecodeFeaturePlacements decodes 136-byte feature records from the selected
// schema's [features] section. Featurename is a 128-byte buffer; X/Z default
// -1 and are cleared when negative before stamping. [GAP T14] [02 "Map files"] [fmt ota]
func DecodeFeaturePlacements(schema *formats.Section) []FeaturePlacement {
	if schema == nil {
		return nil
	}
	featuresSec := schema.Section("features")
	if featuresSec == nil {
		return nil
	}
	var out []FeaturePlacement
	for _, sec := range featuresSec.Sections() {
		f := decodeFeatureSection(sec)
		out = append(out, f)
	}
	return out
}

func decodeFeatureSection(sec *formats.Section) FeaturePlacement {
	var f FeaturePlacement
	name, _ := sec.StringValue("Featurename", "")
	f.Name = name // 128-byte buffer in retail [GAP T14]

	// XPos/ZPos default -1, cleared when negative [GAP T14] [02 "Map files"]
	rawX := sec.IntValue("XPos", -1)
	rawZ := sec.IntValue("ZPos", -1)
	f.RawX = rawX
	f.RawZ = rawZ
	if rawX < 0 {
		f.X = -1 // cleared sentinel [GAP T14]
	} else {
		f.X = rawX
	}
	if rawZ < 0 {
		f.Z = -1
	} else {
		f.Z = rawZ
	}
	return f
}

// HeadingFromDegrees converts an authored `Angle` degree count to the heading
// word retail stores on the placement record [08 "Mission placement record"].
//
// Established, and it is one signed division: the authored degrees are shifted
// left 16 in a 32-bit register and divided by 360, with the quotient truncating
// toward zero. Retail spells the divide as its compiler's magic-multiply idiom
// (multiply by the reciprocal, add the multiplicand back into the high half,
// arithmetic-shift right 8, then add the quotient's sign bit); this reproduces
// the idiom step for step so the wrap behaviour of every stage matches, rather
// than substituting a plain divide whose intermediate does not wrap the same
// way.
//
// Equivalence domain, also Established: the only wrap is that 32-bit shift, so
// for -32768..32767 the result is bitwise identical to truncate-toward-zero of
// degrees × 65536 / 360 reduced modulo the circle — negative degrees included,
// with no bias. Divergence from the unbounded formula starts at 32768 and at
// -32769, where the shift runs into the sign bit. Stock content authors no
// angle outside 0..359 in any of the reference install's 57,485 mission unit
// blocks (WU-19-167 census), so the whole divergence domain is unreachable.
func HeadingFromDegrees(deg int32) uint16 {
	scaled := int32(int64(deg) * 65536) // 32-bit shift, wrap included
	// The magic and its shift are the compiler's reciprocal for 360 with the
	// multiplicand added back, which is what the high-half add below is.
	const recip = int64(0xB60B60B7)
	quotient := (int64(scaled) * recip) >> 40
	if scaled < 0 {
		quotient++ // the sign-bit add: truncate toward zero, not floor
	}
	return uint16(int16(quotient))
}

// FixedFromPixels converts authored pixel coordinates to 16.16 fixed by
// shifting left 16. [GAP T14] [02 "Map files"]
func FixedFromPixels(pixels int32) int32 { return pixels << 16 }

// --- Binary 36/12/136 identity helpers ---
//
// SYNTHETIC round-trip codecs, not retail wire formats: no retail file ever
// stores these records, so the offsets below are this codec's own and are
// deliberately NOT the executable's field identities (I13). The 36/12/136
// byte sizes remain record identity for contract cross-references.

// MarshalUnit encodes a UnitPlacement into a 36-byte record plus
// a tail heap. Name pointers are offsets into the heap. X/Z/Y are fixed
// dwords <<16, Angle is heading, flags packed. [GAP T14] [02 "Map files"] [C6]
// Synthetic codec — see the block comment above; offsets are not retail's.
func MarshalUnit(u UnitPlacement, heap *[]byte) [36]byte {
	var rec [36]byte
	heapBase := len(*heap)
	// Append strings to heap with NUL terminator to mimic retail tail heap.
	unitOff := appendHeapString(heap, u.UnitName)
	identOff := appendHeapString(heap, u.Ident)
	missionOff := appendHeapString(heap, u.InitialMission)
	binary.LittleEndian.PutUint32(rec[0:4], uint32(heapBase+unitOff))
	binary.LittleEndian.PutUint32(rec[4:8], uint32(heapBase+identOff))
	binary.LittleEndian.PutUint32(rec[8:12], uint32(heapBase+missionOff))
	binary.LittleEndian.PutUint32(rec[12:16], uint32(u.X))
	binary.LittleEndian.PutUint32(rec[16:20], uint32(u.Z))
	binary.LittleEndian.PutUint32(rec[20:24], uint32(u.Y))
	binary.LittleEndian.PutUint16(rec[24:26], u.Angle)
	binary.LittleEndian.PutUint16(rec[26:28], uint16(u.Player))
	binary.LittleEndian.PutUint16(rec[28:30], uint16(u.HealthPercentage))
	binary.LittleEndian.PutUint16(rec[30:32], uint16(u.BuildPriority))
	binary.LittleEndian.PutUint16(rec[32:34], uint16(u.CreationCountdown))
	rec[34] = u.RawFlags
	// Low nibble of InitialGroup if numeric, else 0.
	var nibble byte
	if v, err := strconv.Atoi(strings.TrimSpace(u.InitialGroup)); err == nil {
		nibble = byte(v & 0x0F)
	}
	rec[35] = nibble
	return rec
}

// UnmarshalUnit decodes a 36-byte unit record plus heap into a UnitPlacement.
// It reverses MarshalUnit's heap pointer encoding.
func UnmarshalUnit(rec [36]byte, heap []byte) UnitPlacement {
	var u UnitPlacement
	unitOff := binary.LittleEndian.Uint32(rec[0:4])
	identOff := binary.LittleEndian.Uint32(rec[4:8])
	missionOff := binary.LittleEndian.Uint32(rec[8:12])
	u.UnitName = readHeapString(heap, int(unitOff))
	u.Ident = readHeapString(heap, int(identOff))
	u.InitialMission = readHeapString(heap, int(missionOff))
	u.X = int32(binary.LittleEndian.Uint32(rec[12:16]))
	u.Z = int32(binary.LittleEndian.Uint32(rec[16:20]))
	u.Y = int32(binary.LittleEndian.Uint32(rec[20:24]))
	u.Angle = binary.LittleEndian.Uint16(rec[24:26])
	u.Player = int32(int16(binary.LittleEndian.Uint16(rec[26:28])))
	u.HealthPercentage = int32(int16(binary.LittleEndian.Uint16(rec[28:30])))
	u.BuildPriority = int32(int16(binary.LittleEndian.Uint16(rec[30:32])))
	u.CreationCountdown = int32(int16(binary.LittleEndian.Uint16(rec[32:34])))
	u.RawFlags = rec[34]
	u.Immune = IsImmuneFromFlags(u.RawFlags)
	u.AiIgnore = u.RawFlags&(1<<5) != 0
	u.AiPriorityTarget = u.RawFlags&(1<<6) != 0
	u.MissionCriticalUnit = u.RawFlags&(1<<0) != 0
	nib := rec[35] & 0x0F
	if nib != 0 {
		u.InitialGroup = strconv.Itoa(int(nib))
	}
	return u
}

// MarshalSpecial encodes a Special into the 12-byte retail identity.
// [GAP T14] [02 "Map files"] [C6]
func MarshalSpecial(s Special) [12]byte {
	var rec [12]byte
	binary.LittleEndian.PutUint32(rec[0:4], uint32(s.Kind))
	binary.LittleEndian.PutUint32(rec[4:8], uint32(s.ID))
	binary.LittleEndian.PutUint16(rec[8:10], uint16(s.X))
	binary.LittleEndian.PutUint16(rec[10:12], uint16(s.Z))
	return rec
}

// UnmarshalSpecial decodes a 12-byte special record.
func UnmarshalSpecial(rec [12]byte) Special {
	return Special{
		Kind: int32(binary.LittleEndian.Uint32(rec[0:4])),
		ID:   int32(binary.LittleEndian.Uint32(rec[4:8])),
		X:    int16(binary.LittleEndian.Uint16(rec[8:10])),
		Z:    int16(binary.LittleEndian.Uint16(rec[10:12])),
	}
}

// MarshalFeature encodes a FeaturePlacement into the 136-byte retail identity.
// Featurename is a 128-byte buffer; X/Z follow. Negative coordinates were
// already cleared to -1 by decode. [GAP T14] [02 "Map files"] [C6]
func MarshalFeature(f FeaturePlacement) [136]byte {
	var rec [136]byte
	// 128-byte name buffer, null-padded [GAP T14]
	copy(rec[0:128], f.Name)
	binary.LittleEndian.PutUint32(rec[128:132], uint32(f.X))
	binary.LittleEndian.PutUint32(rec[132:136], uint32(f.Z))
	return rec
}

// UnmarshalFeature decodes a 136-byte feature record.
func UnmarshalFeature(rec [136]byte) FeaturePlacement {
	name := string(rec[0:128])
	// Trim at first NUL as retail buffer is nul-terminated.
	if idx := strings.IndexByte(name, 0); idx >= 0 {
		name = name[:idx]
	}
	// Trim spaces as TDF does? Keep as raw.
	name = strings.TrimSpace(name)
	x := int32(binary.LittleEndian.Uint32(rec[128:132]))
	z := int32(binary.LittleEndian.Uint32(rec[132:136]))
	return FeaturePlacement{
		Name: name,
		X:    x,
		Z:    z,
		RawX: x,
		RawZ: z,
	}
}

func appendHeapString(heap *[]byte, s string) int {
	off := len(*heap)
	*heap = append(*heap, s...)
	*heap = append(*heap, 0)
	return off
}

func readHeapString(heap []byte, off int) string {
	if off < 0 || off >= len(heap) {
		return ""
	}
	end := off
	for end < len(heap) && heap[end] != 0 {
		end++
	}
	return string(heap[off:end])
}
