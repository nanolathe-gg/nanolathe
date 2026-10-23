package session

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/netproto"
)

// MatchConfigExtension is one record in configuration version 2's optional
// tail (docs/DESIGN_MULTIPLAYER.md §8.6). Unknown optional records are opaque:
// they affect identity but have no simulation effect. Unknown required
// records are refused, so gameplay never silently falls back to another rule.
// Data is copied when a configuration is resolved, decoded or requested.
type MatchConfigExtension struct {
	ID       uint16
	Required bool
	Data     []byte
}

const (
	matchConfigFirstExtension  = 17
	matchConfigMaxExtensions   = 128
	matchConfigMaxExtensionLen = 64 << 10
	matchConfigRequiredFlag    = 1
)

// Fields 1–16 are frozen positional fields, including RoomContent at 16.
// Required IDs 17 and 18 carry chosen starts and allied sight through their
// typed fields and existing SeatRules seam; required ID 19 carries the Builder
// speed mutator, whose step list field 11 cannot spell
// (DESIGN_MODS_MUTATORS "Builder speed"), and required ID 21 the Smart makers
// switch ("Smart makers"). Future gameplay choices follow
// that same required, typed admission contract (§8.6, DESIGN_GAMEPLAY_RULES §9).

func cloneMatchExtensions(extensions []MatchConfigExtension) []MatchConfigExtension {
	if len(extensions) == 0 {
		return nil
	}
	out := make([]MatchConfigExtension, len(extensions))
	for i, e := range extensions {
		out[i] = e
		out[i].Data = append([]byte(nil), e.Data...)
	}
	return out
}

func validateMatchExtensions(extensions []MatchConfigExtension) error {
	if len(extensions) > matchConfigMaxExtensions {
		return matchFieldError("extensions", fmt.Sprintf("at most %d records", matchConfigMaxExtensions))
	}
	var previous uint16
	for _, e := range extensions {
		if err := validateMatchExtensionID(e.ID, previous); err != nil {
			return err
		}
		if matchSetupExtension(e.ID) && !e.Required {
			return matchFieldError(fmt.Sprintf("extensions[%d].flags", e.ID), "required for a gameplay choice")
		}
		if e.Required && !matchSetupExtension(e.ID) {
			return unknownRequiredMatchExtension(e.ID)
		}
		if len(e.Data) > matchConfigMaxExtensionLen {
			return matchFieldError(fmt.Sprintf("extensions[%d].data", e.ID), fmt.Sprintf("at most %d bytes", matchConfigMaxExtensionLen))
		}
		previous = e.ID
	}
	return nil
}

func validateMatchExtensionID(id, previous uint16) error {
	if id < matchConfigFirstExtension {
		return matchFieldError(fmt.Sprintf("extensions[%d].id", id), "an extension ID of at least 17; fields 1–16 are reserved")
	}
	if id <= previous {
		return matchFieldError(fmt.Sprintf("extensions[%d].id", id), "extension IDs in strictly ascending order")
	}
	return nil
}

func unknownRequiredMatchExtension(id uint16) error {
	return matchFieldError(fmt.Sprintf("extensions[%d]", id), fmt.Sprintf("support for required match configuration extension %d; this build does not recognize it", id))
}

// A configuration with no extensions has no tail, preserving all existing
// version-2 encodings and identities. A nonempty tail ends at ID zero with
// no padding, unlike a replay header (§8.6, §10).
func writeMatchExtensions(w *netproto.Writer, extensions []MatchConfigExtension) {
	if len(extensions) == 0 {
		return
	}
	for _, e := range extensions {
		w.U16(e.ID)
		var flags uint8
		if e.Required {
			flags = matchConfigRequiredFlag
		}
		w.U8(flags)
		w.U32(uint32(len(e.Data)))
		w.Raw(e.Data)
	}
	w.U16(0)
}

func readMatchExtensions(r *netproto.Reader) []MatchConfigExtension {
	var extensions []MatchConfigExtension
	var previous uint16
	for r.Err() == nil {
		id := r.U16()
		if r.Err() != nil {
			break
		}
		if id == 0 {
			if len(extensions) == 0 {
				r.Fail("no extension tail when there are no extension records")
			}
			return extensions
		}
		if len(extensions) == matchConfigMaxExtensions {
			r.Fail(fmt.Sprintf("at most %d extension records", matchConfigMaxExtensions))
			break
		}
		if err := validateMatchExtensionID(id, previous); err != nil {
			r.Abort(err)
			break
		}
		flags := r.U8()
		if r.Err() == nil && flags & ^uint8(matchConfigRequiredFlag) != 0 {
			r.FailAt(r.Offset()-1, "only bit 0 (Required) in extension flags")
		}
		if r.Err() == nil && flags&matchConfigRequiredFlag != 0 && !matchSetupExtension(id) {
			r.Abort(unknownRequiredMatchExtension(id))
		}
		n := r.U32()
		if r.Err() == nil && n > matchConfigMaxExtensionLen {
			r.Fail(fmt.Sprintf("extension %d payload of at most %d bytes", id, matchConfigMaxExtensionLen))
		}
		data := r.Raw(int(n))
		if r.Err() != nil {
			break
		}
		extensions = append(extensions, MatchConfigExtension{ID: id, Required: flags&matchConfigRequiredFlag != 0, Data: append([]byte(nil), data...)})
		previous = id
	}
	return nil
}
