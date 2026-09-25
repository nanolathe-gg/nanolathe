package main

// The megamap's composition: the committed frame drawn into one indexed
// surface over the battle viewport (DESIGN_INTERFACE_HUD_INPUT §3.15). Layer
// order and arithmetic are the shipped ProTA 4.8 megamap's
// ([draw-engine-interface](../../research/extensions/draw-engine-interface.md#prota-48-shipped-megamap));
// host choices are named where they are made. It reads only the committed
// frame, the immutable catalog and map, and host state [I6].

import (
	"fmt"
	"math"
	"os"
	"strings"
	"sync/atomic"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

var megamapSurfaceIdentity atomic.Uint64

// megamapComposition is the retained picture, fog and composed surface.
type megamapComposition struct {
	picture        []byte
	pictureW       int
	pictureH       int
	pictureTerrain *world.Terrain

	fog    []byte
	fogKey megamapFogKey

	image    []byte
	surface  []byte
	key      megamapComposeKey
	valid    bool
	identity uint64
	revision uint64
}

type megamapFogKey struct {
	picture         *byte
	w, h            int
	source, version uint64
	frame           *frame.Frame
	viewer          uint8
}

type megamapComposeKey struct {
	frame        *frame.Frame
	tick         uint32
	lens         camera.MegamapLens
	hover        pool.Handle
	tracked      pool.Handle
	shift        bool
	box          bool
	bx0, by0     int32
	bx1, by1     int32
	options      megamapOptions
	icons        *client.MegamapIconBank
	radarOptions uint32
	pal          *palette.Tables
	ghost        megamapGhostKey
}

// megamapGhostKey is the placement ghost's input: the armed footprint, the
// site-valid bit and the pointer it follows. It is zero with no build armed
// or the pointer off the image.
type megamapGhostKey struct {
	shown        bool
	x, y         int32
	footX, footZ int32
	valid        bool
}

// megamapIconBank loads the pictures once, from the same resolved icon
// configuration the strategic icons use (DESIGN_GPU_RENDERER §18.7).
func (b *battleSession) megamapIconBank() *client.MegamapIconBank {
	if b.megamap.iconsLoaded {
		return b.megamap.icons
	}
	b.megamap.iconsLoaded = true
	var pal *palette.Tables
	if b.hud != nil {
		pal = b.hud.pal
	}
	path, err := megamapIconConfigPath(b.hostPreferences().StrategicIconConfig, b.iconRoots)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		path = ""
	}
	bank, err := client.LoadMegamapIconBank(b.cat, path, pal)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	b.megamap.icons = bank
	return bank
}

// megamapIconConfigPath resolves the icon configuration exactly as
// battleStrategicIcons does: an explicit preference wins, and an empty one
// discovers the running content's own configuration.
func megamapIconConfigPath(preference string, roots []string) (string, error) {
	if strings.TrimSpace(preference) == "" {
		found, err := automaticStrategicIconConfig(roots)
		if err != nil {
			return "", err
		}
		preference = found
	}
	return discoverStrategicIconConfig(preference)
}

// drawMegamap records the overview over the battle viewport. With the view
// hidden it returns at once, so the Zoom overview and a hidden megamap cost
// nothing.
func (b *battleSession) drawMegamap(c *client.Client, cur *frame.Frame) {
	if b == nil || !b.megamap.shown || c == nil || cur == nil || cur.Result.Ended || b.hud == nil || b.hud.pal == nil || b.sess == nil || b.sess.World == nil {
		return
	}
	lens := b.megamapLens()
	if !lens.Valid() || lens.ViewW <= 0 || lens.ViewH <= 0 {
		return
	}
	m := &b.megamap.composition
	if m.identity == 0 {
		m.identity = megamapSurfaceIdentity.Add(1) | 1<<62
	}
	icons := b.megamapIconBank()
	// Weapon rings and the queued-order overlay follow Shift's physical state
	// [draw-engine-interface "Rings", "Selection and order overlay"]; the
	// battle input writes it from the keyboard every frame.
	state := b.battleState().Input
	key := megamapComposeKey{
		frame: cur, tick: cur.Tick, lens: lens, hover: b.footerHoverUnit, shift: state.ShiftHeld,
		box: b.megamap.boxActive, options: b.megamapOptions(), icons: icons,
		radarOptions: b.radarOptions, pal: b.hud.pal,
	}
	if b.cam != nil {
		key.tracked = b.cam.Tracked()
	}
	if key.box {
		key.bx0, key.by0, key.bx1, key.by1 = b.megamap.boxX0, b.megamap.boxY0, b.megamap.boxX1, b.megamap.boxY1
	}
	if b.battleState().PlacementArmed() && lens.ContainsScreen(state.PointerX, state.PointerY) && b.megamapOwnsPointer(state.PointerX, state.PointerY) {
		key.ghost = megamapGhostKey{shown: true, x: state.PointerX, y: state.PointerY, footX: state.BuildFootX, footZ: state.BuildFootZ, valid: state.BuildOK}
	}
	if !m.valid || m.key != key {
		b.composeMegamap(cur, lens, icons, key)
		m.key, m.valid = key, true
		m.revision++
	}
	c.DrawMegamapSurface(m.surface, lens.ViewX, lens.ViewY, lens.ViewW, lens.ViewH, m.identity, m.revision)
}

