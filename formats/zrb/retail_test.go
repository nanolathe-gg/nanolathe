package zrb

import (
	"crypto/md5"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
)

// Reference digests were obtained using FFmpeg 8.1.1 solely as a black-box
// command-line decoder. Video is SHA256 over concatenated per-frame RGB24 MD5
// digests; audio is SHA256 over the complete signed16 little-endian stream.
// This locks every palette, inter-frame dependency and audio predictor in the
// supplied corpus without copying its media into the repository [fmt zrb].
func TestRetailCinematicsMatchReference(t *testing.T) {
	root := testsupport.RetailRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, "Data"))
	if os.IsNotExist(err) {
		t.Skip("optional original cinematic directory is absent")
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, video, audio string }{
		{"1.zrb", "433577d03e8337fdca5f666a76597d1911c145856d395d3f5bcfef0cfae30e14", "c39bd7445bf17e111a5d7dda1ba3b52f8a10fd04413b8f5b3df7d129c9352107"},
		{"2.zrb", "b9a567f03c7edf0c9e765ac89177e5bc96054c99e7bb803fa39202e13ffeffe7", "b41c245b8d46164b80fbc39f5b534b9814757d95d9725346fcfd7f8f0aa21a58"},
		{"3.zrb", "cfdf83f007252953d500a03bd8fe6c0827f30dee4c3f1999ea1ef430562e3f4e", "2150a800554900cb80b7725dc871e8528f048230d936efbcd0ef42dfa837efba"},
		{"4.zrb", "8fa9560ac77f9acbcdde53c29ea50d79dbca350c63de8789c768b8eb1290ee3e", "e2fa9d0e9fb0cc33cfd8de77bb23274de2e06616314f05326b31c5dfd476d2f7"},
		{"5.zrb", "bb86faa9ff49ddab35198dcd8146ffe88d2ae90da6d0aae08f0789a1fb87b5f9", "6cbd8e4c51f78dc9bf9b47b224b5c438a801a7bb5800400d6d9dcd0cb7c7f25a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var path string
			for _, entry := range entries {
				if strings.EqualFold(entry.Name(), tc.name) {
					path = filepath.Join(root, "Data", entry.Name())
					break
				}
			}
			if path == "" {
				t.Skip("optional original cinematic is absent")
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			d, err := New(data)
			if err != nil {
				t.Fatal(err)
			}
			video, audio := sha256.New(), sha256.New()
			rgb := make([]byte, d.Width*d.Height*3)
			for i := 0; i < d.FrameCount; i++ {
				frame, err := d.Next()
				if err != nil {
					t.Fatal(err)
				}
				if frame.Index != i {
					t.Fatalf("frame index = %d, want %d", frame.Index, i)
				}
				for p, index := range frame.Pixels {
					c := frame.Palette[index]
					rgb[p*3] = c.R
					rgb[p*3+1] = c.G
					rgb[p*3+2] = c.B
				}
				sum := md5.Sum(rgb)
				video.Write(sum[:])
				audio.Write(frame.Audio[0])
			}
			if _, err := d.Next(); err != io.EOF {
				t.Fatalf("end of movie = %v", err)
			}
			if got := fmt.Sprintf("%x", video.Sum(nil)); got != tc.video {
				t.Fatalf("video digest = %s, want %s", got, tc.video)
			}
			if got := fmt.Sprintf("%x", audio.Sum(nil)); got != tc.audio {
				t.Fatalf("audio digest = %s, want %s", got, tc.audio)
			}
			pcm, err := d.DecodeAudio(0)
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprintf("%x", sha256.Sum256(pcm)); got != tc.audio {
				t.Fatalf("independent audio digest = %s, want %s", got, tc.audio)
			}
		})
	}
}
