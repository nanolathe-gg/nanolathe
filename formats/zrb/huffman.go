package zrb

import (
	"fmt"
	"io"
)

// Bits and tree serialization use the least-significant-first order [fmt zrb].
type bitReader struct {
	data []byte
	pos  int
	err  error
}

func (b *bitReader) read(n int) uint32 {
	if b.err != nil {
		return 0
	}
	if n > len(b.data)*8-b.pos {
		b.err = io.ErrUnexpectedEOF
		return 0
	}
	var v uint32
	for shift := 0; shift < n; {
		take := min(n-shift, 8-(b.pos&7))
		v |= uint32(b.data[b.pos>>3]>>uint(b.pos&7)) & ((1 << take) - 1) << shift
		b.pos += take
		shift += take
	}
	return v
}

type huffNode struct {
	child [2]int
	value uint16
	cache int8
}
type huffTree struct{ nodes []huffNode }

// Depth and node bounds are host policy; a binary tree over 16-bit symbols has
// at most 131071 nodes when each symbol occurs once [I11].
func readTopology(b *bitReader, t *huffTree, depth, limit int, leaf func() (uint16, int8)) int {
	if b.err != nil {
		return 0
	}
	if depth > 64 || len(t.nodes) >= limit {
		b.err = fmt.Errorf("huffman tree exceeds host limit")
		return 0
	}
	i := len(t.nodes)
	t.nodes = append(t.nodes, huffNode{child: [2]int{-1, -1}, cache: -1})
	if b.read(1) == 0 {
		v, c := leaf()
		t.nodes[i].value = v
		t.nodes[i].cache = c
		return i
	}
	left := readTopology(b, t, depth+1, limit, leaf)
	right := readTopology(b, t, depth+1, limit, leaf)
	t.nodes[i].child = [2]int{left, right}
	return i
}
func readByteTree(b *bitReader) huffTree {
	t := huffTree{}
	if b.read(1) == 0 {
		t.nodes = []huffNode{{child: [2]int{-1, -1}, cache: -1}}
		return t
	}
	readTopology(b, &t, 0, 511, func() (uint16, int8) { return uint16(b.read(8)), -1 })
	if b.read(1) != 0 {
		b.err = fmt.Errorf("invalid Huffman tree delimiter")
	}
	return t
}
func (t *huffTree) node(b *bitReader) huffNode {
	i := 0
	for t.nodes[i].child[0] >= 0 && b.err == nil {
		i = t.nodes[i].child[b.read(1)]
	}
	return t.nodes[i]
}
func (t *huffTree) decode(b *bitReader) uint16 { return t.node(b).value }

type wordTree struct {
	tree   huffTree
	recent [3]uint16
}

func readWordTree(b *bitReader) (wordTree, error) {
	t := wordTree{}
	if b.read(1) == 0 {
		t.tree.nodes = []huffNode{{child: [2]int{-1, -1}, cache: -1}}
		return t, b.err
	}
	low, high := readByteTree(b), readByteTree(b)
	var markers [3]uint16
	for i := range markers {
		markers[i] = uint16(b.read(16))
	}
	readTopology(b, &t.tree, 0, 131071, func() (uint16, int8) {
		v := low.decode(b) | high.decode(b)<<8
		for i, m := range markers {
			if v == m {
				return 0, int8(i)
			}
		}
		return v, -1
	})
	if b.read(1) != 0 {
		b.err = fmt.Errorf("invalid Huffman tree delimiter")
	}
	return t, b.err
}

// Cache hits are resolved before shifting, and repeated newest values do not
// shift the history [fmt zrb]. Keeping cache references separate from leaves
// also covers markers that are not used by any leaf.
func (t *wordTree) decode(b *bitReader) uint16 {
	n := t.tree.node(b)
	v := n.value
	if n.cache >= 0 {
		v = t.recent[n.cache]
	}
	if v != t.recent[0] {
		t.recent = [3]uint16{v, t.recent[0], t.recent[1]}
	}
	return v
}
