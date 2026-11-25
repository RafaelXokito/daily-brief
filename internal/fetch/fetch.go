// Package fetch downloads news articles from untrusted URLs and extracts their text.
package fetch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/markusmobius/go-trafilatura"
)

const (
	maxRedirects     = 5
	defaultTimeout   = 20 * time.Second
	defaultMaxBytes  = 5 << 20
	defaultMinWords  = 150
	defaultMaxWords  = 2500
	defaultWorkers   = 4
	defaultUserAgent = "daily-brief (+https://github.com/RafaelXokito/daily-brief)"
)

var (
	// ErrTooShort means the extracted article has fewer than MinWords words.
	ErrTooShort = errors.New("article too short")
	// ErrNotHTML means the response is not an HTML document.
	ErrNotHTML = errors.New("not an HTML page")
	// ErrBlocked means the scheme or the resolved address is not allowed.
	ErrBlocked = errors.New("address or scheme not allowed")
	// ErrTooLarge means the body exceeds MaxBytes.
	ErrTooLarge = errors.New("page too large")
)

// Options tunes fetching. Zero values select the defaults noted per field.
type Options struct {
	Timeout     time.Duration     // default 20s
	MaxBytes    int64             // default 5 MiB
	MinWords    int               // default 150
	MaxWords    int               // default 2500; text is cut at a sentence end at or before it
	Concurrency int               // default 4
	UserAgent   string            // default "daily-brief (+https://github.com/RafaelXokito/daily-brief)"
	AllowIP     func(net.IP) bool // default PublicIP
}

// Article is the outcome for one URL.
type Article struct {
	Text  string
	Words int
	Err   error
}

func (o Options) withDefaults() Options {
	if o.Timeout <= 0 {
		o.Timeout = defaultTimeout
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = defaultMaxBytes
	}
	if o.MinWords <= 0 {
		o.MinWords = defaultMinWords
	}
	if o.MaxWords <= 0 {
		o.MaxWords = defaultMaxWords
	}
	if o.Concurrency <= 0 {
		o.Concurrency = defaultWorkers
	}
	if o.UserAgent == "" {
		o.UserAgent = defaultUserAgent
	}
	if o.AllowIP == nil {
		o.AllowIP = PublicIP
	}
	return o
}

// Articles fetches each distinct URL once and returns the result per URL.
func Articles(ctx context.Context, urls []string, o Options) map[string]Article {
	o = o.withDefaults()
	client := newClient(o)
	out := make(map[string]Article, len(urls))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, o.Concurrency)
	seen := make(map[string]bool, len(urls))
	for _, u := range urls {
		if seen[u] {
			continue
		}
		seen[u] = true
		wg.Add(1)
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Add(-1)
			mu.Lock()
			out[u] = Article{Err: ctx.Err()}
			mu.Unlock()
			continue
		}
		go func(u string) {
			defer wg.Done()
			defer func() { <-sem }()
			a := fetchOne(ctx, client, u, o)
			mu.Lock()
			out[u] = a
			mu.Unlock()
		}(u)
	}
	wg.Wait()
	return out
}

func newClient(o Options) *http.Client {
	dialer := &net.Dialer{Timeout: o.Timeout, Control: guardControl(o.AllowIP)}
	return &http.Client{
		Timeout: o.Timeout,
		Transport: &http.Transport{
			Proxy:                 nil, // never honour environment proxies: they would bypass the dial guard
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   o.Timeout,
			ResponseHeaderTimeout: o.Timeout,
			DisableKeepAlives:     true,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			return checkScheme(req.URL)
		},
	}
}

func checkScheme(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: scheme %q", ErrBlocked, u.Scheme)
	}
	return nil
}

func fetchOne(ctx context.Context, client *http.Client, raw string, o Options) Article {
	u, err := url.Parse(raw)
	if err != nil {
		return Article{Err: fmt.Errorf("bad url: %w", err)}
	}
	if err := checkScheme(u); err != nil {
		return Article{Err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return Article{Err: err}
	}
	req.Header.Set("User-Agent", o.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := client.Do(req)
	if err != nil {
		return Article{Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Article{Err: fmt.Errorf("http status %d", resp.StatusCode)}
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		mt, _, _ := mime.ParseMediaType(ct)
		if mt != "text/html" && mt != "application/xhtml+xml" {
			return Article{Err: fmt.Errorf("%w: %s", ErrNotHTML, ct)}
		}
	}
	// Read one byte past the limit so an oversized page is refused, not silently truncated.
	body, err := io.ReadAll(io.LimitReader(resp.Body, o.MaxBytes+1))
	if err != nil {
		return Article{Err: err}
	}
	if int64(len(body)) > o.MaxBytes {
		return Article{Err: fmt.Errorf("%w: over %d bytes", ErrTooLarge, o.MaxBytes)}
	}
	res, err := trafilatura.Extract(bytes.NewReader(body), trafilatura.Options{OriginalURL: u, ExcludeComments: true})
	if err != nil {
		return Article{Err: fmt.Errorf("extract: %w", err)}
	}
	if res == nil {
		return Article{Err: ErrTooShort}
	}
	text := strings.TrimSpace(res.ContentText)
	if n := len(strings.Fields(text)); n < o.MinWords {
		return Article{Err: fmt.Errorf("%w: %d words", ErrTooShort, n)}
	}
	text = capWords(text, o.MaxWords)
	return Article{Text: text, Words: len(strings.Fields(text))}
}

// capWords cuts text to at most limit words, backing up to the last sentence end.
func capWords(text string, limit int) string {
	count, inWord, cut := 0, false, len(text)
	for i, r := range text {
		if unicode.IsSpace(r) {
			inWord = false
			continue
		}
		if !inWord {
			if count == limit {
				cut = i
				break
			}
			count++
			inWord = true
		}
	}
	if cut == len(text) {
		return text
	}
	head := strings.TrimRightFunc(text[:cut], unicode.IsSpace)
	if i := strings.LastIndexAny(head, ".!?"); i >= 0 {
		return head[:i+1]
	}
	return head
}
