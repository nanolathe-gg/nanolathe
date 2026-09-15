package main

// Modern resource construction is a user-requested input convenience, not a
// retail rule. Its gesture and classification policy lives in
// DESIGN_INTERFACE_HUD_INPUT §3.10; simulation receives ordinary build orders.

import (
	"slices"
	"strings"
	"unicode"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

const resourceDoubleClickMillis = 400
const resourceDoubleClickPixels = 6

// Modern feedback duration is UI policy: DESIGN_INTERFACE_HUD_INPUT §3.10.
const resourceQueueFeedbackMillis = 1500

type resourceQueueFeedback struct {
	started   uint32
	builder   pool.Handle
	selection []pool.Handle
}

type resourceClick struct {
	x, y         int32
	at           uint32
	builder      pool.Handle
	selection    []pool.Handle
	site         resourceBuildSite
	fallback     session.HumanCommand
	moveSequence uint64
}

type resourceBuildSite struct {
	product    *content.UnitDef
	x, z       int32
	deposit    resourceRect
	geothermal bool
}

func resourceToken(value, token string) bool {
	for _, word := range strings.FieldsFunc(value, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if strings.EqualFold(word, token) {
			return true
		}
	}
	return false
}

// A named SOLAR category or sound family avoids guessing from cost or energy
// output. The installed ARM/CORE collectors use ARM_SOLAR/CORE_SOLAR [fmt fbi].
func resourceSolar(u *content.UnitDef) bool {
	return u != nil && u.BMCode == 0 && !u.Builder && u.ExtractsMetal == 0 &&
		u.WindGenerator == 0 && u.TidalGenerator == 0 && (u.EnergyUse < 0 || u.EnergyMake > 0) &&
		(resourceToken(u.SoundCategory, "solar") || resourceToken(u.Category, "solar"))
}

// Walk the complete authored menu, not just its currently displayed page.
// Equal extraction rates retain menu order; fabricators are not extractors.
func (b *battleSession) resourceProducts(builder string) (extractor, solar, geothermal *content.UnitDef) {
	menu := b.cat.BuildMenus[content.CanonicalKey(builder)]
	if menu == nil {
		return nil, nil, nil
	}
	for _, key := range menu.Buttons {
		u, ok := b.cat.Unit(key)
		if !ok || u == nil {
			continue
		}
		if len(resourceGeoYard(u)) != 0 {
			if geothermal == nil {
				geothermal = u
			}
			continue // a vent-dependent product cannot be a ground shortcut
		}
		if u.BMCode == 0 && !u.Builder && u.ExtractsMetal > 0 && (extractor == nil || u.ExtractsMetal > extractor.ExtractsMetal) {
			extractor = u
		}
		if solar == nil && resourceSolar(u) {
			solar = u
		}
	}
	return extractor, solar, geothermal
}

func (b *battleSession) resourceSite(mx, my int32) (resourceBuildSite, bool) {
	f, ok := b.currentSnapshot()
	if !ok || b.cat == nil || b.cam == nil || b.classifyPointer(mx, my) != battlePointerViewport {
		return resourceBuildSite{}, false
	}
	v, found := snapshotUnitByHandle(f, f.CommandPage.Builder)
	if !found || v.Owner != b.sess.LocalOwner || !b.snapshotBuilder(v) || !slices.Contains(f.Selection.Handles, v.Slot) {
		return resourceBuildSite{}, false
	}
	def, ok := b.cat.Unit(v.DefName)
	if !ok || def == nil || def.BMCode == 0 {
		return resourceBuildSite{}, false // factories keep their product-button input
	}
	handle, _, pos := b.pickTarget(mx, my)
	if handle != 0 || pos == nil {
		return resourceBuildSite{}, false
	}
	extractor, solar, geothermal := b.resourceProducts(v.DefName)
	product := solar
	var deposit resourceRect
	wx, wz := pos.X, pos.Z
	cx, cz := world.WorldToCell(wx), world.WorldToCell(wz)
	for _, feature := range f.Features {
		fx, fz := int32(feature.FootX), int32(feature.FootZ)
		if cx < feature.CX || cx >= feature.CX+fx || cz < feature.CZ || cz >= feature.CZ+fz {
			continue
		}
		fd := b.cat.Features[content.CanonicalKey(feature.DefName)]
		if fd != nil && fd.Geothermal {
			return resourceVentSite(geothermal, resourceRect{feature.CX, feature.CZ, fx, fz})
		}
		// Indestructible metal features seed the deposit field; reclaimable
		// wreck metal does not [05 R-FEAT-01 §7][08 R-AI-03 §1].
		if fd == nil || !fd.Indestructible || fd.Metal == 0 {
			if snapshotFeatureVisible(f, feature, f.ViewingPlayer) {
				return resourceBuildSite{}, false
			}
			continue
		}
		// Permanent map deposits keep their identity outside current LOS;
		// fog must not turn an extractor gesture into solar construction.
		// Placement keeps its known-site check (DESIGN_INTERFACE_HUD_INPUT §3.10).
		product = extractor
		deposit = resourceRect{feature.CX, feature.CZ, fx, fz}
		wx, wz = world.PlacementCenter(feature.CX, feature.CZ, fx, fz)
		break
	}
	if deposit.w == 0 && geothermal != nil {
		if site, found := b.nearbyResourceVent(f, geothermal, wx, wz); found {
			return site, true
		}
	}
	if product == nil {
		return resourceBuildSite{}, false
	}
	fx, fz := footprintCellsForCatalog(b.cat, product)
	x, z := world.PlacementAnchor(wx, wz, fx, fz)
	return resourceBuildSite{product: product, x: x, z: z, deposit: deposit}, true
}

func (b *battleSession) resourceClickNow() uint32 {
	if b.millisSource == nil {
		b.millisSource = newMonotonicMillisSource()
	}
	return b.millisSource.Millis32()
}

// A Shift-click starts the resource gesture (interface design §3.10). Type 0
// queues its move immediately and retains only the command receipt. Type 1's
// left button selects rather than moves, so its deselect still waits for expiry.
func (b *battleSession) beginResourceClick(cl *client.Client, mx, my int32, modifiers input.Modifiers) bool {
	if cl == nil || !cl.Enhanced() || !cl.IsFocused() || b.palettePointerOwned || !modifiers.Shift || modifiers.Ctrl || modifiers.Alt {
		return false
	}
	site, ok := b.resourceSite(mx, my)
	if !ok {
		return false
	}
	f, _ := b.currentSnapshot()
	pending := &resourceClick{x: mx, y: my, at: b.resourceClickNow(),
		builder: f.CommandPage.Builder, selection: slices.Clone(f.Selection.Handles), site: site}
	if b.interfaceTypeRightClick() {
		pending.fallback = session.HumanCommand{Kind: session.HumanSelectionClear}
	} else {
		_, _, pos := b.pickTarget(mx, my)
		command := session.HumanCommand{Kind: session.HumanOrder, Order: session.HumanOrderCommand{
			Handles: pending.selection, Code: 1, Position: *pos, Queued: true, TrackQueuedMove: true,
		}}
		sequence, err := b.sess.EnqueueHumanCommandWithSequence(command)
		if err != nil {
			return false
		}
		pending.moveSequence = sequence
		b.resourceQueueFeedback = nil
	}
	b.resourceClick = pending
	return true
}

func (b *battleSession) flushResourceClick() {
	pending := b.resourceClick
	b.resourceClick = nil
	if pending != nil {
		b.resourceQueueFeedback = nil
		if pending.fallback.Kind == session.HumanSelectionClear {
			_ = b.enqueueSelectionCommand(pending.fallback)
		}
	}
}

// serviceResourceClick runs before the normal mouse paths. It owns the second
// press through release, including a refused build, just like manual placement.
func (b *battleSession) serviceResourceClick(in *input.State, cl *client.Client, mouse input.MouseState, modifiers input.Modifiers) bool {
	pending := b.resourceClick
	if pending == nil {
		return false
	}
	f, ok := b.currentSnapshot()
	state := &b.battleState().Input
	if !ok || cl == nil || !cl.Enhanced() || !cl.IsFocused() || b.palettePointerOwned || state.Latch != input.LatchNormal || state.BuildDef != "" ||
		f.CommandPage.Builder != pending.builder || !slices.Equal(f.Selection.Handles, pending.selection) {
		b.resourceClick = nil
		return false
	}
	// A new keyboard command ends recognition without replaying the first
	// click. An already queued move remains an ordinary order.
	if in.ShortcutTokenMode && in.ShortcutToken.Kind != input.TokenNone {
		b.resourceClick = nil
		return false
	}
	for key := input.Key(1); key < input.KeyCount; key++ {
		if key != input.KeyShift && in.Kbd.KeyDown(key) {
			b.resourceClick = nil
			return false
		}
	}
	mx, my := int32(mouse.X), int32(mouse.Y)
	dx, dy := mx-pending.x, my-pending.y
	matching := dx >= -resourceDoubleClickPixels && dx <= resourceDoubleClickPixels &&
		dy >= -resourceDoubleClickPixels && dy <= resourceDoubleClickPixels &&
		!modifiers.Ctrl && !modifiers.Alt
	if b.resourceClickNow()-pending.at > resourceDoubleClickMillis || !modifiers.Shift || mouse.Pressed(input.MouseButtonRight) ||
		mouse.Pressed(input.MouseButtonMiddle) || (mouse.Pressed(input.MouseButtonLeft) && !matching) {
		b.flushResourceClick()
		return false
	}
	if !mouse.Pressed(input.MouseButtonLeft) {
		return false
	}
	site, valid := b.resourceSite(mx, my)
	if !valid || site != pending.site {
		b.flushResourceClick()
		return false
	}
	b.resourceClick = nil
	// Cancellation crosses the same tick boundary as the build. Only the move
	// created by this first click can be removed, even if it has already run.
	// A refused build also consumes the gesture's move, leaving older work alone.
	if pending.moveSequence != 0 {
		_ = b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanCancelQueuedMove,
			CancelQueuedMove: session.HumanCancelQueuedMoveCommand{Sequence: pending.moveSequence, Handles: pending.selection}})
	}
	state.DragActive = false
	state.PlaceCaptured = true
	site, result, buildOK := b.spaceResourceBuild(site, pending.builder)
	if buildOK {
		fx, fz := footprintCellsForCatalog(b.cat, site.product)
		wx, wz := world.PlacementCenter(site.x, site.z, fx, fz)
		buildOK = b.dispatchMobileBuild(site.product.CanonicalKey, wx, numeric.FixedFromInt(int64(result.SiteHeight)), wz, true, true) == nil
	}
	if !buildOK {
		b.playUICue(cl, "notoktobuild")
	} else {
		b.resourceQueueFeedback = &resourceQueueFeedback{started: b.resourceClickNow(), builder: pending.builder, selection: pending.selection}
		b.playUICue(cl, "oktobuild")
	}
	return true
}

// Expiry is sampled by input, never by the recorder's parallel draw worker.
// Feedback reveals the ordinary committed queue; it never synthesizes Shift.
func (b *battleSession) updateResourceQueueFeedback(cl *client.Client) {
	feedback := b.resourceQueueFeedback
	if feedback == nil {
		return
	}
	f, ok := b.currentSnapshot()
	if cl == nil || !cl.Enhanced() || !cl.IsFocused() || !ok ||
		f.CommandPage.Builder != feedback.builder || !slices.Equal(f.Selection.Handles, feedback.selection) ||
		b.resourceClickNow()-feedback.started >= resourceQueueFeedbackMillis {
		b.resourceQueueFeedback = nil
	}
}
