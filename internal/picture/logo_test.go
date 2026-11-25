package picture

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"math/rand/v2"
	"testing"
)

// noisyPhoto draws an invented photo-like picture: a gradient with
// per-pixel noise, so it has many colours and no flat background.
func noisyPhoto(w, h int) *image.RGBA {
	r := rand.New(rand.NewPCG(1, 2))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			n := func(base int) uint8 { return uint8(max(0, min(255, base+r.IntN(81)-40))) }
			img.Set(x, y, color.RGBA{n(x * 200 / w), n(y * 200 / h), n(120), 255})
		}
	}
	return img
}

// flatLogo draws an invented two-colour logo: a white field with a dark
// block in the middle.
func flatLogo(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{255, 255, 255, 255}
			if x > w/3 && x < 2*w/3 && y > h/3 && y < 2*h/3 {
				c = color.RGBA{20, 40, 120, 255}
			}
			img.Set(x, y, c)
		}
	}
	return img
}

// darkSky draws an invented night photo: a black sky with noise and a
// grey disc whose shading spans many levels, the kind of picture a
// colour count alone could mistake for a logo.
func darkSky(w, h int) *image.RGBA {
	r := rand.New(rand.NewPCG(3, 4))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	cx, cy, rad := w/2, h/2, h/3
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := r.IntN(12)
			if dx, dy := x-cx, y-cy; dx*dx+dy*dy < rad*rad {
				v = 80 + (x-cx+rad)*150/(2*rad) + r.IntN(20)
			}
			g := uint8(min(255, v))
			img.Set(x, y, color.RGBA{g, g, g, 255})
		}
	}
	return img
}

// asJPEG is img after a round trip through JPEG at a web quality, with
// its ringing around edges.
func asJPEG(t *testing.T, img image.Image) image.Image {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 75}); err != nil {
		t.Fatal(err)
	}
	return decodeJPEG(t, b.Bytes())
}

func TestLogoLike(t *testing.T) {
	photo := noisyPhoto(1200, 800)
	for _, tc := range []struct {
		name string
		url  string
		img  image.Image
		want bool
	}{
		{"photo", "https://example.com/2026/11/rocket-launch.jpg", photo, false},
		{"gradient scene", "https://example.com/a/b.png", scene(1200, 630), false},
		{"night photo", "https://example.com/moon.jpg", darkSky(1200, 800), false},
		{"logo in the file name", "https://example.com/assets/site-logo.png", photo, true},
		{"camel case logo", "https://example.com/img/siteLogo@2x.png", photo, true},
		{"favicon", "https://example.com/favicon-192.png", photo, true},
		{"icon word", "https://example.com/apple-touch-icon.png", photo, true},
		{"logos directory", "https://cdn.example.com/logos/a1b2c3.png", photo, true},
		{"avatar", "https://example.com/u/avatar_42.jpg", photo, true},
		{"placeholder", "https://example.com/img/placeholder.jpg", photo, true},
		{"default image", "https://example.com/og-default.jpg", photo, true},
		{"brand", "https://example.com/brand.png", photo, true},
		{"query is not the path", "https://example.com/photo.jpg?ref=logo", photo, false},
		{"host is not the path", "https://logo.example.com/photo.jpg", photo, false},
		{"silicon is not icon", "https://example.com/silicon-wafer.jpg", photo, false},
		{"iconic is not icon", "https://example.com/iconic-bridge.jpg", photo, false},
		{"drupal default directory", "https://example.com/sites/default/files/2026-11/harbour.jpg", photo, false},
		{"brand directory", "https://example.com/brand-studio/harbour.jpg", photo, false},
		{"narrow original", "https://example.com/a.jpg", noisyPhoto(399, 600), true},
		{"short original", "https://example.com/a.jpg", noisyPhoto(1200, 299), true},
		{"just big enough", "https://example.com/a.jpg", noisyPhoto(400, 300), false},
		{"flat two colours", "https://example.com/share.png", flatLogo(1200, 630), true},
		{"flat logo saved as JPEG", "https://example.com/share.jpg", asJPEG(t, flatLogo(1200, 630)), true},
		{"photo saved as JPEG", "https://example.com/harbour.jpg", asJPEG(t, photo), false},
		{"no url", "", photo, false},
		{"bad url", "://", photo, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := logoLike(tc.url, tc.img); got != tc.want {
				t.Errorf("logoLike(%q) = %v, want %v", tc.url, got, tc.want)
			}
		})
	}
}

// Transparent logos count their transparent field as white, so a
// two-colour mark on transparency is flat too.
func TestLogoLikeTransparentField(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 1200, 630))
	for y := 200; y < 400; y++ {
		for x := 400; x < 800; x++ {
			img.Set(x, y, color.NRGBA{200, 30, 30, 255})
		}
	}
	if !logoLike("https://example.com/share.png", img) {
		t.Error("a two-colour mark on transparency is not logo-like")
	}
}

func TestPrepareSmallPictureCapped(t *testing.T) {
	out, err := Prepare(encodePNG(t, flatLogo(1200, 630)), "https://example.com/share.png")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !out.Small {
		t.Error("a flat logo is not small")
	}
	if b := decodeJPEG(t, out.Data).Bounds(); b.Dx() != SmallWidth {
		t.Errorf("width %d, want %d", b.Dx(), SmallWidth)
	}
}

func TestPrepareSmallByURLCapped(t *testing.T) {
	out, err := Prepare(encodePNG(t, noisyPhoto(1000, 600)), "https://example.com/logo.png")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if b := decodeJPEG(t, out.Data).Bounds(); !out.Small || b.Dx() != SmallWidth {
		t.Errorf("small %v, width %d; want small, %d wide", out.Small, b.Dx(), SmallWidth)
	}
}

func TestPreparePhotoStaysLarge(t *testing.T) {
	out, err := Prepare(encodePNG(t, noisyPhoto(1600, 1000)), "https://example.com/harbour.jpg")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if out.Small {
		t.Error("a photo is small")
	}
	if b := decodeJPEG(t, out.Data).Bounds(); b.Dx() != MaxWidth {
		t.Errorf("width %d, want %d", b.Dx(), MaxWidth)
	}
}

// A tiny original is small but never enlarged.
func TestPrepareTinyNotEnlarged(t *testing.T) {
	out, err := Prepare(encodePNG(t, noisyPhoto(120, 80)), "")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if b := decodeJPEG(t, out.Data).Bounds(); !out.Small || b.Dx() != 120 {
		t.Errorf("small %v, width %d; want small, 120 wide", out.Small, b.Dx())
	}
}
