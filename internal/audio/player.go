package audio

import (
	"bytes"
	"encoding/binary"
	"math"
	"sync"

	"github.com/hajimehoshi/ebiten/v2/audio"
)

// Backend is the PCM output over ebiten/v2/audio [03 §8.3] [03 §8.2].
// It owns the audio context and a small player pool, consumes decoded Samples,
// and applies volume/pan from the positional math. Device construction remains
// lazy until the first admitted sample reaches the presentation edge.
type Backend struct {
	sampleRate int
	master     bool
	effects    float64
	mu         sync.Mutex
	ctx        *audio.Context
	players    []*audio.Player
	next       int
}

// NewBackend creates the lazy real presentation backend [03 §8.2].
func NewBackend() *Backend {
	return NewBackendWithRate(44100)
}

// NewBackendWithRate creates a backend with explicit sample rate [03 §8.2] C20.
// Rate 0 defaults to 44100.
func NewBackendWithRate(rate int) *Backend {
	if rate <= 0 {
		rate = 44100
	}
	return &Backend{
		sampleRate: rate,
		master:     true,
		effects:    1,
	}
}

// BackendCapabilities describes the platform-neutral audio surface exposed to
// presentation. The device is created lazily by PlaySample.
type BackendCapabilities struct {
	Device bool
	Stereo bool
}

func (b *Backend) Capabilities() BackendCapabilities {
	if b == nil {
		return BackendCapabilities{}
	}
	return BackendCapabilities{Device: true, Stereo: true}
}

func (b *Backend) StereoCapable() bool {
	return b != nil
}

func (b *Backend) SetMasterEnabled(enabled bool) {
	if b != nil {
		b.master = enabled
	}
}

func (b *Backend) SetEffectsVolume(volume float64) {
	if b == nil {
		return
	}
	if volume < 0 {
		volume = 0
	}
	if volume > 1 {
		volume = 1
	}
	b.effects = volume
}

func (b *Backend) CanPlay() bool {
	return b != nil && b.master && b.effects > 0
}

// SampleRate returns the backend sample rate.
func (b *Backend) SampleRate() int {
	if b == nil || b.sampleRate == 0 {
		return 44100
	}
	return b.sampleRate
}

// VolumeFromCentibel converts the DirectSound centibel volumes [03 §8.3]
// -585 in-view vs -1585 off-screen to linear 0..1 via 10^(dB/20) where dB=v/100.
func VolumeFromCentibel(v int32) float64 {
	// dB = v / 100, linear = 10^(dB/20) = 10^(v/2000)
	lin := math.Pow(10, float64(v)/2000.0)
	if lin < 0 {
		lin = 0
	}
	if lin > 1 {
		lin = 1
	}
	return lin
}

// VolumeFromAttenuation maps the two-level attenuation [03 §8.3] to linear.
func VolumeFromAttenuation(v int32) float64 {
	switch v {
	case VolInView:
		return VolumeFromCentibel(VolInView)
	case VolOffScreen:
		return VolumeFromCentibel(VolOffScreen)
	default:
		return VolumeFromCentibel(v)
	}
}

// PanFloat converts a Pan and Viewport to a stereo pan factor -1..1 [03 §8.3].
// dx = pan.X is viewport-relative in pixel*16 units; normalizing by viewport
// half-width*16 maps the audible field to -1..1. Off-screen clamps.
func PanFloat(p Pan, v Viewport) float64 {
	if v.Width == 0 {
		return 0
	}
	// dx range is roughly -Width*8 .. +Width*8 (see ComputePan dx = px - ((w/2)<<4) - left)
	half := float64(v.Width) * 8.0
	if half == 0 {
		return 0
	}
	f := float64(p.X) / half
	if f < -1 {
		f = -1
	} else if f > 1 {
		f = 1
	}
	return f
}

func (b *Backend) ensureContext() {
	if b == nil {
		return
	}
	if b.ctx != nil {
		return
	}
	// Reuse existing singleton if already created (ebiten allows at most one).
	if cur := audio.CurrentContext(); cur != nil {
		b.ctx = cur
		return
	}
	// Try to create; if it panics due to existing, recover via CurrentContext.
	func() {
		defer func() {
			if r := recover(); r != nil {
				if cur := audio.CurrentContext(); cur != nil {
					b.ctx = cur
				}
			}
		}()
		b.ctx = audio.NewContext(b.sampleRate)
	}()
}

