package picture

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"runtime"
	"sync"
	"testing"
	"time"
)

// scene draws an invented picture: smooth gradients with a few blocks,
// something a camera might produce more than flat colour does.
func scene(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), uint8((x + y) * 127 / (w + h)), 255}
			if (x/(w/6)+y/(h/4))%3 == 0 {
				c.B = 200
			}
			img.Set(x, y, c)
		}
	}
	return img
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func decodeJPEG(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("output is not a JPEG: %v", err)
	}
	return img
}

func TestPrepareScalesAndCompresses(t *testing.T) {
	out, err := Prepare(encodePNG(t, scene(2400, 1600)), "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(out.Data) > MaxBytes {
		t.Errorf("output %d bytes, want at most %d", len(out.Data), MaxBytes)
	}
	b := decodeJPEG(t, out.Data).Bounds()
	if b.Dx() != 800 {
		t.Errorf("width %d, want 800", b.Dx())
	}
	if want := 800 * 1600 / 2400; b.Dy() < want-1 || b.Dy() > want+1 {
		t.Errorf("height %d, want %d within 1 px", b.Dy(), want)
	}
}

func TestPrepareNeverUpscales(t *testing.T) {
	out, err := Prepare(encodePNG(t, scene(600, 400)), "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if b := decodeJPEG(t, out.Data).Bounds(); b.Dx() != 600 || b.Dy() != 400 {
		t.Errorf("size %dx%d, want 600x400", b.Dx(), b.Dy())
	}
}

func TestPrepareFormats(t *testing.T) {
	src := scene(120, 80)
	var j, g bytes.Buffer
	if err := jpeg.Encode(&j, src, nil); err != nil {
		t.Fatal(err)
	}
	if err := gif.Encode(&g, src, nil); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"jpeg": j.Bytes(), "gif": g.Bytes(), "png": encodePNG(t, src)} {
		out, err := Prepare(data, "")
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if b := decodeJPEG(t, out.Data).Bounds(); b.Dx() != 120 {
			t.Errorf("%s: width %d", name, b.Dx())
		}
	}
}

func TestPrepareFlattensTransparencyOnWhite(t *testing.T) {
	out, err := Prepare(encodePNG(t, image.NewNRGBA(image.Rect(0, 0, 40, 40))), "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	r, g, b, _ := decodeJPEG(t, out.Data).At(20, 20).RGBA()
	if r>>8 < 240 || g>>8 < 240 || b>>8 < 240 {
		t.Errorf("transparent pixel became (%d,%d,%d), want white", r>>8, g>>8, b>>8)
	}
}

func TestPrepareRefusesGarbage(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("not an image"), []byte("<svg xmlns='http://www.w3.org/2000/svg'/>")} {
		if _, err := Prepare(data, ""); err == nil {
			t.Errorf("Prepare(%q) accepted", data)
		}
	}
}

// pngChunk is one PNG chunk: length, type, data, CRC.
func pngChunk(typ string, data []byte) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.BigEndian, uint32(len(data)))
	b.WriteString(typ)
	b.Write(data)
	binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(append([]byte(typ), data...)))
	return b.Bytes()
}

// bombPNG is a grey PNG whose header claims w x h pixels but whose image
// data holds only a few rows: tiny in bytes, a 400 MB allocation for a
// decoder that trusts the header.
func bombPNG(w, h uint32) []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8], ihdr[9] = 8, 0 // 8-bit greyscale
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	zw.Write(make([]byte, 4*(int(w)+1)))
	zw.Close()
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	b.Write(pngChunk("IHDR", ihdr))
	b.Write(pngChunk("IDAT", z.Bytes()))
	b.Write(pngChunk("IEND", nil))
	return b.Bytes()
}

func TestPrepareRefusesHugeDimensions(t *testing.T) {
	tests := map[string][2]uint32{
		"20000x20000":       {20000, 20000},
		"too wide":          {8001, 10},
		"too tall":          {10, 8001},
		"over 40 megapixel": {8000, 5001},
	}
	for name, wh := range tests {
		t.Run(name, func(t *testing.T) {
			data := bombPNG(wh[0], wh[1])
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			start := time.Now()
			_, err := Prepare(data, "")
			elapsed := time.Since(start)
			runtime.ReadMemStats(&after)
			if err == nil {
				t.Fatal("Prepare accepted a picture beyond the pixel cap")
			}
			if elapsed > time.Second {
				t.Errorf("took %v, want a refusal before decoding", elapsed)
			}
			if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 8<<20 {
				t.Errorf("allocated %d bytes, want a refusal before decoding", alloc)
			}
		})
	}
}

func TestPrepareAcceptsAtTheCap(t *testing.T) {
	// 8000x10 is within every cap; the refusal must not be off by one.
	img := image.NewGray(image.Rect(0, 0, 8000, 10))
	if _, err := Prepare(encodePNG(t, img), ""); err != nil {
		t.Errorf("Prepare refused an 8000x10 picture: %v", err)
	}
}

func TestPrepareGivesUpWhenItCannotShrink(t *testing.T) {
	// Noise at 800x800 does not fit 90 KB even at quality 40.
	img := image.NewRGBA(image.Rect(0, 0, 800, 800))
	seed := uint32(1)
	for i := range img.Pix {
		seed = seed*1664525 + 1013904223
		img.Pix[i] = uint8(seed >> 24)
	}
	if _, err := Prepare(encodePNG(t, img), ""); err == nil {
		t.Error("Prepare returned a picture over the byte cap")
	}
}

func TestPrepareDecodesOneAtATime(t *testing.T) {
	var mu sync.Mutex
	active, peak := 0, 0
	orig := decode
	decode = func(r io.Reader) (image.Image, string, error) {
		mu.Lock()
		active++
		peak = max(peak, active)
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		defer func() { mu.Lock(); active--; mu.Unlock() }()
		return orig(r)
	}
	defer func() { decode = orig }()
	data := encodePNG(t, scene(60, 40))
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := Prepare(data, ""); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if peak != 1 {
		t.Errorf("%d decodes ran at once, want 1", peak)
	}
}
