package client

import (
	compiledmodel "github.com/nanolathe-gg/nanolathe/internal/model"
)

// modelTexRefs is one compiled model's texture resolution, per piece and
// primitive in load order: the texRef and whether the name resolved. A
// primitive's texture name never changes after load and the indices it
// resolves through change only when the client installs new ones, so the
// per-face lookup — a name-key map, a lowercase scan and two index lookups —
// runs once per model per index generation instead of once per face per
// frame. Entries are immutable once stored; a stale generation is replaced,
// never edited, so record workers can read the table while the recording
// client builds an entry for a model they have not met.
type modelTexRefs struct {
	gen  uint64
	refs [][]texRef
	ok   [][]bool
}

// modelTexRefs returns m's table for the current index generation, building
// it on first use.
func (c *Client) modelTexRefs(m *compiledmodel.Model) *modelTexRefs {
	if c == nil || c.texRefs == nil || m == nil {
		return nil
	}
	if v, ok := c.texRefs.Load(m); ok {
		if t := v.(*modelTexRefs); t.gen == c.texGen {
			return t
		}
	}
	t := &modelTexRefs{gen: c.texGen, refs: make([][]texRef, len(m.Pieces)), ok: make([][]bool, len(m.Pieces))}
	for i := range m.Pieces {
		prims := m.Pieces[i].Primitives
		t.refs[i] = make([]texRef, len(prims))
		t.ok[i] = make([]bool, len(prims))
		for j := range prims {
			t.refs[i][j], t.ok[i][j] = c.resolveModelTexture(prims[j].TextureName)
		}
	}
	c.texRefs.Store(m, t)
	return t
}

// at is the resolution of primitive pri of the piece loaded at index piece,
// or a per-face resolution when the table does not cover it.
func (c *Client) texRefAt(t *modelTexRefs, piece, pri int, name string) (texRef, bool) {
	if t != nil && piece >= 0 && piece < len(t.refs) && pri >= 0 && pri < len(t.refs[piece]) {
		return t.refs[piece][pri], t.ok[piece][pri]
	}
	return c.resolveModelTexture(name)
}
