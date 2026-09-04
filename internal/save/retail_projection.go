package save

import (
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
)

// RetailProjection is the detached, writer-facing view of established retail
// save state. It deliberately contains values and copied byte slices only;
// session pointers, catalog objects, and presentation caches do not cross this
// boundary [08 "Save-file organization"] [08 R-ENTRY-02 §3].
//
// A projection with Summary.BetweenMissions == 1 is the retail continuation
// form. Its writer emits Summary only. A live-battle projection emits the
// remaining accounts in the fixed writer order below.
type RetailProjection struct {
	Summary Summary
	Camera  Camera

	HumanPlayer int32
	Scheduler   [28]byte
	// Each PlayerSlot carries its own alliance row; there is no projection-
	// level row, because the box is emitted inside `Player%i` [08 "Player
	// records"].
	Players []PlayerSlot

	Units    UnitImage
	Features FeatureImage

	Metal          []byte
	PlayerFeatures []byte
	Mapping        []byte

	Meteor   MeteorScalars
	Triggers []RawAccount
}

// RetailProjectionFromBattleImage detaches an already decoded battle image
// for rewriting. The extra copy is intentional: callers may reuse or mutate
// the decoded image after this call without changing the writer input.
func RetailProjectionFromBattleImage(image *BattleImage) (RetailProjection, error) {
	if image == nil {
		return RetailProjection{}, fmt.Errorf("nanolathe: retail projection: nil battle image: logical path save/projection, providers searched [battle image], expected detached image")
	}
	return RetailProjection{
		Summary:        cloneSummary(image.Summary),
		Camera:         image.Camera,
		HumanPlayer:    image.HumanPlayer,
		Scheduler:      image.Scheduler,
		Players:        clonePlayers(image.Players),
		Units:          cloneUnitImage(image.Units),
		Features:       cloneFeatureImage(image.Features),
		Metal:          cloneBytes(image.Metal),
		PlayerFeatures: cloneBytes(image.PlayerFeatures),
		Mapping:        cloneBytes(image.Mapping),
		Meteor:         image.Meteor,
		Triggers:       cloneAccounts(image.Triggers),
	}, nil
}

// Clone returns a detached copy of p.
func (p RetailProjection) Clone() RetailProjection {
	q, err := RetailProjectionFromBattleImage(&BattleImage{
		Summary: p.Summary, Camera: p.Camera, Scheduler: p.Scheduler,
		HumanPlayer: p.HumanPlayer, Players: p.Players, Units: p.Units,
		Features: p.Features, Metal: p.Metal, PlayerFeatures: p.PlayerFeatures,
		Mapping: p.Mapping, Meteor: p.Meteor, Triggers: p.Triggers,
	})
	if err != nil {
		// p is a value and therefore cannot be nil. Keep Clone total even if the
		// constructor gains a future validation rule.
		return p
	}
	return q
}

// Build lays out p into the neutral Builder using established typed and raw
// encoders. Account creation order is Summary, Camera, Players/Player%i,
// Features, Metal, PlayerFeatures, Mapping, Units, Meteor, then trigger accounts;
// this is the writer's stable live-battle order [08 "Save-file organization"].
func (p RetailProjection) Build() (*Builder, error) {
	b := NewBuilder()
	summary := p.Summary
	if summary.BetweenMissions != 0 && summary.BetweenMissions != 1 {
		return nil, fmt.Errorf("nanolathe: retail projection: invalid BetweenMissions value %d: logical path save/Summary, providers searched [projection], expected integer 0 or 1", summary.BetweenMissions)
	}
	// BetweenMissions == 1 is the sole route discriminator. IsBattle is
	// presentation metadata and must not silently select continuation.
	if summary.BetweenMissions == 0 {
		summary.IsBattle = true
	}
	WriteSummary(b, summary)
	if summary.BetweenMissions == 1 {
		// Retail continuation saves contain Summary only. Do not leak an
		// accidentally populated detached battle family into this route.
		return b, nil
	}

	WriteCamera(b, p.Camera)
	players := builderAccount(b, PlayersAccount)
	players.SetInt("Human Player", p.HumanPlayer)
	players.AppendBox(GameTimeBoxName, 0, p.Scheduler[:])
	// The `Players` account carries `Human Player` and the 28-byte `GameTime`
	// box and nothing else; each slot's alliance row is written inside its own
	// `Player%i` account by WritePlayerSlot [08 "Player records"].
	for _, slot := range sortedPlayers(p.Players) {
		WritePlayerSlot(b, slot)
	}

	if err := writeFeatureImage(b, p.Features); err != nil {
		return nil, err
	}
	writeTerrainBox(b, "Metal", "Plotmap", p.Metal)
	writeTerrainBox(b, "PlayerFeatures", "Plotmap", p.PlayerFeatures)
	writeTerrainBox(b, "Mapping", "", p.Mapping)
	if err := writeUnitImage(b, p.Units); err != nil {
		return nil, err
	}
	// Meteor is an unconditional live-battle account; absent runtime state is
	// represented by its nine established zero-valued integer items.
	WriteMeteorScalars(b, p.Meteor)
	for _, account := range p.Triggers {
		writeRawAccount(b, account)
	}
	return b, nil
}

