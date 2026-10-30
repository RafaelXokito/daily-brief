package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testCtx is a context that ends the test after 30 seconds at most.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestNewer(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"v0.1.0", "v0.1.1", true},
		{"v0.1.0", "v0.2.0", true},
		{"v0.1.9", "v0.1.10", true}, // numeric, not lexical
		{"v0.9.0", "v0.10.0", true},
		{"v1.9.9", "v2.0.0", true},
		{"v0.1.1", "v0.1.1", false},
		{"v0.1.1", "v0.1.0", false},
		{"v0.10.0", "v0.9.0", false},
		{"v2.0.0", "v1.99.99", false},
		{"v0.1.0", "v0.1.1-rc.1", false},
		{"v0.1.0", "0.1.1", false},
		{"v0.1.0", "", false},
		{"(devel)", "v9.9.9", false},
		{"v0.1.0", "v0.1.1\n", false},
		{"v0.1.0", "v99999999999999999999.0.0", false}, // overflows: not a version
	}
	for _, c := range cases {
		if got := Newer(c.cur, c.latest); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.cur, c.latest, got, c.want)
		}
	}
}

func TestShouldCheck(t *testing.T) {
	t.Setenv(NoCheckEnv, "")
	t.Setenv("CI", "x") // restored after the test
	os.Unsetenv("CI")
	if !ShouldCheck("v0.1.0", true) {
		t.Error("a release with the check on is skipped")
	}
	if ShouldCheck("v0.1.0", false) {
		t.Error("update_check: false checks")
	}
	for _, v := range []string{"(devel)", "v0.1.1-0.20261030120000-abcdef123456", "v0.1.0+dirty", ""} {
		if ShouldCheck(v, true) {
			t.Errorf("non-release %q checks", v)
		}
	}
	t.Setenv(NoCheckEnv, "1")
	if ShouldCheck("v0.1.0", true) {
		t.Errorf("%s set checks", NoCheckEnv)
	}
	t.Setenv(NoCheckEnv, "")
	t.Setenv("CI", "")
	if ShouldCheck("v0.1.0", true) {
		t.Error("CI set (even empty) checks")
	}
	// Check itself honours them, without a network call (the default
	// client would reach api.github.com).
	t.Setenv("CI", "true")
	if got := Check(testCtx(t), "v0.1.0"); got != "" {
		t.Errorf("Check under CI = %q", got)
	}
}

// fakeGitHub is an HTTPS server standing in for the API and the release
// downloads.
type fakeGitHub struct {
	srv       *httptest.Server
	mu        sync.Mutex
	release   any // the JSON /releases/latest returns
	status    int // its status, 200 if 0
	files     map[string][]byte
	apiHits   atomic.Int32
	downloads atomic.Int32
	headers   http.Header // the last API request's
}

func newFake(t *testing.T) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{files: map[string][]byte{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+Owner+"/"+Repo+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		f.apiHits.Add(1)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.headers = r.Header.Clone()
		if f.status != 0 && f.status != 200 {
			w.WriteHeader(f.status)
			return
		}
		json.NewEncoder(w).Encode(f.release)
	})
	// Release assets are served under the release path, as on github.com,
	// and redirect on ?to=; /dl/ stands in for the redirect target.
	serve := func(w http.ResponseWriter, r *http.Request) {
		if to := r.URL.Query().Get("to"); to != "" {
			http.Redirect(w, r, to, http.StatusFound)
			return
		}
		f.downloads.Add(1)
		f.mu.Lock()
		data, ok := f.files[path.Base(r.URL.Path)]
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	}
	mux.HandleFunc("/dl/", serve)
	mux.HandleFunc("/"+Owner+"/"+Repo+"/releases/download/", serve)
	f.srv = httptest.NewTLSServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGitHub) client(t *testing.T, now time.Time) *client {
	u, _ := url.Parse(f.srv.URL)
	return &client{api: f.srv.URL, hosts: []string{u.Host}, http: f.srv.Client(), cache: t.TempDir(),
		now: func() time.Time { return now }, goos: "linux", goarch: "amd64"}
}

