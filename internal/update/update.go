// Package update finds daily-brief's latest release on GitHub and installs
// it over the running binary. A check asks the GitHub API at most once a
// day and caches the answer; an update downloads the release archive for
// this platform, verifies it against the release's checksums.txt, and
// renames the binary it holds over the old one. Nothing downloaded is run.
package update

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/RafaelXokito/daily-brief/internal/brief"
)

// Where releases come from.
const (
	Owner   = "RafaelXokito"
	Repo    = "daily-brief"
	APIBase = "https://api.github.com"
	// Binary is the archive entry that holds the program.
	Binary = "daily-brief"
	// Checksums is the release asset listing each archive's SHA-256.
	Checksums = "checksums.txt"
)

// Limits on what a check or an update reads.
const (
	CheckTimeout    = 3 * time.Second
	latestTimeout   = 15 * time.Second // the latest release, when updating
	downloadTimeout = 2 * time.Minute
	CheckInterval   = 24 * time.Hour

	maxAPIBody      = 4 << 20   // the release JSON, notes included
	maxArchive      = 100 << 20 // the tar.gz
	maxChecksums    = 64 << 10  // checksums.txt
	maxBinary       = 100 << 20 // the daily-brief entry
	maxUncompressed = 300 << 20 // all the archive's entries together
	maxRedirects    = 10
)

// NoCheckEnv turns the check off when set to anything but "".
const NoCheckEnv = "DAILY_BRIEF_NO_UPDATE_CHECK"

// downloadHosts are the hosts release assets may be fetched from, over
// HTTPS: the download URL's and those GitHub redirects it to.
var downloadHosts = []string{"github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com"}

// ReinstallHint is how to update when the binary's directory can't be
// written.
const ReinstallHint = "try: sudo daily-brief update, or reinstall with go install github.com/RafaelXokito/daily-brief@latest"

var releaseRE = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

// IsRelease reports whether v is a plain release tag like v1.2.3, not a
// pre-release, pseudo-version, dirty build or "(devel)".
func IsRelease(v string) bool { return releaseRE.MatchString(v) }

// parse is v's major, minor and patch numbers, if it is a release tag.
func parse(v string) ([3]uint64, bool) {
	var n [3]uint64
	sub := releaseRE.FindStringSubmatch(v)
	if sub == nil {
		return n, false
	}
	for i := range n {
		x, err := strconv.ParseUint(sub[i+1], 10, 64)
		if err != nil {
			return n, false
		}
		n[i] = x
	}
	return n, true
}

// Newer reports whether latest is a release strictly newer than cur,
// comparing major, minor and patch as numbers. Anything that is not a
// release tag is never newer, nor older.
func Newer(cur, latest string) bool {
	c, ok1 := parse(cur)
	l, ok2 := parse(latest)
	if !ok1 || !ok2 {
		return false
	}
	return slices.Compare(l[:], c[:]) > 0
}

// ShouldCheck reports whether a check may run: cur is a release, the
// config's update_check is on, and neither DAILY_BRIEF_NO_UPDATE_CHECK
// nor CI is set.
func ShouldCheck(cur string, configOn bool) bool {
	if !configOn || !IsRelease(cur) || os.Getenv(NoCheckEnv) != "" {
		return false
	}
	_, ci := os.LookupEnv("CI")
	return !ci
}

// Release is a published release.
type Release struct {
	Tag    string  `json:"tag_name"`
	Draft  bool    `json:"draft"`
	Pre    bool    `json:"prerelease"`
	Assets []Asset `json:"assets"`
}

// Asset is a file attached to a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// client is where releases are looked up and installed from. Only tests
// build one other than defaultClient's.
type client struct {
	api    string       // the API's base URL
	hosts  []string     // the hosts (host[:port]) assets may come from
	http   *http.Client // its CheckRedirect is replaced
	cache  string       // the check cache's directory; "" = brief.DataDir()
	now    func() time.Time
	goos   string
	goarch string
	euid   func() int // os.Geteuid; -1 on Windows
}

func defaultClient() *client {
	return &client{api: APIBase, hosts: downloadHosts, http: &http.Client{}, now: time.Now, goos: runtime.GOOS, goarch: runtime.GOARCH,
		euid: os.Geteuid}
}

// Latest is the latest release, which must be published (not a draft or
// pre-release) and tagged vX.Y.Z. version is this binary's, for the
// User-Agent.
func Latest(ctx context.Context, version string) (Release, error) {
	ctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	return defaultClient().latest(ctx, version)
}