// composeMegamap rebuilds the surface: margins, terrain, fog, projectiles,
// unit icons with their rings, then the selection and order overlay.
func (b *battleSession) composeMegamap(cur *frame.Frame, lens camera.MegamapLens, icons *client.MegamapIconBank, key megamapComposeKey) {
	m := &b.megamap.composition
	w, h := int(lens.W), int(lens.H)
	terrain := b.sess.World
	// The terrain picture is the shipped build's point sample of the tile art
	// over the play area, built once per battle. Nanolathe rebuilds it only if
	// the image size changes (render.BuildMegamapPicture).
	if m.picture == nil || m.pictureW != w || m.pictureH != h || m.pictureTerrain != terrain {
		pic := render.BuildMegamapPicture(terrain, lens.ExtentW, lens.ExtentH, w, h)
		m.picture, m.pictureW, m.pictureH, m.pictureTerrain = nil, w, h, terrain
		if len(pic) == w*h {
			m.picture = pic
		} else {
			m.picture = make([]byte, w*h)
			for i := range m.picture {
				m.picture[i] = render.MegamapMarginIndex
			}
		}
		m.fogKey = megamapFogKey{}
	}
	fogKey := megamapFogKey{picture: &m.picture[0], w: w, h: h, source: cur.Visibility.MappingSource, version: cur.Visibility.MappingVersion, viewer: cur.ViewingPlayer}
	if fogKey.source == 0 {
		// An unversioned visibility publication is dynamic.
		fogKey.frame = cur
	}
	if len(m.fog) != w*h || m.fogKey != fogKey {
		if cap(m.fog) < w*h {
			m.fog = make([]byte, w*h)
		}
		m.fog = m.fog[:w*h]
		sea := int32(cur.Visibility.SeaLevel >> 16)
		if cur.Visibility.Valid {
			render.ComposeMegamapFog(m.fog, m.picture, w, h, cur.Visibility.WordVisible, cur.Visibility.Visible, int(terrain.CellW/2), int(terrain.CellH/2),
				float32(lens.ExtentW)/32, float32(lens.ExtentH)/32, cur.ViewingPlayer, sea, &b.hud.pal.Gray)
		} else {
			copy(m.fog, m.picture)
		}
		m.fogKey = fogKey
	}
	if cap(m.image) < w*h {
		m.image = make([]byte, w*h)
	}
	m.image = m.image[:w*h]
	copy(m.image, m.fog)
	b.drawMegamapProjectiles(cur, lens, icons, key.options)
	b.drawMegamapUnits(cur, lens, icons, key)
	b.drawMegamapOverlay(cur, lens, key)
	// Margins in index 95, then the image at its fitted offset.
	vw, vh := int(lens.ViewW), int(lens.ViewH)
	if cap(m.surface) < vw*vh {
		m.surface = make([]byte, vw*vh)
	}
	m.surface = m.surface[:vw*vh]
	for i := range m.surface {
		m.surface[i] = render.MegamapMarginIndex
	}
	ox, oy := int(lens.X-lens.ViewX), int(lens.Y-lens.ViewY)
	for y := 0; y < h && oy+y < vh; y++ {
		copy(m.surface[(oy+y)*vw+ox:(oy+y)*vw+min(vw, ox+w)], m.image[y*w:y*w+w])
	}
}

