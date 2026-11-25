package dropbox

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testRefresh = "refresh-secret-123"
	testAccess  = "access-secret-456"
)

type recorded struct {
	Path   string
	Header http.Header
	Body   []byte
	Form   map[string]string
}

type fakeDropbox struct {
	mu       sync.Mutex
	reqs     []recorded
	handlers map[string]func(w http.ResponseWriter, r *http.Request, n int)
	counts   map[string]int
}

func newFake(t *testing.T) (*fakeDropbox, *Client) {
	t.Helper()
	f := &fakeDropbox{
		handlers: map[string]func(http.ResponseWriter, *http.Request, int){},
		counts:   map[string]int{},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec := recorded{Path: r.URL.Path, Header: r.Header.Clone(), Body: body}
		if r.URL.Path == "/oauth2/token" {
			vals, _ := url.ParseQuery(string(body))
			rec.Form = map[string]string{}
			for k := range vals {
				rec.Form[k] = vals.Get(k)
			}
		}
		f.mu.Lock()
		f.reqs = append(f.reqs, rec)
		f.counts[r.URL.Path]++
		n := f.counts[r.URL.Path]
		h := f.handlers[r.URL.Path]
		f.mu.Unlock()
		if r.URL.Path == "/oauth2/token" && h == nil {
			_, _ = w.Write([]byte(`{"access_token":"` + testAccess + `","expires_in":14400}`))
			return
		}
		if h == nil {
			http.Error(w, "unexpected "+r.URL.Path, 500)
			return
		}
		h(w, r, n)
	}))
	t.Cleanup(srv.Close)
	c := &Client{
		AppKey: "appkey", RefreshToken: testRefresh, HTTP: srv.Client(),
		TokenURL: srv.URL + "/oauth2/token", APIBase: srv.URL + "/2", ContentBase: srv.URL + "/2",
	}
	return f, c
}

func (f *fakeDropbox) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.counts[path]
}

func (f *fakeDropbox) paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var p []string
	for _, r := range f.reqs {
		p = append(p, r.Path)
	}
	return p
}

func (f *fakeDropbox) find(path string) recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.reqs {
		if r.Path == path {
			return r
		}
	}
	return recorded{}
}

func okJSON(body string) func(http.ResponseWriter, *http.Request, int) {
	return func(w http.ResponseWriter, _ *http.Request, _ int) { _, _ = w.Write([]byte(body)) }
}

func TestUploadRequest(t *testing.T) {
	f, c := newFake(t)
	f.handlers["/2/files/upload"] = okJSON(`{}`)
	data := []byte("epub-bytes\x00\x01")
	name := "/Apps/Rakuten Kobo/The Daily Brief 2026-11-25.epub"
	if err := c.Upload(context.Background(), name, data); err != nil {
		t.Fatal(err)
	}
	if got := f.paths(); len(got) != 2 || got[0] != "/oauth2/token" || got[1] != "/2/files/upload" {
		t.Fatalf("request order = %v", got)
	}
	tok := f.find("/oauth2/token")
	if tok.Form["grant_type"] != "refresh_token" || tok.Form["refresh_token"] != testRefresh || tok.Form["client_id"] != "appkey" {
		t.Errorf("refresh form = %v", tok.Form)
	}
	up := f.find("/2/files/upload")
	if up.Header.Get("Authorization") != "Bearer "+testAccess {
		t.Errorf("auth = %q", up.Header.Get("Authorization"))
	}
	if ct := up.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("content-type = %q", ct)
	}
	want := `{"path":"/Apps/Rakuten Kobo/The Daily Brief 2026-11-25.epub","mode":"add","autorename":false,"mute":true}`
	if got := up.Header.Get("Dropbox-API-Arg"); got != want {
		t.Errorf("arg = %s\nwant  %s", got, want)
	}
	if string(up.Body) != string(data) {
		t.Errorf("body mismatch")
	}
}

func TestUploadNonASCIIName(t *testing.T) {
	f, c := newFake(t)
	f.handlers["/2/files/upload"] = okJSON(`{}`)
	name := "/Apps/Rakuten Kobo/Le Brief Quotidien · été 2026-11-25.epub"
	if err := c.Upload(context.Background(), name, []byte("x")); err != nil {
		t.Fatal(err)
	}
	arg := f.find("/2/files/upload").Header.Get("Dropbox-API-Arg")
	for _, r := range arg {
		if r > 0x7E || r < 0x20 {
			t.Fatalf("non-ASCII rune %U in header %q", r, arg)
		}
	}
	if !strings.Contains(arg, `\u00b7`) {
		t.Errorf("expected \\u escape in %s", arg)
	}
	var decoded struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(arg), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Path != name {
		t.Errorf("decoded path = %q", decoded.Path)
	}
}