func (c *client) latest(ctx context.Context, version string) (Release, error) {
	u := strings.TrimSuffix(c.api, "/") + "/repos/" + Owner + "/" + Repo + "/releases/latest"
	if !strings.HasPrefix(u, "https://") {
		return Release{}, errors.New("update: the GitHub API must be reached over HTTPS")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", userAgent(version))
	hc := *c.http
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := hc.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("update: checking for the latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("update: checking for the latest release: GitHub answered %s", resp.Status)
	}
	data, err := readCapped(resp.Body, maxAPIBody, "the release description")
	if err != nil {
		return Release{}, err
	}
	var r Release
	if err := json.Unmarshal(data, &r); err != nil {
		return Release{}, fmt.Errorf("update: reading the latest release: %w", err)
	}
	switch {
	case r.Draft || r.Pre:
		return Release{}, errors.New("update: the latest release is a draft or pre-release")
	case !IsRelease(r.Tag):
		return Release{}, fmt.Errorf("update: the latest release has an unexpected tag %q", brief.StripControl(r.Tag))
	}
	return r, nil
}

func userAgent(version string) string {
	v := brief.StripControl(version)
	if v == "" || strings.ContainsAny(v, " \t\n") {
		v = "dev"
	}
	return "daily-brief/" + v
}

// cacheEntry is the check cache, update-check.json in the data directory.
type cacheEntry struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
}

func (c *client) cachePath() string {
	dir := c.cache
	if dir == "" {
		dir = brief.DataDir()
	}
	return filepath.Join(dir, "update-check.json")
}

func (c *client) readCache() (cacheEntry, bool) {
	data, err := os.ReadFile(c.cachePath())
	if err != nil || len(data) > 4096 {
		return cacheEntry{}, false
	}
	var e cacheEntry
	if json.Unmarshal(data, &e) != nil {
		return cacheEntry{}, false
	}
	if !IsRelease(e.Latest) {
		e.Latest = ""
	}
	return e, true
}

// underSudo reports whether this is root run through sudo, whose cache
// file would be root's in the user's data directory.
func (c *client) underSudo() bool {
	return c.euid != nil && c.euid() == 0 && os.Getenv("SUDO_USER") != ""
}

// writeCache saves e, replacing the file atomically; errors are ignored,
// as the check is best effort. Under sudo nothing is written. A failed
// write leaves no temporary file.
func (c *client) writeCache(e cacheEntry) {
	if c.underSudo() {
		return
	}
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	p := c.cachePath()
	if os.MkdirAll(filepath.Dir(p), 0o755) != nil {
		return
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".update-check.*.tmp")
	if err != nil {
		return
	}
	_, werr := f.Write(data)
	if cerr := f.Close(); werr != nil || cerr != nil || os.Rename(f.Name(), p) != nil {
		os.Remove(f.Name())
	}
}

// fresh reports whether e was written within the check interval (and
// not in the future, which a clock change could leave).
func (c *client) fresh(e cacheEntry) bool {
	age := c.now().Sub(e.CheckedAt)
	return age >= -time.Hour && age < CheckInterval
}

// Check returns the latest release's tag if it is newer than cur, else
// "". It asks GitHub at most once per CheckInterval, caching the answer in
// the data directory; a failure is cached too, so it is not retried within
// the interval, and is silent. It never runs (returning "") unless
// ShouldCheck(cur, true): the caller checks the config's update_check.
func Check(ctx context.Context, cur string) string {
	if !ShouldCheck(cur, true) {
		return ""
	}
	return defaultClient().check(ctx, cur)
}

func (c *client) check(ctx context.Context, cur string) string {
	e, ok := c.readCache()
	if !ok || !c.fresh(e) {
		ctx, cancel := context.WithTimeout(ctx, CheckTimeout)
		defer cancel()
		r, err := c.latest(ctx, cur)
		if ctx.Err() != nil && errors.Is(context.Cause(ctx), context.Canceled) {
			// The caller gave up (the TUI quit): not a failure to cache.
			return ""
		}
		if err == nil {
			e.Latest = r.Tag
		}
		// A failure keeps the last known latest release, and waits out
		// the interval before asking again.
		e.CheckedAt = c.now().UTC()
		c.writeCache(e)
	}
	if Newer(cur, e.Latest) {
		return e.Latest
	}
	return ""
}

