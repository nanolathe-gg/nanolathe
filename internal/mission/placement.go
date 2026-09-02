package mission

import (
	"encoding/binary"
	"math"
	"strconv"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
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

	Angle uint16 // heading 0..65535, trunc(degrees*65536/360) [GAP T14] [fmt ota]

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

	// Angle degrees conversion via retail fixed-point magic multiply
	// 0xB60B60B7>>40..., bitwise identical to trunc(deg*65536/360) for stock
	// but differs for negative/>360. [P0-06 §4][P0-04 §4][08 "Mission placement record"]
	if raw, ok := sec.FirstValue("Angle"); ok {
		trimmed := strings.TrimSpace(raw)
		if trimmed != "" {
			if f, err := strconv.ParseFloat(trimmed, 64); err == nil {
				// Try integer path for bitwise identical retail result.
				if isIntegerString(trimmed) {
					if deg, err2 := strconv.ParseInt(trimmed, 10, 32); err2 == nil {
						u.Angle = HeadingFromDegrees(int32(deg))
					} else {
						// Fallback to float trunc for out-of-range integer strings.
						heading := f * 65536.0 / 360.0
						t := math.Trunc(heading)
						v := int64(t) % 65536
						if v < 0 {
							v += 65536
						}
						u.Angle = uint16(v)
					}
				} else {
					// Fractional degrees: float trunc toward zero per __ftol [I3].
					heading := f * 65536.0 / 360.0
					t := math.Trunc(heading)
					v := int64(t) % 65536
					if v < 0 {
						v += 65536
					}
					u.Angle = uint16(v)
				}
			}
		}
	}

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

// DecodeSpecials decodes 12-byte special records from the selected schema's
// [specials] section. Kind 1 is StartPos; ID parsed from numeric suffix.
// [GAP T14] [02 "Map files"] [fmt ota]
func DecodeSpecials(schema *formats.Section) []Special {
	if schema == nil {
		return nil
	}
	specialsSec := schema.Section("specials")
	if specialsSec == nil {
		return nil
	}
	var out []Special
	for _, sec := range specialsSec.Sections() {
		s := decodeSpecialSection(sec)
		out = append(out, s)
	}
	return out
}

func decodeSpecialSection(sec *formats.Section) Special {
	var s Special
	sw, _ := sec.StringValue("specialwhat", "")
	s.Name = sw
	lower := strings.ToLower(strings.TrimSpace(sw))
	if strings.HasPrefix(lower, "startpos") {
		s.Kind = 1 // 1=StartPos [GAP T14]
	} else {
		s.Kind = 0
	}
	s.ID = parseSpecialID(sw) // [GAP T14] [02 "Map files"]
	s.X = int16(sec.IntValue("XPos", 0))
	s.Z = int16(sec.IntValue("ZPos", 0))
	return s
}

func parseSpecialID(specialwhat string) int32 {
	trimmed := strings.TrimSpace(specialwhat)
	if trimmed == "" {
		return 0
	}
	// Find trailing numeric suffix. Alphabetic suffixes distinguished from integer ones [02 "Map files"] [GAP T14].
	// If the trailing run is purely alphabetic, return 0 (distinguished).
	i := len(trimmed)
	for i > 0 && trimmed[i-1] >= '0' && trimmed[i-1] <= '9' {
		i--
	}
	if i == len(trimmed) {
		// No trailing digits -> alphabetic suffix or no id
		return 0
	}
	if i == 0 {
		// Entire string is digits (unlikely for specialwhat, but handle)
		val := formats.ParseTDFInteger(trimmed)
		return val
	}
	// There is a digit suffix starting at i; ensure the char before suffix is not a digit? Already.
	suffix := trimmed[i:]
	val := formats.ParseTDFInteger(suffix)
	return val
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

// DegreesToHeading converts authored degrees to retail heading 0..65535 via
// trunc(degrees*65536/360). Bitwise identical to retail magic for stock angles
// but differs for negative/>360 via the magic path. [P0-06 §4][P0-04 §4]
func DegreesToHeading(deg float64) uint16 {
	h := deg * 65536.0 / 360.0
	t := math.Trunc(h) // truncate toward zero [I3] __ftol
	v := int64(t) % 65536
	if v < 0 {
		v += 65536
	}
	return uint16(v)
}

// HeadingFromDegrees is the retail fixed-point magic multiply
// 0xB60B60B7>>40..., bitwise identical to trunc(deg*65536/360) for stock
// but differing for negative/>360 per [P0-06 §4][P0-04 §4].
// Input is integer degrees as authored in TDF Angle key.
func HeadingFromDegrees(deg int32) uint16 {
	// Retail's fixed-point magic-multiply heading conversion
	// [P0-04 §4][P0-06 §4][08 "Mission placement record"]:
	// scaled  = deg*0x10000 (32-bit wrap)
	// wide    = (int64)scaled * 0xB60B60B7
	// high    = wide >> 0x28 (40)
	// corr    = (short)(char)((scaled/0x1680000)+(scaled>>31)) >>0x0F
	// result  = (short)(high - corr)  then uint16
	// 0x1680000 = 360*65536
	tmp := int64(int32(int64(deg) * 0x10000))
	magic := int64(0xB60B60B7)
	hi := (tmp * magic) >> 40 // >>0x28 arithmetic
	// correction with 32-bit IDIV trunc toward zero
	div := int32(tmp) / 0x1680000
	sign := int32(tmp) >> 31
	sum := div + sign
	charVal := int8(sum)
	shortVal := int16(charVal)
	corr := int64(shortVal >> 15) // >>0x0F arithmetic
	res := int16(hi - corr)
	return uint16(res)
}

func isIntegerString(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if s[0] == '+' || s[0] == '-' {
		s = s[1:]
	}
	if s == "" {
		return false
	}
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
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
