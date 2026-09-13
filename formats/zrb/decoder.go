// Package zrb decodes Smacker 2 cinematics without a platform codec.
// Container, video and audio arithmetic are described in [fmt zrb].
package zrb

import (
	"encoding/binary"
	"fmt"
	"image/color"
	"io"
	"time"
)

// AudioTrack describes the authored channel layout and rate. Output samples
// are always signed 16-bit little-endian, independently of the source width.
type AudioTrack struct {
	SampleRate, Channels int
	Present              bool
}

// Frame borrows decoder storage, which the next Next call may overwrite.
// Pixels have the stored Width and Height, without display scaling.
type Frame struct {
	Pixels  []byte
	Palette [256]color.RGBA
	Audio   [7][]byte
	Index   int
}

type packet struct {
	start, end int
	mask       byte
}

// Decoder owns the sequential video state. Its exported metadata is read-only.
// Interlaced requests black alternate display rows; otherwise a doubled
// DisplayHeight requests repeated rows. Neither changes the decoded Pixels.
type Decoder struct {
	Width, Height, DisplayHeight, FrameCount int
	FrameDuration                            time.Duration
	Interlaced                               bool
	Tracks                                   [7]AudioTrack
	data                                     []byte
	packets                                  []packet
	audioFlags                               [7]uint32
	trees                                    [4]wordTree
	frame                                    Frame
	next                                     int
	failure                                  error
}

// Host allocation limits, not restrictions recovered from retail [I11].
const (
	maxPixels      = 16 << 20
	maxAudioPacket = 16 << 20
	maxAudioTrack  = 256 << 20
)

// New parses the container and video trees. The caller must retain data
// unchanged for the lifetime of the decoder. Unsupported codecs are errors.
func New(data []byte) (*Decoder, error) {
	if len(data) < 104 {
		return nil, fmt.Errorf("zrb: truncated header")
	}
	if string(data[:4]) != "SMK2" {
		return nil, fmt.Errorf("zrb: unsupported video signature %q", data[:4])
	}
	u32 := func(p int) uint32 { return binary.LittleEndian.Uint32(data[p:]) }
	d := &Decoder{data: data, Width: int(u32(4)), Height: int(u32(8)), FrameCount: int(u32(12))}
	if d.Width <= 0 || d.Height <= 0 || d.Width > maxPixels || d.Height > maxPixels/d.Width {
		return nil, fmt.Errorf("zrb: invalid dimensions %dx%d", d.Width, d.Height)
	}
	flags := u32(20)
	d.DisplayHeight = d.Height
	d.Interlaced = flags&2 != 0
	if flags&6 != 0 {
		d.DisplayHeight *= 2
	}
	interval := int64(int32(u32(16)))
	switch {
	case interval > 0:
		d.FrameDuration = time.Duration(interval) * time.Millisecond
	case interval < 0:
		d.FrameDuration = time.Duration(-interval) * 10 * time.Microsecond
	default:
		return nil, fmt.Errorf("zrb: unsupported zero frame interval")
	}
	physical := d.FrameCount + int(flags&1)
	if d.FrameCount <= 0 || physical > 1<<20 || physical > (len(data)-104)/5 {
		return nil, fmt.Errorf("zrb: invalid frame count %d", d.FrameCount)
	}
	// Bound cumulative playback time before a caller multiplies the interval
	// by the frame count. This is host overflow protection [I11].
	if int64(d.FrameDuration) > (1<<63-1)/int64(d.FrameCount) {
		return nil, fmt.Errorf("zrb: movie duration exceeds host limit")
	}
	for i := range d.Tracks {
		f := u32(72 + i*4)
		d.audioFlags[i] = f
		if f&(1<<30) == 0 {
			continue
		}
		if f&(3<<26) != 0 {
			return nil, fmt.Errorf("zrb: unsupported audio codec in track %d", i)
		}
		rate := int(f & 0xffffff)
		if rate == 0 {
			return nil, fmt.Errorf("zrb: zero sample rate in track %d", i)
		}
		channels := 1
		if f&(1<<28) != 0 {
			channels = 2
		}
		d.Tracks[i] = AudioTrack{rate, channels, true}
	}
	treeStart := 104 + physical*5
	treeSize := int(u32(52))
	if treeSize > len(data)-treeStart {
		return nil, fmt.Errorf("zrb: truncated video trees")
	}
	bits := bitReader{data: data[treeStart : treeStart+treeSize]}
	for i := range d.trees {
		t, err := readWordTree(&bits)
		if err != nil {
			return nil, fmt.Errorf("zrb: video tree %d: %w", i, err)
		}
		d.trees[i] = t
	}
	d.packets = make([]packet, physical)
	cursor := treeStart + treeSize
	for i := range d.packets {
		// TODO(question): packet-size bit 1 has no established meaning; an
		// independent asset or format analysis would settle it [fmt zrb].
		size := int(u32(104+i*4) &^ 3)
		if size > len(data)-cursor {
			return nil, fmt.Errorf("zrb: truncated frame %d", i)
		}
		d.packets[i] = packet{cursor, cursor + size, data[104+physical*4+i]}
		cursor += size
	}
	d.frame.Pixels = make([]byte, d.Width*d.Height)
	for i := range d.frame.Palette {
		d.frame.Palette[i].A = 255
	}
	return d, nil
}