// Bytes returns the exact HAPIBANK image for p.
func (p RetailProjection) Bytes() ([]byte, error) {
	b, err := p.Build()
	if err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// WriteRetailProjection is the package-level writer entry point.
func WriteRetailProjection(p RetailProjection) ([]byte, error) { return p.Bytes() }

func writeUnitImage(b *Builder, image UnitImage) error {
	ac := builderAccount(b, UnitsAccount)
	if len(image.Records) == 0 && (len(image.TypeNames) != 0 || len(image.Orders) != 0 || len(image.Scripts) != 0 || len(image.Other) != 0) {
		return fmt.Errorf("nanolathe: retail projection: empty Units image has per-unit side data: logical path save/Units, providers searched [projection], expected no type names, orders, scripts, or auxiliary boxes")
	}
	for _, item := range image.TypeNames {
		ac.SetString(item.Name, item.Value)
	}

	// Index the detached side boxes without iterating the index. The record
	// slice remains the caller-supplied retail emission order (reverse pool
	// traversal); maps are used only for keyed validation/lookups [I1].
	accessories := make(map[uint16]RawBox, len(image.Other))
	movers := make(map[uint16]RawBox, len(image.Other))
	for _, box := range image.Other {
		if box.Name == "" {
			return fmt.Errorf("nanolathe: retail projection: unsupported numbered Units box: logical path save/Units, providers searched [projection], expected per-unit accessory or mover box")
		}
		id, kind, ok := parseUnitAuxBoxName(box.Name)
		if !ok {
			return fmt.Errorf("nanolathe: retail projection: unsupported Units box %q: logical path save/Units, providers searched [projection], expected u%%04xacc or u%%04xmob", box.Name)
		}
		if box.Name != fmt.Sprintf("u%04x%s", id, kind) {
			return fmt.Errorf("nanolathe: retail projection: noncanonical Units box %q: logical path save/Units, providers searched [projection], expected u%04x%s", box.Name, id, kind)
		}
		if box.Number != 0 {
			return fmt.Errorf("nanolathe: retail projection: Units box %q has number %d: logical path save/Units, providers searched [projection], expected named box number 0", box.Name, box.Number)
		}
		dst := accessories
		if kind == "mob" {
			dst = movers
		}
		if _, exists := dst[id]; exists {
			return fmt.Errorf("nanolathe: retail projection: duplicate %s box for unit %04x: logical path save/Units, providers searched [projection], expected one box", kind, id)
		}
		dst[id] = box
	}
	orders := make(map[uint16][]OrderRecord, len(image.Records))
	for _, order := range image.Orders {
		orders[order.ParentStableID] = append(orders[order.ParentStableID], order)
	}
	seenIDs := make(map[uint16]struct{}, len(image.Records))
	scripts := make(map[int]ScriptRecord, len(image.Scripts))
	for _, script := range image.Scripts {
		if script.Index < 0 || script.Index >= len(image.Records) {
			return fmt.Errorf("nanolathe: retail projection: Script%d has no emitted unit: logical path save/Units, providers searched [projection], expected enumeration index", script.Index)
		}
		if len(script.Data) < ScriptSnapshotSize {
			return fmt.Errorf("nanolathe: retail projection: Script%d has size %d: logical path save/Units, providers searched [projection], expected at least 0x528-byte script snapshot", script.Index, len(script.Data))
		}
		if _, exists := scripts[script.Index]; exists {
			return fmt.Errorf("nanolathe: retail projection: duplicate Script%d: logical path save/Units, providers searched [projection], expected one script box", script.Index)
		}
		scripts[script.Index] = script
	}

	for i, record := range image.Records {
		if record.Compat || len(record.Data) != UnitBoxSize {
			return fmt.Errorf("nanolathe: retail projection: unit %d has nonstandard record size %d: logical path save/Units, providers searched [projection], expected live 0xB8 record", i, len(record.Data))
		}
		id := UnitStableID(record.Data)
		if id == 0 {
			return fmt.Errorf("nanolathe: retail projection: unit %d has zero stable ID: logical path save/Units, providers searched [projection], expected nonzero stable ID", i)
		}
		if record.StableID != 0 && record.StableID != id {
			return fmt.Errorf("nanolathe: retail projection: unit %d stable ID disagrees with record: logical path save/Units, providers searched [projection], expected matching stable ID", i)
		}
		if _, exists := seenIDs[id]; exists {
			return fmt.Errorf("nanolathe: retail projection: duplicate unit stable ID %04x: logical path save/Units, providers searched [projection], expected unique IDs", id)
		}
		seenIDs[id] = struct{}{}
		script, exists := scripts[i]
		if !exists {
			return fmt.Errorf("nanolathe: retail projection: unit %d has no Script%d: logical path save/Units, providers searched [projection], expected exactly one bound script", i, i)
		}
		ac.AppendBox(ScriptBoxName(i), 0, script.Data)
		unitOrders := orders[id]
		wantOrders := binary.LittleEndian.Uint32(record.Data[0x23:])
		if uint64(wantOrders) != uint64(len(unitOrders)) {
			return fmt.Errorf("nanolathe: retail projection: unit %04x order count %d disagrees with %d records: logical path save/Units, providers searched [projection], expected contiguous queue records", id, wantOrders, len(unitOrders))
		}
		rear := false
		for sequence, order := range unitOrders {
			if order.Sequence != uint32(sequence) {
				return fmt.Errorf("nanolathe: retail projection: unit %04x order sequence %d at position %d: logical path save/Units, providers searched [projection], expected contiguous sequence", id, order.Sequence, sequence)
			}
			if order.Secondary {
				rear = true
			} else if rear {
				return fmt.Errorf("nanolathe: retail projection: unit %04x primary order follows rear order: logical path save/Units, providers searched [projection], expected front orders before rear orders", id)
			}
			if len(order.Main) != OrderBoxSize || binary.LittleEndian.Uint16(order.Main[0:]) != id || binary.LittleEndian.Uint32(order.Main[4:]) != order.SubtypeCode {
				return fmt.Errorf("nanolathe: retail projection: unit %04x order %d main record is inconsistent: logical path save/Units, providers searched [projection], expected matching 0x3A record", id, order.Sequence)
			}
			mainSecondary := binary.LittleEndian.Uint32(order.Main[0x32:])&0x40000 != 0
			if order.Secondary != mainSecondary {
				return fmt.Errorf("nanolathe: retail projection: unit %04x order %d secondary flag disagrees with main record: logical path save/Units, providers searched [projection], expected matching rear-list bit", id, order.Sequence)
			}
			if err := WriteOrderBox(b, id, int(order.Sequence), order.Main); err != nil {
				return err
			}
			if order.SubtypeCode == 0 {
				if len(order.Subtype) != 0 {
					return fmt.Errorf("nanolathe: retail projection: unit %04x order %d has subtype data with zero code: logical path save/Units, providers searched [projection], expected empty subtype", id, order.Sequence)
				}
			} else {
				if !ValidateSubtypeSize(int(order.SubtypeCode), len(order.Subtype)) {
					return fmt.Errorf("nanolathe: retail projection: order subtype %d has invalid size %d: logical path save/Units, providers searched [projection], expected established subtype size", order.SubtypeCode, len(order.Subtype))
				}
				ac.AppendBox(fmt.Sprintf("u%04xm%04xg", id, order.Sequence), 0, order.Subtype)
			}
			if order.DescriptorName != "" {
				ac.SetString(fmt.Sprintf("u%04xm%04x_name", id, order.Sequence), order.DescriptorName)
			}
		}

		hasMover := binary.LittleEndian.Uint32(record.Data[0x27:]) != 0
		if box, exists := movers[id]; exists {
			if !hasMover {
				return fmt.Errorf("nanolathe: retail projection: unit %04x has mover box but base record has no mover: logical path save/Units, providers searched [projection], expected matching has-mover state", id)
			}
			if len(box.Data) != 35 {
				return fmt.Errorf("nanolathe: retail projection: unit %04x mover box has size %d: logical path save/Units, providers searched [projection], expected 35 bytes", id, len(box.Data))
			}
			ac.AppendBox(box.Name, 0, box.Data)
		} else if hasMover {
			return fmt.Errorf("nanolathe: retail projection: unit %04x has mover flag without mover box: logical path save/Units, providers searched [projection], expected u%04xmob", id, id)
		}
		box, exists := accessories[id]
		if !exists {
			return fmt.Errorf("nanolathe: retail projection: unit %04x has no accessory account: logical path save/Units, providers searched [projection], expected u%04xacc", id, id)
		}
		if len(box.Data) != 48 {
			return fmt.Errorf("nanolathe: retail projection: unit %04x accessory box has size %d: logical path save/Units, providers searched [projection], expected 48 bytes", id, len(box.Data))
		}
		ac.AppendBox(box.Name, 0, box.Data)
		ac.AppendBox("", int32(i), record.Data)
	}
	if len(image.Records) > 0 {
		// Retail writes the header scalars only after all live unit records have
		// been visited [08 R-SAVE-02 §6]. Account typed items are serialized
		// before boxes by the bank encoder, but insertion order remains part of
		// the neutral projection contract.
		WriteUnitsHeader(b, len(image.Records))
	}
	// Walk source slices for unmatched references so diagnostics follow the
	// authored order; map iteration would violate deterministic save behavior.
	for _, order := range image.Orders {
		if _, exists := seenIDs[order.ParentStableID]; !exists {
			return fmt.Errorf("nanolathe: retail projection: order references unknown unit %04x: logical path save/Units, providers searched [projection], expected emitted unit", order.ParentStableID)
		}
	}
	for _, box := range image.Other {
		id, kind, ok := parseUnitAuxBoxName(box.Name)
		if !ok {
			continue // already rejected during indexing above
		}
		if _, exists := seenIDs[id]; !exists {
			return fmt.Errorf("nanolathe: retail projection: %s box references unknown unit %04x: logical path save/Units, providers searched [projection], expected emitted unit", kind, id)
		}
	}
	return nil
}

func parseUnitAuxBoxName(name string) (uint16, string, bool) {
	if len(name) != 8 || name[0] != 'u' {
		return 0, "", false
	}
	var kind string
	switch {
	case strings.HasSuffix(name, "acc"):
		kind = "acc"
	case strings.HasSuffix(name, "mob"):
		kind = "mob"
	default:
		return 0, "", false
	}
	var id uint64
	for _, c := range name[1:5] {
		id <<= 4
		switch {
		case c >= '0' && c <= '9':
			id += uint64(c - '0')
		case c >= 'a' && c <= 'f':
			id += uint64(c-'a') + 10
		case c >= 'A' && c <= 'F':
			id += uint64(c-'A') + 10
		default:
			return 0, "", false
		}
	}
	return uint16(id), kind, true
}

func writeFeatureImage(b *Builder, image FeatureImage) error {
	if image.HasTypeNames || len(image.TypeNames) > 0 {
		if err := WriteFeatureTypeNames(b, image.TypeNames); err != nil {
			return err
		}
	}
	ac := builderAccount(b, FeaturesAccount)
	families := []struct {
		count string
		box   string
		size  int
		rows  []FeatureRecord
	}{
		{"Number of Normal Features", featureNormalBox, 8, image.Normal},
		{"Number of Animating Features", featureAnimatingBox, 10, image.Animating},
		{"Number of 3D Features", feature3DBox, 26, image.ThreeD},
	}
	for _, family := range families {
		ac.SetInt(family.count, int32(len(family.rows)))
		if len(family.rows) == 0 {
			continue
		}
		data := make([]byte, 0, len(family.rows)*family.size)
		for _, row := range family.rows {
			if len(row.Data) != family.size {
				return fmt.Errorf("nanolathe: retail projection: feature record has size %d: logical path save/Features, providers searched [projection], expected %d bytes", len(row.Data), family.size)
			}
			data = append(data, row.Data...)
		}
		ac.AppendBox(family.box, 0, data)
	}
	return nil
}

func writeTerrainBox(b *Builder, account, box string, data []byte) {
	if len(data) == 0 {
		return
	}
	builderAccount(b, account).AppendBox(box, 0, data)
}

func writeRawAccount(b *Builder, raw RawAccount) {
	if raw.Name == "" {
		return
	}
	ac := builderAccount(b, raw.Name)
	for _, item := range raw.Ints {
		ac.SetInt(item.Name, item.Value)
	}
	for _, item := range raw.Doubles {
		ac.SetDouble(item.Name, item.Value)
	}
	for _, item := range raw.Strings {
		ac.SetString(item.Name, item.Value)
	}
	for _, box := range raw.Boxes {
		ac.AppendBox(box.Name, box.Number, box.Data)
	}
}

func sortedPlayers(players []PlayerSlot) []PlayerSlot {
	out := append([]PlayerSlot(nil), players...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out
}

func cloneSummary(s Summary) Summary {
	s.RadarImage = cloneBytes(s.RadarImage)
	return s
}

func clonePlayers(in []PlayerSlot) []PlayerSlot { return append([]PlayerSlot(nil), in...) }

func cloneUnitImage(in UnitImage) UnitImage {
	out := UnitImage{Version: in.Version, TypeNames: append([]StringItem(nil), in.TypeNames...)}
	for _, row := range in.Records {
		row.Data = cloneBytes(row.Data)
		out.Records = append(out.Records, row)
	}
	for _, row := range in.Orders {
		row.Main, row.Subtype = cloneBytes(row.Main), cloneBytes(row.Subtype)
		out.Orders = append(out.Orders, row)
	}
	for _, row := range in.Scripts {
		row.Data = cloneBytes(row.Data)
		out.Scripts = append(out.Scripts, row)
	}
	for _, row := range in.Other {
		row.Data = cloneBytes(row.Data)
		out.Other = append(out.Other, row)
	}
	return out
}

func cloneFeatureImage(in FeatureImage) FeatureImage {
	out := FeatureImage{HasTypeNames: in.HasTypeNames, TypeNames: append([]string(nil), in.TypeNames...)}
	clone := func(rows []FeatureRecord) []FeatureRecord {
		out := make([]FeatureRecord, 0, len(rows))
		for _, row := range rows {
			row.Data = cloneBytes(row.Data)
			out = append(out, row)
		}
		return out
	}
	out.Normal, out.Animating, out.ThreeD = clone(in.Normal), clone(in.Animating), clone(in.ThreeD)
	return out
}

func cloneAccounts(in []RawAccount) []RawAccount {
	out := make([]RawAccount, 0, len(in))
	for _, account := range in {
		copy := RawAccount{Name: account.Name, Ints: append([]IntItem(nil), account.Ints...), Doubles: append([]DoubleItem(nil), account.Doubles...), Strings: append([]StringItem(nil), account.Strings...)}
		for _, box := range account.Boxes {
			box.Data = cloneBytes(box.Data)
			copy.Boxes = append(copy.Boxes, box)
		}
		out = append(out, copy)
	}
	return out
}
