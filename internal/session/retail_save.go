package session

import (
	"encoding/binary"
	"fmt"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/features"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/triggers"
	"github.com/nanolathe/nanolathe/internal/units"
)

// RetailSaveInputs supplies the caller-owned metadata and opaque families
// whose runtime source is not yet exposed by Session. In particular, camera,
// description/game identity, radar pixels, mapping bytes, alliances, meteor
// scalars, and the writer's per-unit scratch are external metadata. StableIDs
// is deliberately a caller-supplied table: a pool handle is not a persisted
// stable unit identifier [08 "Summary"] [08 R-SAVE-02 §6].
type RetailSaveInputs struct {
	Summary save.Summary
	Camera  save.Camera

	// StableIDs maps every live unit handle and every referenced unit handle to
	// its save-stable identifier. Missing entries are an exact projection
	// failure, never an implicit handle-as-ID conversion.
	StableIDs map[pool.Handle]uint16
	// UnitWriterScratch supplies the three packed status bits whose values are
	// transient writer state rather than retained Unit fields [08 R-SAVE-02 §6].
	UnitWriterScratch map[pool.Handle]units.RetailUnitWriterScratch

	// Mapping has no exact source in the current runtime. A non-nil value is
	// therefore required for a live save and is copied as supplied [08
	// R-SAVE-02 §12].
	Mapping []byte

	HasAlliances bool
	Alliances    [11]byte
	Meteor       save.MeteorScalars
}

// ProjectRetailSession maps established live Session state into a detached
// save projection. Session-owned economy/player fields and the 28-byte clock
// snapshot are read through their existing APIs; caller-owned metadata and
// opaque subsystem images are copied explicitly [08 "Player records"] [08
// "Scheduler and random state in saves"].
func ProjectRetailSession(s *Session, in RetailSaveInputs) (save.RetailProjection, error) {
	if s == nil {
		return save.RetailProjection{}, fmt.Errorf("nanolathe: retail save projection: nil session: logical path session/save, providers searched [session], expected live Session")
	}
	p := save.RetailProjection{
		Summary:      in.Summary,
		Camera:       in.Camera,
		Mapping:      append([]byte(nil), in.Mapping...),
		HasAlliances: in.HasAlliances,
		Alliances:    in.Alliances,
		Meteor:       in.Meteor,
	}

	// A continuation has no live battle account families and therefore does
	// not need a clock or economy shell. Summary.BetweenMissions is the sole
	// established route discriminator [08 "Summary"].
	if in.Summary.BetweenMissions == 1 {
		return p.Clone(), nil
	}
	if s.Clock == nil {
		return save.RetailProjection{}, fmt.Errorf("nanolathe: retail save projection: missing clock: logical path session/save, providers searched [Session.Clock], expected 28-byte GameTime source")
	}
	box := s.Clock.SaveBox()
	p.Scheduler = box
	p.Summary.GameTime = int32(s.Clock.GlobalTick)
	p.HumanPlayer = int32(s.LocalOwner)

	if s.Econ != nil {
		players := make([]save.PlayerSlot, 0, 10)
		for i := 0; i < 10; i++ {
			player := &s.Econ.Players[i]
			if !player.Exists {
				continue
			}
			players = append(players, save.PlayerSlotFromEconomy(i, *player))
		}
		p.Players = players
	}

	// Terrain-owned save views are the sole source for both byte rasters. The
	// Mapping box is different: the current runtime intentionally has no exact
	// owner, so an omitted caller value is a hard error [08 R-SAVE-02 §12].
	if s.World != nil {
		metal, err := s.World.RetailMetalImage()
		if err != nil {
			return save.RetailProjection{}, fmt.Errorf("nanolathe: retail save projection: Metal: %w", err)
		}
		playerFeatures, err := s.World.RetailPlayerFeaturesImage()
		if err != nil {
			return save.RetailProjection{}, fmt.Errorf("nanolathe: retail save projection: PlayerFeatures: %w", err)
		}
		p.Metal = metal
		p.PlayerFeatures = playerFeatures
	}
	if in.Mapping == nil {
		return save.RetailProjection{}, fmt.Errorf("nanolathe: retail save projection: Mapping source is unavailable: logical path session/save/Mapping, providers searched [caller], expected exact mapping bytes")
	}
	if s.World != nil {
		cells := int64(s.World.CellW) * int64(s.World.CellH)
		want := cells >> 1
		if want < 0 || int64(len(in.Mapping)) != want {
			return save.RetailProjection{}, fmt.Errorf("nanolathe: retail save projection: Mapping has %d bytes, expected %d: logical path session/save/Mapping, providers searched [caller], expected exact half-grid mapping bytes", len(in.Mapping), want)
		}
	}
	p.Mapping = append([]byte(nil), in.Mapping...)

	if s.Features != nil {
		image, err := s.Features.RetailFeatureImage()
		if err != nil {
			return save.RetailProjection{}, fmt.Errorf("nanolathe: retail save projection: Features: %w", err)
		}
		p.Features = projectFeatureImage(image)
	}

	if s.Units != nil {
		image, err := projectUnitImage(s.Units, s.Econ, s.Movement, in)
		if err != nil {
			return save.RetailProjection{}, err
		}
		p.Units = image
	}
	if s.Mission != nil {
		accounts, err := triggers.RetailTriggerImage(s.Mission.Victory, s.Mission.Defeat)
		if err != nil {
			return save.RetailProjection{}, fmt.Errorf("nanolathe: retail save projection: triggers: %w", err)
		}
		p.Triggers = projectTriggerAccounts(accounts)
	}
	return p.Clone(), nil
}

