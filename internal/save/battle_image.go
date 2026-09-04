package save

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// BattleImage is a detached, wire-validated save image. It contains no
// pointers into a session, catalog, terrain, or bank-owned byte slice. The
// session loader owns catalog resolution, map validation, fix-up, and commit
// [08 "Load process"] [08 "Unit and script records"].
type BattleImage struct {
	Summary     Summary
	Camera      Camera
	Scheduler   [28]byte
	HumanPlayer int32
	// Players carries each active slot's scalars and, as the last item of
	// that slot's own account, its eleven-byte alliance row. The box is per
	// `Player%i`; the `Players` account holds only `Human Player` and the
	// 28-byte `GameTime` box [08 "Player records"].
	Players []PlayerSlot

	Units          UnitImage
	Features       FeatureImage
	Metal          []byte
	PlayerFeatures []byte
	Mapping        []byte
	Meteor         MeteorScalars
	Triggers       []RawAccount
}

// RawBox is an opaque binary item retained for a later typed commit pass.
type RawBox struct {
	Name   string
	Number int32
	Data   []byte
}

// RawAccount retains account data whose semantic consumer belongs to a later
// subsystem. All slices are detached from the Bank.
type RawAccount struct {
	Name    string
	Ints    []IntItem
	Doubles []DoubleItem
	Strings []StringItem
	Boxes   []RawBox
}

// UnitRecord is one numbered Units record. An alternate-length record is retained
// as raw bytes but has no stable identity [R-SAVE-UNIT-01].
type UnitRecord struct {
	Number   int
	StableID uint16
	Compat   bool
	Data     []byte
}

// OrderRecord retains the exact main and optional subtype boxes. Descriptor
// and build names are side-channel strings; their catalog interpretation is
// deliberately deferred [R-SAVE-ORDER-01].
type OrderRecord struct {
	ParentStableID uint16
	Sequence       uint32
	Secondary      bool
	Main           []byte
	SubtypeCode    uint32
	Subtype        []byte
	DescriptorName string
	BuildTypeName  string
}

// ScriptRecord is an opaque per-enumeration script box. Its fixed snapshot
// prefix is established; stack and piece meanings belong to the COB pass.
type ScriptRecord struct {
	Index int
	Data  []byte
}

// UnitImage is the detached Units account image.
type UnitImage struct {
	Version   int32
	Records   []UnitRecord
	Orders    []OrderRecord
	Scripts   []ScriptRecord
	TypeNames []StringItem
	Other     []RawBox
}

// FeatureRecord retains one exact-size feature family record. X, Z, and
// TypeID are the only fields interpreted here because they are the established
// wire-level discriminator fields [R-SAVE-FEATURE-01].
type FeatureRecord struct {
	X, Z   uint16
	TypeID uint16
	Data   []byte
}

// FeatureImage is the detached Features account image.
type FeatureImage struct {
	HasTypeNames bool
	TypeNames    []string
	Normal       []FeatureRecord
	Animating    []FeatureRecord
	ThreeD       []FeatureRecord
}

const (
	featureNormalBox    = "Normal Features"
	featureAnimatingBox = "Animating Features"
	feature3DBox        = "3D Features"
	maxWireUnitRecords  = int(^uint16(0))
)

// DecodeBattleImage validates and deep-copies a standard battle save into a
// detached image. It intentionally performs no session, catalog, world, pool,
// RNG, projectile, path, effect, or presentation mutation. Catalog-dependent
// definition lookup, map bounds/footprints, ownership slices, script fix-up,
// trigger semantics, and world commit remain later validation steps [08
// "Load process"] [R-SAVE-FEATURE-01].
func DecodeBattleImage(bank *Bank) (*BattleImage, error) {
	if bank == nil {
		return nil, battleImageError("nil bank", "HAPIBANK battle account")
	}

	var image BattleImage
	if err := decodeSummary(bank, &image); err != nil {
		return nil, err
	}
	if err := decodeCamera(bank, &image); err != nil {
		return nil, err
	}
	if err := decodePlayers(bank, &image); err != nil {
		return nil, err
	}
	if err := decodeUnits(bank, &image.Units); err != nil {
		return nil, err
	}
	if err := decodeFeatures(bank, &image.Features); err != nil {
		return nil, err
	}
	if err := decodeTerrain(bank, &image); err != nil {
		return nil, err
	}
	if err := decodeMeteor(bank, &image.Meteor); err != nil {
		return nil, err
	}
	decodeTriggers(bank, &image.Triggers)
	return &image, nil
}

