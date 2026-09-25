package main

import (
	"strings"
	"unicode/utf8"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// communityPlacementState is host input state for CP-CON-5/6. Facing is the
// user's retained cursor choice. A product that disallows it previews and
// issues south without destroying the choice, so returning to a rotatable
// product restores the previous direction (community patch engine CP-CON-5).
type communityPlacementState struct {
	facing           units.StructureFacing
	overrideHeld     bool
	reclaimSnap      orders.ResolvePos
	reclaimSnapArmed bool
}

func (b *battleSession) communityPlacementFacing(def *content.UnitDef) units.StructureFacing {
	if b == nil || b.sess == nil || b.sess.Build == nil {
		return units.FacingSouth
	}
	return b.sess.Build.ResolveStructureFacing(def, b.communityPlacement.facing)
}

func (b *battleSession) communityPlacementGeometry(def *content.UnitDef) (units.StructureFacing, int32, int32) {
	facing := b.communityPlacementFacing(def)
	if b != nil && b.sess != nil && b.sess.Build != nil {
		if geometry, err := b.sess.Build.StructureGeometry(def, facing); err == nil {
			return geometry.Facing, geometry.FootprintX, geometry.FootprintZ
		}
	}
	var cat *content.Catalog
	if b != nil {
		cat = b.cat
	}
	footX, footZ := footprintCellsForCatalog(cat, def)
	return units.FacingSouth, footX, footZ
}

// cycleCommunityPlacementFacing walks only the definition's allowed cardinals.
// Fewer than two choices is a true no-op: the shortcut remains unconsumed and
// the retained facing is unchanged (community patch engine CP-CON-5).
func (b *battleSession) cycleCommunityPlacementFacing(direction int, mx, my int32) bool {
	if b == nil || b.sess == nil || b.sess.Build == nil || !b.battleState().PlacementArmed() || b.cat == nil {
		return false
	}
	def, ok := b.cat.Unit(b.battleState().Input.BuildDef)
	if !ok || def == nil {
		return false
	}
	allowed := b.sess.Build.AllowedFacings(def)
	count := 0
	for facing := units.FacingSouth; facing <= units.FacingWest; facing++ {
		if allowed&units.FacingMask(facing) != 0 {
			count++
		}
	}
	if count < 2 {
		return false
	}
	step := 1
	if direction < 0 {
		step = -1
	}
	for n := 1; n <= 4; n++ {
		candidate := units.StructureFacing((int(b.communityPlacement.facing) + step*n) & 3)
		if allowed&units.FacingMask(candidate) == 0 {
			continue
		}
		b.communityPlacement.facing = candidate
		_, footX, footZ := b.communityPlacementGeometry(def)
		state := &b.battleState().Input
		state.BuildFootX, state.BuildFootZ = footX, footZ
		b.updatePlacement(mx, my)
		if ring := b.messageRing(); ring != nil {
			directions := [...]string{"S", "E", "N", "W"}
			ring.PostSilent("Build facing: "+directions[candidate], 0, b.currentTick())
		}
		return true
	}
	return false
}

// TODO(question): establish whether the author's Shift+Q/E and v instructions
// need any extension dispatch beyond authored gadget quick keys. A manual
// patched palette with different accelerators would settle this; retain the
// existing gadget dispatch meanwhile [community patch engine CP-CON-6].
// serviceCommunityPlacementInput owns the rotation key and the snap-override
// wheel gesture. Ctrl blocks the key; Shift is deliberately allowed. The
// override modifier consumes a wheel event even when the armed definition has
// no second facing, preventing the same event from reaching zoom.
func (b *battleSession) serviceCommunityPlacementInput(in *input.State, cl *client.Client, mx, my int32) bool {
	if b == nil || in == nil {
		return false
	}
	prefs := b.hostPreferences()
	b.communityPlacement.overrideHeld = communityModifierHeld(in.Kbd, prefs.ClickSnapOverrideKey)
	b.updateCommunityReclaimSnap(mx, my)
	if in.Mouse != nil && in.Mouse.Scrolled() && b.communityPlacementWheelOwned(in) {
		direction := -1
		if in.Mouse.ScrollY > 0 {
			direction = 1
		}
		if in.Mouse.ScrollY != 0 && b.cycleCommunityPlacementFacing(direction, mx, my) {
			b.playUICue(cl, "MORE")
		}
		return true
	}
	if !communityRotationShortcut(in, prefs.BuildRotateKey) || in.Kbd.KeyHeld(input.KeyCtrl) {
		return false
	}
	if !b.cycleCommunityPlacementFacing(1, mx, my) {
		return false
	}
	b.playUICue(cl, "MORE")
	return true
}

// communityBuildSnap applies CP-CON-6 to the native cell-aligned build
// position. The scanned coordinate is the footprint centre cell used by the
// extension; the battle state continues to store the north-west footprint
// anchor expected by construction and rendering.
func (b *battleSession) communityBuildSnap(def *content.UnitDef, rawX, rawZ, footX, footZ int32, self uint16, cursorX, cursorZ numeric.Fixed) (int32, int32) {
	if b == nil || b.sess == nil || def == nil || b.communityPlacement.overrideHeld || footX <= 0 || footZ <= 0 {
		return rawX, rawZ
	}
	prefs := b.hostPreferences()
	features := b.sess.Community
	radius := effectiveClickSnapRadius(prefs.MexSnapRadius, features.MexSnapRadius, features.MexSnapRadiusMax, features.MexSnap)
	if radius <= 0 {
		return rawX, rawZ
	}
	baseX, baseZ := rawX+footX/2, rawZ+footZ/2
	choose := func(countAt func(int32, int32) int) (placementSnapCandidate, bool) {
		return choosePlacementSnap(baseX, baseZ, radius, cursorX, cursorZ, countAt)
	}
	var chosen placementSnapCandidate
	var ok bool
	if def.ExtractsMetal > 0 {
		chosen, ok = choose(func(cx, cz int32) int {
			return b.sess.PlacementSnapSample(cx, cz, footX, footZ).MetalAboveSurface
		})
		if ok && (chosen.cellX != baseX || chosen.cellZ != baseZ) {
			centred, found := choosePlacementSnap(chosen.cellX, chosen.cellZ, max(footX, footZ), cursorX, cursorZ, func(cx, cz int32) int {
				return b.sess.PlacementSnapSample(cx, cz, footX, footZ).MetalAboveSurface
			})
			ok = found && centred.cellX == chosen.cellX && centred.cellZ == chosen.cellZ
		}
	}
	if communityGeoSnapDefinition(def) {
		chosen, ok = choose(func(cx, cz int32) int {
			_, err := b.checkProductPlacement(cx-footX/2, cz-footZ/2, def, footX, footZ, self)
			if err == nil {
				return 1
			}
			return 0
		})
		ok = ok && (chosen.cellX != baseX || chosen.cellZ != baseZ)
	}
	if !ok {
		return rawX, rawZ
	}
	return chosen.cellX - footX/2, chosen.cellZ - footZ/2
}

// communityGeoSnapDefinition keeps the extension's bounded diagnostic read:
// only the first 64 decoded yard cells participate even when authored content
// has a larger footprint.
func communityGeoSnapDefinition(def *content.UnitDef) bool {
	yard := resourceGeoYard(def)
	if len(yard) > 64 {
		yard = yard[:64]
	}
	for _, cell := range yard {
		if cell&0x80 != 0 {
			return true
		}
	}
	return false
}

// updateCommunityReclaimSnap computes the preview from the committed
// command-time session boundary. The raw occupied-cell veto happens before
// the search, as in CP-CON-6.
func (b *battleSession) updateCommunityReclaimSnap(mx, my int32) {
	if b == nil {
		return
	}
	b.communityPlacement.reclaimSnapArmed = false
	if b.sess == nil || b.communityPlacement.overrideHeld || b.battleState().Input.Latch != input.LatchReclaim || !b.communityClickSnapAllowed(mx) {
		return
	}
	features := b.sess.Community
	radius := effectiveClickSnapRadius(b.hostPreferences().WreckSnapRadius, features.WreckSnapRadius, features.WreckSnapRadiusMax, features.WreckSnap)
	if radius <= 0 {
		return
	}
	wx, _, wz := b.cursorWorld(mx, my)
	baseX, baseZ := world.WorldToCell(wx), world.WorldToCell(wz)
	if sample := b.sess.PlacementSnapSample(baseX, baseZ, 1, 1); !sample.Valid || sample.UnitOccupied {
		return
	}
	chosen, ok := choosePlacementSnap(baseX, baseZ, radius, wx, wz, func(cx, cz int32) int {
		if b.sess.PlacementSnapSample(cx, cz, 1, 1).Reclaimable {
			return 1
		}
		return 0
	})
	if !ok {
		return
	}
	sample := b.sess.PlacementSnapSample(chosen.cellX, chosen.cellZ, 1, 1)
	if !sample.Reclaimable || sample.FeatureDef == nil {
		return
	}
	wreck := b.isCorpseName(sample.FeatureDef.CanonicalKey)
	b.communityPlacement.reclaimSnap = orders.ResolvePos{
		X: sample.WX, Y: sample.WY, Z: sample.WZ,
		HasFeature: true, IsWreck: wreck, FeatureResurrectable: wreck,
	}
	b.communityPlacement.reclaimSnapArmed = true
}

// communityClickSnapAllowed follows the extension's horizontal game-rectangle
// test. It deliberately does not impose the ordinary viewport's vertical
// bounds. Strategic view is Nanolathe's overview equivalent and suppresses the
// same gesture through the client's existing blended-view decision.
func (b *battleSession) communityClickSnapAllowed(mx int32) bool {
	if b == nil || b.cl != nil && b.cl.StrategicViewActive() {
		return false
	}
	screenW, _ := b.surfaceSize()
	return mx >= camera.OriginX+1 && mx < screenW
}

func (b *battleSession) communityReclaimSnap() (orders.ResolvePos, bool) {
	if b == nil || !b.communityPlacement.reclaimSnapArmed {
		return orders.ResolvePos{}, false
	}
	return b.communityPlacement.reclaimSnap, true
}

func (b *battleSession) communityReclaimSnapOverlayArmed() bool {
	_, ok := b.communityReclaimSnap()
	return ok
}

// drawCommunityReclaimSnap draws the reclaim cursor animation at the resolved
// feature centre. The ordinary pointer can still be over empty ground, so the
// marker resolves the reclaim entry directly from the shared cursor bank.
func (b *battleSession) drawCommunityReclaimSnap(c *client.Client) {
	pos, ok := b.communityReclaimSnap()
	if !ok || c == nil || b.cam == nil {
		return
	}
	entry := queueIconEntry(b.fs, render.CursorReclamate)
	if entry == nil || len(entry.Frames) == 0 {
		return
	}
	ticksPerFrame := entry.Frames[0].Value
	if ticksPerFrame == 0 {
		ticksPerFrame = 1
	}
	var tick uint32
	if cur, ok := b.presentedSnapshot(c); ok {
		tick = cur.Tick
	}
	ref := entry.Frames[(tick/ticksPerFrame)%uint32(len(entry.Frames))]
	if ref.Frame == nil {
		return
	}
	sx, sy := b.cam.WorldToScreen(pos.X, pos.Y, pos.Z)
	c.UIBlitAnchor(ref.Frame, int(sx-camera.OriginX), int(sy-camera.OriginY))
}

func (b *battleSession) issueCommunityReclaimSnap(queued bool) bool {
	pos, ok := b.communityReclaimSnap()
	if !ok {
		return false
	}
	if b.interfaceTypeRightClick() {
		pos.InterfaceType = orders.InterfaceTypeRightClick
	}
	_ = b.DispatchOrderCommand(session.HumanOrderCommand{Code: 12, Position: pos, Queued: queued})
	return true
}

// communityPlacementWheelOwned is queried by the earlier camera-wheel pass.
// Strict resolves StructureRotation false, so the gesture remains ordinary
// zoom there rather than becoming a second mode check in the host.
func (b *battleSession) communityPlacementWheelOwned(in *input.State) bool {
	if b == nil || b.sess == nil || in == nil || in.Mouse == nil || !in.Mouse.Scrolled() || !b.sess.Community.StructureRotation {
		return false
	}
	return communityModifierHeld(in.Kbd, b.hostPreferences().ClickSnapOverrideKey)
}

func communityModifierHeld(kbd *input.KeyboardState, name string) bool {
	if kbd == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "shift":
		return kbd.KeyHeld(input.KeyShift)
	case "ctrl", "control":
		return kbd.KeyHeld(input.KeyCtrl)
	default: // normalized default and unknown hand edits both retain Alt.
		return kbd.KeyHeld(input.KeyAlt)
	}
}