// Apply installs the latest release over exe, the running binary (its
// symlinks followed), if it is newer than cur, writing progress to
// progress. It returns the version installed, or "" with no error when cur
// is already the latest. The new binary is written to a temporary file
// beside the old one and renamed over it, so the old file (and, on macOS,
// its code-signed inode) is never written to.
func Apply(ctx context.Context, cur, exe string, progress io.Writer) (string, error) {
	return defaultClient().apply(ctx, cur, exe, progress)
}

func (c *client) apply(ctx context.Context, cur, exe string, progress io.Writer) (string, error) {
	if !IsRelease(cur) {
		return "", fmt.Errorf("update: %q is not a release version", brief.StripControl(cur))
	}
	fmt.Fprintln(progress, "Checking for updates…")
	lctx, cancel := context.WithTimeout(ctx, latestTimeout)
	r, err := c.latest(lctx, cur)
	cancel()
	if err != nil {
		return "", err
	}
	if !Newer(cur, r.Tag) {
		fmt.Fprintf(progress, "already up to date (%s)\n", cur)
		c.writeCache(cacheEntry{CheckedAt: c.now().UTC(), Latest: r.Tag})
		return "", nil
	}
	tag := brief.StripControl(r.Tag)

	name := fmt.Sprintf("%s_%s_%s_%s.tar.gz", Binary, strings.TrimPrefix(tag, "v"), c.goos, c.goarch)
	archive, ok := findAsset(r.Assets, name)
	if !ok {
		return "", fmt.Errorf("update: %s has no build for %s/%s (expected %s); %s", tag, c.goos, c.goarch, name,
			"install it with go install github.com/RafaelXokito/daily-brief@latest")
	}
	sums, ok := findAsset(r.Assets, Checksums)
	if !ok {
		return "", fmt.Errorf("update: %s has no %s to verify the download with", tag, Checksums)
	}
	if archive.Size > maxArchive {
		return "", fmt.Errorf("update: %s is too large (%d bytes)", name, archive.Size)
	}

	target, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("update: finding the installed binary: %w", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", fmt.Errorf("update: finding the installed binary: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("update: %s is not a regular file", target)
	}
	dir := filepath.Dir(target)
	// The temporary file is made first, so an unwritable directory fails
	// before anything is downloaded.
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(target)+".update-*")
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return "", fmt.Errorf("update: cannot write to %s (%s)", dir, ReinstallHint)
		}
		return "", fmt.Errorf("update: cannot write to %s: %w (%s)", dir, err, ReinstallHint)
	}
	installed := false
	defer func() {
		if !installed {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()

	dctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	sumData, err := c.download(dctx, sums.URL, tag, maxChecksums, Checksums)
	if err != nil {
		return "", err
	}
	want, err := checksumFor(sumData, name)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(progress, "Downloading %s (%.1f MB)…", tag, float64(max(archive.Size, 0))/1e6)
	data, err := c.download(dctx, archive.URL, tag, maxArchive, name)
	if err != nil {
		fmt.Fprintln(progress)
		return "", err
	}
	if got := sha256.Sum256(data); !bytes.Equal(got[:], want) {
		fmt.Fprintln(progress)
		return "", fmt.Errorf("update: %s does not match its checksum; nothing was installed", name)
	}
	fmt.Fprint(progress, " verified…")
	if err := extract(data, tmp); err != nil {
		fmt.Fprintln(progress)
		return "", err
	}
	if err := finish(tmp, info, c.euid); err != nil {
		fmt.Fprintln(progress)
		return "", fmt.Errorf("update: writing the new binary: %w", err)
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		fmt.Fprintln(progress)
		return "", fmt.Errorf("update: replacing %s: %w (%s)", target, err, ReinstallHint)
	}
	installed = true
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
	fmt.Fprintln(progress, " installed")
	c.writeCache(cacheEntry{CheckedAt: c.now().UTC(), Latest: tag})
	return tag, nil
}

// finish gives the written binary the old one's permission bits (never
// setuid, setgid or sticky) and, when root replaces another user's binary,
// its owner; then makes it durable and closes it.
func finish(f *os.File, old fs.FileInfo, euid func() int) error {
	if euid != nil {
		if uid, gid, ok := ownerToRestore(old, euid()); ok {
			if err := f.Chown(uid, gid); err != nil {
				f.Close()
				return err
			}
		}
	}
	if err := f.Chmod(old.Mode().Perm()); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func findAsset(assets []Asset, name string) (Asset, bool) {
	for _, a := range assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// allowed reports whether u may be fetched: HTTPS to one of the hosts.
func (c *client) allowed(u *url.URL) bool {
	return u.Scheme == "https" && u.User == nil && slices.Contains(c.hosts, u.Host)
}

// releasePath is the path every asset of the release tag is downloaded
// from, before GitHub redirects it.
func releasePath(tag string) string {
	return "/" + Owner + "/" + Repo + "/releases/download/" + tag + "/"
}

// download reads raw, which must be an allowed URL under tag's release
// path, following redirects only to allowed URLs (any path), and refuses
// a body over limit bytes.
func (c *client) download(ctx context.Context, raw, tag string, limit int64, what string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil || !c.allowed(u) || u.RawPath != "" || !strings.HasPrefix(u.Path, releasePath(tag)) ||
		strings.Contains(u.Path[len(releasePath(tag)):], "/") {
		return nil, fmt.Errorf("update: refusing to download %s from %q", what, brief.StripControl(raw))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream")
	hc := *c.http
	hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return errors.New("too many redirects")
		}
		if !c.allowed(req.URL) {
			return fmt.Errorf("redirected to a disallowed location (%s://%s)", req.URL.Scheme, req.URL.Host)
		}
		return nil
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update: downloading %s: %w", what, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update: downloading %s: %s", what, resp.Status)
	}
	if resp.ContentLength > limit {
		return nil, fmt.Errorf("update: %s is too large (%d bytes)", what, resp.ContentLength)
	}
	return readCapped(resp.Body, limit, what)
}

// readCapped reads r, refusing more than limit bytes.
func readCapped(r io.Reader, limit int64, what string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("update: reading %s: %w", what, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("update: %s is larger than %d bytes", what, limit)
	}
	return data, nil
}

// checksumFor is the SHA-256 that checksums.txt (sha256sum's format: the
// hex digest, white space, then the file name, with a "*" before it in
// binary mode) gives for name.
func checksumFor(data []byte, name string) ([]byte, error) {
	var found []byte
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 2 || strings.TrimPrefix(f[1], "*") != name {
			continue
		}
		sum, err := hex.DecodeString(f[0])
		if err != nil || len(sum) != sha256.Size {
			return nil, fmt.Errorf("update: %s has a malformed line for %s", Checksums, name)
		}
		if found != nil && !bytes.Equal(found, sum) {
			return nil, fmt.Errorf("update: %s lists %s twice", Checksums, name)
		}
		found = sum
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("update: reading %s: %w", Checksums, err)
	}
	if found == nil {
		return nil, fmt.Errorf("update: %s has no checksum for %s; nothing was installed", Checksums, name)
	}
	return found, nil
}