// Next returns one logical frame; the optional ring packet is never played.
// After a decoding error the decoder continues returning that error.
func (d *Decoder) Next() (*Frame, error) {
	if d.failure != nil {
		return nil, d.failure
	}
	if d.next >= d.FrameCount {
		return nil, io.EOF
	}
	if err := d.decodeFrame(); err != nil {
		d.failure = fmt.Errorf("zrb: frame %d: %w", d.next, err)
		return nil, d.failure
	}
	d.frame.Index = d.next
	d.next++
	return &d.frame, nil
}

// chunks returns bounded palette, audio and video spans without changing state.
func (d *Decoder) chunks(p packet) ([]byte, [7][]byte, []byte, error) {
	var audio [7][]byte
	b := d.data[p.start:p.end]
	var palette []byte
	if p.mask&1 != 0 {
		if len(b) == 0 || int(b[0])*4 > len(b) || b[0] == 0 {
			return nil, audio, nil, fmt.Errorf("invalid palette chunk length")
		}
		n := int(b[0]) * 4
		palette = b[1:n]
		b = b[n:]
	}
	for i := range audio {
		if p.mask&(2<<i) == 0 {
			continue
		}
		if !d.Tracks[i].Present {
			return nil, audio, nil, fmt.Errorf("audio in absent track %d", i)
		}
		if len(b) < 4 {
			return nil, audio, nil, fmt.Errorf("truncated audio chunk %d", i)
		}
		n := int(binary.LittleEndian.Uint32(b))
		if n < 4 || n > len(b) {
			return nil, audio, nil, fmt.Errorf("invalid audio chunk %d length", i)
		}
		audio[i] = b[4:n]
		b = b[n:]
	}
	return palette, audio, b, nil
}

func (d *Decoder) decodeFrame() error {
	palette, audio, video, err := d.chunks(d.packets[d.next])
	if err != nil {
		return err
	}
	if palette != nil {
		if err := updatePalette(&d.frame.Palette, palette); err != nil {
			return err
		}
	}
	for i := range audio {
		if audio[i] == nil {
			d.frame.Audio[i] = d.frame.Audio[i][:0]
			continue
		}
		pcm, err := decodeAudio(audio[i], d.audioFlags[i], d.frame.Audio[i][:0])
		if err != nil {
			return fmt.Errorf("audio track %d: %w", i, err)
		}
		d.frame.Audio[i] = pcm
	}
	return d.decodeVideo(video)
}

// DecodeAudio decodes a complete logical soundtrack without advancing Next or
// decoding video blocks. The returned PCM is independently owned by the caller.
func (d *Decoder) DecodeAudio(track int) ([]byte, error) {
	if track < 0 || track >= len(d.Tracks) || !d.Tracks[track].Present {
		return nil, fmt.Errorf("zrb: audio track %d is absent", track)
	}
	var result, scratch []byte
	for i := 0; i < d.FrameCount; i++ {
		_, audio, _, err := d.chunks(d.packets[i])
		if err != nil {
			return nil, fmt.Errorf("zrb: frame %d: %w", i, err)
		}
		if audio[track] == nil {
			continue
		}
		scratch, err = decodeAudio(audio[track], d.audioFlags[track], scratch[:0])
		if err != nil {
			return nil, fmt.Errorf("zrb: frame %d audio track %d: %w", i, track, err)
		}
		if len(scratch) > maxAudioTrack-len(result) {
			return nil, fmt.Errorf("zrb: decoded soundtrack exceeds host limit")
		}
		result = append(result, scratch...)
	}
	return result, nil
}

// Palette references always read the old palette, including overlapping moves
// and six-to-eight-bit component expansion [fmt zrb].
func updatePalette(p *[256]color.RGBA, b []byte) error {
	old := *p
	for dst := 0; dst < 256 && len(b) > 0; {
		c := b[0]
		b = b[1:]
		if c&128 != 0 {
			n := int(c&127) + 1
			if n > 256-dst {
				return fmt.Errorf("palette skip exceeds palette")
			}
			dst += n
		} else if c&64 != 0 {
			n := int(c&63) + 1
			if len(b) < 1 {
				return fmt.Errorf("truncated palette copy")
			}
			src := int(b[0])
			b = b[1:]
			if n > 256-dst || n > 256-src {
				return fmt.Errorf("palette copy exceeds palette")
			}
			copy(p[dst:dst+n], old[src:src+n])
			dst += n
		} else {
			if len(b) < 2 || b[0] > 63 || b[1] > 63 {
				return fmt.Errorf("invalid palette color")
			}
			expand := func(v byte) byte { return v<<2 | v>>4 }
			p[dst] = color.RGBA{expand(c), expand(b[0]), expand(b[1]), 255}
			b = b[2:]
			dst++
		}
	}
	return nil
}