func communityRotationShortcut(in *input.State, configured string) bool {
	if in == nil || !in.ShortcutTokenMode || in.ShortcutToken.Kind != input.TokenText || in.ShortcutToken.Ctrl {
		return false
	}
	configured = strings.TrimSpace(configured)
	key, n := utf8.DecodeRuneInString(configured)
	if n != len(configured) || key == utf8.RuneError {
		return false
	}
	pressed := in.ShortcutToken.Rune
	// The configured value denotes the physical slash key. Text input reports
	// its shifted glyph as '?', but CP-CON-5 explicitly allows Shift.
	return pressed == key || in.Kbd != nil && in.Kbd.HasShift() && key == '/' && pressed == '?'
}

// effectiveClickSnapRadius applies the source's stable configuration rule:
// defaults and maxima are capped at nine, defaults are capped to maxima, a
// negative setting selects that default, zero disables, and a value above the
// maximum also resolves to the default (community patch engine CP-CON-6).
func effectiveClickSnapRadius(setting, tableDefault, tableMax int, enabled bool) int32 {
	if !enabled {
		return 0
	}
	if tableMax > 9 {
		tableMax = 9
	}
	if tableMax <= 0 {
		return 0
	}
	if tableDefault > 9 {
		tableDefault = 9
	}
	if tableDefault < 0 {
		tableDefault = 0
	}
	if tableDefault > tableMax {
		tableDefault = tableMax
	}
	if setting < 0 || setting > tableMax {
		setting = tableDefault
	}
	return int32(setting)
}

