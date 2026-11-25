package dropbox

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestPKCE(t *testing.T) {
	v, ch := NewPKCE()
	if len(v) < 43 || len(v) > 128 {
		t.Fatalf("verifier length %d", len(v))
	}
	sum := sha256.Sum256([]byte(v))
	if want := base64.RawURLEncoding.EncodeToString(sum[:]); ch != want {
		t.Errorf("challenge = %s, want %s", ch, want)
	}
	v2, _ := NewPKCE()
	if v == v2 {
		t.Error("verifier repeated")
	}
}

func TestAuthURL(t *testing.T) {
	u, err := url.Parse(AuthURL("key", "chal", "http://localhost:53682/"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	want := map[string]string{
		"client_id": "key", "response_type": "code", "token_access_type": "offline",
		"code_challenge": "chal", "code_challenge_method": "S256", "redirect_uri": "http://localhost:53682/",
	}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	if u.Host != "www.dropbox.com" || u.Path != "/oauth2/authorize" {
		t.Errorf("url = %s", u)
	}
}

func TestParseRedirect(t *testing.T) {
	cases := []struct {
		in, want string
		err      bool
	}{
		{"http://localhost:53682/?code=abc", "abc", false},
		{"  http://localhost:53682/?code=abc&state=x \n", "abc", false},
		{"abc", "abc", false},
		{"http://localhost:53682/?error=access_denied", "", true},
		{"http://localhost:53682/?error=access_denied&error_description=no", "", true},
		{"http://localhost:53682/", "", true},
		{"", "", true},
	}
	for _, tc := range cases {
		got, err := ParseRedirect(tc.in)
		if (err != nil) != tc.err || got != tc.want {
			t.Errorf("ParseRedirect(%q) = %q, %v", tc.in, got, err)
		}
	}
}

func TestExchangeCode(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form = r.PostForm
		_, _ = w.Write([]byte(`{"access_token":"a","refresh_token":"RT","expires_in":1}`))
	}))
	defer srv.Close()
	got, err := ExchangeCode(context.Background(), srv.Client(), srv.URL, "key", "CODE", "VER", "http://localhost:53682/")
	if err != nil || got != "RT" {
		t.Fatalf("got %q, %v", got, err)
	}
	for k, v := range map[string]string{"code": "CODE", "grant_type": "authorization_code", "client_id": "key", "code_verifier": "VER", "redirect_uri": "http://localhost:53682/"} {
		if form.Get(k) != v {
			t.Errorf("%s = %q", k, form.Get(k))
		}
	}
}

func TestExchangeCodeErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()
	if _, err := ExchangeCode(context.Background(), srv.Client(), srv.URL, "k", "c", "v", "r"); err == nil {
		t.Error("expected error")
	}
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"a"}`))
	}))
	defer srv2.Close()
	if _, err := ExchangeCode(context.Background(), srv2.Client(), srv2.URL, "k", "c", "v", "r"); err == nil {
		t.Error("expected error for missing refresh_token")
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a := l.Addr().String()
	_ = l.Close()
	return a
}

func TestListen(t *testing.T) {
	addr := freeAddr(t)
	type res struct {
		code string
		err  error
	}
	ch := make(chan res, 1)
	go func() {
		c, err := Listen(context.Background(), addr)
		ch <- res{c, err}
	}()
	var body string
	for i := 0; i < 50; i++ {
		resp, err := http.Get("http://" + addr + "/?code=XYZ")
		if err == nil {
			b := make([]byte, 512)
			n, _ := resp.Body.Read(b)
			body = string(b[:n])
			_ = resp.Body.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	r := <-ch
	if r.err != nil || r.code != "XYZ" {
		t.Fatalf("got %q, %v", r.code, r.err)
	}
	if !strings.Contains(body, "close this tab") {
		t.Errorf("body = %q", body)
	}
}

func TestListenCancelAndError(t *testing.T) {
	addr := freeAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := Listen(ctx, addr); done <- err }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("expected error on cancel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Listen did not return after cancel")
	}

	addr = freeAddr(t)
	go func() { _, err := Listen(context.Background(), addr); done <- err }()
	for i := 0; i < 50; i++ {
		resp, err := http.Get("http://" + addr + "/?error=access_denied")
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := <-done; err == nil {
		t.Error("expected error for error= redirect")
	}
}
