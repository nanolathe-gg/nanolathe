package session

import (
	"bytes"
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/netproto"
)

// Required gameplay extensions of DESIGN_MULTIPLAYER §8.6. The positional
// fields and the replay header's descriptive metadata remain unchanged.
// Builder speed and Smart makers are mutators rather than seat rules, but
// field 11's eleven step indices are frozen, so Builder speed's own step list
// and the Smart makers switch travel here (docs/DESIGN_MODS_MUTATORS.md
// "Builder speed", "Smart makers").
const (
	matchChosenStartsExtension = 17
	matchAlliedSightExtension  = 18
	matchBuilderSpeedExtension = 19
	// matchGeneratedMapExtension is not a gameplay choice: it carries what
	// reproducing the battle needs, which belongs in a required record
	// (§10 "Header extensions"), with the same admission contract.
	matchGeneratedMapExtension = 20
	matchSmartMakersExtension  = 21
)

func matchSetupExtension(id uint16) bool {
	return id == matchChosenStartsExtension || id == matchAlliedSightExtension || id == matchBuilderSpeedExtension || id == matchGeneratedMapExtension || id == matchSmartMakersExtension
}

func (r *MatchConfigRequest) hasChosenStarts() bool {
	for _, seat := range r.Seats {
		if seat.Start.Set {
			return true
		}
	}
	return false
}

// consumeSetupExtensions projects recognized wire records into their typed
// fields before ordinary admission, then removes those records. Request thus
// returns one editable value for each choice; changing it cannot leave stale
// opaque bytes behind. Unknown descriptive records keep their owned bytes.
func (r *MatchConfigRequest) consumeSetupExtensions() error {
	if err := validateMatchExtensions(r.Extensions); err != nil {
		return err
	}
	kept := r.Extensions[:0]
	for _, e := range r.Extensions {
		switch e.ID {
		case matchChosenStartsExtension:
			if len(r.Seats) < matchMinSeats || len(r.Seats) > SkirmishMaxPlayers {
				return matchFieldError("seats", "2..10 seats before chosen starts")
			}
			reader := netproto.NewReader(e.Data, matchFieldError)
			anySet := false
			for i := range r.Seats {
				var point StartPoint
				if point.Set = reader.Bool(); point.Set {
					x, z := reader.U32(), reader.U32()
					if reader.Err() == nil && (x >= StartPointLimit || z >= StartPointLimit) {
						reader.Fail(fmt.Sprintf("start coordinates 0..%d", StartPointLimit-1))
					}
					point.X, point.Z = int32(x), int32(z)
					anySet = true
				}
				if reader.Err() != nil {
					return reader.Err()
				}
				if old := r.Seats[i].Start; old != (StartPoint{}) && old != point {
					return matchFieldError("chosenStarts", "typed starts equal to extension 17 when both are supplied")
				}
				r.Seats[i].Start = point
			}
			if err := reader.End(); err != nil {
				return err
			}
			if !anySet {
				return matchFieldError("chosenStarts", "omit extension 17 when every start is Auto")
			}
		case matchAlliedSightExtension:
			reader := netproto.NewReader(e.Data, matchFieldError)
			sight := MatchAlliedSight(reader.U8())
			if err := reader.End(); err != nil {
				return err
			}
			if sight != MatchAlliedSightSeparate && sight != MatchAlliedSightShared {
				return matchFieldError("alliedSight", "extension 18 contains 1 separate or 2 shared; omit it for the inherited default")
			}
			if r.AlliedSight != MatchAlliedSightVersion1 && r.AlliedSight != sight {
				return matchFieldError("alliedSight", "typed sight equal to extension 18 when both are supplied")
			}
			r.AlliedSight = sight
		case matchBuilderSpeedExtension:
			reader := netproto.NewReader(e.Data, matchFieldError)
			index := reader.U8()
			if err := reader.End(); err != nil {
				return err
			}
			factor, ok := content.BuilderSpeedStep(index)
			if !ok || index == 0 {
				return matchFieldError("mutators.builderSpeed", "extension 19 contains a Builder speed step index 1..5; omit it for the identity")
			}
			if typed := r.Mutators.BuilderSpeed; !typed.IsIdentity() && typed != factor {
				return matchFieldError("mutators.builderSpeed", "typed Builder speed equal to extension 19 when both are supplied")
			}
			r.Mutators.BuilderSpeed = factor
		case matchGeneratedMapExtension:
			if len(e.Data) == 0 {
				return matchFieldError("generatedMap", "omit extension 20 for a map that was not generated")
			}
			if r.GeneratedMap != nil && !bytes.Equal(r.GeneratedMap, e.Data) {
				return matchFieldError("generatedMap", "typed recipe equal to extension 20 when both are supplied")
			}
			r.GeneratedMap = append([]byte(nil), e.Data...)
		case matchSmartMakersExtension:
			reader := netproto.NewReader(e.Data, matchFieldError)
			on := reader.U8()
			if err := reader.End(); err != nil {
				return err
			}
			if on != 1 {
				return matchFieldError("mutators.smartMakers", "extension 21 contains 1 for on; omit it for off")
			}
			if typed := r.Mutators.SmartMakers; !typed.IsIdentity() && typed != content.SwitchOn() {
				return matchFieldError("mutators.smartMakers", "typed Smart makers equal to extension 21 when both are supplied")
			}
			r.Mutators.SmartMakers = content.SwitchOn()
		default:
			kept = append(kept, e)
		}
	}
	r.Extensions = kept
	return nil
}

// setupExtensions writes only nondefault choices. Their IDs precede the
// unrecognized metadata IDs, which admission keeps in ascending order.
func (r *MatchConfigRequest) setupExtensions() ([]MatchConfigExtension, error) {
	var fields []MatchConfigExtension
	if r.hasChosenStarts() {
		var data netproto.Writer
		for _, seat := range r.Seats {
			data.Bool(seat.Start.Set)
			if seat.Start.Set {
				data.U32(uint32(seat.Start.X))
				data.U32(uint32(seat.Start.Z))
			}
		}
		fields = append(fields, MatchConfigExtension{ID: matchChosenStartsExtension, Required: true, Data: data.Bytes()})
	}
	if r.AlliedSight != MatchAlliedSightVersion1 {
		fields = append(fields, MatchConfigExtension{ID: matchAlliedSightExtension, Required: true, Data: []byte{byte(r.AlliedSight)}})
	}
	if !r.Mutators.BuilderSpeed.IsIdentity() {
		index, ok := content.BuilderSpeedStepIndex(r.Mutators.BuilderSpeed)
		if !ok {
			return nil, matchFieldError("mutators.builderSpeed", "the identity or a factor on the Builder speed step list")
		}
		fields = append(fields, MatchConfigExtension{ID: matchBuilderSpeedExtension, Required: true, Data: []byte{index}})
	}
	if len(r.GeneratedMap) != 0 {
		fields = append(fields, MatchConfigExtension{ID: matchGeneratedMapExtension, Required: true, Data: append([]byte(nil), r.GeneratedMap...)})
	}
	if f := r.Mutators.SmartMakers; !f.IsIdentity() {
		if f != content.SwitchOn() {
			return nil, matchFieldError("mutators.smartMakers", "off or on")
		}
		fields = append(fields, MatchConfigExtension{ID: matchSmartMakersExtension, Required: true, Data: []byte{1}})
	}
	fields = append(fields, r.Extensions...)
	if len(fields) > matchConfigMaxExtensions {
		return nil, matchFieldError("extensions", fmt.Sprintf("at most %d records including gameplay choices", matchConfigMaxExtensions))
	}
	return fields, nil
}