// PlaySample plays a decoded Sample with volume 0..1 and pan -1..1 [03 §8.2] [03 §8.3].
// Volume is linear 0..1 (from VolumeFromCentibel), pan -1 left, 1 right.
func (b *Backend) PlaySample(s *Sample, volume float64, pan float64) error {
	if b == nil || s == nil {
		return nil
	}
	if !b.CanPlay() {
		return nil
	}
	volume *= b.effects
	b.ensureContext()
	if b.ctx == nil {
		return nil
	}
	if volume < 0 {
		volume = 0
	} else if volume > 1 {
		volume = 1
	}
	if pan < -1 {
		pan = -1
	} else if pan > 1 {
		pan = 1
	}
	data := convertSample(s, volume, pan, b.sampleRate)
	if len(data) == 0 {
		return nil
	}
	// Use float32 stereo path (preferred for future) [ebiten audio].
	p, err := b.ctx.NewPlayerF32(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	// Balance pan is already baked into the samples; overall volume also baked.
	// Keep player at unity.
	b.mu.Lock()
	if len(b.players) < 8 {
		b.players = append(b.players, p)
	} else {
		// circular pool: close oldest at next before replace
		old := b.players[b.next]
		if old != nil {
			_ = old.Close()
		}
		b.players[b.next] = p
		b.next = (b.next + 1) % len(b.players)
	}
	b.mu.Unlock()
	p.Play()
	return nil
}

// PlayAlias loads an alias via cache and plays it with volume/pan [03 §8.2] C20.
// Missing alias degrades silently [P1-02 §2.2].
func (b *Backend) PlayAlias(alias string, cache *SampleCache, volume float64, pan float64) error {
	if b == nil || alias == "" {
		return nil
	}
	if cache == nil {
		return nil
	}
	s, err := cache.Load(alias)
	if err != nil || s == nil {
		// Degrade silently [P1-02 §2.2].
		return nil
	}
	return b.PlaySample(s, volume, pan)
}

// Close releases players (presentation-only) [I6].
func (b *Backend) Close() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, p := range b.players {
		if p != nil {
			_ = p.Close()
		}
	}
	b.players = nil
	b.next = 0
}