// projectUnitImage follows the retail writer's reverse-pool traversal. The
// detached image retains that order; save.Build emits each unit's Script,
// front/rear orders, mover, account, and numbered base record in turn [08
// R-SAVE-02 §6].
func projectUnitImage(w *units.World, econ *economy.Service, movement *movement.System, in RetailSaveInputs) (save.UnitImage, error) {
	image := save.UnitImage{Version: save.UnitsVersionRetail}
	resolve := func(h pool.Handle) (uint16, bool) {
		id, ok := in.StableIDs[h]
		return id, ok && id != 0
	}
	for slot := w.TotalRecords() - 1; slot > 0; slot-- {
		h := pool.Handle(slot)
		u := w.Unit(h)
		if u == nil {
			continue
		}
		scratch, ok := in.UnitWriterScratch[h]
		if !ok {
			return save.UnitImage{}, fmt.Errorf("nanolathe: retail save projection: missing unit packed scratch: logical path save/Units/u%04x, providers searched [caller], expected RetailUnitWriterScratch", h)
		}
		ordersImage, err := orders.RetailOrderImages(u, resolve)
		if err != nil {
			return save.UnitImage{}, fmt.Errorf("nanolathe: retail save projection: unit %04x orders: %w", h, err)
		}
		vm := u.GetScript()
		if vm == nil {
			return save.UnitImage{}, fmt.Errorf("nanolathe: retail save projection: unit %04x has no COB runtime: logical path save/Units/Script, providers searched [Session.Units], expected live VM", h)
		}
		script, err := cob.RetailScriptImage(vm)
		if err != nil {
			return save.UnitImage{}, fmt.Errorf("nanolathe: retail save projection: unit %04x Script%d: %w", h, len(image.Records), err)
		}
		var mover []byte
		if u.HasMover {
			if movement == nil {
				return save.UnitImage{}, fmt.Errorf("nanolathe: retail save projection: unit %04x mover is unavailable: logical path save/Units/u%04xmob, providers searched [Session.Movement], expected live mover", h, h)
			}
			mover, err = movement.RetailMoverImage(h)
			if err != nil {
				return save.UnitImage{}, fmt.Errorf("nanolathe: retail save projection: unit %04x mover: %w", h, err)
			}
		}
		if econ == nil {
			return save.UnitImage{}, fmt.Errorf("nanolathe: retail save projection: unit %04x economy is unavailable: logical path save/Units/u%04xacc, providers searched [Session.Econ], expected 48-byte unit account", h, h)
		}
		account, err := econ.RetailUnitAccountImage(h)
		if err != nil {
			return save.UnitImage{}, fmt.Errorf("nanolathe: retail save projection: unit %04x economy: %w", h, err)
		}
		base, err := units.RetailUnitImage(u, uint32(len(ordersImage)), resolve, scratch)
		if err != nil {
			return save.UnitImage{}, fmt.Errorf("nanolathe: retail save projection: unit %04x base: %w", h, err)
		}
		id, ok := resolve(h)
		if !ok {
			return save.UnitImage{}, fmt.Errorf("nanolathe: retail save projection: unit %04x has no stable ID: logical path save/Units, providers searched [caller], expected explicit handle-to-stable-ID entry", h)
		}
		image.Records = append(image.Records, save.UnitRecord{Number: len(image.Records), StableID: id, Data: base})
		image.Scripts = append(image.Scripts, save.ScriptRecord{Index: len(image.Scripts), Data: script})
		for _, order := range ordersImage {
			image.Orders = append(image.Orders, save.OrderRecord{
				ParentStableID: order.ParentStableID, Sequence: order.Sequence, Secondary: order.Secondary,
				Main: order.Main, SubtypeCode: order.SubtypeCode, Subtype: order.Subtype,
				DescriptorName: order.DescriptorName, BuildTypeName: order.BuildTypeName,
			})
		}
		if mover != nil {
			image.Other = append(image.Other, save.RawBox{Name: fmt.Sprintf("u%04xmob", id), Data: mover})
		}
		image.Other = append(image.Other, save.RawBox{Name: fmt.Sprintf("u%04xacc", id), Data: account})
		// Build derives the exact per-unit box order from this side data. Keep
		// the type-name table tied to the saved catalog index, not pool order.
		for _, order := range ordersImage {
			if order.BuildTypeName == "" || !isBuildOrderName(order.DescriptorName) {
				continue
			}
			index := binary.LittleEndian.Uint32(order.Main[0x26:])
			name := fmt.Sprintf("UTYPENAME%4d", index)
			seen := false
			for _, existing := range image.TypeNames {
				if existing.Name == name {
					if existing.Value != order.BuildTypeName {
						return save.UnitImage{}, fmt.Errorf("nanolathe: retail save projection: conflicting %s values %q and %q: logical path save/Units, providers searched [runtime orders], expected one stable definition name", name, existing.Value, order.BuildTypeName)
					}
					seen = true
					break
				}
			}
			if !seen {
				image.TypeNames = append(image.TypeNames, save.StringItem{Name: name, Value: order.BuildTypeName})
			}
		}
	}
	return image, nil
}