func battleImageError(what, expected string) error {
	return fmt.Errorf("nanolathe: decode battle image: %s: logical path save/battle, providers searched [bank], expected %s", what, expected)
}

func decodeSummary(bank *Bank, image *BattleImage) error {
	ac, ok := bank.Account(SummaryAccount)
	if !ok {
		return battleImageError("missing Summary account", "Summary account")
	}
	s, _ := ReadSummary(bank)
	if value, present := ac.Int("Gametype"); !present || (value != 1 && value != 2) {
		return battleImageError("invalid Summary Gametype", "integer Gametype equal to 1 or 2")
	}
	if value, present := ac.Int("BetweenMissions"); present && value != 0 {
		return battleImageError("non-battle Summary image", "BetweenMissions absent or zero")
	}
	image.Summary = s
	image.Summary.RadarImage = append([]byte(nil), s.RadarImage...)
	return nil
}

func decodeCamera(bank *Bank, image *BattleImage) error {
	ac, ok := bank.Account(CameraAccount)
	if !ok {
		return battleImageError("missing Camera account", "Camera account")
	}
	if _, ok := ac.Int("X Position"); !ok {
		return battleImageError("missing Camera X Position", "Camera X Position integer")
	}
	if _, ok := ac.Int("Z Position"); !ok {
		return battleImageError("missing Camera Z Position", "Camera Z Position integer")
	}
	image.Camera, _ = ReadCamera(bank)
	return nil
}

func decodePlayers(bank *Bank, image *BattleImage) error {
	ac, ok := bank.Account(PlayersAccount)
	if !ok {
		return battleImageError("missing Players account", "Players account")
	}
	gameTime, ok := ac.BoxData(GameTimeBoxName, 0)
	if !ok || len(gameTime) < 28 {
		return battleImageError("short Players GameTime box", "at least 28 bytes")
	}
	copy(image.Scheduler[:], gameTime[:28])
	if meta, present := ReadPlayersMeta(bank); present {
		image.HumanPlayer = meta.HumanPlayer
	}
	// Player account fields already have an established typed reader. Missing
	// Player%i accounts are left for battle-entry defaults; this decoder does
	// not infer active ownership from Summary.Players [08 "Player records"].
	for i := 0; i < 10; i++ {
		player, present := ReadPlayerSlot(bank, i)
		if !present {
			continue
		}
		// Each account's own Alliances box is the last item it carries, and
		// it must be exactly 11 bytes [08 "Player records"]. Retail simply
		// declines to load a box of any other size; this decoder stages a
		// whole bank before touching live state, so a present-but-mis-sized
		// box is a malformed image rather than a silent skip.
		if pac, ok := bank.Account(playerAccountName(i)); ok {
			if alliances, ok := pac.BoxData(AlliancesBoxName, 0); ok && len(alliances) != 11 {
				return battleImageError(fmt.Sprintf("invalid Player%d Alliances box", i), "exactly 11 bytes")
			}
		}
		image.Players = append(image.Players, player)
	}
	return nil
}

