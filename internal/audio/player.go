package audio

import (
	"encoding/binary"
	"math"
	"sync"
)

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

// PanFloat converts a Pan to this host backend's balance factor. The existing
// linear-X rule is a presentation policy, not a claimed retail stereo gain
// law: retail delegates that curve to DirectSound. TODO(question): establish
// the platform mix curve needed to replace this approximation [03 R-AUD-01 §1].
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

// ConvertSample converts a Sample to float32 stereo LE bytes at dstRate with
// volume and pan baked in [03 §8.2] [fmt wav]. Handles 8-bit mono 11025,
// 16-bit mono/stereo, and resamples via linear interpolation.
// Volume is linear 0..1, pan -1..1.
func ConvertSample(s *Sample, volume float64, pan float64, dstRate int) []byte {
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
	// Apply volume and pan — the stereo branch of the mixer's placement step
	// [03 §8.3]. Pan -1 is left, 1 is right, 0 is centred; the balance is
	// leftVol = vol * (1 - pan) blended against the opposite channel.
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

// OutputConfig is the presentation-owned state that reaches a device. It is
// retained before a device exists, so loading preferences ahead of the window
// startup produces the same state as a later live options change [03
// R-AUD-01 §2].
type OutputConfig struct {
	MasterEnabled bool
	EffectsVolume float64
	SoundMode     SoundMode
	MixingBuffers int // configured voice limit [03 R-AUD-01 §2]
}

// ConfigurableOutput is the optional output setup seam. Ordinary Output
// implementations retain the PCM fallback; a newly installed configurable
// device immediately receives the retained presentation configuration.
type ConfigurableOutput interface {
	Output
	ConfigureOutput(OutputConfig)
}

// RegisteredOutput optionally accepts a mode-0 registered sample through a
// sample-owned canonical PCM cache. Outputs that do not implement it retain
// the ordinary PlaySample path.
type RegisteredOutput interface {
	Output
	PlayRegisteredSample(*Sample, float64, float64) error
}

// LoopingRegisteredOutput is the optional exclusive-loop seam for registered
// aliases. It deliberately has no ordinary-output fallback [03 R-AUD-01 §1].
type LoopingRegisteredOutput interface {
	RegisteredOutput
	PlayLoopingRegisteredSample(*Sample, float64, float64) error
}

// VoiceOutput owns the ordinary voice table. It is optional because outputs
// that expose only playback stay valid presentation boundaries.
type VoiceOutput interface {
	Output
	StopVoices()
}

// StreamOutput is the optional delayed-stream seam used by briefing and
// glamour narration. Ordinary cue outputs need only implement Output; stream
// support is discovered at the presentation boundary and missing support is
// silent [03 R-AUD-02 §1].
type StreamOutput interface {
	Output
	PlayStream(*Sample, float64) error
	StopStream()
}

// globalOutput is the presentation singleton used by the audio service [I6].
var (
	globalMu     sync.Mutex
	globalOutput Output
	globalConfig = OutputConfig{MasterEnabled: true, EffectsVolume: 1, SoundMode: SoundModeMono, MixingBuffers: 8}
)

// SetGlobalOutput installs the presentation playback boundary and gives a
// configurable output the current retained configuration [I6].
func SetGlobalOutput(o Output) {
	globalMu.Lock()
	globalOutput = o
	config := globalConfig
	globalMu.Unlock()
	if configurable, ok := o.(ConfigurableOutput); ok {
		configurable.ConfigureOutput(config)
	}
}

// GlobalOutput returns the installed presentation playback boundary [I6].
func GlobalOutput() Output {
	globalMu.Lock()
	defer globalMu.Unlock()
	return globalOutput
}

// ConfigureOutput retains presentation configuration and applies it to the
// currently installed device when that device accepts the optional seam. It
// never reaches simulation state [I6].
func ConfigureOutput(config OutputConfig) {
	globalMu.Lock()
	globalConfig = config
	output := globalOutput
	globalMu.Unlock()
	if configurable, ok := output.(ConfigurableOutput); ok {
		configurable.ConfigureOutput(config)
	}
}
