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
// any one of three signals, each strict so that photos are not caught:
//
//   - its URL path names it: a file name word in nameWords (or "logo" or
//     "favicon" anywhere in the file name, for siteLogo.png), or a
//     directory word in dirWords; the host and query are not looked at,
//     and words are whole ("silicon" is not "icon");
//   - the original is smaller than minPhotoWidth by minPhotoHeight
//     pixels, too small to be printed large anyway;
//   - it is flat: the flatColours most common colours of a coarse sample
//     (transparency counted as white) cover flatShare of it, as a logo on
//     a plain field does. A mostly plain background alone is not enough,
//     since a device photographed on white has one too; its shading and
//     shadows spread over many more colours.
func logoLike(rawURL string, img image.Image) bool {
	b := img.Bounds()
	return namedLikeLogo(rawURL) || b.Dx() < minPhotoWidth || b.Dy() < minPhotoHeight || flat(img)
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

// flat reports whether img is a few plain colours (see logoLike).
func flat(img image.Image) bool {
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
	top := 0
	for _, n := range freq[:min(flatColours, len(freq))] {
		top += n
	}
	return float64(top) >= flatShare*flatSamples*flatSamples
}

// quantise flattens c on white and keeps the top 4 bits of each channel.
func quantise(c color.Color) [3]uint32 {
	r, g, b, a := c.RGBA() // premultiplied, 16 bits
	white := 0xffff - a
	return [3]uint32{(r + white) >> 12, (g + white) >> 12, (b + white) >> 12}
}