// megamapDot is the player dot colour for a logo colour selector: the
// `Player1..10DotColors` table is indexed by logo colour, not player slot
// [draw-engine-interface "Player colours and markers"].
func megamapDot(o megamapOptions, selector uint8, known bool) byte {
	if !known || int(selector) >= len(o.dots) {
		return o.dots[0]
	}
	return o.dots[selector]
}

// drawMegamapProjectiles visits every published projectile. Admission is the
// shipped build's: the cell bound against the viewing player's LOS grid, then
// owner or ally, current sight, Unmapped, or the mapping word
// (render.MegamapProjectileAdmitted). A weapon with `twophase`, `cruise` and
// `targetable` together draws `nukeicon` in the owner's colour, or the
// minimap's `nuclogo` frame without one; any other draws a 2×2 block in the
// minimap's projectile colour [draw-engine-interface "Projectiles"].
func (b *battleSession) drawMegamapProjectiles(cur *frame.Frame, lens camera.MegamapLens, icons *client.MegamapIconBank, o megamapOptions) {
	m := &b.megamap.composition
	w, h := int(lens.W), int(lens.H)
	surf := &render.RadarSurface{W: w, H: h, Bits: m.image}
	sight := render.MegamapProjectileSight{
		Mode: cur.Radar.MappingLOS, W: cur.Visibility.W, H: cur.Visibility.H,
		Current: cur.Visibility.Visible, Mapped: cur.Visibility.WordVisible, Viewer: cur.ViewingPlayer,
	}
	dotColor := b.hud.paletteIndex(14)
	contact := 0
	for i := range cur.Projectiles {
		p := &cur.Projectiles[i]
		// The projectile contacts are published one per projectile in the
		// same order; they carry the owner's colour selector.
		var rc *frame.RadarContactView
		for contact < len(cur.Radar.Contacts) {
			c := &cur.Radar.Contacts[contact]
			contact++
			if c.Kind == frame.RadarContactProjectile {
				if c.Handle == p.Handle {
					rc = c
				}
				break
			}
		}
		x, y, z := radarMapPixel(p.X), radarMapPixel(p.Y), radarMapPixel(p.Z)
		allied := p.OwnerKnown && megamapAllied(cur, p.Owner)
		if !cur.Visibility.Valid {
			// No published grid to bound or test against: only the owner and
			// allies pass.
			if !allied {
				continue
			}
		} else if cx, cz, inRange := render.MegamapProjectileCell(x, y, z, cur.Visibility.W, cur.Visibility.H); !inRange || !render.MegamapProjectileAdmitted(allied, cx, cz, sight) {
			continue
		}
		px, py := lens.Project(x, y, z)
		nuke := false
		if b.cat != nil {
			if wd, ok := b.cat.WeaponByID(p.WeaponID); ok && wd != nil {
				nuke = wd.TwoPhase && wd.Cruise && wd.Targetable
			}
		}
		if !nuke {
			render.FillMegamapBlock(m.image, w, h, int(px), int(py), 2, dotColor)
			continue
		}
		known := rc != nil && rc.PaletteKnown
		selector := uint8(0)
		if rc != nil {
			selector = rc.Palette
		}
		if icon := icons.Nuke(); icon != nil {
			render.BlitMegamapIcon(m.image, w, h, int(lens.RowPitch()), icon, int(px)-icon.W/2, int(py)-icon.H/2, render.MegamapIconNormal, megamapDot(o, selector, known))
			continue
		}
		if rc != nil {
			frameIndex := b.hud.radarOwnerFrameIndex(*rc, radarGAFFrameCount(b.hud.radarMarkerGAF))
			blitRadarGAF(surf, px, py, radarGAFFrame(b.hud.radarMarkerGAF, frameIndex))
		}
	}
}

// megamapPicture is the picture a unit contact draws: an identified unit's
// configured row (or `unknow`), otherwise `nothing`. Without a configured
// `nothing` picture the minimap's own `radlogo` frame stands in (host choice).
func (b *battleSession) megamapPicture(cur *frame.Frame, p *frame.RadarContactView, u *frame.UnitView) (*render.MegamapIcon, *formats.GAFFrame) {
	icons := b.megamapIconBank()
	if megamapIdentified(cur, u) {
		return icons.Unit(u.DefName, u.DefID), nil
	}
	if icon := icons.Nothing(); icon != nil {
		return icon, nil
	}
	if b.hud == nil {
		return nil, nil
	}
	return nil, radarGAFFrame(b.hud.radarBlipGAF, b.hud.radarOwnerFrameIndex(*p, radarGAFFrameCount(b.hud.radarBlipGAF)))
}