func TestAPIArgJSONEscapes(t *testing.T) {
	cases := []struct{ in, want string }{
		{"abc", `"abc"`},
		{"é", `"\u00e9"`},
		{"😀", `"\ud83d\ude00"`},
		{"a\"b\\c", `"a\"b\\c"`},
		{"~", `"~"`},
		{"\x7f", `"\u007f"`},
	}
	for _, tc := range cases {
		b, _ := json.Marshal(tc.in)
		if got := asciiJSON(b); !strings.EqualFold(got, tc.want) {
			t.Errorf("asciiJSON(%q) = %s, want %s", tc.in, got, tc.want)
		}
		var back string
		if err := json.Unmarshal([]byte(asciiJSON(b)), &back); err != nil || back != tc.in {
			t.Errorf("roundtrip %q -> %q (%v)", tc.in, back, err)
		}
	}
}

func TestUploadConflict(t *testing.T) {
	f, c := newFake(t)
	f.handlers["/2/files/upload"] = func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.WriteHeader(409)
		_, _ = w.Write([]byte(`{"error_summary":"path/conflict/file/..","error":{".tag":"path"}}`))
	}
	err := c.Upload(context.Background(), "/Apps/Rakuten Kobo/x.epub", []byte("x"))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v", err)
	}
}

func TestListPaging(t *testing.T) {
	f, c := newFake(t)
	f.handlers["/2/files/list_folder"] = func(w http.ResponseWriter, r *http.Request, _ int) {
		_, _ = w.Write([]byte(`{"entries":[{".tag":"file","name":"a.epub"},{".tag":"folder","name":"sub"}],"has_more":true,"cursor":"CUR"}`))
	}
	f.handlers["/2/files/list_folder/continue"] = okJSON(`{"entries":[{".tag":"file","name":"b.pdf"}],"has_more":false,"cursor":"CUR2"}`)
	got, err := c.List(context.Background(), "/Apps/Rakuten Kobo")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "a.epub,b.pdf" {
		t.Errorf("names = %v", got)
	}
	if b := string(f.find("/2/files/list_folder").Body); !strings.Contains(b, `"/Apps/Rakuten Kobo"`) {
		t.Errorf("list body = %s", b)
	}
	if b := string(f.find("/2/files/list_folder/continue").Body); !strings.Contains(b, `"CUR"`) {
		t.Errorf("continue body = %s", b)
	}
}

func TestDeleteRequest(t *testing.T) {
	f, c := newFake(t)
	f.handlers["/2/files/delete_v2"] = okJSON(`{"metadata":{}}`)
	if err := c.Delete(context.Background(), "/Apps/Rakuten Kobo/old.epub"); err != nil {
		t.Fatal(err)
	}
	d := f.find("/2/files/delete_v2")
	var body map[string]string
	if err := json.Unmarshal(d.Body, &body); err != nil || body["path"] != "/Apps/Rakuten Kobo/old.epub" {
		t.Errorf("body = %s (%v)", d.Body, err)
	}
	if d.Header.Get("Content-Type") != "application/json" {
		t.Errorf("content-type = %q", d.Header.Get("Content-Type"))
	}
}

