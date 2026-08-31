package session

import (
	"fmt"

	"github.com/nanolathe/nanolathe/internal/save"
)

// RetailSaveInputs supplies the caller-owned metadata and opaque families
// whose runtime source is not yet exposed by Session. In particular, camera,
// description/game identity, radar pixels, mapping bytes, and the exact unit,
// order, script, and feature records must not be guessed [08 "Summary"] [08
// R-SAVE-02 §6].
type RetailSaveInputs struct {
	Summary save.Summary
	Camera  save.Camera

	// These are explicit detached records. A live session with units or
	// features rejects omitted records rather than writing fabricated bytes.
	Units    save.UnitImage
	Features save.FeatureImage

	PlayerFeatures []byte
	Mapping        []byte

	HasAlliances bool
	Alliances    [11]byte
	Meteor       save.MeteorScalars
	Triggers     []save.RawAccount
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
		Summary:        in.Summary,
		Camera:         in.Camera,
		Units:          in.Units,
		Features:       in.Features,
		PlayerFeatures: append([]byte(nil), in.PlayerFeatures...),
		Mapping:        append([]byte(nil), in.Mapping...),
		HasAlliances:   in.HasAlliances,
		Alliances:      in.Alliances,
		Meteor:         in.Meteor,
		Triggers:       in.Triggers,
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

	// The terrain metal raster is an established direct source. PlayerFeatures
	// and Mapping remain caller inputs: the former's writer image is a packed
	// nibble projection and the latter's save bytes are not the visibility
	// service's word mask, so substituting either would invent state [08
	// R-SAVE-02 §12].
	if len(p.Metal) == 0 && s.World != nil {
		p.Metal = make([]byte, len(s.World.Plot))
		for i := range s.World.Plot {
			p.Metal[i] = s.World.Plot[i].Metal()
		}
	}
	if s.Units != nil && s.Units.Used() != 0 && len(p.Units.Records) == 0 {
		return save.RetailProjection{}, fmt.Errorf("nanolathe: retail save projection: live unit records unavailable: logical path session/save/Units, providers searched [Session.Units], expected detached UnitImage with established 0xB8 records")
	}
	if s.Features != nil && len(s.Features.Instances()) != 0 && len(p.Features.Normal)+len(p.Features.Animating)+len(p.Features.ThreeD) == 0 {
		return save.RetailProjection{}, fmt.Errorf("nanolathe: retail save projection: live feature records unavailable: logical path session/save/Features, providers searched [Session.Features], expected detached FeatureImage with established records")
	}
	return p.Clone(), nil
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