// setAsset changes the archive asset's (index 0) key.
func (f *fakeGitHub) setAsset(key string, v any) {
	f.mu.Lock()
	f.release.(map[string]any)["assets"].([]map[string]any)[0][key] = v
	f.mu.Unlock()
}

func (f *fakeGitHub) set(release any) {
	f.mu.Lock()
	f.release = release
	f.mu.Unlock()
}

// publish makes tag the latest release, with an archive for linux/amd64
// holding entries and a checksums.txt (sums, or the archive's real sum if
// sums is "").
func (f *fakeGitHub) publish(t *testing.T, tag string, archive []byte, sums string) string {
	t.Helper()
	name := fmt.Sprintf("daily-brief_%s_linux_amd64.tar.gz", strings.TrimPrefix(tag, "v"))
	if sums == "" {
		sum := sha256.Sum256(archive)
		sums = hex.EncodeToString(sum[:]) + "  " + name + "\n" +
			strings.Repeat("0", 64) + "  daily-brief_x_darwin_arm64.tar.gz\n"
	}
	f.mu.Lock()
	f.files[name] = archive
	f.files["checksums.txt"] = []byte(sums)
	f.mu.Unlock()
	f.set(map[string]any{"tag_name": tag, "draft": false, "prerelease": false, "body": "notes \x1b[31m",
		"assets": []map[string]any{
			{"name": name, "browser_download_url": f.srv.URL + releasePath(tag) + name, "size": len(archive)},
			{"name": "checksums.txt", "browser_download_url": f.srv.URL + releasePath(tag) + "checksums.txt", "size": len(sums)},
		}})
	return name
}

type entry struct {
	name string
	typ  byte
	body string
	link string
	size int64 // header size if not len(body)
}

func tgz(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: 0o755, Size: int64(len(e.body)), Linkname: e.link}
		if e.typ == 0 {
			h.Typeflag = tar.TypeReg
		}
		if h.Typeflag != tar.TypeReg {
			h.Size = 0
		}
		if e.size != 0 {
			h.Size = e.size
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil && e.size == 0 {
				t.Fatal(err)
			}
		}
	}
	tw.Flush()
	gz.Close()
	return b.Bytes()
}

func goodArchive(t *testing.T) []byte {
	return tgz(t, entry{name: "LICENSE", body: "MIT"}, entry{name: "daily-brief", body: "#!new binary"}, entry{name: "README.md", body: "readme"})
}

// fakeExe is a temporary "executable" holding old contents.
func fakeExe(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "daily-brief")
	if err := os.WriteFile(exe, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

func assertContents(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("%s holds %q, want %q", path, got, want)
	}
}

// assertNoTemp fails if anything but the listed files is in dir.
func assertOnly(t *testing.T, dir string, names ...string) {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range ents {
		got = append(got, e.Name())
	}
	if strings.Join(got, ",") != strings.Join(names, ",") {
		t.Errorf("%s holds %v, want %v (a temporary file left over?)", dir, got, names)
	}
}

func TestLatestHeadersAndValidation(t *testing.T) {
	f := newFake(t)
	c := f.client(t, time.Now())
	f.publish(t, "v0.2.0", goodArchive(t), "")
	r, err := c.latest(testCtx(t), "v0.1.0")
	if err != nil || r.Tag != "v0.2.0" || len(r.Assets) != 2 {
		t.Fatalf("latest = %+v, %v", r, err)
	}
	if got := f.headers.Get("Accept"); got != "application/vnd.github+json" {
		t.Errorf("Accept %q", got)
	}
	if got := f.headers.Get("User-Agent"); got != "daily-brief/v0.1.0" {
		t.Errorf("User-Agent %q", got)
	}
	for _, rel := range []map[string]any{
		{"tag_name": "v0.3.0", "draft": true},
		{"tag_name": "v0.3.0", "prerelease": true},
		{"tag_name": "v0.3.0-rc.1"},
		{"tag_name": "v0.3.0\x1b[2J"},
		{"tag_name": "0.3.0"},
	} {
		f.set(rel)
		if r, err := c.latest(testCtx(t), "v0.1.0"); err == nil {
			t.Errorf("%v accepted as %+v", rel, r)
		}
	}
	// Not HTTPS: refused before any request.
	c.api = strings.Replace(f.srv.URL, "https://", "http://", 1)
	if _, err := c.latest(testCtx(t), "v0.1.0"); err == nil {
		t.Error("an http API base was used")
	}
}

