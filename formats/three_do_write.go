package formats

import (
	"encoding/binary"
	"fmt"
)

// EncodeThreeDO serialises a parsed model back into the 3DO byte layout
// [fmt 3do]: 52-byte object records, 32-byte primitive records, 12-byte
// vertices, NUL-terminated strings, all offsets absolute. Object i is written
// at i*52, so Objects[0] must be the root and child/sibling links are the
// parsed indexes. The format decoder preserves authored primitive order and
// selection; the runtime model compiler derives the selection swap and mean-Y
// order separately [02 "Model archive (3DO)"].
//
// Textured primitives must be quads; retail's quad mapper has no textured
// n-gon path and the renderer skips such a face [R-REN-03A §5].
func EncodeThreeDO(model *ThreeDO) ([]byte, error) {
	if model == nil || len(model.Objects) == 0 || model.Root != 0 {
		return nil, fmt.Errorf("3do encode: model must have root object zero")
	}
	const objectSize = 52
	data := make([]byte, len(model.Objects)*objectSize)
	stringsAt := map[string]uint32{}
	appendString := func(value string) uint32 {
		if offset, ok := stringsAt[value]; ok {
			return offset
		}
		offset := uint32(len(data))
		data = append(data, value...)
		data = append(data, 0)
		stringsAt[value] = offset
		return offset
	}

	objectName := make([]uint32, len(model.Objects))
	textureName := make([][]uint32, len(model.Objects))
	for i, obj := range model.Objects {
		if obj.Name == "" {
			return nil, fmt.Errorf("3do encode: object %d has an empty name", i)
		}
		objectName[i] = appendString(obj.Name)
		textureName[i] = make([]uint32, len(obj.Primitives))
		for p, primitive := range obj.Primitives {
			if primitive.TextureName == "" {
				continue
			}
			if len(primitive.VertexIndices) != 4 {
				return nil, fmt.Errorf("3do encode: textured primitive %s[%d] has %d corners, want 4", obj.Name, p, len(primitive.VertexIndices))
			}
			textureName[i][p] = appendString(primitive.TextureName)
		}
	}
	align4 := func() {
		for len(data)&3 != 0 {
			data = append(data, 0)
		}
	}
	align4()

	vertexOffset := make([]uint32, len(model.Objects))
	indexOffset := make([][]uint32, len(model.Objects))
	primitiveOffset := make([]uint32, len(model.Objects))
	for i, obj := range model.Objects {
		if len(obj.Vertices) != 0 {
			vertexOffset[i] = uint32(len(data))
			for _, vertex := range obj.Vertices {
				data = binary.LittleEndian.AppendUint32(data, uint32(vertex.X))
				data = binary.LittleEndian.AppendUint32(data, uint32(vertex.Y))
				data = binary.LittleEndian.AppendUint32(data, uint32(vertex.Z))
			}
		}
		indexOffset[i] = make([]uint32, len(obj.Primitives))
		for p, primitive := range obj.Primitives {
			if len(primitive.VertexIndices) == 0 {
				continue
			}
			indexOffset[i][p] = uint32(len(data))
			for _, index := range primitive.VertexIndices {
				if int(index) >= len(obj.Vertices) {
					return nil, fmt.Errorf("3do encode: primitive %s[%d] references vertex %d of %d", obj.Name, p, index, len(obj.Vertices))
				}
				data = binary.LittleEndian.AppendUint16(data, index)
			}
		}
		align4()
		if len(obj.Primitives) != 0 {
			primitiveOffset[i] = uint32(len(data))
			for p, primitive := range obj.Primitives {
				data = binary.LittleEndian.AppendUint32(data, primitive.ColorIndex)
				data = binary.LittleEndian.AppendUint32(data, uint32(len(primitive.VertexIndices)))
				// TODO(question): the optional auxiliary target's layout is unknown.
				// A nonzero authored reference or traced reader must establish what
				// data to preserve and relocate; copying its raw word cannot relocate
				// it when this writer repacks the file [fmt 3do].
				data = binary.LittleEndian.AppendUint32(data, uint32(primitive.AlwaysZero))
				data = binary.LittleEndian.AppendUint32(data, indexOffset[i][p])
				data = binary.LittleEndian.AppendUint32(data, textureName[i][p])
				data = binary.LittleEndian.AppendUint32(data, uint32(primitive.Unknown1))
				data = binary.LittleEndian.AppendUint32(data, uint32(primitive.Unknown2))
				data = binary.LittleEndian.AppendUint32(data, uint32(primitive.IsColored))
			}
		}
	}

	put := func(object, field int, value uint32) {
		binary.LittleEndian.PutUint32(data[object*objectSize+field:], value)
	}
	for i, obj := range model.Objects {
		version := obj.Version
		if version == 0 {
			version = 1
		}
		put(i, 0, uint32(version))
		put(i, 4, uint32(len(obj.Vertices)))
		put(i, 8, uint32(len(obj.Primitives)))
		put(i, 12, uint32(obj.Selection))
		put(i, 16, uint32(obj.Translation[0]))
		put(i, 20, uint32(obj.Translation[1]))
		put(i, 24, uint32(obj.Translation[2]))
		put(i, 28, objectName[i])
		// TODO(question): the optional auxiliary target's layout is unknown.
		// A nonzero authored reference or traced reader must establish what data
		// to preserve and relocate; the raw word copied here retains its old
		// file offset despite repacking [fmt 3do].
		put(i, 32, uint32(obj.AlwaysZero))
		put(i, 36, vertexOffset[i])
		put(i, 40, primitiveOffset[i])
		if obj.NextSibling > 0 {
			if int(obj.NextSibling) >= len(model.Objects) {
				return nil, fmt.Errorf("3do encode: object %s sibling %d outside table", obj.Name, obj.NextSibling)
			}
			put(i, 44, uint32(obj.NextSibling)*objectSize)
		}
		if obj.FirstChild > 0 {
			if int(obj.FirstChild) >= len(model.Objects) {
				return nil, fmt.Errorf("3do encode: object %s child %d outside table", obj.Name, obj.FirstChild)
			}
			put(i, 48, uint32(obj.FirstChild)*objectSize)
		}
	}
	return data, nil
}
