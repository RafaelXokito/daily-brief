package dropbox

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	authorizeURL = "https://www.dropbox.com/oauth2/authorize"
	// DefaultListenAddr is where Listen waits for the OAuth redirect.
	DefaultListenAddr = "localhost:53682"
)

// NewPKCE returns a random code verifier and its S256 challenge.
func NewPKCE() (verifier, challenge string) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("dropbox: no randomness: " + err.Error())
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

// AuthURL builds the authorisation URL for the code flow with PKCE, asking for
// an offline (refresh) token.
func AuthURL(appKey, challenge, redirect string) string {
	q := url.Values{
		"client_id":             {appKey},
		"response_type":         {"code"},
		"token_access_type":     {"offline"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"redirect_uri":          {redirect},
	}
	return authorizeURL + "?" + q.Encode()
}

// ParseRedirect extracts the authorisation code from a pasted redirect URL or
// a bare code. A redirect carrying error= is an error.
func ParseRedirect(pasted string) (string, error) {
	s := strings.TrimSpace(pasted)
	if s == "" {
		return "", errors.New("dropbox: empty authorisation code")
	}
	if !strings.Contains(s, "?") && !strings.Contains(s, "://") {
		return s, nil
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("dropbox: cannot parse redirect URL: %w", err)
	}
	return codeFromQuery(u.Query())
}

func codeFromQuery(q url.Values) (string, error) {
	if e := q.Get("error"); e != "" {
		return "", fmt.Errorf("dropbox: authorisation refused: %s", e)
	}
	if c := q.Get("code"); c != "" {
		return c, nil
	}
	return "", errors.New("dropbox: no code in redirect")
}

// ExchangeCode trades an authorisation code for a refresh token.
func ExchangeCode(ctx context.Context, h *http.Client, tokenURL, appKey, code, verifier, redirect string) (string, error) {
	if h == nil {
		h = &http.Client{Timeout: defaultTimeout}
	}
	req, err := formRequest(ctx, tokenURL, url.Values{
		"code":          {code},
		"grant_type":    {"authorization_code"},
		"client_id":     {appKey},
		"code_verifier": {verifier},
		"redirect_uri":  {redirect},
	})
	if err != nil {
		return "", err
	}
	resp, err := h.Do(req)
	if err != nil {
		return "", fmt.Errorf("dropbox: code exchange: %w", urlErrorNoQuery(err))
	}
	defer func() { _ = resp.Body.Close() }()
	var tr struct {
		RefreshToken string `json:"refresh_token"`
		Error        string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, maxErrBody)).Decode(&tr)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("dropbox: code exchange: HTTP %d %s", resp.StatusCode, tr.Error)
	}
	if tr.RefreshToken == "" {
		return "", errors.New("dropbox: code exchange: no refresh_token in response (was token_access_type=offline requested?)")
	}
	return tr.RefreshToken, nil
}

// Listen serves one OAuth redirect on addr (default localhost:53682) and
// returns the authorisation code.
func Listen(ctx context.Context, addr string) (string, error) {
	if addr == "" {
		addr = DefaultListenAddr
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("dropbox: listen on %s: %w", addr, err)
	}
	type result struct {
		code string
		err  error
	}
	ch := make(chan result, 1)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second}
	srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("code") == "" && q.Get("error") == "" {
			http.NotFound(w, r) // favicon and other stray requests
			return
		}
		code, err := codeFromQuery(q)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err != nil {
			_, _ = fmt.Fprint(w, "<!doctype html><title>daily-brief</title><p>Authorisation was refused. You can close this tab.")
		} else {
			_, _ = fmt.Fprint(w, "<!doctype html><title>daily-brief</title><p>Dropbox is connected. You can close this tab.")
		}
		select {
		case ch <- result{code, err}:
		default:
		}
	})
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	select {
	case r := <-ch:
		return r.code, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