func readCacheFile(t *testing.T, c *client) cacheEntry {
	t.Helper()
	data, err := os.ReadFile(c.cachePath())
	if err != nil {
		t.Fatal(err)
	}
	var e cacheEntry
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestCheckCaches(t *testing.T) {
	f := newFake(t)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	c := f.client(t, now)
	c.now = func() time.Time { return now }
	f.publish(t, "v0.1.1", goodArchive(t), "")

	if got := c.check(testCtx(t), "v0.1.0"); got != "v0.1.1" {
		t.Fatalf("check = %q, want v0.1.1", got)
	}
	if e := readCacheFile(t, c); e.Latest != "v0.1.1" || !e.CheckedAt.Equal(now) {
		t.Errorf("cache %+v", e)
	}
	// Within the window the cache answers, without a request, even
	// after a newer release.
	f.publish(t, "v0.1.2", goodArchive(t), "")
	now = now.Add(23 * time.Hour)
	if got := c.check(testCtx(t), "v0.1.0"); got != "v0.1.1" || f.apiHits.Load() != 1 {
		t.Errorf("fresh cache: %q after %d requests", got, f.apiHits.Load())
	}
	if got := c.check(testCtx(t), "v0.1.1"); got != "" {
		t.Errorf("the current version reported as newer: %q", got)
	}
	// After 24h it asks again.
	now = now.Add(time.Hour)
	if got := c.check(testCtx(t), "v0.1.0"); got != "v0.1.2" || f.apiHits.Load() != 2 {
		t.Errorf("stale cache: %q after %d requests", got, f.apiHits.Load())
	}
	// A cache dated in the future is stale.
	c.writeCache(cacheEntry{CheckedAt: now.Add(48 * time.Hour), Latest: "v0.1.1"})
	if got := c.check(testCtx(t), "v0.1.0"); got != "v0.1.2" || f.apiHits.Load() != 3 {
		t.Errorf("future cache: %q after %d requests", got, f.apiHits.Load())
	}
	// A tampered cache's latest is ignored.
	now = now.Add(time.Minute)
	os.WriteFile(c.cachePath(), []byte(`{"checked_at":"`+now.Format(time.RFC3339)+`","latest":"v9.9.9\u001b[2J"}`), 0o644)
	if got := c.check(testCtx(t), "v0.1.0"); got != "" || f.apiHits.Load() != 3 {
		t.Errorf("tampered cache: %q after %d requests", got, f.apiHits.Load())
	}
}

func TestCheckCachesFailures(t *testing.T) {
	f := newFake(t)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	c := f.client(t, now)
	c.now = func() time.Time { return now }
	f.mu.Lock()
	f.status = http.StatusInternalServerError
	f.mu.Unlock()
	if got := c.check(testCtx(t), "v0.1.0"); got != "" {
		t.Errorf("a failure reported %q", got)
	}
	if e := readCacheFile(t, c); e.Latest != "" || !e.CheckedAt.Equal(now) {
		t.Errorf("failure cached as %+v", e)
	}
	// Not retried within the window.
	f.mu.Lock()
	f.status = 0
	f.mu.Unlock()
	f.publish(t, "v0.1.1", goodArchive(t), "")
	now = now.Add(12 * time.Hour)
	if got := c.check(testCtx(t), "v0.1.0"); got != "" || f.apiHits.Load() != 1 {
		t.Errorf("failure retried: %q after %d requests", got, f.apiHits.Load())
	}
	now = now.Add(12 * time.Hour)
	if got := c.check(testCtx(t), "v0.1.0"); got != "v0.1.1" {
		t.Errorf("after the window: %q", got)
	}
	// A later failure keeps the known latest release.
	now = now.Add(25 * time.Hour)
	f.mu.Lock()
	f.status = http.StatusForbidden
	f.mu.Unlock()
	if got := c.check(testCtx(t), "v0.1.0"); got != "v0.1.1" || f.apiHits.Load() != 3 {
		t.Errorf("failure after success: %q after %d requests", got, f.apiHits.Load())
	}

	// An unreachable server fails quietly too.
	f2 := newFake(t)
	c2 := f2.client(t, now)
	f2.srv.Close()
	if got := c2.check(testCtx(t), "v0.1.0"); got != "" {
		t.Errorf("unreachable: %q", got)
	}
	if e := readCacheFile(t, c2); !e.CheckedAt.Equal(now) {
		t.Errorf("unreachable not cached: %+v", e)
	}
}

func TestCheckIgnoresDraftPrereleaseAndBadTags(t *testing.T) {
	for _, rel := range []map[string]any{
		{"tag_name": "v0.2.0", "draft": true},
		{"tag_name": "v0.2.0", "prerelease": true},
		{"tag_name": "v0.2.0-beta"},
		{"tag_name": "v0.2"},
		{"tag_name": "v0.2.0\n"},
	} {
		f := newFake(t)
		c := f.client(t, time.Now())
		f.set(rel)
		if got := c.check(testCtx(t), "v0.1.0"); got != "" {
			t.Errorf("%v: check = %q", rel, got)
		}
	}
}

func TestApplyHappyPath(t *testing.T) {
	f := newFake(t)
	c := f.client(t, time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC))
	f.publish(t, "v0.1.1", goodArchive(t), "")
	exe := fakeExe(t)
	var out bytes.Buffer
	got, err := c.apply(testCtx(t), "v0.1.0", exe, &out)
	if err != nil || got != "v0.1.1" {
		t.Fatalf("apply = %q, %v\n%s", got, err, out.String())
	}
	assertContents(t, exe, "#!new binary")
	info, err := os.Stat(exe)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("mode %v, %v", info.Mode(), err)
	}
	assertOnly(t, filepath.Dir(exe), "daily-brief")
	if want := "Checking for updates…\nDownloading v0.1.1 (0.0 MB)… verified… installed\n"; out.String() != want {
		t.Errorf("progress %q, want %q", out.String(), want)
	}
	if e := readCacheFile(t, c); e.Latest != "v0.1.1" {
		t.Errorf("cache after update %+v", e)
	}
}

