package picture

import (
	"cmp"
	"image"
	"image/color"
	"maps"
	"net/url"
	"path"
	"slices"
	"strings"
	"unicode"
)

const (
	// A source narrower or shorter than this is too small to be an
	// article's photo; it is an icon or a thumbnail.
	minPhotoWidth  = 400
	minPhotoHeight = 300

	// flatSamples is the side of the grid of pixels sampled for the
	// colour count; flatColours of the most common colours, each channel
	// cut to 4 bits, must cover flatShare of the samples.
	flatSamples = 64
	flatColours = 4
	flatShare   = 0.95
	// fieldShare is how much of a flat picture one colour must cover to
	// be a plain field with a mark on it, such as a site's share card.
	fieldShare = 0.70

	// A flat picture is logo-shaped when nearly square (width over
	// height within [squareMin, squareMax]) or a banner wider than
	// bannerMin; a chart or a diagram is usually neither.
	squareMin = 0.8
	squareMax = 1.25
	bannerMin = 3.0
)

// nameWords are whole words of an image's file name that mark it as a
// logo or a stand-in; dirWords the same for a directory. "default",
// "placeholder" and "brand" are left out of directories: Drupal keeps
// every upload under sites/default/files, and a "brand studio" section
// holds sponsored photos.
var (
	nameWords = []string{"logo", "logos", "icon", "icons", "favicon", "brand", "branding",
		"avatar", "avatars", "placeholder", "default"}
	dirWords = []string{"logo", "logos", "icon", "icons", "favicon", "favicons", "avatar", "avatars"}
)

// logoLike reports whether a preview image is a site's logo or a stand-in
// rather than a photo of the story, so the paper prints it small. It is
// any one of three signals, each strict so that photos, charts and
// diagrams are not caught:
//
//   - its URL path names it: a file name word in nameWords (or "logo" or
//     "favicon" anywhere in the file name, for siteLogo.png), or a
//     directory word in dirWords; the host and query are not looked at,
//     and words are whole ("silicon" is not "icon");
//   - the original is smaller than minPhotoWidth by minPhotoHeight
//     pixels, too small to be printed large anyway;
//   - it is flat, the flatColours most common colours of a coarse sample
//     (transparency counted as white) covering flatShare of it, and
//     either logo-shaped (nearly square or a wide banner) or a plain
//     field, its most common colour covering fieldShare, as a share card
//     with a small mark on it is, whatever its shape. A flat chart or
//     diagram is neither: its bars and lines take more of the field. Flat
//     colours or a plain background alone are not enough: a device
//     photographed on white has a plain background, but its shading and
//     shadows spread over many more colours, and so does a night photo.
//
// For an Aid picture (one the editor asked for to help the reader
// understand) only the first two signals count, never flat colours, so a
// mostly white chart stays large.
func logoLike(src Source, img image.Image) bool {
	b := img.Bounds()
	if namedLikeLogo(src.URL) || b.Dx() < minPhotoWidth || b.Dy() < minPhotoHeight {
		return true
	}
	if src.Aid {
		return false
	}
	top, field := colourShares(img)
	return top >= flatShare && (logoShaped(b.Dx(), b.Dy()) || field >= fieldShare)
}

// logoShaped reports whether a w by h picture is nearly square or a wide banner.
func logoShaped(w, h int) bool {
	aspect := float64(w) / float64(h)
	return (aspect >= squareMin && aspect <= squareMax) || aspect > bannerMin
}

// namedLikeLogo reports whether the URL's path names a logo (see logoLike).
func namedLikeLogo(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	p := strings.ToLower(u.Path)
	dir, file := path.Split(p)
	file = strings.TrimSuffix(file, path.Ext(file))
	if strings.Contains(file, "logo") || strings.Contains(file, "favicon") {
		return true
	}
	if hasWord(file, nameWords) {
		return true
	}
	return hasWord(dir, dirWords)
}

// hasWord reports whether s, cut into runs of letters, holds a word of words.
func hasWord(s string, words []string) bool {
	for _, w := range strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) }) {
		if slices.Contains(words, w) {
			return true
		}
	}
	return false
}

// colourShares samples img and returns the share of the flatColours most
// common colours and of the single most common one (see logoLike).
func colourShares(img image.Image) (top, field float64) {
	b := img.Bounds()
	counts := map[[3]uint32]int{}
	for sy := range flatSamples {
		y := b.Min.Y + (2*sy+1)*b.Dy()/(2*flatSamples)
		for sx := range flatSamples {
			x := b.Min.X + (2*sx+1)*b.Dx()/(2*flatSamples)
			counts[quantise(img.At(x, y))]++
		}
	}
	freq := slices.Collect(maps.Values(counts))
	slices.SortFunc(freq, func(a, b int) int { return cmp.Compare(b, a) })
	n := 0
	for _, c := range freq[:min(flatColours, len(freq))] {
		n += c
	}
	const samples = flatSamples * flatSamples
	return float64(n) / samples, float64(freq[0]) / samples
}

// quantise flattens c on white and keeps the top 4 bits of each channel.
func quantise(c color.Color) [3]uint32 {
	r, g, b, a := c.RGBA() // premultiplied, 16 bits
	white := 0xffff - a
	return [3]uint32{(r + white) >> 12, (g + white) >> 12, (b + white) >> 12}
}