// megamapPictureSize is the current picture's size, for the hover box.
func (b *battleSession) megamapPictureSize(cur *frame.Frame, p *frame.RadarContactView, u *frame.UnitView) (int, int) {
	icon, gaf := b.megamapPicture(cur, p, u)
	switch {
	case icon != nil:
		return icon.W, icon.H
	case gaf != nil:
		return int(gaf.Width), int(gaf.Height)
	}
	return 0, 0
}

// drawMegamapUnits draws each admitted contact with a definition in list
// order: the icon, then its hover circle and rings.
func (b *battleSession) drawMegamapUnits(cur *frame.Frame, lens camera.MegamapLens, icons *client.MegamapIconBank, key megamapComposeKey) {
	m := &b.megamap.composition
	w, h := int(lens.W), int(lens.H)
	pitch := lens.RowPitch()
	surf := &render.RadarSurface{W: w, H: h, Bits: m.image}
	o := key.options
	blinkClear := cur.Radar.BlinkPhase&1 == 0
	slots := b.megamapUnitSlots(cur)
	for i := range cur.Radar.Contacts {
		p := &cur.Radar.Contacts[i]
		if !b.megamapContactAdmitted(cur, p) {
			continue
		}
		u, ok := megamapUnitView(cur, slots, p.Handle)
		if !ok || u.DefID == 0 && u.DefName == "" {
			continue
		}
		x, y, z := radarMapPixel(p.X), radarMapPixel(p.Y), radarMapPixel(p.Z)
		// The centre is the position less half the footprint in each
		// horizontal axis, with the half-height shear [draw-engine-interface
		// "Unit icons"].
		cx, cy := lens.Project(x-int32(u.FootX)*8, y, z-int32(u.FootZ)*8)
		hovered := p.Handle != 0 && p.Handle == key.hover
		icon, gaf := b.megamapPicture(cur, p, u)
		// UnderAttackFlash hides only the icon pixels while the unit's
		// damage-blink byte is nonzero and the committed blink phase is clear
		// [draw-engine-interface "Under-attack flash"][01 R-CORE-03].
		hidden := o.flash && p.BlinkSuppress != 0 && blinkClear
		pw, ph := 0, 0
		switch {
		case icon != nil:
			pw, ph = icon.W, icon.H
			if !hidden {
				state := render.MegamapIconNormal
				if p.Selected {
					state = render.MegamapIconSelected
				} else if hovered {
					state = render.MegamapIconHovered
				}
				render.BlitMegamapIcon(m.image, w, h, int(pitch), icon, int(cx)-icon.W/2, int(cy)-icon.H/2, state, megamapDot(o, p.Palette, p.PaletteKnown))
			}
		case gaf != nil:
			pw, ph = int(gaf.Width), int(gaf.Height)
			if !hidden {
				blitRadarGAF(surf, cx, cy, gaf)
			}
		}
		if hovered && icon != nil && icon.Circle {
			// The hover circle's radius is the truncated distance from the
			// icon centre to its corner [draw-engine-interface "Hover"].
			r := int(math.Sqrt(float64(pw*pw+ph*ph)) / 2)
			render.DrawMegamapCircle(m.image, w, h, int(cx), int(cy), r, icon.Hover)
		}
		b.drawMegamapRings(cur, lens, p, u, int(cx), int(cy), hovered, key)
	}
}

// Ring-colour slots, in the `Megamap*Color` key order.
const (
	megamapRingWeapon1 = iota
	megamapRingWeapon2
	megamapRingWeapon3
	megamapRingRadar
	megamapRingSonar
	megamapRingRadarJam
	megamapRingSonarJam
	megamapRingAntinuke
)