func decodeUnits(bank *Bank, image *UnitImage) error {
	ac, ok := bank.Account(UnitsAccount)
	if !ok {
		return battleImageError("missing Units account", "Units account")
	}
	version, count, present := ReadUnitsHeader(bank)
	if !present {
		return battleImageError("missing Units account", "Units account")
	}
	// Empty Units accounts produced by retail may omit both header scalars.
	// Treat that exact empty form as an empty, loadable account; a nonempty
	// account still requires Version 0x11 [08 R-SAVE-02 §6].
	if _, versionPresent := ac.Int("Version"); !versionPresent {
		if _, countPresent := ac.Int("Number of Units"); !countPresent && len(ac.Boxes) == 0 {
			version, count = UnitsVersionRetail, 0
		}
	}
	if version != UnitsVersionRetail {
		return battleImageError("invalid Units version", "integer Version equal to 0x11")
	}
	if count < 0 || int64(count) > int64(maxWireUnitRecords) {
		return battleImageError("invalid Units count", "nonnegative count representable by stable unit IDs")
	}
	image.Version = version

	byNumber := make(map[int32]*Box, count)
	for _, box := range ac.Boxes {
		if box.Name == "" {
			if box.Number < 0 || int64(box.Number) >= int64(count) {
				return battleImageError("unit box number outside declared count", "numbered unit boxes 0 through Number of Units-1")
			}
			if _, exists := byNumber[box.Number]; exists {
				return battleImageError("duplicate numbered unit box", "one unit box per declared number")
			}
			byNumber[box.Number] = box
		}
	}
	if len(byNumber) != int(count) {
		return battleImageError("unit box count does not match Number of Units", "one numbered unit record for every declared unit")
	}
	for i := 0; i < int(count); i++ {
		box := byNumber[int32(i)]
		if !ValidateUnitBoxSize(len(box.Data)) {
			return battleImageError("invalid unit record length", "0xB8 or 0xB6 bytes")
		}
		record := UnitRecord{Number: i, Compat: len(box.Data) == UnitBoxCompatSize, Data: cloneBytes(box.Data)}
		if !record.Compat {
			record.StableID = UnitStableID(record.Data)
		}
		image.Records = append(image.Records, record)
	}

	ids := make(map[uint16]struct{}, len(image.Records))
	for _, record := range image.Records {
		if record.Compat {
			continue
		}
		if record.StableID == 0 {
			return battleImageError("zero unit stable ID", "nonzero stable ID for every standard unit record")
		}
		if _, exists := ids[record.StableID]; exists {
			return battleImageError("duplicate unit stable ID", "unique nonzero stable IDs")
		}
		ids[record.StableID] = struct{}{}
	}
	for _, record := range image.Records {
		if record.Compat {
			continue
		}
		for _, offset := range []int{0x89, 0x8B} {
			ref := binary.LittleEndian.Uint16(record.Data[offset:])
			if ref != 0 {
				if _, exists := ids[ref]; !exists {
					return battleImageError("unit reference outside staged IDs", "zero or a declared unit stable ID")
				}
			}
		}
	}
	if err := validateUnitReferenceGraph(image.Records, ids); err != nil {
		return err
	}

	for _, item := range ac.Strings {
		if strings.HasPrefix(item.Name, "UTYPENAME") {
			image.TypeNames = append(image.TypeNames, StringItem{Name: item.Name, Value: item.Value})
		}
	}
	for _, box := range ac.Boxes {
		if box.Name == "" || isOrderBoxName(box.Name) || strings.HasSuffix(box.Name, "g") || strings.HasSuffix(box.Name, "_name") || strings.HasPrefix(box.Name, "Script") {
			continue
		}
		image.Other = append(image.Other, cloneBox(box))
	}
	if err := decodeOrders(ac, image); err != nil {
		return err
	}
	if err := decodeScripts(ac, image); err != nil {
		return err
	}
	return nil
}

