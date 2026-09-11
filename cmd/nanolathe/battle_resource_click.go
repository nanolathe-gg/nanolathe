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
	x, y      int32
	at        uint32
	builder   pool.Handle
	selection []pool.Handle
	site      resourceBuildSite
	fallback  session.HumanCommand
}

type resourceBuildSite struct {
	product *content.UnitDef
	x, z    int32
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
func (b *battleSession) resourceProducts(builder string) (extractor, solar *content.UnitDef) {
	menu := b.cat.BuildMenus[content.CanonicalKey(builder)]
	if menu == nil {
		return nil, nil
	}
	for _, key := range menu.Buttons {
		u, ok := b.cat.Unit(key)
		if !ok || u == nil {
			continue
		}
		if u.BMCode == 0 && !u.Builder && u.ExtractsMetal > 0 && (extractor == nil || u.ExtractsMetal > extractor.ExtractsMetal) {
			extractor = u
		}
		if solar == nil && resourceSolar(u) {
			solar = u
		}
	}
	return extractor, solar
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
	extractor, solar := b.resourceProducts(v.DefName)
	product := solar
	wx, wz := pos.X, pos.Z
	cx, cz := world.WorldToCell(wx), world.WorldToCell(wz)
	for _, feature := range f.Features {
		fx, fz := int32(feature.FootX), int32(feature.FootZ)
		if cx < feature.CX || cx >= feature.CX+fx || cz < feature.CZ || cz >= feature.CZ+fz {
			continue
		}
		fd := b.cat.Features[content.CanonicalKey(feature.DefName)]
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
		wx, wz = world.PlacementCenter(feature.CX, feature.CZ, fx, fz)
		break
	}
	if product == nil {
		return resourceBuildSite{}, false
	}
	fx, fz := footprintCellsForCatalog(b.cat, product)
	x, z := world.PlacementAnchor(wx, wz, fx, fz)
	return resourceBuildSite{product: product, x: x, z: z}, true
}

func (b *battleSession) resourceClickNow() uint32 {
	if b.millisSource == nil {
		b.millisSource = newMonotonicMillisSource()
	}
	return b.millisSource.Millis32()
}

// Delay only a qualifying idle ground click. In Type 0 its contextual Move
// would contaminate the construction queue; in Type 1 its deselect would lose the builder.
func (b *battleSession) deferResourceClick(cl *client.Client, mx, my int32, modifiers input.Modifiers) bool {
	if cl == nil || !cl.Enhanced() || !cl.IsFocused() || b.palettePointerOwned || modifiers.Ctrl || modifiers.Alt {
		return false
	}
	site, ok := b.resourceSite(mx, my)
	if !ok {
		return false
	}
	f, _ := b.currentSnapshot()
	_, _, pos := b.pickTarget(mx, my)
	fallback := session.HumanCommand{Kind: session.HumanSelectionClear}
	if !b.interfaceTypeRightClick() {
		fallback = session.HumanCommand{Kind: session.HumanOrder, Order: session.HumanOrderCommand{Code: 1, Position: *pos, Queued: modifiers.Shift}}
	}
	b.resourceClick = &resourceClick{x: mx, y: my, at: b.resourceClickNow(),
		builder: f.CommandPage.Builder, selection: slices.Clone(f.Selection.Handles), site: site, fallback: fallback}
	return true
}

func (b *battleSession) flushResourceClick() {
	pending := b.resourceClick
	b.resourceClick = nil
	if pending != nil {
		b.resourceQueueFeedback = nil
		if pending.fallback.Kind == session.HumanSelectionClear {
			_ = b.enqueueSelectionCommand(pending.fallback)
		} else {
			_ = b.enqueueHumanCommand(pending.fallback)
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
	// A new keyboard command supersedes the deferred click (Escape must never
	// replay a Move); pointer commands outside the pair retain event order.
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
	if b.resourceClickNow()-pending.at > resourceDoubleClickMillis || mouse.Pressed(input.MouseButtonRight) ||
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
	state.DragActive = false
	state.PlaceCaptured = true
	fx, fz := footprintCellsForCatalog(b.cat, site.product)
	result, err := b.checkProductPlacement(site.x, site.z, site.product, fx, fz, uint16(pending.builder))
	if err == nil {
		wx, wz := world.PlacementCenter(site.x, site.z, fx, fz)
		err = b.dispatchMobileBuild(site.product.CanonicalKey, wx, numeric.FixedFromInt(int64(result.SiteHeight)), wz, true, true)
	}
	if err != nil {
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