// convertSample converts a Sample to float32 stereo LE bytes at dstRate with
// volume and pan baked in [03 §8.2] [fmt wav]. Handles 8-bit mono 11025,
// 16-bit mono/stereo, and resamples via linear interpolation.
// Volume is linear 0..1, pan -1..1.
func convertSample(s *Sample, volume float64, pan float64, dstRate int) []byte {
	if s == nil || len(s.Data) == 0 || dstRate <= 0 {
		return nil
	}
	srcRate := int(s.SampleRate)
	if srcRate <= 0 {
		srcRate = 11025
	}
	channels := int(s.Channels)
	if channels <= 0 {
		channels = 1
	}
	bits := int(s.BitsPerSample)
	if bits == 0 {
		bits = 8
	}
	// Decode to float32 channels at srcRate, already mono-duplicated to stereo interim.
	var srcLeft, srcRight []float32
	switch {
	case bits == 8 && channels == 1:
		n := len(s.Data)
		srcLeft = make([]float32, n)
		srcRight = make([]float32, n)
		for i, b := range s.Data {
			f := float32(int(b)-128) / 128.0
			srcLeft[i] = f
			srcRight[i] = f
		}
	case bits == 8 && channels == 2:
		n := len(s.Data) / 2
		srcLeft = make([]float32, n)
		srcRight = make([]float32, n)
		for i := 0; i < n; i++ {
			l := s.Data[i*2]
			r := s.Data[i*2+1]
			srcLeft[i] = float32(int(l)-128) / 128.0
			srcRight[i] = float32(int(r)-128) / 128.0
		}
	case bits == 16 && channels == 1:
		n := len(s.Data) / 2
		srcLeft = make([]float32, n)
		srcRight = make([]float32, n)
		for i := 0; i < n; i++ {
			v := int16(binary.LittleEndian.Uint16(s.Data[i*2 : i*2+2]))
			f := float32(v) / 32768.0
			srcLeft[i] = f
			srcRight[i] = f
		}
	case bits == 16 && channels == 2:
		n := len(s.Data) / 4
		srcLeft = make([]float32, n)
		srcRight = make([]float32, n)
		for i := 0; i < n; i++ {
			off := i * 4
			l := int16(binary.LittleEndian.Uint16(s.Data[off : off+2]))
			r := int16(binary.LittleEndian.Uint16(s.Data[off+2 : off+4]))
			srcLeft[i] = float32(l) / 32768.0
			srcRight[i] = float32(r) / 32768.0
		}
	default:
		// Unsupported format degrades silently [P1-02 §2.2]
		return nil
	}
	srcFrames := len(srcLeft)
	if srcFrames == 0 {
		return nil
	}
	// Resample to dstRate via linear interpolation [03 §8.2] C20.
	dstFrames := srcFrames
	if srcRate != dstRate {
		dstFrames = int(int64(srcFrames) * int64(dstRate) / int64(srcRate))
		if dstFrames <= 0 {
			dstFrames = 1
		}
	}
	dstLeft := make([]float32, dstFrames)
	dstRight := make([]float32, dstFrames)
	if srcRate == dstRate {
		copy(dstLeft, srcLeft)
		copy(dstRight, srcRight)
	} else {
		for i := 0; i < dstFrames; i++ {
			// srcPos = i * srcRate / dstRate
			srcPos := float64(i) * float64(srcRate) / float64(dstRate)
			idx := int(srcPos)
			frac := float32(srcPos - float64(idx))
			if idx < 0 {
				idx = 0
				frac = 0
			}
			if idx >= srcFrames-1 {
				dstLeft[i] = srcLeft[srcFrames-1]
				dstRight[i] = srcRight[srcFrames-1]
			} else {
				l0 := srcLeft[idx]
				l1 := srcLeft[idx+1]
				r0 := srcRight[idx]
				r1 := srcRight[idx+1]
				dstLeft[i] = l0 + (l1-l0)*frac
				dstRight[i] = r0 + (r1-r0)*frac
			}
		}
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// Pan -1 left, 1 right, 0 center. Use balance: leftVol = vol*(1 - pan) blend.
	var leftVol, rightVol float64
	if pan < -1 {
		pan = -1
	} else if pan > 1 {
		pan = 1
	}
	if volume < 0 {
		volume = 0
	} else if volume > 1 {
		volume = 1
	}
	if pan < 0 {
		leftVol = volume
		rightVol = volume * (1 + pan) // pan negative reduces right
		if rightVol < 0 {
			rightVol = 0
		}
	} else if pan > 0 {
		rightVol = volume
		leftVol = volume * (1 - pan)
		if leftVol < 0 {
			leftVol = 0
		}
	} else {
		leftVol = volume
		rightVol = volume
	}
	out := make([]byte, dstFrames*8) // 2 channels * 4 bytes float32
	for i := 0; i < dstFrames; i++ {
		l := float32(float64(dstLeft[i]) * leftVol)
		r := float32(float64(dstRight[i]) * rightVol)
		if l < -1 {
			l = -1
		} else if l > 1 {
			l = 1
		}
		if r < -1 {
			r = -1
		} else if r > 1 {
			r = 1
		}
		binary.LittleEndian.PutUint32(out[i*8:], math.Float32bits(l))
		binary.LittleEndian.PutUint32(out[i*8+4:], math.Float32bits(r))
	}
	return out
}

// Output is the narrow presentation playback boundary. Production installs a
// Backend; tests can observe admitted requests with a local fake without
// constructing a device.
type Output interface {
	PlaySample(*Sample, float64, float64) error
}

// globalOutput is the presentation singleton used by the audio service [I6].
var (
	globalMu     sync.Mutex
	globalOutput Output
)

// SetGlobalOutput installs the presentation playback boundary [I6].
func SetGlobalOutput(o Output) {
	globalMu.Lock()
	globalOutput = o
	globalMu.Unlock()
}

// GlobalOutput returns the installed presentation playback boundary [I6].
func GlobalOutput() Output {
	globalMu.Lock()
	defer globalMu.Unlock()
	return globalOutput
}

// SetGlobalBackend installs the concrete backend used by production clients.
func SetGlobalBackend(b *Backend) {
	SetGlobalOutput(b)
}

// GlobalBackend returns the installed concrete backend, if production owns it.
func GlobalBackend() *Backend {
	globalMu.Lock()
	defer globalMu.Unlock()
	b, _ := globalOutput.(*Backend)
	return b
}