func decodeOrders(ac *Account, image *UnitImage) error {
	ids := make(map[uint16]struct{}, len(image.Records))
	counts := make(map[uint16]uint32, len(image.Records))
	for _, record := range image.Records {
		if record.StableID != 0 {
			ids[record.StableID] = struct{}{}
		}
		if !record.Compat {
			counts[record.StableID] = binary.LittleEndian.Uint32(record.Data[0x23:])
		}
	}
	orders := make([]OrderRecord, 0)
	seen := make(map[uint16]map[uint32]struct{})
	for _, box := range ac.Boxes {
		parent, sequence, ok := parseOrderBoxName(box.Name)
		if !ok {
			continue
		}
		if _, exists := ids[parent]; !exists {
			return battleImageError("order parent outside staged IDs", "a declared unit stable ID")
		}
		if len(box.Data) != OrderBoxSize {
			return battleImageError("invalid order record length", "exactly 0x3A bytes")
		}
		main := cloneBytes(box.Data)
		if binary.LittleEndian.Uint16(main[0:]) != parent {
			return battleImageError("order parent field disagrees with box name", "matching parent stable ID")
		}
		if link := binary.LittleEndian.Uint16(main[2:]); link != 0 {
			if _, exists := ids[link]; !exists {
				return battleImageError("order link outside staged IDs", "zero or a declared unit stable ID")
			}
		}
		if seen[parent] == nil {
			seen[parent] = make(map[uint32]struct{})
		}
		if _, exists := seen[parent][sequence]; exists {
			return battleImageError("duplicate order sequence", "one order per parent and sequence")
		}
		seen[parent][sequence] = struct{}{}
		code := binary.LittleEndian.Uint32(main[4:])
		subtype, present := ac.BoxData(box.Name+"g", 0)
		if code == 0 && present {
			return battleImageError("subtype present for zero order code", "matching main order subtype code")
		}
		if code != 0 {
			if !present {
				return battleImageError("missing order subtype box", "subtype box paired with nonzero order code")
			}
			if code < 2 || code > 6 {
				return battleImageError("unsupported order subtype code", "established nonzero subtype code 2 through 6")
			}
			if !ValidateSubtypeSize(int(code), len(subtype)) {
				return battleImageError("invalid order subtype length", "the established length for subtype code")
			}
			if code == 2 {
				for _, offset := range []int{8, 0x1A} {
					if err := validateUnitRef(ids, binary.LittleEndian.Uint16(subtype[offset:])); err != nil {
						return err
					}
				}
			} else if code == 3 {
				if err := validateUnitRef(ids, binary.LittleEndian.Uint16(subtype[8:])); err != nil {
					return err
				}
			}
		}
		name, _ := ac.Str(box.Name + "_name")
		definitionIndex := binary.LittleEndian.Uint32(main[0x26:])
		buildName := findUnitTypeName(ac.Strings, definitionIndex)
		orders = append(orders, OrderRecord{
			ParentStableID: parent,
			Sequence:       sequence,
			Secondary:      binary.LittleEndian.Uint32(main[0x32:])&0x40000 != 0,
			Main:           main,
			SubtypeCode:    code,
			Subtype:        cloneBytes(subtype),
			DescriptorName: name,
			BuildTypeName:  buildName,
		})
	}
	for _, record := range image.Records {
		if record.StableID == 0 || record.Compat {
			continue
		}
		want := binary.LittleEndian.Uint32(record.Data[0x23:])
		if uint64(want) != uint64(len(seen[record.StableID])) {
			return battleImageError("order count does not match unit record", "Number of Units order count and order boxes")
		}
		if want > 0 {
			for sequence := uint32(0); sequence < want; sequence++ {
				if _, ok := seen[record.StableID][sequence]; !ok {
					return battleImageError("order sequence has a gap", "contiguous sequence numbers starting at zero")
				}
			}
		}
	}
	sort.SliceStable(orders, func(i, j int) bool {
		if orders[i].ParentStableID != orders[j].ParentStableID {
			return orders[i].ParentStableID < orders[j].ParentStableID
		}
		return orders[i].Sequence < orders[j].Sequence
	})
	image.Orders = orders
	return nil
}

func decodeScripts(ac *Account, image *UnitImage) error {
	seen := make(map[int]struct{})
	for _, box := range ac.Boxes {
		if !strings.HasPrefix(box.Name, "Script") {
			continue
		}
		index, err := strconv.Atoi(strings.TrimPrefix(box.Name, "Script"))
		if err != nil || index < 0 {
			return battleImageError("invalid script box name", "nonnegative Script enumeration index")
		}
		if _, exists := seen[index]; exists {
			return battleImageError("duplicate script box index", "one Script box per enumeration index")
		}
		seen[index] = struct{}{}
		if len(box.Data) < ScriptSnapshotSize {
			return battleImageError("short script box", "at least the established 0x528-byte snapshot")
		}
		image.Scripts = append(image.Scripts, ScriptRecord{Index: index, Data: cloneBytes(box.Data)})
	}
	sort.SliceStable(image.Scripts, func(i, j int) bool { return image.Scripts[i].Index < image.Scripts[j].Index })
	return nil
}

