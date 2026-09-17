package session

import (
	"encoding/binary"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/triggers"
	"github.com/nanolathe-gg/nanolathe/internal/units"
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
	// DisplayTimers overlays presentation-owned deadline advances on detached
	// player records only. Omitted players keep the restored/seeded value
	// [05 R-ECO-01 §1, §6][I6].
	DisplayTimers map[uint8]uint32

	// StableIDs maps every live unit handle and every referenced pool slot to
	// its save-stable identifier. Missing entries are an exact projection
	// failure, never an implicit handle-as-ID conversion.
	StableIDs map[pool.Handle]uint16
	// UnitWriterScratch supplies the three packed status bits whose values are
	// transient writer state rather than retained Unit fields [08 R-SAVE-02 §6].
	UnitWriterScratch map[pool.Handle]units.RetailUnitWriterScratch
	// UnitMirrors supplies the base-record words whose runtime owner is a
	// service rather than the Unit, for the handles that have one. The
	// projection applies them to a DETACHED copy of the unit; a handle with no
	// entry writes the unit's own retained words [08 R-SAVE-02 §6].
	// RetailBattleSaveInputs fills this from the live services.
	UnitMirrors map[pool.Handle]RetailUnitMirrors

	// Mapping is required for a live save and copied as supplied [08
	// R-SAVE-02 §12]. RetailMappingImage below is the runtime source; a caller
	// that owns its own image (a load round-trip, a fixture) may still supply
	// one directly.
	Mapping []byte

	// Alliances are NOT caller metadata: each active slot's eleven-byte row is
	// the last item of that slot's own `Player%i` account, and the projection
	// takes it from the runtime table through `economy.Player.AllianceRow`
	// [08 "Player records"] [05 R-SHARE-01 §1]. The two fields that stood here
	// modelled one account-level box under `Players`; no caller ever set them,
	// so the box was never written.
	Meteor save.MeteorScalars
}

// ProjectRetailSession maps established live Session state into a detached
// save projection. Session-owned economy/player fields — the nineteen scalars
// and the slot's own alliance row — and the 28-byte clock snapshot are read
// through their existing APIs; caller-owned metadata and
// opaque subsystem images are copied explicitly [08 "Player records"] [08
// "Scheduler and random state in saves"].
func ProjectRetailSession(s *Session, in RetailSaveInputs) (save.RetailProjection, error) {
	if s == nil {
		return save.RetailProjection{}, fmt.Errorf("nanolathe: retail save projection: nil session: logical path session/save, providers searched [session], expected live Session")
	}
	p := save.RetailProjection{
		Summary: in.Summary,
		Camera:  in.Camera,
		Mapping: append([]byte(nil), in.Mapping...),
		Meteor:  in.Meteor,
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
			row := save.PlayerSlotFromEconomy(i, *player)
			if deadline, ok := in.DisplayTimers[uint8(i)]; ok {
				row.DisplayTimer = int32(deadline)
			}
			players = append(players, row)
		}
		p.Players = players
	}

	// Terrain-owned save views are the sole source for both byte rasters. The
	// Mapping box is different: its runtime owner is the visibility service,
	// not the terrain, so an omitted caller value is a hard error rather than
	// a silent empty grid [08 R-SAVE-02 §12].
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

