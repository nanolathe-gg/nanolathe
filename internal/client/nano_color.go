package client

import "github.com/nanolathe-gg/nanolathe/internal/frame"

// nanoTeamRamp caches palette matches, never particle state. The authored logo
// supplies the hue; each retail green shade supplies its peak intensity. This
// is Nanolathe presentation policy, not retail behaviour (GPU design §23.6).
type nanoTeamRamp struct {
	indices [7]uint8
	ready   bool
}

func (c *Client) nanoParticleColor(v frame.StripView) (uint8, [7]uint8, bool) {
	// Explicit Community lists take precedence over the Enhanced logo ramp.
	// Both remain host preferences, and disabling Community restores the
	// existing renderer-specific policy (GPU design §37.1).
	if v.Family == frame.StripFamilyNano && c.communityColors.options.TeamColorNanolathe {
		if !v.NanoOwnerColorKnown || v.NanoOwnerColor >= communityPlayerColors {
			return v.Fill, [7]uint8{}, false
		}
		index := c.communityStreamColor(v.NanoOwnerColor, true, v.Fill, v.ColorSample, v.ColorSequence)
		// Community assignments stay fixed for the particle lifetime. Feed the
		// same colour into Enhanced illumination instead of its stock green.
		return index, [7]uint8{index, index, index, index, index, index, index}, true
	}
	if !c.enhanced || !c.effects.TeamNanospray || v.Family != frame.StripFamilyNano || !v.NanoOwnerColorKnown || c.pal == nil || v.Fill < 0xa1 || v.Fill > 0xa7 {
		return v.Fill, [7]uint8{}, false
	}
	ink, ok := c.strategicTeamColor(v.NanoOwnerColor)
	if !ok {
		return v.Fill, [7]uint8{}, false
	}
	ramp := &c.nanoRamps[ink]
	if !ramp.ready {
		base := c.pal.Base[ink]
		peak := max(int(base[0]), int(base[1]), int(base[2]))
		if peak == 0 {
			return v.Fill, [7]uint8{}, false
		}
		for shade := range ramp.indices {
			green := c.pal.Base[0xa1+shade]
			brightness := max(int(green[0]), int(green[1]), int(green[2]))
			r, g, b := int(base[0])*brightness/peak, int(base[1])*brightness/peak, int(base[2])*brightness/peak
			bestDistance := int(^uint(0) >> 1)
			for index, candidate := range c.pal.Base {
				dr, dg, db := int(candidate[0])-r, int(candidate[1])-g, int(candidate[2])-b
				distance := dr*dr + dg*dg + db*db
				if distance < bestDistance {
					ramp.indices[shade], bestDistance = uint8(index), distance
				}
			}
		}
		ramp.ready = true
	}
	return ramp.indices[v.Fill-0xa1], ramp.indices, true
}
