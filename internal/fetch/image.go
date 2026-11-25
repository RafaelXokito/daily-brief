package fetch

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// maxImageBytes caps an image download, whatever Options.MaxBytes says.
const maxImageBytes = 5 << 20

// ErrNotImage means the response is not a JPEG, PNG, GIF or WebP image.
var ErrNotImage = errors.New("not a supported image")

var imageTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true}

// FetchImage downloads one image through the same guard as Articles
// (public addresses only, no environment proxy, checked redirects) and
// returns its bytes, at most 5 MiB, and its media type.
func FetchImage(ctx context.Context, url string, o Options) ([]byte, string, error) {
	o = o.withDefaults()
	body, mt, _, err := get(ctx, newClient(o), url, o, "image/jpeg,image/png,image/gif,image/webp", maxImageBytes, func(mt string) error {
		if !imageTypes[mt] {
			return fmt.Errorf("%w: %q", ErrNotImage, mt)
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return body, mt, nil
}

// imageURL resolves raw against base and returns it when it is an
// absolute http(s) URL with a host, else "".
func imageURL(raw string, base *url.URL) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	ref, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	abs := base.ResolveReference(ref)
	if (abs.Scheme != "http" && abs.Scheme != "https") || abs.Host == "" {
		return ""
	}
	return abs.String()
}
