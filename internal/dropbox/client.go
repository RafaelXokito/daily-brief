// Package dropbox is a minimal Dropbox API client (net/http only) used to
// deliver the daily EPUB to the Kobo's Dropbox folder, plus the one-time PKCE
// login helpers and refresh-token storage.
package dropbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

const (
	defaultTokenURL    = "https://api.dropboxapi.com/oauth2/token"
	defaultAPIBase     = "https://api.dropboxapi.com/2"
	defaultContentBase = "https://content.dropboxapi.com/2"

	maxAttempts     = 3
	baseBackoff     = time.Second
	maxResponseBody = 8 << 20
	maxRetryAfter   = 60 * time.Second
	listPageLimit   = 100
	tokenSkew       = time.Minute
	defaultTimeout  = 2 * time.Minute
)

// ErrConflict means the upload target already exists (HTTP 409, path/conflict).
var ErrConflict = errors.New("dropbox: path conflict")

// ErrNotFound means the path (or folder) does not exist (HTTP 409, not_found).
var ErrNotFound = errors.New("dropbox: path not found")

// Client talks to the Dropbox HTTP API with a PKCE app key and a long-lived
// refresh token. The zero values of HTTP and the URL fields select defaults.
type Client struct {
	AppKey, RefreshToken           string
	HTTP                           *http.Client
	TokenURL, APIBase, ContentBase string

	mu      sync.Mutex
	access  string
	expires time.Time
	// Sleep is a test seam for backoff waits; nil means a real context-aware sleep.
	Sleep func(ctx context.Context, d time.Duration) error
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: defaultTimeout}
}

func orDefault(v, def string) string {
	if v != "" {
		return strings.TrimRight(v, "/")
	}
	return def
}

func (c *Client) doSleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Upload stores data at path (mode add, no autorename). It returns ErrConflict
// when the path already exists.
func (c *Client) Upload(ctx context.Context, path string, data []byte) error {
	arg := struct {
		Path       string `json:"path"`
		Mode       string `json:"mode"`
		Autorename bool   `json:"autorename"`
		Mute       bool   `json:"mute"`
	}{path, "add", false, true}
	raw, err := json.Marshal(arg)
	if err != nil {
		return err
	}
	_, err = c.call(ctx, "files/upload", func(tok string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, orDefault(c.ContentBase, defaultContentBase)+"/files/upload", bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("Dropbox-API-Arg", asciiJSON(raw))
		return req, nil
	})
	return err
}

// List returns the names of the files (not folders) in folder, following pages.
// The root is "/" here and "" for the API; a trailing slash is dropped.
func (c *Client) List(ctx context.Context, folder string) ([]string, error) {
	folder = strings.TrimRight(folder, "/")
	var names []string
	endpoint, body := "files/list_folder", map[string]any{"path": folder, "limit": listPageLimit}
	for {
		resp, err := c.apiJSON(ctx, endpoint, body)
		if err != nil {
			return nil, err
		}
		var page struct {
			Entries []struct {
				Tag  string `json:".tag"`
				Name string `json:"name"`
			} `json:"entries"`
			HasMore bool   `json:"has_more"`
			Cursor  string `json:"cursor"`
		}
		if err := json.Unmarshal(resp, &page); err != nil {
			return nil, fmt.Errorf("dropbox: decode %s: %w", endpoint, err)
		}
		for _, e := range page.Entries {
			if e.Tag == "file" {
				names = append(names, e.Name)
			}
		}
		if !page.HasMore {
			return names, nil
		}
		endpoint, body = "files/list_folder/continue", map[string]any{"cursor": page.Cursor}
	}
}

// Delete removes path with files/delete_v2.
func (c *Client) Delete(ctx context.Context, path string) error {
	_, err := c.apiJSON(ctx, "files/delete_v2", map[string]any{"path": path})
	return err
}

// Move relocates from to to with files/move_v2. With autorename, Dropbox
// picks a free name ("name (1).ext") when to already exists. It returns
// ErrNotFound when from does not exist.
func (c *Client) Move(ctx context.Context, from, to string, autorename bool) error {
	_, err := c.apiJSON(ctx, "files/move_v2", map[string]any{
		"from_path": from, "to_path": to, "autorename": autorename, "allow_ownership_transfer": false,
	})
	return err
}

func (c *Client) apiJSON(ctx context.Context, endpoint string, body any) ([]byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return c.call(ctx, endpoint, func(tok string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, orDefault(c.APIBase, defaultAPIBase)+"/"+endpoint, bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	})
}

