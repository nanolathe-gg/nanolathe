package client

// Community placement-model previews are host presentation only. They consume
// immutable definitions and a host-resolved placement pose, record through the
// ordinary model draw list, and never read or mutate simulation state [I6].

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe-gg/nanolathe/internal/model"
	presentationrender "github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// CommunityPreviewStyle is the host's three-state placement preview choice.
// Its values match the persisted NanoframePreview setting.
type CommunityPreviewStyle uint8

const (
	CommunityPreviewOff CommunityPreviewStyle = iota
	CommunityPreviewFull
	CommunityPreviewWireframe
)

// CommunityPreviewOptions supplies one already-resolved placement pose. Facing
// is south/east/north/west as 0..3. Heading is kept separate because the host
// face-opponent rule can deliberately bypass the definition's allowed facings.
type CommunityPreviewOptions struct {
	Definition *content.UnitDef
	Facing     uint8
	Heading    uint16
	Owner      uint8
	OwnerColor uint8
	ColorKnown bool
	X, Y, Z    numeric.Fixed
	Style      CommunityPreviewStyle
}

type communityPreviewModelKey struct {
	fs   *vfs.FS
	name string
}

var communityPreviewEphemeral = [...]string{"flare", "flash", "muzzle", "fire", "flame", "wake"}

// DrawCommunityBuildPreview records a placement model at its resolved host
// pose. Full uses the production body composer; wireframe projects the same
// authored primitive rings and records their edges. Both use the same filtered
// hierarchy and PreviewObject3D fallback (community patch engine, CP-UD-2).
func (c *Client) DrawCommunityBuildPreview(opts CommunityPreviewOptions) bool {
	if c == nil || c.cam == nil || opts.Definition == nil || opts.Style == CommunityPreviewOff {
		return false
	}
	def := opts.Definition
	view := frame.UnitView{
		Owner: opts.Owner, OwnerColor: opts.OwnerColor, OwnerColorKnown: opts.ColorKnown,
		Model: def.ObjectName, DefName: def.UnitName, DefID: uint16(def.UnitDefID),
		Heading: opts.Heading, X: opts.X, Y: opts.Y, Z: opts.Z,
		BMCode: def.BMCode != 0, ZBuffer: def.ZBuffer, NoShadow: true,
	}
	base := c.modelForUnit(view)
	model := c.communityPreviewModel(base, def.ObjectName, def.PreviewObject3D)
	if model == nil || model.compiled == nil {
		return false
	}
	model = filterCommunityPreviewModel(model, communityPreviewPieceList(def, opts.Facing))
	states := c.modelStates(model, nil)
	draw := presentationrender.BuildUnitDrawInto(model.compiled, states, opts.Heading, 0, 0, view, nil, c.borrowDrawScratch())
	if draw == nil {
		return false
	}
	draw.Structure = def.BMCode == 0
	draw.KeyPlane = def.ZBuffer
	draw.CastsShadow = false

	if opts.Style == CommunityPreviewFull {
		if !c.drawModel(draw, opts.Owner, unitTeamColor(view), 0, modelCursorUnit, nil, 0) {
			return false
		}
		if !c.communityColors.options.TeamColorNanolathe {
			return true
		}
	}
	anchorX, anchorY := c.modelAnchor(draw)
	color := c.GUIColor(15)
	if c.communityColors.options.TeamColorNanolathe {
		// Nanolathe's preview keeps §37's production body/wire geometry. The
		// source feature's preview sweep begins at the first frame-ramp entry;
		// use that same configured entry for the static host outline shared by
		// both styles (community-patch-engine.md "Team-coloured nanolathe and
		// nanoframe colours").
		color = c.communityFrameColor(opts.OwnerColor, opts.ColorKnown, 0xa0)
	}
	rings := c.modelOutlineGeometry(draw, anchorX, anchorY, color, 1, 0, 0)
	for _, ring := range rings {
		if len(ring.Vertices) < 2 {
			continue
		}
		for i := range ring.Vertices {
			a := ring.Vertices[i]
			b := ring.Vertices[(i+1)%len(ring.Vertices)]
			c.emitLine(drawlist.Line{X0: a.X, Y0: a.Y, X1: b.X, Y1: b.Y, Index: ring.Color})
		}
	}
	return opts.Style == CommunityPreviewFull || len(rings) != 0
}

// communityPreviewModel resolves the optional substitute once per client. A
// failed substitute retains a negative cache entry, reports one host art
// diagnostic, and falls back to the base model as CP-UD-2 requires.
func (c *Client) communityPreviewModel(base *unitModel, baseName, substitute string) *unitModel {
	name := strings.TrimSpace(substitute)
	if name == "" {
		return base
	}
	path := communityPreviewModelPath(name)
	key := communityPreviewModelKey{fs: c.modelFS, name: strings.ToLower(path)}
	if c.communityPreviewModels == nil {
		c.communityPreviewModels = make(map[communityPreviewModelKey]*unitModel)
	}
	if cached, ok := c.communityPreviewModels[key]; ok {
		if cached == nil {
			return base
		}
		return cached
	}
	m, err := expandModelFromFSStrict(c.modelFS, path)
	if err != nil {
		c.communityPreviewModels[key] = nil
		c.recordArtDiagnostic(path, "", fmt.Sprintf("community placement preview substitute unavailable; using %q: %v", baseName, err))
		return base
	}
	c.communityPreviewModels[key] = m
	return m
}

func communityPreviewModelPath(name string) string {
	name = strings.TrimSpace(name)
	if strings.ContainsAny(name, `/\\`) {
		return name
	}
	if strings.HasSuffix(strings.ToLower(name), ".3do") {
		return "objects3d/" + name
	}
	return "objects3d/" + name + ".3do"
}

// communityPreviewPieceList applies the per-facing key before the global key.
// An empty per-facing value falls through, as does an unset one.
func communityPreviewPieceList(def *content.UnitDef, facing uint8) string {
	if def == nil {
		return ""
	}
	var directional string
	switch facing & 3 {
	case 0:
		directional = def.PreviewPiecesS
	case 1:
		directional = def.PreviewPiecesE
	case 2:
		directional = def.PreviewPiecesN
	case 3:
		directional = def.PreviewPiecesW
	}
	if strings.TrimSpace(directional) != "" {
		return directional
	}
	return def.PreviewPieces
}

func parseCommunityPreviewPieces(value string) map[string]struct{} {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || r == ',' || r == ';'
	})
	if len(fields) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		out[strings.ToLower(field)] = struct{}{}
	}
	return out
}

// filterCommunityPreviewModel removes faces from excluded pieces without
// hiding their descendants. The regular Hidden piece state cascades through a
// hierarchy, so it cannot express CP-UD-2's independent child traversal.
func filterCommunityPreviewModel(src *unitModel, whitelistValue string) *unitModel {
	if src == nil || src.compiled == nil {
		return src
	}
	whitelist := parseCommunityPreviewPieces(whitelistValue)
	compiled := *src.compiled
	compiled.Pieces = append([]compiledmodel.Piece(nil), src.compiled.Pieces...)
	for i := range compiled.Pieces {
		name := strings.ToLower(compiled.Pieces[i].Name)
		keep := false
		if whitelist != nil {
			_, keep = whitelist[name]
		} else {
			keep = true
			for _, fragment := range communityPreviewEphemeral {
				if strings.Contains(name, fragment) {
					keep = false
					break
				}
			}
		}
		if !keep {
			compiled.Pieces[i].Primitives = nil
		}
	}
	return &unitModel{compiled: &compiled, pieceByName: src.pieceByName}
}