func TestTokenCachedPerClient(t *testing.T) {
	f, c := newFake(t)
	f.handlers["/2/files/delete_v2"] = okJSON(`{}`)
	for i := 0; i < 2; i++ {
		if err := c.Delete(context.Background(), "/x"); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.count("/oauth2/token"); n != 1 {
		t.Errorf("refresh calls = %d", n)
	}
}

func TestErrorsCarryNoToken(t *testing.T) {
	f, c := newFake(t)
	f.handlers["/2/files/delete_v2"] = func(w http.ResponseWriter, r *http.Request, _ int) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error_summary":"invalid_access_token/.."}`))
	}
	err := c.Delete(context.Background(), "/x")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), testRefresh) || strings.Contains(err.Error(), testAccess) {
		t.Errorf("error leaks token: %v", err)
	}
	// token endpoint failure echoing the request must not leak either
	f2, c2 := newFake(t)
	f2.handlers["/oauth2/token"] = func(w http.ResponseWriter, r *http.Request, _ int) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"refresh_token ` + testRefresh + ` expired"}`))
	}
	err = c2.Delete(context.Background(), "/x")
	if err == nil || strings.Contains(err.Error(), testRefresh) {
		t.Errorf("token error leaks refresh token: %v", err)
	}
}

func TestRetriesTransient(t *testing.T) {
	t.Run("503 then 200", func(t *testing.T) {
		f, c := newFake(t)
		var slept []time.Duration
		c.Sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
		f.handlers["/2/files/delete_v2"] = func(w http.ResponseWriter, _ *http.Request, n int) {
			if n == 1 {
				w.WriteHeader(503)
				return
			}
			_, _ = w.Write([]byte(`{}`))
		}
		if err := c.Delete(context.Background(), "/x"); err != nil {
			t.Fatal(err)
		}
		if f.count("/2/files/delete_v2") != 2 || len(slept) != 1 || slept[0] != time.Second {
			t.Errorf("calls=%d slept=%v", f.count("/2/files/delete_v2"), slept)
		}
	})
	t.Run("429 Retry-After", func(t *testing.T) {
		f, c := newFake(t)
		var slept []time.Duration
		c.Sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
		f.handlers["/2/files/delete_v2"] = func(w http.ResponseWriter, _ *http.Request, n int) {
			if n == 1 {
				w.Header().Set("Retry-After", "5")
				w.WriteHeader(429)
				return
			}
			_, _ = w.Write([]byte(`{}`))
		}
		if err := c.Delete(context.Background(), "/x"); err != nil {
			t.Fatal(err)
		}
		if len(slept) != 1 || slept[0] != 5*time.Second {
			t.Errorf("slept = %v", slept)
		}
	})
	t.Run("three 503s fail", func(t *testing.T) {
		f, c := newFake(t)
		var slept []time.Duration
		c.Sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
		f.handlers["/2/files/delete_v2"] = func(w http.ResponseWriter, _ *http.Request, _ int) { w.WriteHeader(503) }
		if err := c.Delete(context.Background(), "/x"); err == nil {
			t.Fatal("expected error")
		}
		if f.count("/2/files/delete_v2") != 3 || len(slept) != 2 || slept[1] != 2*time.Second {
			t.Errorf("calls=%d slept=%v", f.count("/2/files/delete_v2"), slept)
		}
	})
	t.Run("400 no retry", func(t *testing.T) {
		f, c := newFake(t)
		c.Sleep = func(context.Context, time.Duration) error { t.Error("slept"); return nil }
		f.handlers["/2/files/delete_v2"] = func(w http.ResponseWriter, _ *http.Request, _ int) { w.WriteHeader(400) }
		if err := c.Delete(context.Background(), "/x"); err == nil {
			t.Fatal("expected error")
		}
		if f.count("/2/files/delete_v2") != 1 {
			t.Errorf("calls = %d", f.count("/2/files/delete_v2"))
		}
	})
	t.Run("upload body resent on retry", func(t *testing.T) {
		f, c := newFake(t)
		c.Sleep = func(context.Context, time.Duration) error { return nil }
		f.handlers["/2/files/upload"] = func(w http.ResponseWriter, r *http.Request, n int) {
			if n == 1 {
				w.WriteHeader(500)
				return
			}
			_, _ = w.Write([]byte(`{}`))
		}
		if err := c.Upload(context.Background(), "/x.epub", []byte("DATA")); err != nil {
			t.Fatal(err)
		}
		var bodies []string
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, r := range f.reqs {
			if r.Path == "/2/files/upload" {
				bodies = append(bodies, string(r.Body))
			}
		}
		if len(bodies) != 2 || bodies[0] != "DATA" || bodies[1] != "DATA" {
			t.Errorf("bodies = %q", bodies)
		}
	})
}

func TestRetryAfterCapped(t *testing.T) {
	f, c := newFake(t)
	var slept []time.Duration
	c.Sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
	f.handlers["/2/files/delete_v2"] = func(w http.ResponseWriter, _ *http.Request, n int) {
		if n == 1 {
			w.Header().Set("Retry-After", "86400")
			w.WriteHeader(429)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}
	if err := c.Delete(context.Background(), "/x"); err != nil {
		t.Fatal(err)
	}
	if len(slept) != 1 || slept[0] != time.Minute {
		t.Errorf("slept = %v", slept)
	}
}

func TestListLargePage(t *testing.T) {
	f, c := newFake(t)
	var sb strings.Builder
	sb.WriteString(`{"entries":[`)
	const n = 1500
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{".tag":"file","name":"The Daily Brief 2026-01-01 padding padding padding padding.epub"}`)
	}
	sb.WriteString(`],"has_more":false,"cursor":"C"}`)
	if sb.Len() < 100<<10 {
		t.Fatalf("fixture too small: %d", sb.Len())
	}
	f.handlers["/2/files/list_folder"] = okJSON(sb.String())
	got, err := c.List(context.Background(), "/f")
	if err != nil || len(got) != n {
		t.Fatalf("got %d names, %v", len(got), err)
	}
	var body map[string]any
	if err := json.Unmarshal(f.find("/2/files/list_folder").Body, &body); err != nil || body["limit"] != float64(100) {
		t.Errorf("list body = %s", f.find("/2/files/list_folder").Body)
	}
}

