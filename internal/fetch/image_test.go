package fetch

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// pageWithImage is an invented article whose head names image as its preview.
func pageWithImage(image string) string {
	return `<html><head><title>Invented report</title><meta property="og:image" content="` + image +
		`"></head><body><article><h1>Invented report</h1><p>` + words(400) + `</p></article></body></html>`
}

func TestArticleImageFromMeta(t *testing.T) {
	s := serve(t, pageWithImage("/i.jpg"))
	a := Articles(context.Background(), []string{s.URL + "/news/a"}, Options{AllowIP: allowLoopback})[s.URL+"/news/a"]
	if a.Err != nil {
		t.Fatalf("err = %v", a.Err)
	}
	if a.Image != s.URL+"/i.jpg" {
		t.Errorf("Image = %q, want %q", a.Image, s.URL+"/i.jpg")
	}
	for _, bad := range []string{"data:image/png;base64,iVBORw0KGgo=", "javascript:alert(1)"} {
		s := serve(t, pageWithImage(bad))
		a := Articles(context.Background(), []string{s.URL}, Options{AllowIP: allowLoopback})[s.URL]
		if a.Err != nil {
			t.Fatalf("%s: err = %v", bad, a.Err)
		}
		if a.Image != "" {
			t.Errorf("%s: Image = %q, want empty", bad, a.Image)
		}
	}
}

func TestImageURL(t *testing.T) {
	base, _ := url.Parse("https://news.example/world/today.html")
	cases := map[string]string{
		"/i.jpg":                         "https://news.example/i.jpg",
		"pics/i.jpg":                     "https://news.example/world/pics/i.jpg",
		"//cdn.example/i.jpg":            "https://cdn.example/i.jpg",
		"http://cdn.example/i.jpg":       "http://cdn.example/i.jpg",
		"  https://cdn.example/i.jpg  ":  "https://cdn.example/i.jpg",
		"":                               "",
		"data:image/png;base64,AAAA":     "",
		"javascript:alert(1)":            "",
		"file:///etc/passwd":             "",
		"ftp://cdn.example/i.jpg":        "",
		"https:///no-host.jpg":           "",
		"https://cdn.example/%zz":        "",
		"JavaScript:alert(document.url)": "",
	}
	for in, want := range cases {
		if got := imageURL(in, base); got != want {
			t.Errorf("imageURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func serveImage(t *testing.T, contentType string, body []byte) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Write(body)
	}))
	t.Cleanup(s.Close)
	return s
}

func TestFetchImage(t *testing.T) {
	body := []byte("\xff\xd8\xff invented jpeg bytes")
	for _, ct := range []string{"image/jpeg", "image/png", "image/gif", "image/webp", "image/JPEG; charset=binary"} {
		s := serveImage(t, ct, body)
		data, got, err := FetchImage(context.Background(), s.URL, Options{AllowIP: allowLoopback})
		if err != nil {
			t.Fatalf("%s: err = %v", ct, err)
		}
		if !bytes.Equal(data, body) {
			t.Errorf("%s: body %q", ct, data)
		}
		if want := strings.ToLower(strings.SplitN(ct, ";", 2)[0]); got != want {
			t.Errorf("content type %q, want %q", got, want)
		}
	}
}

func TestFetchImageBlocked(t *testing.T) {
	s := serveImage(t, "image/jpeg", []byte("\xff\xd8\xff"))
	if _, _, err := FetchImage(context.Background(), s.URL, Options{}); !errors.Is(err, ErrBlocked) {
		t.Errorf("loopback: err = %v, want ErrBlocked", err)
	}

	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer target.Close()
	// 127.0.0.2 stands in for a private address: loopback, not on the allow list below.
	dest := strings.Replace(target.URL, "127.0.0.1", "127.0.0.2", 1)
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest, http.StatusFound)
	}))
	defer first.Close()
	opts := Options{AllowIP: func(ip net.IP) bool { return ip.String() == "127.0.0.1" }}
	if _, _, err := FetchImage(context.Background(), first.URL, opts); !errors.Is(err, ErrBlocked) {
		t.Errorf("redirect to a private address: err = %v, want ErrBlocked", err)
	}
	if hits.Load() != 0 {
		t.Error("redirect target was contacted")
	}

	for _, u := range []string{"file:///etc/passwd", "data:image/png;base64,AAAA", "ftp://x/i.jpg"} {
		if _, _, err := FetchImage(context.Background(), u, Options{AllowIP: allowLoopback}); !errors.Is(err, ErrBlocked) {
			t.Errorf("%s: err = %v, want ErrBlocked", u, err)
		}
	}
}

func TestFetchImageNotImage(t *testing.T) {
	for _, ct := range []string{"text/html; charset=utf-8", "image/svg+xml", "application/octet-stream", ""} {
		s := serveImage(t, ct, []byte("<html></html>"))
		if _, _, err := FetchImage(context.Background(), s.URL, Options{AllowIP: allowLoopback}); !errors.Is(err, ErrNotImage) {
			t.Errorf("%q: err = %v, want ErrNotImage", ct, err)
		}
	}
}

func TestFetchImageTooLarge(t *testing.T) {
	s := serveImage(t, "image/jpeg", bytes.Repeat([]byte{0xff}, maxImageBytes+1))
	if _, _, err := FetchImage(context.Background(), s.URL, Options{AllowIP: allowLoopback}); !errors.Is(err, ErrTooLarge) {
		t.Errorf("err = %v, want ErrTooLarge", err)
	}
}

func TestFetchImageStatus(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.WriteHeader(http.StatusNotFound)
	}))
	defer s.Close()
	if _, _, err := FetchImage(context.Background(), s.URL, Options{AllowIP: allowLoopback}); err == nil {
		t.Error("a 404 was accepted")
	}
}