func TestApplyNotNewerDownloadsNothing(t *testing.T) {
	for _, tag := range []string{"v0.1.0", "v0.0.9"} {
		f := newFake(t)
		c := f.client(t, time.Now())
		f.publish(t, tag, goodArchive(t), "")
		exe := fakeExe(t)
		var out bytes.Buffer
		got, err := c.apply(testCtx(t), "v0.1.0", exe, &out)
		if err != nil || got != "" {
			t.Errorf("%s: apply = %q, %v", tag, got, err)
		}
		if !strings.Contains(out.String(), "already up to date (v0.1.0)") {
			t.Errorf("%s: progress %q", tag, out.String())
		}
		if n := f.downloads.Load(); n != 0 {
			t.Errorf("%s: %d downloads", tag, n)
		}
		assertContents(t, exe, "old binary")
		assertOnly(t, filepath.Dir(exe), "daily-brief")
	}
	// A dev build is refused before any request.
	f := newFake(t)
	if _, err := f.client(t, time.Now()).apply(testCtx(t), "(devel)", fakeExe(t), &bytes.Buffer{}); err == nil || f.apiHits.Load() != 0 {
		t.Errorf("dev build: %v after %d requests", err, f.apiHits.Load())
	}
}

// Each broken release leaves the old binary as it was and no temporary
// file behind.
func TestApplyRefusals(t *testing.T) {
	big := entry{name: "daily-brief", size: maxBinary + 1, body: "x"}
	cases := []struct {
		name    string
		archive func(t *testing.T) []byte
		sums    string // "" = the right checksum
		want    string
	}{
		{"checksum mismatch", goodArchive, strings.Repeat("ab", 32) + "  daily-brief_0.1.1_linux_amd64.tar.gz\n", "does not match"},
		{"missing checksum line", goodArchive, strings.Repeat("ab", 32) + "  daily-brief_0.1.1_darwin_arm64.tar.gz\n", "no checksum"},
		{"malformed checksum", goodArchive, "xyz  daily-brief_0.1.1_linux_amd64.tar.gz\n", "malformed"},
		{"no daily-brief entry", func(t *testing.T) []byte {
			return tgz(t, entry{name: "LICENSE", body: "MIT"}, entry{name: "bin/daily-brief", body: "nested"})
		}, "", "has no daily-brief"},
		{"path traversal", func(t *testing.T) []byte {
			return tgz(t, entry{name: "../daily-brief", body: "evil"}, entry{name: "daily-brief", body: "ok"})
		}, "", "unsafe path"},
		{"absolute path", func(t *testing.T) []byte {
			return tgz(t, entry{name: "daily-brief", body: "ok"}, entry{name: "/etc/daily-brief", body: "evil"})
		}, "", "unsafe path"},
		{"nested traversal", func(t *testing.T) []byte {
			return tgz(t, entry{name: "x/../../README.md", body: "evil"}, entry{name: "daily-brief", body: "ok"})
		}, "", "unsafe path"},
		{"dot slash", func(t *testing.T) []byte {
			return tgz(t, entry{name: "./daily-brief", body: "evil"})
		}, "", "refusing"},
		{"symlink", func(t *testing.T) []byte {
			return tgz(t, entry{name: "daily-brief", typ: tar.TypeSymlink, link: "/bin/sh"})
		}, "", "not a regular file"},
		{"hard link", func(t *testing.T) []byte {
			return tgz(t, entry{name: "LICENSE", body: "MIT"}, entry{name: "daily-brief", typ: tar.TypeLink, link: "LICENSE"})
		}, "", "not a regular file"},
		{"directory", func(t *testing.T) []byte {
			return tgz(t, entry{name: "daily-brief", typ: tar.TypeDir})
		}, "", "not a regular file"},
		{"twice", func(t *testing.T) []byte {
			return tgz(t, entry{name: "daily-brief", body: "a"}, entry{name: "daily-brief", body: "b"})
		}, "", "twice"},
		{"oversize", func(t *testing.T) []byte { return tgz(t, big) }, "", "unexpected size"},
		{"empty", func(t *testing.T) []byte { return tgz(t, entry{name: "daily-brief"}) }, "", "unexpected size"},
		{"not gzip", func(*testing.T) []byte { return []byte("not an archive") }, "", "reading the archive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t)
			c := f.client(t, time.Now())
			f.publish(t, "v0.1.1", tc.archive(t), tc.sums)
			exe := fakeExe(t)
			got, err := c.apply(testCtx(t), "v0.1.0", exe, &bytes.Buffer{})
			if err == nil || got != "" || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("apply = %q, %v; want an error with %q", got, err, tc.want)
			}
			assertContents(t, exe, "old binary")
			assertOnly(t, filepath.Dir(exe), "daily-brief")
		})
	}
}