// projectUnitImage follows the retail writer's ascending pool traversal: the
// single Units writer starts its cursor at the unit pool's base record, tests
// it against the pool's end, and advances one record stride per iteration, so
// the running numbered-box index rises with pool position. The detached image
// retains that order; save.Build emits each unit's Script, front/rear orders,
// mover, account, and numbered base record in turn [08 R-SAVE-02 §6].
//
// The direction matters on load, not only for byte comparability: the reader
// restores numbered boxes 0..count-1 in index order, and the base record's AI
// group word is the one word with a side effect beyond a field copy (it
// appends the unit to the owner's group vector), so a reversed walk would
// build every group vector backwards [08 R-SAVE-02 §6].
func projectUnitImage(w *units.World, econ *economy.Service, movement *movement.System, in RetailSaveInputs) (save.UnitImage, error) {
	image := save.UnitImage{Version: save.UnitsVersionRetail}
	resolve := func(h pool.Handle) (uint16, bool) {
		id, ok := in.StableIDs[h]
		return id, ok && id != 0 && w.Unit(h) != nil
	}
	for slot := 1; slot < w.TotalRecords(); slot++ {
		h := pool.Handle(slot)
		u := w.Unit(h)
		if u == nil {
			continue
		}
		// The words below belong to a service, not to the Unit, and the save
		// is the only consumer. They are applied to a DETACHED copy so that
		// taking a save cannot change live state: the committed cell pair has
		// a live simulation reader (the corpse anchor of the death hook) and
		// writing it from here moved later wrecks [05 R-FEAT-01 §13].
		record := u
		if mirrors, ok := in.UnitMirrors[h]; ok {
			mirrored := *u
			mirrors.applyTo(&mirrored)
			record = &mirrored
		}
		scratch, ok := in.UnitWriterScratch[h]
		if !ok {
			return save.UnitImage{}, fmt.Errorf("nanolathe: retail save projection: missing unit packed scratch: logical path save/Units/u%04x, providers searched [caller], expected RetailUnitWriterScratch", h)
		}
		var payloadSource orders.RetailPayloadSource
		if movement != nil {
			payloadSource = movement.RetailOrderPayload
		}
		ordersImage, err := orders.RetailOrderImagesWithPayload(u, resolve, func(target pool.Handle) bool {
			return w.Unit(target) != nil
		}, payloadSource)
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
		if record.HasMover {
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
		base, err := units.RetailUnitImage(record, uint32(len(ordersImage)), resolve, func(target pool.Handle) (uint16, bool) {
			if target == 0 || int(target) >= w.TotalRecords() {
				return 0, false
			}
			id, ok := in.StableIDs[target]
			return id, ok && id != 0
		}, scratch)
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
	data, err := p.Bytes()
	if err != nil {
		return err
	}
	return save.WriteFile(path, data)
}

// RetailSaveDirName is the directory the save screen enumerates and writes
// into, relative to the install root [08 R-SAVE-02 §1].
const RetailSaveDirName = "savegame"

// RetailSaveExt is the extension the path builder appends after the strip
// [08 "File naming and write policy"].
const RetailSaveExt = ".SAV"

// RetailSavePath assembles `<dir>/<name>.SAV` under retail's normalization:
// the assembled path is truncated at its **last** dot and the extension is
// appended. The strip is deliberately not path-component aware, so a dot in a
// directory name is hit too, and a typed name of `v1.2 final` saves as
// `v1.SAV` [08 "File naming and write policy"] [08 R-SAVE-02 §1].
//
// An empty name yields an empty path: retail's save action does nothing at
// all for an empty GAMENAME — no file and no message [08 R-SAVE-02 §1].
func RetailSavePath(dir, name string) string {
	if strings.TrimSpace(name) == "" {
		return ""
	}
	assembled := name
	if dir != "" {
		assembled = filepath.Join(dir, name)
	}
	if dot := strings.LastIndex(assembled, "."); dot >= 0 {
		assembled = assembled[:dot]
	}
	return assembled + RetailSaveExt
}

// WriteRetailContinuationSave writes a between-missions bank. A continuation
// carries the Summary account and nothing else — there is no battle state in
// it, which is exactly why the loader's BetweenMissions gate can route it to
// the fresh mission spawner [08 "battle versus campaign continuations and
// timing"] [08 R-CAMP-01 §8].
func WriteRetailContinuationSave(path string, summary save.Summary) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("nanolathe: retail continuation save: empty path: logical path save/Summary, providers searched [caller], expected an assembled SAVEGAME path")
	}
	if summary.BetweenMissions != 1 {
		return fmt.Errorf("nanolathe: retail continuation save: BetweenMissions is %d: logical path save/Summary, providers searched [caller], expected the continuation marker 1", summary.BetweenMissions)
	}
	data, err := save.RetailProjection{Summary: summary}.Bytes()
	if err != nil {
		return err
	}
	return save.WriteFile(path, data)
}