func isBuildOrderName(name string) bool {
	switch name {
	case "MobileBuild", "VTOL_MobileBuild", "BuildingBuild":
		return true
	default:
		return false
	}
}

func projectFeatureImage(in features.RetailFeatureImage) save.FeatureImage {
	out := save.FeatureImage{HasTypeNames: len(in.TypeNames) != 0, TypeNames: append([]string(nil), in.TypeNames...)}
	convert := func(rows []features.RetailFeatureRecord) []save.FeatureRecord {
		out := make([]save.FeatureRecord, 0, len(rows))
		for _, row := range rows {
			out = append(out, save.FeatureRecord{X: row.X, Z: row.Z, TypeID: row.TypeID, Data: append([]byte(nil), row.Data...)})
		}
		return out
	}
	out.Normal = convert(in.Normal)
	out.Animating = convert(in.Animating)
	out.ThreeD = convert(in.ThreeD)
	return out
}

func projectTriggerAccounts(in []triggers.RetailTriggerAccount) []save.RawAccount {
	out := make([]save.RawAccount, 0, len(in))
	for _, account := range in {
		raw := save.RawAccount{Name: account.Name}
		for _, item := range account.Ints {
			raw.Ints = append(raw.Ints, save.IntItem{Name: item.Name, Value: item.Value})
		}
		for _, box := range account.Boxes {
			raw.Boxes = append(raw.Boxes, save.RawBox{Name: box.Name, Number: box.Number, Data: append([]byte(nil), box.Data...)})
		}
		out = append(out, raw)
	}
	return out
}

// RetailProjection is the method form of ProjectRetailSession.
func (s *Session) RetailProjection(in RetailSaveInputs) (save.RetailProjection, error) {
	return ProjectRetailSession(s, in)
}

// WriteRetailSave projects s and writes the retail bank using the direct
// truncate-write policy already owned by save.WriteFile [08 "File naming and
// write policy"].
func (s *Session) WriteRetailSave(path string, in RetailSaveInputs) error {
	p, err := ProjectRetailSession(s, in)
	if err != nil {
		return err
	}
	data, err := p.RetailBytes()
	if err != nil {
		return err
	}
	return save.WriteFile(path, data)
}