func validateUnitReferenceGraph(records []UnitRecord, ids map[uint16]struct{}) error {
	graph := make(map[uint16][]uint16, len(ids))
	for _, record := range records {
		if record.Compat {
			continue
		}
		refs := make([]uint16, 0, 2)
		for _, offset := range []int{0x89, 0x8B} {
			if ref := binary.LittleEndian.Uint16(record.Data[offset:]); ref != 0 {
				refs = append(refs, ref)
			}
		}
		graph[record.StableID] = refs
	}
	const (
		unvisited = uint8(iota)
		visiting
		visited
	)
	state := make(map[uint16]uint8, len(ids))
	var visit func(uint16) error
	visit = func(id uint16) error {
		switch state[id] {
		case visiting:
			return battleImageError("cyclic unit reference", "acyclic established cross-unit references")
		case visited:
			return nil
		}
		state[id] = visiting
		for _, ref := range graph[id] {
			if err := visit(ref); err != nil {
				return err
			}
		}
		state[id] = visited
		return nil
	}
	for id := range ids {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

func decodeFeatures(bank *Bank, image *FeatureImage) error {
	ac, ok := bank.Account(FeaturesAccount)
	if !ok {
		return battleImageError("missing Features account", "Features account")
	}
	names, present := ac.BoxData(FeatureTypeNamesBox, 0)
	image.HasTypeNames = present
	if present && !ValidateFeatureTypeNamesSize(len(names)) {
		return battleImageError("invalid Feature Type Names box", "present complete 0x80-byte name entries")
	}
	if present {
		for start := 0; start < len(names); start += FeatureTypeName {
			name := names[start : start+FeatureTypeName]
			if end := bytes.IndexByte(name, 0); end >= 0 {
				name = name[:end]
			}
			image.TypeNames = append(image.TypeNames, string(name))
		}
	}
	anchors := make(map[[2]uint16]struct{})
	counts := []struct {
		name string
		size int
		dst  *[]FeatureRecord
	}{
		{"Number of Normal Features", 8, &image.Normal},
		{"Number of Animating Features", 10, &image.Animating},
		{"Number of 3D Features", 26, &image.ThreeD},
	}
	for _, family := range counts {
		count, exists := ac.Int(family.name)
		if !exists || count < 0 {
			return battleImageError("invalid feature family count", "nonnegative count item for each feature family")
		}
		boxName := map[string]string{
			"Number of Normal Features":    featureNormalBox,
			"Number of Animating Features": featureAnimatingBox,
			"Number of 3D Features":        feature3DBox,
		}[family.name]
		data, boxPresent := ac.BoxData(boxName, 0)
		if count == 0 {
			if boxPresent && len(data) != 0 {
				return battleImageError("nonempty zero-count feature box", "zero bytes")
			}
			continue
		}
		var previous [2]uint16
		havePrevious := false
		if !boxPresent || int64(count) > int64(math.MaxInt/family.size) || len(data) != int(count)*family.size {
			return battleImageError("feature family size does not match count", "count multiplied by its exact record size")
		}
		for start := 0; start < len(data); start += family.size {
			record := cloneBytes(data[start : start+family.size])
			decoded := FeatureRecord{X: binary.LittleEndian.Uint16(record[0:]), Z: binary.LittleEndian.Uint16(record[2:]), TypeID: binary.LittleEndian.Uint16(record[4:]), Data: record}
			anchor := [2]uint16{decoded.Z, decoded.X}
			if havePrevious && (decoded.Z < previous[0] || (decoded.Z == previous[0] && decoded.X < previous[1])) {
				return battleImageError("feature family order is not row-major", "nondecreasing (z,x) record anchors")
			}
			if _, exists := anchors[anchor]; exists {
				return battleImageError("duplicate feature anchor", "unique (x,z) anchor across feature families")
			}
			anchors[anchor] = struct{}{}
			previous = anchor
			havePrevious = true
			if image.HasTypeNames && int(decoded.TypeID) >= len(image.TypeNames) {
				return battleImageError("feature type ordinal outside saved name table", "a declared Feature Type Names ordinal")
			}
			*family.dst = append(*family.dst, decoded)
		}
	}
	return nil
}

func decodeTerrain(bank *Bank, image *BattleImage) error {
	for _, family := range []struct {
		account string
		box     string
		dst     *[]byte
	}{
		{account: "Metal", box: "Plotmap", dst: &image.Metal},
		{account: "PlayerFeatures", box: "Plotmap", dst: &image.PlayerFeatures},
		{account: "Mapping", box: "", dst: &image.Mapping},
	} {
		ac, ok := bank.Account(family.account)
		if !ok {
			return battleImageError("missing terrain account", family.account+" account")
		}
		data, ok := ac.BoxData(family.box, 0)
		if !ok {
			return battleImageError("missing terrain box", family.account+"/"+family.box)
		}
		*family.dst = cloneBytes(data)
	}
	return nil
}

func decodeMeteor(bank *Bank, meteor *MeteorScalars) error {
	// Meteor is optional. Missing or mistyped items are zero and therefore
	// disable/deactivate the scheduler [08 R-SAVE-02 §12].
	var value MeteorScalars
	if bank != nil {
		if ac, ok := bank.Account(MeteorAccount); ok {
			fields := [...]*int32{&value.Enabled, &value.Active, &value.NextStrikeTime, &value.TimeStrikeEnds, &value.NextHitTime, &value.OriginX, &value.OriginZ, &value.TargetX, &value.TargetZ}
			names := [...]string{"Enabled", "Active", "Next Strike Time", "Time Strike Ends", "Next Hit Time", "Origin X", "Origin Z", "Target X", "Target Z"}
			for i, name := range names {
				if v, present := ac.Int(name); present {
					*fields[i] = v
				}
			}
		}
	}
	*meteor = value
	return nil
}

func decodeTriggers(bank *Bank, out *[]RawAccount) {
	for _, account := range bank.Accounts() {
		if strings.HasPrefix(account.Name, "VictoryCondition_") || strings.HasPrefix(account.Name, "DefeatCondition_") {
			copy := RawAccount{Name: account.Name}
			copy.Ints = append([]IntItem(nil), account.Ints...)
			copy.Doubles = append([]DoubleItem(nil), account.Doubles...)
			for _, item := range account.Strings {
				copy.Strings = append(copy.Strings, StringItem{Name: item.Name, Value: item.Value})
			}
			for _, box := range account.Boxes {
				copy.Boxes = append(copy.Boxes, cloneBox(box))
			}
			*out = append(*out, copy)
		}
	}
}

func validateUnitRef(ids map[uint16]struct{}, ref uint16) error {
	if ref == 0 {
		return nil
	}
	if _, ok := ids[ref]; !ok {
		return battleImageError("order subtype reference outside staged IDs", "zero or a declared unit stable ID")
	}
	return nil
}

func parseOrderBoxName(name string) (uint16, uint32, bool) {
	if len(name) < 10 || name[0] != 'u' || name[5] != 'm' {
		return 0, 0, false
	}
	parent, err1 := strconv.ParseUint(name[1:5], 16, 16)
	sequence, err2 := strconv.ParseUint(name[6:], 16, 32)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return uint16(parent), uint32(sequence), true
}

func findUnitTypeName(items []StringItem, definitionIndex uint32) string {
	for _, item := range items {
		if !strings.HasPrefix(item.Name, "UTYPENAME") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(item.Name, "UTYPENAME"))
		index, err := strconv.ParseUint(value, 10, 32)
		if err == nil && uint32(index) == definitionIndex {
			return item.Value
		}
	}
	return ""
}

func isOrderBoxName(name string) bool {
	_, _, ok := parseOrderBoxName(name)
	return ok
}

func cloneBytes(data []byte) []byte { return append([]byte(nil), data...) }

func cloneBox(box *Box) RawBox {
	return RawBox{Name: box.Name, Number: box.Number, Data: cloneBytes(box.Data)}
}