// RetailMappingImage returns the Mapping box bytes for a live battle.
//
// The Mapping box is "the mapping grid verbatim", one unnamed box of
// `(width x height) >> 1` bytes [08 R-SAVE-02 §12]. That grid is the
// explored-memory word grid of [03 §3.1] — the same grid the share screen's
// merge walks, `(cell width x cell height) / 4` sixteen-bit words with ten
// usable player bits [05 R-SHARE-01 §6] — which the visibility service owns.
// The words are stored in their in-memory little-endian order.
func RetailMappingImage(s *Session) ([]byte, error) {
	if s == nil || s.Vis == nil || s.World == nil {
		return nil, fmt.Errorf("nanolathe: retail save: Mapping source is unavailable: logical path session/save/Mapping, providers searched [Session.Vis], expected the explored-memory word grid")
	}
	words := s.Vis.WordMask()
	cells := int64(s.World.CellW) * int64(s.World.CellH)
	want := cells >> 2
	if want <= 0 || int64(len(words)) != want {
		return nil, fmt.Errorf("nanolathe: retail save: Mapping grid has %d words, expected %d: logical path session/save/Mapping, providers searched [Session.Vis], expected one word per four terrain cells", len(words), want)
	}
	out := make([]byte, len(words)*2)
	for i, word := range words {
		binary.LittleEndian.PutUint16(out[i*2:], word)
	}
	return out, nil
}

// RetailBattleSaveInputs assembles the caller-owned side of a live-battle
// save from the session itself.
//
// StableIDs includes free pool slots so a held weapon target survives a save
// after its target was freed. Retail's save identities are "stable
// identifiers rather than native pointers: unit slot zero is null and each
// live unit's slot index is its stable identifier"
// [08 "Account inventory"] [08 R-SAVE-02 §6].
//
// UnitWriterScratch stays zero. Word bits 17..19 of the packed status word are
// "never written" by the retail writer — they carry whatever stack residue the
// writer's frame held, and the reader does not consume them
// [08 R-SAVE-02 §6 "the packed status word"]. Nanolathe has no such residue,
// so the honest value is zero rather than an invented pattern.
//
// Script piece state is read directly from each bound VM by the projection;
// draw/cache/shade flags are ordinary persisted values [08 R-SAVE-02 §9].
func (s *Session) RetailBattleSaveInputs(summary save.Summary, camera save.Camera) (RetailSaveInputs, error) {
	if s == nil || s.Units == nil {
		return RetailSaveInputs{}, fmt.Errorf("nanolathe: retail save inputs: no live battle: logical path session/save, providers searched [Session], expected a composed battle")
	}
	mapping, err := RetailMappingImage(s)
	if err != nil {
		return RetailSaveInputs{}, err
	}
	in := RetailSaveInputs{
		Summary:           summary,
		Camera:            camera,
		StableIDs:         make(map[pool.Handle]uint16),
		UnitWriterScratch: make(map[pool.Handle]units.RetailUnitWriterScratch),
		Mapping:           mapping,
		Meteor:            retailMeteorScalars(s.Meteor),
	}
	for slot := 1; slot < s.Units.TotalRecords(); slot++ {
		h := pool.Handle(slot)
		if slot > 0xffff {
			return RetailSaveInputs{}, fmt.Errorf("nanolathe: retail save inputs: unit slot %d exceeds the 16-bit stable identifier: logical path save/Units, providers searched [Session.Units], expected a slot below 65536", slot)
		}
		in.StableIDs[h] = uint16(slot)
		if s.Units.Unit(h) == nil {
			continue
		}
		in.UnitWriterScratch[h] = units.RetailUnitWriterScratch{}
	}
	in.UnitMirrors = retailUnitMirrors(s)
	return in, nil
}

// RetailUnitMirrors carries the base-record words whose runtime owner is some
// other service, not the Unit: the has-mover flag, the committed occupancy
// cell pair, the footprint size pair and the AI group index [08 R-SAVE-02 §6].
//
// Retail keeps all four on the unit itself — the occupancy stamp refreshes the
// cell pair every time the unit moves [04 R-COLL-01 §1] "the cached cell pair
// is the unit's committed footprint anchor". This build keeps the first three
// on the movement system's collision record or construction's retained
// placement, and the fourth on the unit's live group field, while the common
// initializer seeds the Unit's own copies at allocation and the save RESTORE
// is the only later writer.
//
// **Correction.** These values used to be written back onto the LIVE unit
// records just before a projection read them, under a comment claiming that
// nothing but the save consumes them. That claim was false: the death hook's
// corpse stamp reads the committed cell pair as the wreck anchor whenever the
// movement system has no committed footprint for the handle
// [05 R-FEAT-01 §13], so taking a save — or an autosave — moved where a later
// corpse was stamped and reclaimed. The words are now projected into this
// detached structure and applied to a copy of the unit, and the live record is
// never written by the save path. Making the stamp, the mover allocation and
// the group writer write the Unit words directly, as retail does, would remove
// the need for this projection entirely.
type RetailUnitMirrors struct {
	HasMover     bool
	AIGroup      int32
	OccupancyX   int16
	OccupancyZ   int16
	FootprintX   int16
	FootprintZ   int16
	hasFootprint bool
}