// megamapRingColors resolves the eight ring colours. Each `Megamap*Color`
// setting of -1 keeps the research default — colour-map entry 6 for weapon
// slot 1, raw palette index 1 for slots 2 and 3, entry 10 for radar and sonar,
// entry 12 for both jammers and entry 15 for interceptor coverage — and any
// other value is the palette index itself. Colour-map entries resolve through
// the logical-to-physical map [draw-engine-interface "`Megamap*Color` keys"]
// [03 R-MM-01 §2].
func megamapRingColors(settingsColors [8]int, logical func(byte) byte) [8]byte {
	out := [8]byte{logical(6), 1, 1, logical(10), logical(10), logical(12), logical(12), logical(15)}
	for i, v := range settingsColors {
		if v >= 0 && v <= 255 {
			out[i] = byte(v)
		}
	}
	return out
}

// drawMegamapRings draws a unit's sensor, interceptor and weapon rings
// [draw-engine-interface "Rings"]. Radii use the four-aligned pitch over the
// horizontal extent.
func (b *battleSession) drawMegamapRings(cur *frame.Frame, lens camera.MegamapLens, p *frame.RadarContactView, u *frame.UnitView, cx, cy int, hovered bool, key megamapComposeKey) {
	if b.cat == nil || !megamapAllied(cur, p.Owner) {
		return
	}
	def, ok := b.cat.Unit(u.DefName)
	if !ok || def == nil {
		return
	}
	m := &b.megamap.composition
	w, h := int(lens.W), int(lens.H)
	pitch := lens.RowPitch()
	radius := func(d int32) int { return int(render.MegamapRingRadius(d, pitch, lens.ExtentW)) }
	t := key.options.thresholds
	colors := megamapRingColors(key.options.colors, b.hud.paletteIndex)
	if p.Selected {
		// Selected allied unit: no activation test, unlike the retail
		// minimap's circle gate.
		radar, sonar, radarJam, sonarJam := int32(int16(def.RadarDistance)), int32(int16(def.SonarDistance)), int32(int16(def.RadarDistanceJam)), int32(int16(def.SonarDistanceJam))
		dRadar, dSonar, dRadarJam, dSonarJam := render.MegamapSensorRings(t, radar, sonar, radarJam, sonarJam)
		if dRadar {
			render.DrawMegamapCircle(m.image, w, h, cx, cy, radius(radar), colors[megamapRingRadar])
		}
		if dSonar {
			render.DrawMegamapCircle(m.image, w, h, cx, cy, radius(sonar), colors[megamapRingSonar])
		}
		if dRadarJam {
			render.DrawMegamapCircle(m.image, w, h, cx, cy, radius(radarJam), colors[megamapRingRadarJam])
		}
		if dSonarJam {
			render.DrawMegamapCircle(m.image, w, h, cx, cy, radius(sonarJam), colors[megamapRingSonarJam])
		}
		if def.AntiWeapons {
			ordinal := 0
			for _, wd := range [3]*content.WeaponDef{def.Weapon1Def, def.Weapon2Def, def.Weapon3Def} {
				if wd == nil {
					continue
				}
				// The published ring at this ordinal carries the slot's
				// interceptor indicator, the retail minimap's dashed state.
				dashed := wd.Interceptor
				if ordinal < len(p.Rings) {
					dashed = p.Rings[ordinal].Dashed
				}
				ordinal++
				if !wd.Interceptor {
					continue
				}
				r, draw := render.MegamapInterceptorRing(t, wd.Coverage)
				if !draw {
					continue
				}
				color := colors[megamapRingAntinuke]
				if dashed {
					render.DrawMegamapDashedCircle(m.image, w, h, cx, cy, radius(r), color, cur.Radar.BlinkPhase&1 != 0)
				} else {
					render.DrawMegamapCircle(m.image, w, h, cx, cy, radius(r), color)
				}
			}
		}
	}
	// Weapon rings: the hovered allied unit and the allied unit whose command
	// page is open, only while Shift is physically held, slots 3, 2, 1 with
	// the enabled bit and a nonzero authored range, at that raw range (no
	// ballistic limit) [07 R-P0-11 §3].
	page := p.Handle != 0 && p.Handle == cur.CommandPage.Builder
	if !(hovered || page) || !key.shift {
		return
	}
	weapons := [3]*content.WeaponDef{def.Weapon1Def, def.Weapon2Def, def.Weapon3Def}
	for slot := 2; slot >= 0; slot-- {
		wd := weapons[slot]
		if wd == nil || !u.EnabledWeaponSlots[slot] || wd.Range == 0 {
			continue
		}
		render.DrawMegamapCircle(m.image, w, h, cx, cy, radius(wd.Range), colors[megamapRingWeapon1+slot])
	}
}
