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

func allowLoopback(ip net.IP) bool { return ip.IsLoopback() || PublicIP(ip) }

// words builds n words of invented prose in sentences of ten words.
func words(n int) string {
	vocab := []string{"harbor", "lantern", "copper", "meadow", "signal", "orchard", "ledger", "violet", "granite", "ribbon", "compass", "thistle"}
	var b strings.Builder
	for i := 0; i < n; i++ {
		w := vocab[(i*7+i/3)%len(vocab)]
		if i%10 == 0 {
			w = strings.ToUpper(w[:1]) + w[1:]
		}
		b.WriteString(w)
		if i%10 == 9 || i == n-1 {
			b.WriteString(".")
			if i%30 == 29 {
				b.WriteString("</p><p>")
			} else {
				b.WriteString(" ")
			}
		} else {
			b.WriteString(" ")
		}
	}
	return b.String()
}

func page(n int) string {
	return "<html><head><title>Invented report</title></head><body><nav>Home About</nav><article><h1>Invented report</h1><p>" +
		words(n) + "</p></article><footer>Contact</footer></body></html>"
}

func serve(t *testing.T, body string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(s.Close)
	return s
}

func TestPublicIP(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", false}, {"::1", false}, {"10.0.0.1", false}, {"172.16.0.1", false},
		{"192.168.1.1", false}, {"169.254.1.1", false}, {"fe80::1", false}, {"100.64.0.1", false},
		{"0.0.0.0", false}, {"224.0.0.1", false},
		{"::ffff:127.0.0.1", false}, {"::ffff:10.0.0.1", false}, {"fd00::1", false}, {"fc00::1", false},
		{"169.254.169.254", false}, {"64:ff9b::7f00:1", false}, {"64:ff9b::a9fe:a9fe", false},
		{"64:ff9b:1::1", false}, {"2002:7f00:1::", false}, {"::7f00:1", false},
		{"0.1.2.3", false}, {"192.0.0.1", false}, {"198.18.0.1", false}, {"240.0.0.1", false},
		{"192.0.2.1", false}, {"198.51.100.1", false}, {"203.0.113.1", false},
		{"2001:db8::1", false}, {"fec0::1", false},
		{"93.184.216.34", true}, {"2606:4700::1111", true},
	}
	for _, c := range cases {
		if got := PublicIP(net.ParseIP(c.ip)); got != c.want {
			t.Errorf("PublicIP(%s) = %v, want %v", c.ip, got, c.want)
		}
	}
}

func TestArticleExtracted(t *testing.T) {
	s := serve(t, page(400))
	a := Articles(context.Background(), []string{s.URL}, Options{AllowIP: allowLoopback})[s.URL]
	if a.Err != nil {
		t.Fatal(a.Err)
	}
	if a.Words < 300 {
		t.Errorf("Words = %d, want >= 300", a.Words)
	}
	if strings.Contains(a.Text, "<") {
		t.Errorf("text contains markup: %q", a.Text[:80])
	}
}

func TestArticleCappedAtMaxWords(t *testing.T) {
	s := serve(t, page(4000))
	a := Articles(context.Background(), []string{s.URL}, Options{AllowIP: allowLoopback, MaxWords: 2500})[s.URL]
	if a.Err != nil {
		t.Fatal(a.Err)
	}
	if a.Words > 2500 || a.Words < 2000 {
		t.Errorf("Words = %d, want within (2000, 2500]", a.Words)
	}
	if last := a.Text[len(a.Text)-1:]; !strings.Contains(".!?", last) {
		t.Errorf("text ends with %q, want a sentence end", last)
	}
}

func TestTooShort(t *testing.T) {
	s := serve(t, page(80))
	a := Articles(context.Background(), []string{s.URL}, Options{AllowIP: allowLoopback})[s.URL]
	if !errors.Is(a.Err, ErrTooShort) {
		t.Errorf("err = %v, want ErrTooShort", a.Err)
	}
}

func TestNotHTML(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		fmt.Fprint(w, "%PDF-1.4 not really")
	}))
	defer s.Close()
	a := Articles(context.Background(), []string{s.URL}, Options{AllowIP: allowLoopback})[s.URL]
	if !errors.Is(a.Err, ErrNotHTML) {
		t.Errorf("err = %v, want ErrNotHTML", a.Err)
	}
}

func TestDuplicateURLsFetchedOnce(t *testing.T) {
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, page(400))
	}))
	defer s.Close()
	got := Articles(context.Background(), []string{s.URL, s.URL}, Options{AllowIP: allowLoopback})
	if hits.Load() != 1 || len(got) != 1 {
		t.Errorf("hits = %d, results = %d, want 1 and 1", hits.Load(), len(got))
	}
}
