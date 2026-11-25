// Package picture turns an untrusted downloaded image into a small JPEG
// for the paper: checked for size before decoding, scaled down to the
// page's width (or to a thumbnail's for a logo) and compressed under a
// byte cap.
package picture

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"sync"

	// Decoders for the formats FetchImage accepts.
	_ "image/gif"
	_ "image/png"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	// MaxWidth is the widest picture Prepare returns, in pixels.
	MaxWidth = 800
	// SmallWidth is the widest small (logo-like) picture Prepare returns.
	SmallWidth = 240
	// MaxBytes is the largest JPEG Prepare returns.
	MaxBytes = 90_000

	maxSide        = 8000
	maxPixels      = 40_000_000
	startQuality   = 70
	minQuality     = 40
	qualityStep    = 10
	backgroundGrey = 0xff
)

var (
	// ErrTooManyPixels means the image's header claims more pixels than
	// Prepare will decode.
	ErrTooManyPixels = errors.New("picture: too many pixels")
	// ErrTooLarge means the picture stays over MaxBytes even at the lowest quality.
	ErrTooLarge = errors.New("picture: cannot compress under the byte cap")
)

// decodeMu lets one image at a time be decoded and encoded, so callers
// running in parallel do not multiply the peak memory of a large picture.
var decodeMu sync.Mutex

// decode is image.Decode; tests replace it to watch the serialisation.
var decode = image.Decode

// Picture is a prepared JPEG. Small means the source looked like a logo
// or a stand-in (see logoLike): it is at most SmallWidth wide and the
// paper prints it small.
type Picture struct {
	Data  []byte
	Small bool
}

// Prepare decodes a JPEG, PNG, GIF or WebP image downloaded from rawURL
// and returns it as a JPEG at most MaxWidth pixels wide (SmallWidth when
// Small) and MaxBytes long, transparency flattened on white. The header
// is checked before the image is decoded: sides over 8000 pixels or over
// 40 megapixels are refused, and for WebP every frame header too (it must
// match the canvas; animation is refused). Concurrent calls decode one at
// a time.
func Prepare(data []byte, rawURL string) (Picture, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Picture{}, fmt.Errorf("picture: %w", err)
	}
	if err := checkSize(cfg.Width, cfg.Height); err != nil {
		return Picture{}, err
	}
	if format == "webp" {
		if err := checkWebP(data); err != nil {
			return Picture{}, err
		}
	}
	decodeMu.Lock()
	defer decodeMu.Unlock()
	src, _, err := decode(bytes.NewReader(data))
	if err != nil {
		return Picture{}, fmt.Errorf("picture: %w", err)
	}
	small := logoLike(rawURL, src)
	width := MaxWidth
	if small {
		width = SmallWidth
	}
	out, err := compress(scale(src, width))
	if err != nil {
		return Picture{}, err
	}
	return Picture{Data: out, Small: small}, nil
}

func checkSize(w, h int) error {
	if w <= 0 || h <= 0 || w > maxSide || h > maxSide || w*h > maxPixels {
		return fmt.Errorf("%w: %dx%d", ErrTooManyPixels, w, h)
	}
	return nil
}

// scale returns src on a white canvas, scaled to width when wider and
// keeping its aspect; it never enlarges.
func scale(src image.Image, width int) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > width {
		h = max(1, (h*width+w/2)/w)
		w = width
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.Gray{Y: backgroundGrey}), image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return dst
}

// compress encodes img as a JPEG, lowering the quality until it fits MaxBytes.
func compress(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	for q := startQuality; q >= minQuality; q -= qualityStep {
		buf.Reset()
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: q}); err != nil {
			return nil, fmt.Errorf("picture: %w", err)
		}
		if buf.Len() <= MaxBytes {
			return bytes.Clone(buf.Bytes()), nil
		}
	}
	return nil, fmt.Errorf("%w: %d bytes at quality %d", ErrTooLarge, buf.Len(), minQuality)
}
