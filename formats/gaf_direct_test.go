package formats

import (
	"bytes"
	"testing"
)

func TestGAFDirectRasterReadsAuthoredRLEStorage(t *testing.T) {
	data, err := EncodeGAF([]GAFWriteEntry{{Name: "mask", Frames: []GAFWriteFrame{{
		Width: 3, Height: 1, Pixels: []byte{7, 7, 7}, Transparent: []bool{true, true, true},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	g, err := LoadGAF(data)
	if err != nil {
		t.Fatal(err)
	}
	f := g.Entries[0].Frames[0].Frame
	raster, ok := f.DirectRaster()
	if !ok {
		t.Fatal("encoded storage fits but direct view rejected")
	}
	want := append([]byte(nil), data[f.DataOffset:int(f.DataOffset)+3]...)
	if !bytes.Equal(raster.Pixels, want) {
		t.Fatalf("direct bytes %v, want storage %v", raster.Pixels, want)
	}
	if bytes.Equal(raster.Pixels, f.Pixels) {
		t.Fatal("direct view used decoded RLE pixels")
	}
	// The immutable view must not retain the caller's mutable file buffer.
	data[f.DataOffset] ^= 255
	if !bytes.Equal(raster.Pixels, want) {
		t.Fatal("caller file mutation changed direct raster")
	}
}

func TestGAFDirectRasterRejectsCompositeAndShortStorage(t *testing.T) {
	for _, authored := range []GAFWriteFrame{
		{Width: 1, Height: 1, Subframes: []GAFWriteFrame{{Width: 1, Height: 1, Pixels: []byte{7}}}},
		{Width: 100, Height: 1, Pixels: make([]byte, 100), Transparent: func() []bool {
			v := make([]bool, 100)
			for i := range v {
				v[i] = true
			}
			return v
		}()},
	} {
		data, err := EncodeGAF([]GAFWriteEntry{{Name: "unsafe", Frames: []GAFWriteFrame{authored}}})
		if err != nil {
			t.Fatal(err)
		}
		g, err := LoadGAF(data)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := g.Entries[0].Frames[0].Frame.DirectRaster(); ok {
			t.Fatal("unsafe direct raster accepted")
		}
	}
}