func TestApplyNoAssetForPlatform(t *testing.T) {
	f := newFake(t)
	c := f.client(t, time.Now())
	c.goos, c.goarch = "plan9", "mips"
	f.publish(t, "v0.1.1", goodArchive(t), "")
	exe := fakeExe(t)
	_, err := c.apply(testCtx(t), "v0.1.0", exe, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "no build for plan9/mips") {
		t.Errorf("err %v", err)
	}
	if f.downloads.Load() != 0 {
		t.Error("downloaded without a matching asset")
	}
	assertOnly(t, filepath.Dir(exe), "daily-brief")
}

func TestApplyUnwritableDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can write anywhere")
	}
	f := newFake(t)
	c := f.client(t, time.Now())
	f.publish(t, "v0.1.1", goodArchive(t), "")
	exe := fakeExe(t)
	dir := filepath.Dir(exe)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	_, err := c.apply(testCtx(t), "v0.1.0", exe, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "try: sudo daily-brief update, or reinstall with go install") {
		t.Errorf("err %v", err)
	}
	if f.downloads.Load() != 0 {
		t.Error("downloaded before finding the directory unwritable")
	}
	assertContents(t, exe, "old binary")
	assertOnly(t, dir, "daily-brief")
}

func TestApplySymlinkedExe(t *testing.T) {
	f := newFake(t)
	c := f.client(t, time.Now())
	f.publish(t, "v0.1.1", goodArchive(t), "")
	exe := fakeExe(t)
	linkDir := t.TempDir()
	link := filepath.Join(linkDir, "daily-brief")
	if err := os.Symlink(exe, link); err != nil {
		t.Fatal(err)
	}
	if _, err := c.apply(testCtx(t), "v0.1.0", link, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	assertContents(t, exe, "#!new binary")
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the link was replaced: %v %v", info.Mode(), err)
	}
	if dest, _ := os.Readlink(link); dest != exe {
		t.Errorf("the link points to %q", dest)
	}
	assertOnly(t, filepath.Dir(exe), "daily-brief")
	assertOnly(t, linkDir, "daily-brief")
}

