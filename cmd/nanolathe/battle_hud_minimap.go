package main

// The side rail's minimap: the radar surface rebuild, its contact admission
// rules and the blit into the authored rectangle [03 §3.7-§3.9].

import (
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// minimapRect returns the battle composer's fixed radar canvas rectangle.
func (h *retailBattleHUD) minimapRect() (hud.Rect, bool) {
	if h == nil || !h.minimapAnchorOK {
		return hud.Rect{}, false
	}
	left, top, right, bottom := h.minimapAnchor.Ordered()
	if right < left || bottom < top {
		return hud.Rect{}, false
	}
	return h.minimapAnchor, true
}

// radarMapPixel performs the retail signed high-word narrowing before radar
// projection. Keeping the narrowing explicit matters for map coordinates below
// zero and for values whose high word does not fit an int32 map coordinate [03
// §3.9].
func radarMapPixel(v numeric.Fixed) int32 {
	return int32(int16(int64(v) >> 16))
}

func radarGAFFrame(entry *formats.GAFEntry, index int) *formats.GAFFrame {
	if entry == nil || index < 0 || index >= len(entry.Frames) {
		return nil
	}
	return entry.Frames[index].Frame
}

func radarGAFFrameCount(entry *formats.GAFEntry) int {
	if entry == nil {
		return 0
	}
	return len(entry.Frames)
}

// blitRadarGAF copies an authored marker around its projected anchor. GAF
// pixels are already PALETTE.PAL indexes; transparent bytes are skipped and
// no GUI remap is applied [03 §3.9][fmt gaf].
func blitRadarGAF(dst *render.RadarSurface, anchorX, anchorY int32, f *formats.GAFFrame) {
	if dst == nil || f == nil {
		return
	}
	left, top := int(anchorX)-int(f.XOffset), int(anchorY)-int(f.YOffset)
	for y := 0; y < int(f.Height); y++ {
		for x := 0; x < int(f.Width); x++ {
			p, ok := f.At(x, y)
			if ok {
				dst.Set(left+x, top+y, p)
			}
		}
	}
}

func radarContactAdmitted(c render.MinimapContact, blink render.BlinkState) bool {
	// Player zero is a valid owner, but zero-valued unpublished records must
	// not become visible merely because the local player is also zero. The
	// authoritative visible/friendly bits cover genuine local-player contacts;
	// nonzero owner identity is the only owner bypass at this seam [03 §3.9].
	admit := c.Visible || c.Options&(1<<9) != 0 || c.MinimapMode&3 == 0 || c.Status&0x300 != 0 || c.Owner != 0 && c.Owner == c.LocalPlayer
	// The blink term is the whole of the second half of the gate: the blip draws
	// when the per-unit blink-suppress countdown reads zero OR the shared blink
	// phase bit is set. Definition `stealth` is not a term of it — stealth is
	// the sensor phase's contact-callback reject, which is where a stealthy
	// unit loses the seen bit that would have admitted it, and one admitted by
	// line of sight draws a steady blip [03 §3.9][03 R-VIS-01 §5].
	return admit && (c.BlinkSuppress == 0 || blink.Phase&1 != 0)
}

func radarPublishedContactVisible(c frame.RadarContactView, local uint8) bool {
	// Retail's second pass over the projectile/feature list admits through
	// the mode-selected local player visibility source at the projected
	// cell, with owner-local identity as the only bypass [03 §3.9]. The
	// friendly-contact status pair 0x300 is a term of the UNIT pass's blip
	// gate (radarContactAdmitted) only; a feature's status word and a
	// projectile's flags word do not carry those bits with that meaning, so
	// this second-pass gate must not read them.
	return c.Visible || c.OwnerKnown && c.Owner == local
}

func radarContactRangeEnabled(c frame.RadarContactView) bool {
	// The publisher resolves the selected/range status and activation definition
	// gate before the frame boundary. Consume that immutable result directly;
	// presentation does not reconstruct it from mutable unit state [03 §3.9].
	return c.RangeStatus
}

func radarProjectileDot(c frame.RadarContactView) bool {
	// Retail selects dot-vs-marker art purely from the shared list record's
	// status bits 29/30 (clear takes the dot, itself suppressed by bit 0x40)
	// [03 §3.9] over one kind-agnostic projectile/feature list. Nanolathe's
	// publisher does not carry that shared-list record status for a feature:
	// frame.RadarContactView.Status for a RadarContactFeature is the feature
	// INSTANCE's own status word from internal/features (only bit 0x01 is
	// ever written there, by service.go/burn.go), not the capture-path
	// record whose bits 29/30 retail's selector reads. A projectile's Status
	// is that record's Flags word, which does carry retail's bits. Until the
	// feature capture path publishes the shared-list record status, gate the
	// dot path on Kind so a feature's unrelated status word cannot be
	// misread as clear bits 29/30.
	//
	// TODO(question): what do bits 29/30 hold for a feature entry in
	// retail's shared projectile/feature list record? Settle by tracing the
	// projectile/feature capture path's feature-record write [03 §3.9][06].
	return c.Kind == frame.RadarContactProjectile && c.Status&(1<<29|1<<30|0x40) == 0
}

func (h *retailBattleHUD) radarOwnerFrameIndex(contact frame.RadarContactView, frameCount int) int {
	if frameCount <= 0 || !contact.PaletteKnown || int(contact.Palette) >= frameCount {
		return -1
	}
	return int(contact.Palette)
}

// rebuildRadar consumes only the committed frame's radar payload. In
// particular, circles come from the published callback list and contacts are
// not reconstructed from the live unit or visibility services [03 §3.4][03
// §3.6][03 §3.9].
func (h *retailBattleHUD) rebuildRadar(b *battleSession, cur *frame.Frame, layout camera.Minimap) *render.RadarSurface {
	if h == nil || b == nil || b.sess == nil || h.radar == nil || cur == nil {
		return nil
	}
	// Consume only the committed phase. Presentation may redraw the same frame
	// repeatedly without advancing or otherwise owning the cadence [R-CORE-03]
	// [03 §3.6][I6].
	h.radar.SetBlinkPhase(cur.Radar.BlinkPhase)
	if cur.Visibility.Valid {
		h.radar.RebuildMappedVersion(cur.Visibility.WordVisible, cur.Visibility.Visible, cur.Visibility.MappingVersion)
	}
	// The committed contacts are the whole circle input: the sensor phase has no
	// surface of its own and rasterizes nothing [03 §3.10] correction of
	// 2026-08-29. FINAL is wiped from MAPPED and rebuilt from these records
	// every tick, so no stale circle can survive a frame.
	contacts := make([]render.MinimapContact, 0, len(cur.Radar.Contacts))
	regularArt := make([]*formats.GAFFrame, 0, len(cur.Radar.Contacts))
	commanderArt := make([]*formats.GAFFrame, 0, len(cur.Radar.Contacts))
	blink := h.radar.Blink()
	for _, published := range cur.Radar.Contacts {
		// The renderer's contact adapter owns the unit/commander/ring passes.
		// Projectile and feature records are applied below, after rings, in the
		// order required by the retail contacts pass [03 §3.9].
		if published.Kind != frame.RadarContactUnit {
			continue
		}
		// The selected-unit circle gate [03 §3.9]. Only a selected unit's
		// authored distances reach layer 4, and an on/off-capable one must also
		// be active; the publisher folded both terms before the frame boundary.
		rangeCircles := radarContactRangeEnabled(published)
		contact := render.MinimapContact{
			WorldX:      radarMapPixel(published.X),
			WorldZ:      radarMapPixel(published.Z),
			WorldY:      radarMapPixel(published.Y),
			Owner:       published.Owner,
			IsCommander: published.Commander, Stealth: published.Stealth,
			// RangeStatus is the selected-unit circle gate; Stealth stays separate
			// because it is a blip-blink term, not a circle term [03 §3.9].
			RangeStatus: rangeCircles, Status: published.Status,
			BlinkSuppress: published.BlinkSuppress, Visible: published.Visible,
			LocalPlayer:  cur.Selection.LocalPlayer,
			RawDistRadar: published.RadarDistance, RawDistSonar: published.SonarDistance,
			RawDistJamR: published.RadarJam, RawDistJamS: published.SonarJam,
			MinimapMode: b.minimapMaskWord(),
		}
		if radarContactAdmitted(contact, blink) {
			regularArt = append(regularArt, radarGAFFrame(h.radarBlipGAF, h.radarOwnerFrameIndex(published, radarGAFFrameCount(h.radarBlipGAF))))
			if contact.IsCommander {
				commanderArt = append(commanderArt, radarGAFFrame(h.radarCommanderGAF, 0))
			}
		}
		if len(published.Rings) != 0 {
			ring := published.Rings[0]
			contact.RingEnabled = ring.Enabled
			contact.RingDashed = ring.Dashed
			contact.RingRange = ring.Range
		}
		contacts = append(contacts, contact)
		for i := 1; i < len(published.Rings); i++ {
			ring := published.Rings[i]
			ringContact := render.MinimapContact{
				WorldX: contact.WorldX, WorldZ: contact.WorldZ, WorldY: contact.WorldY,
				Owner: contact.Owner, Status: contact.Status, Stealth: contact.Stealth,
				RangeStatus: contact.RangeStatus, BlinkSuppress: contact.BlinkSuppress,
				Visible: contact.Visible, LocalPlayer: contact.LocalPlayer, MinimapMode: contact.MinimapMode,
				RingEnabled: ring.Enabled, RingDashed: ring.Dashed, RingRange: ring.Range,
			}
			contacts = append(contacts, ringContact)
			if radarContactAdmitted(ringContact, blink) {
				regularArt = append(regularArt, nil)
			}
		}
	}
	playW, playH, ok := b.sess.PlayArea()
	if !ok {
		return nil
	}
	regularIndex, commanderIndex := 0, 0
	returnFinal := h.radar.RebuildFinalVersion(layout, playW, playH, contacts, func(dst *render.RadarSurface, x, y int, p byte, commander bool) {
		if commander {
			if commanderIndex < len(commanderArt) {
				blitRadarGAF(dst, int32(x), int32(y), commanderArt[commanderIndex])
			}
			commanderIndex++
			return
		}
		if regularIndex < len(regularArt) {
			blitRadarGAF(dst, int32(x), int32(y), regularArt[regularIndex])
		}
		regularIndex++
	}, h.paletteIndex(10), h.paletteIndex(12), h.paletteIndex(15), uint64(cur.Tick))
	if !returnFinal {
		return nil
	}
	final := h.radar.Final()
	if final == nil {
		return nil
	}
	// The projectile/feature pass follows rings. The published payload carries
	// the status and owner/visibility gates. Retail selects dot-vs-marker art
	// purely from the shared list record's status bits [03 §3.9], but our
	// publisher does not carry that shared-list status for a feature (see
	// radarProjectileDot); gate explicitly on Kind so a projectile's status
	// word selects the dot/marker split while every feature draws its marker.
	for _, published := range cur.Radar.Contacts {
		if published.Kind == frame.RadarContactUnit || !radarPublishedContactVisible(published, cur.Selection.LocalPlayer) {
			continue
		}
		rx, ry := render.RadarProjection(radarMapPixel(published.X), radarMapPixel(published.Z), radarMapPixel(published.Y), playW, playH, layout)
		if radarProjectileDot(published) {
			final.Set(int(rx), int(ry), h.paletteIndex(14))
			continue
		}
		if published.Kind == frame.RadarContactFeature || published.Kind == frame.RadarContactProjectile {
			index := h.radarOwnerFrameIndex(published, radarGAFFrameCount(h.radarFeatureGAF))
			blitRadarGAF(final, rx, ry, radarGAFFrame(h.radarFeatureGAF, index))
		}
	}
	return final
}

func (h *retailBattleHUD) drawMinimap(c *client.Client, b *battleSession, cur *frame.Frame) {
	if h == nil || c == nil || b == nil || b.sess == nil || b.cam == nil || cur == nil {
		return
	}
	layout, dst, ok := b.minimapLayout()
	if !ok {
		return
	}
	surf := h.rebuildRadar(b, cur, layout)
	if surf == nil {
		return
	}
	// Drawing and input receive the same layout and destination rectangle.
	c.DrawMinimapLayoutVersion(surf, dst, layout, h.radar.FinalIdentity(), h.radar.FinalRevision())
	// Then the viewport rectangle, exactly as retail's minimap repaint pre-pass
	// strokes the camera-to-radar rectangle over the copied radar surface
	// [03 R-MM-01 §1][03 R-COMP-02 §5]. The five-pixel cross that used to be
	// drawn here instead is a film-mode diagnostic the **world** composer draws
	// over the game viewport [03 §3.12] — a different figure.
	playW, playH, ok := b.sess.PlayArea()
	if !ok {
		return
	}
	if marker, ok := hud.MinimapViewportRect(b.cam, layout, playW, playH, dst); ok {
		c.DrawMinimapViewportRect(dst, marker, h.paletteIndex(hud.ViewportMarkerLogicalColor))
	}
}