// call obtains an access token, then sends the request built by build with
// retries. An expired or invalid access token is refreshed and retried once.
func (c *Client) call(ctx context.Context, name string, build func(token string) (*http.Request, error)) ([]byte, error) {
	for refreshed := false; ; refreshed = true {
		tok, err := c.accessToken(ctx)
		if err != nil {
			return nil, err
		}
		status, body, err := c.send(ctx, func() (*http.Request, error) { return build(tok) })
		if err != nil {
			return nil, fmt.Errorf("dropbox: %s: %w", name, err)
		}
		if status >= 200 && status < 300 {
			return body, nil
		}
		summary := errorField(body, "error_summary")
		if status == http.StatusUnauthorized && !refreshed && isTokenExpired(summary) {
			c.clearToken()
			continue
		}
		return nil, statusError(name, status, summary)
	}
}

func isTokenExpired(summary string) bool {
	return strings.HasPrefix(summary, "invalid_access_token") || strings.HasPrefix(summary, "expired_access_token")
}

func statusError(name string, status int, summary string) error {
	if status == http.StatusConflict {
		switch {
		case strings.HasPrefix(summary, "path/conflict"):
			return fmt.Errorf("dropbox: %s: %w (%s)", name, ErrConflict, summary)
		case strings.HasPrefix(summary, "path_lookup/not_found"), strings.HasPrefix(summary, "path/not_found"), strings.HasPrefix(summary, "from_lookup/not_found"):
			return fmt.Errorf("dropbox: %s: %w (%s)", name, ErrNotFound, summary)
		}
	}
	return fmt.Errorf("dropbox: %s: HTTP %d %s", name, status, summary)
}

func (c *Client) clearToken() {
	c.mu.Lock()
	c.access, c.expires = "", time.Time{}
	c.mu.Unlock()
}

// send performs the request, retrying 429 and 5xx up to maxAttempts times with
// exponential backoff from one second, honouring Retry-After.
func (c *Client) send(ctx context.Context, build func() (*http.Request, error)) (int, []byte, error) {
	backoff := baseBackoff
	for attempt := 1; ; attempt++ {
		req, err := build()
		if err != nil {
			return 0, nil, err
		}
		resp, err := c.httpClient().Do(req)
		var status int
		var body []byte
		var retryAfter time.Duration
		if err == nil {
			status = resp.StatusCode
			body, _ = io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
			retryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
			_ = resp.Body.Close()
		} else if ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		transient := err != nil || status == http.StatusTooManyRequests || status >= 500
		if !transient {
			return status, body, nil
		}
		if attempt >= maxAttempts {
			if err != nil {
				return 0, nil, fmt.Errorf("giving up after %d attempts: %w", attempt, urlErrorNoQuery(err))
			}
			return status, body, nil
		}
		wait := backoff
		if retryAfter > wait {
			wait = retryAfter
		}
		if err := c.doSleep(ctx, wait); err != nil {
			return 0, nil, err
		}
		backoff *= 2
	}
}

// urlErrorNoQuery unwraps *url.Error so the message carries no request URL.
func urlErrorNoQuery(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func parseRetryAfter(v string) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return 0
	}
	return min(time.Duration(n)*time.Second, maxRetryAfter)
}

// errorField extracts one string field from a JSON error body, never echoing
// the raw body (which could in principle contain request material).
func errorField(body []byte, field string) string {
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return ""
	}
	s, _ := m[field].(string)
	return s
}

func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.access != "" && time.Now().Before(c.expires) {
		return c.access, nil
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {c.RefreshToken},
		"client_id":     {c.AppKey},
	}
	status, body, err := c.send(ctx, func() (*http.Request, error) {
		return formRequest(ctx, orDefault(c.TokenURL, defaultTokenURL), form)
	})
	if err != nil {
		return "", fmt.Errorf("dropbox: token refresh: %w", err)
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("dropbox: token refresh: HTTP %d %s", status, errorField(body, "error"))
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tr); err != nil || tr.AccessToken == "" {
		return "", errors.New("dropbox: token refresh: no access_token in response")
	}
	c.access = tr.AccessToken
	c.expires = time.Now().Add(time.Duration(tr.ExpiresIn)*time.Second - tokenSkew)
	return c.access, nil
}

func formRequest(ctx context.Context, endpoint string, form url.Values) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req, nil
}

// asciiJSON rewrites JSON text so every rune above 0x7E becomes \uXXXX (UTF-16
// surrogate pairs above the BMP). Dropbox-API-Arg must be ASCII; non-ASCII can
// only occur inside JSON strings, where \u escapes are equivalent.
func asciiJSON(raw []byte) string {
	var b strings.Builder
	for _, r := range string(raw) {
		if r <= 0x7E {
			b.WriteRune(r)
			continue
		}
		for _, u := range utf16.Encode([]rune{r}) {
			fmt.Fprintf(&b, `\u%04x`, u)
		}
	}
	return b.String()
}