func TestNotFound(t *testing.T) {
	for _, summary := range []string{"path_lookup/not_found/..", "path/not_found/.."} {
		f, c := newFake(t)
		h := func(w http.ResponseWriter, _ *http.Request, _ int) {
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"error_summary":"` + summary + `"}`))
		}
		f.handlers["/2/files/delete_v2"] = h
		f.handlers["/2/files/list_folder"] = h
		if err := c.Delete(context.Background(), "/x"); !errors.Is(err, ErrNotFound) {
			t.Errorf("Delete %s: %v", summary, err)
		}
		if _, err := c.List(context.Background(), "/x"); !errors.Is(err, ErrNotFound) {
			t.Errorf("List %s: %v", summary, err)
		}
	}
}

func TestExpiredTokenRefreshedOnce(t *testing.T) {
	f, c := newFake(t)
	f.handlers["/2/files/delete_v2"] = func(w http.ResponseWriter, _ *http.Request, n int) {
		if n == 1 {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error_summary":"expired_access_token/.."}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}
	if err := c.Delete(context.Background(), "/x"); err != nil {
		t.Fatal(err)
	}
	if f.count("/oauth2/token") != 2 || f.count("/2/files/delete_v2") != 2 {
		t.Errorf("token=%d delete=%d", f.count("/oauth2/token"), f.count("/2/files/delete_v2"))
	}
	// a persistent 401 is retried only once
	f2, c2 := newFake(t)
	f2.handlers["/2/files/delete_v2"] = func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error_summary":"invalid_access_token/.."}`))
	}
	if err := c2.Delete(context.Background(), "/x"); err == nil {
		t.Fatal("expected error")
	}
	if f2.count("/2/files/delete_v2") != 2 {
		t.Errorf("delete calls = %d", f2.count("/2/files/delete_v2"))
	}
}

func TestMoveRequest(t *testing.T) {
	f, c := newFake(t)
	f.handlers["/2/files/move_v2"] = okJSON(`{"metadata":{}}`)
	if err := c.Move(context.Background(), "/Kobo/old é.epub", "/Archive/old é.epub", true); err != nil {
		t.Fatal(err)
	}
	m := f.find("/2/files/move_v2")
	var body map[string]any
	if err := json.Unmarshal(m.Body, &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"from_path": "/Kobo/old é.epub", "to_path": "/Archive/old é.epub", "autorename": true, "allow_ownership_transfer": false}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("%s = %v, want %v", k, body[k], v)
		}
	}
	if err := c.Move(context.Background(), "/a", "/b", false); err != nil {
		t.Fatal(err)
	}
}

func TestMoveNotFound(t *testing.T) {
	for _, summary := range []string{"from_lookup/not_found/..", "path_lookup/not_found/.."} {
		f, c := newFake(t)
		f.handlers["/2/files/move_v2"] = func(w http.ResponseWriter, _ *http.Request, _ int) {
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"error_summary":"` + summary + `"}`))
		}
		if err := c.Move(context.Background(), "/a", "/b", true); !errors.Is(err, ErrNotFound) {
			t.Errorf("Move %s: %v", summary, err)
		}
	}
}

func TestMoveOtherConflictIsPlainError(t *testing.T) {
	f, c := newFake(t)
	f.handlers["/2/files/move_v2"] = func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.WriteHeader(409)
		_, _ = w.Write([]byte(`{"error_summary":"to/conflict/file/.."}`))
	}
	err := c.Move(context.Background(), "/a", "/b", false)
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}
