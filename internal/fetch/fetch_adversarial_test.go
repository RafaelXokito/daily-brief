package fetch

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLoopbackBlockedByDefault(t *testing.T) {
	s := serve(t, page(400))
	a := Articles(context.Background(), []string{s.URL}, Options{})[s.URL]
	if !errors.Is(a.Err, ErrBlocked) {
		t.Errorf("err = %v, want ErrBlocked", a.Err)
	}
}

func TestRedirectToLoopbackBlocked(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer target.Close()
	// 127.0.0.2 is loopback too but not on the allow list below.
	dest := strings.Replace(target.URL, "127.0.0.1", "127.0.0.2", 1)
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest, http.StatusFound)
	}))
	defer first.Close()
	opts := Options{AllowIP: func(ip net.IP) bool { return ip.String() == "127.0.0.1" }}
	a := Articles(context.Background(), []string{first.URL}, opts)[first.URL]
	if !errors.Is(a.Err, ErrBlocked) {
		t.Errorf("err = %v, want ErrBlocked", a.Err)
	}
	if hits.Load() != 0 {
		t.Error("redirect target was contacted")
	}
}

func TestNonHTTPSchemes(t *testing.T) {
	urls := []string{"file:///etc/passwd", "gopher://x", "ftp://x"}
	got := Articles(context.Background(), urls, Options{AllowIP: allowLoopback})
	for _, u := range urls {
		if !errors.Is(got[u].Err, ErrBlocked) {
			t.Errorf("%s: err = %v, want ErrBlocked", u, got[u].Err)
		}
	}
}

func TestOversizedBody(t *testing.T) {
	s := serve(t, "<html><body><p>"+strings.Repeat("harbor lantern copper. ", 6<<20/23)+"</p></body></html>")
	a := Articles(context.Background(), []string{s.URL}, Options{AllowIP: allowLoopback})[s.URL]
	if !errors.Is(a.Err, ErrTooLarge) {
		t.Errorf("err = %v, want ErrTooLarge", a.Err)
	}
}

func TestTooManyRedirects(t *testing.T) {
	var hits atomic.Int32
	mux := http.NewServeMux()
	var n atomic.Int32
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, fmt.Sprintf("/hop%d", n.Add(1)), http.StatusFound)
	})
	s := httptest.NewServer(mux)
	defer s.Close()
	a := Articles(context.Background(), []string{s.URL}, Options{AllowIP: allowLoopback})[s.URL]
	if a.Err == nil {
		t.Fatal("expected an error")
	}
	if h := hits.Load(); h != 6 {
		t.Errorf("hits = %d, want 6 (original plus 5 hops)", h)
	}
}

func TestLocalhostNameBlockedByDefault(t *testing.T) {
	s := serve(t, page(400))
	u := strings.Replace(s.URL, "127.0.0.1", "localhost", 1)
	a := Articles(context.Background(), []string{u}, Options{})[u]
	if !errors.Is(a.Err, ErrBlocked) {
		t.Errorf("err = %v, want ErrBlocked", a.Err)
	}
}

func TestRedirectToFileSchemeBlocked(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
	}))
	defer s.Close()
	a := Articles(context.Background(), []string{s.URL}, Options{AllowIP: allowLoopback})[s.URL]
	if !errors.Is(a.Err, ErrBlocked) {
		t.Errorf("err = %v, want ErrBlocked", a.Err)
	}
	if a.Text != "" {
		t.Error("text returned from a blocked redirect")
	}
}
