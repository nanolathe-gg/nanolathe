package zrb

import "fmt"

func (d *Decoder) decodeVideo(data []byte) error {
	b := bitReader{data: data}
	for i := range d.trees {
		d.trees[i].recent = [3]uint16{}
	}
	columns, rows := (d.Width+3)/4, (d.Height+3)/4
	blocks := columns * rows
	for block := 0; block < blocks; {
		code := d.trees[3].decode(&b)
		run := int((code>>2)&63) + 1
		if run >= 60 {
			run = 128 << (run - 60)
		}
		// Runs can extend past the image; the image extent ends decoding [fmt zrb].
		run = min(run, blocks-block)
		for k := 0; k < run; k++ {
			x, y := (block%columns)*4, (block/columns)*4
			block++
			if code&3 == 2 {
				continue
			}
			var pixels [16]byte
			switch code & 3 {
			case 0:
				colors := d.trees[1].decode(&b)
				mask := d.trees[0].decode(&b)
				for i := range pixels {
					pixels[i] = byte(colors)
					if mask&(1<<i) != 0 {
						pixels[i] = byte(colors >> 8)
					}
				}
			case 1:
				for row := 0; row < 4; row++ {
					right := d.trees[2].decode(&b)
					left := d.trees[2].decode(&b)
					pixels[row*4] = byte(left)
					pixels[row*4+1] = byte(left >> 8)
					pixels[row*4+2] = byte(right)
					pixels[row*4+3] = byte(right >> 8)
				}
			case 3:
				for i := range pixels {
					pixels[i] = byte(code >> 8)
				}
			}
			for row := 0; row < 4 && y+row < d.Height; row++ {
				copy(d.frame.Pixels[(y+row)*d.Width+x:(y+row)*d.Width+min(x+4, d.Width)], pixels[row*4:row*4+min(4, d.Width-x)])
			}
		}
		if b.err != nil {
			return fmt.Errorf("video data: %w", b.err)
		}
	}
	return nil
}