// Downloads go only to HTTPS on the allowed hosts, redirects included.
func TestApplyDownloadHosts(t *testing.T) {
	f := newFake(t)
	c := f.client(t, time.Now())
	name := f.publish(t, "v0.1.1", goodArchive(t), "")
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, f.srv.URL+"/dl/"+name, http.StatusFound)
	}))
	t.Cleanup(other.Close)
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(plain.Close)
	redirect := func(to string) string {
		return f.srv.URL + releasePath("v0.1.1") + name + "?to=" + url.QueryEscape(to)
	}
	for _, u := range []string{
		f.srv.URL + "/dl/" + name,                       // an allowed host, but not the release path
		f.srv.URL + releasePath("v0.1.0") + name,        // another release's path
		f.srv.URL + releasePath("v0.1.1") + "x/" + name, // below it
		f.srv.URL + "/" + Owner + "/other/releases/download/v0.1.1/" + name,
		f.srv.URL + releasePath("v0.1.1") + "..%2F..%2Fdl%2F" + name, // an escaped path
		other.URL + "/dl/" + name,                                    // a host not allowed
		plain.URL + "/dl/" + name,                                    // http
		redirect(plain.URL + "/x"),
		redirect(other.URL + "/x"),
		redirect(strings.Replace(f.srv.URL, "https", "http", 1) + "/dl/" + name),
	} {
		f.setAsset("browser_download_url", u)
		exe := fakeExe(t)
		if _, err := c.apply(testCtx(t), "v0.1.0", exe, &bytes.Buffer{}); err == nil {
			t.Errorf("downloaded from %s", u)
		}
		assertContents(t, exe, "old binary")
		assertOnly(t, filepath.Dir(exe), "daily-brief")
	}
	// A redirect within the allowed hosts is followed.
	f.setAsset("browser_download_url", redirect(f.srv.URL+"/dl/"+name))
	exe := fakeExe(t)
	if _, err := c.apply(testCtx(t), "v0.1.0", exe, &bytes.Buffer{}); err != nil {
		t.Errorf("allowed redirect: %v", err)
	}
	assertContents(t, exe, "#!new binary")
}

func TestApplyOversizeDownload(t *testing.T) {
	f := newFake(t)
	c := f.client(t, time.Now())
	f.publish(t, "v0.1.1", goodArchive(t), "")
	f.setAsset("size", maxArchive+1)
	exe := fakeExe(t)
	if _, err := c.apply(testCtx(t), "v0.1.0", exe, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("err %v", err)
	}
	if f.downloads.Load() != 0 {
		t.Error("downloaded an archive declared too large")
	}
	// A checksums.txt over its cap.
	f.mu.Lock()
	f.files["checksums.txt"] = bytes.Repeat([]byte("a"), maxChecksums+1)
	f.mu.Unlock()
	f.setAsset("size", 10)
	if _, err := c.apply(testCtx(t), "v0.1.0", exe, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("checksums err %v", err)
	}
	assertContents(t, exe, "old binary")
	assertOnly(t, filepath.Dir(exe), "daily-brief")
}