// applyTo writes the projected words onto a detached unit copy. The occupancy
// and footprint pairs are written only when a service owned them; otherwise
// the copy keeps the allocation origin the common initializer retained
// [04 R-ORD-01 §1][05 R-FEAT-01 §13].
func (m RetailUnitMirrors) applyTo(u *units.Unit) {
	if u == nil {
		return
	}
	u.HasMover = m.HasMover
	u.RestoredAIGroup = m.AIGroup
	if !m.hasFootprint {
		return
	}
	u.CachedOccupancyX, u.CachedOccupancyZ = m.OccupancyX, m.OccupancyZ
	u.FootprintSizeX, u.FootprintSizeZ = m.FootprintX, m.FootprintZ
}

// retailUnitMirrors projects the service-owned base-record words for every
// live unit. It reads the live services and writes nothing [08 R-SAVE-02 §6].
func retailUnitMirrors(s *Session) map[pool.Handle]RetailUnitMirrors {
	if s == nil || s.Units == nil || s.Movement == nil {
		return nil
	}
	out := make(map[pool.Handle]RetailUnitMirrors)
	for slot := 1; slot < s.Units.TotalRecords(); slot++ {
		h := pool.Handle(slot)
		u := s.Units.Unit(h)
		if u == nil {
			continue
		}
		m := RetailUnitMirrors{
			// A live non-building collision record is the mover
			// [04 R-PATH-01 §14], which is exactly what the record's has-mover
			// word selects.
			HasMover: s.Movement.HasMover(h),
			// The AI group index is -1 for none [08 R-SAVE-02 §6]. Its runtime
			// authority is Unit.Group, the 0..9 manager/control-group field the
			// group writer stores [R-P0-04 §3]; group 0 is the ungrouped record
			// and is what the file's -1 means.
			AIGroup: -1,
		}
		if u.Group != 0 {
			m.AIGroup = int32(u.Group)
		}
		if anchor, footX, footZ, ok := s.Movement.OverlapRect(slot); ok {
			m.OccupancyX, m.OccupancyZ = int16(anchor.X), int16(anchor.Z)
			m.FootprintX, m.FootprintZ = footX, footZ
			m.hasFootprint = true
		} else if rect, held := s.Build.PlacementForProduct(h); held {
			// An unfinished structure can own a construction placement without
			// a movement collision record. Its retained rectangle supplies the
			// same committed cell pair the loader uses to rebuild its yard;
			// resnapping its position would lose that saved ownership
			// [08 R-SAVE-02 §6, §11][04 R-COLL-01 §4].
			m.OccupancyX, m.OccupancyZ = int16(rect.MinX()), int16(rect.MinZ())
			m.FootprintX, m.FootprintZ = int16(rect.Width()), int16(rect.Depth())
			m.hasFootprint = true
		}
		out[h] = m
	}
	return out
}

// retailMeteorScalars projects the live shower into the nine integer items of
// the Meteor account. The first five globals round-trip whole; the four
// coordinates are sixteen-bit globals the writer sign-extends to the item's
// integer width [08 "Account inventory"].
func retailMeteorScalars(m MeteorState) save.MeteorScalars {
	boolean := func(v bool) int32 {
		if v {
			return 1
		}
		return 0
	}
	return save.MeteorScalars{
		Enabled:        boolean(m.Enabled),
		Active:         boolean(m.Active),
		NextStrikeTime: int32(m.NextStrike),
		TimeStrikeEnds: int32(m.StrikeEnds),
		NextHitTime:    int32(m.NextHit),
		OriginX:        int32(int16(m.OriginX)),
		OriginZ:        int32(int16(m.OriginZ)),
		TargetX:        int32(int16(m.TargetX)),
		TargetZ:        int32(int16(m.TargetZ)),
	}
}

