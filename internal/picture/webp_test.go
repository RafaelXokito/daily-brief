package picture

import (
	"encoding/binary"
	"errors"
	"runtime"
	"testing"
)

// riffChunk is one RIFF chunk, padded to an even length.
func riffChunk(id string, data []byte) []byte {
	b := binary.LittleEndian.AppendUint32([]byte(id), uint32(len(data)))
	b = append(b, data...)
	if len(data)%2 == 1 {
		b = append(b, 0)
	}
	return b
}

func webpFile(chunks ...[]byte) []byte {
	body := []byte("WEBP")
	for _, c := range chunks {
		body = append(body, c...)
	}
	return append(binary.LittleEndian.AppendUint32([]byte("RIFF"), uint32(len(body))), body...)
}

// vp8x is an extended header for a w x h canvas with the given flags.
func vp8x(flags byte, w, h int) []byte {
	p := []byte{flags, 0, 0, 0}
	p = append(p, byte(w-1), byte((w-1)>>8), byte((w-1)>>16))
	return riffChunk("VP8X", append(p, byte(h-1), byte((h-1)>>8), byte((h-1)>>16)))
}

// vp8Frame is a lossy key frame header claiming w x h, with filler data.
func vp8Frame(w, h int) []byte {
	p := []byte{0x10, 0x00, 0x00, 0x9d, 0x01, 0x2a, byte(w), byte(w >> 8), byte(h), byte(h >> 8)}
	return riffChunk("VP8 ", append(p, make([]byte, 64)...))
}

// vp8lFrame is a lossless header claiming w x h, with filler data.
func vp8lFrame(w, h int) []byte {
	b := uint32(w-1) | uint32(h-1)<<14
	p := binary.LittleEndian.AppendUint32([]byte{vp8lMagic}, b)
	return riffChunk("VP8L", append(p, make([]byte, 64)...))
}

func TestPrepareRefusesWebPFrameBombs(t *testing.T) {
	tests := map[string][]byte{
		// The canvas says 1x1, the frame 16383x16383: DecodeConfig sees
		// the canvas, the decoder would allocate for the frame.
		"VP8 in VP8X":               webpFile(vp8x(0, 1, 1), vp8Frame(16383, 16383)),
		"VP8L in VP8X":              webpFile(vp8x(0, 1, 1), vp8lFrame(16383, 16383)),
		"frame differs from canvas": webpFile(vp8x(0, 100, 100), vp8Frame(120, 100)),
		"huge plain VP8L":           webpFile(vp8lFrame(16383, 16383)),
		"huge canvas":               webpFile(vp8x(0, 9000, 10), vp8Frame(9000, 10)),
		"animated flag":             webpFile(vp8x(webpAnimBit, 10, 10), vp8Frame(10, 10)),
		"ANIM chunk":                webpFile(vp8x(0, 10, 10), riffChunk("ANIM", make([]byte, 6)), vp8Frame(10, 10)),
		"ANMF chunk":                webpFile(vp8x(0, 10, 10), riffChunk("ANMF", make([]byte, 16)), vp8Frame(10, 10)),
		"zero-size VP8 frame":       webpFile(vp8x(0, 10, 10), vp8Frame(0, 0)),
		"chunk overruns file":       webpFile(vp8x(0, 10, 10), []byte("VP8 \xff\xff\x00\x00")),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			_, err := Prepare(data)
			runtime.ReadMemStats(&after)
			if err == nil {
				t.Fatal("Prepare accepted the file")
			}
			if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 8<<20 {
				t.Errorf("allocated %d bytes, want a refusal before decoding", alloc)
			}
		})
	}
}

func TestCheckWebPAcceptsMatchingFrames(t *testing.T) {
	for name, data := range map[string][]byte{
		"VP8 in VP8X":  webpFile(vp8x(0, 300, 200), riffChunk("ICCP", make([]byte, 3)), vp8Frame(300, 200)),
		"VP8L in VP8X": webpFile(vp8x(0, 300, 200), vp8lFrame(300, 200)),
		"simple VP8":   webpFile(vp8Frame(8000, 10)),
		"not WebP":     []byte("\x89PNG\r\n\x1a\n"),
	} {
		if err := checkWebP(data); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := checkWebP(webpFile(vp8x(0, 1, 1), vp8Frame(2, 1))); !errors.Is(err, ErrBadWebP) {
		t.Errorf("mismatched frame: err = %v, want ErrBadWebP", err)
	}
}