// extract writes the archive's one regular-file entry named exactly
// Binary to w. Other entries are skipped, never written anywhere; an
// archive without that entry, or with it as anything but a regular file,
// twice, under another spelling of its path, or too large, or with any
// absolute or ".." path, is refused.
func extract(data []byte, w io.Writer) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("update: reading the archive: %w", err)
	}
	defer gz.Close()
	limited := &io.LimitedReader{R: gz, N: maxUncompressed}
	tr := tar.NewReader(limited)
	found := false
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if limited.N <= 0 {
				return errors.New("update: the archive unpacks to too much data")
			}
			return fmt.Errorf("update: reading the archive: %w", err)
		}
		name := strings.ReplaceAll(h.Name, `\`, "/")
		if strings.HasPrefix(name, "/") || slices.Contains(strings.Split(name, "/"), "..") {
			return fmt.Errorf("update: the archive has an unsafe path %q; refusing it", brief.StripControl(h.Name))
		}
		if h.Name != Binary {
			if path.Clean(name) == Binary {
				return fmt.Errorf("update: the archive holds %s as %q; refusing it", Binary, brief.StripControl(h.Name))
			}
			continue
		}
		switch {
		case found:
			return fmt.Errorf("update: the archive holds %s twice", Binary)
		case h.Typeflag != tar.TypeReg:
			return fmt.Errorf("update: the archive's %s is not a regular file", Binary)
		case h.Size <= 0 || h.Size > maxBinary:
			return fmt.Errorf("update: the archive's %s has an unexpected size (%d bytes)", Binary, h.Size)
		}
		n, err := io.Copy(w, io.LimitReader(tr, maxBinary+1))
		if err != nil {
			return fmt.Errorf("update: unpacking %s: %w", Binary, err)
		}
		if n != h.Size {
			return fmt.Errorf("update: the archive's %s is truncated", Binary)
		}
		found = true
	}
	if !found {
		return fmt.Errorf("update: the archive has no %s", Binary)
	}
	return nil
}