func TestChecksumFor(t *testing.T) {
	sum := strings.Repeat("0f", 32)
	data := []byte(sum + "  other.tar.gz\n" + sum + " *daily-brief_1_linux_amd64.tar.gz\n")
	if got, err := checksumFor(data, "daily-brief_1_linux_amd64.tar.gz"); err != nil || hex.EncodeToString(got) != sum {
		t.Errorf("binary-mode line: %x, %v", got, err)
	}
	// A name that only contains it is not it.
	if _, err := checksumFor([]byte(sum+"  xdaily-brief_1_linux_amd64.tar.gz\n"), "daily-brief_1_linux_amd64.tar.gz"); err == nil {
		t.Error("a different name matched")
	}
	if _, err := checksumFor([]byte(sum+"  a\n"+strings.Repeat("1f", 32)+"  a\n"), "a"); err == nil {
		t.Error("conflicting lines accepted")
	}
}

// The new binary keeps the old one's permission bits, but never setuid,
// setgid or sticky.
func TestApplyKeepsPermissions(t *testing.T) {
	for _, tc := range []struct {
		old, want os.FileMode
	}{
		{0o700, 0o700},
		{0o755 | os.ModeSetuid | os.ModeSetgid, 0o755},
		{0o750, 0o750},
	} {
		f := newFake(t)
		c := f.client(t, time.Now())
		f.publish(t, "v0.1.1", goodArchive(t), "")
		exe := fakeExe(t)
		if err := os.Chmod(exe, tc.old); err != nil {
			t.Fatal(err)
		}
		if _, err := c.apply(testCtx(t), "v0.1.0", exe, &bytes.Buffer{}); err != nil {
			t.Fatalf("%v: %v", tc.old, err)
		}
		info, err := os.Stat(exe)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode() & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky); got != tc.want {
			t.Errorf("old %v: new mode %v, want %v", tc.old, got, tc.want)
		}
		assertContents(t, exe, "#!new binary")
	}
}

// Run as root through sudo, an update and a check leave no cache file.
func TestNoCacheUnderSudo(t *testing.T) {
	f := newFake(t)
	c := f.client(t, time.Now())
	c.euid = func() int { return 0 }
	t.Setenv("SUDO_USER", "someone")
	f.publish(t, "v0.1.1", goodArchive(t), "")
	if got := c.check(testCtx(t), "v0.1.0"); got != "v0.1.1" {
		t.Errorf("check under sudo = %q", got)
	}
	exe := fakeExe(t)
	if _, err := c.apply(testCtx(t), "v0.1.0", exe, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	assertContents(t, exe, "#!new binary")
	if _, err := os.Stat(c.cachePath()); !os.IsNotExist(err) {
		t.Errorf("a cache was written under sudo: %v", err)
	}
	// Root without sudo, or sudo's variable without root, writes it.
	t.Setenv("SUDO_USER", "")
	c.writeCache(cacheEntry{CheckedAt: time.Now(), Latest: "v0.1.1"})
	if _, err := os.Stat(c.cachePath()); err != nil {
		t.Errorf("no cache as plain root: %v", err)
	}
}

// A check its caller cancels is not cached as a failure.
func TestCheckCanceledNotCached(t *testing.T) {
	f := newFake(t)
	c := f.client(t, time.Now())
	f.publish(t, "v0.1.1", goodArchive(t), "")
	ctx, cancel := context.WithCancel(testCtx(t))
	cancel()
	if got := c.check(ctx, "v0.1.0"); got != "" {
		t.Errorf("canceled check = %q", got)
	}
	if _, err := os.Stat(c.cachePath()); !os.IsNotExist(err) {
		t.Errorf("a canceled check was cached: %v", err)
	}
}

// A cache write that can't be renamed into place leaves no temporary
// file.
func TestWriteCacheCleansUp(t *testing.T) {
	c := &client{cache: t.TempDir()}
	if err := os.MkdirAll(filepath.Join(c.cachePath(), "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	c.writeCache(cacheEntry{CheckedAt: time.Now(), Latest: "v0.1.1"})
	assertOnly(t, c.cache, "update-check.json")
}