// RetailBattleSummary projects a live battle into the Summary account the
// retail writer emits from inside a battle: it names the **current** mission
// and omits `BetweenMissions`, which is what routes its load through battle
// restoration rather than campaign continuation [08 R-CAMP-01 §8
// "Between-missions save quirk"] [08 "Summary"].
//
// `Description` is the name typed into the save screen and `Game ID` the
// wall-clock seconds at the moment of saving, so both are caller-owned [08
// R-SAVE-02 §1] [I6]. The multiplayer rule integers are emitted only for
// game type 2 [08 "Summary"].
//
// Strict 3.1 and campaign saves write configuredUnitLimit as Summary.maxunits
// [08 R-SESS-01 §9]. Modern skirmish saves write the actual battle limit so a
// later load can reconstruct its player slices even after settings change
// (DESIGN_SESSIONS_AI_SAVE "Modern save unit limits").
func RetailBattleSummary(s *Session, description, gameID string, configuredUnitLimit int) save.Summary {
	if s == nil {
		return save.Summary{}
	}
	summary := save.Summary{
		// Retail's writer always emits `maxunits` [08 "Summary"]. The word it
		// initially records is the configured limit; the reader's low-16-bit
		// truncation is a read-side rule, so the word is stored whole here.
		MaxUnits:    int32(configuredUnitLimit),
		HasMaxUnits: true,
		Description: description,
		GameID:      gameID,
		IsBattle:    true,
	}
	if s.Clock != nil {
		summary.GameTime = int32(s.Clock.GlobalTick)
	}
	campaign := s.Mission != nil && s.Mission.Type == mission.TypeCampaign
	if campaign {
		summary.Gametype = GametypeCampaign
		summary.Campaign = s.Mission.CampaignPath
		summary.Mission = s.Mission.CampaignMissionName
		summary.MapName = s.Mission.CampaignMissionName
		summary.Difficulty = int32(s.Mission.Difficulty)
		summary.Players = RetailPlayerCount(s)
		summary.Thumbs = string(s.Progress.Thumbs[:])
	} else {
		// Skirmish is session kind 2, the same game type the summary panel
		// renders as `Skirmish (%d players)` [08 R-SAVE-02 §3] [08 R-SAVE-02 §4].
		if s.Gameplay.Normalize() == gameplay.Modern {
			summary.MaxUnits = int32(sessionUnitLimit(s))
		}
		summary.Gametype = GametypeMultiplayer
		summary.IsMultiplayer = true
		summary.MapName = s.Skirmish.MapName
		summary.Mission = s.Skirmish.MapName
		summary.Difficulty = int32(s.Skirmish.Difficulty)
		summary.Players = int32(s.Skirmish.NumPlayers)
		summary.CommanderDeath = int32(s.Skirmish.CommanderDeath)
		summary.Location = int32(s.Skirmish.Location)
		summary.Mapping = int32(s.Skirmish.Mapping)
		summary.LineOfSight = int32(s.Skirmish.LineOfSight)
		summary.LineOfSightType = int32(s.Skirmish.LOSType)
	}
	// The Summary's `Side` is the local slot's side [08 R-SKIR-01 §2] "Save
	// persistence", read off the player record like every other post-entry
	// side read: a battle that was itself restored has no setup rows to read
	// (see player_record.go), so writing them back would zero the item on the
	// second save of a chain.
	if side, ok := s.sideForOwner(int(s.LocalOwner)); ok {
		summary.Side = int32(side)
	}
	// The `Radar Image` box is a presentation preview the load dispatcher never
	// restores [08 "Account inventory"]. Its raster is the radar surface the
	// side rail composes, which a session neither owns nor may reach [I6], so
	// the box is the caller's half of this summary: the host fills
	// Summary.RadarImage before handing it to RetailBattleSaveInputs, and a
	// caller with no radar (a headless save) writes no box — the panel then
	// shows no preview, exactly as a short box does [08 R-SAVE-02 §3].
	return summary
}

// RetailPlayerCount is the session's live player count, the value the Summary
// `Players` item carries. The load screen's summary panel renders `???` for a
// zero value, so a saved session reports the slots it actually has
// [08 R-SAVE-02 §3].
func RetailPlayerCount(s *Session) int32 {
	if s == nil || s.Econ == nil {
		return 0
	}
	count := int32(0)
	for i := 0; i < len(s.Econ.Players); i++ {
		if s.Econ.Players[i].Exists {
			count++
		}
	}
	return count
}