type placementSnapCandidate struct {
	cellX, cellZ int32
	count        int
	distance2    uint64
}

// choosePlacementSnap reproduces the square scan and its two-stage ordering:
// largest positive count, then nearest raw cursor with a half-cell bias, then
// dx-major/dz-minor scan order. Fixed arithmetic exactly represents the source
// distance comparison without adding authoritative floating-point state.
func choosePlacementSnap(baseX, baseZ, radius int32, cursorX, cursorZ numeric.Fixed, countAt func(int32, int32) int) (placementSnapCandidate, bool) {
	var best placementSnapCandidate
	found := false
	const halfCell = int64(1 << 15)
	for dx := -radius; dx <= radius; dx++ {
		for dz := -radius; dz <= radius; dz++ {
			x, z := baseX+dx, baseZ+dz
			count := countAt(x, z)
			if count <= 0 {
				continue
			}
			// cursorX/Z are world 16.16. Dividing by 16 retains a 16.16
			// cell coordinate; candidate+1/2 is represented in the same scale.
			sx := int64(x)<<16 + halfCell - int64(cursorX)/16
			sz := int64(z)<<16 + halfCell - int64(cursorZ)/16
			distance2 := uint64(sx*sx + sz*sz)
			if !found || count > best.count || count == best.count && distance2 < best.distance2 {
				best = placementSnapCandidate{cellX: x, cellZ: z, count: count, distance2: distance2}
				found = true
			}
		}
	}
	return best, found
}
